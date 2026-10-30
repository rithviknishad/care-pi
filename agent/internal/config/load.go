package config

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/santhosh-tekuri/jsonschema/v6/kind"
	"go.yaml.in/yaml/v3"
	"golang.org/x/text/language"
	"golang.org/x/text/message"
)

//go:embed schema/config.schema.json
var schemaJSON []byte

const schemaURL = "https://github.com/rithviknishad/care-pi/agent/internal/config/schema/config.schema.json"

// SchemaJSON returns the JSON Schema of config.yaml.
func SchemaJSON() []byte { return schemaJSON }

// ValidationError lists every problem in a config file.
type ValidationError struct {
	Source   string
	Problems []string
	// Details are the same problems with a JSON Pointer and the JSON Schema keyword.
	Details []Detail
}

// Detail is one problem: a JSON Pointer into the document, a message, and the failed keyword.
type Detail struct {
	Pointer string
	Message string
	Keyword string
}

func invalid(source string, details ...Detail) *ValidationError {
	e := &ValidationError{Source: source, Details: details}
	for _, d := range details {
		e.Problems = append(e.Problems, d.Pointer+": "+d.Message)
	}
	return e
}

func (e *ValidationError) Error() string {
	var b strings.Builder
	_, _ = fmt.Fprintf(&b, "%s is not valid:", e.Source)
	for _, p := range e.Problems {
		b.WriteString("\n  - ")
		b.WriteString(p)
	}
	return b.String()
}

var (
	compileOnce sync.Once
	compiled    *jsonschema.Schema
	compileErr  error
)

func schema() (*jsonschema.Schema, error) {
	compileOnce.Do(func() {
		doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(schemaJSON))
		if err != nil {
			compileErr = fmt.Errorf("care-pi: embedded config schema is not valid JSON: %w", err)
			return
		}
		c := jsonschema.NewCompiler()
		if err := c.AddResource(schemaURL, doc); err != nil {
			compileErr = fmt.Errorf("care-pi: cannot add the config schema: %w", err)
			return
		}
		compiled, compileErr = c.Compile(schemaURL)
	})
	return compiled, compileErr
}

// Load reads, validates, and decodes the config file at path.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("cannot read the config: %w", err)
	}
	return Parse(data, path)
}

// Parse validates and decodes a config document. source names the document in errors.
func Parse(data []byte, source string) (*Config, error) {
	var raw any
	if err := yaml.Unmarshal(data, &raw); err != nil {
		e := invalid(source, Detail{Message: "YAML syntax: " + err.Error(), Keyword: "syntax"})
		e.Problems = []string{"YAML syntax: " + err.Error()}
		return nil, e
	}
	if raw == nil {
		e := invalid(source, Detail{Message: "the file is empty; set at least \"version: 1\"", Keyword: "required"})
		e.Problems = []string{e.Details[0].Message}
		return nil, e
	}
	if details := validateSchema(raw); len(details) > 0 {
		return nil, invalid(source, details...)
	}
	cfg := Default()
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(cfg); err != nil {
		e := invalid(source, Detail{Message: err.Error(), Keyword: "type"})
		e.Problems = []string{err.Error()}
		return nil, e
	}
	if details := cfg.check(); len(details) > 0 {
		return nil, invalid(source, details...)
	}
	return cfg, nil
}

// Validate checks a config value against the schema and the semantic rules.
// Use it for configs that code builds, for example the bootstrap default.
func (c *Config) Validate() error {
	data, err := yaml.Marshal(c)
	if err != nil {
		return fmt.Errorf("cannot encode the config: %w", err)
	}
	_, err = Parse(data, "config")
	return err
}

// Marshal encodes the config as YAML.
func (c *Config) Marshal() ([]byte, error) {
	var b bytes.Buffer
	enc := yaml.NewEncoder(&b)
	enc.SetIndent(2)
	if err := enc.Encode(c); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

func validateSchema(raw any) []Detail {
	sch, err := schema()
	if err != nil {
		return []Detail{{Message: err.Error(), Keyword: "schema"}}
	}
	js, err := json.Marshal(normalize(raw))
	if err != nil {
		return []Detail{{Message: "cannot convert the YAML document to JSON: " + err.Error(), Keyword: "type"}}
	}
	inst, err := jsonschema.UnmarshalJSON(bytes.NewReader(js))
	if err != nil {
		return []Detail{{Message: "cannot read the converted document: " + err.Error(), Keyword: "type"}}
	}
	err = sch.Validate(inst)
	if err == nil {
		return nil
	}
	var verr *jsonschema.ValidationError
	if !errors.As(err, &verr) {
		return []Detail{{Message: err.Error(), Keyword: "schema"}}
	}
	printer := message.NewPrinter(language.English)
	seen := map[string]bool{}
	var out []Detail
	add := func(d Detail) {
		k := d.Pointer + ": " + d.Message
		if !seen[k] {
			seen[k] = true
			out = append(out, d)
		}
	}
	var walk func(e *jsonschema.ValidationError)
	walk = func(e *jsonschema.ValidationError) {
		if pn, ok := e.ErrorKind.(*kind.PropertyNames); ok {
			// jsonschema v6.0.3 aliases InstanceLocation for propertyNames errors,
			// so derive the location from the schema location.
			add(Detail{Pointer: instanceFromSchema(e.SchemaURL),
				Message: fmt.Sprintf("key %q is not allowed here (care-pi sets this variable, or the name is not valid)", pn.Property),
				Keyword: "propertyNames"})
			return
		}
		if len(e.Causes) == 0 {
			kw := ""
			if kp := e.ErrorKind.KeywordPath(); len(kp) > 0 {
				kw = kp[len(kp)-1]
			}
			ptr := ""
			for _, tok := range e.InstanceLocation {
				ptr += "/" + strings.ReplaceAll(strings.ReplaceAll(tok, "~", "~0"), "/", "~1")
			}
			add(Detail{Pointer: ptr, Message: e.ErrorKind.LocalizedString(printer), Keyword: kw})
			return
		}
		for _, c := range e.Causes {
			walk(c)
		}
	}
	walk(verr)
	sort.Slice(out, func(i, j int) bool { return out[i].Pointer+out[i].Message < out[j].Pointer+out[j].Message })
	return out
}

// Normalize converts a decoded YAML value to a value that encoding/json accepts.
func Normalize(v any) any { return normalize(v) }

// instanceFromSchema maps ".../x.json#/properties/env/properties/backend/propertyNames" to "/env/backend".
func instanceFromSchema(u string) string {
	_, frag, _ := strings.Cut(u, "#")
	var out []string
	for _, tok := range strings.Split(frag, "/") {
		if tok == "" || tok == "properties" || tok == "propertyNames" {
			continue
		}
		out = append(out, tok)
	}
	return "/" + strings.Join(out, "/")
}

// normalize converts YAML values to values that encoding/json accepts.
func normalize(v any) any {
	switch t := v.(type) {
	case map[string]any:
		m := make(map[string]any, len(t))
		for k, val := range t {
			m[k] = normalize(val)
		}
		return m
	case map[any]any:
		m := make(map[string]any, len(t))
		for k, val := range t {
			m[fmt.Sprint(k)] = normalize(val)
		}
		return m
	case []any:
		s := make([]any, len(t))
		for i, val := range t {
			s[i] = normalize(val)
		}
		return s
	default:
		return v
	}
}
