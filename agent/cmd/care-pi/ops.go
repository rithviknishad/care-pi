package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/rithviknishad/care-pi/agent/internal/applock"
	"github.com/rithviknishad/care-pi/agent/internal/exitcode"
	"github.com/rithviknishad/care-pi/agent/internal/jobs"
	"github.com/rithviknishad/care-pi/agent/internal/paths"
	"github.com/rithviknishad/care-pi/agent/internal/service"
)

var opsFlags struct {
	json   bool
	kind   string
	state  string
	limit  int
	out    string
	lines  int
	format string
	delay  time.Duration
}

// ---- jobs ----

var jobsCmd = &cobra.Command{
	Use:   "jobs",
	Short: "List, show, follow, and cancel the jobs of the agent and of the CLI",
}

var jobsListCmd = &cobra.Command{
	Use:   "list [--json] [--kind K] [--state S] [--limit N]",
	Short: "List the jobs, newest first",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		items, next := jobs.List(rootPaths(), jobs.Filter{Kind: opsFlags.kind, State: opsFlags.state, Limit: opsFlags.limit})
		w := cmd.OutOrStdout()
		if opsFlags.json {
			var nb any
			if next != "" {
				nb = next
			}
			return printJSON(w, map[string]any{"items": items, "next_before": nb})
		}
		tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
		_, _ = fmt.Fprintln(tw, "ID\tKIND\tSTATE\tSOURCE\tSTEP")
		for _, j := range items {
			step := ""
			if j.Step != nil {
				step = *j.Step
			} else if j.ErrorTitle != nil {
				step = *j.ErrorTitle
			}
			_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", j.ID, j.Kind, j.State, j.Source, step)
		}
		return tw.Flush()
	},
}

func loadJob(id string) (*jobs.Job, error) {
	j, err := jobs.Load(rootPaths(), id)
	if errors.Is(err, jobs.ErrNotFound) {
		return nil, exitcode.With(exitcode.BadInput, fmt.Errorf("no job has the id %s", id))
	}
	return j, err
}

var jobsShowCmd = &cobra.Command{
	Use:   "show JOB_ID [--json]",
	Short: "Show one job",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		j, err := loadJob(args[0])
		if err != nil {
			return err
		}
		w := cmd.OutOrStdout()
		if opsFlags.json {
			return printJSON(w, j)
		}
		_, _ = fmt.Fprintf(w, "job %s: %s, %s (source %s)\n", j.ID, j.Kind, j.State, j.Source)
		for _, s := range j.Steps {
			line := fmt.Sprintf("  %-10s %s", s.State, s.Name)
			if s.Detail != nil {
				line += ": " + *s.Detail
			}
			_, _ = fmt.Fprintln(w, line)
		}
		if j.Error != nil {
			_, _ = fmt.Fprintln(w, "error:", j.Error.Detail)
		}
		return nil
	},
}

var jobsFollowCmd = &cobra.Command{
	Use:   "follow JOB_ID",
	Short: "Print the events of a job until it ends",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if _, err := loadJob(args[0]); err != nil {
			return err
		}
		j, err := followJob(cmd.Context(), cmd.OutOrStdout(), newService(cmd), args[0])
		if err != nil {
			return err
		}
		return jobError(j)
	},
}

var jobsCancelCmd = &cobra.Command{
	Use:   "cancel JOB_ID",
	Short: "Ask a running fe-build or support-bundle job to stop",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if _, err := loadJob(args[0]); err != nil {
			return err
		}
		if _, err := jobs.RequestCancel(rootPaths(), args[0]); err != nil {
			return cliError(err)
		}
		_, _ = fmt.Fprintln(cmd.OutOrStdout(), "The job stops soon. Follow it with: care-pi jobs follow", args[0])
		return nil
	},
}

// ---- support bundle ----

var supportCmd = &cobra.Command{
	Use:   "support-bundle [--out FILE] [--lines N]",
	Short: "Make a support bundle: status, config, units, and journals without secrets",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		svc := newService(cmd)
		if opsFlags.lines < 100 || opsFlags.lines > 10000 {
			return exitcode.With(exitcode.BadInput, errors.New("--lines must be from 100 to 10000"))
		}
		rec, err := jobs.Create(jobs.CreateOptions{Paths: svc.Paths, Kind: jobs.KindSupportBundle, Source: jobs.SourceCLI, Cancellable: true})
		if err != nil {
			return err
		}
		res, err := svc.BuildSupportBundle(cmd.Context(), rec, service.BundleRequest{LogLines: opsFlags.lines, SinceHours: 24})
		if err != nil {
			rec.Finish(err, nil)
			return err
		}
		rec.Finish(nil, res)
		src, _, err := svc.BundleFile(res.BundleID)
		if err != nil {
			return err
		}
		w := cmd.OutOrStdout()
		if opsFlags.out == "" {
			_, _ = fmt.Fprintf(w, "%s (%d bytes, sha256 %s)\n", src, res.SizeBytes, res.SHA256)
			return nil
		}
		if err := service.CopyBundle(src, opsFlags.out); err != nil {
			return err
		}
		_, _ = fmt.Fprintf(w, "%s (%d bytes, sha256 %s)\n", opsFlags.out, res.SizeBytes, res.SHA256)
		return nil
	},
}

// ---- tls ----

var tlsCmd = &cobra.Command{Use: "tls", Short: "Show the TLS modes and download the root CA"}

var tlsShowCmd = &cobra.Command{
	Use:   "show [--json]",
	Short: "Show the TLS modes and the root CA",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		t := newService(cmd).TLS()
		w := cmd.OutOrStdout()
		if opsFlags.json {
			return printJSON(w, t)
		}
		_, _ = fmt.Fprintf(w, "local CA: %v\n", t.LocalCA.Enabled)
		if r := t.LocalCA.Root; r != nil {
			_, _ = fmt.Fprintf(w, "  root:        %s\n  fingerprint: %s\n  valid until: %s\n", r.Subject, r.FingerprintColon, r.NotAfter.Format(time.RFC3339))
		} else {
			_, _ = fmt.Fprintln(w, "  root: none yet (Caddy makes it at its first start)")
		}
		_, _ = fmt.Fprintf(w, "  names: %v\npublic: %v\ntailscale: %v\n", t.LocalCA.Hostnames, t.Public.Enabled, t.Tailscale.Enabled)
		return nil
	},
}

var tlsRootCACmd = &cobra.Command{
	Use:   "root-ca [--format der|pem|mobileconfig] [--out FILE]",
	Short: "Write the root CA certificate to a file",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		svc := newService(cmd)
		var f *service.RootCAFile
		var err error
		if opsFlags.format == "mobileconfig" {
			f, err = svc.Mobileconfig()
		} else {
			f, err = svc.RootCA(opsFlags.format)
		}
		if err != nil {
			return cliError(err)
		}
		out := opsFlags.out
		if out == "" {
			out = f.Filename
		}
		if err := os.WriteFile(out, f.Data, 0o644); err != nil {
			return err
		}
		abs, _ := filepath.Abs(out)
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s\nSHA-256 fingerprint: %s\n", abs, f.Fingerprint)
		return nil
	},
}

// ---- care info ----

var careInfoCmd = &cobra.Command{
	Use:   "info [--json]",
	Short: "Show the Care URLs and the hand-off link",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		i := newService(cmd).CareInfo(cmd.Context())
		w := cmd.OutOrStdout()
		if opsFlags.json {
			return printJSON(w, i)
		}
		v := "unknown"
		if i.AppVersion != nil {
			v = *i.AppVersion
		}
		_, _ = fmt.Fprintf(w, "care-api healthy: %v (version %s)\nhand-off: %s\n", i.APIHealthy, v, i.HandoffURL)
		for _, u := range i.URLs {
			_, _ = fmt.Fprintln(w, "  "+u)
		}
		return nil
	},
}

// ---- power ----

var powerCmd = &cobra.Command{Use: "power", Short: "Reboot or shut down the host"}

func powerRun(action string) func(*cobra.Command, []string) error {
	return func(cmd *cobra.Command, _ []string) error {
		svc := newService(cmd)
		if applock.Held(svc.Paths) {
			return exitcode.With(exitcode.Failed, fmt.Errorf("a job holds the apply lock %s; wait until it ends", paths.ApplyLock))
		}
		done := make(chan struct{})
		svc.AfterPower = func(d time.Duration, fn func()) {
			time.Sleep(d)
			fn()
			close(done)
		}
		if _, err := svc.Power(action, opsFlags.delay); err != nil {
			return cliError(err)
		}
		<-done
		return nil
	}
}

var powerRebootCmd = &cobra.Command{Use: "reboot", Short: "Reboot the host", Args: cobra.NoArgs, RunE: powerRun("reboot")}
var powerShutdownCmd = &cobra.Command{Use: "shutdown", Short: "Shut down the host (a person must connect the power again)", Args: cobra.NoArgs, RunE: powerRun("shutdown")}

func init() {
	jl := jobsListCmd.Flags()
	jl.BoolVar(&opsFlags.json, "json", false, "print JSON")
	jl.StringVar(&opsFlags.kind, "kind", "", "only jobs of this kind")
	jl.StringVar(&opsFlags.state, "state", "", "only jobs in this state")
	jl.IntVar(&opsFlags.limit, "limit", 20, "the largest number of jobs")
	jobsShowCmd.Flags().BoolVar(&opsFlags.json, "json", false, "print JSON")
	jobsCmd.AddCommand(jobsListCmd, jobsShowCmd, jobsFollowCmd, jobsCancelCmd)

	supportCmd.Flags().StringVar(&opsFlags.out, "out", "", "copy the bundle to this file")
	supportCmd.Flags().IntVar(&opsFlags.lines, "lines", 1000, "the last lines of each journal")

	tlsShowCmd.Flags().BoolVar(&opsFlags.json, "json", false, "print JSON")
	tlsRootCACmd.Flags().StringVar(&opsFlags.format, "format", "der", "der, pem, or mobileconfig")
	tlsRootCACmd.Flags().StringVar(&opsFlags.out, "out", "", "the file (default: the download name in this directory)")
	tlsCmd.AddCommand(tlsShowCmd, tlsRootCACmd)

	careInfoCmd.Flags().BoolVar(&opsFlags.json, "json", false, "print JSON")
	careCmd.AddCommand(careInfoCmd)

	for _, c := range []*cobra.Command{powerRebootCmd, powerShutdownCmd} {
		c.Flags().DurationVar(&opsFlags.delay, "delay", 0, "wait this long first")
	}
	powerCmd.AddCommand(powerRebootCmd, powerShutdownCmd)

	rootCmd.AddCommand(jobsCmd, supportCmd, tlsCmd, powerCmd)
}
