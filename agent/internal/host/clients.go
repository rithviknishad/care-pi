package host

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
)

// Systemd controls systemd units.
type Systemd interface {
	DaemonReload(ctx context.Context) error
	Enable(ctx context.Context, units ...string) error
	Disable(ctx context.Context, units ...string) error
	Start(ctx context.Context, units ...string) error
	Stop(ctx context.Context, units ...string) error
	Restart(ctx context.Context, units ...string) error
	// TryRestart restarts the units that run now. It does not start a stopped unit.
	TryRestart(ctx context.Context, units ...string) error
	ResetFailed(ctx context.Context, units ...string) error
	// Show returns the properties of each unit, in the order of units.
	Show(ctx context.Context, units []string, props ...string) ([]map[string]string, error)
}

// Podman runs podman commands.
type Podman interface {
	Version(ctx context.Context) (string, error)
	SecretNames(ctx context.Context) (map[string]bool, error)
	// SecretSet creates or replaces a podman secret. The value goes through stdin.
	SecretSet(ctx context.Context, name string, value []byte) error
	ImageExists(ctx context.Context, ref string) (bool, error)
	Pull(ctx context.Context, ref string) error
	// Exec runs a command in a container and returns its output.
	Exec(ctx context.Context, container, user string, args ...string) ([]byte, error)
	// ExecAttached runs a command in a container on the terminal of care-pi.
	ExecAttached(ctx context.Context, container string, tty bool, env []string, stdin io.Reader, args ...string) error
	// Health returns the health text of each container in the pod, for example "healthy".
	Health(ctx context.Context, pod string) (map[string]string, error)
}

// Skopeo inspects remote images.
type Skopeo interface {
	// Digest returns the manifest digest of ref, for example "sha256:...".
	Digest(ctx context.Context, ref, os, arch string) (string, error)
}

// Journal shows unit logs.
type Journal interface {
	Show(ctx context.Context, unit string, follow bool, lines int) error
}

// Clock reads the time synchronisation state.
type Clock interface {
	NTPSynchronized(ctx context.Context) (bool, error)
}

// Host groups the clients that care-pi uses.
type Host struct {
	Runner  Runner
	Systemd Systemd
	Podman  Podman
	Skopeo  Skopeo
	Journal Journal
	Clock   Clock
	Git     Git
}

// New returns the exec-based clients on top of r. The Git client uses HTTPS directly.
func New(r Runner) *Host {
	return &Host{
		Runner:  r,
		Systemd: ExecSystemd{R: r},
		Podman:  ExecPodman{R: r},
		Skopeo:  ExecSkopeo{R: r},
		Journal: ExecJournal{R: r},
		Clock:   ExecClock{R: r},
		Git:     HTTPGit{},
	}
}

// ExecSystemd runs systemctl.
type ExecSystemd struct{ R Runner }

func (s ExecSystemd) run(ctx context.Context, args ...string) error {
	_, err := s.R.Run(ctx, Cmd{Name: "systemctl", Args: args})
	return err
}

// DaemonReload implements Systemd.
func (s ExecSystemd) DaemonReload(ctx context.Context) error { return s.run(ctx, "daemon-reload") }

// Enable implements Systemd.
func (s ExecSystemd) Enable(ctx context.Context, units ...string) error {
	return s.run(ctx, append([]string{"enable"}, units...)...)
}

// Disable implements Systemd.
func (s ExecSystemd) Disable(ctx context.Context, units ...string) error {
	return s.run(ctx, append([]string{"disable"}, units...)...)
}

// Start implements Systemd.
func (s ExecSystemd) Start(ctx context.Context, units ...string) error {
	return s.run(ctx, append([]string{"start"}, units...)...)
}

// Stop implements Systemd.
func (s ExecSystemd) Stop(ctx context.Context, units ...string) error {
	return s.run(ctx, append([]string{"stop"}, units...)...)
}

// Restart implements Systemd.
func (s ExecSystemd) Restart(ctx context.Context, units ...string) error {
	return s.run(ctx, append([]string{"restart"}, units...)...)
}

// TryRestart implements Systemd.
func (s ExecSystemd) TryRestart(ctx context.Context, units ...string) error {
	return s.run(ctx, append([]string{"try-restart"}, units...)...)
}

// ResetFailed implements Systemd.
// A unit that systemd has not loaded has no failed state. For such a unit, systemctl prints
// "Unit X not loaded." and exits with code 1, but it resets the other units. This occurs at
// the first apply: daemon-reload makes the quadlet units, but nothing loads them before the
// start of care.target (wave 3 on lumine). ResetFailed ignores this error.
func (s ExecSystemd) ResetFailed(ctx context.Context, units ...string) error {
	err := s.run(ctx, append([]string{"reset-failed"}, units...)...)
	if err != nil && onlyNotLoaded(err) {
		return nil
	}
	return err
}

// onlyNotLoaded reports whether err is a systemctl exit error with only "not loaded" lines.
func onlyNotLoaded(err error) bool {
	var xe *ExitError
	if !errors.As(err, &xe) {
		return false
	}
	n := 0
	for _, l := range strings.Split(xe.Stderr, "\n") {
		l = strings.TrimSpace(l)
		if l == "" {
			continue
		}
		n++
		if !strings.HasSuffix(l, " not loaded.") {
			return false
		}
	}
	return n > 0
}

// Show implements Systemd.
func (s ExecSystemd) Show(ctx context.Context, units []string, props ...string) ([]map[string]string, error) {
	args := []string{"show"}
	for _, p := range props {
		args = append(args, "-p", p)
	}
	args = append(args, units...)
	out, err := s.R.Run(ctx, Cmd{Name: "systemctl", Args: args})
	if err != nil {
		return nil, err
	}
	return ParseShow(out, len(units)), nil
}

// ParseShow splits "systemctl show" output into one map for each unit.
func ParseShow(out []byte, n int) []map[string]string {
	res := make([]map[string]string, 0, n)
	cur := map[string]string{}
	sc := bufio.NewScanner(bytes.NewReader(out))
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Text()
		if strings.TrimSpace(line) == "" {
			if len(cur) > 0 {
				res = append(res, cur)
				cur = map[string]string{}
			}
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if ok {
			cur[k] = v
		}
	}
	if len(cur) > 0 {
		res = append(res, cur)
	}
	for len(res) < n {
		res = append(res, map[string]string{})
	}
	return res
}

// ExecPodman runs podman.
type ExecPodman struct{ R Runner }

// Version implements Podman.
func (p ExecPodman) Version(ctx context.Context) (string, error) {
	out, err := p.R.Run(ctx, Cmd{Name: "podman", Args: []string{"version", "--format", "{{.Client.Version}}"}})
	return strings.TrimSpace(string(out)), err
}

// SecretNames implements Podman.
func (p ExecPodman) SecretNames(ctx context.Context) (map[string]bool, error) {
	out, err := p.R.Run(ctx, Cmd{Name: "podman", Args: []string{"secret", "ls", "--format", "{{.Name}}"}})
	if err != nil {
		return nil, err
	}
	names := map[string]bool{}
	for _, l := range strings.Split(string(out), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			names[l] = true
		}
	}
	return names, nil
}

// SecretSet implements Podman.
func (p ExecPodman) SecretSet(ctx context.Context, name string, value []byte) error {
	_, err := p.R.Run(ctx, Cmd{
		Name:  "podman",
		Args:  []string{"secret", "create", "--replace", name, "-"},
		Stdin: bytes.NewReader(value),
	})
	return err
}

// ImageExists implements Podman.
func (p ExecPodman) ImageExists(ctx context.Context, ref string) (bool, error) {
	_, err := p.R.Run(ctx, Cmd{Name: "podman", Args: []string{"image", "exists", ref}})
	if err == nil {
		return true, nil
	}
	if xe, ok := err.(*ExitError); ok && xe.Code == 1 {
		return false, nil
	}
	return false, err
}

// Pull implements Podman.
func (p ExecPodman) Pull(ctx context.Context, ref string) error {
	_, err := p.R.Run(ctx, Cmd{Name: "podman", Args: []string{"pull", "--quiet", ref}})
	return err
}

// Exec implements Podman.
func (p ExecPodman) Exec(ctx context.Context, container, user string, args ...string) ([]byte, error) {
	a := []string{"exec"}
	if user != "" {
		a = append(a, "--user", user)
	}
	a = append(a, container)
	a = append(a, args...)
	return p.R.Run(ctx, Cmd{Name: "podman", Args: a})
}

// ExecAttached implements Podman.
func (p ExecPodman) ExecAttached(ctx context.Context, container string, tty bool, env []string, stdin io.Reader, args ...string) error {
	a := []string{"exec", "--interactive"}
	if tty {
		a = append(a, "--tty")
	}
	var cmdEnv []string
	for _, kv := range env {
		k, _, _ := strings.Cut(kv, "=")
		// "--env NAME" without a value copies NAME from the environment of podman.
		// The value does not show in the process list.
		a = append(a, "--env", k)
		cmdEnv = append(cmdEnv, kv)
	}
	a = append(a, container)
	a = append(a, args...)
	_, err := p.R.Run(ctx, Cmd{Name: "podman", Args: a, Env: cmdEnv, Stdin: stdin, Attach: true})
	return err
}

// Health implements Podman.
func (p ExecPodman) Health(ctx context.Context, pod string) (map[string]string, error) {
	out, err := p.R.Run(ctx, Cmd{Name: "podman", Args: []string{
		"ps", "--all", "--filter", "pod=" + pod, "--format", "{{.Names}}\t{{.Status}}",
	}})
	if err != nil {
		return nil, err
	}
	return ParseHealth(out), nil
}

// ParseHealth reads "name<TAB>status" lines and returns the health of each container.
// A container without a health check maps to "".
func ParseHealth(out []byte) map[string]string {
	res := map[string]string{}
	for _, l := range strings.Split(string(out), "\n") {
		name, status, ok := strings.Cut(strings.TrimSpace(l), "\t")
		if !ok {
			continue
		}
		h := ""
		if i := strings.LastIndex(status, "("); i >= 0 && strings.HasSuffix(status, ")") {
			h = status[i+1 : len(status)-1]
		}
		if strings.HasPrefix(status, "Exited") || strings.HasPrefix(status, "Created") {
			h = "stopped"
		}
		res[name] = h
	}
	return res
}

// ExecSkopeo runs skopeo.
type ExecSkopeo struct{ R Runner }

// Digest implements Skopeo.
func (s ExecSkopeo) Digest(ctx context.Context, ref, goos, arch string) (string, error) {
	out, err := s.R.Run(ctx, Cmd{Name: "skopeo", Args: []string{
		"inspect", "--override-os", goos, "--override-arch", arch,
		"--format", "{{.Digest}}", "docker://" + ref,
	}})
	if err != nil {
		return "", err
	}
	d := strings.TrimSpace(string(out))
	if !strings.HasPrefix(d, "sha256:") {
		return "", fmt.Errorf("skopeo returned an unexpected digest %q for %s", d, ref)
	}
	return d, nil
}

// ExecJournal runs journalctl on the terminal.
type ExecJournal struct{ R Runner }

// Show implements Journal.
func (j ExecJournal) Show(ctx context.Context, unit string, follow bool, lines int) error {
	args := []string{"--unit", unit, "--no-pager"}
	if lines > 0 {
		args = append(args, "--lines", fmt.Sprint(lines))
	}
	if follow {
		args = append(args, "--follow")
	}
	_, err := j.R.Run(ctx, Cmd{Name: "journalctl", Args: args, Attach: true})
	return err
}

// ExecClock runs timedatectl.
type ExecClock struct{ R Runner }

// NTPSynchronized implements Clock.
func (c ExecClock) NTPSynchronized(ctx context.Context) (bool, error) {
	out, err := c.R.Run(ctx, Cmd{Name: "timedatectl", Args: []string{"show", "--property", "NTPSynchronized", "--value"}})
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(string(out)) == "yes", nil
}
