package main

import (
	"encoding/json"
	"fmt"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/rithviknishad/care-pi/agent/internal/applock"
	"github.com/rithviknishad/care-pi/agent/internal/exitcode"
	"github.com/rithviknishad/care-pi/agent/internal/jobs"
	"github.com/rithviknishad/care-pi/agent/internal/service"
	"github.com/rithviknishad/care-pi/agent/internal/update"
)

var updateFlags struct {
	json             bool
	scheduled        bool
	version          string
	lockFile         string
	now              bool
	to               string
	limit            int
	before           string
	os               bool
	check            bool
	reboot           bool
	rebootIfRequired bool
	reason           string
}

// ---- update (docs/solutions.md, section 15) ----

var updateCmd = &cobra.Command{
	Use:   "update",
	Short: "Check, stage, apply, and roll back solution updates; update the OS packages (--os)",
	Long: `care-pi update manages the Phase 4 updates (docs/solutions.md).

  care-pi update status --json            the update state (GET /updates)
  care-pi update check [--scheduled]      fetch index.json and find the target
  care-pi update stage [--version V]      download and build the target while Care runs
  care-pi update apply [--version V] [--now] [--lock-file FILE]
  care-pi update rollback [--to LOCK_ID]
  care-pi update history --json
  care-pi update --os --check --json      the OS update state
  care-pi update --os [--now] [--reboot-if-required]
  care-pi update --os --reboot [--now]`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		if !updateFlags.os {
			return cmd.Help()
		}
		return runOSUpdate(cmd)
	},
}

func runOSUpdate(cmd *cobra.Command) error {
	svc := newService(cmd)
	w := cmd.OutOrStdout()
	f := updateFlags
	switch {
	case f.check:
		o, err := svc.OSUpdates(cmd.Context())
		if err != nil {
			return cliError(err)
		}
		if f.json {
			return printJSON(w, o)
		}
		_, _ = fmt.Fprintf(w, "%s: %d upgradable package(s), reboot required: %v\n", o.Distro.PrettyName, len(o.Upgradable), o.RebootRequired)
		for _, h := range o.Held {
			tested := "-"
			if h.Tested != nil {
				tested = *h.Tested
			}
			_, _ = fmt.Fprintf(w, "  held %-14s installed %-20s tested %-20s %s\n", h.Name, h.Installed, tested, h.Action)
		}
		return nil
	case f.reboot:
		r, err := svc.RebootForOS(f.now, 5*time.Second)
		if err != nil {
			return cliError(err)
		}
		if f.json {
			return printJSON(w, r)
		}
		_, _ = fmt.Fprintf(w, "reboot at %s (scheduled: %v)\n", r.At.Format(time.RFC3339), r.Scheduled)
		return nil
	}
	j, err := svc.StartOSUpdate(cmd.Context(), service.OSRequest{Now: f.now, RebootIfRequired: f.rebootIfRequired, Reason: f.reason}, jobs.SourceCLI)
	if err != nil {
		return cliError(err)
	}
	return follow(cmd, svc, j)
}

// follow prints the events of a job of this process until it ends.
func follow(cmd *cobra.Command, svc *service.Service, j *jobs.Job) error {
	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "job %s\n", j.ID)
	done, err := followJob(cmd.Context(), cmd.OutOrStdout(), svc, j.ID)
	if err != nil {
		return err
	}
	if err := svc.Jobs.Wait(cmd.Context(), j.ID); err != nil {
		return err
	}
	if updateFlags.json && done != nil {
		_ = printJSON(cmd.OutOrStdout(), done)
	}
	return jobError(done)
}

var updateStatusCmd = &cobra.Command{
	Use:   "status [--json]",
	Short: "Show the update state (GET /updates)",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		o, err := newService(cmd).Updates(cmd.Context())
		if err != nil {
			return cliError(err)
		}
		w := cmd.OutOrStdout()
		if updateFlags.json {
			return printJSON(w, o)
		}
		cur := "none"
		if o.Current != nil && o.Current.Version != nil {
			cur = *o.Current.Version
		}
		_, _ = fmt.Fprintf(w, "solution: %s (track %s), running: %s, state: %s\n", o.Solution.Name, o.Track, cur, o.State)
		if o.Target != nil {
			v := "custom"
			if o.Target.Version != nil {
				v = *o.Target.Version
			}
			_, _ = fmt.Fprintf(w, "target: %s (%s, path %s, staged %v), update available: %v\n", v, o.Target.TargetID, o.Target.Path, o.Target.Staged, o.UpdateAvailable)
		}
		if o.BlockedBy != nil {
			_, _ = fmt.Fprintf(w, "blocked: %s: %s\n", o.BlockedBy.Code, o.BlockedBy.Detail)
		}
		_, _ = fmt.Fprintf(w, "window: %v %s-%s %s, open: %v, next opening %s\n", o.Window.Days, o.Window.Start, o.Window.End, o.Window.Timezone,
			o.Window.Open, o.Window.NextOpenAt.Format(time.RFC3339))
		if o.Rollback.Available {
			_, _ = fmt.Fprintf(w, "rollback: to %s\n", *o.Rollback.ToLock)
		}
		return nil
	},
}

var updateCheckCmd = &cobra.Command{
	Use:   "check [--scheduled]",
	Short: "Fetch index.json and find the target (a job update-check)",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		svc := newService(cmd)
		source := jobs.SourceCLI
		if updateFlags.scheduled {
			source = jobs.SourceTimer
		}
		j, err := svc.StartUpdateCheck(cmd.Context(), service.CheckRequest{Scheduled: updateFlags.scheduled}, source)
		if err != nil {
			return cliError(err)
		}
		if err := follow(cmd, svc, j); err != nil {
			return err
		}
		// updates.auto: the check can start a staging job in this process. Wait for it.
		if done, err := jobs.Load(svc.Paths, j.ID); err == nil && done.Result != nil {
			var r update.CheckResult
			if json.Unmarshal(done.Result, &r) == nil && r.StageJobID != nil {
				if sj, err := jobs.Load(svc.Paths, *r.StageJobID); err == nil {
					return follow(cmd, svc, sj)
				}
			}
		}
		return nil
	},
}

var updateStageCmd = &cobra.Command{
	Use:   "stage [--version V] [--lock-file FILE]",
	Short: "Download and build the target while Care runs (a job update-stage)",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		svc := newService(cmd)
		j, err := svc.StartUpdateStage(cmd.Context(), service.StageRequest{Version: updateFlags.version, LockFile: updateFlags.lockFile}, jobs.SourceCLI)
		if err != nil {
			return cliError(err)
		}
		return follow(cmd, svc, j)
	},
}

var updateApplyCmd = &cobra.Command{
	Use:   "apply [--version V] [--now] [--lock-file FILE] [--scheduled]",
	Short: "Install the target (a job update-apply); --scheduled is the run of the update window",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		svc := newService(cmd)
		if updateFlags.scheduled {
			return scheduledApply(cmd, svc)
		}
		j, err := svc.StartUpdateApply(cmd.Context(), service.UpdateRequest{Version: updateFlags.version, Now: updateFlags.now,
			Reason: updateFlags.reason, LockFile: updateFlags.lockFile}, jobs.SourceCLI)
		if err != nil {
			return cliError(err)
		}
		return follow(cmd, svc, j)
	},
}

// scheduledApply is "care-pi update apply --scheduled" (docs/solutions.md, section 10.2).
func scheduledApply(cmd *cobra.Command, svc *service.Service) error {
	w := cmd.OutOrStdout()
	ctx := cmd.Context()
	u := svc.Updater()
	sp, err := u.Scheduled(ctx, applock.Held(svc.Paths))
	if err != nil {
		return cliError(err)
	}
	if !sp.WindowOpen {
		_, _ = fmt.Fprintln(w, "the update window is closed: nothing to do")
		return nil
	}
	var failed error
	if sp.Check {
		j, err := svc.StartUpdateCheck(ctx, service.CheckRequest{Scheduled: true}, jobs.SourceTimer)
		if err == nil {
			err = follow(cmd, svc, j)
		}
		if err != nil {
			_, _ = fmt.Fprintln(w, "warning: the check failed:", err)
		}
	}
	if sp.Update && u.UpdateReady(ctx) {
		j, err := svc.StartUpdateApply(ctx, service.UpdateRequest{Now: true, Reason: "update window"}, jobs.SourceTimer)
		if err == nil {
			err = follow(cmd, svc, j)
		}
		if err != nil {
			failed = err
			_, _ = fmt.Fprintln(w, "the update failed:", err)
		}
	}
	if sp.OS && failed == nil {
		j, err := svc.StartOSUpdate(ctx, service.OSRequest{Now: true, Reason: "update window"}, jobs.SourceTimer)
		if err == nil {
			err = follow(cmd, svc, j)
		}
		if err != nil {
			failed = err
		}
	}
	sp, err = u.Scheduled(ctx, applock.Held(svc.Paths))
	if err == nil && sp.WindowOpen && sp.Reboot {
		_ = update.UpdateState(svc.Paths, func(s *update.State) { s.RebootScheduledAt = nil })
		_, _ = fmt.Fprintln(w, "reboot: /run/reboot-required exists (or a reboot is scheduled)")
		if _, err := svc.Power("reboot", 5*time.Second); err != nil {
			return cliError(err)
		}
		time.Sleep(6 * time.Second)
	}
	return failed
}

var updateRollbackCmd = &cobra.Command{
	Use:   "rollback [--to LOCK_ID]",
	Short: "Install a previous lock (a job update-rollback)",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		svc := newService(cmd)
		j, err := svc.StartUpdateRollback(cmd.Context(), service.RollbackRequest{ToLock: updateFlags.to, Reason: updateFlags.reason}, jobs.SourceCLI)
		if err != nil {
			return cliError(err)
		}
		return follow(cmd, svc, j)
	},
}

var updateHistoryCmd = &cobra.Command{
	Use:   "history [--json] [--limit N] [--before JOB_ID]",
	Short: "List the updates, rollbacks, plug changes, and OS updates",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		if updateFlags.limit < 1 || updateFlags.limit > 100 {
			return exitcode.With(exitcode.BadInput, fmt.Errorf("--limit: use a number from 1 to 100"))
		}
		h := newService(cmd).UpdateHistory(updateFlags.limit, updateFlags.before)
		w := cmd.OutOrStdout()
		if updateFlags.json {
			return printJSON(w, h)
		}
		tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
		_, _ = fmt.Fprintln(tw, "JOB\tKIND\tOUTCOME\tFROM\tTO")
		for _, e := range h.Items {
			_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", e.JobID, e.Kind, e.Outcome, deref(e.FromVersion), deref(e.ToVersion))
		}
		return tw.Flush()
	},
}

func deref(s *string) string {
	if s == nil {
		return "-"
	}
	return *s
}

func init() {
	pf := updateCmd.Flags()
	pf.BoolVar(&updateFlags.os, "os", false, "the OS packages (apt) instead of the solution")
	pf.BoolVar(&updateFlags.check, "check", false, "with --os: show the OS update state")
	pf.BoolVar(&updateFlags.reboot, "reboot", false, "with --os: reboot now (--now) or in the next window")
	pf.BoolVar(&updateFlags.rebootIfRequired, "reboot-if-required", false, "with --os: reboot at the end when /run/reboot-required exists")
	pf.BoolVar(&updateFlags.now, "now", false, "run now, also outside the update window")
	pf.BoolVar(&updateFlags.json, "json", false, "print JSON")
	pf.StringVar(&updateFlags.reason, "reason", "", "a text for the job journal and the history")
	updateStatusCmd.Flags().BoolVar(&updateFlags.json, "json", false, "print JSON")
	updateCheckCmd.Flags().BoolVar(&updateFlags.scheduled, "scheduled", false, "the run of care-update-check.timer")
	updateCheckCmd.Flags().BoolVar(&updateFlags.json, "json", false, "print the job as JSON at the end")
	for _, c := range []*cobra.Command{updateStageCmd, updateApplyCmd} {
		c.Flags().StringVar(&updateFlags.version, "version", "", "the lock version YYYY.MM.N (default: the target)")
		c.Flags().StringVar(&updateFlags.lockFile, "lock-file", "", "install a solution lock file (a test of a candidate)")
		c.Flags().BoolVar(&updateFlags.json, "json", false, "print the job as JSON at the end")
		addLockFlag(c)
	}
	updateApplyCmd.Flags().BoolVar(&updateFlags.now, "now", false, "run the cutover now, also outside the update window")
	updateApplyCmd.Flags().BoolVar(&updateFlags.scheduled, "scheduled", false, "the run of care-update-apply.timer (the update window)")
	updateApplyCmd.Flags().StringVar(&updateFlags.reason, "reason", "", "a text for the job journal and the history")
	updateRollbackCmd.Flags().StringVar(&updateFlags.to, "to", "", "the lock id in /var/lib/care-pi/state/locks (default: history.previous)")
	updateRollbackCmd.Flags().StringVar(&updateFlags.reason, "reason", "", "a text for the job journal and the history")
	updateRollbackCmd.Flags().BoolVar(&updateFlags.json, "json", false, "print the job as JSON at the end")
	addLockFlag(updateRollbackCmd)
	updateHistoryCmd.Flags().BoolVar(&updateFlags.json, "json", false, "print JSON")
	updateHistoryCmd.Flags().IntVar(&updateFlags.limit, "limit", 20, "the largest number of entries")
	updateHistoryCmd.Flags().StringVar(&updateFlags.before, "before", "", "only entries of jobs before this job")
	updateCmd.AddCommand(updateStatusCmd, updateCheckCmd, updateStageCmd, updateApplyCmd, updateRollbackCmd, updateHistoryCmd)
	rootCmd.AddCommand(updateCmd)
}
