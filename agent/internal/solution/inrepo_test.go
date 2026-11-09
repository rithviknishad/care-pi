package solution

import (
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

// Wave 13 (S73): an in-repo plug. pip reads a fragment in package_name as a part of the path, so the version has it.
func TestPipVersion(t *testing.T) {
	sha := strings.Repeat("a", 40)
	for _, c := range []struct{ ref, sub, want string }{
		{sha, "", "@" + sha},
		{"main", "", "@main"},
		{sha, "plugs/care_pi_onboarding", "@" + sha + "#subdirectory=plugs/care_pi_onboarding"},
	} {
		if got := PipVersion(c.ref, c.sub); got != c.want {
			t.Errorf("PipVersion(%q, %q) = %q, want %q", c.ref, c.sub, got, c.want)
		}
		b := &BackendPlug{Subdirectory: c.sub}
		if got := b.Version(c.ref); got != c.want {
			t.Errorf("Version(%q) = %q, want %q", c.ref, got, c.want)
		}
	}
}

func TestInRepoPlugFields(t *testing.T) {
	var p Plug
	src := `id: care_pi_onboarding
backend:
  name: care_pi_onboarding
  package: git+https://github.com/rithviknishad/care-pi.git
  subdirectory: plugs/care_pi_onboarding
  ref: main
frontend:
  slug: care_pi_onboarding_fe
  repo: "."
  path: plugs/care_pi_onboarding_fe
  ref: main
`
	if err := yaml.Unmarshal([]byte(src), &p); err != nil {
		t.Fatal(err)
	}
	if p.Backend.Subdirectory != "plugs/care_pi_onboarding" || p.Frontend.Path != "plugs/care_pi_onboarding_fe" {
		t.Fatalf("%+v %+v", p.Backend, p.Frontend)
	}
	if p.Frontend.RepoURL() != SelfRepo || p.Backend.Package != "git+https://github.com/rithviknishad/care-pi.git" {
		t.Fatalf("repo %q", p.Frontend.RepoURL())
	}
	other := &FrontendPlug{Repo: "https://github.com/ohcnetwork/care_fasterappointments_fe"}
	if other.RepoURL() != other.Repo || (*FrontendPlug)(nil).RepoURL() != "" {
		t.Fatal("RepoURL changes a repository URL")
	}
}

// The lock schema takes the subdirectory of a backend plug, its version with the fragment, and the path of a built
// frontend bundle; a version with another fragment or a path that leaves the repository fails.
func TestLockSchemaInRepoPlug(t *testing.T) {
	sch, _ := compileSchema(t, "lock")
	raw, err := AssembleLock(lockInput(t))
	if err != nil {
		t.Fatal(err)
	}
	sha := strings.Repeat("b", 40)
	edit := func(sub, version, path string) any {
		var doc map[string]any
		if err := yaml.Unmarshal(raw, &doc); err != nil {
			t.Fatal(err)
		}
		plugs := doc["plugs"].(map[string]any)
		b := plugs["care_filly"].(map[string]any)["backend"].(map[string]any)
		b["subdirectory"] = sub
		b["additional_plug"].(map[string]any)["version"] = version
		for _, id := range []string{"care_onboarding_fe", "care_pi_onboarding"} {
			if pl, ok := plugs[id].(map[string]any); ok {
				pl["frontend"].(map[string]any)["path"] = path
			}
		}
		out, err := yaml.Marshal(doc)
		if err != nil {
			t.Fatal(err)
		}
		return yamlValue(t, string(out))
	}
	if err := sch.Validate(edit("plugs/care_filly", "@"+sha+"#subdirectory=plugs/care_filly", "plugs/care_pi_onboarding_fe")); err != nil {
		t.Fatalf("an in-repo plug: %v", err)
	}
	for name, v := range map[string]any{
		"another fragment":       edit("plugs/care_filly", "@"+sha+"#egg=x", "plugs/x"),
		"a subdirectory with ..": edit("../etc", "@"+sha+"#subdirectory=../etc", "plugs/x"),
		"an absolute path":       edit("plugs/care_filly", "@"+sha+"#subdirectory=plugs/care_filly", "/etc"),
	} {
		if err := sch.Validate(v); err == nil {
			t.Errorf("%s: want a validation error", name)
		}
	}
}
