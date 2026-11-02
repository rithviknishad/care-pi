package update

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/rithviknishad/care-pi/agent/internal/apply"
	"github.com/rithviknishad/care-pi/agent/internal/host"
	"github.com/rithviknishad/care-pi/agent/internal/jobs"
	"github.com/rithviknishad/care-pi/agent/internal/lock"
	"github.com/rithviknishad/care-pi/agent/internal/paths"
	"github.com/rithviknishad/care-pi/agent/internal/problem"
	"github.com/rithviknishad/care-pi/agent/internal/update/updatetest"
)

// TestRollbackChecksTheWAFImage (m2 of the Phase 7 review): with security.waf on, a rollback to a lock whose Caddy
// image has no Coraza module (a lock of before Phase 7) refuses with unsupported-config and names the image, before
// the cutover stops anything. The check lists the modules of the image of the target, not of the running Caddy.
func TestRollbackChecksTheWAFImage(t *testing.T) {
	e := newTEnv(t, "pinned", "2026.11.0")
	e.install()
	e.writeConfig("stable", "")
	if _, err := e.u.Apply(context.Background(), e.job(jobs.KindUpdateApply), ApplyOptions{Now: true, Source: jobs.SourceCLI}); err != nil {
		t.Fatal(err)
	}
	cur := e.running()
	if cur.Solution.Version != "2026.11.1" {
		t.Fatalf("running %+v", cur.Solution)
	}
	// The previous lock has an older Caddy image.
	prevID := cur.History.PreviousID()
	prev, err := lock.ReadHistory(e.p, prevID)
	if err != nil {
		t.Fatal(err)
	}
	old := prev.Images["caddy"]
	old.Digest = updatetest.Digest("caddy-2.10-before-phase-7")
	prev.Images["caddy"] = old
	data, err := prev.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(e.p.Abs(paths.LockHistory+"/"+prevID+".yaml"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := os.ReadFile(e.cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(e.cfg, append(cfg, []byte("security:\n  waf:\n    enabled: true\n")...), 0o600); err != nil {
		t.Fatal(err)
	}
	newImage, oldImage := cur.Images["caddy"].Pull(), old.Pull()
	if newImage == oldImage || !strings.Contains(oldImage, "/caddy@sha256:") {
		t.Fatalf("images %s %s", newImage, oldImage)
	}
	e.fake.On(apply.WAFModulesCmd(newImage).String(), host.Response{Out: "dns.providers.acmedns\nhttp.handlers.waf\n"})
	e.fake.On(apply.WAFModulesCmd(oldImage).String(), host.Response{Out: "dns.providers.acmedns\ntls.get_certificate.tailscale\n"})

	e.fake.Calls = nil
	rec := e.job(jobs.KindUpdateRollback)
	_, err = e.u.Rollback(context.Background(), rec, RollbackOptions{Source: jobs.SourceConsole})
	if !problem.Is(err, problem.UnsupportedConfig) || !strings.Contains(err.Error(), oldImage) || !strings.Contains(err.Error(), "Coraza") {
		t.Fatalf("rollback: %v", err)
	}
	if got := e.fake.Filter("systemctl stop", "podman exec care-caddy"); len(got) != 0 {
		t.Fatalf("the refused rollback ran %q", got)
	}
	if l := e.running(); l.Solution.Version != "2026.11.1" {
		t.Fatalf("lock.yaml changed: %+v", l.Solution)
	}
	if a := actions(rec); len(a) != 2 || a[1] != "stage-pull" {
		t.Fatalf("steps %v", a)
	}
	// With the module in the old image, the same rollback runs.
	e.fake.Set(apply.WAFModulesCmd(oldImage).String(), host.Response{Out: "http.handlers.waf\n"})
	if _, err := e.u.Rollback(context.Background(), e.job(jobs.KindUpdateRollback), RollbackOptions{Source: jobs.SourceConsole}); err != nil {
		t.Fatal(err)
	}
	if l := e.running(); l.Solution.Version != "2026.11.0" || l.Images["caddy"].Pull() != oldImage {
		t.Fatalf("after the rollback %+v %s", l.Solution, l.Images["caddy"].Pull())
	}
}
