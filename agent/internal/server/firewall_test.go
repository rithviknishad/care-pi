package server

import (
	"bytes"
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
	// M1 of the Phase 7 review: the boot file keeps accept until the confirmation; the drop table of the trial is the
	// pending file, and the apply loads it with nft -f.
	nft, _ := os.ReadFile(e.p.Abs(paths.FirewallFile))
	pending, _ := os.ReadFile(e.p.Abs(paths.FirewallPendingFile))
	if !strings.Contains(string(nft), "policy accept;") || !strings.Contains(string(pending), "policy drop;") || !strings.Contains(string(pending), "tcp dport 22 accept") {
		t.Fatalf("boot table:\n%s\npending table:\n%s", nft, pending)
	}
	calls := e.fake.Filter("systemd-run --quiet --collect --unit="+firewall.RollbackUnit, "systemctl try-reload-or-restart care-pi-firewall.service", "nft ")
	if len(calls) != 2 || !strings.HasPrefix(calls[0], "systemd-run") || calls[1] != "nft -f "+paths.FirewallPendingFile {
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
	nft, _ = os.ReadFile(e.p.Abs(paths.FirewallFile))
	if !strings.Contains(string(nft), "policy drop;") || fileExists(e.p.Abs(paths.FirewallPendingFile)) {
		t.Fatalf("the confirmation must move the pending table over the boot file:\n%s", nft)
	}
	// The next apply renders the confirmed drop to the boot file: nothing changes, nothing loads.
	e.fake.Calls = nil
	e.jobOK(e.do("POST", "/api/v1/apply", map[string]any{}))
	if got := e.fake.Filter("systemctl try-reload-or-restart care-pi-firewall.service", "nft ", "systemd-run --quiet --collect --unit="+firewall.RollbackUnit); len(got) != 0 {
		t.Fatalf("a load after the confirmation %q", got)
	}

	// The rollback of the timer (care-pi firewall rollback --reason timeout).
	e.fake.Calls = nil
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

// TestFirewallRebootInTheWindow (M1 of the Phase 7 review): a reboot before the confirmation loses the transient
// timer. The boot loads the boot file (accept), and "care-pi firewall boot-check" (ExecStartPre of the unit) ends the
// trial as the rollback does: config.yaml, the pending file, and state/firewall.json. A later confirmation refuses.
func TestFirewallRebootInTheWindow(t *testing.T) {
	e, _ := readyConnEnv(t)
	e.fake.On("systemctl show -p ActiveState care-pi-firewall.service", host.Response{Out: "ActiveState=active\n"})
	etag := e.do("GET", "/api/v1/config", nil).header.Get("ETag")
	r := e.do("PATCH", "/api/v1/config?apply=true", map[string]any{"security": map[string]any{"firewall": map[string]any{"policy": "drop"}}},
		header("Content-Type", "application/merge-patch+json"), header("If-Match", etag))
	e.want(r, 202, "")
	job, _ := r.json(t)["job"].(map[string]any)
	if j := e.waitJob(job["id"].(string)); j.State != jobs.StateSucceeded {
		t.Fatalf("apply %s %+v", j.State, j.Error)
	}
	if st := firewall.Read(e.p); st.PendingUntil == nil {
		t.Fatalf("no trial %+v", st)
	}
	drop, _ := os.ReadFile(e.p.Abs(paths.FirewallPendingFile))
	unit, _ := os.ReadFile(e.p.Abs(paths.FirewallDropIn))
	if !strings.Contains(string(unit), "ExecStartPre=-"+paths.Binary+" firewall boot-check") {
		t.Fatalf("drop-in:\n%s", unit)
	}

	// The reboot: the boot check of a new process.
	e.fake.Calls = nil
	stale, err := e.svc.FirewallBootCheck()
	if err != nil || !stale {
		t.Fatalf("boot check %v %v", stale, err)
	}
	if len(e.fake.Calls) != 0 {
		t.Fatalf("the boot check runs no host command (systemd waits for it): %q", e.fake.Calls)
	}
	nft, _ := os.ReadFile(e.p.Abs(paths.FirewallFile))
	if !strings.Contains(string(nft), "policy accept;") || fileExists(e.p.Abs(paths.FirewallPendingFile)) {
		t.Fatalf("the boot must load accept:\n%s", nft)
	}
	cfg, err := config.Load(e.p.Abs(paths.ConfigFile))
	if err != nil || cfg.Security.Firewall.Policy != "accept" {
		t.Fatalf("config %+v %v", cfg.Security, err)
	}
	st := firewall.Read(e.p)
	if st.PendingUntil != nil || st.Policy != "accept" || st.RollbackReason != "timeout" || st.RolledBackAt == nil {
		t.Fatalf("state %+v", st)
	}
	// A confirmation after the reboot changes nothing, also with the old drop table back in place.
	must(t, os.WriteFile(e.p.Abs(paths.FirewallPendingFile), drop, 0o644))
	e.want(e.do("POST", "/api/v1/security/firewall/confirm", nil), 409, "state-conflict")
	if nft2, _ := os.ReadFile(e.p.Abs(paths.FirewallFile)); !bytes.Equal(nft, nft2) {
		t.Fatal("a refused confirmation changed the boot file")
	}
	must(t, os.Remove(e.p.Abs(paths.FirewallPendingFile)))
	// The next apply renders the same files: nothing loads. A second boot check has nothing to do.
	e.fake.Calls = nil
	e.jobOK(e.do("POST", "/api/v1/apply", map[string]any{}))
	if got := e.fake.Filter("systemctl try-reload-or-restart care-pi-firewall.service", "nft ", "systemd-run"); len(got) != 0 {
		t.Fatalf("a load after the boot check %q", got)
	}
	if stale, err := e.svc.FirewallBootCheck(); err != nil || stale {
		t.Fatalf("second boot check %v %v", stale, err)
	}
}

// TestFirewallAllowPorts (S64): security.firewall.allow_ports goes into the table (a reload-firewall) and into
// GET /security/firewall; a port that care-pi already accepts gets 422 invalid-config.
func TestFirewallAllowPorts(t *testing.T) {
	e, _ := readyConnEnv(t)
	e.fake.On("systemctl show -p ActiveState care-pi-firewall.service", host.Response{Out: "ActiveState=active\n"})
	r := e.do("GET", "/api/v1/security/firewall", nil)
	e.want(r, 200, "")
	if st := r.json(t); len(st["allow_ports"].([]any)) != 0 {
		t.Fatalf("firewall %v", st)
	}
	patch := func(ports []any, opts ...reqOpt) *resp {
		etag := e.do("GET", "/api/v1/config", nil).header.Get("ETag")
		return e.do("PATCH", "/api/v1/config?apply=true", map[string]any{"security": map[string]any{"firewall": map[string]any{"allow_ports": ports}}},
			append([]reqOpt{header("Content-Type", "application/merge-patch+json"), header("If-Match", etag)}, opts...)...)
	}
	r = patch([]any{map[string]any{"port": 443}}, invalid)
	e.want(r, 422, "invalid-config")
	if !strings.Contains(string(r.body), "443/tcp is already allowed by care-pi") {
		t.Fatalf("422 %s", r.body)
	}
	e.fake.Calls = nil
	r = patch([]any{
		map[string]any{"port": 9100, "iface": "tailscale0", "comment": `node "exporter"`},
		map[string]any{"port": 9256, "proto": "udp"},
	})
	e.want(r, 202, "")
	job, _ := r.json(t)["job"].(map[string]any)
	if j := e.waitJob(job["id"].(string)); j.State != jobs.StateSucceeded {
		t.Fatalf("apply %s %+v", j.State, j.Error)
	}
	if got := e.fake.Filter("systemctl try-reload-or-restart care-pi-firewall.service"); len(got) != 1 {
		t.Fatalf("reload-firewall %q", got)
	}
	nft, _ := os.ReadFile(e.p.Abs(paths.FirewallFile))
	for _, want := range []string{"iifname \"tailscale0\" tcp dport 9100 accept comment \"node exporter\"\n", "    udp dport 9256 accept\n"} {
		if !strings.Contains(string(nft), want) {
			t.Fatalf("no %q in:\n%s", want, nft)
		}
	}
	r = e.do("GET", "/api/v1/security/firewall", nil)
	ports, _ := r.json(t)["allow_ports"].([]any)
	if len(ports) != 2 {
		t.Fatalf("allow_ports %s", r.body)
	}
	a, b := ports[0].(map[string]any), ports[1].(map[string]any)
	if a["port"] != 9100.0 || a["proto"] != "tcp" || a["iface"] != "tailscale0" || a["comment"] != `node "exporter"` ||
		b["proto"] != "udp" || b["iface"] != nil || b["comment"] != nil {
		t.Fatalf("allow_ports %v", ports)
	}
}

func fileExists(f string) bool {
	_, err := os.Stat(f)
	return err == nil
}
