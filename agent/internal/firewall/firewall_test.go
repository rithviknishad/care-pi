package firewall

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rithviknishad/care-pi/agent/internal/host"
	"github.com/rithviknishad/care-pi/agent/internal/paths"
)

const showActive = "systemctl show -p ActiveState care-pi-firewall.service"

func env(t *testing.T, active bool) (*host.Fake, *host.Host, paths.Paths) {
	t.Helper()
	f := host.NewFake()
	state := "inactive"
	if active {
		state = "active"
	}
	f.On(showActive, host.Response{Out: "ActiveState=" + state + "\n"})
	return f, host.New(f), paths.New(t.TempDir())
}

var t0 = time.Date(2026, 11, 2, 10, 0, 0, 0, time.UTC)

const (
	acceptTable = "table inet care-pi { chain input { policy accept; } }\n"
	dropTable   = "table inet care-pi { chain input { policy drop; } }\n"
)

// trial writes the files of an apply that renders a policy drop before its confirmation: the boot file with accept and
// the pending file with drop.
func trial(t *testing.T, p paths.Paths) {
	t.Helper()
	for f, data := range map[string]string{paths.FirewallFile: acceptTable, paths.FirewallPendingFile: dropTable} {
		if err := os.MkdirAll(filepath.Dir(p.Abs(f)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p.Abs(f), []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func read(t *testing.T, p paths.Paths, f string) string {
	t.Helper()
	data, err := os.ReadFile(p.Abs(f))
	if err != nil {
		return ""
	}
	return string(data)
}

// TestDropArmsTheTimerFirst: a switch from accept to drop starts the rollback timer before the load of the pending
// table; the boot file keeps accept until the confirmation, which moves the pending table over it.
func TestDropArmsTheTimerFirst(t *testing.T) {
	f, h, p := env(t, true)
	trial(t, p)
	var out bytes.Buffer
	if err := Reload(context.Background(), h, p, Drop, t0, &out); err != nil {
		t.Fatal(err)
	}
	calls := f.Filter("systemd-run", "systemctl try-reload-or-restart", "systemctl stop", "nft")
	if len(calls) != 3 || calls[0] != "systemctl stop care-pi-firewall-rollback.timer" ||
		!strings.HasPrefix(calls[1], "systemd-run --quiet --collect --unit=care-pi-firewall-rollback --on-active=90s") ||
		!strings.HasSuffix(calls[1], "-- /usr/local/bin/care-pi firewall rollback --reason timeout") ||
		calls[2] != "nft -f /etc/care-pi/rendered/firewall/care-pi.pending.nft" {
		t.Fatalf("calls %q", calls)
	}
	st := Read(p)
	if st.Policy != Drop || st.PendingUntil == nil || !st.PendingUntil.Equal(t0.Add(RollbackDelay)) || st.DropConfirmed() {
		t.Fatalf("state %+v", st)
	}
	if read(t, p, paths.FirewallFile) != acceptTable {
		t.Fatal("the boot file must keep accept before the confirmation")
	}
	if !strings.Contains(out.String(), "care-pi firewall confirm") {
		t.Fatalf("output %q", out.String())
	}
	// A second load in the window (for example a new Tailscale rule) loads the pending table again; no second timer.
	f.Calls = nil
	if err := Reload(context.Background(), h, p, Drop, t0.Add(time.Minute), &out); err != nil {
		t.Fatal(err)
	}
	if got := f.Filter("systemd-run", "systemctl try-reload-or-restart", "nft"); len(got) != 1 || got[0] != "nft -f /etc/care-pi/rendered/firewall/care-pi.pending.nft" {
		t.Fatalf("second load %q", got)
	}

	// Confirm stops the timer and moves the pending table over the boot file; a second confirm has nothing to do.
	st, err := Confirm(context.Background(), f, p, t0.Add(30*time.Second), "cli")
	if err != nil || st.PendingUntil != nil || st.ConfirmedAt == nil || st.ConfirmedBy != "cli" || !st.DropConfirmed() {
		t.Fatalf("confirm %+v %v", st, err)
	}
	if read(t, p, paths.FirewallFile) != dropTable || read(t, p, paths.FirewallPendingFile) != "" {
		t.Fatal("the confirmation must move the pending table over the boot file")
	}
	if _, err := Confirm(context.Background(), f, p, t0, "cli"); !errors.Is(err, ErrNothingPending) {
		t.Fatalf("second confirm %v", err)
	}
	// A later apply of the confirmed drop renders no pending file: the boot file loads, and no timer starts.
	f.Calls = nil
	if err := Reload(context.Background(), h, p, Drop, t0.Add(time.Hour), &out); err != nil {
		t.Fatal(err)
	}
	if got := f.Filter("systemd-run", "systemctl try-reload-or-restart", "nft"); len(got) != 1 || got[0] != "systemctl try-reload-or-restart care-pi-firewall.service" {
		t.Fatalf("confirmed drop %q", got)
	}
	// Back to accept: no timer, and the state follows.
	f.Calls = nil
	if err := Reload(context.Background(), h, p, Accept, t0.Add(2*time.Hour), &out); err != nil {
		t.Fatal(err)
	}
	if got := f.Filter("systemd-run"); len(got) != 0 || Read(p).Policy != Accept {
		t.Fatalf("accept %q %+v", got, Read(p))
	}
}

// TestConfirmRefusesAStaleTrial (M1 of the Phase 7 review, a fake clock): a confirmation after the time to confirm,
// or without the pending file, changes nothing.
func TestConfirmRefusesAStaleTrial(t *testing.T) {
	f, h, p := env(t, true)
	trial(t, p)
	if err := Reload(context.Background(), h, p, Drop, t0, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	f.Calls = nil
	if _, err := Confirm(context.Background(), f, p, t0.Add(RollbackDelay), "cli"); !errors.Is(err, ErrWindowEnded) {
		t.Fatalf("confirm at the end of the window: %v", err)
	}
	if read(t, p, paths.FirewallFile) != acceptTable || read(t, p, paths.FirewallPendingFile) != dropTable || len(f.Calls) != 0 {
		t.Fatalf("a refused confirmation changed something: %q", f.Calls)
	}
	if err := os.Remove(p.Abs(paths.FirewallPendingFile)); err != nil {
		t.Fatal(err)
	}
	if _, err := Confirm(context.Background(), f, p, t0.Add(time.Second), "cli"); !errors.Is(err, ErrNothingPending) {
		t.Fatalf("confirm without the pending file: %v", err)
	}
	if read(t, p, paths.FirewallFile) != acceptTable {
		t.Fatal("the boot file changed")
	}
	// An apply after the end of the window (the rollback did not run) starts a new trial with a new timer.
	trial(t, p)
	f.Calls = nil
	if err := Reload(context.Background(), h, p, Drop, t0.Add(time.Hour), &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if got := f.Filter("systemd-run"); len(got) != 1 || !Read(p).PendingUntil.Equal(t0.Add(time.Hour+RollbackDelay)) {
		t.Fatalf("re-arm %q %+v", got, Read(p))
	}
}

// TestRebootDuringTheWindow (M1 of the Phase 7 review): the timer is transient, so a reboot in the window loses it.
// The boot file has accept, and the boot check (ClearPending, then the caller records the rollback) ends the trial: a
// later confirmation refuses.
func TestRebootDuringTheWindow(t *testing.T) {
	f, h, p := env(t, true)
	trial(t, p)
	if err := Reload(context.Background(), h, p, Drop, t0, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	// The reboot: a new process reads the state from the disk.
	stale, err := ClearPending(p)
	if err != nil || !stale {
		t.Fatalf("boot check %v %v", stale, err)
	}
	if read(t, p, paths.FirewallFile) != acceptTable || read(t, p, paths.FirewallPendingFile) != "" {
		t.Fatal("the boot must load accept, and the pending table must go")
	}
	if _, err := Confirm(context.Background(), f, p, t0.Add(10*time.Second), "cli"); !errors.Is(err, ErrNothingPending) {
		t.Fatalf("confirm after the reboot: %v", err)
	}
	if read(t, p, paths.FirewallFile) != acceptTable {
		t.Fatal("a confirmation after the reboot changed the boot file")
	}
	if _, err := MarkRolledBack(p, t0.Add(20*time.Second), "timeout"); err != nil {
		t.Fatal(err)
	}
	if stale, err := ClearPending(p); err != nil || stale {
		t.Fatalf("a second boot check %v %v", stale, err)
	}
}

// TestNoTimerWithoutTheUnit: without care-pi-firewall.service the table never loads, so no timer runs.
func TestNoTimerWithoutTheUnit(t *testing.T) {
	f, h, p := env(t, false)
	trial(t, p)
	var out bytes.Buffer
	if err := Reload(context.Background(), h, p, Drop, t0, &out); err != nil {
		t.Fatal(err)
	}
	if got := f.Filter("systemd-run", "nft"); len(got) != 0 {
		t.Fatalf("timer %q", got)
	}
	if st := Read(p); st.Policy != "" || st.PendingUntil != nil {
		t.Fatalf("state %+v", st)
	}
	if !strings.Contains(out.String(), "is not active") {
		t.Fatalf("output %q", out.String())
	}
}

// TestTimerFailureLoadsNothing: when systemd-run fails, the drop table does not load.
func TestTimerFailureLoadsNothing(t *testing.T) {
	f, h, p := env(t, true)
	trial(t, p)
	f.On(strings.Join(append([]string{"systemd-run"}, TimerCmd(RollbackDelay).Args...), " "), host.Response{Code: 1})
	if err := Reload(context.Background(), h, p, Drop, t0, &bytes.Buffer{}); err == nil {
		t.Fatal("no error")
	}
	if got := f.Filter("systemctl try-reload-or-restart", "nft"); len(got) != 0 {
		t.Fatalf("loaded %q", got)
	}
}

// TestRolledBack: the record of a rollback.
func TestRolledBack(t *testing.T) {
	f, h, p := env(t, true)
	trial(t, p)
	if err := Reload(context.Background(), h, p, Drop, t0, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	f.Calls = nil
	if err := AcceptNow(context.Background(), f); err != nil {
		t.Fatal(err)
	}
	if got := f.Filter("nft"); len(got) != 1 || got[0] != "nft chain inet care-pi input { policy accept ; }" {
		t.Fatalf("nft %q", got)
	}
	st, err := MarkRolledBack(p, t0.Add(RollbackDelay), "timeout")
	if err != nil || st.Policy != Accept || st.PendingUntil != nil || st.RollbackReason != "timeout" || st.RolledBackAt == nil {
		t.Fatalf("rollback %+v %v", st, err)
	}
}
