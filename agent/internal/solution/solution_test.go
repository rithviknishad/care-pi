package solution

import (
	"reflect"
	"testing"

	"github.com/rithviknishad/care-pi/agent/internal/config"
)

// The real solutions/care-clinic/solution.yaml must load and validate.
func TestEmbeddedCareClinic(t *testing.T) {
	s, err := Load("care-clinic", "")
	if err != nil {
		t.Fatal(err)
	}
	if s.Care.Image == "" || s.CareFE.Repo == "" {
		t.Fatalf("incomplete solution: %+v", s)
	}
	cfg := config.Default()
	plugs, err := s.Effective(cfg, 1)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range plugs {
		if p.On && p.HasBackend() {
			t.Errorf("care-clinic must have no backend plug on one hostname, got %s", p.ID)
		}
	}
	two, _ := s.Effective(cfg, 2)
	on := false
	for _, p := range two {
		if p.ID == "care_multihost_files_be" {
			on = p.On
		}
	}
	if !on {
		t.Error("care_multihost_files_be must be on with two hostnames")
	}
}

func TestUnknownSolution(t *testing.T) {
	if _, err := Load("nope", ""); err == nil {
		t.Fatal("want an error")
	}
}

func TestCustomOverrides(t *testing.T) {
	s, err := Load("care-clinic", "testdata")
	if err != nil {
		t.Fatal(err)
	}
	yes, no := true, false
	cfg := config.Default()
	cfg.Solution.Track = "custom"
	cfg.Solution.Custom.Plugs = []config.PlugCustom{
		{ID: "care_filly", Enabled: &yes, Backend: &config.PlugCustomBackend{Ref: "v1"}},
		{ID: "care_onboarding_fe", Enabled: &no},
		{ID: "my_plug", Backend: &config.PlugCustomBackend{Name: "my_plug", Package: "git+https://example.org/my_plug.git"}},
	}
	plugs, err := s.Effective(cfg, 1)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]EffectivePlug{}
	for _, p := range plugs {
		got[p.ID] = p
	}
	if !got["care_filly"].On || got["care_filly"].Backend.Ref != "v1" {
		t.Errorf("care_filly: %+v", got["care_filly"])
	}
	if got["care_onboarding_fe"].On {
		t.Error("care_onboarding_fe must be off")
	}
	if !got["my_plug"].On || got["my_plug"].Backend.Ref != "main" {
		t.Errorf("my_plug: %+v", got["my_plug"])
	}
	// The solution itself must not change.
	if s.Plugs[1].Enabled {
		t.Error("Effective changed the solution")
	}
}

func TestMergeEnv(t *testing.T) {
	got := MergeEnv(MultiValueFrontendKeys,
		map[string]string{"A": "1", "REACT_MFE_REGISTERED_COMPONENTS": "X"},
		map[string]string{"A": "2", "REACT_MFE_REGISTERED_COMPONENTS": "Y,X"},
	)
	want := map[string]string{"A": "2", "REACT_MFE_REGISTERED_COMPONENTS": "X,Y"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v", got)
	}
}
