package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/rithviknishad/care-pi/agent/internal/apply"
	"github.com/rithviknishad/care-pi/agent/internal/exitcode"
	"github.com/rithviknishad/care-pi/agent/internal/netinfo"
	"github.com/rithviknishad/care-pi/agent/internal/s3admin"
	"github.com/rithviknishad/care-pi/agent/internal/setup"
)

var applyFlags struct {
	dryRun    bool
	reason    string
	update    bool
	solutions string
	phase     string
}

var applyCmd = &cobra.Command{
	Use:   "apply",
	Short: "Validate the config, resolve the lock, render, and apply the changes",
	Long: `apply makes the host match config.yaml.

Steps: validate the config, resolve the lock, render the files, compare them
with the files on disk, write the changes, reload systemd, and restart only
the units that the changes affect. Then apply makes sure that the buckets and
the pgBackRest stanza exist.

Only one apply, bootstrap, fe build, or fe prune runs at a time. A second
command waits for the lock /run/care-pi/apply.lock, at most --lock-timeout.
With the lock, no FE build runs. Thus apply removes the scale-down drop-in
of an FE build that stopped before its restore.

During the setup (setup mode), apply renders only care.pod, care-caddy, and the
start units (--phase=bootstrap). The setup finish runs the full apply. --phase
overrides the phase of the host.

Exit codes: 0 when apply completes or finds no change, 1 when a step fails
or the lock is not free before the timeout, 2 when the config is not valid.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		if err := checkPhase(applyFlags.phase); err != nil {
			return err
		}
		return runApply(cmd, applyFlags.reason)
	},
}

// checkPhase accepts "", bootstrap, or full.
func checkPhase(p string) error {
	if p != "" && p != setup.PhaseBootstrap && p != setup.PhaseFull {
		return exitcode.With(exitcode.BadInput, fmt.Errorf("--phase must be bootstrap or full"))
	}
	return nil
}

// runApply runs apply in this process behind the apply lock. When the agent runs, it does not
// matter: the lock serialises the two, and the job journal of this run is in the same directory.
func runApply(cmd *cobra.Command, reason string) error {
	_, err := apply.Run(cmd.Context(), apply.Options{
		Paths:        rootPaths(),
		ConfigPath:   configPath(cmd),
		SolutionsDir: applyFlags.solutions,
		DryRun:       applyFlags.dryRun,
		Update:       applyFlags.update,
		Reason:       reason,
		Out:          cmd.OutOrStdout(),
		Host:         newHost(cmd),
		IPs:          netinfo.System{},
		S3:           s3admin.MinIO{},
		Chown:        os.Geteuid() == 0,
		LockTimeout:  lockTimeout,
		Phase:        applyFlags.phase,
	})
	return err
}

func init() {
	f := applyCmd.Flags()
	f.BoolVar(&applyFlags.dryRun, "dry-run", false, "print the plan and the diff; write nothing")
	f.StringVar(&applyFlags.reason, "reason", "", "reason for this run, for the job journal")
	f.BoolVar(&applyFlags.update, "update", false, "resolve the tracked refs again")
	f.StringVar(&applyFlags.phase, "phase", "", "bootstrap (setup mode: the pod and Caddy only) or full (default: the phase of the host)")
	f.StringVar(&applyFlags.solutions, "solutions", "", "directory with <name>/solution.yaml (default: the embedded solutions)")
	_ = f.MarkHidden("solutions")
	addLockFlag(applyCmd)
	rootCmd.AddCommand(applyCmd)
}
