package main

import (
	"log/slog"
	"os"
	"path/filepath"
	"runtime/debug"
	"strconv"
	"strings"
)

// applySoftMemoryLimit gives the Go garbage collector a soft limit of half the memory cap of the
// cgroup of the agent (MemoryMax=128M of care-pi.service, plans/01 section 8: a soft limit of 64 MiB).
// The other half is for the child processes (podman, skopeo, journalctl) and the page cache, which
// the cgroup also counts. Without the limit, the heap grows to twice the live heap before a
// collection. On lumine, with the earlier cap of 64M, the agent reached 73 MiB RSS (argon2id hashes
// of 19 MiB), the cgroup reached its cap 89 times, and the swap reached 31 of the 32 MiB of
// MemorySwapMax during the first setup job (wave 4c). GOMEMLIMIT in the environment wins.
func applySoftMemoryLimit(log *slog.Logger) {
	if os.Getenv("GOMEMLIMIT") != "" {
		return
	}
	limit, ok := cgroupMemoryMax("/proc/self/cgroup", "/sys/fs/cgroup")
	if !ok {
		return
	}
	debug.SetMemoryLimit(limit / 2)
	log.Info("go memory limit", "cgroup_memory_max", limit, "soft_limit", limit/2)
}

// cgroupMemoryMax reads memory.max of the cgroup v2 of this process. It reports false when the
// cgroup has no cap ("max") or when the files do not exist (macOS, cgroup v1).
func cgroupMemoryMax(procCgroup, root string) (int64, bool) {
	data, err := os.ReadFile(procCgroup)
	if err != nil {
		return 0, false
	}
	for _, line := range strings.Split(string(data), "\n") {
		rel, ok := strings.CutPrefix(line, "0::")
		if !ok {
			continue
		}
		b, err := os.ReadFile(filepath.Join(root, filepath.Clean("/"+rel), "memory.max"))
		if err != nil {
			return 0, false
		}
		n, err := strconv.ParseInt(strings.TrimSpace(string(b)), 10, 64)
		if err != nil || n <= 0 {
			return 0, false
		}
		return n, true
	}
	return 0, false
}
