// Package solution loads solution.yaml files and computes the effective plug set and env.
package solution

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"sort"
	"strings"

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
	// MinAgentVersion is the oldest agent that can install a lock of the solution (Phase 4).
	MinAgentVersion string `yaml:"min_agent_version"`
	// Doc is the document as data, for the API (SolutionDocument) and the lock (solution).
	Doc map[string]any `yaml:"-"`
}

// Care is the CARE backend: an upstream image (source image), or a git ref that CI builds (source git, S71).
type Care struct {
	Image string `yaml:"image"`
	Ref   string `yaml:"ref"`
	// Source is image (or empty) or git. Repo is the git repository of source git.
	Source string `yaml:"source"`
	Repo   string `yaml:"repo"`
	// Resolved is the image tag of a source git after the resolution of Ref (the first 12 hex digits of the sha);
	// the agent sets it, it is not in solution.yaml.
	Resolved string `yaml:"-"`
}

// The CARE images.
const (
	// CareUpstream is the upstream image (source image).
	CareUpstream = "ghcr.io/ohcnetwork/care"
	// CareBase is the repository of the images that CI builds from the git source (source git, S71).
	CareBase = "ghcr.io/rithviknishad/care-pi/care-base"
	// CareBaseLocal is the repository of a local build from the git source (track custom, 4 GB or more).
	CareBaseLocal = "localhost/care-pi/care-base"
)

// IsGit reports whether CI builds the CARE image from the git source.
func (c Care) IsGit() bool { return c.Source == "git" }

// ImageRepo returns the image repository: Image, or the default of the source.
func (c Care) ImageRepo() string {
	switch {
	case c.Image != "":
		return c.Image
	case c.IsGit():
		return CareBase
	}
	return CareUpstream
}

// GitTag returns the image tag of a commit of the git source: its first 12 hex digits.
func GitTag(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
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
	// Entry is the path of remoteEntry.js in the built bundle (Phase 4).
	Entry string `yaml:"entry"`
	// Build tells how CI and the local builder build the bundle (Phase 4).
	Build *FrontendBuild `yaml:"build"`
}

// FrontendBuild is the npm script and the output directory of a plug bundle.
type FrontendBuild struct {
	Script string `yaml:"script"`
	Out    string `yaml:"out"`
}

// EntryPath returns the entry of the bundle, with the default.
func (f *FrontendPlug) EntryPath() string {
	if f == nil || f.Entry == "" {
		return "assets/remoteEntry.js"
	}
	return f.Entry
}

// BuildScript returns the npm script of the bundle, with the default.
func (f *FrontendPlug) BuildScript() string {
	if f == nil || f.Build == nil || f.Build.Script == "" {
		return "build"
	}
	return f.Build.Script
}

// BuildOut returns the output directory of the bundle, with the default.
func (f *FrontendPlug) BuildOut() string {
	if f == nil || f.Build == nil || f.Build.Out == "" {
		return "dist"
	}
	return f.Build.Out
}

// External reports whether the browser loads the bundle from url (no repo).
func (f *FrontendPlug) External() bool { return f != nil && f.Repo == "" && f.URL != "" }

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
	if err := ValidateData(KindSolution, data); err != nil {
		return nil, fmt.Errorf("%s is not valid: %w", src, err)
	}
	var s Solution
	if err := yaml.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("cannot decode %s: %w", src, err)
	}
	doc, err := toJSONValue(data)
	if err != nil {
		return nil, fmt.Errorf("cannot decode %s: %w", src, err)
	}
	s.Doc, _ = doc.(map[string]any)
	if err := checkPlugIDs(&s); err != nil {
		return nil, fmt.Errorf("%s is not valid: %w", src, err)
	}
	return &s, nil
}

// checkPlugIDs checks the rule that the schema cannot express: each plug id occurs one time.
func checkPlugIDs(s *Solution) error {
	seen := map[string]bool{}
	for _, p := range s.Plugs {
		if seen[p.ID] {
			return fmt.Errorf("plug %q occurs more than once", p.ID)
		}
		seen[p.ID] = true
	}
	return nil
}

// EffectivePlug is a plug after the config overrides.
type EffectivePlug struct {
	Plug
	// On reports whether the plug runs on this host.
	On bool
	// Custom reports whether config.yaml changed or added the plug.
	Custom bool
	// Reason is the rule that gives On (docs/solutions.md, section 1.3): custom, operator, auto, or solution.
	Reason string
	// AutoActive reports whether the auto rule turns the plug on now.
	AutoActive bool
}

// Reasons of EffectivePlug.Reason.
const (
	ReasonCustom   = "custom"
	ReasonOperator = "operator"
	ReasonAuto     = "auto"
	ReasonSolution = "solution"
)

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
	customEnabled := map[string]bool{}
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
				customEnabled[c.ID] = *c.Enabled
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
		p.AutoActive = p.Auto == AutoWhenMultihost && hostnames > 1
		choice, chosen := cfg.Solution.Plugs[p.ID]
		v, custom := customEnabled[p.ID]
		switch {
		case custom:
			p.On, p.Reason = v, ReasonCustom
		case chosen && choice.Enabled != nil:
			p.On, p.Reason = *choice.Enabled, ReasonOperator
		case p.AutoActive:
			p.On, p.Reason = true, ReasonAuto
		default:
			p.On, p.Reason = p.Enabled, ReasonSolution
		}
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
// solution image. From Phase 4 this is each plug of the solution with a backend part, on or off
// (the superset rule, docs/solutions.md section 1.5).
func (s *Solution) DefaultBackendPlugs() map[string]string {
	out := map[string]string{}
	for _, p := range s.Plugs {
		if p.Backend != nil {
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

// RegisteredComponents is the frontend variable that registers the components that plugs override.
const RegisteredComponents = "REACT_MFE_REGISTERED_COMPONENTS"

// PlugEnv returns the merged env of the plugs that are on, in plug order.
// REACT_MFE_REGISTERED_COMPONENTS has the components of each plug, on or off (the superset rule,
// docs/solutions.md section 1.5), so a plug switch does not change the FE build id.
func PlugEnv(plugs []EffectivePlug) EnvSet {
	be, fe := map[string]string{}, map[string]string{}
	for _, p := range plugs {
		if v := p.Env.Frontend[RegisteredComponents]; v != "" {
			fe = MergeEnv(MultiValueFrontendKeys, fe, map[string]string{RegisteredComponents: v})
		}
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
