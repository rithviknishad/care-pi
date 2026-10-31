package solution

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/fs"
	"sync"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"go.yaml.in/yaml/v3"

	"github.com/rithviknishad/care-pi/solutions"
)

// SchemaBase is the $id prefix of the schemas in solutions/schema/.
const SchemaBase = "https://github.com/rithviknishad/care-pi/blob/main/solutions/schema/"

// Document kinds (the kind member of a document, and the name of its schema).
const (
	KindSolution = "solution"
	KindLock     = "lock"
	KindIndex    = "index"
)

var (
	schemaOnce sync.Once
	schemas    map[string]*jsonschema.Schema
	schemaErr  error
)

func compileSchemas() {
	c := jsonschema.NewCompiler()
	for _, f := range []string{KindSolution, KindLock, KindIndex} {
		data, err := fs.ReadFile(solutions.FS, "schema/"+f+".schema.json")
		if err != nil {
			schemaErr = fmt.Errorf("cannot read the embedded schema %s: %w", f, err)
			return
		}
		doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
		if err != nil {
			schemaErr = err
			return
		}
		if err := c.AddResource(SchemaBase+f+".schema.json", doc); err != nil {
			schemaErr = err
			return
		}
	}
	schemas = map[string]*jsonschema.Schema{}
	for _, f := range []string{KindSolution, KindLock, KindIndex} {
		sch, err := c.Compile(SchemaBase + f + ".schema.json")
		if err != nil {
			schemaErr = fmt.Errorf("cannot compile the schema %s: %w", f, err)
			return
		}
		schemas[f] = sch
	}
}

// toJSONValue decodes YAML or JSON into the value types of encoding/json.
func toJSONValue(data []byte) (any, error) {
	var raw any
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	js, err := json.Marshal(raw)
	if err != nil {
		return nil, err
	}
	var v any
	d := json.NewDecoder(bytes.NewReader(js))
	d.UseNumber()
	if err := d.Decode(&v); err != nil {
		return nil, err
	}
	return v, nil
}

// ValidateData checks a YAML or JSON document against the schema of kind.
func ValidateData(kind string, data []byte) error {
	schemaOnce.Do(compileSchemas)
	if schemaErr != nil {
		return schemaErr
	}
	sch, ok := schemas[kind]
	if !ok {
		return fmt.Errorf("no schema for the kind %q", kind)
	}
	var raw any
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return err
	}
	js, err := json.Marshal(raw)
	if err != nil {
		return err
	}
	inst, err := jsonschema.UnmarshalJSON(bytes.NewReader(js))
	if err != nil {
		return err
	}
	return sch.Validate(inst)
}

// DetectKind returns the schema kind of a document from its kind member: Solution, SolutionLock, or Index.
func DetectKind(data []byte) (string, error) {
	var head struct {
		Kind string `yaml:"kind"`
	}
	if err := yaml.Unmarshal(data, &head); err != nil {
		return "", fmt.Errorf("the file is not YAML or JSON: %w", err)
	}
	switch head.Kind {
	case "Solution":
		return KindSolution, nil
	case "SolutionLock":
		return KindLock, nil
	case "Index":
		return KindIndex, nil
	}
	return "", fmt.Errorf("the file has the kind %q; use Solution, SolutionLock, or Index", head.Kind)
}

// Validate checks a solution.yaml, a solution lock, or an index.json (by its kind) with its schema
// and the rules that the schema cannot express. It returns the kind.
func Validate(data []byte) (string, error) {
	kind, err := DetectKind(data)
	if err != nil {
		return "", err
	}
	switch kind {
	case KindSolution:
		_, err = Parse(data, "the file")
	case KindLock:
		_, err = ParseLock(data)
	case KindIndex:
		_, err = ParseIndex(data)
	}
	return kind, err
}

// DocumentJSON returns a YAML or JSON document as a JSON value.
func DocumentJSON(data []byte) (any, error) { return toJSONValue(data) }
