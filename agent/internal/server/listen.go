package server

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/rithviknishad/care-pi/agent/internal/paths"
)

// CheckListen checks spec without a change on the host: "unix://PATH", or "tcp://HOST:PORT"
// with dev mode and a loopback address. An empty spec is the agent socket.
func CheckListen(spec string, dev bool) error {
	switch {
	case spec == "", strings.HasPrefix(spec, "unix://"):
		return nil
	case strings.HasPrefix(spec, "tcp://"):
		addr := strings.TrimPrefix(spec, "tcp://")
		if !dev {
			return errors.New("a TCP listener needs --insecure-dev; the agent listens only on its unix socket")
		}
		host, _, err := net.SplitHostPort(addr)
		if err != nil {
			return fmt.Errorf("the address %s is not HOST:PORT: %w", addr, err)
		}
		ip := net.ParseIP(host)
		if host != "localhost" && (ip == nil || !ip.IsLoopback()) {
			return fmt.Errorf("--insecure-dev listens only on a loopback address, not on %s", host)
		}
		return nil
	}
	return fmt.Errorf("the listen address %q must start with unix:// or tcp://", spec)
}

// Listen opens the listener of spec: "unix://PATH" (the default is the agent socket below root)
// or "tcp://HOST:PORT". A TCP listener needs dev mode and a loopback address.
// Listen does not remove a socket that a server answers on.
func Listen(spec string, root paths.Paths, dev bool) (net.Listener, error) {
	if err := CheckListen(spec, dev); err != nil {
		return nil, err
	}
	if spec == "" {
		spec = "unix://" + root.Abs(paths.AgentSocket)
	}
	if !strings.HasPrefix(spec, "unix://") {
		return net.Listen("tcp", strings.TrimPrefix(spec, "tcp://"))
	}
	path := strings.TrimPrefix(spec, "unix://")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("cannot create the directory of %s: %w", path, err)
	}
	if st, err := os.Lstat(path); err == nil {
		if st.Mode()&os.ModeSocket == 0 {
			return nil, fmt.Errorf("%s exists and is not a socket", path)
		}
		if conn, err := net.DialTimeout("unix", path, 2*time.Second); err == nil {
			_ = conn.Close()
			return nil, fmt.Errorf("another care-pi serve answers on %s; stop it first (sudo systemctl stop care-pi.service)", path)
		}
		if err := os.Remove(path); err != nil {
			return nil, fmt.Errorf("cannot remove the old socket %s: %w", path, err)
		}
	}
	l, err := net.Listen("unix", path)
	if err != nil {
		return nil, err
	}
	// Caddy runs as root in its container, so 0660 root:root is enough.
	if err := os.Chmod(path, 0o660); err != nil {
		_ = l.Close()
		return nil, err
	}
	return l, nil
}

// Notify sends a state to systemd (sd_notify) when NOTIFY_SOCKET is set. Type=notify needs READY=1.
func Notify(state string) error {
	addr := os.Getenv("NOTIFY_SOCKET")
	if addr == "" {
		return nil
	}
	if strings.HasPrefix(addr, "@") {
		addr = "\x00" + addr[1:]
	}
	conn, err := net.DialUnix("unixgram", nil, &net.UnixAddr{Name: addr, Net: "unixgram"})
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()
	_, err = conn.Write([]byte(state))
	return err
}

// Serve runs the HTTP server on l until ctx ends, then shuts it down within 10 s.
func (s *Server) Serve(ctx context.Context, l net.Listener) error {
	// No ReadTimeout: net/http also applies it to its background read during the handler,
	// and that timeout cancels the event streams. ServeHTTP sets a read deadline for each body.
	hs := &http.Server{Handler: s, ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 2 * time.Minute,
		MaxHeaderBytes: 64 << 10,
		BaseContext:    func(net.Listener) context.Context { return context.WithoutCancel(ctx) }}
	errc := make(chan error, 1)
	go func() { errc <- hs.Serve(l) }()
	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	s.Close()
	sctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	err := hs.Shutdown(sctx)
	if errors.Is(err, context.DeadlineExceeded) {
		err = hs.Close()
	}
	return err
}
