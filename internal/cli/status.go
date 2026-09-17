package cli

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/This-Is-NPC/backstage/internal/machine"
	"github.com/This-Is-NPC/backstage/internal/workspace"
)

var errSceneStatus = errors.New("one or more scenes are in error")

func statusCmd() *cobra.Command {
	var asJSON bool
	c := &cobra.Command{
		Use:           "status [DIR]",
		Short:         "Report which recording takes are stale, blocked or missing",
		Args:          cobra.MaximumNArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(c *cobra.Command, args []string) error {
			dir := ""
			if len(args) == 1 {
				dir = args[0]
			} else if projectFlag != "" {
				dir = projectFlag
			}
			store, err := machine.DefaultStore()
			if err != nil {
				return err
			}
			res, err := workspace.Report(workspace.Options{Dir: dir, Store: store})
			if err != nil {
				if werr := writeStatusFailure(c, asJSON, err); werr != nil {
					return werr
				}
				return err
			}
			if asJSON {
				if werr := workspace.WriteJSON(c.OutOrStdout(), res); werr != nil {
					return werr
				}
			} else if werr := workspace.WriteText(c.OutOrStdout(), res); werr != nil {
				return werr
			}
			if res.HasError() {
				return errSceneStatus
			}
			return nil
		},
	}
	c.Flags().BoolVar(&asJSON, "json", false, "machine-readable scene status")
	return c
}

func writeStatusFailure(c *cobra.Command, asJSON bool, err error) error {
	var conflict *workspace.ConflictError
	if errors.As(err, &conflict) {
		if asJSON {
			return writeJSON(c, conflict)
		}
		_, werr := fmt.Fprintln(c.OutOrStdout(), conflict.Error())
		return werr
	}
	fmt.Fprintln(c.ErrOrStderr(), err)
	return nil
}
