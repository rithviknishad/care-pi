package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/rithviknishad/care-pi/agent/internal/buildinfo"
	"github.com/rithviknishad/care-pi/agent/internal/lock"
	"github.com/rithviknishad/care-pi/agent/internal/minisign"
	"github.com/rithviknishad/care-pi/agent/internal/paths"
	"github.com/rithviknishad/care-pi/agent/internal/problem"
	"github.com/rithviknishad/care-pi/agent/internal/solution"
)

// Limits of the index client (docs/solutions.md, section 4.3).
const (
	IndexTimeout  = 30 * time.Second
	MaxIndexBytes = 1 << 20
	MaxLockBytes  = 1 << 20
)

// indexMeta is state/update-cache/index.meta.json.
type indexMeta struct {
	URL          string    `json:"url"`
	ETag         string    `json:"etag,omitempty"`
	LastModified string    `json:"last_modified,omitempty"`
	FetchedAt    time.Time `json:"fetched_at"`
	// SignedBy is the key id of the signature that the agent checked (updates.require_signed), and SignedSHA256 the
	// SHA-256 of the index file that it covers. Empty: the cache had no check.
	SignedBy     string `json:"signed_by,omitempty"`
	SignedSHA256 string `json:"signed_sha256,omitempty"`
}

// signedMeta returns the meta of a cached index that the key keyID verified ("" without a check).
func signedMeta(m indexMeta, keyID string, data []byte) indexMeta {
	if keyID != "" {
		sum := sha256.Sum256(data)
		m.SignedBy, m.SignedSHA256 = keyID, hex.EncodeToString(sum[:])
	}
	return m
}

// errUnsignedCache tells that the cached index had no signature check of a trusted key.
var errUnsignedCache = errors.New("the cached index is unsigned")

// TrustedCache returns the cached index (nil without a cache). With RequireSigned, it returns the cache only when a
// check of a trusted key accepted it (index.meta.json: the key id and the SHA-256 of the file); else errUnsignedCache.
// A cache from before updates.require_signed has no such record.
func (c Client) TrustedCache() (*solution.Index, error) {
	ix, err := solution.CachedIndex(c.Paths)
	if err != nil || ix == nil || !c.RequireSigned {
		return ix, err
	}
	var meta indexMeta
	if data, err := os.ReadFile(c.Paths.Abs(indexMetaFile)); err == nil {
		_ = json.Unmarshal(data, &meta)
	}
	data, err := os.ReadFile(c.Paths.Abs(solution.IndexCacheFile))
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(data)
	if meta.SignedBy == "" || meta.SignedSHA256 != hex.EncodeToString(sum[:]) || !slices.Contains(c.Keys.IDs(), meta.SignedBy) {
		return nil, errUnsignedCache
	}
	return ix, nil
}

const indexMetaFile = paths.UpdateCache + "/index.meta.json"

// Client fetches index.json and the solution locks. It supports https://, http://, and file:// URLs.
type Client struct {
	Paths paths.Paths
	HTTP  *http.Client
	Now   func() time.Time
	// RequireSigned accepts index.json and a lock only with a valid signature of one of Keys (updates.require_signed,
	// Phase 7).
	RequireSigned bool
	Keys          minisign.Keys
}

func (c Client) http() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.RegisterProtocol("file", http.NewFileTransport(http.Dir("/")))
	return &http.Client{Transport: t, Timeout: IndexTimeout}
}

func (c Client) now() time.Time {
	if c.Now == nil {
		return time.Now().UTC()
	}
	return c.Now().UTC()
}

// IndexURL returns updates.index_url, or the default of the agent release.
func IndexURL(configured string) string {
	if configured != "" {
		return configured
	}
	return solution.DefaultIndexURL
}

func indexProblem(format string, a ...any) *problem.Problem {
	return problem.Newf(problem.IndexUnavailable, format, a...)
}

func (c Client) get(ctx context.Context, url string, header http.Header, limit int64) (*http.Response, []byte, error) {
	ctx, cancel := context.WithTimeout(ctx, IndexTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, nil, err
	}
	for k, v := range header {
		req.Header[k] = v
	}
	req.Header.Set("User-Agent", "care-pi/"+buildinfo.Version)
	resp, err := c.http().Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return resp, nil, err
	}
	if int64(len(data)) > limit {
		return resp, nil, fmt.Errorf("%s has more than %d bytes", url, limit)
	}
	return resp, data, nil
}

// FetchIndex downloads index.json with If-None-Match and If-Modified-Since of the cache, validates it,
// and writes the cache. A failure gives index-unavailable; the cache stays.
func (c Client) FetchIndex(ctx context.Context, url string) (*solution.Index, error) {
	var meta indexMeta
	if data, err := os.ReadFile(c.Paths.Abs(indexMetaFile)); err == nil {
		_ = json.Unmarshal(data, &meta)
	}
	h := http.Header{}
	cached, _ := solution.CachedIndex(c.Paths)
	// file:// has a resolution of 1 s for If-Modified-Since: a USB copy can change in the same second.
	// With signatures, each check downloads and verifies the index (the cache can be from before require_signed).
	if meta.URL == url && cached != nil && !strings.HasPrefix(url, "file://") && !c.RequireSigned {
		if meta.ETag != "" {
			h.Set("If-None-Match", meta.ETag)
		}
		if meta.LastModified != "" {
			h.Set("If-Modified-Since", meta.LastModified)
		}
	}
	resp, data, err := c.get(ctx, url, h, MaxIndexBytes)
	if err != nil {
		return nil, indexProblem("GET %s failed: %v", url, err)
	}
	if resp.StatusCode == http.StatusNotModified && cached != nil {
		meta.FetchedAt = c.now()
		_ = writeJSON(c.Paths.Abs(indexMetaFile), meta)
		return cached, nil
	}
	if resp.StatusCode != http.StatusOK {
		return nil, indexProblem("GET %s gave HTTP %d.", url, resp.StatusCode)
	}
	keyID, err := c.verifyRemote(ctx, url, data)
	if err != nil {
		return nil, err
	}
	ix, err := solution.ParseIndex(data)
	if err != nil {
		return nil, indexProblem("%s: %v", url, err)
	}
	if err := lock.WriteFileAtomic(c.Paths.Abs(solution.IndexCacheFile), data, 0o600); err != nil {
		return nil, err
	}
	meta = signedMeta(indexMeta{URL: url, ETag: resp.Header.Get("ETag"), LastModified: resp.Header.Get("Last-Modified"), FetchedAt: c.now()}, keyID, data)
	_ = writeJSON(c.Paths.Abs(indexMetaFile), meta)
	return ix, nil
}

// IndexFetchedAt returns the time of the last successful download of the index, or zero.
func IndexFetchedAt(p paths.Paths) (string, time.Time) {
	var meta indexMeta
	if data, err := os.ReadFile(p.Abs(indexMetaFile)); err == nil {
		_ = json.Unmarshal(data, &meta)
	}
	return meta.URL, meta.FetchedAt
}

func writeJSON(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return lock.WriteFileAtomic(path, data, 0o600)
}

// FetchLock downloads the lock of an index entry, checks its SHA-256, its schema, its name, and
// its version, and writes it into the cache (docs/solutions.md, section 3.3).
func (c Client) FetchLock(ctx context.Context, ix *solution.Index, name string, entry solution.IndexLock) (*solution.Lock, error) {
	url := solution.ResolveURL(ix.DistBase, entry.URL)
	resp, data, err := c.get(ctx, url, nil, MaxLockBytes)
	if err != nil {
		return nil, indexProblem("GET %s failed: %v", url, err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, problem.Newf(problem.ArtefactInvalid, "GET %s gave HTTP %d.", url, resp.StatusCode)
	}
	if _, err := c.verifyRemote(ctx, url, data); err != nil {
		return nil, err
	}
	return c.Accept(data, name, entry.Version, entry.SHA256)
}

// Accept checks a lock file (from the index or from a file) and writes it into the cache.
// wantSHA256 "" skips the hash check (a --lock-file of the CLI).
func (c Client) Accept(data []byte, name, version, wantSHA256 string) (*solution.Lock, error) {
	sum := sha256.Sum256(data)
	got := hex.EncodeToString(sum[:])
	if wantSHA256 != "" && got != strings.ToLower(wantSHA256) {
		return nil, problem.Newf(problem.ArtefactInvalid, "The SHA-256 of %s-%s.lock.yaml is %s, not the value of the index (%s).", name, version, got, wantSHA256)
	}
	l, err := solution.ParseLock(data)
	if err != nil {
		return nil, problem.New(problem.ArtefactInvalid, err.Error())
	}
	if (name != "" && l.Name != name) || (version != "" && solution.CompareLockVersions(l.Version, version) != 0) {
		return nil, problem.Newf(problem.ArtefactInvalid, "The lock file is %s %s, not %s %s.", l.Name, l.Version, name, version)
	}
	if err := (solution.LockStore{Paths: c.Paths}).Put(l); err != nil {
		return nil, err
	}
	return l, nil
}

// errNoIndex tells that no index is cached.
var errNoIndex = errors.New("no index.json is cached")

// LocalIndexURL is the index of the dist snapshot of the Raspberry Pi image.
const LocalIndexURL = "file://" + paths.LocalDist + "/index/index.json"

// ImportLocalDist reads the dist snapshot in dir (the layout of the release assets: index/index.json
// and <tag>/<asset>), puts each lock of its index into the lock cache, and makes its index the cached
// index (docs/image.md, "The dist snapshot"). Each lock must have the SHA-256 of the index entry.
// It returns nil and fs.ErrNotExist when dir has no index.
func (c Client) ImportLocalDist(dir string) (*solution.Index, error) {
	data, err := os.ReadFile(c.Paths.Abs(dir + "/index/index.json"))
	if err != nil {
		return nil, err
	}
	// The index has the SHA-256 of each lock, so its signature covers the locks of the snapshot.
	keyID, err := c.verifyLocal(c.Paths.Abs(dir+"/index/index.json"), data)
	if err != nil {
		return nil, err
	}
	ix, err := solution.ParseIndex(data)
	if err != nil {
		return nil, problem.Newf(problem.ArtefactInvalid, "%s/index/index.json: %v", dir, err)
	}
	for _, s := range ix.Solutions {
		for _, e := range s.Locks {
			if strings.Contains(e.URL, "://") {
				continue
			}
			lockData, err := os.ReadFile(c.Paths.Abs(dir + "/" + e.URL))
			if err != nil {
				return nil, problem.Newf(problem.ArtefactInvalid, "The snapshot index lists %s, but the file is missing: %v", e.URL, err)
			}
			if _, err := c.Accept(lockData, s.Name, e.Version, e.SHA256); err != nil {
				return nil, err
			}
		}
	}
	if err := lock.WriteFileAtomic(c.Paths.Abs(solution.IndexCacheFile), data, 0o600); err != nil {
		return nil, err
	}
	_ = writeJSON(c.Paths.Abs(indexMetaFile), signedMeta(indexMeta{URL: "file://" + dir + "/index/index.json", FetchedAt: c.now()}, keyID, data))
	return ix, nil
}
