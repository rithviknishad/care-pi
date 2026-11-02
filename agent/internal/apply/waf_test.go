package apply

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/rithviknishad/care-pi/agent/internal/host"
	"github.com/rithviknishad/care-pi/agent/internal/paths"
	"github.com/rithviknishad/care-pi/agent/internal/plan"
	"github.com/rithviknishad/care-pi/agent/internal/render"
)

// TestWAFModuleCheck: an apply that turns on the WAF needs the Coraza module in the running Caddy (Phase 7).
func TestWAFModuleCheck(t *testing.T) {
	list := "podman exec care-caddy caddy list-modules --skip-standard"
	pr := &Prepared{Model: &render.Model{WAF: render.WAFModel{Enabled: true}, Images: map[string]string{"caddy": "ghcr.io/x/caddy@sha256:1"}}}
	caddyChange := &plan.Plan{Changes: []plan.Change{{Path: paths.RenderedCaddy + "/Caddyfile", Kind: plan.Modify}}}
	f := host.NewFake()
	o := Options{Host: host.New(f), Out: &bytes.Buffer{}}

	f.On(list, host.Response{Out: "dns.providers.acmedns\ntls.get_certificate.tailscale\n"})
	err := checkWAFModule(context.Background(), o, pr, caddyChange)
	if err == nil || !strings.Contains(err.Error(), "no Coraza module") {
		t.Fatalf("old image: %v", err)
	}
	f.Set(list, host.Response{Out: "dns.providers.acmedns\nhttp.handlers.waf\n"})
	if err := checkWAFModule(context.Background(), o, pr, caddyChange); err != nil {
		t.Fatal(err)
	}
	// No running Caddy: a warning, no error. No Caddyfile change, or no WAF: no check.
	f.Set(list, host.Response{Code: 125})
	if err := checkWAFModule(context.Background(), o, pr, caddyChange); err != nil {
		t.Fatal(err)
	}
	f.Calls = nil
	if err := checkWAFModule(context.Background(), o, pr, &plan.Plan{}); err != nil || len(f.Calls) != 0 {
		t.Fatalf("no change: %v %v", err, f.Calls)
	}
	pr.Model.WAF.Enabled = false
	if err := checkWAFModule(context.Background(), o, pr, caddyChange); err != nil || len(f.Calls) != 0 {
		t.Fatalf("no WAF: %v %v", err, f.Calls)
	}
}
