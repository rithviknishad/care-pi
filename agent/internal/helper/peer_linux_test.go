//go:build linux

package helper

import (
	"context"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rithviknishad/care-pi/agent/internal/host"
)

// TestPeerRefused: a peer that is not root and not the allowed user gets a denial and no command runs.
func TestPeerRefused(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root is always allowed")
	}
	dir := t.TempDir()
	sock := filepath.Join(dir, "h.sock")
	l, err := Listener(sock)
	if err != nil {
		t.Fatal(err)
	}
	f := host.NewFake()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	srv := &Server{Exec: f, Log: slog.New(slog.NewTextHandler(os.Stderr, nil)), Allowed: PeerCheckUID(-1)}
	go func() { _ = srv.Serve(ctx, l) }()
	conn, err := net.DialTimeout("unix", sock, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, 512)
	n, _ := conn.Read(buf)
	if got := string(buf[:n]); !strings.Contains(got, `"denied"`) {
		t.Fatalf("expected a denial, got %q", got)
	}
	if calls := f.Filter(""); len(calls) != 0 {
		t.Fatalf("a refused peer must run nothing, got %v", calls)
	}
}
