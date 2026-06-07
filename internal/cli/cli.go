// Package cli wires the backstage verbs (list/play/rehearse/produce/setup/kill)
// onto cobra.
package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

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

// Execute runs the backstage CLI.
func Execute(version string) error {
	root := &cobra.Command{
		Use:     "backstage",
		Short:   "Declarative terminal screencast recorder — Lights, camera... Automation!",
		Version: version,
	}
	root.PersistentFlags().StringVar(&projectFlag, "project", "",
		"project dir (default: search up from cwd)")
	root.AddCommand(listCmd(), playCmd(), rehearseCmd(), produceCmd(), setupCmd(), killCmd())
	return root.Execute()
}

func produceCmd() *cobra.Command {
	var scenesCSV, trans, out string
	var speed float64
	var showStaging, keepSegments bool
	c := &cobra.Command{
		Use:   "produce [PRODUCTION]",
		Short: "Record several scenes with transitions into one video",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
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
				Project: p, Prod: prod, OutPath: out,
				ShowStaging: showStaging, KeepSegments: keepSegments, Speed: speed,
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
		fmt.Fprintf(out, "  %-22s  layout=%-12s steps=%d\n", name, s.LayoutName(), len(s.Steps))
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
	return &cobra.Command{
		Use:   "play SCENE",
		Short: "Stage the scene, record it, and write an mp4",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			return runScene(args[0], engine.Options{Record: true, Speed: 1})
		},
	}
}

func rehearseCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "rehearse SCENE",
		Short: "Dry-run the scene fast, without recording",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			return runScene(args[0], engine.Options{Record: false, Speed: rehearseSpeed})
		},
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
			_ = (&prompter.Hypr{}).Close()
			return (&stage.Hypr{}).Teardown()
		},
	}
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
	return engine.New(p).Run(s, opts)
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
