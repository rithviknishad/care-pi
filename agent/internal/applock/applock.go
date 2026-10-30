// Package applock makes sure that only one care-pi command changes the host at a time.
//
// apply, bootstrap, fe build, and fe prune take an exclusive flock on /run/care-pi/apply.lock
// for their full run. Two applies that overlap can start care.target while the other
// apply waits for the stops of a full cycle, and that start cancels the stops (SP6 finding 10).
package applock

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"syscall"
	"time"

	"github.com/rithviknishad/care-pi/agent/internal/paths"
)

// DefaultTimeout is the default of --lock-timeout.
const DefaultTimeout = 30 * time.Minute

// Waiting is the notice when the lock is not free after NoticeAfter.
const Waiting = "another care-pi command holds the lock; waiting"

// NoticeAfter and Poll are variables, so that tests can make them short.
var (
	NoticeAfter = 2 * time.Second
	Poll        = 100 * time.Millisecond
)

// ErrTimeout tells that the lock was not free before the timeout.
var ErrTimeout = errors.New("another care-pi command holds the lock")

// Acquire takes the exclusive lock. It waits until the lock is free, ctx ends, or the
// timeout ends. A timeout of 0 waits without a limit. When the lock is not free after
// NoticeAfter, Acquire writes one notice to out. The returned function releases the lock.
func Acquire(ctx context.Context, p paths.Paths, timeout time.Duration, out io.Writer) (func(), error) {
	if err := os.MkdirAll(p.Abs(paths.RunDir), 0o700); err != nil {
		return nil, fmt.Errorf("cannot create %s: %w", paths.RunDir, err)
	}
	f, err := os.OpenFile(p.Abs(paths.ApplyLock), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("cannot open %s: %w", paths.ApplyLock, err)
	}
	start := time.Now()
	noticed := false
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
			return nil, fmt.Errorf("cannot lock %s: %w", paths.ApplyLock, err)
		}
		waited := time.Since(start)
		if timeout > 0 && waited >= timeout {
			_ = f.Close()
			return nil, fmt.Errorf("%w %s for more than %s; try again later, or set --lock-timeout", ErrTimeout, paths.ApplyLock, timeout)
		}
		if !noticed && waited >= NoticeAfter && out != nil {
			_, _ = fmt.Fprintln(out, Waiting)
			noticed = true
		}
		select {
		case <-ctx.Done():
			_ = f.Close()
			return nil, ctx.Err()
		case <-time.After(Poll):
		}
	}
}

// Held reports whether a process holds the apply lock now. It never waits.
func Held(p paths.Paths) bool {
	f, err := os.OpenFile(p.Abs(paths.ApplyLock), os.O_RDWR, 0o600)
	if err != nil {
		return false
	}
	defer func() { _ = f.Close() }()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return errors.Is(err, syscall.EWOULDBLOCK)
	}
	_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	return false
}
