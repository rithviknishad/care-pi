package firewall

import (
	"bytes"
	"context"
	"errors"
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

// TestDropArmsTheTimerFirst: a switch from accept to drop starts the rollback timer before the load of the table.
func TestDropArmsTheTimerFirst(t *testing.T) {
	f, h, p := env(t, true)
	var out bytes.Buffer
	if err := Reload(context.Background(), h, p, Drop, t0, &out); err != nil {
		t.Fatal(err)
	}
	calls := f.Filter("systemd-run", "systemctl try-reload-or-restart", "systemctl stop")
	if len(calls) != 3 || calls[0] != "systemctl stop care-pi-firewall-rollback.timer" ||
		!strings.HasPrefix(calls[1], "systemd-run --quiet --collect --unit=care-pi-firewall-rollback --on-active=90s") ||
		!strings.HasSuffix(calls[1], "-- /usr/local/bin/care-pi firewall rollback --reason timeout") ||
		calls[2] != "systemctl try-reload-or-restart care-pi-firewall.service" {
		t.Fatalf("calls %q", calls)
	}
	st := Read(p)
	if st.Policy != Drop || st.PendingUntil == nil || !st.PendingUntil.Equal(t0.Add(RollbackDelay)) {
		t.Fatalf("state %+v", st)
	}
	if !strings.Contains(out.String(), "care-pi firewall confirm") {
		t.Fatalf("output %q", out.String())
	}
	// A second load of the same policy (for example a new Tailscale rule) starts no second timer.
	f.Calls = nil
	if err := Reload(context.Background(), h, p, Drop, t0.Add(time.Minute), &out); err != nil {
		t.Fatal(err)
	}
	if got := f.Filter("systemd-run"); len(got) != 0 {
		t.Fatalf("second timer %q", got)
	}

	// Confirm stops the timer; a second confirm has nothing to do.
	st, err := Confirm(context.Background(), f, p, t0.Add(30*time.Second), "cli")
	if err != nil || st.PendingUntil != nil || st.ConfirmedAt == nil || st.ConfirmedBy != "cli" {
		t.Fatalf("confirm %+v %v", st, err)
	}
	if _, err := Confirm(context.Background(), f, p, t0, "cli"); !errors.Is(err, ErrNothingPending) {
		t.Fatalf("second confirm %v", err)
	}
	// Back to accept: no timer, and the state follows.
	f.Calls = nil
	if err := Reload(context.Background(), h, p, Accept, t0.Add(time.Hour), &out); err != nil {
		t.Fatal(err)
	}
	if got := f.Filter("systemd-run"); len(got) != 0 || Read(p).Policy != Accept {
		t.Fatalf("accept %q %+v", got, Read(p))
	}
}

// TestNoTimerWithoutTheUnit: without care-pi-firewall.service the table never loads, so no timer runs.
func TestNoTimerWithoutTheUnit(t *testing.T) {
	f, h, p := env(t, false)
	var out bytes.Buffer
	if err := Reload(context.Background(), h, p, Drop, t0, &out); err != nil {
		t.Fatal(err)
	}
	if got := f.Filter("systemd-run"); len(got) != 0 {
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
	f.On(strings.Join(append([]string{"systemd-run"}, TimerCmd(RollbackDelay).Args...), " "), host.Response{Code: 1})
	if err := Reload(context.Background(), h, p, Drop, t0, &bytes.Buffer{}); err == nil {
		t.Fatal("no error")
	}
	if got := f.Filter("systemctl try-reload-or-restart"); len(got) != 0 {
		t.Fatalf("loaded %q", got)
	}
}

// TestRolledBack: the record of a rollback.
func TestRolledBack(t *testing.T) {
	f, h, p := env(t, true)
	if err := Reload(context.Background(), h, p, Drop, t0, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
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
