package helper

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/rithviknishad/care-pi/agent/internal/host"
)

// Executor runs the allowed commands: host.Transient in "care-pi helper" (the pulls, restic, and the disk commands in
// transient units, podman exec in the mount namespace of PID 1), a fake in the tests.
type Executor interface {
	host.Runner
	host.Streamer
}

// Server answers the requests of the agent on the helper socket.
type Server struct {
	Exec Executor
	Log  *slog.Logger
	// Allowed checks the peer of a connection (the uid of SO_PEERCRED). nil allows each peer: the socket mode
	// (0660 root:care-pi) is the access control.
	Allowed func(conn net.Conn) (string, bool)
	// MaxRequest bounds the request line (default 4 MiB: a stdin with a keyfile or a script).
	MaxRequest int
	// Denied, when it is set, gets each refused command (a test fails on it).
	Denied func(c host.Cmd, err error)
	// Root is the root of the paths of the agent in a test (paths.Paths.Root): the validation sees the host paths.
	// Empty or "/" on a host.
	Root string
}

// hostView returns the arguments with the test root removed, for the validation.
func (s *Server) hostView(c host.Cmd) host.Cmd {
	if s.Root == "" || s.Root == "/" {
		return c
	}
	v := c
	v.Args = make([]string, len(c.Args))
	for i, a := range c.Args {
		v.Args[i] = strings.ReplaceAll(a, s.Root, "")
	}
	return v
}

// Serve accepts connections until ctx ends.
func (s *Server) Serve(ctx context.Context, l net.Listener) error {
	go func() {
		<-ctx.Done()
		_ = l.Close()
	}()
	var wg sync.WaitGroup
	defer wg.Wait()
	for {
		conn, err := l.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			var ne net.Error
			if errors.As(err, &ne) && ne.Timeout() {
				time.Sleep(50 * time.Millisecond)
				continue
			}
			return err
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.handle(ctx, conn)
		}()
	}
}

func (s *Server) send(conn net.Conn, f host.HelperFrame) error {
	data, _ := json.Marshal(f)
	_, err := conn.Write(append(data, '\n'))
	return err
}

func (s *Server) handle(ctx context.Context, conn net.Conn) {
	defer func() { _ = conn.Close() }()
	peer := "unix"
	if s.Allowed != nil {
		var ok bool
		if peer, ok = s.Allowed(conn); !ok {
			s.Log.Warn("helper: refused peer", "peer", peer)
			_ = s.send(conn, host.HelperFrame{Denied: "the peer is not root or the care-pi user"})
			return
		}
	}
	max := s.MaxRequest
	if max == 0 {
		max = 4 << 20
	}
	r := bufio.NewReaderSize(conn, 64*1024)
	line, err := readLine(r, max)
	if err != nil {
		return
	}
	var req host.HelperRequest
	if err := json.Unmarshal(line, &req); err != nil {
		_ = s.send(conn, host.HelperFrame{Done: true, Error: "a bad request"})
		return
	}
	c := host.Cmd{Name: req.Name, Args: req.Args, Env: req.Env}
	if err := Validate(s.hostView(c)); err != nil {
		s.Log.Warn("helper: denied", "peer", peer, "cmd", Describe(c), "reason", err.Error())
		if s.Denied != nil {
			s.Denied(c, err)
		}
		_ = s.send(conn, host.HelperFrame{Denied: err.Error()})
		return
	}
	if req.Stdin != nil {
		c.Stdin = bytes.NewReader(req.Stdin)
	}
	s.Log.Info("helper: run", "peer", peer, "cmd", Describe(c), "stream", req.Stream, "stdin_bytes", len(req.Stdin))
	// The client keeps the connection open while the command runs; its close (or EOF) stops the command.
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() {
		_, _ = io.Copy(io.Discard, r)
		cancel()
	}()
	start := time.Now()
	if req.Stream {
		err = s.Exec.Stream(ctx, c, func(l []byte) error {
			text := string(l)
			return s.send(conn, host.HelperFrame{Line: &text})
		})
		f := host.HelperFrame{Done: true}
		fill(&f, err)
		_ = s.send(conn, f)
	} else {
		out, err := s.Exec.Run(ctx, c)
		f := host.HelperFrame{Done: true, Stdout: out}
		fill(&f, err)
		_ = s.send(conn, f)
	}
	s.Log.Info("helper: done", "cmd", c.Name, "ms", time.Since(start).Milliseconds())
}

func fill(f *host.HelperFrame, err error) {
	if err == nil {
		return
	}
	var xe *host.ExitError
	if errors.As(err, &xe) {
		f.Code, f.Stderr = xe.Code, xe.Stderr
		return
	}
	f.Error = err.Error()
}

func readLine(r *bufio.Reader, max int) ([]byte, error) {
	var b []byte
	for {
		chunk, isPrefix, err := r.ReadLine()
		if err != nil {
			return nil, err
		}
		b = append(b, chunk...)
		if len(b) > max {
			return nil, errors.New("request too large")
		}
		if !isPrefix {
			return b, nil
		}
	}
}
