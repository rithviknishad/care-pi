package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/spf13/cobra"

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
Policy ntp: wait until "timedatectl show -p NTPSynchronized" is yes.
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
		p := rootPaths()
		flag := p.Abs(paths.ModeDir + "/" + paths.ModeClockWait)
		h := newHost(cmd)
		ok, err := h.Clock.NTPSynchronized(cmd.Context())
		if err == nil && ok {
			clockPassed(cmd.Context(), h, flag, cmd.OutOrStdout())
			return nil
		}
		if err := os.MkdirAll(p.Abs(paths.ModeDir), 0o755); err == nil {
			_ = os.WriteFile(flag, nil, 0o644)
		}
		_, _ = fmt.Fprintln(cmd.OutOrStdout(), "clock: wait for NTP synchronization")
		var deadline <-chan time.Time
		if clockTimeout > 0 {
			deadline = time.After(clockTimeout)
		}
		tick := time.NewTicker(2 * time.Second)
		defer tick.Stop()
		for {
			select {
			case <-cmd.Context().Done():
				return cmd.Context().Err()
			case <-deadline:
				return &exitcode.Error{Code: exitcode.Failed, Err: errors.New("the clock did not synchronize before the timeout")}
			case <-tick.C:
				ok, err := h.Clock.NTPSynchronized(cmd.Context())
				if err != nil {
					_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "care-pi: warning: %v\n", err)
					continue
				}
				if ok {
					clockPassed(cmd.Context(), h, flag, cmd.OutOrStdout())
					return nil
				}
			}
		}
	},
}

// caddyReload makes Caddy read its config and check its certificates again.
// --force is necessary: Caddy skips the reload of a config that did not change.
var caddyReload = []string{"caddy", "reload", "--config", "/etc/caddy/Caddyfile", "--adapter", "caddyfile", "--force"}

// clockPassed deletes the clock-wait flag. Then it reloads Caddy.
//
// Caddy starts before the clock gate, so it can show the clock page. With a stale clock
// (fake-hwclock), Caddy can load an expired certificate as valid. After NTP sets the clock,
// Caddy serves that certificate until its next renewal check, about 10 minutes later.
// The reload checks the certificates now. The reload is best effort: at boot, Caddy can
// be not running yet. Then Caddy reads the certificates with the correct clock when it starts.
func clockPassed(ctx context.Context, h *host.Host, flag string, w io.Writer) {
	_ = os.Remove(flag)
	_, _ = fmt.Fprintln(w, "clock: synchronized")
	if _, err := h.Podman.Exec(ctx, "care-caddy", "", caddyReload...); err == nil {
		_, _ = fmt.Fprintln(w, "clock: reloaded Caddy")
		return
	}
	// try-restart does not start a Caddy that does not run.
	if err := h.Systemd.TryRestart(ctx, "care-caddy.service"); err != nil {
		_, _ = fmt.Fprintf(w, "clock: warning: cannot restart care-caddy.service: %v\n", err)
	}
}

func init() {
	clockWaitCmd.Flags().DurationVar(&clockTimeout, "timeout", 0, "maximum wait, for example 10m (0: no limit)")
	clockCmd.AddCommand(clockWaitCmd)
	rootCmd.AddCommand(clockCmd)
}
