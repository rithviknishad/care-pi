// Package host is the boundary between care-pi and the host.
//
// Every command that care-pi runs goes through a Runner. The typed clients
// (Systemd, Podman, Skopeo, Clock) build the command lines. Tests use a Fake
// runner that records each command line and returns scripted output.
package host

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
)

// Cmd is one command to run.
type Cmd struct {
	Name string
	Args []string
	// Stdin is the input of the command. The Fake does not record it, so it can carry secrets.
	Stdin io.Reader
	// Env adds variables to the environment of the command.
	Env []string
	// Attach connects the command to the terminal of care-pi (logs, interactive exec).
	Attach bool
}

// String returns the command line.
func (c Cmd) String() string {
	return strings.TrimSpace(c.Name + " " + strings.Join(c.Args, " "))
}

// Runner runs commands.
type Runner interface {
	Run(ctx context.Context, c Cmd) ([]byte, error)
}

// ExitError carries the exit code of a command.
type ExitError struct {
	Cmd    string
	Code   int
	Stderr string
}

func (e *ExitError) Error() string {
	msg := fmt.Sprintf("%s: exit code %d", e.Cmd, e.Code)
	if s := strings.TrimSpace(e.Stderr); s != "" {
		msg += ": " + s
	}
	return msg
}

// Exec runs commands on the host.
type Exec struct {
	// Log receives each command line before it runs. It can be nil.
	Log io.Writer
}

// Run implements Runner.
func (e Exec) Run(ctx context.Context, c Cmd) ([]byte, error) {
	if e.Log != nil {
		_, _ = fmt.Fprintln(e.Log, "+", c.String())
	}
	cmd := exec.CommandContext(ctx, c.Name, c.Args...)
	if len(c.Env) > 0 {
		cmd.Env = append(os.Environ(), c.Env...)
	}
	var stdout, stderr bytes.Buffer
	if c.Attach {
		cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
		if c.Stdin != nil {
			cmd.Stdin = c.Stdin
		}
	} else {
		cmd.Stdin = c.Stdin
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
	}
	err := cmd.Run()
	if err != nil {
		var xe *exec.ExitError
		if errors.As(err, &xe) {
			return stdout.Bytes(), &ExitError{Cmd: c.String(), Code: xe.ExitCode(), Stderr: stderr.String()}
		}
		return stdout.Bytes(), fmt.Errorf("cannot run %s: %w", c.Name, err)
	}
	return stdout.Bytes(), nil
}
