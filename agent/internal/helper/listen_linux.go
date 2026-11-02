//go:build linux

package helper

import (
	"fmt"
	"net"
	"os/user"
	"strconv"
	"syscall"
)

// PeerCheck returns a check that allows root and the user name (the care-pi user of the daemon).
func PeerCheck(name string) func(net.Conn) (string, bool) {
	uid := -1
	if u, err := user.Lookup(name); err == nil {
		uid, _ = strconv.Atoi(u.Uid)
	}
	return func(conn net.Conn) (string, bool) {
		uc, ok := conn.(*net.UnixConn)
		if !ok {
			return "not unix", false
		}
		raw, err := uc.SyscallConn()
		if err != nil {
			return err.Error(), false
		}
		var cred *syscall.Ucred
		var cerr error
		_ = raw.Control(func(fd uintptr) {
			cred, cerr = syscall.GetsockoptUcred(int(fd), syscall.SOL_SOCKET, syscall.SO_PEERCRED)
		})
		if cerr != nil || cred == nil {
			return fmt.Sprint(cerr), false
		}
		peer := fmt.Sprintf("uid=%d pid=%d", cred.Uid, cred.Pid)
		return peer, cred.Uid == 0 || (uid >= 0 && int(cred.Uid) == uid)
	}
}
