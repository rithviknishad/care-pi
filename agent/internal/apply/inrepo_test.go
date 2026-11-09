package apply

import (
	"strings"
	"testing"

	"github.com/rithviknishad/care-pi/agent/internal/lock"
)

// Wave 13 (S73): the rollback to a lock with a local build of an in-repo backend plug keeps @<sha>#subdirectory=<dir>;
// a plug without a subdirectory keeps @<sha>.
func TestPinnedBuildSubdirectory(t *testing.T) {
	sha := strings.Repeat("a", 40)
	l := &lock.Lock{Components: lock.Components{
		Care: &lock.CareComponent{Image: "ghcr.io/ohcnetwork/care", Ref: "latest", Digest: "sha256:" + strings.Repeat("1", 64),
			Build: &lock.LocalBuild{ID: "0123456789abcdef", BaseDigest: "sha256:" + strings.Repeat("1", 64),
				InstalledPlugs: []string{"care_multihost_files_be", "care_pi_onboarding"}}},
		Plugs: map[string]lock.PlugComponent{
			"care_pi_onboarding": {Backend: &lock.PlugBackend{Name: "care_pi_onboarding", Repo: "https://github.com/rithviknishad/care-pi.git",
				Ref: "main", SHA: sha, Subdirectory: "plugs/care_pi_onboarding"}},
			"care_multihost_files_be": {Backend: &lock.PlugBackend{Name: "care_multihost_files_be",
				Repo: "https://github.com/ohcnetwork/care_multihost_files_be.git", Ref: "main", SHA: sha}},
		}}}
	cb := pinnedBuild(l, nil)
	want := map[string]string{
		"care_pi_onboarding":      "git+https://github.com/rithviknishad/care-pi.git @" + sha + "#subdirectory=plugs/care_pi_onboarding",
		"care_multihost_files_be": "git+https://github.com/ohcnetwork/care_multihost_files_be.git @" + sha,
	}
	if len(cb.Plugs) != 2 {
		t.Fatalf("plugs %+v", cb.Plugs)
	}
	for _, p := range cb.Plugs {
		if got := p.PackageName + " " + p.Version; got != want[p.Name] {
			t.Errorf("%s: %s, want %s", p.Name, got, want[p.Name])
		}
	}
	if !strings.Contains(cb.PlugsJSON, `"version":"@`+sha+`#subdirectory=plugs/care_pi_onboarding"`) {
		t.Fatalf("install-plugs.json %s", cb.PlugsJSON)
	}
}

// The on-device lock records the subdirectory of a backend plug and the path of a frontend plug; an empty field is
// not written, so a lock without in-repo plugs keeps its bytes.
func TestLockComponentsInRepoFields(t *testing.T) {
	l := &lock.Lock{Version: lock.FileVersion, Components: lock.Components{Plugs: map[string]lock.PlugComponent{
		"a": {Enabled: true, Backend: &lock.PlugBackend{Name: "a", Ref: "main", SHA: strings.Repeat("a", 40), Subdirectory: "plugs/a"},
			Frontend: &lock.PlugFrontend{Slug: "a_fe", Source: "built", Repo: "https://github.com/rithviknishad/care-pi", Path: "plugs/a_fe"}},
		"b": {Enabled: true, Backend: &lock.PlugBackend{Name: "b", Ref: "main", SHA: strings.Repeat("b", 40)},
			Frontend: &lock.PlugFrontend{Slug: "b_fe", Source: "external", URL: "https://example.org/remoteEntry.js"}},
	}}}
	data, err := l.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	s := string(data)
	if strings.Count(s, "subdirectory: plugs/a") != 1 || strings.Count(s, "path: plugs/a_fe") != 1 ||
		strings.Count(s, "subdirectory:") != 1 || strings.Count(s, "path:") != 1 {
		t.Fatalf("lock:\n%s", s)
	}
	back, err := lock.Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	if back.Components.Plugs["a"].Backend.Subdirectory != "plugs/a" || back.Components.Plugs["a"].Frontend.Path != "plugs/a_fe" {
		t.Fatalf("parse %+v", back.Components.Plugs["a"])
	}
}
