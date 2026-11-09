package apply

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/rithviknishad/care-pi/agent/internal/buildinfo"
	"github.com/rithviknishad/care-pi/agent/internal/config"
	"github.com/rithviknishad/care-pi/agent/internal/host"
	"github.com/rithviknishad/care-pi/agent/internal/lock"
	"github.com/rithviknishad/care-pi/agent/internal/render"
	"github.com/rithviknishad/care-pi/agent/internal/solution"
	carebuild "github.com/rithviknishad/care-pi/images/care"
)

// trackLock returns the solution lock that a plain apply installs (docs/solutions.md, section 7):
// the lock of the running on-device lock, the pin, or (stable, first install) the newest lock of the
// cache and of the agent. nil keeps the Phase 1 behaviour: the refs of solution.yaml.
func trackLock(cfg *config.Config, running *lock.Lock, store solution.LockStore, rollback bool) (*solution.Lock, error) {
	name := cfg.Solution.Name
	cur := ""
	if running != nil && running.Solution.Name == name && running.Solution.Version != "" {
		cur = running.Solution.Version
	}
	get := func(v string) (*solution.Lock, error) {
		l, _, err := store.Get(name, v)
		return l, err
	}
	if rollback {
		if cur == "" {
			return nil, nil
		}
		return get(cur)
	}
	switch cfg.Solution.Track {
	case "pinned":
		l, err := get(cfg.Solution.Pin)
		if err != nil {
			return nil, fmt.Errorf("solution.pin %s: %w. Run care-pi update check, or install the lock with care-pi update apply --lock-file FILE", cfg.Solution.Pin, err)
		}
		return l, nil
	case "custom":
		v := cfg.Solution.Pin
		if v == "" {
			v = cur
		}
		if v == "" {
			return nil, nil
		}
		return get(v)
	}
	if cur != "" {
		l, err := get(cur)
		if err != nil {
			return nil, fmt.Errorf("the running solution lock %s %s is not in the cache: %w. Run care-pi update check", name, cur, err)
		}
		return l, nil
	}
	for _, v := range store.Versions(name) {
		l, err := get(v)
		if err != nil {
			continue
		}
		if old, _ := solution.AgentTooOld(buildinfo.Version, l.MinAgentVersion); old {
			continue
		}
		return l, nil
	}
	return nil, nil
}

// careBuild returns the local image build of care for the track custom (docs/solutions.md, section 13.1),
// or nil when the solution image (or the upstream image) has each backend plug that is on.
func careBuild(ctx context.Context, cfg *config.Config, sol *solution.Solution, sl *solution.Lock, prev *lock.Lock,
	r lock.Resolver, update bool) (*render.CareBuild, error) {
	if cfg.Solution.Track != "custom" || cfg.Images.Care != "" {
		return nil, nil
	}
	plugs, err := sol.Effective(cfg, len(render.Hostnames(cfg)))
	if err != nil {
		return nil, err
	}
	installed := map[string]string{}
	if sl != nil {
		for _, id := range sl.SolutionImage.InstalledPlugs {
			if lp := sl.Plugs[id]; lp.Backend != nil {
				installed[id] = lp.Backend.Ref
			}
		}
	} else {
		installed = sol.DefaultBackendPlugs()
	}
	needs, on := false, 0
	for _, p := range plugs {
		if p.On && p.HasBackend() {
			on++
			if ref, ok := installed[p.ID]; !ok || ref != p.Backend.Ref {
				needs = true
			}
		}
	}
	if cfg.Solution.Custom.CareRef != "" && on > 0 {
		needs = true
	}
	if !needs {
		return nil, nil
	}
	image := sol.Care.ImageRepo()
	baseRef := image + ":" + firstNonEmpty(sol.Care.Ref, "latest")
	if strings.HasPrefix(sol.Care.Ref, "sha256:") {
		baseRef = image + "@" + sol.Care.Ref
	}
	var seedDigest string
	switch {
	case sol.Care.Resolved != "":
		// A git source (S71): resolveGitCare resolved the ref (or solution.custom.care_ref) to the image of the commit.
		baseRef = image + ":" + sol.Care.Resolved
		if sl != nil && sl.Care.IsGit() && sl.Care.BaseRef() == baseRef {
			seedDigest = sl.Care.Digest
		}
	case cfg.Solution.Custom.CareRef != "" && strings.HasPrefix(cfg.Solution.Custom.CareRef, "sha256:"):
		baseRef = image + "@" + cfg.Solution.Custom.CareRef
	case cfg.Solution.Custom.CareRef != "":
		baseRef = image + ":" + cfg.Solution.Custom.CareRef
	case sl != nil:
		image = sl.Care.Image
		baseRef, seedDigest = image+":"+sl.Care.Ref, sl.Care.Digest
		if strings.HasPrefix(sl.Care.Ref, "sha256:") {
			baseRef = image + "@" + sl.Care.Ref
		}
	}
	w := lock.Wants{Images: map[string]string{"care-base": baseRef}, Plugs: map[string]lock.GitWant{}}
	seed := &lock.Lock{Images: map[string]lock.Image{}, Plugs: map[string]lock.GitRef{}}
	if seedDigest != "" {
		seed.Images["care-base"] = lock.Image{Ref: baseRef, Digest: seedDigest}
	}
	if prev != nil && prev.Components.Care != nil && prev.Components.Care.Build != nil && seedDigest == "" {
		c := prev.Components.Care
		if joinRef(c.Image, c.Ref) == baseRef {
			seed.Images["care-base"] = lock.Image{Ref: baseRef, Digest: c.Build.BaseDigest}
		}
	}
	if prev != nil {
		for id, g := range prev.Plugs {
			seed.Plugs[id] = g
		}
	}
	byID := map[string]solution.EffectivePlug{}
	for _, p := range plugs {
		// The superset rule: each backend plug of the solution, and each added plug that is on.
		if !p.HasBackend() || (!inSolution(sol, p.ID) && !p.On) {
			continue
		}
		byID[p.ID] = p
		repo := strings.TrimPrefix(p.Backend.Package, "git+")
		w.Plugs[p.ID] = lock.GitWant{Repo: repo, Ref: p.Backend.Ref}
		if sl != nil {
			if lp := sl.Plugs[p.ID]; lp.Backend != nil && lp.Backend.Ref == p.Backend.Ref {
				seed.Plugs[p.ID] = lock.GitRef{Repo: repo, Ref: p.Backend.Ref, SHA: lp.Backend.SHA}
			}
		}
	}
	res, err := r.Resolve(ctx, w, seed, update)
	if err != nil {
		return nil, err
	}
	base := res.Images["care-base"]
	if base.Digest == "" && !base.Local {
		return nil, fmt.Errorf("the local image build needs the digest of %s; connect the device to the internet", baseRef)
	}
	cb := &render.CareBuild{Base: image + "@" + base.Digest, Refs: map[string]lock.GitRef{}, BaseImage: image,
		BaseRef: strings.TrimPrefix(strings.TrimPrefix(baseRef, image), ":"), BaseDigest: base.Digest}
	if base.Local {
		// The base is the local build of CARE from its git source (S71): no digest.
		cb.Base = baseRef
	}
	ids := make([]string, 0, len(byID))
	for id := range byID {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		p := byID[id]
		g := res.Plugs[id]
		if g.SHA == "" {
			return nil, fmt.Errorf("the local image build needs the sha of the plug %s; connect the device to the internet", id)
		}
		cb.Refs[id] = g
		cb.Plugs = append(cb.Plugs, render.AdditionalPlug{Name: p.Backend.Name, PackageName: p.Backend.Package,
			Version: p.Backend.Version(g.SHA), Configs: map[string]any{}})
	}
	finishBuild(cb)
	return cb, nil
}

func inSolution(sol *solution.Solution, id string) bool {
	for _, p := range sol.Plugs {
		if p.ID == id {
			return true
		}
	}
	return false
}

func joinRef(image, ref string) string {
	if strings.HasPrefix(ref, "sha256:") {
		return image + "@" + ref
	}
	return image + ":" + ref
}

// finishBuild computes the canonical install list, the Containerfile hash, and the build id.
func finishBuild(cb *render.CareBuild) {
	sort.Slice(cb.Plugs, func(i, j int) bool { return cb.Plugs[i].Name < cb.Plugs[j].Name })
	plugs := cb.Plugs
	if plugs == nil {
		plugs = []render.AdditionalPlug{}
	}
	js, _ := json.Marshal(plugs)
	cb.PlugsJSON = string(js)
	sum := sha256.Sum256(carebuild.Containerfile)
	cb.ContainerfileSHA256 = hex.EncodeToString(sum[:])
	h := sha256.New()
	_, _ = fmt.Fprintf(h, "base=%s\ncontainerfile=%s\nplugs=%s\n", cb.Base, cb.ContainerfileSHA256, cb.PlugsJSON)
	cb.ID = hex.EncodeToString(h.Sum(nil))[:16]
}

// pinnedBuild rebuilds the description of the local build of a previous lock (a rollback).
func pinnedBuild(l *lock.Lock, _ *config.Config) *render.CareBuild {
	if l == nil || l.Components.Care == nil || l.Components.Care.Build == nil {
		return nil
	}
	c := l.Components.Care
	cb := &render.CareBuild{Base: c.Image + "@" + c.Build.BaseDigest, Refs: map[string]lock.GitRef{}, BaseImage: c.Image,
		BaseRef: c.Ref, BaseDigest: c.Build.BaseDigest}
	for _, id := range c.Build.InstalledPlugs {
		p, ok := l.Components.Plugs[id]
		if !ok || p.Backend == nil {
			continue
		}
		cb.Refs[id] = lock.GitRef{Repo: p.Backend.Repo, Ref: p.Backend.Ref, SHA: p.Backend.SHA}
		cb.Plugs = append(cb.Plugs, render.AdditionalPlug{Name: p.Backend.Name, PackageName: "git+" + p.Backend.Repo,
			Version: solution.PipVersion(p.Backend.SHA, p.Backend.Subdirectory), Configs: map[string]any{}})
	}
	finishBuild(cb)
	cb.ID = c.Build.ID
	return cb
}

// seedLock returns the previous identities for the resolver: the running lock, then the identities of
// the solution lock and of the local build. With refresh, the overrides of solution.custom resolve again.
func seedLock(sl *solution.Lock, w lock.Wants, prev *lock.Lock, cb *render.CareBuild, refresh bool) *lock.Lock {
	seed := &lock.Lock{Images: map[string]lock.Image{}, Git: map[string]lock.GitRef{}, Plugs: map[string]lock.GitRef{}}
	if prev != nil {
		for k, v := range prev.Images {
			seed.Images[k] = v
		}
		for k, v := range prev.Git {
			seed.Git[k] = v
		}
		for k, v := range prev.Plugs {
			seed.Plugs[k] = v
		}
	}
	if sl != nil {
		for name, ref := range w.Images {
			switch {
			case name == "care" && ref == sl.SolutionImage.Ref():
				seed.Images[name] = lock.Image{Ref: ref, Digest: sl.SolutionImage.Digest}
			case sl.Images[name].Ref == ref && sl.Images[name].Digest != "":
				seed.Images[name] = lock.Image{Ref: ref, Digest: sl.Images[name].Digest}
			}
		}
		if g, ok := w.Git["care_fe"]; ok && g.Repo == sl.CareFE.Repo && g.Ref == sl.CareFE.Ref {
			seed.Git["care_fe"] = lock.GitRef{Repo: g.Repo, Ref: g.Ref, SHA: sl.CareFE.SHA}
		}
		for id, g := range w.Plugs {
			if lp := sl.Plugs[id]; lp.Backend != nil && lp.Backend.Ref == g.Ref {
				seed.Plugs[id] = lock.GitRef{Repo: g.Repo, Ref: g.Ref, SHA: lp.Backend.SHA}
			}
		}
	}
	if cb != nil {
		for id, g := range cb.Refs {
			seed.Plugs[id] = g
		}
	}
	if refresh {
		if sl == nil || w.Git["care_fe"].Ref != sl.CareFE.Ref {
			delete(seed.Git, "care_fe")
		}
		for id, g := range w.Plugs {
			if sl == nil || sl.Plugs[id].Backend == nil || sl.Plugs[id].Backend.Ref != g.Ref {
				if cb == nil || cb.Refs[id].SHA == "" {
					delete(seed.Plugs, id)
				}
			}
		}
	}
	return seed
}

// fillComponents writes the version 2 components of the on-device lock (docs/solutions.md, section 6).
func fillComponents(lk *lock.Lock, cfg *config.Config, des *render.Plan, sl *solution.Lock, cb *render.CareBuild,
	m *render.Model, distBase string) {
	src := lock.SourceSolution
	if sl != nil {
		src = lock.SourceLock
		lk.Solution.Version, lk.Solution.Source, lk.Solution.LockSHA256 = sl.Version, lock.SourceLock, sl.SHA256
	}
	if cfg.Solution.Track == "custom" {
		lk.Solution.Source = lock.SourceCustom
	}
	if img, ok := lk.Images["care"]; ok {
		switch {
		case cb != nil:
			s := src
			if cfg.Solution.Custom.CareRef != "" {
				s = lock.SourceOperator
			}
			var ids []string
			for id := range cb.Refs {
				ids = append(ids, id)
			}
			sort.Strings(ids)
			lk.Components.Care = &lock.CareComponent{Image: cb.BaseImage, Ref: cb.BaseRef, Digest: cb.BaseDigest,
				Tracked: !strings.HasPrefix(cb.BaseRef, "sha256:"), Source: s,
				Build: &lock.LocalBuild{ID: cb.ID, BaseDigest: cb.BaseDigest, InstalledPlugs: ids, ContainerfileSHA256: cb.ContainerfileSHA256}}
		case sl != nil && img.Ref == sl.SolutionImage.Ref():
			c := &lock.CareComponent{Image: sl.Care.Image, Ref: sl.Care.Ref, Digest: sl.Care.Digest,
				Tracked: !strings.HasPrefix(sl.Care.Ref, "sha256:"), Source: lock.SourceLock}
			if sl.Care.GitSHA != nil {
				c.GitSHA = *sl.Care.GitSHA
			}
			lk.Components.Care = c
		default:
			repo, tag, digest := lock.SplitImage(img.Ref)
			s := src
			if cfg.Images.Care != "" {
				s = lock.SourceOperator
			}
			lk.Components.Care = &lock.CareComponent{Image: repo, Ref: firstNonEmpty(tag, digest), Digest: img.Digest,
				Tracked: !img.Pinned && !img.Local, Source: s}
		}
	}
	if g, ok := lk.Git["care_fe"]; ok {
		s := src
		if cfg.Solution.Track == "custom" && cfg.Solution.Custom.CareFERef != "" {
			s = lock.SourceOperator
		}
		lk.Components.CareFE = &lock.CareFEComponent{Repo: g.Repo, Ref: g.Ref, SHA: g.SHA, Tracked: !g.Pinned, Source: s}
	}
	installed := map[string]bool{}
	switch {
	case cb != nil:
		for id := range cb.Refs {
			installed[id] = true
		}
	case sl != nil:
		for _, id := range sl.SolutionImage.InstalledPlugs {
			installed[id] = true
		}
	case strings.HasPrefix(lk.Images["care"].Ref, render.ImageCareSolution+":"):
		for id := range des.Plugs {
			if des.Plugs[id].HasBackend() && !des.Plugs[id].Custom {
				installed[des.Plugs[id].ID] = true
			}
		}
	}
	plugs := map[string]lock.PlugComponent{}
	for _, p := range des.Plugs {
		pc := lock.PlugComponent{Enabled: p.On, Reason: p.Reason}
		var lp solution.LockPlug
		if sl != nil {
			lp = sl.Plugs[p.ID]
		}
		if p.HasBackend() {
			b := lock.PlugBackend{Name: p.Backend.Name, Repo: strings.TrimPrefix(p.Backend.Package, "git+"), Ref: p.Backend.Ref,
				Tracked: !host.IsFullSHA(p.Backend.Ref), Installed: installed[p.ID], Subdirectory: p.Backend.Subdirectory}
			if g, ok := lk.Plugs[p.ID]; ok {
				b.SHA = g.SHA
			} else if lp.Backend != nil && lp.Backend.Ref == p.Backend.Ref {
				b.SHA = lp.Backend.SHA
			} else if cb != nil {
				b.SHA = cb.Refs[p.ID].SHA
			}
			pc.Backend = &b
		}
		if f := p.Frontend; f != nil {
			pf := lock.PlugFrontend{Slug: f.Slug, Repo: f.RepoURL(), Ref: f.Ref, Entry: f.EntryPath(), Tracked: !host.IsFullSHA(f.Ref),
				Path: f.Path}
			lf := lp.Frontend
			switch {
			case lf != nil && lf.Source == solution.FrontendBuilt && (lf.Ref == nil || *lf.Ref == f.Ref):
				pf.Source, pf.SHA, pf.URL = solution.FrontendBuilt, deref(lf.SHA), solution.ResolveURL(distBase, lf.URL)
				pf.SHA256, pf.Entry = deref(lf.SHA256), firstNonEmpty(deref(lf.Entry), pf.Entry)
			case lf != nil && lf.Source == solution.FrontendExternal:
				pf.Source, pf.URL, pf.Tracked = solution.FrontendExternal, lf.URL, false
				pf.Ref, pf.Repo, pf.Path = "", "", ""
			case f.Repo != "" && host.IsFullSHA(f.Ref) && p.Custom:
				pf.Source, pf.SHA = solution.FrontendLocalBuild, f.Ref
			case f.URL != "":
				pf.Source, pf.URL, pf.Tracked = solution.FrontendExternal, f.URL, false
			default:
				pf.Source = solution.FrontendLocalBuild
			}
			switch {
			case pf.Source == solution.FrontendExternal:
				pf.MetaURL = pf.URL
			case pf.SHA != "" && m.Canonical != "":
				pf.MetaURL = "https://" + m.Canonical + "/plugs/" + f.Slug + "/" + pf.SHA[:12] + "/" + pf.Entry
			}
			pc.Frontend = &pf
		}
		plugs[p.ID] = pc
	}
	if len(plugs) > 0 {
		lk.Components.Plugs = plugs
	}
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func firstNonEmpty(vs ...string) string {
	for _, v := range vs {
		if v != "" {
			return v
		}
	}
	return ""
}
