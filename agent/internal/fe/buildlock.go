package fe

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/rithviknishad/care-pi/agent/internal/paths"
)

// BuildLockFile serialises the FE builds, the downloads, and prune.
const BuildLockFile = paths.StateDir + "/fe-build.lock"

// ErrBusy tells that a different care-pi process holds the FE build lock.
var ErrBusy = errors.New("a different care-pi process builds or downloads an FE build now")

// takeLock takes the FE build lock. With wait, it waits until the lock is free or ctx ends.
func takeLock(ctx context.Context, p paths.Paths, wait bool) (func(), error) {
	path := p.Abs(BuildLockFile)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("cannot create the directory of the FE build lock: %w", err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("cannot open the FE build lock: %w", err)
	}
	for {
		err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return func() {
				_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
				_ = f.Close()
			}, nil
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) {
			_ = f.Close()
			return nil, fmt.Errorf("cannot take the FE build lock: %w", err)
		}
		if !wait {
			_ = f.Close()
			return nil, ErrBusy
		}
		select {
		case <-ctx.Done():
			_ = f.Close()
			return nil, ctx.Err()
		case <-time.After(time.Second):
		}
	}
}
