package apply

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/rithviknishad/care-pi/agent/internal/host"
	"github.com/rithviknishad/care-pi/agent/internal/paths"
	"github.com/rithviknishad/care-pi/agent/internal/plan"
	"github.com/rithviknishad/care-pi/agent/internal/problem"
	"github.com/rithviknishad/care-pi/agent/internal/render"
)

// TestWAFModuleCheck: an apply that turns on the WAF needs the Coraza module in the Caddy image of the target, not in
// the running Caddy (Phase 7; m2 of the review).
func TestWAFModuleCheck(t *testing.T) {
	const image = "ghcr.io/x/caddy@sha256:1"
	list := WAFModulesCmd(image).String()
	if list != "systemd-run --quiet --wait --pipe --collect --property=MemoryMax=512M --description=care-pi caddy modules -- podman run --rm --network none "+image+" caddy list-modules --skip-standard" {
		t.Fatalf("command %q", list)
	}
	pr := &Prepared{Model: &render.Model{WAF: render.WAFModel{Enabled: true}, Images: map[string]string{"caddy": image}}}
	caddyChange := &plan.Plan{Changes: []plan.Change{{Path: paths.RenderedCaddy + "/Caddyfile", Kind: plan.Modify}}}
	imageChange := &plan.Plan{Changes: []plan.Change{{Path: paths.QuadletDir + "/care-caddy.container", Kind: plan.Modify}}}
	f := host.NewFake()
	o := Options{Host: host.New(f), Out: &bytes.Buffer{}}

	f.On(list, host.Response{Out: "dns.providers.acmedns\ntls.get_certificate.tailscale\n"})
	for _, p := range []*plan.Plan{caddyChange, imageChange} {
		err := checkWAFModule(context.Background(), o, pr, p)
		if !problem.Is(err, problem.UnsupportedConfig) || !strings.Contains(err.Error(), "no Coraza module") || !strings.Contains(err.Error(), image) {
			t.Fatalf("old image: %v", err)
		}
	}
	if got := f.Filter("podman exec"); len(got) != 0 {
		t.Fatalf("the check must not read the running Caddy: %q", got)
	}
	f.Set(list, host.Response{Out: "dns.providers.acmedns\nhttp.handlers.waf\n"})
	if err := checkWAFModule(context.Background(), o, pr, caddyChange); err != nil {
		t.Fatal(err)
	}
	// The modules cannot be listed: a warning, no error. No Caddy change, or no WAF: no check.
	f.Set(list, host.Response{Code: 125})
	if err := checkWAFModule(context.Background(), o, pr, caddyChange); err != nil || !strings.Contains(o.Out.(*bytes.Buffer).String(), "warning") {
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
