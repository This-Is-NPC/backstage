package cli

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/This-Is-NPC/backstage/internal/presentation"
	"github.com/This-Is-NPC/backstage/internal/take"
)

func cacheCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "cache",
		Short: "Manage the presentation render cache",
	}
	c.AddCommand(cachePruneCmd())
	return c
}

func cachePruneCmd() *cobra.Command {
	var maxSize string
	var dryRun, asJSON bool
	c := &cobra.Command{
		Use:   "prune",
		Short: "Remove unused presentation track and audio cache entries",
		RunE: func(c *cobra.Command, _ []string) error {
			root, err := presentation.DefaultRenderCacheRoot()
			if err != nil {
				return err
			}
			opts := presentation.CachePruneOptions{DryRun: dryRun}
			if maxSize != "" {
				n, err := take.ParseSize(maxSize)
				if err != nil {
					return err
				}
				opts.MaxSize = n
			}
			rep, err := presentation.PruneRenderCache(c.Context(), root, opts)
			if err != nil {
				return err
			}
			if asJSON {
				enc := json.NewEncoder(c.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(rep)
			}
			verb := "removed"
			if dryRun {
				verb = "would remove"
			}
			for _, p := range rep.Orphans {
				fmt.Fprintf(c.OutOrStdout(), "%s orphan %s\n", verb, p)
			}
			for _, p := range rep.Removed {
				fmt.Fprintf(c.OutOrStdout(), "%s %s\n", verb, p)
			}
			for _, p := range rep.Skipped {
				fmt.Fprintf(c.OutOrStdout(), "skipped %s\n", p)
			}
			if rep.MemoPruned > 0 {
				fmt.Fprintf(c.OutOrStdout(), "%s %d memo entries\n", verb, rep.MemoPruned)
			}
			if len(rep.Removed)+len(rep.Orphans)+rep.MemoPruned == 0 {
				fmt.Fprintln(c.OutOrStdout(), "nothing to prune")
			}
			return nil
		},
	}
	c.Flags().StringVar(&maxSize, "max-size", "10G", "keep the render cache under SIZE (10G, 500M)")
	c.Flags().BoolVar(&dryRun, "dry-run", false, "print actions without changing files")
	c.Flags().BoolVar(&asJSON, "json", false, "write a JSON report")
	return c
}
