// Package firewall loads the table inet care-pi of the agent and runs the rollback timer of the policy drop
// (Phase 7, S42: "default-drop moves to Phase 7"; docs/console-server.md, section 13).
//
// A switch of security.firewall.policy from accept to drop can cut the SSH session of a headless host. So the load
// of a drop table first starts a transient timer (care-pi-firewall-rollback.timer, 90 s). When it fires,
// "care-pi firewall rollback" sets the policy to accept again, in nftables and in config.yaml. The operator stops
// the timer with "care-pi firewall confirm" or the button of the console: the "are you still there?" pattern.
package firewall

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/rithviknishad/care-pi/agent/internal/host"
	"github.com/rithviknishad/care-pi/agent/internal/paths"
)

// Policies.
const (
	Accept = "accept"
	Drop   = "drop"
)

// RollbackUnit is the transient timer and service of the rollback. RollbackDelay is the time to confirm.
const (
	RollbackUnit  = "care-pi-firewall-rollback"
	RollbackDelay = 90 * time.Second
)

// ErrNothingPending is the error of Confirm when no rollback timer runs.
var ErrNothingPending = errors.New("no firewall change waits for a confirmation")

// State is state/firewall.json.
type State struct {
	// Policy is the policy of the last load: accept or drop. "" before the first load of the agent table.
	Policy string `json:"policy"`
	// PendingUntil is the time of the rollback while the timer runs, else nil.
	PendingUntil *time.Time `json:"pending_until,omitempty"`
	ArmedAt      *time.Time `json:"armed_at,omitempty"`
	ConfirmedAt  *time.Time `json:"confirmed_at,omitempty"`
	ConfirmedBy  string     `json:"confirmed_by,omitempty"`
	RolledBackAt *time.Time `json:"rolled_back_at,omitempty"`
	// RollbackReason is "timeout" (nobody confirmed) or "manual".
	RollbackReason string `json:"rollback_reason,omitempty"`
}

// Read returns the state, or the zero state.
func Read(p paths.Paths) State {
	var st State
	data, err := os.ReadFile(p.Abs(paths.FirewallState))
	if err == nil {
		_ = json.Unmarshal(data, &st)
	}
	return st
}

// Write writes the state atomically.
func Write(p paths.Paths, st State) error {
	f := p.Abs(paths.FirewallState)
	if err := os.MkdirAll(filepath.Dir(f), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	tmp := f + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, f)
}

// TimerCmd returns the systemd-run command of the rollback timer.
func TimerCmd(delay time.Duration) host.Cmd {
	return host.Cmd{Name: "systemd-run", Args: []string{
		"--quiet", "--collect", "--unit=" + RollbackUnit,
		fmt.Sprintf("--on-active=%ds", int(delay.Seconds())), "--timer-property=AccuracySec=1s",
		"--description=care-pi firewall rollback (no confirmation of the policy drop)",
		"--", paths.Binary, "firewall", "rollback", "--reason", "timeout",
	}}
}

// StopTimerCmd stops the rollback timer. systemctl fails when the timer is not loaded; the callers ignore that.
func StopTimerCmd() host.Cmd {
	return host.Cmd{Name: "systemctl", Args: []string{"stop", RollbackUnit + ".timer"}}
}

// UnitActive reports whether care-pi-firewall.service is active. Without it (install.sh --no-firewall, ufw, or
// firewalld), the table of the agent never loads, so a rollback timer is useless.
func UnitActive(ctx context.Context, h *host.Host) bool {
	st, err := h.Systemd.Show(ctx, []string{paths.FirewallUnit}, "ActiveState")
	return err == nil && len(st) == 1 && st[0]["ActiveState"] == "active"
}

// Reload loads the rendered table (systemctl try-reload-or-restart of the firewall unit). When the policy changes
// to drop, it starts the rollback timer first, so a load that cuts the session is undone also when the agent dies.
func Reload(ctx context.Context, h *host.Host, p paths.Paths, policy string, now time.Time, out io.Writer) error {
	st := Read(p)
	prev := st.Policy
	if prev == "" {
		prev = Accept
	}
	active := UnitActive(ctx, h)
	armed := false
	switch {
	case !active:
		if policy == Drop {
			_, _ = fmt.Fprintf(out, "    warning: %s is not active: the policy drop is not loaded (install.sh --no-firewall, or ufw or firewalld)\n", paths.FirewallUnit)
		}
	case policy == Drop && prev != Drop:
		_, _ = h.Runner.Run(ctx, StopTimerCmd())
		if _, err := h.Runner.Run(ctx, TimerCmd(RollbackDelay)); err != nil {
			return fmt.Errorf("cannot start the rollback timer, so the policy drop is not loaded: %w", err)
		}
		until := now.Add(RollbackDelay)
		st.ArmedAt, st.PendingUntil, st.ConfirmedAt, st.ConfirmedBy, st.RolledBackAt, st.RollbackReason = &now, &until, nil, "", nil, ""
		armed = true
	case policy == Accept && st.PendingUntil != nil:
		_, _ = h.Runner.Run(ctx, StopTimerCmd())
		st.PendingUntil = nil
	}
	if _, err := h.Runner.Run(ctx, host.Cmd{Name: "systemctl", Args: []string{"try-reload-or-restart", paths.FirewallUnit}}); err != nil {
		if armed {
			_ = Write(p, st)
		}
		return err
	}
	if active {
		st.Policy = policy
	}
	if err := Write(p, st); err != nil {
		return err
	}
	if armed {
		_, _ = fmt.Fprintf(out, "    the policy is drop. Check that a NEW SSH session and the console still work, then run within %s:\n"+
			"      care-pi firewall confirm\n    (or Security > Firewall > Keep in the console). Else the policy goes back to accept.\n", RollbackDelay)
	}
	return nil
}

// Confirm stops the rollback timer.
func Confirm(ctx context.Context, r host.Runner, p paths.Paths, now time.Time, by string) (State, error) {
	st := Read(p)
	if st.PendingUntil == nil {
		return st, ErrNothingPending
	}
	_, _ = r.Run(ctx, StopTimerCmd())
	st.PendingUntil = nil
	st.ConfirmedAt = &now
	st.ConfirmedBy = by
	return st, Write(p, st)
}

// AcceptNow sets the policy of the live table to accept, in one nft command. The rollback runs it first: it does
// not depend on the render or on config.yaml. It fails when the table is not loaded.
func AcceptNow(ctx context.Context, r host.Runner) error {
	_, err := r.Run(ctx, host.Cmd{Name: "nft", Args: []string{"chain inet care-pi input { policy accept ; }"}})
	return err
}

// MarkRolledBack records a rollback.
func MarkRolledBack(p paths.Paths, now time.Time, reason string) (State, error) {
	st := Read(p)
	st.Policy = Accept
	st.PendingUntil = nil
	st.RolledBackAt = &now
	st.RollbackReason = reason
	return st, Write(p, st)
}
