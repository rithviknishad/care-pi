package solution

import (
	"os"
	"strings"
	"testing"

	"github.com/rithviknishad/care-pi/agent/internal/config"
)

func TestVersions(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"2026.11.1", "2026.11.0", 1}, {"2026.1.0", "2026.01.0", 0}, {"2026.12.0", "2027.01.0", -1}, {"2026.11.10", "2026.11.9", 1}, {"bad", "2026.11.0", -1},
	}
	for _, c := range cases {
		if got := CompareLockVersions(c.a, c.b); got != c.want {
			t.Errorf("compare %s %s = %d, want %d", c.a, c.b, got, c.want)
		}
	}
	if NormalizeLockVersion("2026.1.3") != "2026.01.3" {
		t.Fatal("normalize")
	}
	for v, want := range map[string]bool{"v0.1.0": true, "v10.20.30": true, "0.1.0": false, "v0.1.0-dirty": false,
		"v0.1.0-3-g9ff6634": false, "9ff6634": false, "dev": false, "v0.1": false} {
		if IsRelease(v) != want {
			t.Errorf("IsRelease(%q) = %v", v, !want)
		}
	}
	// Only a release build compares; a dev build (git describe after the tag, -dirty, a commit, "dev") is the newest.
	for _, c := range []struct {
		agent, min string
		old        bool
		warn       bool
	}{
		{"v0.4.0", "v0.4.0", false, false}, {"v0.3.9", "v0.4.0", true, false}, {"v0.4.0-12-gabcdef-dirty", "v0.4.0", false, true},
		{"v0.3.0-1-gabc", "v0.4.0", false, true}, {"dev", "v0.4.0", false, true}, {"v1.0.0", "v0.9.9", false, false},
		{"v0.1.0", "v0.2.0", true, false}, {"v0.1.0", "v0.1.0", false, false}, {"v0.2.0", "v0.1.0", false, false},
		{"v0.1.0-dirty", "v0.2.0", false, true}, {"9ff6634", "v0.2.0", false, true}, {"9ff6634-dirty", "v0.2.0", false, true},
		{"v0.1.0", "", false, false}, {"v0.10.0", "v0.9.0", false, false},
	} {
		old, warn := AgentTooOld(c.agent, c.min)
		if old != c.old || (warn != "") != c.warn {
			t.Errorf("AgentTooOld(%s, %s) = %v %q", c.agent, c.min, old, warn)
		}
	}
}

func lockInput(t *testing.T) LockInput {
	t.Helper()
	data, err := os.ReadFile("../../../solutions/care-clinic/solution.yaml")
	if err != nil {
		t.Fatal(err)
	}
	d := "sha256:" + strings.Repeat("a", 64)
	sha := strings.Repeat("b", 40)
	in := LockInput{Solution: data, Version: "2026.11.0", CreatedAt: "2026-11-14T10:00:00Z", CreatedBy: "test", MinAgentVersion: "v0.4.0",
		Commit: sha, CareFESHA: sha, Plugs: map[string]PlugInput{
			"care_onboarding_fe":         {FrontendSHA: sha, FrontendURL: "plug-care_onboarding_fe-bbbbbbbbbbbb/plug-care_onboarding_fe-bbbbbbbbbbbb.tar.gz", FrontendSHA256: strings.Repeat("c", 64), FrontendSizeBytes: 10},
			"care_multihost_files_be":    {BackendSHA: sha},
			"care_fasterappointments_fe": {},
			"care_filly":                 {BackendSHA: sha},
		}, Images: map[string]LockImg{}}
	in.Care.Digest = d
	in.SolutionImage.Image, in.SolutionImage.Tag, in.SolutionImage.Digest = "ghcr.io/rithviknishad/care-pi/care", "care-clinic-2026.11.0", d
	in.FE = LockFE{BuildID: "0123456789abcdef", URL: "fe-care-clinic-bbbbbbbbbbbb-0123456789abcdef/care_fe.tar.gz", SHA256: strings.Repeat("d", 64), SizeBytes: 5, EnvKeys: []string{"REACT_CARE_API_URL"}}
	for _, n := range []string{"postgres", "caddy", "redis", "s3", "node"} {
		in.Images[n] = LockImg{Ref: "example.org/" + n + ":1", Digest: d}
	}
	return in
}

// TestAssembleLockAndIndex: the lock passes lock.schema.json, installs each backend plug (the superset
// rule), and the index lists it; a withdrawn lock is not latest.
func TestAssembleLockAndIndex(t *testing.T) {
	in := lockInput(t)
	data, err := AssembleLock(in)
	if err != nil {
		t.Fatal(err)
	}
	l, err := ParseLock(data)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(l.SolutionImage.InstalledPlugs, ","); got != "care_filly,care_multihost_files_be" {
		t.Fatalf("installed plugs %s", got)
	}
	if f := l.Plugs["care_fasterappointments_fe"].Frontend; f == nil || f.Source != FrontendExternal || f.SHA != nil {
		t.Fatalf("an FE plug without a CI bundle is external: %+v", f)
	}
	if f := l.Plugs["care_onboarding_fe"].Frontend; f == nil || f.Source != FrontendBuilt || *f.Entry != "assets/remoteEntry.js" {
		t.Fatalf("built FE plug %+v", f)
	}
	if b := l.Plugs["care_filly"].Backend; b == nil || b.AdditionalPlug.Version != "@"+strings.Repeat("b", 40) {
		t.Fatalf("filly %+v", b)
	}
	in.Version = "2026.11.1"
	data2, err := AssembleLock(in)
	if err != nil {
		t.Fatal(err)
	}
	ix, err := AssembleIndex(IndexInput{DistBase: "https://example.org/dist/", GeneratedAt: "2026-11-21T09:00:00Z",
		Releases: []IndexRelease{{Data: data}, {Data: data2}}, Withdrawn: map[string]string{"care-clinic/2026.11.1": "broken"}})
	if err != nil {
		t.Fatal(err)
	}
	idx, err := ParseIndex(ix)
	if err != nil {
		t.Fatal(err)
	}
	s := idx.Find("care-clinic")
	if s == nil || *s.Latest != "2026.11.0" || len(s.Locks) != 2 || !s.Locks[0].Withdrawn || s.Locks[0].Version != "2026.11.1" {
		t.Fatalf("index %+v", s)
	}
	if s.Lock("2026.11.0").SHA256 != l.SHA256 {
		t.Fatal("the index has another SHA-256")
	}
	if ResolveURL(idx.DistBase, s.Locks[1].URL) != "https://example.org/dist/lock-care-clinic-2026.11.0/care-clinic-2026.11.0.lock.yaml" {
		t.Fatalf("resolve %s", ResolveURL(idx.DistBase, s.Locks[1].URL))
	}
	if kind, err := Validate(ix); err != nil || kind != KindIndex {
		t.Fatalf("validate %s %v", kind, err)
	}
	// A bad lock: the schema rejects it.
	bad := strings.Replace(string(data), "digest: sha256:", "digest: md5:", 1)
	if _, err := ParseLock([]byte(bad)); err == nil {
		t.Fatal("a bad digest must fail")
	}
}

// TestOperatorChoiceAndSuperset: solution.plugs.<id>.enabled wins over the default and the auto rule;
// REACT_MFE_REGISTERED_COMPONENTS has the components of each plug, on or off.
func TestOperatorChoiceAndSuperset(t *testing.T) {
	sol, err := Load("care-clinic", "")
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Solution.Name = "care-clinic"
	on, off := true, false
	cfg.Solution.Plugs = map[string]config.PlugChoice{"care_filly": {Enabled: &on}, "care_onboarding_fe": {Enabled: &off}}
	plugs, err := sol.Effective(cfg, 2)
	if err != nil {
		t.Fatal(err)
	}
	by := map[string]EffectivePlug{}
	for _, p := range plugs {
		by[p.ID] = p
	}
	if p := by["care_filly"]; !p.On || p.Reason != ReasonOperator {
		t.Fatalf("filly %+v", p)
	}
	if p := by["care_onboarding_fe"]; p.On || p.Reason != ReasonOperator {
		t.Fatalf("onboarding %+v", p)
	}
	if p := by["care_multihost_files_be"]; !p.On || p.Reason != ReasonAuto || !p.AutoActive {
		t.Fatalf("multihost with 2 host names %+v", p)
	}
	if p := by["care_fasterappointments_fe"]; p.On || p.Reason != ReasonSolution {
		t.Fatalf("faster appointments %+v", p)
	}
	env := PlugEnv(plugs)
	if got := env.Frontend[RegisteredComponents]; got != "UserDashboard,AppointmentDetail,ScheduleResourceSelector" {
		t.Fatalf("registered components %q", got)
	}
}

func TestSettingsSchemaAndValues(t *testing.T) {
	sol, err := Load("care-clinic", "")
	if err != nil {
		t.Fatal(err)
	}
	var filly, fast Plug
	for _, p := range sol.Plugs {
		switch p.ID {
		case "care_filly":
			filly = p
		case "care_fasterappointments_fe":
			fast = p
		}
	}
	s := SettingsSchema(filly)
	be := s["properties"].(map[string]any)["backend"].(map[string]any)
	key := be["properties"].(map[string]any)["MEDISPEAK_API_KEY"].(map[string]any)
	if key["writeOnly"] != true || len(be["required"].([]any)) != 2 {
		t.Fatalf("filly schema %v", be)
	}
	set := filly.TypedSettings()
	if fe := CheckValue(set[1], "ftp://x"); fe == nil || fe.Pointer != "/settings/backend/MEDISPEAK_BASE_URL" {
		t.Fatalf("url check %+v", fe)
	}
	choices := fast.TypedSettings()[5]
	if fe := CheckValue(choices, []any{"cash", "nope"}); fe == nil || fe.Code != "enum" {
		t.Fatalf("choices %+v", fe)
	}
	if fe := CheckValue(choices, []any{"cash", "debc"}); fe != nil {
		t.Fatalf("valid choices: %+v", fe)
	}
	if Pointer("frontend", "a/b") != "/settings/frontend/a~1b" {
		t.Fatal("pointer escape")
	}
}
