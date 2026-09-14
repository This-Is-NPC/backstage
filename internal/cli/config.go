package cli

import (
	"github.com/This-Is-NPC/backstage/internal/scene"
	"github.com/spf13/cobra"
)

func configCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "config",
		Short: "Inspect project configuration",
	}
	c.AddCommand(&cobra.Command{
		Use:   "show",
		Short: "Print the merged configuration and the file each key came from",
		RunE: func(c *cobra.Command, _ []string) error {
			p, err := loadProjectFrom(projectFlag)
			if err != nil {
				return err
			}
			return scene.WriteConfigShow(c.OutOrStdout(), p)
		},
	})
	return c
}
