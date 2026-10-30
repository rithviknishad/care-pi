package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/santhosh-tekuri/jsonschema/v6"
	"go.yaml.in/yaml/v3"
)

const (
	specFile    = "openapi.yaml"
	configFile  = "../../agent/internal/config/schema/config.schema.json"
	problemBase = "https://care-pi.rithviknishad.dev/problems/"
)

// publicOperations are the operations without a session (docs/api/README.md, "Authentication").
var publicOperations = []string{
	"GET /_care-pi/status",
	"GET /health",
	"POST /session/claim",
	"POST /session/login",
	"POST /session/recover",
	"GET /tls/root-ca",
	"GET /tls/root-ca/mobileconfig",
	"GET /tls/instructions/{os}",
}

// generalProblems can come from each operation (the default response).
var generalProblems = []string{"malformed-request", "payload-too-large", "unsupported-media-type", "rate-limited", "internal", "unavailable"}

func loadSpec(t *testing.T) *openapi3.T {
	t.Helper()
	loader := openapi3.NewLoader()
	loader.IsExternalRefsAllowed = true
	doc, err := loader.LoadFromFile(specFile)
	if err != nil {
		t.Fatalf("load %s: %v", specFile, err)
	}
	return doc
}

// TestValidate loads the document with its external references and validates it.
func TestValidate(t *testing.T) {
	doc := loadSpec(t)
	if !doc.IsOpenAPI31OrLater() || doc.OpenAPI != "3.1.0" {
		t.Fatalf("openapi = %q, want 3.1.0", doc.OpenAPI)
	}
	// TestExamples also validates every example with JSON Schema 2020-12 and the external config schema.
	if err := doc.Validate(context.Background()); err != nil {
		t.Fatalf("validate: %v", err)
	}
	cfg := doc.Components.Schemas["Config"]
	if cfg == nil || cfg.Value == nil || cfg.Value.Properties["version"] == nil {
		t.Fatalf("components.schemas.Config does not resolve to config.schema.json")
	}
}

type operation struct {
	key    string
	method string
	path   string
	op     *openapi3.Operation
}

func operations(doc *openapi3.T) []operation {
	var out []operation
	for path, item := range doc.Paths.Map() {
		for method, op := range item.Operations() {
			out = append(out, operation{key: method + " " + path, method: method, path: path, op: op})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].key < out[j].key })
	return out
}

func catalogue(t *testing.T, doc *openapi3.T) []string {
	t.Helper()
	pt := doc.Components.Schemas["ProblemType"]
	if pt == nil || pt.Value == nil {
		t.Fatal("components.schemas.ProblemType is missing")
	}
	var slugs []string
	for _, v := range pt.Value.Enum {
		s, _ := v.(string)
		slug, ok := strings.CutPrefix(s, problemBase)
		if !ok || slug == "" {
			t.Errorf("problem type %q does not start with %s", s, problemBase)
			continue
		}
		slugs = append(slugs, slug)
	}
	return slugs
}

func stringList(v any) ([]string, bool) {
	list, ok := v.([]any)
	if !ok {
		return nil, false
	}
	out := make([]string, 0, len(list))
	for _, item := range list {
		s, ok := item.(string)
		if !ok {
			return nil, false
		}
		out = append(out, s)
	}
	return out, true
}

func phase(op *openapi3.Operation) int {
	switch v := op.Extensions["x-phase"].(type) {
	case float64:
		return int(v)
	case int:
		return v
	}
	return 2
}

// TestOperations checks the rules of the contract for each operation.
func TestOperations(t *testing.T) {
	doc := loadSpec(t)
	slugs := catalogue(t, doc)
	if len(slugs) < 12 {
		t.Errorf("the problem catalogue has %d types, want 12 or more", len(slugs))
	}
	known := map[string]bool{}
	for _, s := range slugs {
		known[s] = true
	}
	for _, s := range generalProblems {
		if !known[s] {
			t.Errorf("general problem %q is not in the catalogue", s)
		}
	}
	ids := map[string]string{}
	var public []string
	for _, o := range operations(doc) {
		op := o.op
		if op.OperationID == "" {
			t.Errorf("%s: no operationId", o.key)
		} else if prev, ok := ids[op.OperationID]; ok {
			t.Errorf("%s: operationId %q is also on %s", o.key, op.OperationID, prev)
		} else {
			ids[op.OperationID] = o.key
		}
		if op.Summary == "" {
			t.Errorf("%s: no summary", o.key)
		}
		if len(op.Tags) != 1 {
			t.Errorf("%s: %d tags, want 1", o.key, len(op.Tags))
		}
		if s, ok := op.Extensions["x-cli"].(string); !ok || s == "" {
			t.Errorf("%s: no x-cli", o.key)
		}
		problems, ok := stringList(op.Extensions["x-problems"])
		if !ok {
			t.Errorf("%s: x-problems is not a list of strings", o.key)
		}
		for _, p := range problems {
			if !known[p] {
				t.Errorf("%s: x-problems has %q, which is not in the catalogue", o.key, p)
			}
		}
		if op.Responses.Default() == nil {
			t.Errorf("%s: no default response", o.key)
		}
		has4xx := false
		for code := range op.Responses.Map() {
			if strings.HasPrefix(code, "4") {
				has4xx = true
			}
		}
		if !has4xx {
			t.Errorf("%s: no 4xx response", o.key)
		}
		if p := phase(op); p > 2 {
			if op.Responses.Status(http.StatusNotImplemented) == nil {
				t.Errorf("%s: x-phase %d but no 501 response", o.key, p)
			}
			if !slices.Contains(problems, "not-implemented-in-this-version") {
				t.Errorf("%s: x-phase %d but x-problems has no not-implemented-in-this-version", o.key, p)
			}
		}
		if op.Extensions["x-reauth"] == true {
			if op.Responses.Status(http.StatusForbidden) == nil || !slices.Contains(problems, "reauth-required") {
				t.Errorf("%s: x-reauth but no 403 with reauth-required", o.key)
			}
		}

		isPublic := op.Security != nil && len(*op.Security) == 0
		if isPublic {
			public = append(public, o.key)
			continue
		}
		reqs := doc.Security
		if op.Security != nil {
			reqs = *op.Security
		}
		unsafe := o.method != http.MethodGet && o.method != http.MethodHead
		for _, r := range reqs {
			if _, ok := r["session"]; !ok {
				t.Errorf("%s: a security requirement without session", o.key)
			}
			if _, ok := r["csrf"]; unsafe && !ok {
				t.Errorf("%s: an unsafe operation without the csrf requirement", o.key)
			}
			if _, ok := r["csrf"]; !unsafe && ok {
				t.Errorf("%s: a safe operation with the csrf requirement", o.key)
			}
		}
		if op.Responses.Status(http.StatusUnauthorized) == nil || !slices.Contains(problems, "not-authenticated") {
			t.Errorf("%s: needs a session but has no 401 with not-authenticated", o.key)
		}
		if unsafe && !slices.Contains(problems, "csrf") {
			t.Errorf("%s: unsafe operation without csrf in x-problems", o.key)
		}
	}
	sort.Strings(public)
	want := slices.Clone(publicOperations)
	sort.Strings(want)
	if !slices.Equal(public, want) {
		t.Errorf("public operations:\n got  %v\n want %v", public, want)
	}
}

// TestSeparateOrigin checks decision S48: the console origin on port 8443.
func TestSeparateOrigin(t *testing.T) {
	doc := loadSpec(t)
	if len(doc.Servers) != 1 || doc.Servers[0].URL != "/api/v1" {
		t.Errorf("servers = %v, want one server /api/v1", doc.Servers)
	}
	if s := doc.Components.SecuritySchemes["session"]; s == nil || s.Value.Name != "__Host-care_pi_session" || s.Value.In != "cookie" {
		t.Errorf("the session cookie must be __Host-care_pi_session")
	}
	portsOK := func(srv *openapi3.Server) bool {
		v := srv.Variables["port"]
		if v == nil || srv.Variables["host"] == nil {
			return false
		}
		got := slices.Clone(v.Enum)
		sort.Strings(got)
		return slices.Equal(got, []string{"443", "8443"})
	}
	for _, key := range publicOperations {
		path := strings.SplitN(key, " ", 2)[1]
		item := doc.Paths.Value(path)
		if item == nil {
			t.Errorf("%s: no such path", key)
			continue
		}
		var urls []string
		for _, srv := range item.Servers {
			urls = append(urls, srv.URL)
			if strings.HasPrefix(srv.URL, "https://") && !portsOK(srv) {
				t.Errorf("%s: server %s needs the variables host and port (443, 8443)", key, srv.URL)
			}
		}
		switch {
		case path == "/_care-pi/status":
			if !slices.Equal(urls, []string{"https://{host}:{port}"}) {
				t.Errorf("%s: servers %v, want the root of both ports", key, urls)
			}
		case path == "/health" || strings.HasPrefix(path, "/tls/"):
			if !slices.Equal(urls, []string{"/api/v1", "https://{host}:{port}/_care-pi"}) {
				t.Errorf("%s: servers %v, want /api/v1 and /_care-pi on both ports", key, urls)
			}
		}
	}
	data, err := os.ReadFile(specFile)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, old := range []string{"/console/api/v1", " care_pi_session=", "Path=/console"} {
		if strings.Contains(text, old) {
			t.Errorf("%s still contains %q", specFile, old)
		}
	}
	checkHeaders := func(where string, h openapi3.Headers) {
		for name := range h {
			if strings.HasPrefix(strings.ToLower(name), "access-control-") {
				t.Errorf("%s: CORS header %s (S48: no CORS)", where, name)
			}
		}
	}
	checkHeaders("components.headers", doc.Components.Headers)
	for name, r := range doc.Components.Responses {
		checkHeaders("components.responses."+name, r.Value.Headers)
	}
	for _, o := range operations(doc) {
		for code, r := range o.op.Responses.Map() {
			if r.Value != nil {
				checkHeaders(o.key+" "+code, r.Value.Headers)
			}
		}
	}
}

// TestSchemas checks that each component schema has a description and an example.
func TestSchemas(t *testing.T) {
	doc := loadSpec(t)
	for name, ref := range doc.Components.Schemas {
		s := ref.Value
		if s == nil {
			t.Errorf("%s: not resolved", name)
			continue
		}
		if strings.TrimSpace(s.Description) == "" {
			t.Errorf("%s: no description", name)
		}
		if len(s.Examples) == 0 {
			t.Errorf("%s: no examples", name)
		}
		if s.Type != nil && s.Type.Is("object") && len(s.Properties) > 0 {
			if s.AdditionalProperties.Has == nil || *s.AdditionalProperties.Has {
				t.Errorf("%s: a closed object needs additionalProperties: false", name)
			}
			for _, r := range s.Required {
				if _, ok := s.Properties[r]; !ok {
					t.Errorf("%s: required %q is not a property", name, r)
				}
			}
		}
	}
}

// specTree reads a YAML or JSON file as a JSON value.
func specTree(t *testing.T, file string) any {
	t.Helper()
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	var v any
	if err := yaml.Unmarshal(data, &v); err != nil {
		t.Fatalf("parse %s: %v", file, err)
	}
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("%s: %v", file, err)
	}
	out, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("%s: %v", file, err)
	}
	return out
}

func fileURL(t *testing.T, file string) string {
	t.Helper()
	abs, err := filepath.Abs(file)
	if err != nil {
		t.Fatal(err)
	}
	return "file://" + filepath.ToSlash(abs)
}

func pointerEscape(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "~", "~0"), "/", "~1")
}

type exampleCase struct {
	where  string
	schema string // JSON Pointer of the schema in the document
	value  any
}

func asMap(v any) map[string]any {
	m, _ := v.(map[string]any)
	return m
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// mediaExamples collects the named examples of the JSON media types in a content map.
func mediaExamples(where, ptr string, content map[string]any) []exampleCase {
	var out []exampleCase
	for _, mt := range sortedKeys(content) {
		if !strings.Contains(mt, "json") {
			continue
		}
		media := asMap(content[mt])
		if media["schema"] == nil {
			continue
		}
		examples := asMap(media["examples"])
		for _, name := range sortedKeys(examples) {
			ex := asMap(examples[name])
			if v, ok := ex["value"]; ok {
				out = append(out, exampleCase{
					where:  fmt.Sprintf("%s %s example %s", where, mt, name),
					schema: ptr + "/" + pointerEscape(mt) + "/schema",
					value:  v,
				})
			}
		}
	}
	return out
}

// TestExamples validates each example of the document against its schema with JSON Schema 2020-12.
func TestExamples(t *testing.T) {
	spec := specTree(t, specFile)
	specURL := fileURL(t, specFile)
	c := jsonschema.NewCompiler()
	c.DefaultDraft(jsonschema.Draft2020)
	c.AssertFormat()
	if err := c.AddResource(specURL, spec); err != nil {
		t.Fatal(err)
	}
	if err := c.AddResource(fileURL(t, configFile), specTree(t, configFile)); err != nil {
		t.Fatal(err)
	}

	var cases []exampleCase
	root := asMap(spec)
	components := asMap(root["components"])
	schemas := asMap(components["schemas"])
	for _, name := range sortedKeys(schemas) {
		s := asMap(schemas[name])
		list, _ := s["examples"].([]any)
		for i, v := range list {
			cases = append(cases, exampleCase{
				where:  fmt.Sprintf("components.schemas.%s.examples[%d]", name, i),
				schema: "/components/schemas/" + pointerEscape(name),
				value:  v,
			})
		}
	}
	responses := asMap(components["responses"])
	for _, name := range sortedKeys(responses) {
		cases = append(cases, mediaExamples("components.responses."+name,
			"/components/responses/"+pointerEscape(name)+"/content", asMap(asMap(responses[name])["content"]))...)
	}
	paths := asMap(root["paths"])
	for _, p := range sortedKeys(paths) {
		item := asMap(paths[p])
		for _, m := range sortedKeys(item) {
			op := asMap(item[m])
			if op == nil || m == "servers" || m == "parameters" {
				continue
			}
			base := "/paths/" + pointerEscape(p) + "/" + m
			if rb := asMap(op["requestBody"]); rb != nil {
				cases = append(cases, mediaExamples(m+" "+p+" request", base+"/requestBody/content", asMap(rb["content"]))...)
			}
			rs := asMap(op["responses"])
			for _, code := range sortedKeys(rs) {
				r := asMap(rs[code])
				cases = append(cases, mediaExamples(m+" "+p+" "+code, base+"/responses/"+code+"/content", asMap(r["content"]))...)
			}
		}
	}
	t.Logf("validating %d examples", len(cases))
	if len(cases) < 100 {
		t.Fatalf("found %d examples, want 100 or more", len(cases))
	}

	compiled := map[string]*jsonschema.Schema{}
	for _, tc := range cases {
		sch, ok := compiled[tc.schema]
		if !ok {
			var err error
			sch, err = c.Compile(specURL + "#" + tc.schema)
			if err != nil {
				t.Errorf("%s: compile %s: %v", tc.where, tc.schema, err)
				continue
			}
			compiled[tc.schema] = sch
		}
		if err := sch.Validate(tc.value); err != nil {
			t.Errorf("%s: %v", tc.where, err)
		}
	}
}

// TestProblemExamples checks that each problem example uses a type of the catalogue with its status.
func TestProblemExamples(t *testing.T) {
	doc := loadSpec(t)
	known := map[string]bool{}
	for _, s := range catalogue(t, doc) {
		known[problemBase+s] = true
	}
	for name, ref := range doc.Components.Responses {
		media := ref.Value.Content.Get("application/problem+json")
		if media == nil {
			continue
		}
		for exName, ex := range media.Examples {
			v := asMap(ex.Value.Value)
			typ, _ := v["type"].(string)
			if !known[typ] {
				t.Errorf("responses.%s example %s: type %q is not in the catalogue", name, exName, typ)
			}
			if slug := strings.TrimPrefix(typ, problemBase); slug != exName {
				t.Errorf("responses.%s example %s: the example name must be the slug %q", name, exName, slug)
			}
		}
	}
}
