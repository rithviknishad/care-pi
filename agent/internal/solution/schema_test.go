package solution

import (
	"bytes"
	"encoding/json"
	"io/fs"
	"os"
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"go.yaml.in/yaml/v3"

	"github.com/rithviknishad/care-pi/solutions"
)

// The schemas of docs/solutions.md: the solution lock and the index (Phase 4).
const schemaBase = "https://github.com/rithviknishad/care-pi/blob/main/solutions/schema/"

func compileSchema(t *testing.T, name string) (*jsonschema.Schema, []any) {
	t.Helper()
	c := jsonschema.NewCompiler()
	c.AssertFormat()
	var examples []any
	for _, f := range []string{"solution", "lock", "index"} {
		data, err := fs.ReadFile(solutions.FS, "schema/"+f+".schema.json")
		if err != nil {
			t.Fatalf("embedded schema %s: %v", f, err)
		}
		doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
		if err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		if err := c.AddResource(schemaBase+f+".schema.json", doc); err != nil {
			t.Fatal(err)
		}
		if f == name {
			var raw struct {
				Examples []json.RawMessage `json:"examples"`
			}
			if err := json.Unmarshal(data, &raw); err != nil {
				t.Fatal(err)
			}
			for _, ex := range raw.Examples {
				v, err := jsonschema.UnmarshalJSON(bytes.NewReader(ex))
				if err != nil {
					t.Fatal(err)
				}
				examples = append(examples, v)
			}
		}
	}
	sch, err := c.Compile(schemaBase + name + ".schema.json")
	if err != nil {
		t.Fatalf("compile %s: %v", name, err)
	}
	return sch, examples
}

func yamlValue(t *testing.T, src string) any {
	t.Helper()
	var v any
	if err := yaml.Unmarshal([]byte(src), &v); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	out, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// clone returns a deep copy of a decoded JSON value, changed by edit.
func clone(t *testing.T, v any, edit func(m map[string]any)) any {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	edit(m)
	data, _ = json.Marshal(m)
	out, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func obj(m map[string]any, path ...string) map[string]any {
	for _, p := range path {
		m = m[p].(map[string]any)
	}
	return m
}

func TestLockSchemaExamples(t *testing.T) {
	sch, examples := compileSchema(t, "lock")
	if len(examples) == 0 {
		t.Fatal("lock.schema.json has no examples")
	}
	for i, ex := range examples {
		if err := sch.Validate(ex); err != nil {
			t.Errorf("example %d: %v", i, err)
		}
	}
	bad := map[string]func(m map[string]any){
		"month without two digits": func(m map[string]any) { m["version"] = "2026.1.0" },
		"a short sha":              func(m map[string]any) { obj(m, "care_fe")["sha"] = "1f944e82" },
		"a tag instead of a digest": func(m map[string]any) {
			obj(m, "solution_image")["digest"] = "care-clinic-2026.11.0"
		},
		"a built bundle without sha256": func(m map[string]any) {
			obj(m, "plugs", "care_onboarding_fe", "frontend")["sha256"] = nil
		},
		"an external bundle with a relative url": func(m map[string]any) {
			f := obj(m, "plugs", "care_onboarding_fe", "frontend")
			f["source"], f["sha"], f["sha256"], f["entry"], f["size_bytes"], f["ref"] = "external", nil, nil, nil, nil, nil
		},
		"a missing base image": func(m map[string]any) { delete(obj(m, "images"), "node") },
		"an invalid solution":  func(m map[string]any) { delete(obj(m, "solution"), "care") },
		"an unknown member":    func(m map[string]any) { m["signature"] = "x" },
	}
	for name, edit := range bad {
		if err := sch.Validate(clone(t, examples[0], edit)); err == nil {
			t.Errorf("%s: want a validation error", name)
		}
	}
	good := clone(t, examples[0], func(m map[string]any) {
		f := obj(m, "plugs", "care_onboarding_fe", "frontend")
		f["source"], f["sha"], f["sha256"], f["entry"], f["size_bytes"], f["ref"] = "external", nil, nil, nil, nil, nil
		f["url"] = "https://ohcnetwork.github.io/care_onboarding_fe/assets/remoteEntry.js"
	})
	if err := sch.Validate(good); err != nil {
		t.Errorf("an external bundle: %v", err)
	}
}

func TestIndexSchemaExamples(t *testing.T) {
	sch, examples := compileSchema(t, "index")
	if len(examples) == 0 {
		t.Fatal("index.schema.json has no examples")
	}
	for i, ex := range examples {
		if err := sch.Validate(ex); err != nil {
			t.Errorf("example %d: %v", i, err)
		}
	}
	bad := map[string]func(m map[string]any){
		"dist_base without a slash at the end": func(m map[string]any) {
			m["dist_base"] = "https://github.com/rithviknishad/care-pi/releases/download"
		},
		"an http dist_base": func(m map[string]any) { m["dist_base"] = "http://example.org/" },
		"a lock without sha256": func(m map[string]any) {
			lock := m["solutions"].([]any)[0].(map[string]any)["locks"].([]any)[0].(map[string]any)
			delete(lock, "sha256")
		},
	}
	for name, edit := range bad {
		if err := sch.Validate(clone(t, examples[0], edit)); err == nil {
			t.Errorf("%s: want a validation error", name)
		}
	}
	offline := clone(t, examples[0], func(m map[string]any) { m["dist_base"] = "file:///media/usb/care-pi/" })
	if err := sch.Validate(offline); err != nil {
		t.Errorf("a file:// dist_base: %v", err)
	}
}

// The Phase 4 additions of solution.schema.json are additive: care-clinic stays valid,
// and the new rules reject what they must.
func TestSolutionSchemaPhase4(t *testing.T) {
	sch, _ := compileSchema(t, "solution")
	data, err := os.ReadFile("../../../solutions/care-clinic/solution.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if err := sch.Validate(yamlValue(t, string(data))); err != nil {
		t.Fatalf("solutions/care-clinic/solution.yaml: %v", err)
	}
	base := `
apiVersion: care-pi/v1
kind: Solution
name: demo
title: Demo
description: Demo.
min_agent_version: v0.4.0
care: {image: ghcr.io/ohcnetwork/care, ref: latest}
care_fe: {repo: https://github.com/ohcnetwork/care_fe, ref: develop}
plugs:
  - id: demo_fe
    label: Demo
    enabled: true
    frontend:
      slug: demo_fe
      repo: https://github.com/example/demo_fe
      ref: main
      entry: assets/remoteEntry.js
      build: {script: build, out: dist}
    settings:
      - {part: frontend, key: config.limit, label: Limit, type: number, min: 1, max: 10, advanced: true}
      - {part: frontend, key: config.code, label: Code, type: string, pattern: "^[A-Z]{3}$"}
`
	if err := sch.Validate(yamlValue(t, base)); err != nil {
		t.Fatalf("the Phase 4 fields: %v", err)
	}
	git := strings.Replace(base, "care: {image: ghcr.io/ohcnetwork/care, ref: latest}", "care: {source: git, repo: https://github.com/ohcnetwork/care, ref: 3.1/base}", 1)
	if err := sch.Validate(yamlValue(t, git)); err != nil {
		t.Fatalf("care.source git (S71): %v", err)
	}
	// The rules that the schema does not express (checkCare): Parse refuses these.
	for name, src := range map[string]string{
		"a git source without a repo":   strings.Replace(git, "repo: https://github.com/ohcnetwork/care, ", "", 1),
		"an image source without image": strings.Replace(base, "image: ghcr.io/ohcnetwork/care, ", "", 1),
		"a slash in an image tag":       strings.Replace(base, "ref: latest}", "ref: 3.1/base}", 1),
	} {
		if _, err := Parse([]byte(src), name); err == nil || !strings.Contains(err.Error(), "care") {
			t.Errorf("%s: want an error of care, got %v", name, err)
		}
	}
	if _, err := Parse([]byte(git), "git"); err != nil {
		t.Fatalf("care.source git: %v", err)
	}
	for name, src := range map[string]string{
		"an unknown source":             strings.Replace(git, "source: git", "source: svn", 1),
		"a secret in the frontend part": strings.Replace(base, "type: string, pattern", "type: secret, pattern", 1),
		"a bad min_agent_version":       strings.Replace(base, "v0.4.0", "0.4", 1),
		"an entry that is not a .js":    strings.Replace(base, "assets/remoteEntry.js", "assets/remoteEntry.css", 1),
		"an unknown build member":       strings.Replace(base, "out: dist}", "out: dist, env: {}}", 1),
	} {
		if err := sch.Validate(yamlValue(t, src)); err == nil {
			t.Errorf("%s: want a validation error", name)
		}
	}
}
