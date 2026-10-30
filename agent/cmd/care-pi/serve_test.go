package main

import (
	"net"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/rithviknishad/care-pi/agent/internal/auth"
	"github.com/rithviknishad/care-pi/agent/internal/exitcode"
	"github.com/rithviknishad/care-pi/agent/internal/paths"
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
	code, err := bootstrapClaimCode(a)
	if err != nil || len(code) != 14 {
		t.Fatalf("claim code %q: %v", code, err)
	}
	if got := a.ClaimMethods(); len(got) != 2 || got[0] != auth.ClaimCode {
		t.Fatalf("after bootstrap: %v", got)
	}
	// A second bootstrap keeps the code.
	if again, err := bootstrapClaimCode(a); err != nil || again != code {
		t.Fatalf("second bootstrap: %q %v", again, err)
	}
	if _, err := os.Stat(p.Abs(auth.ClaimHintFile)); err != nil {
		t.Fatal(err)
	}
}
