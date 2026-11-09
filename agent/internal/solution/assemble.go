package solution

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"
)

// LockInput holds the resolved identities of a new solution lock. CI (solution-lock.yml) and
// solutions/scripts/make-lock.sh write it as JSON; "care-pi solution lock" assembles the lock.
type LockInput struct {
	// SolutionFile is the path of solution.yaml (CLI); Solution is its content.
	SolutionFile    string `json:"solution_file,omitempty"`
	Solution        []byte `json:"-"`
	Version         string `json:"version"`
	CreatedAt       string `json:"created_at,omitempty"`
	CreatedBy       string `json:"created_by"`
	Notes           string `json:"notes"`
	MinAgentVersion string `json:"min_agent_version,omitempty"`
	Commit          string `json:"commit"`
	RunURL          string `json:"run_url,omitempty"`
	Care            struct {
		Digest string `json:"digest"`
		GitSHA string `json:"git_sha,omitempty"`
		// Image is the repository of the care image (source git: the image that CI built; default
		// solution.care.image, else the default of the source).
		Image string `json:"image,omitempty"`
	} `json:"care"`
	SolutionImage struct {
		Image  string `json:"image"`
		Tag    string `json:"tag"`
		Digest string `json:"digest"`
	} `json:"solution_image"`
	CareFESHA string `json:"care_fe_sha"`
	FE        LockFE `json:"fe"`
	// Plugs holds the resolved parts of each plug by id.
	Plugs map[string]PlugInput `json:"plugs"`
	// Images holds the base images by name (postgres, caddy, redis, s3, node).
	Images map[string]LockImg `json:"images"`
}

// PlugInput is the resolved parts of one plug.
type PlugInput struct {
	BackendSHA string `json:"backend_sha,omitempty"`
	// Frontend: a built bundle (sha, url, sha256, size_bytes), or nothing for an external bundle.
	FrontendSHA       string `json:"frontend_sha,omitempty"`
	FrontendURL       string `json:"frontend_url,omitempty"`
	FrontendSHA256    string `json:"frontend_sha256,omitempty"`
	FrontendSizeBytes int64  `json:"frontend_size_bytes,omitempty"`
}

// AssembleLock makes the solution lock file (docs/solutions.md, section 3) and checks it with lock.schema.json.
func AssembleLock(in LockInput) ([]byte, error) {
	sol, err := Parse(in.Solution, "solution.yaml")
	if err != nil {
		return nil, err
	}
	if _, ok := ParseLockVersion(in.Version); !ok {
		return nil, fmt.Errorf("the version %q is not YYYY.MM.N", in.Version)
	}
	sum := sha256.Sum256(in.Solution)
	created := in.CreatedAt
	if created == "" {
		created = time.Now().UTC().Format(time.RFC3339)
	}
	minAgent := in.MinAgentVersion
	if minAgent == "" {
		minAgent = sol.MinAgentVersion
	}
	if minAgent == "" {
		return nil, fmt.Errorf("min_agent_version is not set (solution.yaml or the input)")
	}
	var installed []string
	plugs := map[string]any{}
	for _, p := range sol.Plugs {
		pi := in.Plugs[p.ID]
		entry := map[string]any{}
		if p.Backend != nil {
			if !isSHA40(pi.BackendSHA) {
				return nil, fmt.Errorf("plug %s: backend_sha is not a 40-digit sha", p.ID)
			}
			installed = append(installed, p.ID)
			configs := p.Backend.Configs
			if configs == nil {
				configs = map[string]any{}
			}
			entry["backend"] = map[string]any{"ref": p.Backend.Ref, "sha": pi.BackendSHA, "additional_plug": map[string]any{
				"name": p.Backend.Name, "package_name": p.Backend.Package, "version": p.Backend.Version(pi.BackendSHA), "configs": configs}}
			if p.Backend.Subdirectory != "" {
				entry["backend"].(map[string]any)["subdirectory"] = p.Backend.Subdirectory
			}
		}
		if f := p.Frontend; f != nil {
			if f.Repo != "" && pi.FrontendURL != "" {
				fr := map[string]any{"slug": f.Slug, "source": FrontendBuilt, "ref": f.Ref, "sha": pi.FrontendSHA,
					"url": pi.FrontendURL, "sha256": pi.FrontendSHA256, "entry": f.EntryPath(), "size_bytes": pi.FrontendSizeBytes}
				if f.Path != "" {
					fr["path"] = f.Path
				}
				entry["frontend"] = fr
			} else if f.URL != "" {
				entry["frontend"] = map[string]any{"slug": f.Slug, "source": FrontendExternal, "ref": nil, "sha": nil,
					"url": f.URL, "sha256": nil, "entry": nil, "size_bytes": nil}
			} else {
				return nil, fmt.Errorf("plug %s: the frontend has no bundle (no frontend_url and no url)", p.ID)
			}
		}
		plugs[p.ID] = entry
	}
	sort.Strings(installed)
	if installed == nil {
		installed = []string{}
	}
	var runURL any
	if in.RunURL != "" {
		runURL = in.RunURL
	}
	var gitSHA any
	if in.Care.GitSHA != "" {
		gitSHA = in.Care.GitSHA
	}
	envKeys := in.FE.EnvKeys
	if envKeys == nil {
		envKeys = []string{}
	}
	sort.Strings(envKeys)
	doc := yaml.Node{Kind: yaml.MappingNode}
	add := func(k string, v any) {
		var n yaml.Node
		_ = n.Encode(v)
		doc.Content = append(doc.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: k}, &n)
	}
	add("apiVersion", "care-pi/v1")
	add("kind", "SolutionLock")
	add("name", sol.Name)
	add("version", NormalizeLockVersion(in.Version))
	add("created_at", created)
	add("created_by", in.CreatedBy)
	add("notes", in.Notes)
	add("min_agent_version", minAgent)
	add("source", map[string]any{"commit": in.Commit, "solution_sha256": hex.EncodeToString(sum[:]), "run_url": runURL})
	add("solution", sol.Doc)
	care := map[string]any{"image": firstOr(in.Care.Image, sol.Care.ImageRepo()), "ref": firstOr(sol.Care.Ref, "latest"),
		"digest": in.Care.Digest, "git_sha": gitSHA}
	if sol.Care.IsGit() {
		if in.Care.GitSHA == "" {
			return nil, fmt.Errorf("care.source is git: the input needs care.git_sha (the commit that CI built)")
		}
		care["source"], care["repo"] = "git", sol.Care.Repo
	}
	add("care", care)
	add("solution_image", map[string]any{"image": in.SolutionImage.Image, "tag": in.SolutionImage.Tag, "digest": in.SolutionImage.Digest,
		"installed_plugs": installed})
	add("care_fe", map[string]any{"repo": sol.CareFE.Repo, "ref": sol.CareFE.Ref, "sha": in.CareFESHA})
	add("fe", map[string]any{"build_id": in.FE.BuildID, "url": in.FE.URL, "sha256": in.FE.SHA256, "size_bytes": in.FE.SizeBytes, "env_keys": envKeys})
	add("plugs", plugs)
	images := map[string]any{}
	for k, v := range in.Images {
		images[k] = map[string]any{"ref": v.Ref, "digest": v.Digest}
	}
	add("images", images)
	var b bytes.Buffer
	b.WriteString("# Solution lock " + sol.Name + " " + NormalizeLockVersion(in.Version) + ". Made by care-pi solution lock. Do not edit.\n")
	enc := yaml.NewEncoder(&b)
	enc.SetIndent(2)
	if err := enc.Encode(&doc); err != nil {
		return nil, err
	}
	_ = enc.Close()
	if _, err := ParseLock(b.Bytes()); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

func isSHA40(s string) bool {
	if len(s) != 40 {
		return false
	}
	for _, c := range s {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

func firstOr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// IndexRelease is one released lock for AssembleIndex.
type IndexRelease struct {
	Data         []byte
	URL          string
	ReleasedAt   string
	ChangelogURL string
}

// IndexInput holds the inputs of index.json.
type IndexInput struct {
	DistBase    string
	GeneratedAt string
	Agent       *IndexAgent
	Releases    []IndexRelease
	// Withdrawn maps "<solution>/<version>" to the reason (solutions/<name>/locks/withdrawn.yaml).
	Withdrawn map[string]string
	// Titles holds title and description by solution (from the newest lock without them).
	HeldPackages map[string]map[string]string
}

// AssembleIndex makes index.json (docs/solutions.md, section 4) and checks it with index.schema.json.
func AssembleIndex(in IndexInput) ([]byte, error) {
	byName := map[string]*IndexSolution{}
	var names []string
	for _, r := range in.Releases {
		l, err := ParseLock(r.Data)
		if err != nil {
			return nil, err
		}
		s, ok := byName[l.Name]
		if !ok {
			s = &IndexSolution{Name: l.Name, Title: l.Solution.Title, Description: l.Solution.Description, Locks: []IndexLock{}}
			byName[l.Name] = s
			names = append(names, l.Name)
		}
		url := r.URL
		if url == "" {
			v := NormalizeLockVersion(l.Version)
			url = "lock-" + l.Name + "-" + v + "/" + l.Name + "-" + v + ".lock.yaml"
		}
		rel := r.ReleasedAt
		if rel == "" {
			rel = l.CreatedAt
		}
		il := IndexLock{Version: NormalizeLockVersion(l.Version), URL: url, SHA256: l.SHA256, ReleasedAt: rel,
			MinAgentVersion: l.MinAgentVersion, Summary: summaryOf(l)}
		if r.ChangelogURL != "" {
			c := r.ChangelogURL
			il.ChangelogURL = &c
		}
		if reason, ok := in.Withdrawn[l.Name+"/"+il.Version]; ok {
			il.Withdrawn = true
			rr := reason
			il.WithdrawnReason = &rr
		}
		s.Locks = append(s.Locks, il)
	}
	sort.Strings(names)
	ix := Index{APIVersion: "care-pi/v1", Kind: "Index", GeneratedAt: in.GeneratedAt, DistBase: in.DistBase, Agent: in.Agent,
		Solutions: []IndexSolution{}}
	if ix.GeneratedAt == "" {
		ix.GeneratedAt = time.Now().UTC().Format(time.RFC3339)
	}
	for _, n := range names {
		s := byName[n]
		sort.SliceStable(s.Locks, func(i, j int) bool { return CompareLockVersions(s.Locks[i].Version, s.Locks[j].Version) > 0 })
		if nl := s.Newest(); nl != nil {
			v := nl.Version
			s.Latest = &v
		}
		ix.Solutions = append(ix.Solutions, *s)
	}
	if len(in.HeldPackages) > 0 {
		ix.OS = &IndexOS{HeldPackages: in.HeldPackages}
	}
	data, err := json.MarshalIndent(ix, "", "  ")
	if err != nil {
		return nil, err
	}
	data = append(data, '\n')
	if _, err := ParseIndex(data); err != nil {
		return nil, err
	}
	return data, nil
}

func summaryOf(l *Lock) string {
	s := strings.TrimSpace(l.Notes)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if s == "" {
		s = l.Name + " " + l.Version
	}
	if len(s) > 300 {
		s = s[:300]
	}
	return s
}
