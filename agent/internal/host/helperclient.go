package host

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"time"
)

// The protocol of the privileged helper (Phase 7, docs/console-server.md section 14): one request for each
// connection on the unix socket /run/care-pi-helper/helper.sock, as one JSON line; the helper answers with JSON lines (a
// line of the output for each "line" frame of a stream, then one "done" frame). The client keeps the connection
// open while the command runs: a close of the connection stops the command.

// HelperRequest is one command for the helper.
type HelperRequest struct {
	Name  string   `json:"name"`
	Args  []string `json:"args"`
	Env   []string `json:"env,omitempty"`
	Stdin []byte   `json:"stdin,omitempty"`
	// Stream sends each line of the output in a frame (journalctl --follow).
	Stream bool `json:"stream,omitempty"`
}

// HelperFrame is one answer line of the helper.
type HelperFrame struct {
	Line   *string `json:"line,omitempty"`
	Done   bool    `json:"done,omitempty"`
	Stdout []byte  `json:"stdout,omitempty"`
	Code   int     `json:"code,omitempty"`
	Stderr string  `json:"stderr,omitempty"`
	// Denied is the reason when the allow-list of the helper refuses the command.
	Denied string `json:"denied,omitempty"`
	Error  string `json:"error,omitempty"`
}

// ErrHelperDenied is the error of a command that the helper refuses.
var ErrHelperDenied = errors.New("the helper refused the command")

// HelperClient runs each command through the privileged helper. It implements Runner and Streamer, so
// New(NewHelperClient(socket)) is a Host whose daemon needs no privileges for the commands.
type HelperClient struct {
	Socket string
	// DialTimeout bounds the connect (default 5 s): systemd starts the helper on the first connect.
	DialTimeout time.Duration
}

// NewHelperClient returns a client of the helper socket.
func NewHelperClient(socket string) *HelperClient { return &HelperClient{Socket: socket} }

// helperConn is the connection of one request. close stops the watch of the context and closes the connection.
type helperConn struct {
	conn net.Conn
	r    *bufio.Reader
	stop func() bool
}

func (hc *helperConn) close() {
	hc.stop()
	_ = hc.conn.Close()
}

func (h *HelperClient) request(ctx context.Context, c Cmd, stream bool) (*helperConn, error) {
	if c.Attach {
		return nil, fmt.Errorf("%w: a terminal command (%s) runs only in the CLI", ErrHelperDenied, c.Name)
	}
	req := HelperRequest{Name: c.Name, Args: c.Args, Env: c.Env, Stream: stream}
	if c.Stdin != nil {
		data, err := io.ReadAll(c.Stdin)
		if err != nil {
			return nil, err
		}
		req.Stdin = data
	}
	t := h.DialTimeout
	if t == 0 {
		t = 5 * time.Second
	}
	d := net.Dialer{Timeout: t}
	conn, err := d.DialContext(ctx, "unix", h.Socket)
	if err != nil {
		return nil, fmt.Errorf("cannot reach the helper (%s): %w", h.Socket, err)
	}
	hc := &helperConn{conn: conn, r: bufio.NewReaderSize(conn, 64*1024), stop: context.AfterFunc(ctx, func() { _ = conn.Close() })}
	line, _ := json.Marshal(req)
	if _, err := conn.Write(append(line, '\n')); err != nil {
		hc.close()
		return nil, err
	}
	return hc, nil
}

func readFrame(r *bufio.Reader) (*HelperFrame, error) {
	line, err := r.ReadBytes('\n')
	if err != nil {
		if len(line) == 0 {
			return nil, err
		}
	}
	var f HelperFrame
	if err := json.Unmarshal(bytes.TrimSpace(line), &f); err != nil {
		return nil, fmt.Errorf("helper: a bad answer: %w", err)
	}
	return &f, nil
}

func frameError(c Cmd, f *HelperFrame) error {
	switch {
	case f.Denied != "":
		return fmt.Errorf("%w: %s: %s", ErrHelperDenied, c.String(), f.Denied)
	case f.Code != 0:
		return &ExitError{Cmd: c.String(), Code: f.Code, Stderr: f.Stderr}
	case f.Error != "":
		return fmt.Errorf("helper: %s: %s", c.Name, f.Error)
	}
	return nil
}

// Run implements Runner.
func (h *HelperClient) Run(ctx context.Context, c Cmd) ([]byte, error) {
	hc, err := h.request(ctx, c, false)
	if err != nil {
		return nil, err
	}
	defer hc.close()
	for {
		f, err := readFrame(hc.r)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			return nil, fmt.Errorf("helper: %s: %w", c.Name, err)
		}
		if f.Done || f.Denied != "" {
			return f.Stdout, frameError(c, f)
		}
	}
}

// Stream implements Streamer.
func (h *HelperClient) Stream(ctx context.Context, c Cmd, fn func(line []byte) error) error {
	hc, err := h.request(ctx, c, true)
	if err != nil {
		return err
	}
	defer hc.close()
	for {
		f, err := readFrame(hc.r)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return fmt.Errorf("helper: %s: %w", c.Name, err)
		}
		if f.Line != nil {
			if err := fn([]byte(*f.Line)); err != nil {
				return err
			}
			continue
		}
		if f.Done || f.Denied != "" {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return frameError(c, f)
		}
	}
}
