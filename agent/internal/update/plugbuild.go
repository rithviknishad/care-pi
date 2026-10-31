package update

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/rithviknishad/care-pi/agent/internal/apply"
	"github.com/rithviknishad/care-pi/agent/internal/config"
	"github.com/rithviknishad/care-pi/agent/internal/fe"
	"github.com/rithviknishad/care-pi/agent/internal/host"
	"github.com/rithviknishad/care-pi/agent/internal/lock"
	"github.com/rithviknishad/care-pi/agent/internal/paths"
	"github.com/rithviknishad/care-pi/agent/internal/problem"
	"github.com/rithviknishad/care-pi/agent/internal/render"
	"github.com/rithviknishad/care-pi/agent/internal/solution"
)

// PlugBuildRequest is one FE plug bundle to build (docs/solutions.md, section 13.2).
type PlugBuildRequest struct {
	Slug string
	// Repo is OWNER/NAME on github.com.
	Repo   string
	SHA    string
	Script string
	Out    string
	Entry  string
	// Image is the node image of the builder.
	Image string
}

// Dir returns the host path of the bundle.
func (r PlugBuildRequest) Dir() string { return paths.PlugsDir + "/" + r.Slug + "/" + r.SHA[:12] }

// PlugBundleProvider makes an FE plug bundle that has no CI artefact. The local provider is experimental.
type PlugBundleProvider interface {
	Name() string
	Ensure(ctx context.Context, r PlugBuildRequest) error
}

// LocalPlugProvider builds a plug bundle in care-plug-build.service (node image, plug-build.sh) with
// the scale-down rule of the FE build.
type LocalPlugProvider struct {
	Paths  paths.Paths
	Host   *host.Host
	Config *config.Config
	Out    io.Writer
}

// Name implements PlugBundleProvider.
func (LocalPlugProvider) Name() string { return "local" }

// Ensure implements PlugBundleProvider.
func (l LocalPlugProvider) Ensure(ctx context.Context, r PlugBuildRequest) error {
	env := fmt.Sprintf("PLUG_REPO=%s\nPLUG_SHA=%s\nPLUG_SLUG=%s\nPLUG_SCRIPT=%s\nPLUG_OUT=%s\nPLUG_ENTRY=%s\n",
		r.Repo, r.SHA, r.Slug, r.Script, r.Out, r.Entry)
	if err := lock.WriteFileAtomic(l.Paths.Abs(paths.BuildPlugs+"/current.env"), []byte(env), 0o644); err != nil {
		return err
	}
	e := fe.Env{Config: l.Config, Paths: l.Paths, Host: l.Host, Out: l.Out}
	return fe.RunScaled(ctx, e, r.Image, "care-plug-build.service", "the FE plug build "+r.Slug, 30*time.Minute)
}

// PlugBuilder builds the FE plug bundles of a target that have no CI artefact (source local-build).
type PlugBuilder struct {
	Paths    paths.Paths
	Host     *host.Host
	Config   *config.Config
	Out      io.Writer
	Provider PlugBundleProvider
}

// Requests returns the bundles that a target needs and that are not on disk.
func (b PlugBuilder) Requests(pr *apply.Prepared) ([]PlugBuildRequest, error) {
	var out []PlugBuildRequest
	plugs := map[string]solution.EffectivePlug{}
	for _, p := range pr.Desire.Plugs {
		plugs[p.ID] = p
	}
	for _, id := range sortedKeys(pr.Lock.Components.Plugs) {
		c := pr.Lock.Components.Plugs[id]
		f := c.Frontend
		if !c.Enabled || f == nil || f.Source != solution.FrontendLocalBuild {
			continue
		}
		if f.SHA == "" {
			return nil, problem.Newf(problem.BuildRequiredOffline, "The FE plug %s has no resolved sha; set solution.custom.plugs[].frontend.ref to a 40-digit sha.", id)
		}
		p := plugs[id]
		repo := render.GitHubSlug(strings.TrimSuffix(f.Repo, ".git"))
		if repo == "" {
			return nil, problem.Newf(problem.ArtefactInvalid, "The FE plug %s needs a github.com repository for a local build.", id)
		}
		r := PlugBuildRequest{Slug: f.Slug, Repo: repo, SHA: f.SHA, Script: p.Frontend.BuildScript(), Out: p.Frontend.BuildOut(),
			Entry: f.Entry, Image: pr.Model.Images["node"]}
		if _, err := os.Stat(b.Paths.Abs(r.Dir() + "/" + r.Entry)); err == nil {
			continue
		}
		out = append(out, r)
	}
	return out, nil
}

// EnsureAll builds each missing bundle. It returns the built bundles (plugs/<slug>/<sha12>).
func (b PlugBuilder) EnsureAll(ctx context.Context, pr *apply.Prepared) ([]string, error) {
	reqs, err := b.Requests(pr)
	if err != nil || len(reqs) == 0 {
		return nil, err
	}
	if pr.Config.FE.Provider != "local" {
		return nil, problem.Newf(problem.ArtefactInvalid, "The FE plug %s has no CI bundle for %s. Set fe.provider: local to build it on the device (experimental).", reqs[0].Slug, reqs[0].SHA[:12])
	}
	prov := b.Provider
	if prov == nil {
		prov = LocalPlugProvider{Paths: b.Paths, Host: b.Host, Config: b.Config, Out: b.Out}
	}
	var built []string
	for _, r := range reqs {
		if err := prov.Ensure(ctx, r); err != nil {
			return built, err
		}
		if _, err := os.Stat(b.Paths.Abs(r.Dir() + "/" + r.Entry)); err != nil {
			return built, fmt.Errorf("care-plug-build.service completed, but %s/%s does not exist", r.Dir(), r.Entry)
		}
		built = append(built, "plugs/"+r.Slug+"/"+r.SHA[:12])
	}
	return built, nil
}
