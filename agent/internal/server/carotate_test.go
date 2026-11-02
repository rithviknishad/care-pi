package server

import (
	"os"
	"testing"

	"github.com/rithviknishad/care-pi/agent/internal/ca"
	"github.com/rithviknishad/care-pi/agent/internal/jobs"
	"github.com/rithviknishad/care-pi/agent/internal/paths"
	"github.com/rithviknishad/care-pi/agent/internal/service"
)

// TestLocalCARotate: POST /tls/local-ca/rotate makes a new root, keeps the old one for the page, and restarts Caddy
// (Phase 7, docs/api/README.md section 17.3).
func TestLocalCARotate(t *testing.T) {
	service.CaddyRestartDelay = 0
	e, _ := readyConnEnv(t)
	e.want(e.do("POST", "/api/v1/tls/local-ca/rotate", nil), 409, "state-conflict")
	e.jobOK(e.do("POST", "/api/v1/apply", map[string]any{}))
	before := e.do("GET", "/api/v1/tls", nil).json(t)["local_ca"].(map[string]any)
	oldFP := before["root"].(map[string]any)["fingerprint_sha256"].(string)
	if before["previous_root"] != nil || before["change_reason"] != nil {
		t.Fatalf("before %v", before)
	}

	e.fake.Calls = nil
	r := e.do("POST", "/api/v1/tls/local-ca/rotate", nil)
	id := e.jobOK(r)
	j, _ := jobs.Load(e.p, id)
	if j.Kind != jobs.KindTLSLocalCARotate || len(j.Steps) != 2 || act(j.Steps[0].Action) != "ca-rotate" || act(j.Steps[1].Action) != "restart-caddy" {
		t.Fatalf("job %+v", j.Steps)
	}
	if got := e.fake.Filter("systemctl try-restart care-caddy.service"); len(got) != 1 {
		t.Fatalf("caddy restart %q", got)
	}
	after := e.do("GET", "/api/v1/tls", nil).json(t)["local_ca"].(map[string]any)
	newFP := after["root"].(map[string]any)["fingerprint_sha256"].(string)
	prev, _ := after["previous_root"].(map[string]any)
	if newFP == oldFP || after["change_reason"] != "rotation" || after["previous_fingerprint_sha256"] != oldFP ||
		prev == nil || prev["fingerprint_sha256"] != oldFP || after["ca_changed_at"] == nil {
		t.Fatalf("after %v", after)
	}
	if _, err := os.Stat(e.p.Abs(ca.PrevCertPath())); err != nil {
		t.Fatal(err)
	}
	if res := j.Result; len(res) == 0 {
		t.Fatal("no result")
	}
	// The root download gives the new root.
	r = e.do("GET", "/api/v1/tls/root-ca?format=pem", nil)
	e.want(r, 200, "")
	if _, der, err := ca.ParseCertPEM(r.body); err != nil || ca.Fingerprint(der) != newFP {
		t.Fatalf("download %v", err)
	}
	_ = paths.CADir
}

func act(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
