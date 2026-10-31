package solution

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/rithviknishad/care-pi/agent/internal/paths"
	"github.com/rithviknishad/care-pi/solutions"
)

// EmbeddedNames returns the names of the solutions that the agent embeds.
func EmbeddedNames() []string {
	ents, err := fs.ReadDir(solutions.FS, ".")
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range ents {
		if e.IsDir() && e.Name() != "schema" {
			if _, err := fs.Stat(solutions.FS, e.Name()+"/solution.yaml"); err == nil {
				out = append(out, e.Name())
			}
		}
	}
	sort.Strings(out)
	return out
}

// EmbeddedLockVersions returns the versions of the locks that the agent embeds for the solution, newest first.
func EmbeddedLockVersions(name string) []string {
	ents, err := fs.ReadDir(solutions.FS, name+"/locks")
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range ents {
		if v, ok := strings.CutSuffix(e.Name(), ".lock.yaml"); ok && !e.IsDir() {
			if _, ok := ParseLockVersion(v); ok {
				out = append(out, v)
			}
		}
	}
	sortVersions(out)
	return out
}

func sortVersions(v []string) {
	sort.SliceStable(v, func(a, b int) bool { return CompareLockVersions(v[a], v[b]) > 0 })
}

// LockStore finds solution locks in the cache (state/update-cache/locks) and in the agent.
type LockStore struct {
	Paths paths.Paths
	// Dir is a directory of solutions (solutions/<name>/locks) for tests and the CLI. Empty uses the embedded locks.
	Dir string
}

// ErrLockNotFound tells that no lock has the version.
var ErrLockNotFound = errors.New("the solution lock does not exist")

// CachePath returns the file of a cached lock.
func (s LockStore) CachePath(name, version string) string {
	return s.Paths.Abs(paths.UpdateLockCache + "/" + name + "-" + NormalizeLockVersion(version) + ".lock.yaml")
}

// Get returns the lock and where it came from: "cache" or "embedded".
func (s LockStore) Get(name, version string) (*Lock, string, error) {
	if data, err := os.ReadFile(s.CachePath(name, version)); err == nil {
		l, err := ParseLock(data)
		if err != nil {
			return nil, "cache", err
		}
		return l, "cache", nil
	}
	for _, v := range s.embedded(name) {
		if CompareLockVersions(v, version) != 0 {
			continue
		}
		data, err := s.readEmbedded(name, v)
		if err != nil {
			return nil, "embedded", err
		}
		l, err := ParseLock(data)
		return l, "embedded", err
	}
	return nil, "", fmt.Errorf("%w: %s %s", ErrLockNotFound, name, version)
}

func (s LockStore) embedded(name string) []string {
	if s.Dir == "" {
		return EmbeddedLockVersions(name)
	}
	ents, err := os.ReadDir(filepath.Join(s.Dir, name, "locks"))
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range ents {
		if v, ok := strings.CutSuffix(e.Name(), ".lock.yaml"); ok {
			if _, ok := ParseLockVersion(v); ok {
				out = append(out, v)
			}
		}
	}
	sortVersions(out)
	return out
}

func (s LockStore) readEmbedded(name, v string) ([]byte, error) {
	if s.Dir == "" {
		return fs.ReadFile(solutions.FS, path.Join(name, "locks", v+".lock.yaml"))
	}
	return os.ReadFile(filepath.Join(s.Dir, name, "locks", v+".lock.yaml"))
}

// Embedded returns the versions that the agent embeds (or that Dir has), newest first.
func (s LockStore) Embedded(name string) []string { return s.embedded(name) }

// Cached returns the versions in the cache, newest first.
func (s LockStore) Cached(name string) []string {
	ents, err := os.ReadDir(s.Paths.Abs(paths.UpdateLockCache))
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range ents {
		rest, ok := strings.CutPrefix(e.Name(), name+"-")
		if !ok {
			continue
		}
		if v, ok := strings.CutSuffix(rest, ".lock.yaml"); ok {
			if _, ok := ParseLockVersion(v); ok {
				out = append(out, v)
			}
		}
	}
	sortVersions(out)
	return out
}

// Versions returns the versions of the cache and of the agent, without duplicates, newest first.
func (s LockStore) Versions(name string) []string {
	seen := map[string]bool{}
	var out []string
	for _, v := range append(s.Cached(name), s.embedded(name)...) {
		n := NormalizeLockVersion(v)
		if !seen[n] {
			seen[n] = true
			out = append(out, n)
		}
	}
	sortVersions(out)
	return out
}

// Put writes the lock file into the cache.
func (s LockStore) Put(l *Lock) error {
	p := s.CachePath(l.Name, l.Version)
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, l.Raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

// DefaultDistBase is the dist base of the agent release: the GitHub release assets of care-pi
// (docs/solutions.md, section 2.2). A move to GitHub Pages changes only this value and the index URL.
const DefaultDistBase = "https://github.com/rithviknishad/care-pi/releases/download/"

// DefaultIndexURL is the URL of index.json when updates.index_url is empty.
const DefaultIndexURL = DefaultDistBase + "index/index.json"

// IndexCacheFile is the cached index.json.
const IndexCacheFile = paths.UpdateCache + "/index.json"

// CachedIndex reads the cached index.json. It returns nil and no error when no cache exists.
func CachedIndex(p paths.Paths) (*Index, error) {
	data, err := os.ReadFile(p.Abs(IndexCacheFile))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	return ParseIndex(data)
}

// DistBase returns dist_base of the cached index, else DefaultDistBase.
func DistBase(p paths.Paths) string {
	if ix, err := CachedIndex(p); err == nil && ix != nil && ix.DistBase != "" {
		return ix.DistBase
	}
	return DefaultDistBase
}
