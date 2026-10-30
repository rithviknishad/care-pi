package fe

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/rithviknishad/care-pi/agent/internal/buildinfo"
	"github.com/rithviknishad/care-pi/agent/internal/lock"
	"github.com/rithviknishad/care-pi/agent/internal/paths"
)

// DefaultArtifactBase is where the fe-bundle workflow publishes the default bundles.
const DefaultArtifactBase = "https://github.com/rithviknishad/care-pi/releases/download"

// Limits of the artifact provider.
const (
	MaxArtifactBytes  = 200 << 20
	maxExtractedBytes = 1 << 30
	maxEntries        = 100000
	downloadTimeout   = 15 * time.Minute
)

// MarkerFile is the file in the root of a build that fe-build.sh writes.
// It records the build id, the care_fe sha, and the names of the env variables.
const MarkerFile = "care-pi-build.json"

// Marker is the content of MarkerFile.
type Marker struct {
	BuildID    string   `json:"build_id"`
	CareFERepo string   `json:"care_fe_repo"`
	CareFESHA  string   `json:"care_fe_sha"`
	BuiltAt    string   `json:"built_at"`
	EnvKeys    []string `json:"env_keys"`
}

// BundleName returns the release tag and the file name of the default bundle:
// fe-<solution>-<sha12>-<build id> and care_fe-<solution>-<sha12>-<build id>.tar.gz.
// The build id is in the name, so a bundle with a different env never replaces a bundle
// that a Pi expects.
func BundleName(solution, careFESHA, buildID string) (tag, file string) {
	sha12 := careFESHA
	if len(sha12) > 12 {
		sha12 = sha12[:12]
	}
	base := solution + "-" + sha12 + "-" + buildID
	return "fe-" + base, "care_fe-" + base + ".tar.gz"
}

// DefaultArtifactURL returns the URL of the default bundle of the solution for the care_fe
// sha and the build id.
func DefaultArtifactURL(solution, careFESHA, buildID string) string {
	return artifactURL(DefaultArtifactBase, solution, careFESHA, buildID)
}

func artifactURL(base, solution, careFESHA, buildID string) string {
	tag, file := BundleName(solution, careFESHA, buildID)
	return base + "/" + tag + "/" + file
}

func init() {
	Register("artifact", func(env Env) (Provider, error) { return NewArtifact(env), nil })
}

// Artifact downloads a prebuilt care_fe bundle (a tar.gz file).
type Artifact struct {
	env Env
	// Client does the downloads. It supports https:// and file:// URLs.
	Client *http.Client
	// MaxBytes limits the size of the download.
	MaxBytes int64
	// Base is the release download URL for the default bundles.
	Base string
}

// NewArtifact returns the artifact provider.
func NewArtifact(env Env) *Artifact {
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.RegisterProtocol("file", http.NewFileTransport(http.Dir("/")))
	return &Artifact{
		env:      env,
		Client:   &http.Client{Transport: t, Timeout: downloadTimeout},
		MaxBytes: MaxArtifactBytes,
		Base:     DefaultArtifactBase,
	}
}

// Name implements Provider.
func (a *Artifact) Name() string { return "artifact" }

// URL returns the configured URL, or the default URL for the solution, the care_fe sha,
// and the build id. derived is true for the default URL.
func (a *Artifact) URL(buildID string) (url string, derived bool) {
	if a.env.Config != nil && a.env.Config.FE.ArtifactURL != "" {
		return a.env.Config.FE.ArtifactURL, false
	}
	name := ""
	if a.env.Config != nil {
		name = a.env.Config.Solution.Name
	}
	return artifactURL(a.Base, name, a.env.CareFESHA, buildID), true
}

// Ensure implements Provider.
func (a *Artifact) Ensure(ctx context.Context, buildID string) (Build, error) {
	if !validID(buildID) {
		return Build{}, fmt.Errorf("bad FE build id %q", buildID)
	}
	p := a.env.Paths
	dir := Dir(buildID)
	rec := lock.FE{Provider: a.Name(), BuildID: buildID, CareFESHA: a.env.CareFESHA}
	if Exists(p, buildID) {
		return Build{Dir: dir, Record: rec}, nil
	}
	unlock, err := takeLock(ctx, p, true)
	if err != nil {
		return Build{}, err
	}
	defer unlock()
	if Exists(p, buildID) {
		return Build{Dir: dir, Record: rec}, nil
	}
	url, derived := a.URL(buildID)
	if derived && a.env.CareFESHA == "" {
		return Build{}, errors.New("the lock has no care_fe sha, so care-pi cannot find the default bundle. Run care-pi apply with network access, or set fe.artifact_url")
	}
	out := a.env.out()
	_, _ = fmt.Fprintf(out, "    download %s\n", url)

	feDir := p.Abs(paths.FEDir)
	if err := os.MkdirAll(feDir, 0o755); err != nil {
		return Build{}, fmt.Errorf("cannot create %s: %w", paths.FEDir, err)
	}
	tmp, err := os.MkdirTemp(feDir, ".tmp-"+buildID+"-")
	if err != nil {
		return Build{}, fmt.Errorf("cannot create a temporary directory in %s: %w", paths.FEDir, err)
	}
	defer func() { _ = os.RemoveAll(tmp) }()

	file := filepath.Join(tmp, "bundle.tar.gz")
	sum, size, err := a.download(ctx, url, file, derived)
	if err != nil {
		return Build{}, err
	}
	want := ""
	if a.env.Config != nil {
		want = strings.ToLower(a.env.Config.FE.ArtifactSHA256)
	}
	if want == "" && derived {
		if want, err = a.sidecar(ctx, url+".sha256"); err != nil {
			return Build{}, err
		}
	}
	if want != "" && want != sum {
		return Build{}, fmt.Errorf("the SHA-256 of %s is %s, but %s is expected; care-pi did not install the bundle", url, sum, want)
	}
	root := filepath.Join(tmp, "root")
	if err := extractTarGz(file, root); err != nil {
		return Build{}, fmt.Errorf("cannot extract %s: %w", url, err)
	}
	if fi, err := os.Stat(filepath.Join(root, "index.html")); err != nil || !fi.Mode().IsRegular() {
		return Build{}, fmt.Errorf("the bundle %s has no index.html in its root", url)
	}
	if m, ok := readMarker(root); ok && m.BuildID != buildID {
		msg := fmt.Sprintf("the bundle is for FE build %s, but this config needs FE build %s", m.BuildID, buildID)
		if derived {
			return Build{}, fmt.Errorf("%s. The default bundle has the env of the solution, and env.frontend or the plugs change it. Use fe.provider: local, or set fe.artifact_url to a bundle with this env", msg)
		}
		_, _ = fmt.Fprintf(out, "    warning: %s; care-pi installs it because fe.artifact_url is set\n", msg)
	}
	if err := os.Chmod(root, 0o755); err != nil {
		return Build{}, err
	}
	dst := p.Abs(dir)
	if err := os.RemoveAll(dst); err != nil {
		return Build{}, fmt.Errorf("cannot remove the incomplete build %s: %w", dir, err)
	}
	if err := os.Rename(root, dst); err != nil {
		return Build{}, fmt.Errorf("cannot move the bundle to %s: %w", dir, err)
	}
	_, _ = fmt.Fprintf(out, "    installed FE build %s (%d bytes, sha256 %s)\n", buildID, size, sum)
	rec.URL, rec.SHA256 = url, sum
	return Build{Dir: dir, Record: rec}, nil
}

func (a *Artifact) get(ctx context.Context, url string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("bad artifact URL %q: %w", url, err)
	}
	req.Header.Set("User-Agent", "care-pi/"+buildinfo.Version)
	resp, err := a.Client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("cannot download %s: %w", url, err)
	}
	return resp, nil
}

// download writes url to file. It returns the SHA-256 in hex and the size.
func (a *Artifact) download(ctx context.Context, url, file string, derived bool) (string, int64, error) {
	resp, err := a.get(ctx, url)
	if err != nil {
		return "", 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		msg := fmt.Sprintf("cannot download %s: HTTP %d", url, resp.StatusCode)
		if derived && resp.StatusCode == http.StatusNotFound {
			msg += ". The fe-bundle workflow did not publish a bundle for this care_fe sha and FE build id (or the repository is private). The default bundle has only the default env of the solution: env.frontend or a plug with frontend env gives a different build id. Run the workflow for this sha, set fe.artifact_url, or use fe.provider: local"
		}
		return "", 0, errors.New(msg)
	}
	if resp.ContentLength > a.MaxBytes {
		return "", 0, fmt.Errorf("%s has %d bytes; the limit is %d bytes", url, resp.ContentLength, a.MaxBytes)
	}
	f, err := os.OpenFile(file, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return "", 0, err
	}
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, h), io.LimitReader(resp.Body, a.MaxBytes+1))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return "", 0, fmt.Errorf("cannot download %s: %w", url, err)
	}
	if n > a.MaxBytes {
		return "", 0, fmt.Errorf("%s has more than %d bytes", url, a.MaxBytes)
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}

var hexSHA256 = regexp.MustCompile(`^[0-9a-f]{64}$`)

// sidecar reads a .sha256 file ("<hex>  <name>"). It returns "" when the file does not exist.
func (a *Artifact) sidecar(ctx context.Context, url string) (string, error) {
	resp, err := a.get(ctx, url)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusNotFound {
		return "", nil
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("cannot download %s: HTTP %d", url, resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if err != nil {
		return "", fmt.Errorf("cannot download %s: %w", url, err)
	}
	fields := strings.Fields(string(data))
	if len(fields) == 0 || !hexSHA256.MatchString(strings.ToLower(fields[0])) {
		return "", fmt.Errorf("%s does not start with a SHA-256 in hex", url)
	}
	return strings.ToLower(fields[0]), nil
}

func readMarker(dir string) (Marker, bool) {
	var m Marker
	data, err := os.ReadFile(filepath.Join(dir, MarkerFile))
	if err != nil || json.Unmarshal(data, &m) != nil {
		return Marker{}, false
	}
	return m, true
}

// ReadMarker returns the marker of a build on the host, if the build has one.
func ReadMarker(p paths.Paths, buildID string) (Marker, bool) {
	return readMarker(p.Abs(Dir(buildID)))
}

// tarEntry is one entry of the archive, after the checks.
type tarEntry struct {
	name string
	dir  bool
}

// extractTarGz extracts a tar.gz file to dst. It accepts only files and directories,
// and rejects names that leave dst. When all entries are below one top-level
// directory, it removes that directory from the names.
func extractTarGz(file, dst string) error {
	var entries []tarEntry
	err := walkTarGz(file, func(hdr *tar.Header, name string, _ io.Reader) error {
		entries = append(entries, tarEntry{name: name, dir: hdr.Typeflag == tar.TypeDir})
		return nil
	})
	if err != nil {
		return err
	}
	if len(entries) == 0 {
		return errors.New("the archive is empty")
	}
	strip := commonTopDir(entries)
	if err := os.Mkdir(dst, 0o755); err != nil {
		return err
	}
	var total int64
	return walkTarGz(file, func(hdr *tar.Header, name string, r io.Reader) error {
		if strip != "" {
			if name == strip {
				return nil
			}
			name = strings.TrimPrefix(name, strip+"/")
		}
		target := filepath.Join(dst, filepath.FromSlash(name))
		if hdr.Typeflag == tar.TypeDir {
			return os.MkdirAll(target, 0o755)
		}
		total += hdr.Size
		if total > maxExtractedBytes {
			return fmt.Errorf("the archive has more than %d bytes of files", int64(maxExtractedBytes))
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
		if err != nil {
			return err
		}
		if _, err := io.Copy(f, io.LimitReader(r, hdr.Size)); err != nil {
			_ = f.Close()
			return err
		}
		return f.Close()
	})
}

// walkTarGz calls fn for each file and directory entry with its clean name.
func walkTarGz(file string, fn func(hdr *tar.Header, name string, r io.Reader) error) error {
	f, err := os.Open(file)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return fmt.Errorf("not a gzip file: %w", err)
	}
	defer func() { _ = gz.Close() }()
	tr := tar.NewReader(gz)
	for n := 0; ; n++ {
		if n > maxEntries {
			return fmt.Errorf("the archive has more than %d entries", maxEntries)
		}
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("bad tar data: %w", err)
		}
		switch hdr.Typeflag {
		case tar.TypeXGlobalHeader:
			continue
		case tar.TypeReg, tar.TypeDir:
		default:
			return fmt.Errorf("the archive has the entry %q of type %q; only files and directories are allowed", hdr.Name, hdr.Typeflag)
		}
		name := path.Clean(strings.ReplaceAll(hdr.Name, "\\", "/"))
		if name == "." {
			continue
		}
		if !fs.ValidPath(name) {
			return fmt.Errorf("the archive has the unsafe path %q", hdr.Name)
		}
		if err := fn(hdr, name, tr); err != nil {
			return err
		}
	}
}

// commonTopDir returns the top-level directory when every entry is that directory or below it.
func commonTopDir(entries []tarEntry) string {
	top := ""
	for _, e := range entries {
		first, _, below := strings.Cut(e.name, "/")
		if !below && !e.dir {
			return ""
		}
		if top == "" {
			top = first
		} else if top != first {
			return ""
		}
	}
	return top
}
