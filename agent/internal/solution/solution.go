// Package solution loads solution.yaml files and computes the effective plug set and env.
package solution

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"sort"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"go.yaml.in/yaml/v3"

	"github.com/rithviknishad/care-pi/agent/internal/config"
	"github.com/rithviknishad/care-pi/solutions"
)

// Solution is one solutions/<name>/solution.yaml. See solutions/schema/solution.schema.json.
type Solution struct {
	APIVersion  string   `yaml:"apiVersion"`
	Kind        string   `yaml:"kind"`
	Name        string   `yaml:"name"`
	Title       string   `yaml:"title"`
	Description string   `yaml:"description"`
	Care        Care     `yaml:"care"`
	CareFE      CareFE   `yaml:"care_fe"`
	Plugs       []Plug   `yaml:"plugs"`
	Env         EnvSet   `yaml:"env"`
	Defaults    Defaults `yaml:"defaults"`
}

// Care is the upstream backend image.
type Care struct {
	Image string `yaml:"image"`
	Ref   string `yaml:"ref"`
}

// CareFE is the frontend source.
type CareFE struct {
	Repo string `yaml:"repo"`
	Ref  string `yaml:"ref"`
}

// EnvSet holds backend and frontend variables.
type EnvSet struct {
	Backend  map[string]string `yaml:"backend"`
	Frontend map[string]string `yaml:"frontend"`
}

// Defaults are the wizard defaults of the solution.
type Defaults struct {
	SizePreset  string `yaml:"size_preset"`
	ClockPolicy string `yaml:"clock_policy"`
}

// Plug is one plug of a solution.
type Plug struct {
	ID               string           `yaml:"id"`
	Label            string           `yaml:"label"`
	Description      string           `yaml:"description"`
	Enabled          bool             `yaml:"enabled"`
	Auto             string           `yaml:"auto"`
	RequiresInternet bool             `yaml:"requires_internet"`
	Backend          *BackendPlug     `yaml:"backend"`
	Frontend         *FrontendPlug    `yaml:"frontend"`
	Env              EnvSet           `yaml:"env"`
	Settings         []map[string]any `yaml:"settings"`
	SetupSteps       []string         `yaml:"setup_steps"`
}

// BackendPlug is the Python package of a plug.
type BackendPlug struct {
	Name    string         `yaml:"name"`
	Package string         `yaml:"package"`
	Ref     string         `yaml:"ref"`
	Configs map[string]any `yaml:"configs"`
}

// FrontendPlug is the module federation bundle of a plug.
type FrontendPlug struct {
	Slug string         `yaml:"slug"`
	Repo string         `yaml:"repo"`
	Ref  string         `yaml:"ref"`
	URL  string         `yaml:"url"`
	Meta map[string]any `yaml:"meta"`
}

// AutoWhenMultihost enables a plug when more than one hostname is served.
const AutoWhenMultihost = "when_multihost"

// Load reads a solution. When dir is empty, Load reads the embedded copy.
// Otherwise it reads <dir>/<name>/solution.yaml.
func Load(name, dir string) (*Solution, error) {
	var (
		data []byte
		err  error
		src  string
	)
	if dir == "" {
		src = "embedded solutions/" + name + "/solution.yaml"
		data, err = fs.ReadFile(solutions.FS, name+"/solution.yaml")
	} else {
		src = dir + "/" + name + "/solution.yaml"
		data, err = os.ReadFile(src)
	}
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("solution %q does not exist (%s)", name, src)
		}
		return nil, fmt.Errorf("cannot read solution %q: %w", name, err)
	}
	return Parse(data, src)
}

// Parse validates and decodes a solution document.
func Parse(data []byte, src string) (*Solution, error) {
	if err := validate(data); err != nil {
		return nil, fmt.Errorf("%s is not valid: %w", src, err)
	}
	var s Solution
	if err := yaml.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("cannot decode %s: %w", src, err)
	}
	seen := map[string]bool{}
	for _, p := range s.Plugs {
		if seen[p.ID] {
			return nil, fmt.Errorf("%s is not valid: plug %q occurs more than once", src, p.ID)
		}
		seen[p.ID] = true
	}
	return &s, nil
}

func validate(data []byte) error {
	schemaData, err := fs.ReadFile(solutions.FS, "schema/solution.schema.json")
	if err != nil {
		return fmt.Errorf("cannot read the embedded solution schema: %w", err)
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(schemaData))
	if err != nil {
		return err
	}
	c := jsonschema.NewCompiler()
	const url = "solution.schema.json"
	if err := c.AddResource(url, doc); err != nil {
		return err
	}
	sch, err := c.Compile(url)
	if err != nil {
		return err
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

// EffectivePlug is a plug after the config overrides.
type EffectivePlug struct {
	Plug
	// On reports whether the plug runs on this host.
	On bool
	// Custom reports whether config.yaml changed or added the plug.
	Custom bool
}

// HasBackend reports whether the plug has a backend part.
func (p EffectivePlug) HasBackend() bool { return p.Backend != nil }

// Effective returns the plugs of the solution after the config overrides.
// hostnames is the number of hostnames that the host serves.
func (s *Solution) Effective(cfg *config.Config, hostnames int) ([]EffectivePlug, error) {
	out := make([]EffectivePlug, 0, len(s.Plugs))
	index := map[string]int{}
	for _, p := range s.Plugs {
		index[p.ID] = len(out)
		out = append(out, EffectivePlug{Plug: clonePlug(p)})
	}
	if cfg.Solution.Track == "custom" {
		for _, c := range cfg.Solution.Custom.Plugs {
			i, ok := index[c.ID]
			if !ok {
				p := Plug{ID: c.ID, Label: c.ID, Enabled: true}
				if c.Backend != nil {
					if c.Backend.Name == "" || c.Backend.Package == "" {
						return nil, fmt.Errorf("solution.custom.plugs %q: a new plug needs backend.name and backend.package", c.ID)
					}
					p.Backend = &BackendPlug{}
				}
				if c.Frontend != nil {
					p.Frontend = &FrontendPlug{Slug: c.ID}
				}
				if p.Backend == nil && p.Frontend == nil {
					return nil, fmt.Errorf("solution.custom.plugs %q: a new plug needs a backend or a frontend part", c.ID)
				}
				i = len(out)
				index[c.ID] = i
				out = append(out, EffectivePlug{Plug: p})
			}
			ep := &out[i]
			ep.Custom = true
			if c.Enabled != nil {
				ep.Enabled = *c.Enabled
			}
			if c.Backend != nil {
				if ep.Backend == nil {
					ep.Backend = &BackendPlug{}
				}
				if c.Backend.Name != "" {
					ep.Backend.Name = c.Backend.Name
				}
				if c.Backend.Package != "" {
					ep.Backend.Package = c.Backend.Package
				}
				if c.Backend.Ref != "" {
					ep.Backend.Ref = c.Backend.Ref
				}
				if c.Backend.Configs != nil {
					ep.Backend.Configs = c.Backend.Configs
				}
				if ep.Backend.Ref == "" {
					ep.Backend.Ref = "main"
				}
			}
			if c.Frontend != nil {
				if ep.Frontend == nil {
					ep.Frontend = &FrontendPlug{Slug: c.ID}
				}
				if c.Frontend.Repo != "" {
					ep.Frontend.Repo = c.Frontend.Repo
				}
				if c.Frontend.Ref != "" {
					ep.Frontend.Ref = c.Frontend.Ref
				}
				if c.Frontend.URL != "" {
					ep.Frontend.URL = c.Frontend.URL
				}
			}
		}
	}
	for i := range out {
		p := &out[i]
		p.On = p.Enabled || (p.Auto == AutoWhenMultihost && hostnames > 1)
	}
	return out, nil
}

func clonePlug(p Plug) Plug {
	if p.Backend != nil {
		b := *p.Backend
		p.Backend = &b
	}
	if p.Frontend != nil {
		f := *p.Frontend
		p.Frontend = &f
	}
	return p
}

// DefaultBackendPlugs returns the ids and refs of the backend plugs that CI puts into the
// solution image: the plugs with enabled: true and a backend part.
func (s *Solution) DefaultBackendPlugs() map[string]string {
	out := map[string]string{}
	for _, p := range s.Plugs {
		if p.Enabled && p.Backend != nil {
			out[p.ID] = p.Backend.Ref
		}
	}
	return out
}

// MultiValueFrontendKeys are the frontend variables whose values the merge joins with commas.
var MultiValueFrontendKeys = map[string]bool{"REACT_MFE_REGISTERED_COMPONENTS": true}

// MergeEnv merges env maps from low to high precedence.
// For the keys in multi, the merge joins the distinct values with commas instead.
func MergeEnv(multi map[string]bool, layers ...map[string]string) map[string]string {
	out := map[string]string{}
	for _, layer := range layers {
		for k, v := range layer {
			if multi[k] && out[k] != "" {
				out[k] = joinDistinct(out[k], v)
				continue
			}
			out[k] = v
		}
	}
	return out
}

func joinDistinct(a, b string) string {
	var parts []string
	seen := map[string]bool{}
	for _, s := range append(strings.Split(a, ","), strings.Split(b, ",")...) {
		s = strings.TrimSpace(s)
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		parts = append(parts, s)
	}
	return strings.Join(parts, ",")
}

// PlugEnv returns the merged env of the plugs that are on, in plug order.
func PlugEnv(plugs []EffectivePlug) EnvSet {
	be, fe := map[string]string{}, map[string]string{}
	for _, p := range plugs {
		if !p.On {
			continue
		}
		be = MergeEnv(nil, be, p.Env.Backend)
		fe = MergeEnv(MultiValueFrontendKeys, fe, p.Env.Frontend)
	}
	return EnvSet{Backend: be, Frontend: fe}
}

// SortedKeys returns the keys of m in order.
func SortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
