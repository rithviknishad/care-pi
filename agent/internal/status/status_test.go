package status

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rithviknishad/care-pi/agent/internal/host"
	"github.com/rithviknishad/care-pi/agent/internal/netinfo"
	"github.com/rithviknishad/care-pi/agent/internal/paths"
)

func TestCollect(t *testing.T) {
	root := t.TempDir()
	p := paths.New(root)
	pod := p.Abs(paths.QuadletDir + "/care.pod")
	_ = os.MkdirAll(filepath.Dir(pod), 0o755)
	_ = os.WriteFile(pod, []byte("[Pod]\nPublishPort=192.168.1.10:80:80\nPublishPort=192.168.1.10:443:443\nPublishPort=127.0.0.1:19000:9000\n"), 0o644)

	units := Units()
	var b strings.Builder
	for _, u := range units {
		state := "active"
		if u == "care-worker.service" {
			state = "failed"
		}
		fmt.Fprintf(&b, "Id=%s\nActiveState=%s\nSubState=running\nMemoryCurrent=104857600\nMemoryPeak=[not set]\nMemoryMax=infinity\nNRestarts=1\n\n", u, state)
	}
	f := host.NewFake()
	f.On("systemctl show -p Id -p ActiveState -p SubState -p MemoryCurrent -p MemoryPeak -p MemoryMax -p NRestarts "+strings.Join(units, " "),
		host.Response{Out: b.String()})
	f.On("podman ps --all --filter pod=care --format {{.Names}}\t{{.Status}}",
		host.Response{Out: "care-api\tUp 1 minute (healthy)\ncare-db\tUp 2 minutes (unhealthy)\n"})
	r, err := Collect(context.Background(), host.New(f), p, []string{"care.local"}, netinfo.Static{"192.168.1.10", "10.0.0.5"})
	if err != nil {
		t.Fatal(err)
	}
	if !r.Degraded {
		t.Fatal("want degraded")
	}
	var out strings.Builder
	r.WriteText(&out)
	s := out.String()
	for _, want := range []string{"state: degraded", "care.local", "addresses published: 192.168.1.10", "run \"care-pi apply\"", "care-worker.service is failed", "care-db.service is unhealthy", "100M"} {
		if !strings.Contains(s, want) {
			t.Errorf("output lacks %q:\n%s", want, s)
		}
	}
}
