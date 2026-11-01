package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/rithviknishad/care-pi/agent/internal/clockgate"
	"github.com/rithviknishad/care-pi/agent/internal/config"
	"github.com/rithviknishad/care-pi/agent/internal/exitcode"
	"github.com/rithviknishad/care-pi/agent/internal/host"
	"github.com/rithviknishad/care-pi/agent/internal/paths"
)

var clockTimeout time.Duration

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
		if policy == "rtc" {
			return fmt.Errorf("clock policy rtc is %w (SP9); set clock.policy to ntp", config.ErrNotImplemented)
		}
		return clockWait(cmd.Context(), clockWaitEnv{paths: rootPaths(), host: newHost(cmd), timeout: clockTimeout,
			grace: clockgate.NTPGrace, tick: 2 * time.Second, out: cmd.OutOrStdout(), errw: cmd.ErrOrStderr()})
	},
}

type clockWaitEnv struct {
	paths       paths.Paths
	host        *host.Host
	timeout     time.Duration
	grace, tick time.Duration
	out, errw   io.Writer
}

// clockWait waits for NTP, or for the clock that the browser set at the claim (after the NTP grace time).
func clockWait(ctx context.Context, e clockWaitEnv) error {
	p, h := e.paths, e.host
	flag := p.Abs(paths.ModeDir + "/" + paths.ModeClockWait)
	ok, err := h.Clock.NTPSynchronized(ctx)
	if err == nil && ok {
		_ = clockgate.Clear(p)
		clockPassed(ctx, h, flag, e.out)
		return nil
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
				_ = clockgate.Clear(p)
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

func init() {
	clockWaitCmd.Flags().DurationVar(&clockTimeout, "timeout", 0, "maximum wait, for example 10m (0: no limit)")
	clockCmd.AddCommand(clockWaitCmd)
	rootCmd.AddCommand(clockCmd)
}
