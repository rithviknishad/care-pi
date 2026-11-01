package solution

import (
	"fmt"
	"regexp"
	"strings"
)

// Setting is one field of the settings form of a plug (solution.schema.json, $defs/setting).
type Setting struct {
	Part        string   `json:"part"`
	Key         string   `json:"key"`
	Label       string   `json:"label"`
	Help        *string  `json:"help"`
	Type        string   `json:"type"`
	Required    bool     `json:"required"`
	Placeholder *string  `json:"placeholder"`
	Options     []Option `json:"options"`
	Default     any      `json:"default"`
	Min         *float64 `json:"min"`
	Max         *float64 `json:"max"`
	Pattern     *string  `json:"pattern"`
	Advanced    bool     `json:"advanced"`
}

// Option is one option of a choices field.
type Option struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

// Setting parts and types.
const (
	PartBackend  = "backend"
	PartFrontend = "frontend"
	TypeSecret   = "secret"
)

// TypedSettings returns the settings form of the plug.
func (p Plug) TypedSettings() []Setting {
	out := make([]Setting, 0, len(p.Settings))
	for _, m := range p.Settings {
		s := Setting{Part: str(m["part"]), Key: str(m["key"]), Label: str(m["label"]), Type: str(m["type"]), Default: m["default"]}
		if v, ok := m["help"].(string); ok {
			s.Help = &v
		}
		if v, ok := m["placeholder"].(string); ok {
			s.Placeholder = &v
		}
		if v, ok := m["pattern"].(string); ok {
			s.Pattern = &v
		}
		s.Required, _ = m["required"].(bool)
		s.Advanced, _ = m["advanced"].(bool)
		s.Min, s.Max = num(m["min"]), num(m["max"])
		if opts, ok := m["options"].([]any); ok {
			for _, o := range opts {
				om, _ := o.(map[string]any)
				s.Options = append(s.Options, Option{Value: str(om["value"]), Label: str(om["label"])})
			}
		}
		out = append(out, s)
	}
	return out
}

func str(v any) string {
	s, _ := v.(string)
	return s
}

func num(v any) *float64 {
	switch n := v.(type) {
	case int:
		f := float64(n)
		return &f
	case int64:
		f := float64(n)
		return &f
	case float64:
		return &n
	}
	return nil
}

// SettingsSchema returns the JSON Schema (draft 2020-12) of the body member settings of PUT /plugs/{id}
// (docs/api/README.md, section 14.6).
func SettingsSchema(p Plug) map[string]any {
	parts := map[string]map[string]any{}
	for _, part := range []string{PartBackend, PartFrontend} {
		parts[part] = map[string]any{"type": "object", "properties": map[string]any{}, "additionalProperties": false}
	}
	for _, s := range p.TypedSettings() {
		prop := valueSchema(s)
		prop["title"] = s.Label
		if s.Help != nil {
			prop["description"] = *s.Help
		}
		if s.Default != nil {
			prop["default"] = s.Default
		}
		if s.Advanced {
			prop["x-advanced"] = true
		}
		if s.Placeholder != nil {
			prop["x-placeholder"] = *s.Placeholder
		}
		part := parts[s.Part]
		if part == nil {
			continue
		}
		part["properties"].(map[string]any)[s.Key] = prop
		if s.Required {
			req, _ := part["required"].([]any)
			part["required"] = append(req, s.Key)
		}
	}
	return map[string]any{
		"$schema":              "https://json-schema.org/draft/2020-12/schema",
		"title":                "Settings of " + p.Label,
		"type":                 "object",
		"properties":           map[string]any{PartBackend: parts[PartBackend], PartFrontend: parts[PartFrontend]},
		"additionalProperties": false,
	}
}

func valueSchema(s Setting) map[string]any {
	switch s.Type {
	case "boolean":
		return map[string]any{"type": "boolean"}
	case "number":
		m := map[string]any{"type": "number"}
		if s.Min != nil {
			m["minimum"] = *s.Min
		}
		if s.Max != nil {
			m["maximum"] = *s.Max
		}
		return m
	case "url":
		return map[string]any{"type": "string", "format": "uri", "pattern": "^https?://"}
	case TypeSecret:
		m := map[string]any{"type": []any{"string", "null"}, "writeOnly": true}
		if s.Pattern != nil {
			m["pattern"] = *s.Pattern
		}
		return m
	case "list":
		items := map[string]any{"type": "string", "minLength": 1}
		if s.Pattern != nil {
			items["pattern"] = *s.Pattern
		}
		return map[string]any{"type": "array", "items": items}
	case "choices":
		var one []any
		for _, o := range s.Options {
			one = append(one, map[string]any{"const": o.Value, "title": o.Label})
		}
		return map[string]any{"type": "array", "uniqueItems": true, "items": map[string]any{"oneOf": one}}
	}
	m := map[string]any{"type": "string"}
	if s.Pattern != nil {
		m["pattern"] = *s.Pattern
	}
	return m
}

// FieldError is one setting that is not valid. Pointer is relative to the body (/settings/<part>/<key>).
type FieldError struct {
	Pointer string
	Message string
	Code    string
}

// Pointer returns the JSON pointer of a setting in the body of PUT /plugs/{id}.
func Pointer(part, key string) string {
	k := strings.ReplaceAll(strings.ReplaceAll(key, "~", "~0"), "/", "~1")
	return "/settings/" + part + "/" + k
}

// CheckValue checks one value against its field. nil is always valid here (it removes a value).
func CheckValue(s Setting, v any) *FieldError {
	if v == nil {
		return nil
	}
	ptr := Pointer(s.Part, s.Key)
	bad := func(msg, code string) *FieldError { return &FieldError{Pointer: ptr, Message: msg, Code: code} }
	matches := func(x string) bool {
		if s.Pattern == nil {
			return true
		}
		re, err := regexp.Compile(*s.Pattern)
		return err != nil || re.MatchString(x)
	}
	switch s.Type {
	case "boolean":
		if _, ok := v.(bool); !ok {
			return bad("must be true or false", "type")
		}
	case "number":
		f, ok := toFloat(v)
		if !ok {
			return bad("must be a number", "type")
		}
		if s.Min != nil && f < *s.Min {
			return bad(fmt.Sprintf("must be %v or more", *s.Min), "minimum")
		}
		if s.Max != nil && f > *s.Max {
			return bad(fmt.Sprintf("must be %v or less", *s.Max), "maximum")
		}
	case "url":
		x, ok := v.(string)
		if !ok {
			return bad("must be a string", "type")
		}
		if x != "" && !strings.HasPrefix(x, "https://") && !strings.HasPrefix(x, "http://") {
			return bad("must be an http or https URL", "format")
		}
	case "list":
		items, ok := v.([]any)
		if !ok {
			return bad("must be a list of strings", "type")
		}
		for _, it := range items {
			x, ok := it.(string)
			if !ok || x == "" {
				return bad("each line must be a non-empty string", "type")
			}
			if !matches(x) {
				return bad("a line does not match the pattern", "pattern")
			}
		}
	case "choices":
		items, ok := v.([]any)
		if !ok {
			return bad("must be a list of options", "type")
		}
		seen := map[string]bool{}
		for _, it := range items {
			x, _ := it.(string)
			found := false
			for _, o := range s.Options {
				found = found || o.Value == x
			}
			if !found {
				return bad(fmt.Sprintf("%q is not an option", x), "enum")
			}
			if seen[x] {
				return bad("an option occurs more than once", "uniqueItems")
			}
			seen[x] = true
		}
	default:
		x, ok := v.(string)
		if !ok {
			return bad("must be a string", "type")
		}
		if !matches(x) {
			return bad("does not match the pattern", "pattern")
		}
	}
	return nil
}

func toFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case interface{ Float64() (float64, error) }:
		f, err := n.Float64()
		return f, err == nil
	}
	return 0, false
}

// Empty reports whether a value counts as "no value" for a required field.
func Empty(v any) bool {
	switch x := v.(type) {
	case nil:
		return true
	case string:
		return x == ""
	case []any:
		return len(x) == 0
	}
	return false
}

// GetPath returns the value at a dotted path in m.
func GetPath(m map[string]any, dotted string) (any, bool) {
	cur := any(m)
	for _, k := range strings.Split(dotted, ".") {
		mm, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		cur, ok = mm[k]
		if !ok {
			return nil, false
		}
	}
	return cur, true
}

// SetPath sets the value at a dotted path in m. It creates the objects on the way.
func SetPath(m map[string]any, dotted string, v any) {
	parts := strings.Split(dotted, ".")
	cur := m
	for _, k := range parts[:len(parts)-1] {
		next, ok := cur[k].(map[string]any)
		if !ok {
			next = map[string]any{}
			cur[k] = next
		}
		cur = next
	}
	cur[parts[len(parts)-1]] = v
}

// DeepCopy copies a JSON-like value.
func DeepCopy(v any) any {
	switch x := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, e := range x {
			out[k] = DeepCopy(e)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = DeepCopy(e)
		}
		return out
	}
	return v
}
