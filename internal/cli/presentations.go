package cli

import (
	"fmt"
	"github.com/This-Is-NPC/backstage/internal/presentation"
	"github.com/spf13/cobra"
)

func loadPresentation(name, aspect string) (*presentation.Plan, error) {
	p, err := loadProjectFrom(projectFlag)
	if err != nil {
		return nil, err
	}
	plan, err := presentation.Load(p, name)
	if err != nil {
		return nil, err
	}
	if err := plan.Reframe(aspect); err != nil {
		_ = plan.Close()
		return nil, err
	}
	return plan, nil
}

func lintCmd() *cobra.Command {
	var aspect string
	c := &cobra.Command{Use: "lint PRESENTATION", Short: "Check presentation typography and reading time", Args: cobra.ExactArgs(1), RunE: func(c *cobra.Command, args []string) error {
		plan, err := loadPresentation(args[0], aspect)
		if err != nil {
			return err
		}
		defer plan.Close()
		if err := presentation.Lint(c.Context(), plan); err != nil {
			return err
		}
		fmt.Fprintln(c.OutOrStdout(), "Presentation meets the visual rubric")
		return nil
	}}
	aspectFlag(c, &aspect)
	return c
}

func renderCmd() *cobra.Command {
	var check bool
	var out, aspect string
	c := &cobra.Command{Use: "render PRESENTATION", Short: "Compose existing media into an MP4", Args: cobra.ExactArgs(1), RunE: func(c *cobra.Command, args []string) error {
		plan, err := loadPresentation(args[0], aspect)
		if err != nil {
			return err
		}
		defer plan.Close()
		output, err := plan.Output(out)
		if err != nil {
			return err
		}
		if check {
			if err = presentation.Check(c.Context(), plan); err != nil {
				return err
			}
			fmt.Fprintln(c.OutOrStdout(), "Presentation is valid")
			return nil
		}
		if err = presentation.Render(c.Context(), plan, output, c.OutOrStdout()); err != nil {
			return err
		}
		fmt.Fprintln(c.OutOrStdout(), output)
		return nil
	}}
	c.Flags().BoolVar(&check, "check", false, "Validate media, timing and templates without exporting")
	c.Flags().StringVar(&out, "out", "", "Output MP4 relative to the project")
	aspectFlag(c, &aspect)
	return c
}
func previewCmd() *cobra.Command {
	var from, to, scale float64
	var aspect string
	c := &cobra.Command{Use: "preview PRESENTATION", Short: "Render and preview a presentation with playback controls", Args: cobra.ExactArgs(1), RunE: func(c *cobra.Command, args []string) error {
		plan, err := loadPresentation(args[0], aspect)
		if err != nil {
			return err
		}
		defer plan.Close()
		opts := presentation.PreviewOpts{From: from, To: to, Scale: scale}
		if !c.Flags().Changed("to") {
			opts.To = plan.Document.Duration
		}
		return presentation.Preview(c.Context(), plan, c.OutOrStdout(), opts)
	}}
	c.Flags().Float64Var(&from, "from", 0, "Start of the preview interval in presentation seconds")
	c.Flags().Float64Var(&to, "to", 0, "End of the preview interval in presentation seconds")
	c.Flags().Float64Var(&scale, "scale", 1, "Draft screenshot scale in (0, 1]")
	aspectFlag(c, &aspect)
	return c
}

func aspectFlag(c *cobra.Command, value *string) {
	c.Flags().StringVar(value, "aspect", "", "Output aspect ratio: 16:9, 9:16 or 1:1")
}

func exportRemotionCmd() *cobra.Command {
	var out, aspect string
	c := &cobra.Command{Use: "export-remotion PRESENTATION", Short: "Export media and resolved timing to an editable Remotion project", Args: cobra.ExactArgs(1), RunE: func(c *cobra.Command, args []string) error {
		plan, err := loadPresentation(args[0], aspect)
		if err != nil {
			return err
		}
		defer plan.Close()
		path, err := presentation.ExportRemotion(c.Context(), plan, out, c.OutOrStdout())
		if err != nil {
			return err
		}
		fmt.Fprintln(c.OutOrStdout(), path)
		return nil
	}}
	c.Flags().StringVar(&out, "out", "", "New export directory relative to the project")
	aspectFlag(c, &aspect)
	return c
}
func templateCmd() *cobra.Command {
	c := &cobra.Command{Use: "template", Short: "Create editable presentation templates"}
	c.AddCommand(&cobra.Command{Use: "init NAME", Args: cobra.ExactArgs(1), RunE: func(_ *cobra.Command, args []string) error {
		p, err := loadProjectFrom(projectFlag)
		if err != nil {
			return err
		}
		return presentation.InitTemplate(p.Dir, args[0])
	}})
	return c
}
