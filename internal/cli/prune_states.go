package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/This-Is-NPC/backstage/internal/machine"
	"github.com/This-Is-NPC/backstage/internal/workspace"
)

var errWorkspacePrune = errors.New("workspace has configuration or scene errors")

const missingProjectHint = "use --include-missing-projects to remove snapshots from projects that are no longer on disk"

type pruneDelete func(r *machine.Record, name string) (string, error)

type pruneStatesExec struct {
	Store       *machine.Store
	Delete      pruneDelete
	Lock        func(names ...string) (func(), error)
	WaitCatalog func(ctx context.Context) (func(), error)
	Out         io.Writer
	Err         io.Writer
}

func stagePruneStatesCmd() *cobra.Command {
	return pruneStatesCmd(pruneStatesExec{})
}

func pruneStatesCmd(x pruneStatesExec) *cobra.Command {
	var workspaceDir string
	var dryRun, asJSON, includeMissing bool
	c := &cobra.Command{
		Use:           "prune-states STAGE",
		Short:         "Remove produced snapshots no scene in the workspace still declares",
		SilenceUsage:  true,
		SilenceErrors: true,
		Args: func(c *cobra.Command, args []string) error {
			if err := cobra.ExactArgs(1)(c, args); err != nil {
				fmt.Fprintln(c.ErrOrStderr(), err)
				if asJSON {
					stage := ""
					if len(args) == 1 {
						stage = args[0]
					}
					_ = writeErrorJSON(c.OutOrStdout(), stage, err)
				}
				return err
			}
			return nil
		},
		RunE: func(c *cobra.Command, args []string) error {
			if x.Out == nil {
				x.Out = c.OutOrStdout()
			}
			if x.Err == nil {
				x.Err = c.ErrOrStderr()
			}
			if x.Store == nil {
				store, err := machine.DefaultStore()
				if err != nil {
					return x.failDoc(args[0], asJSON, err)
				}
				x.Store = store
			}
			if x.Lock == nil {
				x.Lock = x.Store.LockMany
			}
			if x.WaitCatalog == nil {
				x.WaitCatalog = func(ctx context.Context) (func(), error) {
					return x.Store.LockWait(ctx, "image-catalog")
				}
			}
			if x.Delete == nil {
				m, err := machine.New()
				if err != nil {
					return x.failDoc(args[0], asJSON, err)
				}
				m.Store = x.Store
				x.Delete = m.DeleteSnapshot
			}
			return x.run(c.Context(), args[0], workspaceDir, dryRun, asJSON, includeMissing)
		},
	}
	c.Flags().StringVar(&workspaceDir, "workspace", "", "workspace directory (same discovery as status)")
	c.Flags().BoolVar(&dryRun, "dry-run", false, "list removals without taking locks or changing files")
	c.Flags().BoolVar(&asJSON, "json", false, "machine-readable prune report")
	c.Flags().BoolVar(&includeMissing, "include-missing-projects", false, "remove snapshots whose origin project is gone from disk")
	c.SetFlagErrorFunc(func(c *cobra.Command, err error) error {
		fmt.Fprintln(c.ErrOrStderr(), err)
		return err
	})
	return c
}

type pruneFailure struct {
	Snapshot string `json:"snapshot"`
	Error    string `json:"error"`
}

type pruneReport struct {
	Stage           string                    `json:"stage"`
	DryRun          bool                      `json:"dry-run"`
	Removed         []workspace.PruneDecision `json:"removed"`
	Kept            []workspace.PruneDecision `json:"kept"`
	CleanupWarnings []string                  `json:"cleanup-warnings"`
	Failed          *pruneFailure             `json:"failed,omitempty"`
	NotAttempted    []string                  `json:"not-attempted,omitempty"`
}

type pruneErrorDoc struct {
	Stage string `json:"stage"`
	Error string `json:"error"`
}

func (x pruneStatesExec) fail(err error) error {
	if err == nil {
		return nil
	}
	fmt.Fprintln(x.Err, err)
	return err
}

func (x pruneStatesExec) failDoc(stage string, asJSON bool, err error) error {
	if err == nil {
		return nil
	}
	fmt.Fprintln(x.Err, err)
	if asJSON {
		if werr := writeErrorJSON(x.Out, stage, err); werr != nil {
			fmt.Fprintln(x.Err, werr)
		}
	}
	return err
}

func (x pruneStatesExec) run(ctx context.Context, stage, dir string, dryRun, asJSON, includeMissing bool) error {
	if x.WaitCatalog == nil {
		x.WaitCatalog = func(ctx context.Context) (func(), error) {
			return x.Store.LockWait(ctx, "image-catalog")
		}
	}
	if dir == "" {
		return x.failDoc(stage, asJSON, errors.New("prune-states requires --workspace"))
	}
	if err := machine.ValidateName(stage); err != nil {
		return x.failDoc(stage, asJSON, err)
	}
	opts := workspace.Options{Dir: dir, Store: x.Store}
	ev, err := x.evaluate(stage, opts, asJSON)
	if err != nil {
		return err
	}

	if dryRun {
		rec, err := x.Store.Load(stage)
		if err != nil {
			return x.failDoc(stage, asJSON, err)
		}
		return x.finishReport(writePruneReport(x.Out, asJSON, pruneReportFrom(stage, true, ev.PlanPrune(stage, rec, workspace.PruneOptions{IncludeMissingProjects: includeMissing}), nil, nil, nil)))
	}

	release, err := x.Lock(stage)
	if err != nil {
		return x.failDoc(stage, asJSON, err)
	}
	defer release()
	ev, err = x.evaluate(stage, opts, asJSON)
	if err != nil {
		return err
	}
	rec, err := x.Store.Load(stage)
	if err != nil {
		return x.failDoc(stage, asJSON, err)
	}
	plan := ev.PlanPrune(stage, rec, workspace.PruneOptions{IncludeMissingProjects: includeMissing})
	var warnings []string
	for i, d := range plan.Remove {
		cat, err := x.WaitCatalog(ctx)
		if err != nil {
			report := pruneReportFrom(stage, false, workspace.PrunePlan{Remove: removedSoFar(plan, d), Keep: plan.Keep}, warnings, &pruneFailure{Snapshot: d.Snapshot, Error: err.Error()}, notAttempted(plan.Remove, i+1))
			_ = writePruneReport(x.Out, asJSON, report)
			return x.fail(err)
		}
		warn, err := x.Delete(rec, d.Snapshot)
		cat()
		if err != nil {
			report := pruneReportFrom(stage, false, workspace.PrunePlan{Remove: removedSoFar(plan, d), Keep: plan.Keep}, warnings, &pruneFailure{Snapshot: d.Snapshot, Error: err.Error()}, notAttempted(plan.Remove, i+1))
			_ = writePruneReport(x.Out, asJSON, report)
			return x.fail(err)
		}
		if warn != "" {
			warnings = append(warnings, warn)
		}
	}
	return x.finishReport(writePruneReport(x.Out, asJSON, pruneReportFrom(stage, false, plan, warnings, nil, nil)))
}

func (x pruneStatesExec) finishReport(err error) error {
	if err == nil {
		return nil
	}
	return x.fail(err)
}

func (x pruneStatesExec) evaluate(stage string, opts workspace.Options, asJSON bool) (*workspace.Evaluation, error) {
	ev, err := workspace.Evaluate(opts)
	if err != nil {
		var conflict *workspace.ConflictError
		if errors.As(err, &conflict) {
			if asJSON {
				if werr := writeJSONTo(x.Out, conflict); werr != nil {
					return nil, x.fail(werr)
				}
				return nil, err
			}
			if _, werr := fmt.Fprintln(x.Out, conflict.Error()); werr != nil {
				return nil, x.fail(werr)
			}
			return nil, err
		}
		return nil, x.failDoc(stage, asJSON, err)
	}
	res, err := ev.Result()
	if err != nil {
		return nil, x.failDoc(stage, asJSON, err)
	}
	if res.HasError() || res.HasWarning() {
		abort := &workspace.Result{Errors: res.Errors, Warnings: res.Warnings}
		if asJSON {
			if werr := workspace.WriteJSON(x.Out, abort); werr != nil {
				return nil, x.fail(werr)
			}
		} else if werr := workspace.WriteText(x.Out, abort); werr != nil {
			return nil, x.fail(werr)
		}
		return nil, errWorkspacePrune
	}
	return ev, nil
}

func removedSoFar(plan workspace.PrunePlan, failed workspace.PruneDecision) []workspace.PruneDecision {
	var out []workspace.PruneDecision
	for _, d := range plan.Remove {
		if d.Snapshot == failed.Snapshot {
			break
		}
		out = append(out, d)
	}
	if out == nil {
		return []workspace.PruneDecision{}
	}
	return out
}

func notAttempted(remove []workspace.PruneDecision, from int) []string {
	var out []string
	for _, d := range remove[from:] {
		out = append(out, d.Snapshot)
	}
	if out == nil {
		return []string{}
	}
	return out
}

func pruneReportFrom(stage string, dryRun bool, plan workspace.PrunePlan, warnings []string, failed *pruneFailure, skipped []string) pruneReport {
	if plan.Remove == nil {
		plan.Remove = []workspace.PruneDecision{}
	}
	if plan.Keep == nil {
		plan.Keep = []workspace.PruneDecision{}
	}
	if warnings == nil {
		warnings = []string{}
	}
	return pruneReport{Stage: stage, DryRun: dryRun, Removed: plan.Remove, Kept: plan.Keep, CleanupWarnings: warnings, Failed: failed, NotAttempted: skipped}
}

func writePruneReport(w io.Writer, asJSON bool, report pruneReport) error {
	if asJSON {
		return writeJSONTo(w, report)
	}
	verb := "removed"
	if report.DryRun {
		verb = "would remove"
	}
	for _, d := range report.Removed {
		if _, err := fmt.Fprintf(w, "%s %s (%s)\n", verb, d.Snapshot, d.Reason); err != nil {
			return err
		}
	}
	if report.Failed != nil {
		if _, err := fmt.Fprintf(w, "failed %s: %s\n", report.Failed.Snapshot, report.Failed.Error); err != nil {
			return err
		}
	}
	for _, name := range report.NotAttempted {
		if _, err := fmt.Fprintf(w, "not attempted %s\n", name); err != nil {
			return err
		}
	}
	missing := false
	for _, d := range report.Kept {
		if d.Reason == workspace.PruneMissingProject {
			missing = true
		}
		if _, err := fmt.Fprintf(w, "kept %s (%s)\n", d.Snapshot, d.Reason); err != nil {
			return err
		}
	}
	for _, warn := range report.CleanupWarnings {
		if _, err := fmt.Fprintf(w, "warning: %s\n", warn); err != nil {
			return err
		}
	}
	if missing {
		if _, err := fmt.Fprintln(w, missingProjectHint); err != nil {
			return err
		}
	}
	if len(report.Removed) == 0 && len(report.Kept) == 0 && len(report.CleanupWarnings) == 0 && report.Failed == nil && len(report.NotAttempted) == 0 {
		_, err := fmt.Fprintln(w, "nothing to prune")
		return err
	}
	return nil
}

func writeJSONTo(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func writeErrorJSON(w io.Writer, stage string, err error) error {
	return writeJSONTo(w, pruneErrorDoc{Stage: stage, Error: err.Error()})
}
