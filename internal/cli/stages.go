package cli

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/This-Is-NPC/backstage/internal/machine"
	"github.com/spf13/cobra"
)

func stageCmd() *cobra.Command {
	c := &cobra.Command{Use: "stage", Short: "Create and manage shared Omarchy recording VMs"}
	c.AddCommand(stageDoctorCmd(), stageCreateCmd(), stageListCmd(), stageInspectCmd(), stageCloneCmd())
	for _, verb := range []string{"start", "stop", "ssh", "credentials", "snapshot", "snapshots", "restore", "delete"} {
		c.AddCommand(stageOperationCmd(verb))
	}
	return c
}

func writeJSON(c *cobra.Command, v any) error {
	enc := json.NewEncoder(c.OutOrStdout())
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func stageDoctorCmd() *cobra.Command {
	var asJSON bool
	c := &cobra.Command{Use: "doctor", Short: "Check host virtualization and provisioning dependencies", Args: cobra.NoArgs, RunE: func(c *cobra.Command, _ []string) error {
		m, err := machine.New()
		if err != nil {
			return err
		}
		checks := m.Doctor(c.Context())
		good := true
		for _, check := range checks {
			if !check.OK {
				good = false
			}
			if !asJSON {
				label := "ok"
				if !check.OK {
					label = "missing"
				}
				fmt.Fprintf(c.OutOrStdout(), "%-10s %-20s %s\n", label, check.Name, check.Detail)
			}
		}
		if asJSON {
			if err := writeJSON(c, checks); err != nil {
				return err
			}
		}
		if !good {
			return errors.New("host is not ready; resolve the failed checks before creating a stage")
		}
		return nil
	}}
	c.Flags().BoolVar(&asJSON, "json", false, "machine-readable checks")
	return c
}

func stageCreateCmd() *cobra.Command {
	spec := machine.DefaultSpec()
	var memory, disk, resolution string
	var timeout time.Duration
	c := &cobra.Command{Use: "create NAME", Short: "Install Omarchy or reuse a verified base image", Args: cobra.ExactArgs(1), RunE: func(c *cobra.Command, args []string) error {
		var err error
		spec.Memory, err = machine.ParseSize(memory)
		if err != nil {
			return err
		}
		spec.Disk, err = machine.ParseSize(disk)
		if err != nil {
			return err
		}
		if _, err = fmt.Sscanf(resolution, "%dx%d", &spec.Width, &spec.Height); err != nil {
			return errors.New("resolution must be WIDTHxHEIGHT")
		}
		if err = spec.Validate(); err != nil {
			return err
		}
		m, err := machine.New()
		if err != nil {
			return err
		}
		m.Timeout = timeout
		m.Output = c.ErrOrStderr()
		if timeout <= 0 {
			return errors.New("timeout must be positive")
		}
		for _, check := range m.Doctor(c.Context()) {
			if !check.OK {
				return fmt.Errorf("%s: %s; run backstage stage doctor", check.Name, check.Detail)
			}
		}
		release, err := m.Store.LockMany(args[0], "image-catalog")
		if err != nil {
			return err
		}
		defer release()
		r, err := m.Create(c.Context(), args[0], spec)
		if err != nil {
			return err
		}
		fmt.Fprintf(c.OutOrStdout(), "Stage %s ready. Reference it with: \"vms\": {\"%s\": {\"stage\": \"%s\"}}\n", r.Name, r.Name, r.Name)
		return nil
	}}
	c.Flags().StringVar(&spec.Omarchy, "omarchy", "latest", "stable ISO version or latest")
	c.Flags().IntVar(&spec.CPUs, "cpus", 4, "virtual CPUs")
	c.Flags().StringVar(&memory, "memory", "8G", "guest memory")
	c.Flags().StringVar(&disk, "disk", "40G", "virtual disk capacity")
	c.Flags().StringVar(&resolution, "resolution", "1920x1080", "recording display size")
	c.Flags().StringVar(&spec.Keyboard, "keyboard", "us", "guest keyboard layout")
	c.Flags().StringVar(&spec.Locale, "locale", "en_US.UTF-8", "guest locale")
	c.Flags().StringVar(&spec.Timezone, "timezone", "UTC", "guest time zone")
	c.Flags().DurationVar(&timeout, "timeout", 40*time.Minute, "installation timeout")
	return c
}

func stageListCmd() *cobra.Command {
	var asJSON bool
	c := &cobra.Command{Use: "list", Short: "List shared stages", Args: cobra.NoArgs, RunE: func(c *cobra.Command, _ []string) error {
		m, err := machine.New()
		if err != nil {
			return err
		}
		list, err := m.Store.List()
		if err != nil {
			return err
		}
		type item struct {
			*machine.Record
			State      string `json:"state"`
			StateError string `json:"state-error,omitempty"`
		}
		items := []item{}
		for _, r := range list {
			state, e := m.State(c.Context(), r)
			entry := item{Record: r, State: state}
			if e != nil {
				entry.State = "unavailable"
				entry.StateError = e.Error()
			}
			items = append(items, entry)
			if !asJSON {
				fmt.Fprintf(c.OutOrStdout(), "%-24s %-12s %-12s Omarchy %s\n", r.Name, r.Status, entry.State, r.Source.Version)
			}
		}
		if asJSON {
			return writeJSON(c, items)
		}
		return nil
	}}
	c.Flags().BoolVar(&asJSON, "json", false, "machine-readable stages")
	return c
}

func stageInspectCmd() *cobra.Command {
	var asJSON bool
	c := &cobra.Command{Use: "inspect NAME", Short: "Show stage configuration and live state without secrets", Args: cobra.ExactArgs(1), RunE: func(c *cobra.Command, args []string) error {
		m, err := machine.New()
		if err != nil {
			return err
		}
		r, err := m.Store.Load(args[0])
		if err != nil {
			return err
		}
		state, stateErr := m.State(c.Context(), r)
		if asJSON {
			detail := ""
			if stateErr != nil {
				detail = stateErr.Error()
			}
			return writeJSON(c, struct {
				*machine.Record
				State string `json:"state"`
				Error string `json:"state-error,omitempty"`
			}{r, state, detail})
		}
		fmt.Fprintf(c.OutOrStdout(), "%s\nDomain: %s\nStatus: %s / %s\nPhase: %s\nOmarchy: %s\nCPU: %d  Memory: %d MiB  Disk: %d GiB\nResolution: %dx%d\nOrigin: %s\nLast error: %s\n", r.Name, r.Domain, r.Status, state, r.Phase, r.Source.Version, r.Spec.CPUs, r.Spec.Memory>>20, r.Spec.Disk>>30, r.Spec.Width, r.Spec.Height, r.Source.Image, r.LastError)
		return stateErr
	}}
	c.Flags().BoolVar(&asJSON, "json", false, "machine-readable details")
	return c
}

func stageCloneCmd() *cobra.Command {
	var snapshot string
	c := &cobra.Command{Use: "clone SOURCE NAME --snapshot SNAPSHOT", Short: "Create an independent stage from a saved state", Args: cobra.ExactArgs(2), RunE: func(c *cobra.Command, args []string) error {
		if snapshot == "" {
			return errors.New("clone requires --snapshot (use initial for the original prepared state)")
		}
		m, err := machine.New()
		if err != nil {
			return err
		}
		release, err := m.Store.LockMany(args[0], args[1], "image-catalog")
		if err != nil {
			return err
		}
		defer release()
		r, err := m.Store.Load(args[0])
		if err != nil {
			return err
		}
		cloned, err := m.Clone(c.Context(), r, args[1], snapshot)
		if err != nil {
			return err
		}
		fmt.Fprintf(c.OutOrStdout(), "Stage %s ready (stopped).\n", cloned.Name)
		return nil
	}}
	c.Flags().StringVar(&snapshot, "snapshot", "", "saved state to clone")
	return c
}

func stageOperationCmd(verb string) *cobra.Command {
	var force, yes bool
	use := verb + " NAME"
	count := 1
	if verb == "snapshot" || verb == "restore" {
		use += " SNAPSHOT"
		count = 2
	}
	c := &cobra.Command{Use: use, Short: map[string]string{"start": "Boot a stage and wait for SSH", "stop": "Shut down a stage", "ssh": "Open an administrative shell (invalidates continuity)", "credentials": "Explicitly reveal the filmed user's password", "snapshot": "Save a cold disk and firmware snapshot", "snapshots": "List saved states", "restore": "Restore a saved state, leaving the VM stopped", "delete": "Delete a managed stage"}[verb], Args: cobra.ExactArgs(count), RunE: func(c *cobra.Command, args []string) error {
		m, err := machine.New()
		if err != nil {
			return err
		}
		names := []string{args[0]}
		if verb == "snapshot" || verb == "restore" || verb == "delete" {
			names = append(names, "image-catalog")
		}
		release, err := m.Store.LockMany(names...)
		if err != nil {
			return err
		}
		defer release()
		r, err := m.Store.Load(args[0])
		if err != nil {
			return err
		}
		if verb != "credentials" && verb != "snapshots" && verb != "delete" {
			if err := m.Recover(c.Context(), r); err != nil {
				return err
			}
		}
		switch verb {
		case "start":
			_, err = m.Start(c.Context(), r)
			return err
		case "stop":
			return m.Stop(c.Context(), r, force)
		case "snapshot":
			return m.Snapshot(c.Context(), r, args[1])
		case "restore":
			return m.Restore(c.Context(), r, args[1])
		case "snapshots":
			return writeJSON(c, r.Snapshots)
		case "credentials":
			secret, err := m.Store.Credentials(r.Name)
			if err != nil {
				return err
			}
			fmt.Fprintf(c.OutOrStdout(), "User: omarchy\nPassword: %s\n", secret.Password)
			return nil
		case "delete":
			if !yes {
				fmt.Fprintf(c.ErrOrStderr(), "Delete stage %s and its private disks? Type its name: ", r.Name)
				line, err := bufio.NewReader(c.InOrStdin()).ReadString('\n')
				if err != nil {
					return err
				}
				if strings.TrimSpace(line) != r.Name {
					return errors.New("deletion cancelled")
				}
			}
			return m.Delete(c.Context(), r)
		case "ssh":
			r.Continuity = nil
			if err := m.Store.Save(r); err != nil {
				return err
			}
			g, err := m.Start(c.Context(), r)
			if err != nil {
				return err
			}
			cmd := g.InteractiveSSH(c.Context())
			cmd.Stdin = c.InOrStdin()
			cmd.Stdout = c.OutOrStdout()
			cmd.Stderr = c.ErrOrStderr()
			return cmd.Run()
		}
		return nil
	}}
	if verb == "stop" {
		c.Flags().BoolVar(&force, "force", false, "force power off instead of graceful shutdown")
	}
	if verb == "delete" {
		c.Flags().BoolVar(&yes, "yes", false, "confirm removal of this managed stage")
	}
	return c
}
