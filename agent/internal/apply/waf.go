package apply

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/rithviknishad/care-pi/agent/internal/host"
	"github.com/rithviknishad/care-pi/agent/internal/paths"
	"github.com/rithviknishad/care-pi/agent/internal/plan"
	"github.com/rithviknishad/care-pi/agent/internal/problem"
)

// WAFModule is the Caddy module of coraza-caddy (the directive coraza_waf).
const WAFModule = "http.handlers.waf"

// WAFModulesCmd lists the Caddy modules of an image: "caddy list-modules" in a container without a network and without
// a volume, in a transient unit (the memory of a pull, as restore.PodmanRun).
func WAFModulesCmd(image string) host.Cmd {
	return host.Cmd{Name: "systemd-run", Args: []string{"--quiet", "--wait", "--pipe", "--collect", "--property=MemoryMax=512M",
		"--description=care-pi caddy modules", "--", "podman", "run", "--rm", "--network", "none", image, "caddy", "list-modules", "--skip-standard"}}
}

// CheckWAFImage refuses security.waf.enabled (Phase 7) when the Caddy image of the target (pr.Model.Images["caddy"])
// has no Coraza module: a Caddyfile with coraza_waf stops care-caddy at its next start (an image of an older lock, for
// example after a rollback), and the whole stack with it. The error is unsupported-config and names the image. When
// the modules cannot be listed (no podman, an image that does not pull), it writes a warning and checks nothing.
func CheckWAFImage(ctx context.Context, h *host.Host, out io.Writer, pr *Prepared) error {
	if !pr.Model.WAF.Enabled {
		return nil
	}
	image := pr.Model.Images["caddy"]
	list, err := h.Runner.Run(ctx, WAFModulesCmd(image))
	if err != nil {
		_, _ = fmt.Fprintf(out, "warning: cannot list the modules of the Caddy image %s (%v); security.waf needs the Coraza module of the care-pi Caddy image\n", image, err)
		return nil
	}
	for _, l := range strings.Split(string(list), "\n") {
		if strings.TrimSpace(l) == WAFModule {
			return nil
		}
	}
	return problem.Newf(problem.UnsupportedConfig, "security.waf.enabled is true, but the Caddy image %s has no Coraza module (%s), so care-caddy would not start. "+
		"Use a lock with the Phase 7 Caddy image, or set security.waf.enabled: false.", image, WAFModule)
}

// checkWAFModule runs CheckWAFImage for an apply that changes the Caddyfile or the image of care-caddy.
func checkWAFModule(ctx context.Context, o Options, pr *Prepared, p *plan.Plan) error {
	for _, c := range p.Changes {
		if c.Path == paths.RenderedCaddy+"/Caddyfile" || c.Path == paths.QuadletDir+"/care-caddy.container" {
			return CheckWAFImage(ctx, o.Host, o.Out, pr)
		}
	}
	return nil
}
