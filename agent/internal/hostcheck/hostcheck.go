// Package hostcheck reports the host prerequisites that the installer provides.
// It only reads. It never changes the host.
package hostcheck

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"github.com/rithviknishad/care-pi/agent/internal/host"
	"github.com/rithviknishad/care-pi/agent/internal/paths"
)

// Level tells how a failed check counts.
type Level string

// Levels.
const (
	Required Level = "required"
	Advised  Level = "advised"
)

// Check is one prerequisite.
type Check struct {
	Name   string `json:"name"`
	Level  Level  `json:"level"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail"`
	Fix    string `json:"fix,omitempty"`
}

// Report lists every check.
type Report struct {
	Checks []Check `json:"checks"`
}

// OK reports whether every required check passed.
func (r *Report) OK() bool {
	for _, c := range r.Checks {
		if c.Level == Required && !c.OK {
			return false
		}
	}
	return true
}

// Missing returns the failed required checks.
func (r *Report) Missing() []Check {
	var out []Check
	for _, c := range r.Checks {
		if c.Level == Required && !c.OK {
			out = append(out, c)
		}
	}
	return out
}

// Env is what the checks read.
type Env struct {
	Paths  paths.Paths
	Podman host.Podman
	// LookPath finds a binary. Tests replace it.
	LookPath func(string) (string, error)
	// SkipDirs skips the directory checks (bootstrap creates the directories after the check).
	SkipDirs bool
}

// MinPodman is the lowest podman version with the quadlet features that care-pi uses.
var MinPodman = [2]int{5, 4}

// Run executes the checks.
func Run(ctx context.Context, e Env) *Report {
	if e.LookPath == nil {
		e.LookPath = exec.LookPath
	}
	r := &Report{}
	add := func(c Check) { r.Checks = append(r.Checks, c) }

	// podman
	c := Check{Name: "podman", Level: Required, Fix: "install podman 5.4 or later"}
	if _, err := e.LookPath("podman"); err != nil {
		c.Detail = "podman is not installed"
	} else if v, err := e.Podman.Version(ctx); err != nil {
		c.Detail = "cannot read the podman version: " + err.Error()
	} else if !atLeast(v, MinPodman) {
		c.Detail = fmt.Sprintf("podman %s is older than %d.%d", v, MinPodman[0], MinPodman[1])
	} else {
		c.OK, c.Detail = true, "podman "+v
	}
	add(c)

	q := Check{Name: "quadlet", Level: Required, Fix: "install the podman package that has the quadlet generator"}
	for _, p := range []string{"/usr/libexec/podman/quadlet", "/usr/lib/systemd/system-generators/podman-system-generator"} {
		if fileExists(e.Paths.Abs(p)) {
			q.OK, q.Detail = true, p
			break
		}
	}
	if !q.OK {
		q.Detail = "the quadlet generator is missing"
	}
	add(q)

	add(binary(e, "skopeo", Required, "install skopeo"))
	add(binary(e, "systemctl", Required, "care-pi needs systemd"))
	add(binary(e, "timedatectl", Required, "install systemd-timesyncd"))
	add(binary(e, "restic", Advised, "install restic (Phase 3 uses it)"))
	add(binary(e, "nft", Advised, "install nftables"))
	add(binary(e, "avahi-daemon", Advised, "install avahi-daemon"))

	add(cgroupMemory(e))
	add(zram(e))
	add(avahiInterfaces(e))

	if !e.SkipDirs {
		for _, d := range paths.Layout() {
			ok := dirExists(e.Paths.Abs(d.Path))
			detail := "present"
			if !ok {
				detail = "missing"
			}
			add(Check{Name: "dir " + d.Path, Level: Advised, OK: ok, Detail: detail, Fix: "run care-pi bootstrap"})
		}
	}
	return r
}

func binary(e Env, name string, lvl Level, fix string) Check {
	c := Check{Name: name, Level: lvl, Fix: fix}
	if p, err := e.LookPath(name); err == nil {
		c.OK, c.Detail = true, p
	} else {
		c.Detail = name + " is not installed"
	}
	return c
}

// cgroupMemory passes when cgroup.controllers lists memory. Only this file decides.
// The Pi firmware adds cgroup_disable=memory to /proc/cmdline itself, also when cmdline.txt
// has cgroup_enable=memory. The later token wins, so /proc/cmdline only adds a detail.
func cgroupMemory(e Env) Check {
	c := Check{Name: "cgroup memory controller", Level: Required,
		Fix: "add \"cgroup_enable=memory cgroup_memory=1\" to /boot/firmware/cmdline.txt, remove \"cgroup_disable=memory\", then reboot"}
	note := ""
	if cmd, err := os.ReadFile(e.Paths.Abs("/proc/cmdline")); err == nil {
		disable, enable := -1, -1
		for i, f := range strings.Fields(string(cmd)) {
			switch f {
			case "cgroup_disable=memory":
				disable = i
			case "cgroup_enable=memory":
				enable = i
			}
		}
		switch {
		case disable >= 0 && enable > disable:
			note = "; /proc/cmdline has cgroup_disable=memory, but the later cgroup_enable=memory wins"
		case disable >= 0:
			note = "; /proc/cmdline has cgroup_disable=memory"
		}
	}
	ctrl, err := os.ReadFile(e.Paths.Abs("/sys/fs/cgroup/cgroup.controllers"))
	if err != nil {
		c.Detail = "cannot read /sys/fs/cgroup/cgroup.controllers (cgroup v2 is required)"
		return c
	}
	if !containsWord(string(ctrl), "memory") {
		c.Detail = "the memory controller is not active; MemoryMax has no effect" + note
		return c
	}
	c.OK, c.Detail = true, "memory controller active"+note
	return c
}

func zram(e Env) Check {
	c := Check{Name: "zram swap", Level: Required,
		Fix: "enable zram swap with a size of at least the RAM size (Pi OS rpi-swap, or systemd-zram-generator with zram-size = ram)"}
	memKB := meminfo(e, "MemTotal")
	swaps, err := os.ReadFile(e.Paths.Abs("/proc/swaps"))
	if err != nil || memKB == 0 {
		c.Detail = "cannot read /proc/swaps or /proc/meminfo"
		return c
	}
	var zramKB uint64
	sc := bufio.NewScanner(bytes.NewReader(swaps))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) >= 3 && strings.HasPrefix(f[0], "/dev/zram") {
			v, _ := strconv.ParseUint(f[2], 10, 64)
			zramKB += v
		}
	}
	switch {
	case zramKB == 0:
		c.Detail = "no zram swap device is active; the FE build needs it (SP4)"
	case zramKB*100 < memKB*95:
		c.Detail = fmt.Sprintf("zram swap is %d MiB, less than the RAM size %d MiB", zramKB/1024, memKB/1024)
	default:
		c.OK, c.Detail = true, fmt.Sprintf("zram swap %d MiB, RAM %d MiB", zramKB/1024, memKB/1024)
	}
	return c
}

func avahiInterfaces(e Env) Check {
	c := Check{Name: "avahi allow-interfaces", Level: Advised,
		Fix: "set allow-interfaces=eth0,wlan0 in /etc/avahi/avahi-daemon.conf (SP5 finding 6)"}
	data, err := os.ReadFile(e.Paths.Abs("/etc/avahi/avahi-daemon.conf"))
	if err != nil {
		c.Detail = "cannot read /etc/avahi/avahi-daemon.conf"
		return c
	}
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		l := strings.TrimSpace(sc.Text())
		if v, ok := strings.CutPrefix(l, "allow-interfaces="); ok {
			c.OK, c.Detail = true, "allow-interfaces="+v
			return c
		}
	}
	c.Detail = "allow-interfaces is not set; avahi can announce the podman bridge address"
	return c
}

func meminfo(e Env, key string) uint64 {
	data, err := os.ReadFile(e.Paths.Abs("/proc/meminfo"))
	if err != nil {
		return 0
	}
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) >= 2 && f[0] == key+":" {
			v, _ := strconv.ParseUint(f[1], 10, 64)
			return v
		}
	}
	return 0
}

func containsWord(s, w string) bool {
	for _, f := range strings.Fields(s) {
		if f == w {
			return true
		}
	}
	return false
}

func fileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}

func dirExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}

func atLeast(v string, min [2]int) bool {
	parts := strings.SplitN(strings.TrimPrefix(v, "v"), ".", 3)
	if len(parts) < 2 {
		return false
	}
	maj, err1 := strconv.Atoi(parts[0])
	minor, err2 := strconv.Atoi(strings.TrimFunc(parts[1], func(r rune) bool { return r < '0' || r > '9' }))
	if err1 != nil || err2 != nil {
		return false
	}
	return maj > min[0] || (maj == min[0] && minor >= min[1])
}

// WriteText prints the report.
func (r *Report) WriteText(w io.Writer) {
	for _, c := range r.Checks {
		mark := "ok  "
		if !c.OK {
			mark = "FAIL"
			if c.Level == Advised {
				mark = "warn"
			}
		}
		_, _ = fmt.Fprintf(w, "%s  %-28s %s\n", mark, c.Name, c.Detail)
		if !c.OK && c.Fix != "" {
			_, _ = fmt.Fprintf(w, "      %-28s fix: %s\n", "", c.Fix)
		}
	}
}

// WriteJSON prints the report as JSON.
func (r *Report) WriteJSON(w io.Writer) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(struct {
		OK bool `json:"ok"`
		*Report
	}{r.OK(), r})
}
