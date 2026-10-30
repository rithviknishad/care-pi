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
		return nil, &ValidationError{Source: source, Problems: []string{"YAML syntax: " + err.Error()}}
	}
	if raw == nil {
		return nil, &ValidationError{Source: source, Problems: []string{"the file is empty; set at least \"version: 1\""}}
	}
	if problems := validateSchema(raw); len(problems) > 0 {
		return nil, &ValidationError{Source: source, Problems: problems}
	}
	cfg := Default()
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(cfg); err != nil {
		return nil, &ValidationError{Source: source, Problems: []string{err.Error()}}
	}
	if problems := cfg.check(); len(problems) > 0 {
		return nil, &ValidationError{Source: source, Problems: problems}
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

func validateSchema(raw any) []string {
	sch, err := schema()
	if err != nil {
		return []string{err.Error()}
	}
	js, err := json.Marshal(normalize(raw))
	if err != nil {
		return []string{"cannot convert the YAML document to JSON: " + err.Error()}
	}
	inst, err := jsonschema.UnmarshalJSON(bytes.NewReader(js))
	if err != nil {
		return []string{"cannot read the converted document: " + err.Error()}
	}
	err = sch.Validate(inst)
	if err == nil {
		return nil
	}
	var verr *jsonschema.ValidationError
	if !errors.As(err, &verr) {
		return []string{err.Error()}
	}
	printer := message.NewPrinter(language.English)
	seen := map[string]bool{}
	var out []string
	var walk func(e *jsonschema.ValidationError)
	walk = func(e *jsonschema.ValidationError) {
		if pn, ok := e.ErrorKind.(*kind.PropertyNames); ok {
			// jsonschema v6.0.3 aliases InstanceLocation for propertyNames errors,
			// so derive the location from the schema location.
			loc := instanceFromSchema(e.SchemaURL)
			msg := fmt.Sprintf("%s: key %q is not allowed here (care-pi sets this variable, or the name is not valid)", loc, pn.Property)
			if !seen[msg] {
				seen[msg] = true
				out = append(out, msg)
			}
			return
		}
		if len(e.Causes) == 0 {
			loc := "/" + strings.Join(e.InstanceLocation, "/")
			msg := loc + ": " + e.ErrorKind.LocalizedString(printer)
			if !seen[msg] {
				seen[msg] = true
				out = append(out, msg)
			}
			return
		}
		for _, c := range e.Causes {
			walk(c)
		}
	}
	walk(verr)
	sort.Strings(out)
	return out
}

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
