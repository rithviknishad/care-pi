package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/spf13/cobra"
	"go.yaml.in/yaml/v3"

	"github.com/rithviknishad/care-pi/agent/internal/clockgate"
	"github.com/rithviknishad/care-pi/agent/internal/config"
	"github.com/rithviknishad/care-pi/agent/internal/exitcode"
	"github.com/rithviknishad/care-pi/agent/internal/host"
	"github.com/rithviknishad/care-pi/agent/internal/lock"
	"github.com/rithviknishad/care-pi/agent/internal/paths"
)

var (
	clockTimeout                        time.Duration
	clockDetectDryRun, clockDetectForce bool
	clockDetectJSON                     bool
)

var clockCmd = &cobra.Command{
	Use:   "clock",
	Short: "Clock gate commands",
}

var clockWaitCmd = &cobra.Command{
	Use:   "wait",
	Short: "Wait until the clock policy passes",
	Long: `wait blocks until the clock policy of config.yaml passes.
Policy ntp: wait until "timedatectl show -p NTPSynchronized" is yes. Without
internet, the console sends the time of the browser at the claim, and the agent
sets the clock (state/clock-trusted.json, S52): then wait tries NTP for 20 s more
and passes with that clock. The clock is not trusted until NTP synchronizes it;
then wait (or the status of the agent) deletes the file.
Policy rtc (S75): pass at once when the RTC time (/sys/class/rtc/rtc0) is after
the build time of the agent; systemd-timesyncd still runs. The clock is not
trusted until NTP synchronizes it; then the agent records the drift of the RTC
(state/clock-rtc-check.json; more than 5 minutes gives a warning of the doctor).
An RTC time before the build time (no battery) waits as policy ntp does.
While wait runs, Caddy shows the clock status page.
When the clock passes, wait reloads Caddy, so that Caddy checks its certificates
with the correct clock. When Caddy does not run, wait does not start it.
Exit code 0: the clock passed. Exit code 1: timeout or error.
A timeout of 0 waits without a limit.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		policy := config.DefaultClockPolicy
		if cfg, err := config.Load(configPath(cmd)); err == nil {
			policy = cfg.Clock.Policy
		} else {
			_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "care-pi: warning: %v; use policy ntp\n", err)
		}
		return clockWait(cmd.Context(), clockWaitEnv{paths: rootPaths(), host: newHost(cmd), timeout: clockTimeout,
			grace: clockgate.NTPGrace, tick: 2 * time.Second, out: cmd.OutOrStdout(), errw: cmd.ErrOrStderr(), policy: policy})
	},
}

type clockWaitEnv struct {
	paths       paths.Paths
	host        *host.Host
	timeout     time.Duration
	grace, tick time.Duration
	out, errw   io.Writer
	// policy is clock.policy: ntp (or empty) or rtc.
	policy string
}

// clockWait waits for NTP, or for the clock that the browser set at the claim (after the NTP grace time).
func clockWait(ctx context.Context, e clockWaitEnv) error {
	p, h := e.paths, e.host
	flag := p.Abs(paths.ModeDir + "/" + paths.ModeClockWait)
	ok, err := h.Clock.NTPSynchronized(ctx)
	if err == nil && ok {
		_ = clockgate.Synchronized(p, time.Now())
		clockPassed(ctx, h, flag, e.out)
		return nil
	}
	if e.policy == "rtc" {
		t, pass, err := clockgate.FromRTC(p, time.Now())
		switch {
		case err != nil:
			_, _ = fmt.Fprintf(e.errw, "care-pi: warning: %v; wait as policy ntp\n", err)
		case pass:
			// timesyncd still runs: NTP confirms the RTC later, and the clock is trusted then.
			if err := h.Systemd.Start(ctx, "systemd-timesyncd.service"); err != nil {
				_, _ = fmt.Fprintf(e.errw, "care-pi: warning: cannot start systemd-timesyncd.service: %v\n", err)
			}
			_, _ = fmt.Fprintf(e.out, "clock: policy rtc: the RTC time %s is after the build time of the agent; pass (not trusted until NTP synchronizes it)\n",
				t.Format(time.RFC3339))
			clockPassed(ctx, h, flag, e.out)
			return nil
		default:
			_, _ = fmt.Fprintf(e.out, "clock: policy rtc: the RTC time %s is before the build time of the agent (no battery?); wait as policy ntp\n",
				t.Format(time.RFC3339))
		}
	}
	if err := os.MkdirAll(p.Abs(paths.ModeDir), 0o755); err == nil {
		_ = os.WriteFile(flag, nil, 0o644)
	}
	_, _ = fmt.Fprintln(e.out, "clock: wait for NTP synchronization")
	var deadline <-chan time.Time
	if e.timeout > 0 {
		deadline = time.After(e.timeout)
	}
	var browserSince time.Time
	browser := func() bool {
		m, ok := clockgate.Read(p)
		if !ok || m.Source != clockgate.SourceBrowser {
			return false
		}
		if browserSince.IsZero() {
			browserSince = time.Now()
			_, _ = fmt.Fprintf(e.out, "clock: the browser set the clock at the claim (%s); try NTP for %s more\n",
				m.SetAt.Format(time.RFC3339), e.grace)
		}
		return time.Since(browserSince) >= e.grace
	}
	if browser() {
		clockFromBrowser(ctx, h, flag, e.out)
		return nil
	}
	tick := time.NewTicker(e.tick)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline:
			return &exitcode.Error{Code: exitcode.Failed, Err: errors.New("the clock did not synchronize before the timeout")}
		case <-tick.C:
			ok, err := h.Clock.NTPSynchronized(ctx)
			if err != nil {
				_, _ = fmt.Fprintf(e.errw, "care-pi: warning: %v\n", err)
			}
			if err == nil && ok {
				_ = clockgate.Synchronized(p, time.Now())
				clockPassed(ctx, h, flag, e.out)
				return nil
			}
			if browser() {
				clockFromBrowser(ctx, h, flag, e.out)
				return nil
			}
		}
	}
}

func clockFromBrowser(ctx context.Context, h *host.Host, flag string, w io.Writer) {
	_, _ = fmt.Fprintln(w, "clock: no NTP; use the clock that the browser set (not trusted until NTP synchronizes it)")
	clockPassed(ctx, h, flag, w)
}

// clockPassed deletes the clock-wait flag. Then it reloads Caddy.
//
// Caddy starts before the clock gate, so it can show the clock page. With a stale clock
// (fake-hwclock), Caddy can load an expired certificate as valid. After NTP sets the clock,
// Caddy serves that certificate until its next renewal check, about 10 minutes later.
// The reload checks the certificates now. The reload is best effort: at boot, Caddy can
// be not running yet. Then Caddy reads the certificates with the correct clock when it starts.
func clockPassed(ctx context.Context, h *host.Host, flag string, w io.Writer) {
	_ = os.Remove(flag)
	_, _ = fmt.Fprintln(w, "clock: passed")
	clockgate.ReloadCaddy(ctx, h, true, w)
}

var clockDetectCmd = &cobra.Command{
	Use:   "detect [--dry-run] [--force] [--json]",
	Short: "Choose clock.policy from the RTC of the host (S75)",
	Long: `detect reads the RTC of the host and chooses clock.policy:
rtc when /dev/rtc0 exists, its time at boot (the kernel message "setting system
clock to") is after the build time of the agent, and the host does not report a
battery voltage of 0 (/sys/class/rtc/rtc0/battery_voltage on a Pi 5); else ntp.
A Pi 5 without a battery reads 1970-01-01 at boot. fake-hwclock is never trusted.
detect writes clock.policy to config.yaml (comments stay) unless the operator
set it: a value that is not the one of the last detection
(state/clock-detect.json), or, without a detection, not ntp. --force writes it
anyway. --dry-run prints what detect would set and writes nothing.
bootstrap runs detect (the installer and the first boot of the image).
detect does not apply the config: the policy acts at the next "clock wait".`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		d, err := clockDetect(cmd.Context(), clockDetectEnv{paths: rootPaths(), host: newHost(cmd), configPath: configPath(cmd),
			dryRun: clockDetectDryRun, force: clockDetectForce, out: cmd.OutOrStdout()}, !clockDetectJSON)
		if err != nil {
			return err
		}
		if clockDetectJSON {
			return printJSON(cmd.OutOrStdout(), d)
		}
		return nil
	},
}

type clockDetectEnv struct {
	paths         paths.Paths
	host          *host.Host
	configPath    string
	dryRun, force bool
	// keep: the operator set clock.policy (a pre-seed with the key); detect prints but does not change it.
	keep bool
	out  io.Writer
	// kernelLog overrides the read of the kernel log (tests).
	kernelLog func(context.Context) []byte
	now       time.Time
}

// readKernelLog returns the kernel log of this boot: journalctl, else dmesg.
func readKernelLog(ctx context.Context, h *host.Host) []byte {
	if out, err := h.Runner.Run(ctx, host.Cmd{Name: "journalctl", Args: []string{"-k", "-b", "0", "-o", "cat", "--no-pager"}}); err == nil && len(out) > 0 {
		return out
	}
	out, _ := h.Runner.Run(ctx, host.Cmd{Name: "dmesg"})
	return out
}

// clockDetect chooses clock.policy from the RTC (S75) and writes it to config.yaml unless the operator set it.
func clockDetect(ctx context.Context, e clockDetectEnv, text bool) (clockgate.Detection, error) {
	p, h := e.paths, e.host
	synced, _ := h.Clock.NTPSynchronized(ctx)
	var klog []byte
	if e.kernelLog != nil {
		klog = e.kernelLog(ctx)
	} else {
		klog = readKernelLog(ctx, h)
	}
	now := e.now
	if now.IsZero() {
		now = time.Now()
	}
	d := clockgate.Detect(clockgate.ReadRTC(p, klog, synced), clockgate.Floor(), now)
	out := func(format string, a ...any) {
		if text {
			_, _ = fmt.Fprintf(e.out, format, a...)
		}
	}
	out("clock detect: policy %s: %s\n", d.Policy, d.Reason)
	if !e.dryRun {
		// A new detection: the drift that an earlier boot measured (state/clock-rtc-check.json) means nothing now.
		if err := clockgate.ClearRTCCheck(p); err != nil {
			return d, fmt.Errorf("cannot delete the RTC check record: %w", err)
		}
	}
	data, err := os.ReadFile(e.configPath)
	if err != nil {
		out("clock detect: no %s yet; nothing to write\n", e.configPath)
		return d, nil
	}
	cfg, err := config.Parse(data, e.configPath)
	if err != nil {
		return d, exitcode.With(exitcode.BadInput, err)
	}
	note := &clockgate.ConfigNote{Before: cfg.Clock.Policy, After: cfg.Clock.Policy}
	d.Config = note
	switch {
	case cfg.Clock.Policy == d.Policy:
		note.Reason = "clock.policy is " + d.Policy + " already"
	case e.keep || (!e.force && clockgate.OperatorSet(p, cfg.Clock.Policy)):
		note.Reason = "the operator set clock.policy " + cfg.Clock.Policy + "; keep it (use --force to change it)"
	default:
		note.After = d.Policy
		note.Reason = "set clock.policy " + d.Policy
	}
	if e.dryRun {
		out("clock detect: dry run: %s (config.yaml has %s); nothing written\n", note.Reason, note.Before)
		return d, nil
	}
	if note.After != note.Before {
		nd, err := config.WithClockPolicy(data, note.After)
		if err != nil {
			return d, err
		}
		if err := lock.WriteFileAtomic(e.configPath, nd, 0o600); err != nil {
			return d, fmt.Errorf("cannot write %s: %w", e.configPath, err)
		}
		note.Written = true
	}
	if err := clockgate.SaveDetection(p, d); err != nil {
		return d, fmt.Errorf("cannot write the detection record: %w", err)
	}
	out("clock detect: %s\n", note.Reason)
	return d, nil
}

func init() {
	clockWaitCmd.Flags().DurationVar(&clockTimeout, "timeout", 0, "maximum wait, for example 10m (0: no limit)")
	clockDetectCmd.Flags().BoolVar(&clockDetectDryRun, "dry-run", false, "print what detect would set; write nothing")
	clockDetectCmd.Flags().BoolVar(&clockDetectForce, "force", false, "write the detected policy also when the operator set another one")
	clockDetectCmd.Flags().BoolVar(&clockDetectJSON, "json", false, "print the detection as JSON")
	clockCmd.AddCommand(clockWaitCmd, clockDetectCmd)
	rootCmd.AddCommand(clockCmd)
}

// preseedSetsClock reports whether the config (a pre-seed) has the key clock.policy.
func preseedSetsClock(path string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	var doc struct {
		Clock map[string]any `yaml:"clock"`
	}
	if yaml.Unmarshal(data, &doc) != nil {
		return false
	}
	_, ok := doc.Clock["policy"]
	return ok
}
