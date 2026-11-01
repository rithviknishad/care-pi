package server

import (
	"os"
	"testing"
	"time"

	"github.com/rithviknishad/care-pi/agent/internal/paths"
)

// TestMaintenanceState: the public status and the status report the maintenance page of the
// flag file (schema MaintenanceState, contract 0.5.0). The maintenance page of Caddy polls it.
func TestMaintenanceState(t *testing.T) {
	e := newEnv(t)
	r := e.do("GET", "/_care-pi/status", nil)
	e.want(r, 200, "")
	if m := r.json(t)["maintenance"].(map[string]any); m["active"] != false || m["started_at"] != nil {
		t.Fatalf("maintenance without the flag file: %v", m)
	}
	must(t, os.MkdirAll(e.p.Abs(paths.ModeDir), 0o755))
	must(t, os.WriteFile(e.p.Abs(paths.ModeDir+"/"+paths.ModeMaintenance), nil, 0o644))
	e.clock.add(3 * time.Second)
	r = e.do("GET", "/_care-pi/status", nil)
	e.want(r, 200, "")
	pub := r.json(t)
	if m := pub["maintenance"].(map[string]any); pub["site"] != "maintenance" || m["active"] != true || m["started_at"] == nil || m["job_id"] != nil {
		t.Fatalf("maintenance with the flag file: %v", pub)
	}
	e.claim()
	r = e.do("GET", "/api/v1/status", nil)
	e.want(r, 200, "")
	if m := r.json(t)["maintenance"].(map[string]any); m["active"] != true {
		t.Fatalf("status maintenance: %v", m)
	}
}
