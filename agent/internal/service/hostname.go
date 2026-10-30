package service

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"strings"

	"github.com/rithviknishad/care-pi/agent/internal/host"
	"github.com/rithviknishad/care-pi/agent/internal/jobs"
	"github.com/rithviknishad/care-pi/agent/internal/problem"
)

// The files of the OS host name. care-pi.service has them in ReadWritePaths.
const (
	etcHostname = "/etc/hostname"
	etcHosts    = "/etc/hosts"
)

// setHostname makes identity.hostname the OS host name (docs/api/README.md, section 5: the finish
// job applies a new host name). avahi announces the OS host name, so the mDNS name and the names
// of Caddy agree only after this step. It does nothing when /etc/hostname has the name already,
// or when /etc/hostname does not exist (a dev root).
func (s *Service) setHostname(ctx context.Context, rec *jobs.Recorder, name string) error {
	current := strings.SplitN(readTrim(s.Paths.Abs(etcHostname)), ".", 2)[0]
	if current == "" || name == "" || current == name {
		return nil
	}
	end := rec.StepAction("set-hostname: "+current+" -> "+name, "set-hostname")
	err := s.applyHostname(ctx, name)
	end(err)
	if err != nil {
		return problem.Newf(problem.Internal, "The agent cannot set the host name %s: %v.", name, err)
	}
	rec.Logf("the host name is %s; mDNS announces %s.local", name, name)
	return nil
}

// applyHostname runs the same changes as the installer (step 4, "host name"): hostnamectl, the
// 127.0.1.1 line of /etc/hosts, then a restart of avahi-daemon, so it announces the new name.
func (s *Service) applyHostname(ctx context.Context, name string) error {
	if _, err := s.Host.Runner.Run(ctx, host.Cmd{Name: "hostnamectl", Args: []string{"set-hostname", name}}); err != nil {
		return err
	}
	if err := setHostsName(s.Paths.Abs(etcHosts), name); err != nil {
		return err
	}
	return s.Host.Systemd.TryRestart(ctx, "avahi-daemon.service")
}

// setHostsName puts "127.0.1.1<TAB>name" in place of the first 127.0.1.1 line of the hosts file
// and removes the other 127.0.1.1 lines. Without such a line, it adds one at the end. It writes
// the file in place, because the sandbox of care-pi.service bind-mounts the file (no rename).
func setHostsName(path, name string) error {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	text := strings.TrimSuffix(string(data), "\n")
	var out []string
	done := false
	if text != "" {
		for _, l := range strings.Split(text, "\n") {
			if f := strings.Fields(l); len(f) > 0 && f[0] == "127.0.1.1" {
				if !done {
					out = append(out, "127.0.1.1\t"+name)
					done = true
				}
				continue
			}
			out = append(out, l)
		}
	}
	if !done {
		out = append(out, "127.0.1.1\t"+name)
	}
	next := strings.Join(out, "\n") + "\n"
	if next == string(data) {
		return nil
	}
	return os.WriteFile(path, []byte(next), 0o644)
}
