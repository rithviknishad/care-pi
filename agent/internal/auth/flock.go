package auth

import (
	"os"
	"syscall"
)

// flock takes an exclusive lock on f. Close releases it.
func flock(f *os.File) error {
	return syscall.Flock(int(f.Fd()), syscall.LOCK_EX)
}
