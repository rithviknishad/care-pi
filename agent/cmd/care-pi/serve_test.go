package main

import (
	"context"
	"log/slog"
	"net"
	"os"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rithviknishad/care-pi/agent/internal/applock"
	"github.com/rithviknishad/care-pi/agent/internal/apply"
	"github.com/rithviknishad/care-pi/agent/internal/auth"
	"github.com/rithviknishad/care-pi/agent/internal/exitcode"
	"github.com/rithviknishad/care-pi/agent/internal/host"
	"github.com/rithviknishad/care-pi/agent/internal/jobs"
	"github.com/rithviknishad/care-pi/agent/internal/paths"
	"github.com/rithviknishad/care-pi/agent/internal/service"
	"github.com/rithviknishad/care-pi/agent/internal/setup"
	"github.com/rithviknishad/care-pi/agent/internal/update"
)

// TestServeChecksFlagsBeforeListen: a wrong flag never removes the socket of a running agent.
func TestServeChecksFlagsBeforeListen(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	live, err := net.Listen("unix", "api.sock")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = live.Close()
		serveFlags = serveOptions{}
		global.root = "/"
	})
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"serve", "--insecure-dev", "--listen", "unix://api.sock"}, "--insecure-dev needs --root"},
		{[]string{"serve", "--insecure-dev", "--root", dir, "--listen", "tcp://0.0.0.0:8443"}, "loopback"},
		{[]string{"serve", "--root", dir, "--listen", "tcp://127.0.0.1:8443"}, "needs --insecure-dev"},
		{[]string{"serve", "--fake-host", "--listen", "unix://api.sock"}, "need --insecure-dev"},
		{[]string{"serve", "--root", dir, "--listen", "unix://api.sock"}, "another care-pi serve answers on api.sock"},
	} {
		serveFlags, global.root = serveOptions{}, "/"
		out, err := execute(t, tc.args...)
		if exitcode.Code(err) != exitcode.BadInput || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("%v: %v, want %q\n%s", tc.args, err, tc.want, out)
		}
		// The connection must reach the first listener, not a new socket at the same path.
		conn, err := net.Dial("unix", "api.sock")
		if err != nil {
			t.Fatalf("%v removed the live socket: %v", tc.args, err)
		}
		_ = conn.Close()
		if err := live.(*net.UnixListener).SetDeadline(time.Now().Add(2 * time.Second)); err != nil {
			t.Fatal(err)
		}
		got, err := live.Accept()
		if err != nil {
			t.Fatalf("%v replaced the live socket: %v", tc.args, err)
		}
		_ = got.Close()
	}

	// serve checks the flags before Listen: Listen does not touch an old socket either.
	stale, err := net.Listen("unix", "stale.sock")
	if err != nil {
		t.Fatal(err)
	}
	stale.(*net.UnixListener).SetUnlinkOnClose(false)
	_ = stale.Close()
	before, err := os.Stat("stale.sock")
	if err != nil {
		t.Fatal(err)
	}
	serveFlags, global.root = serveOptions{}, "/"
	if _, err := execute(t, "serve", "--insecure-dev", "--listen", "unix://stale.sock"); exitcode.Code(err) != exitcode.BadInput {
		t.Fatalf("no --root: %v", err)
	}
	if after, err := os.Stat("stale.sock"); err != nil || !os.SameFile(before, after) {
		t.Fatalf("serve replaced the old socket before the flag check: %v", err)
	}
}

type imager struct{}

func (imager) Available() bool      { return true }
func (imager) Verify(s string) bool { return s == "imager password" }

// TestBootstrapClaimHint: bootstrap makes the claim code, so the console shows that field first.
func TestBootstrapClaimHint(t *testing.T) {
	p := paths.New(t.TempDir())
	a := auth.New(auth.Options{Paths: p, OS: imager{}})
	if got := a.ClaimMethods(); got[0] != auth.ClaimOSPassword {
		t.Fatalf("before bootstrap: %v", got)
	}
	code, err := bootstrapClaimCode(a, true)
	if err != nil || len(code) != 14 {
		t.Fatalf("claim code %q: %v", code, err)
	}
	if got := a.ClaimMethods(); len(got) != 2 || got[0] != auth.ClaimCode {
		t.Fatalf("after bootstrap: %v", got)
	}
	// A second bootstrap keeps the code.
	if again, err := bootstrapClaimCode(a, true); err != nil || again != code {
		t.Fatalf("second bootstrap: %q %v", again, err)
	}
	if _, err := os.Stat(p.Abs(auth.ClaimHintFile)); err != nil {
		t.Fatal(err)
	}
}

// TestRecoverUpdatesEachTick: serve looks for an interrupted update at each tick (a fake clock), not only at its
// start. A timer job of the CLI that stopped inside its cutover before activate gets the previous tree started
// again and the maintenance page turned off, one time.
func TestRecoverUpdatesEachTick(t *testing.T) {
	p := paths.New(t.TempDir())
	fake := host.NewFake()
	svc := &service.Service{Paths: p, Host: host.New(fake), Jobs: &jobs.Manager{Paths: p}, Setup: &setup.Store{Paths: p}}
	var calls atomic.Int32
	check := func(ctx context.Context) (*jobs.Job, error) {
		calls.Add(1)
		return svc.RecoverUpdates(ctx)
	}
	tick := make(chan time.Time)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		recoverUpdates(ctx, tick, check, slog.New(slog.DiscardHandler))
		close(done)
	}()
	tick <- time.Now()
	if err := update.UpdateState(p, func(s *update.State) {
		s.Cutover = &update.CutoverRecord{JobID: "20261122T020300.000Z-update-apply", Kind: jobs.KindUpdateApply, Step: "stop"}
	}); err != nil {
		t.Fatal(err)
	}
	if err := apply.MaintenanceOn(p, apply.MaintenanceInfo{JobID: "20261122T020300.000Z-update-apply", Kind: jobs.KindUpdateApply}); err != nil {
		t.Fatal(err)
	}
	// While a process holds the apply lock, its cutover is not interrupted.
	release, err := applock.Acquire(context.Background(), p, time.Second, nil)
	if err != nil {
		t.Fatal(err)
	}
	tick <- time.Now()
	tick <- time.Now()
	if got := fake.Filter("systemctl start"); len(got) != 0 {
		t.Fatalf("a recovery under the apply lock: %q", got)
	}
	release()
	tick <- time.Now()
	deadline := time.Now().Add(5 * time.Second)
	for update.ReadState(p).Cutover != nil {
		if time.Now().After(deadline) {
			t.Fatal("no recovery after the tick")
		}
		time.Sleep(10 * time.Millisecond)
	}
	tick <- time.Now()
	cancel()
	<-done
	if got := fake.Filter("systemctl start"); !reflect.DeepEqual(got, []string{"systemctl start care.target"}) {
		t.Fatalf("recovery commands %q", got)
	}
	if _, on := apply.ReadMaintenance(p); on {
		t.Fatal("the maintenance page stays on")
	}
	if n := calls.Load(); n != 5 {
		t.Fatalf("%d checks for 5 ticks", n)
	}
}
