package fe

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/rithviknishad/care-pi/agent/internal/config"
	"github.com/rithviknishad/care-pi/agent/internal/paths"
)

const testSHA = "83b007b72cd0e692464d9ad61f7f3bfc0dbb317a"

type entry struct {
	name string
	body string
	typ  byte
}

func tarGz(t *testing.T, entries ...entry) []byte {
	t.Helper()
	var b bytes.Buffer
	gz := gzip.NewWriter(&b)
	tw := tar.NewWriter(gz)
	for _, e := range entries {
		typ := e.typ
		if typ == 0 {
			typ = tar.TypeReg
			if strings.HasSuffix(e.name, "/") {
				typ = tar.TypeDir
			}
		}
		hdr := &tar.Header{Name: e.name, Typeflag: typ, Mode: 0o644, Size: int64(len(e.body))}
		if typ == tar.TypeSymlink {
			hdr.Linkname, hdr.Size = e.body, 0
		}
		if typ == tar.TypeDir {
			hdr.Mode, hdr.Size = 0o755, 0
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
		if hdr.Size > 0 {
			if _, err := tw.Write([]byte(e.body)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func sum(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func bundle(t *testing.T) []byte {
	return tarGz(t,
		entry{name: "./"},
		entry{name: "./index.html", body: "<html>care</html>"},
		entry{name: "./assets/"},
		entry{name: "./assets/index-abc.js", body: "console.log(1)"},
	)
}

// server serves files and counts the requests.
type server struct {
	*httptest.Server
	files map[string][]byte
	hits  atomic.Int32
	ua    atomic.Value
}

func newServer(t *testing.T, files map[string][]byte) *server {
	s := &server{files: files}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.hits.Add(1)
		s.ua.Store(r.UserAgent())
		data, ok := s.files[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(data)
	}))
	t.Cleanup(s.Close)
	return s
}

func artifactFor(t *testing.T, url, sha string) (*Artifact, paths.Paths, *bytes.Buffer) {
	t.Helper()
	p := paths.New(t.TempDir())
	cfg := config.Default()
	cfg.FE = config.FE{Provider: "artifact", ArtifactURL: url, ArtifactSHA256: sha}
	var out bytes.Buffer
	a := NewArtifact(Env{Config: cfg, Paths: p, CareFESHA: testSHA, Out: &out})
	return a, p, &out
}

// feEntries lists the names in the FE directory.
func feEntries(t *testing.T, p paths.Paths) []string {
	t.Helper()
	ents, _ := os.ReadDir(p.Abs(paths.FEDir))
	var out []string
	for _, e := range ents {
		out = append(out, e.Name())
	}
	return out
}

func TestArtifactDownloadAndIdempotence(t *testing.T) {
	data := bundle(t)
	s := newServer(t, map[string][]byte{"/b.tar.gz": data})
	a, p, _ := artifactFor(t, s.URL+"/b.tar.gz", strings.ToUpper(sum(data)))
	ctx := context.Background()
	b, err := a.Ensure(ctx, "build1")
	if err != nil {
		t.Fatal(err)
	}
	if b.Dir != "/var/lib/care-pi/fe/build1" {
		t.Fatalf("dir %s", b.Dir)
	}
	for _, f := range []string{"index.html", "assets/index-abc.js"} {
		if _, err := os.Stat(filepath.Join(p.Abs(b.Dir), f)); err != nil {
			t.Fatal(err)
		}
	}
	fi, _ := os.Stat(p.Abs(b.Dir))
	if fi.Mode().Perm() != 0o755 {
		t.Fatalf("mode %v", fi.Mode())
	}
	if b.Record.URL != s.URL+"/b.tar.gz" || b.Record.SHA256 != sum(data) || b.Record.CareFESHA != testSHA || b.Record.Provider != "artifact" {
		t.Fatalf("record %+v", b.Record)
	}
	if ua, _ := s.ua.Load().(string); !strings.HasPrefix(ua, "care-pi/") {
		t.Fatalf("user agent %q", ua)
	}
	if got := feEntries(t, p); strings.Join(got, " ") != "build1" {
		t.Fatalf("FE dir has %v", got)
	}
	hits := s.hits.Load()
	if _, err := a.Ensure(ctx, "build1"); err != nil {
		t.Fatal(err)
	}
	if s.hits.Load() != hits {
		t.Fatal("the second Ensure downloaded again")
	}
}

func TestArtifactSHAMismatch(t *testing.T) {
	data := bundle(t)
	s := newServer(t, map[string][]byte{"/b.tar.gz": data})
	a, p, _ := artifactFor(t, s.URL+"/b.tar.gz", strings.Repeat("0", 64))
	_, err := a.Ensure(context.Background(), "build1")
	if err == nil || !strings.Contains(err.Error(), "SHA-256") {
		t.Fatalf("want a SHA-256 error, got %v", err)
	}
	if got := feEntries(t, p); len(got) != 0 {
		t.Fatalf("FE dir has %v", got)
	}
}

func TestArtifactRejectsUnsafeEntries(t *testing.T) {
	for name, data := range map[string][]byte{
		"traversal": tarGz(t, entry{name: "index.html", body: "x"}, entry{name: "../evil", body: "x"}),
		"nested":    tarGz(t, entry{name: "index.html", body: "x"}, entry{name: "a/../../evil", body: "x"}),
		"absolute":  tarGz(t, entry{name: "index.html", body: "x"}, entry{name: "/evil", body: "x"}),
		"symlink":   tarGz(t, entry{name: "index.html", body: "x"}, entry{name: "evil", body: "/etc/passwd", typ: tar.TypeSymlink}),
		"no index":  tarGz(t, entry{name: "app.js", body: "x"}),
		"not gzip":  []byte("plain text"),
	} {
		t.Run(name, func(t *testing.T) {
			s := newServer(t, map[string][]byte{"/b.tar.gz": data})
			a, p, _ := artifactFor(t, s.URL+"/b.tar.gz", "")
			if _, err := a.Ensure(context.Background(), "build1"); err == nil {
				t.Fatal("want an error")
			}
			if got := feEntries(t, p); len(got) != 0 {
				t.Fatalf("FE dir has %v", got)
			}
			if _, err := os.Stat(p.Abs(paths.DataDir + "/evil")); err == nil {
				t.Fatal("an entry left the target directory")
			}
		})
	}
}

func TestArtifactStripsOneTopDir(t *testing.T) {
	data := tarGz(t,
		entry{name: "build/"},
		entry{name: "build/index.html", body: "<html>"},
		entry{name: "build/assets/a.js", body: "1"},
	)
	s := newServer(t, map[string][]byte{"/b.tar.gz": data})
	a, p, _ := artifactFor(t, s.URL+"/b.tar.gz", "")
	b, err := a.Ensure(context.Background(), "build1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(p.Abs(b.Dir), "assets/a.js")); err != nil {
		t.Fatal(err)
	}
}

func TestArtifactFileURL(t *testing.T) {
	data := bundle(t)
	file := filepath.Join(t.TempDir(), "b.tar.gz")
	if err := os.WriteFile(file, data, 0o644); err != nil {
		t.Fatal(err)
	}
	a, p, _ := artifactFor(t, "file://"+file, "")
	if _, err := a.Ensure(context.Background(), "build1"); err != nil {
		t.Fatal(err)
	}
	if !Exists(p, "build1") {
		t.Fatal("no build")
	}
	a, _, _ = artifactFor(t, "file://"+file+".missing", "")
	if _, err := a.Ensure(context.Background(), "build1"); err == nil || !strings.Contains(err.Error(), "404") {
		t.Fatalf("want HTTP 404, got %v", err)
	}
}

func TestArtifactSizeLimit(t *testing.T) {
	s := newServer(t, map[string][]byte{"/b.tar.gz": bundle(t)})
	a, p, _ := artifactFor(t, s.URL+"/b.tar.gz", "")
	a.MaxBytes = 10
	if _, err := a.Ensure(context.Background(), "build1"); err == nil || !strings.Contains(err.Error(), "limit") {
		t.Fatalf("want a size error, got %v", err)
	}
	if got := feEntries(t, p); len(got) != 0 {
		t.Fatalf("FE dir has %v", got)
	}
}

func TestArtifactDefaultURL(t *testing.T) {
	if got := DefaultArtifactURL("care-clinic", testSHA, "b5e55d6293a872fb"); got != "https://github.com/rithviknishad/care-pi/releases/download/fe-care-clinic-83b007b72cd0-b5e55d6293a872fb/care_fe-care-clinic-83b007b72cd0-b5e55d6293a872fb.tar.gz" {
		t.Fatalf("default URL %s", got)
	}
	marked := func(id string) []byte {
		return tarGz(t,
			entry{name: "index.html", body: "<html>"},
			entry{name: MarkerFile, body: `{"build_id":"` + id + `","care_fe_sha":"` + testSHA + `"}`},
		)
	}
	path := "/fe-care-clinic-83b007b72cd0-build1/care_fe-care-clinic-83b007b72cd0-build1.tar.gz"
	good := marked("build1")
	cases := []struct {
		name  string
		files map[string][]byte
		err   string
	}{
		{"sidecar ok", map[string][]byte{path: good, path + ".sha256": []byte(sum(good) + "  care_fe.tar.gz\n")}, ""},
		{"no sidecar", map[string][]byte{path: good}, ""},
		{"sidecar mismatch", map[string][]byte{path: good, path + ".sha256": []byte(strings.Repeat("1", 64) + "\n")}, "SHA-256"},
		{"other build", map[string][]byte{path: marked("other")}, "fe.provider: local"},
		{"not published", map[string][]byte{}, "did not publish"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := newServer(t, c.files)
			a, p, _ := artifactFor(t, "", "")
			a.Base = s.URL
			_, err := a.Ensure(context.Background(), "build1")
			if c.err == "" {
				if err != nil {
					t.Fatal(err)
				}
				if m, ok := ReadMarker(p, "build1"); !ok || m.BuildID != "build1" {
					t.Fatalf("marker %+v", m)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), c.err) {
				t.Fatalf("want %q, got %v", c.err, err)
			}
			if Exists(p, "build1") {
				t.Fatal("the build exists after an error")
			}
		})
	}
}

func TestArtifactExplicitURLWarnsOnOtherBuild(t *testing.T) {
	data := tarGz(t,
		entry{name: "index.html", body: "<html>"},
		entry{name: MarkerFile, body: `{"build_id":"other"}`},
	)
	s := newServer(t, map[string][]byte{"/b.tar.gz": data})
	a, p, out := artifactFor(t, s.URL+"/b.tar.gz", "")
	if _, err := a.Ensure(context.Background(), "build1"); err != nil {
		t.Fatal(err)
	}
	if !Exists(p, "build1") || !strings.Contains(out.String(), "warning: the bundle is for FE build other") {
		t.Fatalf("output:\n%s", out.String())
	}
}
