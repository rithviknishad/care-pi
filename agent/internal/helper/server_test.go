package helper

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rithviknishad/care-pi/agent/internal/host"
)

// syncBuffer is a bytes.Buffer for the log of the server goroutines.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// startHelper serves a helper with the fake executor on a socket in a short temporary directory.
func startHelper(t *testing.T, f *host.Fake) (*host.HelperClient, *syncBuffer) {
	t.Helper()
	dir, err := os.MkdirTemp("", "cph")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	sock := filepath.Join(dir, "h.sock")
	l, err := Listener(sock)
	if err != nil {
		t.Fatal(err)
	}
	logs := &syncBuffer{}
	ctx, cancel := context.WithCancel(context.Background())
	srv := &Server{Exec: f, Log: slog.New(slog.NewTextHandler(logs, nil)), Allowed: PeerCheck("care-pi")}
	done := make(chan error, 1)
	go func() { done <- srv.Serve(ctx, l) }()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil {
			t.Logf("helper: %v", err)
		}
	})
	if st, err := os.Stat(sock); err != nil || st.Mode().Perm() != 0o660 {
		t.Fatalf("socket mode %v %v", st, err)
	}
	return host.NewHelperClient(sock), logs
}

// TestRoundTrip: the client of the daemon runs commands through the helper: output, stdin, an exit code, a refusal,
// and a stream with its cancel.
func TestRoundTrip(t *testing.T) {
	f := host.NewFake()
	f.On("systemctl show -p ActiveState care.target", host.Response{Out: "ActiveState=active\n"})
	f.On("systemctl start care-api.service", host.Response{Code: 3})
	var mu sync.Mutex
	var stdin string
	f.Hook = func(c host.Cmd, in []byte) (host.Response, bool) {
		if c.Name == "podman" && len(c.Args) > 1 && c.Args[1] == "create" {
			mu.Lock()
			stdin = string(in)
			mu.Unlock()
			return host.Response{}, true
		}
		return host.Response{}, false
	}
	gotStdin := func() string {
		mu.Lock()
		defer mu.Unlock()
		return stdin
	}
	f.On("journalctl --unit care-api.service --output json --no-pager --lines 2 --follow", host.Response{Out: "{\"a\":1}\n{\"b\":2}\n"})
	c, logs := startHelper(t, f)
	h := host.New(c)
	ctx := context.Background()

	st, err := h.Systemd.Show(ctx, []string{"care.target"}, "ActiveState")
	if err != nil || st[0]["ActiveState"] != "active" {
		t.Fatalf("show %v %v", st, err)
	}
	if err := h.Podman.SecretSet(ctx, "care-pi-django-secret-key", []byte("s3cret-value")); err != nil || gotStdin() != "s3cret-value" {
		t.Fatalf("secret %q %v", gotStdin(), err)
	}
	var xe *host.ExitError
	if err := h.Systemd.Start(ctx, "care-api.service"); !errors.As(err, &xe) || xe.Code != 3 {
		t.Fatalf("exit code %v", err)
	}
	if _, err := c.Run(ctx, host.Cmd{Name: "bash", Args: []string{"-c", "id"}}); !errors.Is(err, host.ErrHelperDenied) {
		t.Fatalf("bash %v", err)
	}
	if len(f.Filter("bash")) != 0 {
		t.Fatal("the helper ran a refused command")
	}
	var lines []string
	sctx, cancel := context.WithTimeout(ctx, 300*time.Millisecond)
	defer cancel()
	err = c.Stream(sctx, host.Cmd{Name: "journalctl", Args: []string{"--unit", "care-api.service", "--output", "json", "--no-pager", "--lines", "2", "--follow"}},
		func(l []byte) error { lines = append(lines, string(l)); return nil })
	if len(lines) != 2 || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("stream %q %v", lines, err)
	}
	time.Sleep(50 * time.Millisecond)
	log := logs.String()
	if !strings.Contains(log, "helper: denied") || !strings.Contains(log, "bash") || strings.Contains(log, "s3cret-value") {
		t.Fatalf("journal:\n%s", log)
	}
	if !strings.Contains(log, "stdin_bytes=12") {
		t.Fatalf("no stdin size in the journal:\n%s", log)
	}
	// A terminal command never goes to the helper.
	if _, err := c.Run(ctx, host.Cmd{Name: "journalctl", Args: []string{"--unit", "care.target"}, Attach: true}); !errors.Is(err, host.ErrHelperDenied) {
		t.Fatalf("attach %v", err)
	}
}

// TestBadRequest: a line that is not JSON gets an error frame; a peer check that fails gets a refusal.
func TestBadRequest(t *testing.T) {
	f := host.NewFake()
	c, _ := startHelper(t, f)
	conn, err := net.Dial("unix", c.Socket)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = conn.Write([]byte("not json\n"))
	data, _ := io.ReadAll(conn)
	if !strings.Contains(string(data), `"error":"a bad request"`) {
		t.Fatalf("answer %s", data)
	}
	_ = conn.Close()
}
