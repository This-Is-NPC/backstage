// Package cli wires the backstage verbs (list/play/rehearse/produce/setup/kill)
// onto cobra.
package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/This-Is-NPC/backstage/internal/engine"
	"github.com/This-Is-NPC/backstage/internal/production"
	"github.com/This-Is-NPC/backstage/internal/prompter"
	"github.com/This-Is-NPC/backstage/internal/scene"
	"github.com/This-Is-NPC/backstage/internal/stage"
)

// rehearseSpeed compresses delays during a dry-run.
const rehearseSpeed = 0.2

// projectFlag is the persistent --project value (project dir). Empty means search
// up from the current directory.
var projectFlag string

// binVersion is the binary version passed to Execute, written into clip facts.
var binVersion string

// Execute runs the backstage CLI.
func Execute(version string) error {
	binVersion = version
	root := &cobra.Command{
		Use:     "backstage",
		Short:   "Declarative terminal screencast recorder — Lights, camera... Automation!",
		Version: version,
	}
	root.PersistentFlags().StringVar(&projectFlag, "project", "",
		"project dir (default: search up from cwd)")
	root.AddCommand(listCmd(), playCmd(), rehearseCmd(), produceCmd(), setupCmd(), killCmd(), stageCmd(), renderCmd(), previewCmd(), templateCmd(), configCmd(), takesCmd(), statusCmd())
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	return root.ExecuteContext(ctx)
}

func produceCmd() *cobra.Command {
	var scenesCSV, trans, out string
	var speed float64
	var showStaging, keepSegments bool
	c := &cobra.Command{
		Use:   "produce [PRODUCTION]",
		Short: "Record several scenes with transitions into one video",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			p, err := loadProjectFrom(projectFlag)
			if err != nil {
				return err
			}
			var prod scene.Production
			switch {
			case len(args) == 1:
				if prod, err = p.Production(args[0]); err != nil {
					return err
				}
			case scenesCSV != "":
				prod = production.AdHoc(splitCSV(scenesCSV), trans)
			default:
				return fmt.Errorf("produce needs a PRODUCTION name or --scenes a,b,c")
			}
			outPath, err := production.Run(production.Options{
				Context: c.Context(),
				Project: p, Prod: prod, OutPath: out,
				ShowStaging: showStaging, KeepSegments: keepSegments, Speed: speed,
				Version: binVersion,
			})
			if err != nil {
				return err
			}
			fmt.Printf(">> done. %s  (stage open — backstage kill)\n", outPath)
			return nil
		},
	}
	c.Flags().StringVar(&scenesCSV, "scenes", "", "comma-separated scene names (ad-hoc production)")
	c.Flags().StringVar(&trans, "transition", "", "transition inserted between ad-hoc scenes")
	c.Flags().StringVar(&out, "out", "", "output file (default <project>/<out>/production.mp4)")
	c.Flags().BoolVar(&showStaging, "show-staging", false, "include the stage montage in the video")
	c.Flags().BoolVar(&keepSegments, "keep-segments", false, "keep intermediate clips")
	c.Flags().Float64Var(&speed, "speed", 1, "scene timing multiplier (1 = real time, smaller = faster)")
	return c
}

// splitCSV splits a comma list, trimming spaces and dropping empties.
func splitCSV(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func listCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List the scenes and productions in a project",
		RunE: func(_ *cobra.Command, _ []string) error {
			p, err := loadProjectFrom(projectFlag)
			if err != nil {
				return err
			}
			return listProject(os.Stdout, p)
		},
	}
}

// listProject prints a project's scenes (name, layout, step count) and declared
// productions. Invalid scene files are flagged, not fatal.
func listProject(out io.Writer, p *scene.Project) error {
	scenesDir := filepath.Join(p.Dir, "scenes")
	files, err := filepath.Glob(filepath.Join(scenesDir, "*.json"))
	if err != nil {
		return err
	}
	sort.Strings(files)
	fmt.Fprintf(out, "Scenes (%s):\n", scenesDir)
	if len(files) == 0 {
		fmt.Fprintln(out, "  (none)")
	}
	for _, f := range files {
		name := strings.TrimSuffix(filepath.Base(f), ".json")
		s, err := scene.LoadScene(f)
		if err != nil {
			fmt.Fprintf(out, "  %-22s  (invalid: %v)\n", name, err)
			continue
		}
		if err := s.Validate(p); err != nil {
			fmt.Fprintf(out, "  %-22s  (invalid: %v)\n", name, err)
			continue
		}
		if s.Type == "visual" {
			fmt.Fprintf(out, "  %-22s  type=visual duration=%.2fs\n", name, s.Duration)
		} else {
			fmt.Fprintf(out, "  %-22s  layout=%-12s steps=%d\n", name, s.LayoutName(), len(s.Steps))
		}
	}

	if len(p.Presentations) > 0 {
		names := make([]string, 0, len(p.Presentations))
		for name := range p.Presentations {
			names = append(names, name)
		}
		sort.Strings(names)
		fmt.Fprintln(out, "\nPresentations:")
		for _, name := range names {
			fmt.Fprintf(out, "  %-22s  file=%s\n", name, p.Presentations[name].File)
		}
	}
	if len(p.Productions) > 0 {
		names := make([]string, 0, len(p.Productions))
		for n := range p.Productions {
			names = append(names, n)
		}
		sort.Strings(names)
		fmt.Fprintln(out, "\nProductions:")
		for _, n := range names {
			prod := p.Productions[n]
			fmt.Fprintf(out, "  %-22s  scenes=%d transitions=%d\n", n, len(prod.Scenes), len(prod.Transitions))
		}
	}
	return nil
}

func playCmd() *cobra.Command {
	var adopt, withDeps, jsonOut, adoptConfirmed, stale bool
	var jobs, reservedFD, progressFD int
	var reservedStage string
	c := &cobra.Command{
		Use:   "play [SCENE | --stale [DIR]]",
		Short: "Stage the scene, record it, and write an mp4",
		Args:  playRehearseArgs,
		RunE: func(c *cobra.Command, args []string) error {
			opts := engine.Options{Context: c.Context(), Record: true, Speed: 1, Adopt: adopt}
			if err := applyInternalChild(&opts, reservedStage, reservedFD, progressFD, adoptConfirmed); err != nil {
				return err
			}
			if err := requireSchedulerFlags(c, withDeps); err != nil {
				return err
			}
			if adopt && !adoptConfirmed {
				opts.ConfirmAdopt = func(snapshot string) error {
					return confirmAdoptNames(c.Context(), c.InOrStdin(), c.ErrOrStderr(), snapshot)
				}
			}
			d, err := newDepsExec(c.OutOrStdout(), nil, nil)
			if err != nil {
				return err
			}
			d.Jobs = jobs
			d.JSON = jsonOut
			if stale {
				if withDeps {
					return fmt.Errorf("--stale cannot be used with --with-deps")
				}
				dir, err := staleWorkspace(args)
				if err != nil {
					return err
				}
				return d.runStale(dir, opts)
			}
			if withDeps {
				return d.run(args[0], opts)
			}
			return runScene(args[0], opts)
		},
	}
	c.Flags().BoolVar(&adopt, "adopt", false, "replace a snapshot that has no origin after typing its name")
	c.Flags().BoolVar(&withDeps, "with-deps", false, "record stale or missing producers first")
	c.Flags().BoolVar(&stale, "stale", false, "record every stale or missing scene; optional DIR argument defaults to .")
	c.Flags().IntVar(&jobs, "jobs", 0, "limit concurrent takes (0 = host budget only)")
	c.Flags().BoolVar(&jsonOut, "json", false, "emit job.progress lines and a final JSON report")
	c.Flags().StringVar(&reservedStage, "internal-reserved-stage", "", "")
	c.Flags().IntVar(&reservedFD, "internal-reserved-fd", -1, "")
	c.Flags().IntVar(&progressFD, "internal-progress-fd", -1, "")
	c.Flags().BoolVar(&adoptConfirmed, "internal-adopt-confirmed", false, "")
	_ = c.Flags().MarkHidden("internal-reserved-stage")
	_ = c.Flags().MarkHidden("internal-reserved-fd")
	_ = c.Flags().MarkHidden("internal-progress-fd")
	_ = c.Flags().MarkHidden("internal-adopt-confirmed")
	return c
}

func rehearseCmd() *cobra.Command {
	var replaceState, withDeps, jsonOut, adoptConfirmed, stale bool
	var jobs, reservedFD, progressFD int
	var reservedStage string
	c := &cobra.Command{
		Use:   "rehearse [SCENE | --stale [DIR]]",
		Short: "Dry-run the scene fast, without recording",
		Args:  playRehearseArgs,
		RunE: func(c *cobra.Command, args []string) error {
			opts := engine.Options{Context: c.Context(), Record: false, Speed: rehearseSpeed, ReplaceState: replaceState}
			if err := applyInternalChild(&opts, reservedStage, reservedFD, progressFD, adoptConfirmed); err != nil {
				return err
			}
			if err := requireSchedulerFlags(c, withDeps); err != nil {
				return err
			}
			d, err := newDepsExec(c.OutOrStdout(), nil, nil)
			if err != nil {
				return err
			}
			d.Jobs = jobs
			d.JSON = jsonOut
			if stale {
				if withDeps {
					return fmt.Errorf("--stale cannot be used with --with-deps")
				}
				dir, err := staleWorkspace(args)
				if err != nil {
					return err
				}
				return d.runStale(dir, opts)
			}
			if withDeps {
				return d.run(args[0], opts)
			}
			return runScene(args[0], opts)
		},
	}
	c.Flags().BoolVar(&replaceState, "replace-state", false, "let a rehearsal replace a snapshot a recording made")
	c.Flags().BoolVar(&withDeps, "with-deps", false, "rehearse stale or missing producers first")
	c.Flags().BoolVar(&stale, "stale", false, "rehearse every stale or missing scene; optional DIR argument defaults to .")
	c.Flags().IntVar(&jobs, "jobs", 0, "limit concurrent takes (0 = host budget only)")
	c.Flags().BoolVar(&jsonOut, "json", false, "emit job.progress lines and a final JSON report")
	c.Flags().StringVar(&reservedStage, "internal-reserved-stage", "", "")
	c.Flags().IntVar(&reservedFD, "internal-reserved-fd", -1, "")
	c.Flags().IntVar(&progressFD, "internal-progress-fd", -1, "")
	c.Flags().BoolVar(&adoptConfirmed, "internal-adopt-confirmed", false, "")
	_ = c.Flags().MarkHidden("internal-reserved-stage")
	_ = c.Flags().MarkHidden("internal-reserved-fd")
	_ = c.Flags().MarkHidden("internal-progress-fd")
	_ = c.Flags().MarkHidden("internal-adopt-confirmed")
	return c
}

func requireSchedulerFlags(cmd *cobra.Command, withDeps bool) error {
	if !cmd.Flags().Changed("jobs") && !cmd.Flags().Changed("json") {
		return nil
	}
	if withDeps || cmd.Flags().Changed("stale") {
		return nil
	}
	return fmt.Errorf("--jobs and --json require --with-deps or --stale")
}

func playRehearseArgs(cmd *cobra.Command, args []string) error {
	stale, err := cmd.Flags().GetBool("stale")
	if err != nil {
		return err
	}
	if stale {
		if cmd.Flags().Changed("with-deps") {
			return fmt.Errorf("--stale cannot be used with --with-deps")
		}
		return cobra.MaximumNArgs(1)(cmd, args)
	}
	return cobra.ExactArgs(1)(cmd, args)
}

func staleWorkspace(args []string) (string, error) {
	dir := "."
	if len(args) == 1 {
		dir = args[0]
	}
	info, err := os.Stat(dir)
	if err != nil {
		return "", fmt.Errorf("--stale: %w", err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("--stale needs a workspace directory, not a file: %s", dir)
	}
	return dir, nil
}

func confirmAdoptNames(ctx context.Context, in io.Reader, errOut io.Writer, snapshot string) error {
	names := strings.Fields(snapshot)
	if len(names) <= 1 {
		return confirmSnapshotName(ctx, in, errOut, snapshot)
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return interrupted(err)
	}
	fmt.Fprintf(errOut, "Replace snapshots %s? Type their names: ", strings.Join(names, ", "))
	type readResult struct {
		line string
		err  error
	}
	ch := make(chan readResult, 1)
	go func() {
		line, err := bufio.NewReader(in).ReadString('\n')
		ch <- readResult{line: line, err: err}
	}()
	select {
	case <-ctx.Done():
		return interrupted(ctx.Err())
	case got := <-ch:
		if got.err != nil {
			return got.err
		}
		gotNames := strings.Fields(got.line)
		if !sameNameSet(gotNames, names) {
			return errors.New("adoption cancelled")
		}
		return nil
	}
}

func sameNameSet(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	need := map[string]int{}
	for _, n := range want {
		need[n]++
	}
	for _, n := range got {
		need[n]--
		if need[n] < 0 {
			return false
		}
	}
	for _, n := range need {
		if n != 0 {
			return false
		}
	}
	return true
}

func confirmSnapshotName(ctx context.Context, in io.Reader, errOut io.Writer, snapshot string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return interrupted(err)
	}
	fmt.Fprintf(errOut, "Replace snapshot %s? Type its name: ", snapshot)
	type readResult struct {
		line string
		err  error
	}
	ch := make(chan readResult, 1)
	go func() {
		line, err := bufio.NewReader(in).ReadString('\n')
		ch <- readResult{line: line, err: err}
	}()
	select {
	case <-ctx.Done():
		return interrupted(ctx.Err())
	case got := <-ch:
		if got.err != nil {
			return got.err
		}
		if strings.TrimSpace(got.line) != snapshot {
			return errors.New("adoption cancelled")
		}
		return nil
	}
}

func setupCmd() *cobra.Command {
	var stageName string
	c := &cobra.Command{
		Use:   "setup --stage LAYOUT",
		Short: "Stage a layout only (no recording), for debugging",
		RunE: func(_ *cobra.Command, _ []string) error {
			if stageName == "" {
				return fmt.Errorf("setup needs --stage LAYOUT")
			}
			p, err := loadProjectFrom(projectFlag)
			if err != nil {
				return err
			}
			layout, ok := p.Layouts[stageName]
			if !ok {
				return fmt.Errorf("layout %q not in config", stageName)
			}
			if _, err := (&stage.Hypr{}).Setup(layout, p); err != nil {
				return err
			}
			fmt.Printf(">> staged %q (stage open — backstage kill)\n", stageName)
			return nil
		},
	}
	c.Flags().StringVar(&stageName, "stage", "", "layout name to stage")
	return c
}

func killCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "kill",
		Short: "Tear down the stage and dismiss any popup",
		RunE: func(_ *cobra.Command, _ []string) error {
			// Resolve the configured popup class tolerantly: even if the rest of
			// the config no longer validates, a custom-class popup must still be
			// dismissible. Fall back to the default class only if no config is found.
			class := prompter.DefaultClass
			if cfgPath, err := findConfigFrom(projectFlag); err == nil {
				class = scene.PopupClassFor(cfgPath)
			}
			_ = (&prompter.Hypr{}).CloseClass(class)
			return (&stage.Hypr{}).Teardown()
		},
	}
}

// findConfigFrom locates the project config from an explicit dir or by searching
// up from the current directory, without loading/validating it.
func findConfigFrom(dir string) (string, error) {
	if dir == "" {
		var err error
		if dir, err = os.Getwd(); err != nil {
			return "", err
		}
	}
	cfgPath, _, err := scene.FindConfig(filepath.Join(dir, "_"))
	return cfgPath, err
}

// runScene loads the scene + its project, validates, and runs it.
func runScene(scenePath string, opts engine.Options) error {
	cfgPath, _, err := scene.FindConfig(scenePath)
	if err != nil {
		return err
	}
	p, err := scene.LoadProject(cfgPath)
	if err != nil {
		return err
	}
	s, err := scene.LoadScene(scenePath)
	if err != nil {
		return err
	}
	if err := s.Validate(p); err != nil {
		return err
	}
	eng, err := engine.NewForScene(p, s)
	if err != nil {
		return err
	}
	if opts.Version == "" {
		opts.Version = binVersion
	}
	return eng.Run(s, opts)
}

// loadProjectFrom resolves a project config from an explicit dir or by searching
// up from the current directory.
func loadProjectFrom(dir string) (*scene.Project, error) {
	if dir == "" {
		var err error
		if dir, err = os.Getwd(); err != nil {
			return nil, err
		}
	}
	cfgPath, _, err := scene.FindConfig(filepath.Join(dir, "_"))
	if err != nil {
		return nil, err
	}
	return scene.LoadProject(cfgPath)
}
