package solution

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strings"

	"go.yaml.in/yaml/v3"
)

// Lock is one solution lock: solutions/<name>/locks/<version>.lock.yaml (docs/solutions.md, section 3).
// Each identity in it is immutable.
type Lock struct {
	APIVersion      string              `yaml:"apiVersion" json:"apiVersion"`
	Kind            string              `yaml:"kind" json:"kind"`
	Name            string              `yaml:"name" json:"name"`
	Version         string              `yaml:"version" json:"version"`
	CreatedAt       string              `yaml:"created_at" json:"created_at"`
	CreatedBy       string              `yaml:"created_by" json:"created_by"`
	Notes           string              `yaml:"notes" json:"notes"`
	MinAgentVersion string              `yaml:"min_agent_version" json:"min_agent_version"`
	Source          LockSource          `yaml:"source" json:"source"`
	SolutionDoc     map[string]any      `yaml:"solution" json:"solution"`
	Care            LockCare            `yaml:"care" json:"care"`
	SolutionImage   LockSolutionImage   `yaml:"solution_image" json:"solution_image"`
	CareFE          LockCareFE          `yaml:"care_fe" json:"care_fe"`
	FE              LockFE              `yaml:"fe" json:"fe"`
	Plugs           map[string]LockPlug `yaml:"plugs" json:"plugs"`
	Images          map[string]LockImg  `yaml:"images" json:"images"`

	// Solution is SolutionDoc, decoded and checked.
	Solution *Solution `yaml:"-" json:"-"`
	// SHA256 is the SHA-256 of the file bytes. Raw holds the bytes.
	SHA256 string `yaml:"-" json:"-"`
	Raw    []byte `yaml:"-" json:"-"`
}

// LockSource records where the lock comes from.
type LockSource struct {
	Commit         string  `yaml:"commit" json:"commit"`
	SolutionSHA256 string  `yaml:"solution_sha256" json:"solution_sha256"`
	RunURL         *string `yaml:"run_url" json:"run_url"`
}

// LockCare is the care image: upstream (source image), or built by CI from the git source (source git, S71).
type LockCare struct {
	Image  string  `yaml:"image" json:"image"`
	Ref    string  `yaml:"ref" json:"ref"`
	Digest string  `yaml:"digest" json:"digest"`
	GitSHA *string `yaml:"git_sha" json:"git_sha"`
	Source string  `yaml:"source,omitempty" json:"source,omitempty"`
	Repo   string  `yaml:"repo,omitempty" json:"repo,omitempty"`
}

// IsGit reports whether CI built the care image from the git source.
func (c LockCare) IsGit() bool { return c.Source == "git" }

// BaseRef returns the reference of the care image with a tag: <image>:<sha12> for source git (CI tags the image with
// the commit), else <image>:<ref> or <image>@<ref> for a digest.
func (c LockCare) BaseRef() string {
	switch {
	case c.IsGit() && c.GitSHA != nil:
		return c.Image + ":" + GitTag(*c.GitSHA)
	case strings.HasPrefix(c.Ref, "sha256:"):
		return c.Image + "@" + c.Ref
	}
	return c.Image + ":" + c.Ref
}

// LockSolutionImage is the image that api, worker, and beat run.
type LockSolutionImage struct {
	Image          string   `yaml:"image" json:"image"`
	Tag            string   `yaml:"tag" json:"tag"`
	Digest         string   `yaml:"digest" json:"digest"`
	InstalledPlugs []string `yaml:"installed_plugs" json:"installed_plugs"`
}

// Ref returns the image reference with the tag.
func (i LockSolutionImage) Ref() string { return i.Image + ":" + i.Tag }

// LockCareFE is the care_fe source.
type LockCareFE struct {
	Repo string `yaml:"repo" json:"repo"`
	Ref  string `yaml:"ref" json:"ref"`
	SHA  string `yaml:"sha" json:"sha"`
}

// LockFE is the default FE bundle.
type LockFE struct {
	BuildID   string   `yaml:"build_id" json:"build_id"`
	URL       string   `yaml:"url" json:"url"`
	SHA256    string   `yaml:"sha256" json:"sha256"`
	SizeBytes int64    `yaml:"size_bytes" json:"size_bytes"`
	EnvKeys   []string `yaml:"env_keys" json:"env_keys"`
}

// LockPlug holds the resolved parts of one plug.
type LockPlug struct {
	Backend  *LockPlugBackend  `yaml:"backend,omitempty" json:"backend,omitempty"`
	Frontend *LockPlugFrontend `yaml:"frontend,omitempty" json:"frontend,omitempty"`
}

// LockPlugBackend is the backend part of a plug.
type LockPlugBackend struct {
	Ref            string         `yaml:"ref" json:"ref"`
	SHA            string         `yaml:"sha" json:"sha"`
	AdditionalPlug AdditionalPlug `yaml:"additional_plug" json:"additional_plug"`
}

// AdditionalPlug is one entry of ADDITIONAL_PLUGS (upstream plugs/plug.py).
type AdditionalPlug struct {
	Name        string         `yaml:"name" json:"name"`
	PackageName string         `yaml:"package_name" json:"package_name"`
	Version     string         `yaml:"version" json:"version"`
	Configs     map[string]any `yaml:"configs" json:"configs"`
}

// LockPlugFrontend is the frontend part of a plug: a bundle that CI built, or an external URL.
type LockPlugFrontend struct {
	Slug      string  `yaml:"slug" json:"slug"`
	Source    string  `yaml:"source" json:"source"`
	Ref       *string `yaml:"ref" json:"ref"`
	SHA       *string `yaml:"sha" json:"sha"`
	URL       string  `yaml:"url" json:"url"`
	SHA256    *string `yaml:"sha256" json:"sha256"`
	Entry     *string `yaml:"entry" json:"entry"`
	SizeBytes *int64  `yaml:"size_bytes" json:"size_bytes"`
}

// Frontend sources.
const (
	FrontendBuilt      = "built"
	FrontendExternal   = "external"
	FrontendLocalBuild = "local-build"
)

// LockImg is one base image.
type LockImg struct {
	Ref    string `yaml:"ref" json:"ref"`
	Digest string `yaml:"digest" json:"digest"`
}

// ParseLock validates a solution lock file against lock.schema.json and decodes it.
func ParseLock(data []byte) (*Lock, error) {
	if err := ValidateData(KindLock, data); err != nil {
		return nil, fmt.Errorf("the solution lock is not valid: %w", err)
	}
	var l Lock
	if err := yaml.Unmarshal(data, &l); err != nil {
		return nil, fmt.Errorf("cannot decode the solution lock: %w", err)
	}
	docData, err := yaml.Marshal(l.SolutionDoc)
	if err != nil {
		return nil, err
	}
	sol, err := Parse(docData, "solution of the lock "+l.Name+" "+l.Version)
	if err != nil {
		return nil, err
	}
	if sol.Name != l.Name {
		return nil, fmt.Errorf("the solution lock %s has the solution %q", l.Name, sol.Name)
	}
	l.SolutionDoc = sol.Doc
	l.Solution = sol
	for _, p := range sol.Plugs {
		if _, ok := l.Plugs[p.ID]; !ok {
			return nil, fmt.Errorf("the solution lock %s %s has no entry for the plug %s", l.Name, l.Version, p.ID)
		}
	}
	sum := sha256.Sum256(data)
	l.SHA256 = hex.EncodeToString(sum[:])
	l.Raw = append([]byte(nil), data...)
	return &l, nil
}

// JSON returns the lock as a JSON value (schema SolutionLockDocument).
func (l *Lock) JSON() (any, error) {
	return toJSONValue(l.Raw)
}

// Index is index.json (docs/solutions.md, section 4).
type Index struct {
	APIVersion  string          `json:"apiVersion"`
	Kind        string          `json:"kind"`
	GeneratedAt string          `json:"generated_at"`
	DistBase    string          `json:"dist_base"`
	Agent       *IndexAgent     `json:"agent,omitempty"`
	Solutions   []IndexSolution `json:"solutions"`
	OS          *IndexOS        `json:"os,omitempty"`
}

// IndexAgent is the newest agent release.
type IndexAgent struct {
	Latest     string `json:"latest"`
	ReleaseURL string `json:"release_url"`
}

// IndexSolution is one solution of the index.
type IndexSolution struct {
	Name        string      `json:"name"`
	Title       string      `json:"title"`
	Description string      `json:"description"`
	Latest      *string     `json:"latest"`
	Locks       []IndexLock `json:"locks"`
}

// IndexLock is one released lock.
type IndexLock struct {
	Version         string  `json:"version"`
	URL             string  `json:"url"`
	SHA256          string  `json:"sha256"`
	ReleasedAt      string  `json:"released_at"`
	MinAgentVersion string  `json:"min_agent_version"`
	ChangelogURL    *string `json:"changelog_url"`
	Summary         string  `json:"summary"`
	Withdrawn       bool    `json:"withdrawn"`
	WithdrawnReason *string `json:"withdrawn_reason"`
}

// IndexOS holds the tested versions of the held packages by Debian code name.
type IndexOS struct {
	HeldPackages map[string]map[string]string `json:"held_packages,omitempty"`
}

// ParseIndex validates index.json against index.schema.json and decodes it.
func ParseIndex(data []byte) (*Index, error) {
	if err := ValidateData(KindIndex, data); err != nil {
		return nil, fmt.Errorf("index.json is not valid: %w", err)
	}
	var ix Index
	v, err := toJSONValue(data)
	if err != nil {
		return nil, err
	}
	js, _ := json.Marshal(v)
	if err := json.Unmarshal(js, &ix); err != nil {
		return nil, fmt.Errorf("cannot decode index.json: %w", err)
	}
	for i := range ix.Solutions {
		s := &ix.Solutions[i]
		sort.SliceStable(s.Locks, func(a, b int) bool { return CompareLockVersions(s.Locks[a].Version, s.Locks[b].Version) > 0 })
	}
	return &ix, nil
}

// Find returns the entry of the solution, or nil.
func (ix *Index) Find(name string) *IndexSolution {
	if ix == nil {
		return nil
	}
	for i := range ix.Solutions {
		if ix.Solutions[i].Name == name {
			return &ix.Solutions[i]
		}
	}
	return nil
}

// Lock returns the entry of the version, or nil.
func (s *IndexSolution) Lock(version string) *IndexLock {
	if s == nil {
		return nil
	}
	for i := range s.Locks {
		if CompareLockVersions(s.Locks[i].Version, version) == 0 {
			return &s.Locks[i]
		}
	}
	return nil
}

// Newest returns the newest lock that is not withdrawn, or nil.
func (s *IndexSolution) Newest() *IndexLock {
	if s == nil {
		return nil
	}
	for i := range s.Locks {
		if !s.Locks[i].Withdrawn {
			return &s.Locks[i]
		}
	}
	return nil
}

// ResolveURL resolves an artefact reference (<tag>/<asset>, or an absolute URL) against the dist base.
func ResolveURL(distBase, ref string) string {
	if strings.Contains(ref, "://") {
		return ref
	}
	base, err := url.Parse(distBase)
	if err != nil {
		return strings.TrimSuffix(distBase, "/") + "/" + ref
	}
	r, err := url.Parse(ref)
	if err != nil {
		return strings.TrimSuffix(distBase, "/") + "/" + ref
	}
	return base.ResolveReference(r).String()
}
