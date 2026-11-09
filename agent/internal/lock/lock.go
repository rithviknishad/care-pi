// Package lock reads, writes, and resolves /etc/care-pi/lock.yaml.
//
// The lock records the immutable identity of each component: image digests,
// git shas, and the FE build id. apply renders units from the lock, so a host
// without network access keeps working with the last resolved lock.
//
// Phase 4 writes version 2 (docs/solutions.md, section 6). Read accepts version 1 and
// migrates it in memory (section 6.3).
package lock

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"

	"github.com/rithviknishad/care-pi/agent/internal/host"
	"github.com/rithviknishad/care-pi/agent/internal/paths"
)

// FileVersion is the version that Write writes.
const FileVersion = 2

// KeepHistory is the number of files in state/locks that Write keeps (and each file that the
// current lock references).
const KeepHistory = 20

// Lock is the content of lock.yaml.
//
// Git and Plugs are the version 1 members. Version 2 keeps them in components; Read fills
// Git and Plugs from components, so the resolver and the render use one form.
type Lock struct {
	Version      int
	GeneratedAt  string
	AgentVersion string
	Solution     Solution
	Components   Components
	Images       map[string]Image
	// Git maps a component (care_fe) to its resolved ref.
	Git map[string]GitRef
	// Plugs maps a backend plug that is on to its resolved ref.
	Plugs   map[string]GitRef
	FE      FE
	History History
	// FileVersion is the version of the file that Read read (1 or 2). 0 for a new lock.
	FileVersion int
}

// Solution records the solution that produced the lock.
type Solution struct {
	Name string `yaml:"name"`
	// Version is the solution lock that the device installed. Empty without one.
	Version string `yaml:"version"`
	Track   string `yaml:"track"`
	// Source is lock, solution-refs, or custom.
	Source string `yaml:"source,omitempty"`
	// LockSHA256 is the SHA-256 of the solution lock file. Empty without one.
	LockSHA256 string `yaml:"lock_sha256"`
}

// Sources of a lock (solution.source) and of a component (source).
const (
	SourceLock         = "lock"
	SourceSolutionRefs = "solution-refs"
	SourceCustom       = "custom"
	SourceOperator     = "operator"
	SourceSolution     = "solution"
)

// Components holds the identity of each component (version 2).
type Components struct {
	Care   *CareComponent           `yaml:"care,omitempty"`
	CareFE *CareFEComponent         `yaml:"care_fe,omitempty"`
	Plugs  map[string]PlugComponent `yaml:"plugs,omitempty"`
}

// CareComponent is the upstream care image.
type CareComponent struct {
	Image   string      `yaml:"image"`
	Ref     string      `yaml:"ref"`
	Digest  string      `yaml:"digest"`
	GitSHA  string      `yaml:"git_sha,omitempty"`
	Tracked bool        `yaml:"tracked"`
	Source  string      `yaml:"source"`
	Build   *LocalBuild `yaml:"build,omitempty"`
}

// LocalBuild describes a local image build (docs/solutions.md, section 13.1).
type LocalBuild struct {
	ID                  string   `yaml:"id"`
	BaseDigest          string   `yaml:"base_digest"`
	InstalledPlugs      []string `yaml:"installed_plugs"`
	ContainerfileSHA256 string   `yaml:"containerfile_sha256"`
}

// CareFEComponent is the care_fe identity.
type CareFEComponent struct {
	Repo    string `yaml:"repo"`
	Ref     string `yaml:"ref"`
	SHA     string `yaml:"sha"`
	Tracked bool   `yaml:"tracked"`
	Source  string `yaml:"source"`
}

// PlugComponent is one plug of the effective plug set, on or off.
type PlugComponent struct {
	Enabled  bool           `yaml:"enabled"`
	Reason   string         `yaml:"reason"`
	Backend  *PlugBackend   `yaml:"backend,omitempty"`
	Frontend *PlugFrontend  `yaml:"frontend,omitempty"`
	Settings map[string]any `yaml:"settings,omitempty"`
}

// PlugBackend is the backend part of a plug.
type PlugBackend struct {
	Name      string `yaml:"name"`
	Repo      string `yaml:"repo,omitempty"`
	Ref       string `yaml:"ref"`
	SHA       string `yaml:"sha"`
	Tracked   bool   `yaml:"tracked"`
	Installed bool   `yaml:"installed"`
	// Subdirectory is the directory of the package in Repo (wave 13): the pip version is @<sha>#subdirectory=<dir>.
	Subdirectory string `yaml:"subdirectory,omitempty"`
}

// PlugFrontend is the frontend part of a plug.
type PlugFrontend struct {
	Slug    string `yaml:"slug"`
	Source  string `yaml:"source"`
	Repo    string `yaml:"repo,omitempty"`
	Ref     string `yaml:"ref,omitempty"`
	SHA     string `yaml:"sha,omitempty"`
	Tracked bool   `yaml:"tracked"`
	URL     string `yaml:"url"`
	SHA256  string `yaml:"sha256,omitempty"`
	Entry   string `yaml:"entry,omitempty"`
	MetaURL string `yaml:"meta_url"`
	// Path is the directory of the plug in Repo (wave 13).
	Path string `yaml:"path,omitempty"`
}

// History points at the previous lock.
type History struct {
	Dir string `yaml:"dir"`
	// Previous is the file in Dir of the lock before this one. Empty for the first lock.
	Previous string `yaml:"previous"`
}

// PreviousID returns the lock id of Previous (the file name without .yaml), or "".
func (h History) PreviousID() string { return strings.TrimSuffix(h.Previous, ".yaml") }

// Image is one resolved image.
type Image struct {
	// Ref is the reference that the config asks for, for example "ghcr.io/ohcnetwork/care:latest".
	Ref string `yaml:"ref"`
	// Digest is the manifest digest. It is empty for a local image (localhost/...).
	Digest string `yaml:"digest,omitempty"`
	// Pinned is true when Ref already names a digest.
	Pinned bool `yaml:"pinned,omitempty"`
	// Local is true for an image that only exists in the local storage.
	Local bool `yaml:"local,omitempty"`
}

// Pull returns the reference that units use: <repo>@<digest>, or Ref for a local image.
func (i Image) Pull() string {
	if i.Digest == "" {
		return i.Ref
	}
	repo, _, _ := SplitImage(i.Ref)
	return repo + "@" + i.Digest
}

// GitRef is one resolved git ref.
type GitRef struct {
	Repo   string `yaml:"repo"`
	Ref    string `yaml:"ref"`
	SHA    string `yaml:"sha"`
	Pinned bool   `yaml:"pinned,omitempty"`
}

// FE records the frontend build.
type FE struct {
	Provider string `yaml:"provider"`
	BuildID  string `yaml:"build_id,omitempty"`
	// CareFESHA is the care_fe sha of the build.
	CareFESHA string `yaml:"care_fe_sha,omitempty"`
	// URL and SHA256 identify the bundle that the artifact provider downloaded.
	URL    string `yaml:"url,omitempty"`
	SHA256 string `yaml:"sha256,omitempty"`
	// BuiltAt (RFC 3339, UTC) and Duration describe the last build of the local provider.
	BuiltAt  string `yaml:"built_at,omitempty"`
	Duration string `yaml:"duration,omitempty"`
}

// Wants lists what the config asks for.
type Wants struct {
	Solution Solution
	// Images maps a component name (care, postgres, caddy, redis, s3, node) to a reference.
	Images map[string]string
	// Git maps a component name (care_fe) to a repository and ref.
	Git map[string]GitWant
	// Plugs maps a backend plug id to its repository and ref.
	Plugs map[string]GitWant
}

// GitWant is one git component to resolve.
type GitWant struct {
	Repo string
	Ref  string
}

// fileV2 is the order and the form of a version 2 file.
type fileV2 struct {
	Version      int              `yaml:"version"`
	GeneratedAt  string           `yaml:"generated_at"`
	AgentVersion string           `yaml:"agent_version"`
	Solution     solutionV2       `yaml:"solution"`
	Components   Components       `yaml:"components"`
	Images       map[string]Image `yaml:"images"`
	FE           FE               `yaml:"fe"`
	History      historyV2        `yaml:"history"`
}

type solutionV2 struct {
	Name       string  `yaml:"name"`
	Version    *string `yaml:"version"`
	Track      string  `yaml:"track"`
	Source     string  `yaml:"source"`
	LockSHA256 *string `yaml:"lock_sha256"`
}

type historyV2 struct {
	Dir      string  `yaml:"dir"`
	Previous *string `yaml:"previous"`
}

// fileAny reads version 1 and version 2.
type fileAny struct {
	Version      int               `yaml:"version"`
	GeneratedAt  string            `yaml:"generated_at"`
	AgentVersion string            `yaml:"agent_version"`
	Solution     Solution          `yaml:"solution"`
	Components   *Components       `yaml:"components"`
	Images       map[string]Image  `yaml:"images"`
	Git          map[string]GitRef `yaml:"git"`
	Plugs        map[string]GitRef `yaml:"plugs"`
	FE           FE                `yaml:"fe"`
	History      History           `yaml:"history"`
}

func nilIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// MarshalYAML writes version 2.
func (l Lock) MarshalYAML() (any, error) {
	src := l.Solution.Source
	if src == "" {
		src = SourceSolutionRefs
		if l.Solution.Track == "custom" {
			src = SourceCustom
		}
	}
	dir := l.History.Dir
	if dir == "" {
		dir = paths.LockHistory
	}
	return fileV2{
		Version: FileVersion, GeneratedAt: l.GeneratedAt, AgentVersion: l.AgentVersion,
		Solution: solutionV2{Name: l.Solution.Name, Version: nilIfEmpty(l.Solution.Version), Track: l.Solution.Track,
			Source: src, LockSHA256: nilIfEmpty(l.Solution.LockSHA256)},
		Components: l.completeComponents(),
		Images:     l.Images,
		FE:         l.FE,
		History:    historyV2{Dir: dir, Previous: nilIfEmpty(l.History.Previous)},
	}, nil
}

// UnmarshalYAML reads version 1 or 2. A version 1 file is migrated (docs/solutions.md, section 6.3).
func (l *Lock) UnmarshalYAML(n *yaml.Node) error {
	var f fileAny
	if err := n.Decode(&f); err != nil {
		return err
	}
	*l = Lock{Version: FileVersion, GeneratedAt: f.GeneratedAt, AgentVersion: f.AgentVersion, Solution: f.Solution,
		Images: f.Images, Git: f.Git, Plugs: f.Plugs, FE: f.FE, History: f.History, FileVersion: f.Version}
	if l.FileVersion == 0 {
		l.FileVersion = 1
	}
	if f.Components != nil && f.Version >= 2 {
		l.Components = *f.Components
		l.fillFromComponents()
	} else {
		l.migrateV1()
	}
	return nil
}

// migrateV1 fills the version 2 members of a version 1 lock.
func (l *Lock) migrateV1() {
	if l.Solution.Source == "" {
		l.Solution.Source = SourceSolutionRefs
		if l.Solution.Track == "custom" {
			l.Solution.Source = SourceCustom
		}
	}
	l.Components = l.completeComponents()
	for id, p := range l.Components.Plugs {
		p.Enabled, p.Reason = true, SourceSolution
		l.Components.Plugs[id] = p
	}
}

// fillFromComponents fills Git and Plugs from the components of a version 2 lock.
func (l *Lock) fillFromComponents() {
	if l.Git == nil {
		l.Git = map[string]GitRef{}
	}
	if c := l.Components.CareFE; c != nil && c.Repo != "" {
		l.Git["care_fe"] = GitRef{Repo: c.Repo, Ref: c.Ref, SHA: c.SHA, Pinned: !c.Tracked}
	}
	for id, p := range l.Components.Plugs {
		if p.Enabled && p.Backend != nil && p.Backend.SHA != "" {
			if l.Plugs == nil {
				l.Plugs = map[string]GitRef{}
			}
			l.Plugs[id] = GitRef{Repo: p.Backend.Repo, Ref: p.Backend.Ref, SHA: p.Backend.SHA, Pinned: !p.Backend.Tracked}
		}
	}
	if len(l.Git) == 0 {
		l.Git = nil
	}
}

// completeComponents returns the components with the identities of Images, Git, and Plugs.
func (l Lock) completeComponents() Components {
	c := l.Components
	src := SourceSolution
	if l.Solution.Source == SourceLock {
		src = SourceLock
	}
	if img, ok := l.Images["care"]; ok && (c.Care == nil || (c.Care.Digest == "" && c.Care.Build == nil)) {
		repo, tag, digest := SplitImage(img.Ref)
		ref := tag
		if ref == "" {
			ref = digest
		}
		old := CareComponent{}
		if c.Care != nil {
			old = *c.Care
		}
		c.Care = &CareComponent{Image: repo, Ref: ref, Digest: img.Digest, GitSHA: old.GitSHA, Tracked: !img.Pinned && !img.Local,
			Source: firstNonEmpty(old.Source, src)}
	}
	if g, ok := l.Git["care_fe"]; ok && (c.CareFE == nil || c.CareFE.SHA == "") {
		s := src
		if c.CareFE != nil && c.CareFE.Source != "" {
			s = c.CareFE.Source
		}
		c.CareFE = &CareFEComponent{Repo: g.Repo, Ref: g.Ref, SHA: g.SHA, Tracked: !g.Pinned, Source: s}
	}
	if len(l.Plugs) > 0 || len(c.Plugs) > 0 {
		out := map[string]PlugComponent{}
		for id, p := range c.Plugs {
			out[id] = p
		}
		for id, g := range l.Plugs {
			p, ok := out[id]
			if !ok {
				p = PlugComponent{Enabled: true, Reason: SourceSolution}
			}
			b := PlugBackend{Name: id, Installed: true}
			if p.Backend != nil {
				b = *p.Backend
			}
			b.Repo, b.Ref, b.SHA, b.Tracked = g.Repo, g.Ref, g.SHA, !g.Pinned
			p.Backend = &b
			out[id] = p
		}
		c.Plugs = out
	}
	return c
}

func firstNonEmpty(vs ...string) string {
	for _, v := range vs {
		if v != "" {
			return v
		}
	}
	return ""
}

// SplitImage splits an image reference into repository, tag, and digest.
// A reference without tag and digest gets the tag "latest".
func SplitImage(ref string) (repo, tag, digest string) {
	rest := ref
	if i := strings.Index(rest, "@"); i >= 0 {
		digest = rest[i+1:]
		rest = rest[:i]
	}
	slash := strings.LastIndex(rest, "/")
	if i := strings.LastIndex(rest, ":"); i > slash {
		tag = rest[i+1:]
		rest = rest[:i]
	}
	if tag == "" && digest == "" {
		tag = "latest"
	}
	return rest, tag, digest
}

// IsLocalImage reports whether ref names an image in the local storage only.
func IsLocalImage(ref string) bool {
	return strings.HasPrefix(ref, "localhost/")
}

// Read reads the lock file. It returns nil and no error when the file does not exist.
func Read(path string) (*Lock, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("cannot read the lock: %w", err)
	}
	l, err := Parse(data)
	if err != nil {
		return nil, fmt.Errorf("cannot decode the lock %s: %w", path, err)
	}
	return l, nil
}

// Parse decodes a lock file of version 1 or 2.
func Parse(data []byte) (*Lock, error) {
	var l Lock
	if err := yaml.Unmarshal(data, &l); err != nil {
		return nil, err
	}
	return &l, nil
}

// Load reads /etc/care-pi/lock.yaml below the root. For a version 1 file, history.previous is the
// newest file in state/locks.
func Load(p paths.Paths) (*Lock, error) {
	l, err := Read(p.Abs(paths.LockFile))
	if err != nil || l == nil {
		return l, err
	}
	if l.FileVersion < 2 {
		l.History.Previous = newestHistory(p.Abs(paths.LockHistory))
	}
	return l, nil
}

// ReadHistory reads the lock with the id (the file name in state/locks without .yaml).
func ReadHistory(p paths.Paths, id string) (*Lock, error) {
	if !historyIDOK(id) {
		return nil, fmt.Errorf("%q is not a lock id", id)
	}
	l, err := Read(p.Abs(paths.LockHistory + "/" + id + ".yaml"))
	if err == nil && l == nil {
		return nil, fs.ErrNotExist
	}
	return l, err
}

func historyIDOK(id string) bool {
	_, err := time.Parse("20060102T150405Z", id)
	return err == nil
}

// HistoryIDs returns the lock ids in state/locks, newest first.
func HistoryIDs(p paths.Paths) []string {
	ents, err := os.ReadDir(p.Abs(paths.LockHistory))
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range ents {
		if id, ok := strings.CutSuffix(e.Name(), ".yaml"); ok && historyIDOK(id) {
			out = append(out, id)
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(out)))
	return out
}

func newestHistory(dir string) string {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	newest := ""
	for _, e := range ents {
		if id, ok := strings.CutSuffix(e.Name(), ".yaml"); ok && historyIDOK(id) && e.Name() > newest {
			newest = e.Name()
		}
	}
	return newest
}

// Marshal encodes the lock (version 2) with a header comment.
func (l *Lock) Marshal() ([]byte, error) {
	var b bytes.Buffer
	b.WriteString("# Generated by care-pi. Do not edit. Resolved digests, shas, and build ids.\n")
	enc := yaml.NewEncoder(&b)
	enc.SetIndent(2)
	if err := enc.Encode(l); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

// Equal reports whether two locks pin the same identities. GeneratedAt, AgentVersion, and the
// history do not count. A version 1 file is not equal to a version 2 lock, so apply writes version 2.
func Equal(a, b *Lock) bool {
	if a == nil || b == nil {
		return a == b
	}
	if (a.FileVersion == 1) != (b.FileVersion == 1) {
		return false
	}
	return sameIdentity(a, b, false)
}

// sameIdentity compares the identities. ignoreFE also ignores the FE record.
func sameIdentity(a, b *Lock, ignoreFE bool) bool {
	x, y := *a, *b
	x.GeneratedAt, y.GeneratedAt = "", ""
	x.AgentVersion, y.AgentVersion = "", ""
	x.History, y.History = History{}, History{}
	if ignoreFE {
		x.FE, y.FE = FE{}, FE{}
	}
	bx, err1 := x.Marshal()
	by, err2 := y.Marshal()
	return err1 == nil && err2 == nil && bytes.Equal(bx, by)
}

// ImagesChanged returns the names of the images whose pull reference differs between a and b.
func ImagesChanged(a, b *Lock) []string {
	var out []string
	if a == nil || b == nil {
		return out
	}
	names := map[string]bool{}
	for k := range a.Images {
		names[k] = true
	}
	for k := range b.Images {
		names[k] = true
	}
	for k := range names {
		if a.Images[k].Pull() != b.Images[k].Pull() {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

// BackendPlugsOn returns the ids of the backend plugs that are on, sorted.
func (l *Lock) BackendPlugsOn() []string {
	if l == nil {
		return nil
	}
	var out []string
	for id := range l.Plugs {
		out = append(out, id)
	}
	for id, p := range l.Components.Plugs {
		if p.Enabled && p.Backend != nil {
			if _, ok := l.Plugs[id]; !ok {
				out = append(out, id)
			}
		}
	}
	sort.Strings(out)
	return out
}

// Write writes the lock atomically. When a different lock exists, Write first copies it
// to <state>/locks/<timestamp>.yaml, and history.previous points at the copy. A change of only
// the FE record keeps history.previous.
func Write(p paths.Paths, l *Lock, now time.Time) error {
	path := p.Abs(paths.LockFile)
	prev, err := os.ReadFile(path)
	if err == nil {
		hist := p.Abs(paths.LockHistory)
		if err := os.MkdirAll(hist, 0o700); err != nil {
			return fmt.Errorf("cannot create %s: %w", paths.LockHistory, err)
		}
		name := now.UTC().Format("20060102T150405Z") + ".yaml"
		if err := os.WriteFile(filepath.Join(hist, name), prev, 0o600); err != nil {
			return fmt.Errorf("cannot keep the previous lock: %w", err)
		}
		old, perr := Parse(prev)
		switch {
		case perr == nil && sameIdentity(old, l, true):
			if old.FileVersion < 2 && old.History.Previous == "" {
				old.History.Previous = newestHistoryExcept(hist, name)
			}
			l.History.Previous = old.History.Previous
		default:
			l.History.Previous = name
		}
		pruneHistory(hist, l.History.Previous)
	}
	l.History.Dir = paths.LockHistory
	data, err := l.Marshal()
	if err != nil {
		return err
	}
	if err := WriteFileAtomic(path, data, 0o600); err != nil {
		return err
	}
	l.FileVersion = FileVersion
	return nil
}

func newestHistoryExcept(dir, except string) string {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	newest := ""
	for _, e := range ents {
		if e.Name() != except && strings.HasSuffix(e.Name(), ".yaml") && historyIDOK(strings.TrimSuffix(e.Name(), ".yaml")) && e.Name() > newest {
			newest = e.Name()
		}
	}
	return newest
}

// pruneHistory keeps the KeepHistory newest files and keep.
func pruneHistory(dir, keep string) {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	var names []string
	for _, e := range ents {
		if strings.HasSuffix(e.Name(), ".yaml") && historyIDOK(strings.TrimSuffix(e.Name(), ".yaml")) {
			names = append(names, e.Name())
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(names)))
	for i, n := range names {
		if i >= KeepHistory && n != keep {
			_ = os.Remove(filepath.Join(dir, n))
		}
	}
}

// WriteFileAtomic writes data to a temporary file in the same directory and renames it.
func WriteFileAtomic(path string, data []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-"+filepath.Base(path)+"-")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// Resolver turns wants into a lock.
type Resolver struct {
	Skopeo host.Skopeo
	Git    host.Git
	OS     string
	Arch   string
	// Offline forbids network access. Missing entries then fall back to the plain reference.
	Offline bool
	// Warn receives a message for each entry that Offline could not resolve.
	Warn func(string)
	// Progress receives one line for each network resolution, for example
	// "resolve docker.io/library/redis:8-alpine … 2f7a1c9e3b4d". Nil prints nothing.
	Progress io.Writer
}

// Resolve builds a lock for wants. It reuses each entry of prev that has the same reference,
// unless update is true and the entry is tracked (not pinned).
func (r Resolver) Resolve(ctx context.Context, w Wants, prev *Lock, update bool) (*Lock, error) {
	out := &Lock{
		Version:  FileVersion,
		Solution: w.Solution,
		Images:   map[string]Image{},
		Git:      map[string]GitRef{},
		Plugs:    map[string]GitRef{},
	}
	var errs []error
	for _, name := range sortedKeys(w.Images) {
		ref := w.Images[name]
		img, err := r.image(ctx, name, ref, prev, update)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		out.Images[name] = img
	}
	for _, name := range sortedGitKeys(w.Git) {
		var old *GitRef
		if prev != nil {
			if g, ok := prev.Git[name]; ok {
				old = &g
			}
		}
		g, err := r.git(ctx, name, w.Git[name], old, update)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		out.Git[name] = g
	}
	for _, id := range sortedGitKeys(w.Plugs) {
		var old *GitRef
		if prev != nil {
			if g, ok := prev.Plugs[id]; ok {
				old = &g
			}
		}
		g, err := r.git(ctx, "plug "+id, w.Plugs[id], old, update)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		out.Plugs[id] = g
	}
	if len(out.Plugs) == 0 {
		out.Plugs = nil
	}
	if err := errors.Join(errs...); err != nil {
		return nil, err
	}
	return out, nil
}

func (r Resolver) warn(msg string) {
	if r.Warn != nil {
		r.Warn(msg)
	}
}

// begin prints the start of a progress line before a network call.
// The caller ends the line with end.
func (r Resolver) begin(what string) {
	if r.Progress != nil {
		_, _ = fmt.Fprintf(r.Progress, "resolve %s … ", what)
	}
}

// end completes the progress line with the first 12 characters of the result, or "failed".
func (r Resolver) end(id string, err error) {
	if r.Progress == nil {
		return
	}
	if err != nil {
		_, _ = fmt.Fprintln(r.Progress, "failed")
		return
	}
	_, hex, ok := strings.Cut(id, ":")
	if !ok {
		hex = id
	}
	if len(hex) > 12 {
		hex = hex[:12]
	}
	_, _ = fmt.Fprintln(r.Progress, hex)
}

func (r Resolver) image(ctx context.Context, name, ref string, prev *Lock, update bool) (Image, error) {
	repo, _, digest := SplitImage(ref)
	if digest != "" {
		return Image{Ref: ref, Digest: digest, Pinned: true}, nil
	}
	if IsLocalImage(ref) {
		return Image{Ref: ref, Local: true}, nil
	}
	if prev != nil {
		if old, ok := prev.Images[name]; ok && old.Ref == ref && old.Digest != "" && !update {
			return old, nil
		}
	}
	if r.Offline {
		r.warn(fmt.Sprintf("the lock has no digest for %s (%s); the rendered unit uses the tag", name, ref))
		return Image{Ref: ref}, nil
	}
	r.begin(ref)
	d, err := r.Skopeo.Digest(ctx, ref, r.OS, r.Arch)
	r.end(d, err)
	if err != nil {
		return Image{}, fmt.Errorf("cannot resolve image %s (%s): %w", ref, repo, err)
	}
	return Image{Ref: ref, Digest: d}, nil
}

func (r Resolver) git(ctx context.Context, name string, w GitWant, old *GitRef, update bool) (GitRef, error) {
	if host.IsFullSHA(w.Ref) {
		return GitRef{Repo: w.Repo, Ref: w.Ref, SHA: w.Ref, Pinned: true}, nil
	}
	if old != nil && old.Repo == w.Repo && old.Ref == w.Ref && old.SHA != "" && !update {
		return *old, nil
	}
	if r.Offline {
		r.warn(fmt.Sprintf("the lock has no sha for %s (%s@%s)", name, w.Repo, w.Ref))
		return GitRef{Repo: w.Repo, Ref: w.Ref}, nil
	}
	r.begin(name + " " + w.Ref)
	refs, err := r.Git.LsRemote(ctx, w.Repo)
	if err != nil {
		r.end("", err)
		return GitRef{}, fmt.Errorf("cannot resolve %s ref %q in %s: %w", name, w.Ref, w.Repo, err)
	}
	sha, ok := host.ResolveRef(refs, w.Ref)
	if !ok {
		err := fmt.Errorf("cannot resolve %s: %s has no branch or tag %q", name, w.Repo, w.Ref)
		r.end("", err)
		return GitRef{}, err
	}
	r.end(sha, nil)
	return GitRef{Repo: w.Repo, Ref: w.Ref, SHA: sha}, nil
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func sortedGitKeys(m map[string]GitWant) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
