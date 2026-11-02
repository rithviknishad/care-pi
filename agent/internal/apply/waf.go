package apply

import (
	"context"
	"fmt"
	"strings"

	"github.com/rithviknishad/care-pi/agent/internal/host"
	"github.com/rithviknishad/care-pi/agent/internal/paths"
	"github.com/rithviknishad/care-pi/agent/internal/plan"
)

// WAFModule is the Caddy module of coraza-caddy (the directive coraza_waf).
const WAFModule = "http.handlers.waf"

// checkWAFModule stops an apply that turns on security.waf (Phase 7) when the running Caddy has no Coraza module: a
// Caddyfile with coraza_waf would stop care-caddy at its next start (an image of an older lock). Without a running
// care-caddy, or when the Caddyfile does not change, it checks nothing; a new caddy image of the same apply is not
// checked either (the reload then fails and Caddy keeps its old configuration).
func checkWAFModule(ctx context.Context, o Options, pr *Prepared, p *plan.Plan) error {
	if !pr.Model.WAF.Enabled {
		return nil
	}
	changed := false
	for _, c := range p.Changes {
		if c.Path == paths.RenderedCaddy+"/Caddyfile" {
			changed = true
		}
	}
	if !changed {
		return nil
	}
	out, err := o.Host.Runner.Run(ctx, host.Cmd{Name: "podman", Args: []string{"exec", "care-caddy", "caddy", "list-modules", "--skip-standard"}})
	if err != nil {
		_, _ = fmt.Fprintln(o.Out, "warning: cannot list the modules of care-caddy (it does not run?); security.waf needs the Coraza module of the care-pi Caddy image")
		return nil
	}
	for _, l := range strings.Split(string(out), "\n") {
		if strings.TrimSpace(l) == WAFModule {
			return nil
		}
	}
	return fmt.Errorf("security.waf.enabled is true, but the Caddy image %s has no Coraza module (%s); update to a lock with the Phase 7 Caddy image, or set security.waf.enabled: false", pr.Model.Images["caddy"], WAFModule)
}
