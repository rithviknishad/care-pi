package main

import (
	"os"

	"github.com/spf13/cobra"

	"github.com/rithviknishad/care-pi/agent/internal/apply"
	"github.com/rithviknishad/care-pi/agent/internal/netinfo"
	"github.com/rithviknishad/care-pi/agent/internal/s3admin"
)

var applyFlags struct {
	dryRun    bool
	reason    string
	update    bool
	solutions string
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

Exit codes: 0 when apply completes or finds no change, 1 when a step fails
or the lock is not free before the timeout, 2 when the config is not valid.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		_, err := apply.Run(cmd.Context(), apply.Options{
			Paths:        rootPaths(),
			ConfigPath:   configPath(cmd),
			SolutionsDir: applyFlags.solutions,
			DryRun:       applyFlags.dryRun,
			Update:       applyFlags.update,
			Reason:       applyFlags.reason,
			Out:          cmd.OutOrStdout(),
			Host:         newHost(cmd),
			IPs:          netinfo.System{},
			S3:           s3admin.MinIO{},
			Chown:        os.Geteuid() == 0,
			LockTimeout:  lockTimeout,
		})
		return err
	},
}

func init() {
	f := applyCmd.Flags()
	f.BoolVar(&applyFlags.dryRun, "dry-run", false, "print the plan and the diff; write nothing")
	f.StringVar(&applyFlags.reason, "reason", "", "reason for this run, for the job journal")
	f.BoolVar(&applyFlags.update, "update", false, "resolve the tracked refs again")
	f.StringVar(&applyFlags.solutions, "solutions", "", "directory with <name>/solution.yaml (default: the embedded solutions)")
	_ = f.MarkHidden("solutions")
	addLockFlag(applyCmd)
	rootCmd.AddCommand(applyCmd)
}
