package cli

import (
	"fmt"
	"github.com/This-Is-NPC/backstage/internal/presentation"
	"github.com/spf13/cobra"
)

func renderCmd() *cobra.Command {
	var check bool
	var out string
	c := &cobra.Command{Use: "render PRESENTATION", Short: "Compose existing media into an MP4", Args: cobra.ExactArgs(1), RunE: func(c *cobra.Command, args []string) error {
		p, err := loadProjectFrom(projectFlag)
		if err != nil {
			return err
		}
		plan, err := presentation.Load(p, args[0])
		if err != nil {
			return err
		}
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
	return c
}
func previewCmd() *cobra.Command {
	return &cobra.Command{Use: "preview PRESENTATION", Short: "Render and preview a presentation with playback controls", Args: cobra.ExactArgs(1), RunE: func(c *cobra.Command, args []string) error {
		p, err := loadProjectFrom(projectFlag)
		if err != nil {
			return err
		}
		plan, err := presentation.Load(p, args[0])
		if err != nil {
			return err
		}
		return presentation.Preview(c.Context(), plan, c.OutOrStdout())
	}}
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
