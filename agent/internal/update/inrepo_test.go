package update

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/rithviknishad/care-pi/agent/internal/apply"
	"github.com/rithviknishad/care-pi/agent/internal/host"
	"github.com/rithviknishad/care-pi/agent/internal/lock"
	"github.com/rithviknishad/care-pi/agent/internal/paths"
	"github.com/rithviknishad/care-pi/agent/internal/render"
	"github.com/rithviknishad/care-pi/agent/internal/solution"
)

// Wave 13 (S73): a local build of an in-repo FE plug: repo "." is the repository of care-pi, and the path goes to
// plug-build.sh (PLUG_PATH). A plug in the root of its repository has no PLUG_PATH line.
func TestPlugBuildInRepo(t *testing.T) {
	sha := strings.Repeat("c", 40)
	pr := &apply.Prepared{
		Desire: &render.Plan{Plugs: []solution.EffectivePlug{
			{Plug: solution.Plug{ID: "care_pi_onboarding", Frontend: &solution.FrontendPlug{Slug: "care_pi_onboarding_fe", Repo: ".", Path: "plugs/care_pi_onboarding_fe"}}, On: true},
			{Plug: solution.Plug{ID: "other", Frontend: &solution.FrontendPlug{Slug: "other_fe", Repo: "https://github.com/example/other_fe"}}, On: true},
		}},
		Lock: &lock.Lock{Components: lock.Components{Plugs: map[string]lock.PlugComponent{
			"care_pi_onboarding": {Enabled: true, Frontend: &lock.PlugFrontend{Slug: "care_pi_onboarding_fe", Source: solution.FrontendLocalBuild,
				Repo: ".", SHA: sha, Entry: "assets/remoteEntry.js", Path: "plugs/care_pi_onboarding_fe"}},
			"other": {Enabled: true, Frontend: &lock.PlugFrontend{Slug: "other_fe", Source: solution.FrontendLocalBuild,
				Repo: "https://github.com/example/other_fe", SHA: sha, Entry: "assets/remoteEntry.js"}},
		}}},
		Model: &render.Model{Images: map[string]string{"node": "docker.io/library/node@sha256:" + strings.Repeat("d", 64)}},
	}
	p := paths.New(t.TempDir())
	reqs, err := PlugBuilder{Paths: p}.Requests(pr)
	if err != nil || len(reqs) != 2 {
		t.Fatalf("%v %+v", err, reqs)
	}
	byID := map[string]PlugBuildRequest{}
	for _, r := range reqs {
		byID[r.Slug] = r
	}
	if r := byID["care_pi_onboarding_fe"]; r.Repo != "rithviknishad/care-pi" || r.Path != "plugs/care_pi_onboarding_fe" {
		t.Fatalf("in-repo %+v", r)
	}
	if r := byID["other_fe"]; r.Repo != "example/other_fe" || r.Path != "" {
		t.Fatalf("other %+v", r)
	}
	if err := os.MkdirAll(p.Abs(paths.BuildPlugs), 0o755); err != nil {
		t.Fatal(err)
	}
	fake := host.NewFake()
	fake.On("systemctl start care-plug-build.service", host.Response{Err: errors.New("stop here")})
	prov := LocalPlugProvider{Paths: p, Host: host.New(fake), Out: &strings.Builder{}}
	for _, c := range []struct {
		r    PlugBuildRequest
		path bool
	}{{byID["care_pi_onboarding_fe"], true}, {byID["other_fe"], false}} {
		_ = prov.Ensure(context.Background(), c.r)
		env, err := os.ReadFile(p.Abs(paths.BuildPlugs + "/current.env"))
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(env), "PLUG_PATH=plugs/care_pi_onboarding_fe\n") != c.path || (!c.path && strings.Contains(string(env), "PLUG_PATH")) {
			t.Fatalf("%s: current.env\n%s", c.r.Slug, env)
		}
	}
}
