package cli

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/This-Is-NPC/backstage/internal/take"
)

func takesCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "takes",
		Short: "Manage recorded takes",
	}
	c.AddCommand(takesPruneCmd())
	return c
}

func takesPruneCmd() *cobra.Command {
	var older, maxSize string
	var dryRun bool
	c := &cobra.Command{
		Use:   "prune",
		Short: "Remove abandoned or old recorded takes",
		RunE: func(c *cobra.Command, _ []string) error {
			p, err := loadProjectFrom(projectFlag)
			if err != nil {
				return err
			}
			opts := take.PruneOptions{}
			if older != "" {
				d, err := take.ParseDuration(older)
				if err != nil {
					return err
				}
				opts.OlderThan = d
			}
			if maxSize != "" {
				n, err := take.ParseSize(maxSize)
				if err != nil {
					return err
				}
				opts.MaxSize = n
			}
			opts.DryRun = dryRun
			opts.Now = time.Now()
			got, err := take.Prune(c.Context(), p, opts)
			if err != nil {
				return err
			}
			out := c.OutOrStdout()
			for _, path := range got.Removed {
				fmt.Fprintf(out, "removed %s\n", path)
			}
			for _, path := range got.Moved {
				fmt.Fprintf(out, "moved %s\n", path)
			}
			for _, path := range got.Skipped {
				fmt.Fprintf(out, "skipped %s\n", path)
			}
			for _, path := range got.Repaired {
				fmt.Fprintf(out, "repaired %s\n", path)
			}
			if len(got.Removed)+len(got.Moved)+len(got.Skipped)+len(got.Repaired) == 0 {
				fmt.Fprintln(out, "nothing to prune")
			}
			return nil
		},
	}
	c.Flags().StringVar(&older, "older-than", "", "remove unreferenced takes older than DURATION (72h, 7d)")
	c.Flags().StringVar(&maxSize, "max-size", "", "shrink .takes until it fits SIZE (500M, 2G)")
	c.Flags().BoolVar(&dryRun, "dry-run", false, "print actions without changing files")
	return c
}
