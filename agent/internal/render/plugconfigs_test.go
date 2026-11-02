package render

import (
	"path/filepath"
	"reflect"
	"testing"

	"github.com/rithviknishad/care-pi/agent/internal/config"
	"github.com/rithviknishad/care-pi/agent/internal/solution"
)

// TestPlugConfigsDeviceHosts: the multihost plug (auto: when_multihost) gets each name of the device that no pattern
// of ALLOWED_HOSTS matches (wave 8: the lock of lumine, made before Phase 6, had no *.care-pi.rithviknishad.dev, so
// each file link named the public name, also for a request on lumine.local).
func TestPlugConfigsDeviceHosts(t *testing.T) {
	cfg, err := config.Load(filepath.Join("testdata", "config-public-tailscale.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	// sunrise-4f2a.care-pi.rithviknishad.dev, care.local, care.tail1234.ts.net
	names := Hostnames(cfg)
	multihost := func(configs map[string]any, settings ...map[string]any) solution.Plug {
		return solution.Plug{ID: "care_multihost_files_be", Auto: solution.AutoWhenMultihost,
			Backend:  &solution.BackendPlug{Name: "care_multihost_files_be", Configs: configs},
			Settings: settings}
	}
	listSetting := map[string]any{"part": "backend", "key": "ALLOWED_HOSTS", "label": "Addresses", "type": "list"}
	cases := []struct {
		name     string
		plug     solution.Plug
		operator []any
		want     any
	}{
		{"old lock: the public name is added", multihost(map[string]any{"ALLOWED_HOSTS": []any{"*.local", "*.ts.net"}}), nil,
			[]any{"*.local", "*.ts.net", names[0]}},
		{"the solution of main: nothing to add",
			multihost(map[string]any{"ALLOWED_HOSTS": []any{"*.local", "*.ts.net", "*.care-pi.rithviknishad.dev"}}), nil,
			[]any{"*.local", "*.ts.net", "*.care-pi.rithviknishad.dev"}},
		{"an operator list without the names: each name is added",
			multihost(map[string]any{"ALLOWED_HOSTS": []any{"*.local"}}, listSetting), []any{"*.lan", "CARE.LOCAL"},
			[]any{"*.lan", "CARE.LOCAL", names[0], names[2]}},
		{"no configs, a declared setting: the names", multihost(nil, listSetting), nil,
			[]any{names[0], names[1], names[2]}},
		{"no configs, no setting: unchanged", multihost(nil), nil, nil},
		{"not a list: unchanged", multihost(map[string]any{"ALLOWED_HOSTS": "*"}), nil, "*"},
		{"no auto rule: unchanged", solution.Plug{ID: "care_filly",
			Backend: &solution.BackendPlug{Name: "care_filly", Configs: map[string]any{"ALLOWED_HOSTS": []any{"*.lan"}}}}, nil,
			[]any{"*.lan"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := *cfg
			c.Solution.Plugs = map[string]config.PlugChoice{}
			if tc.operator != nil {
				c.Solution.Plugs[tc.plug.ID] = config.PlugChoice{Settings: &config.PlugSettings{Backend: map[string]any{"ALLOWED_HOSTS": tc.operator}}}
			}
			full, red := PlugConfigs(&c, tc.plug, nil)
			if got := full["ALLOWED_HOSTS"]; !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("ALLOWED_HOSTS %#v, want %#v", got, tc.want)
			}
			if !reflect.DeepEqual(full, red) {
				t.Fatalf("redacted %#v differs from %#v (no secret)", red, full)
			}
		})
	}

	// One name: the plug is off, and the configs are the same as before the name was added.
	one := *cfg
	one.TLS.Public.Enabled, one.TLS.Tailscale.Enabled = false, false
	full, _ := PlugConfigs(&one, multihost(map[string]any{"ALLOWED_HOSTS": []any{"*.local", "*.ts.net"}}), nil)
	if want := []any{"*.local", "*.ts.net"}; !reflect.DeepEqual(full["ALLOWED_HOSTS"], want) {
		t.Fatalf("one name: %#v, want %#v", full["ALLOWED_HOSTS"], want)
	}
}
