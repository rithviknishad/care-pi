package server

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/rithviknishad/care-pi/agent/internal/host"
	"github.com/rithviknishad/care-pi/agent/internal/jobs"
)

// TestDoctor: GET /api/v1/doctor (Phase 7) and the sections of the support bundle that Phase 7 adds.
func TestDoctor(t *testing.T) {
	e, _ := readyConnEnv(t)
	e.fake.On("ss -ltnpH", host.Response{Out: "LISTEN 0 4096 192.168.1.10:443 0.0.0.0:* users:((\"conmon\",pid=1,fd=5))\n" +
		"LISTEN 0 4096 0.0.0.0:8443 0.0.0.0:* users:((\"nginx\",pid=2,fd=6))\n"})
	e.fake.On("tailscale status --json", host.Response{Out: `{"Self":{"DNSName":"care.tail1234.ts.net.","PublicKey":"nodekey:SECRET"},"Peer":{"x":{"HostName":"laptop"}}}`})
	r := e.do("GET", "/api/v1/doctor", nil)
	e.want(r, 200, "")
	var rep struct {
		Status string `json:"status"`
		Checks []struct {
			ID, Status, Detail string
			Fix                *string
		} `json:"checks"`
	}
	must(t, json.Unmarshal(r.body, &rep))
	byID := map[string]string{}
	for _, c := range rep.Checks {
		byID[c.ID] = c.Status + ": " + c.Detail
	}
	if !strings.HasPrefix(byID["ports"], "fail: nginx on 0.0.0.0:8443") || rep.Status != "fail" {
		t.Fatalf("ports %q, status %s", byID["ports"], rep.Status)
	}
	for _, id := range []string{"mdns", "clock", "memory", "units", "firewall", "reboot"} {
		if byID[id] == "" {
			t.Errorf("no check %s in %v", id, byID)
		}
	}
	if rep.Checks[0].Status != "fail" {
		t.Fatalf("the failed checks come first: %+v", rep.Checks[0])
	}

	e.jobOK(e.do("POST", "/api/v1/apply", map[string]any{}))
	r = e.do("POST", "/api/v1/support-bundle", map[string]any{"log_lines": 100, "since_hours": 1})
	e.want(r, 202, "")
	j := e.waitJob(r.json(t)["id"].(string))
	var res jobs.SupportBundleResult
	must(t, json.Unmarshal(j.Result, &res))
	r = e.do("GET", res.DownloadPath, nil)
	e.want(r, 200, "")
	files := map[string]string{}
	gz, err := gzip.NewReader(bytes.NewReader(r.body))
	must(t, err)
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		must(t, err)
		data, _ := io.ReadAll(tr)
		files[strings.TrimPrefix(h.Name, "care-pi-support/")] = string(data)
	}
	for _, f := range []string{"doctor.json", "nft-ruleset.txt", "podman-ps.txt", "memory-peaks.txt", "firewall-state.json", "listening-sockets.txt"} {
		if _, ok := files[f]; !ok {
			t.Errorf("no %s in the bundle", f)
		}
	}
	if ts := files["tailscale-status.json"]; strings.Contains(ts, "SECRET") || strings.Contains(ts, "laptop") || !strings.Contains(ts, "care.tail1234.ts.net") {
		t.Fatalf("tailscale status: %s", ts)
	}
	rendered := false
	for f := range files {
		if strings.HasPrefix(f, "rendered-tree/etc/care-pi/rendered/") || strings.HasPrefix(f, "rendered-tree/etc/containers/systemd/") {
			rendered = true
		}
		if strings.HasSuffix(f, "additional-plugs.json") && strings.HasPrefix(f, "rendered-tree") {
			t.Fatalf("the plug configs are in the bundle: %s", f)
		}
	}
	if !rendered {
		t.Fatalf("no rendered tree in the bundle: %v", len(files))
	}
}
