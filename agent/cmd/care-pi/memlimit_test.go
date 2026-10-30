package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCgroupMemoryMax(t *testing.T) {
	dir := t.TempDir()
	unit := filepath.Join(dir, "sys", "system.slice", "care-pi.service")
	if err := os.MkdirAll(unit, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(path, s string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	proc := filepath.Join(dir, "cgroup")
	write(proc, "0::/system.slice/care-pi.service\n")
	write(filepath.Join(unit, "memory.max"), "67108864\n")
	if n, ok := cgroupMemoryMax(proc, filepath.Join(dir, "sys")); !ok || n != 64<<20 {
		t.Fatalf("got %d %v, want 64 MiB", n, ok)
	}
	write(filepath.Join(unit, "memory.max"), "max\n")
	if _, ok := cgroupMemoryMax(proc, filepath.Join(dir, "sys")); ok {
		t.Fatal("memory.max \"max\" is not a cap")
	}
	write(proc, "12:memory:/system.slice/care-pi.service\n")
	if _, ok := cgroupMemoryMax(proc, filepath.Join(dir, "sys")); ok {
		t.Fatal("cgroup v1 has no 0:: line")
	}
	if _, ok := cgroupMemoryMax(filepath.Join(dir, "missing"), filepath.Join(dir, "sys")); ok {
		t.Fatal("a missing /proc/self/cgroup is not a cap")
	}
}
