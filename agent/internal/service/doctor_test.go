package service

import (
	"reflect"
	"testing"
)

const ssOut = `LISTEN 0      4096   192.168.1.10:443        0.0.0.0:*    users:(("conmon",pid=812,fd=5))
LISTEN 0      4096   100.101.102.103:443     0.0.0.0:*    users:(("tailscaled",pid=640,fd=30))
LISTEN 0      4096         0.0.0.0:80        0.0.0.0:*    users:(("nginx",pid=99,fd=6))
LISTEN 0      128          0.0.0.0:22        0.0.0.0:*    users:(("sshd",pid=500,fd=3))
LISTEN 0      4096       127.0.0.1:19000     0.0.0.0:*    users:(("conmon",pid=812,fd=7))
LISTEN 0      4096   [fe80::1%eth0]:8443        [::]:*    users:(("caddy",pid=1,fd=9))
LISTEN 0      4096            [::]:8443         [::]:*    users:(("python3",pid=7,fd=3))
`

func TestPortConflicts(t *testing.T) {
	ls := ParseSS(ssOut)
	if len(ls) != 7 || ls[1].Process != "tailscaled" || ls[1].Addr != "100.101.102.103" || ls[1].Port != 443 || ls[5].Addr != "fe80::1" {
		t.Fatalf("parse %+v", ls)
	}
	got := PortConflicts(ls, []string{"192.168.1.10", "100.101.102.103"})
	want := []Listener{{Addr: "100.101.102.103", Port: 443, Process: "tailscaled"}, {Addr: "0.0.0.0", Port: 80, Process: "nginx"},
		{Addr: "::", Port: 8443, Process: "python3"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("conflicts %+v", got)
	}
	if got := PortConflicts(ParseSS(""), nil); len(got) != 0 {
		t.Fatalf("empty %+v", got)
	}
}
