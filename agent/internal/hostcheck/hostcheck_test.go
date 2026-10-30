package hostcheck

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rithviknishad/care-pi/agent/internal/host"
	"github.com/rithviknishad/care-pi/agent/internal/paths"
)

func write(t *testing.T, root, p, data string) {
	t.Helper()
	abs := filepath.Join(root, p)
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(abs, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
}

func goodHost(t *testing.T) string {
	root := t.TempDir()
	write(t, root, "/usr/libexec/podman/quadlet", "")
	write(t, root, "/sys/fs/cgroup/cgroup.controllers", "cpuset cpu io memory pids\n")
	write(t, root, "/proc/cmdline", "console=tty1 cgroup_enable=memory cgroup_memory=1\n")
	write(t, root, "/proc/meminfo", "MemTotal:        2031616 kB\nMemFree: 1 kB\n")
	write(t, root, "/proc/swaps", "Filename Type Size Used Priority\n/dev/zram0 partition 2031612 0 100\n")
	write(t, root, "/etc/avahi/avahi-daemon.conf", "[server]\nallow-interfaces=eth0,wlan0\n")
	return root
}

func env(root, podman string, missing ...string) Env {
	f := host.NewFake().On("podman version --format {{.Client.Version}}", host.Response{Out: podman + "\n"})
	return Env{
		Paths:  paths.New(root),
		Podman: host.New(f).Podman,
		LookPath: func(name string) (string, error) {
			for _, m := range missing {
				if m == name {
					return "", errors.New("not found")
				}
			}
			return "/usr/bin/" + name, nil
		},
		SkipDirs: true,
	}
}

func TestGoodHost(t *testing.T) {
	r := Run(context.Background(), env(goodHost(t), "5.4.2"))
	if !r.OK() {
		var b strings.Builder
		r.WriteText(&b)
		t.Fatalf("want OK:\n%s", b.String())
	}
}

func TestMissingPrerequisites(t *testing.T) {
	root := goodHost(t)
	write(t, root, "/sys/fs/cgroup/cgroup.controllers", "cpuset cpu io pids\n")
	write(t, root, "/proc/cmdline", "console=tty1 cgroup_disable=memory\n")
	write(t, root, "/proc/swaps", "Filename Type Size Used Priority\n/dev/zram0 partition 1015806 0 100\n")
	r := Run(context.Background(), env(root, "4.9.3", "skopeo", "restic"))
	if r.OK() {
		t.Fatal("want failures")
	}
	var names []string
	for _, c := range r.Missing() {
		names = append(names, c.Name)
	}
	got := strings.Join(names, ",")
	for _, want := range []string{"podman", "skopeo", "cgroup memory controller", "zram swap"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing list %q lacks %q", got, want)
		}
	}
	if strings.Contains(got, "restic") {
		t.Error("restic is advised, not required")
	}
}

func memoryCheck(t *testing.T, r *Report) Check {
	t.Helper()
	for _, c := range r.Checks {
		if c.Name == "cgroup memory controller" {
			return c
		}
	}
	t.Fatal("no cgroup memory controller check")
	return Check{}
}

// The Pi firmware adds cgroup_disable=memory to /proc/cmdline. This is the real shape of lumine.
func TestPiCmdlineWithFirmwareDisable(t *testing.T) {
	root := goodHost(t)
	write(t, root, "/proc/cmdline", "reboot=w coherent_pool=1M 8250.nr_uarts=1 pci=pcie_bus_safe cgroup_disable=memory "+
		"numa_policy=interleave smsc95xx.macaddr=2C:CF:67:00:00:00 vc_mem.mem_base=0x3fc00000 vc_mem.mem_size=0x40000000 "+
		"console=ttyAMA10,115200 console=tty1 root=PARTUUID=00000000-02 rootfstype=ext4 fsck.repair=yes rootwait "+
		"cfg80211.ieee80211_regdom=IN cgroup_enable=memory cgroup_memory=1\n")
	write(t, root, "/sys/fs/cgroup/cgroup.controllers", "cpuset cpu io memory hugetlb pids rdma misc\n")
	r := Run(context.Background(), env(root, "5.4.2"))
	c := memoryCheck(t, r)
	if !c.OK || !r.OK() {
		t.Fatalf("want the memory check to pass: %+v", c)
	}
	if !strings.Contains(c.Detail, "later cgroup_enable=memory wins") {
		t.Fatalf("want an advisory detail, got %q", c.Detail)
	}
}

// Only cgroup.controllers decides. A command line without cgroup_disable=memory does not
// make the check pass when the controller is not active.
func TestMemoryControllerDecides(t *testing.T) {
	root := goodHost(t)
	write(t, root, "/sys/fs/cgroup/cgroup.controllers", "cpuset cpu io pids\n")
	if c := memoryCheck(t, Run(context.Background(), env(root, "5.4.2"))); c.OK {
		t.Fatalf("want a failure without the memory controller: %+v", c)
	}
	write(t, root, "/sys/fs/cgroup/cgroup.controllers", "cpuset cpu io memory pids\n")
	write(t, root, "/proc/cmdline", "console=tty1 cgroup_disable=memory\n")
	c := memoryCheck(t, Run(context.Background(), env(root, "5.4.2")))
	if !c.OK || !strings.Contains(c.Detail, "cgroup_disable=memory") {
		t.Fatalf("want a pass with a detail: %+v", c)
	}
}
