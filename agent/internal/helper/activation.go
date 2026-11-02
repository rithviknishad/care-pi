package helper

import (
	"errors"
	"fmt"
	"net"
	"os"
	"strconv"
)

// Listener returns the socket of socket activation (LISTEN_FDS of care-pi-helper.socket, fd 3), or a new socket at
// path with mode 0660 when systemd gave none (a test or a start by hand).
func Listener(path string) (net.Listener, error) {
	if pid, _ := strconv.Atoi(os.Getenv("LISTEN_PID")); pid == os.Getpid() {
		if n, _ := strconv.Atoi(os.Getenv("LISTEN_FDS")); n >= 1 {
			_ = os.Unsetenv("LISTEN_PID")
			_ = os.Unsetenv("LISTEN_FDS")
			_ = os.Unsetenv("LISTEN_FDNAMES")
			f := os.NewFile(3, "care-pi-helper.socket")
			l, err := net.FileListener(f)
			_ = f.Close()
			if err != nil {
				return nil, fmt.Errorf("the socket of systemd: %w", err)
			}
			return l, nil
		}
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	l, err := net.Listen("unix", path)
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(path, 0o660); err != nil {
		_ = l.Close()
		return nil, err
	}
	return l, nil
}
