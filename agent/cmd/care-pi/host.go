package main

import (
	"errors"

	"github.com/spf13/cobra"

	"github.com/rithviknishad/care-pi/agent/internal/exitcode"
	"github.com/rithviknishad/care-pi/agent/internal/hostcheck"
)

var hostCheckJSON bool

var hostCmd = &cobra.Command{
	Use:   "host",
	Short: "Host commands",
}

var hostCheckCmd = &cobra.Command{
	Use:   "check",
	Short: "Report the host prerequisites; change nothing",
	Long: `check reports the prerequisites that the installer provides: podman 5.4 or
later with quadlet, skopeo, the cgroup memory controller, zram swap, avahi,
nftables, and the directories. check changes nothing.
Exit code 0: every required check passes. Exit code 3: a required check fails.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		r := hostcheck.Run(cmd.Context(), hostcheck.Env{Paths: rootPaths(), Podman: newHost(cmd).Podman})
		if hostCheckJSON {
			if err := r.WriteJSON(cmd.OutOrStdout()); err != nil {
				return err
			}
		} else {
			r.WriteText(cmd.OutOrStdout())
		}
		if !r.OK() {
			return &exitcode.Error{Code: exitcode.HostMissing, Err: errors.New("a required host prerequisite is missing")}
		}
		return nil
	},
}

func init() {
	hostCheckCmd.Flags().BoolVar(&hostCheckJSON, "json", false, "print JSON")
	hostCmd.AddCommand(hostCheckCmd)
	rootCmd.AddCommand(hostCmd)
}
