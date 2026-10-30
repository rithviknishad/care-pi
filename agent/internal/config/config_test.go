package config

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

func TestValidFixtures(t *testing.T) {
	files, _ := filepath.Glob("testdata/valid-*.yaml")
	if len(files) < 5 {
		t.Fatalf("want at least 5 valid fixtures, got %d", len(files))
	}
	for _, f := range files {
		t.Run(filepath.Base(f), func(t *testing.T) {
			if _, err := Load(f); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestInvalidFixtures(t *testing.T) {
	cases := map[string]string{
		"invalid-unknown-key.yaml":    "sizing",
		"invalid-preset.yaml":         "/size/preset",
		"invalid-hostname.yaml":       "/identity/hostname",
		"invalid-reserved-env.yaml":   "DATABASE_URL",
		"invalid-custom-missing.yaml": "/size",
		"invalid-version.yaml":        "/version",
		"invalid-nested-unknown.yaml": "mode",
	}
	for file, want := range cases {
		t.Run(file, func(t *testing.T) {
			_, err := Load(filepath.Join("testdata", file))
			var verr *ValidationError
			if !errors.As(err, &verr) {
				t.Fatalf("want a ValidationError, got %v", err)
			}
			if !strings.Contains(err.Error(), want) {
				t.Fatalf("error does not mention %q:\n%v", want, err)
			}
		})
	}
}

func TestDefaults(t *testing.T) {
	c, err := Load("testdata/valid-minimal.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if c.Identity.Hostname != "care" || c.Size.Preset != "small-clinic" || !c.TLS.LocalCA.Enabled ||
		c.Clock.Policy != "ntp" || c.Uploads.MaxBodyMB != 200 || c.FE.Provider != "local" ||
		c.Backups.WALArchiveTimeoutMin != 5 || c.Solution.Name != "care-clinic" {
		t.Fatalf("unexpected defaults: %+v", c)
	}
	s := c.Sizing()
	if s.GunicornWorkers != 2 || s.CeleryConcurrency != 1 || s.PGSharedBuffersMB != 128 {
		t.Fatalf("small-clinic sizing: %+v", s)
	}
}

func TestPublicTLSIsApplyTimeError(t *testing.T) {
	c, err := Load("testdata/valid-public-tls.yaml")
	if err != nil {
		t.Fatalf("the schema must accept tls.public: %v", err)
	}
	err = c.CheckImplemented()
	if !errors.Is(err, ErrNotImplemented) || !strings.Contains(err.Error(), "tls.public") {
		t.Fatalf("want a not-implemented error for tls.public, got %v", err)
	}
}

func TestDefaultYAMLIsValid(t *testing.T) {
	for _, p := range []string{"solo", "small-clinic", "busy-counter"} {
		data, err := DefaultYAML("Sunrise Clinic", "sunrise", p, "")
		if err != nil {
			t.Fatal(err)
		}
		c, err := Parse(data, "default")
		if err != nil {
			t.Fatal(err)
		}
		if c.Size.Preset != p || c.Identity.Hostname != "sunrise" || c.FE.Provider != "local" {
			t.Fatalf("unexpected config: %+v", c)
		}
	}
	for _, fp := range []string{"none", "artifact", "local"} {
		data, err := DefaultYAML("", "", "", fp)
		if err != nil {
			t.Fatal(err)
		}
		if c, err := Parse(data, "default"); err != nil || c.FE.Provider != fp {
			t.Fatalf("fe provider %s: %v %+v", fp, err, c)
		}
	}
	if _, err := DefaultYAML("", "", "custom", ""); err == nil {
		t.Fatal("want an error for preset custom")
	}
	if _, err := DefaultYAML("", "", "", "remote"); err == nil {
		t.Fatal("want an error for fe provider remote")
	}
}

func TestRoundTrip(t *testing.T) {
	for _, f := range []string{"testdata/valid-small-clinic.yaml", "testdata/valid-custom.yaml"} {
		c, err := Load(f)
		if err != nil {
			t.Fatal(err)
		}
		if err := c.Validate(); err != nil {
			t.Fatalf("%s: marshal and validate again: %v", f, err)
		}
	}
	if err := Default().Validate(); err != nil {
		t.Fatalf("Default() is not valid: %v", err)
	}
}

func TestMemoryCaps(t *testing.T) {
	cases := []struct {
		got  Mem
		want Mem
	}{
		{MemAPI(1), Mem{320, 64}},
		{MemAPI(2), Mem{512, 128}},
		{MemAPI(3), Mem{768, 128}},
		{MemWorker(1), Mem{384, 128}},
		{MemWorker(2), Mem{512, 128}},
		{MemDB(128), Mem{384, 0}},
		{MemDB(256), Mem{512, 0}},
		{MemBeat, Mem{512, 128}},
		{MemRedis, Mem{96, 0}},
		{MemS3, Mem{256, 0}},
		{MemCaddy, Mem{96, 0}},
	}
	for i, c := range cases {
		if c.got != c.want {
			t.Errorf("case %d: got %+v, want %+v", i, c.got, c.want)
		}
	}
	if MemDB(128).Swap() != "0" || MemAPI(1).Swap() != "64M" {
		t.Fatal("bad systemd values")
	}
}

// The reserved env list in Go and in the schema must be the same.
func TestReservedEnvMatchesSchema(t *testing.T) {
	var doc struct {
		Properties struct {
			Env struct {
				Properties struct {
					Backend struct {
						PropertyNames struct {
							Not struct {
								Enum []string `json:"enum"`
							} `json:"not"`
						} `json:"propertyNames"`
					} `json:"backend"`
					Frontend struct {
						PropertyNames struct {
							Not struct {
								Enum []string `json:"enum"`
							} `json:"not"`
						} `json:"propertyNames"`
					} `json:"frontend"`
				} `json:"properties"`
			} `json:"env"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(SchemaJSON(), &doc); err != nil {
		t.Fatal(err)
	}
	be := doc.Properties.Env.Properties.Backend.PropertyNames.Not.Enum
	fe := doc.Properties.Env.Properties.Frontend.PropertyNames.Not.Enum
	want := append([]string(nil), ReservedBackendEnv...)
	sort.Strings(be)
	sort.Strings(want)
	if !reflect.DeepEqual(be, want) {
		t.Fatalf("schema backend list %v != Go list %v", be, want)
	}
	if !reflect.DeepEqual(fe, ReservedFrontendEnv) {
		t.Fatalf("schema frontend list %v != Go list %v", fe, ReservedFrontendEnv)
	}
}

// docs/config.md must name every top-level key of the schema.
func TestDocsMentionEveryKey(t *testing.T) {
	data, err := os.ReadFile("../../../docs/config.md")
	if err != nil {
		t.Skip("docs/config.md is missing")
	}
	var doc struct {
		Properties map[string]json.RawMessage `json:"properties"`
	}
	if err := json.Unmarshal(SchemaJSON(), &doc); err != nil {
		t.Fatal(err)
	}
	for k := range doc.Properties {
		if !strings.Contains(string(data), "`"+k+"`") {
			t.Errorf("docs/config.md does not mention `%s`", k)
		}
	}
}
