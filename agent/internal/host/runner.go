// Package host is the boundary between care-pi and the host.
//
// Every command that care-pi runs goes through a Runner. The typed clients
// (Systemd, Podman, Skopeo, Clock) build the command lines. Tests use a Fake
// runner that records each command line and returns scripted output.
package host

import (
	"bufio"
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

// Streamer runs a command and gives each line of its output to fn while it runs.
// "care-pi serve" uses it to follow the journal.
type Streamer interface {
	Stream(ctx context.Context, c Cmd, fn func(line []byte) error) error
}

// Stream implements Streamer. It stops the command when ctx ends or fn returns an error.
func (e Exec) Stream(ctx context.Context, c Cmd, fn func(line []byte) error) error {
	if e.Log != nil {
		_, _ = fmt.Fprintln(e.Log, "+", c.String())
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	cmd := exec.CommandContext(ctx, c.Name, c.Args...)
	if len(c.Env) > 0 {
		cmd.Env = append(os.Environ(), c.Env...)
	}
	cmd.Stdin = c.Stdin
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("cannot run %s: %w", c.Name, err)
	}
	sc := bufio.NewScanner(out)
	sc.Buffer(make([]byte, 64*1024), 4*1024*1024)
	var fnErr error
	for sc.Scan() {
		if fnErr = fn(sc.Bytes()); fnErr != nil {
			cancel()
			break
		}
	}
	err = cmd.Wait()
	if fnErr != nil {
		return fnErr
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err != nil {
		var xe *exec.ExitError
		if errors.As(err, &xe) {
			return &ExitError{Cmd: c.String(), Code: xe.ExitCode(), Stderr: stderr.String()}
		}
		return err
	}
	return nil
}

// Transient runs "podman pull" in a transient service unit (systemd-run --wait --pipe), "podman exec"
// in the mount namespace of PID 1, and each other command like Exec. "care-pi serve" uses it:
//   - An image pull needs more memory than the MemoryMax of care-pi.service. A transient unit has its
//     own cgroup and no sandbox of the agent.
//   - The sandbox of care-pi.service (ProtectSystem=strict) gives the agent its own mount namespace.
//     podman mounts the root file system of a container that starts after the agent only in the host
//     namespace (containers/storage keeps its overlay mounts private). "podman exec" reads the passwd
//     file of the container there for a user name ("--user postgres", or the image user "django"),
//     so in the agent namespace it fails with "unable to find user". nsenter runs it in the host namespace.
type Transient struct {
	Exec
	// HostMountNS reports whether the agent runs in the mount namespace of PID 1.
	// Nil: compare /proc/self/ns/mnt with /proc/1/ns/mnt.
	HostMountNS func() bool
}

// Run implements Runner.
func (t Transient) Run(ctx context.Context, c Cmd) ([]byte, error) {
	return t.Exec.Run(ctx, t.Wrap(c))
}

// Wrap returns the command that Run runs for c.
func (t Transient) Wrap(c Cmd) Cmd {
	if c.Name != "podman" || len(c.Args) == 0 {
		return c
	}
	switch c.Args[0] {
	case "pull":
		args := []string{"--quiet", "--wait", "--pipe", "--collect", "--description=care-pi image pull", "--", "podman"}
		return Cmd{Name: "systemd-run", Args: append(args, c.Args...), Stdin: c.Stdin, Env: c.Env, Attach: c.Attach}
	case "exec":
		inHost := t.HostMountNS
		if inHost == nil {
			inHost = sameMountNS
		}
		if !inHost() {
			args := []string{"--target", "1", "--mount", "--", "podman"}
			return Cmd{Name: "nsenter", Args: append(args, c.Args...), Stdin: c.Stdin, Env: c.Env, Attach: c.Attach}
		}
	}
	return c
}

// sameMountNS reports whether this process runs in the mount namespace of PID 1.
// When it cannot read the links (not root), it reports true: then nsenter cannot help.
func sameMountNS() bool {
	self, err := os.Readlink("/proc/self/ns/mnt")
	if err != nil {
		return true
	}
	init, err := os.Readlink("/proc/1/ns/mnt")
	if err != nil {
		return true
	}
	return self == init
}
