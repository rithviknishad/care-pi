package server

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/rithviknishad/care-pi/agent/internal/config"
	"github.com/rithviknishad/care-pi/agent/internal/firewall"
	"github.com/rithviknishad/care-pi/agent/internal/host"
	"github.com/rithviknishad/care-pi/agent/internal/jobs"
	"github.com/rithviknishad/care-pi/agent/internal/paths"
)

// TestFirewallDrop: a config change to the policy drop starts the rollback timer before the load; the console
// confirms it; a rollback sets accept in nftables and in config.yaml (Phase 7, docs/console-server.md section 13).
func TestFirewallDrop(t *testing.T) {
	e, _ := readyConnEnv(t)
	e.fake.On("systemctl show -p ActiveState care-pi-firewall.service", host.Response{Out: "ActiveState=active\n"})

	r := e.do("GET", "/api/v1/security/firewall", nil)
	e.want(r, 200, "")
	if st := r.json(t); st["policy"] != "accept" || st["pending_until"] != nil || st["unit_active"] != true || st["ssh_allowed"] != true {
		t.Fatalf("firewall %v", st)
	}
	e.want(e.do("POST", "/api/v1/security/firewall/confirm", nil), 409, "state-conflict")

	etag := e.do("GET", "/api/v1/config", nil).header.Get("ETag")
	r = e.do("PATCH", "/api/v1/config?apply=true", map[string]any{"security": map[string]any{"firewall": map[string]any{"policy": "drop"}}},
		header("Content-Type", "application/merge-patch+json"), header("If-Match", etag))
	e.want(r, 202, "")
	job, _ := r.json(t)["job"].(map[string]any)
	if job == nil {
		t.Fatalf("no apply job: %s", r.body)
	}
	if j := e.waitJob(job["id"].(string)); j.State != jobs.StateSucceeded {
		t.Fatalf("apply %s %+v", j.State, j.Error)
	}
	nft, _ := os.ReadFile(e.p.Abs(paths.FirewallFile))
	if !strings.Contains(string(nft), "policy drop;") || !strings.Contains(string(nft), "tcp dport 22 accept") {
		t.Fatalf("table:\n%s", nft)
	}
	calls := e.fake.Filter("systemd-run --quiet --collect --unit="+firewall.RollbackUnit, "systemctl try-reload-or-restart care-pi-firewall.service")
	if len(calls) != 2 || !strings.HasPrefix(calls[0], "systemd-run") {
		t.Fatalf("the timer must start before the load: %q", calls)
	}
	r = e.do("GET", "/api/v1/security/firewall", nil)
	if st := r.json(t); st["policy"] != "drop" || st["live_policy"] != "drop" || st["pending_until"] == nil {
		t.Fatalf("firewall %v", st)
	}
	r = e.do("POST", "/api/v1/security/firewall/confirm", nil)
	e.want(r, 200, "")
	if st := r.json(t); st["pending_until"] != nil || st["confirmed_at"] == nil {
		t.Fatalf("confirm %v", st)
	}
	if len(e.fake.Filter("systemctl stop care-pi-firewall-rollback.timer")) < 2 {
		t.Fatal("the confirm did not stop the timer")
	}

	// The rollback of the timer (care-pi firewall rollback --reason timeout).
	if err := e.svc.FirewallRollback(context.Background(), "timeout"); err != nil {
		t.Fatal(err)
	}
	if got := e.fake.Filter("nft "); len(got) != 1 || got[0] != "nft chain inet care-pi input { policy accept ; }" {
		t.Fatalf("nft %q", got)
	}
	cfg, err := config.Load(e.p.Abs(paths.ConfigFile))
	if err != nil || cfg.Security.Firewall.Policy != "accept" {
		t.Fatalf("config %+v %v", cfg.Security, err)
	}
	nft, _ = os.ReadFile(e.p.Abs(paths.FirewallFile))
	if !strings.Contains(string(nft), "policy accept;") {
		t.Fatalf("table:\n%s", nft)
	}
	r = e.do("GET", "/api/v1/security/firewall", nil)
	if st := r.json(t); st["policy"] != "accept" || st["live_policy"] != "accept" || st["rollback_reason"] != "timeout" || st["rolled_back_at"] == nil {
		t.Fatalf("firewall %v", st)
	}
	// The next apply renders the same table: nothing to load again.
	e.fake.Calls = nil
	e.jobOK(e.do("POST", "/api/v1/apply", map[string]any{}))
	if got := e.fake.Filter("systemctl try-reload-or-restart care-pi-firewall.service"); len(got) != 0 {
		t.Fatalf("reload after the rollback %q", got)
	}
}
