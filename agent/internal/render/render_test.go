package render

import (
	"bytes"
	"context"
	"crypto/sha256"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/rithviknishad/care-pi/agent/internal/config"
	"github.com/rithviknishad/care-pi/agent/internal/fe"
	"github.com/rithviknishad/care-pi/agent/internal/host"
	"github.com/rithviknishad/care-pi/agent/internal/lock"
	"github.com/rithviknishad/care-pi/agent/internal/solution"
)

var update = flag.Bool("update", false, "rewrite the golden files")

// TestIPs are the fixed LAN addresses of the golden cases.
var TestIPs = []string{"192.168.1.10", "10.0.0.5"}

type fakeSkopeo struct{}

func (fakeSkopeo) Digest(_ context.Context, ref, _, _ string) (string, error) {
	return fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(ref))), nil
}

func fakeGit() *host.FakeGit {
	return &host.FakeGit{Refs: map[string]map[string]string{
		"https://github.com/ohcnetwork/care_fe": {
			"refs/heads/develop":   "573c5620573c5620573c5620573c5620573c5620",
			"refs/tags/v25.2.0":    "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			"refs/tags/v25.2.0^{}": "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		},
		"https://github.com/ohcnetwork/care_multihost_files_be.git": {"refs/heads/main": "cccccccccccccccccccccccccccccccccccccccc"},
	}}
}

// fixture renders one golden case.
func fixture(t *testing.T, name string) (Tree, *lock.Lock) {
	t.Helper()
	cfg, err := config.Load(filepath.Join("testdata", "config-"+name+".yaml"))
	if err != nil {
		t.Fatal(err)
	}
	sol, err := solution.Load("care-clinic", "testdata/solutions")
	if err != nil {
		t.Fatal(err)
	}
	pl, err := Desire(cfg, sol)
	if err != nil {
		t.Fatal(err)
	}
	r := lock.Resolver{Skopeo: fakeSkopeo{}, Git: fakeGit(), OS: "linux", Arch: "arm64"}
	lk, err := r.Resolve(context.Background(), pl.Wants, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	m, err := NewModel(Input{Config: cfg, Solution: sol, Lock: lk, IPs: TestIPs})
	if err != nil {
		t.Fatal(err)
	}
	lk.GeneratedAt = "2026-10-30T00:00:00Z"
	lk.AgentVersion = "test"
	lk.FE = lock.FE{Provider: m.FEProvider, BuildID: m.FEBuildID}
	tree, err := Render(m)
	if err != nil {
		t.Fatal(err)
	}
	return tree, lk
}

func TestGolden(t *testing.T) {
	for _, name := range []string{"solo", "small-clinic", "busy-counter", "custom"} {
		t.Run(name, func(t *testing.T) {
			tree, lk := fixture(t, name)
			lockData, err := lk.Marshal()
			if err != nil {
				t.Fatal(err)
			}
			got := map[string][]byte{"/etc/care-pi/lock.yaml": lockData}
			for p, f := range tree {
				got[p] = f.Content
			}
			dir := filepath.Join("testdata", "golden", name)
			if *update {
				if err := os.RemoveAll(dir); err != nil {
					t.Fatal(err)
				}
				for p, data := range got {
					fp := filepath.Join(dir, filepath.FromSlash(p))
					if err := os.MkdirAll(filepath.Dir(fp), 0o755); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(fp, data, 0o644); err != nil {
						t.Fatal(err)
					}
				}
				return
			}
			var onDisk []string
			_ = filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
				if err == nil && !info.IsDir() {
					rel, _ := filepath.Rel(dir, p)
					onDisk = append(onDisk, "/"+filepath.ToSlash(rel))
				}
				return nil
			})
			var want []string
			for p := range got {
				want = append(want, p)
			}
			sort.Strings(want)
			sort.Strings(onDisk)
			if strings.Join(want, "\n") != strings.Join(onDisk, "\n") {
				t.Fatalf("file list differs; run go test ./agent/internal/render -update\nwant:\n%s\ngolden:\n%s",
					strings.Join(want, "\n"), strings.Join(onDisk, "\n"))
			}
			for p, data := range got {
				golden, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(p)))
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(golden, data) {
					t.Errorf("%s differs from the golden file; run go test ./agent/internal/render -update\n--- got ---\n%s", p, data)
				}
			}
		})
	}
}

func TestEveryFileHasHeader(t *testing.T) {
	tree, _ := fixture(t, "custom")
	for p, f := range tree {
		s := string(f.Content)
		if strings.HasPrefix(s, "#!") {
			s = s[strings.Index(s, "\n")+1:]
		}
		if !strings.Contains(strings.SplitN(s, "\n", 2)[0], Header) {
			t.Errorf("%s: the first line is not the header: %q", p, strings.SplitN(s, "\n", 2)[0])
		}
	}
}

func TestContainersFollowTheRules(t *testing.T) {
	for _, name := range []string{"solo", "custom"} {
		tree, _ := fixture(t, name)
		for p, f := range tree {
			if !strings.HasSuffix(p, ".container") {
				continue
			}
			s := string(f.Content)
			for _, key := range []string{"RunInit=true", "MemoryMax=", "MemorySwapMax=", "PartOf=care.target"} {
				if !strings.Contains(s, key) {
					t.Errorf("%s: %s missing", p, key)
				}
			}
			if strings.HasSuffix(p, "care-fe-build.container") {
				if strings.Contains(s, "WantedBy=") || strings.Contains(s, "Restart=always") || strings.Contains(s, "Pod=") {
					t.Errorf("%s: the builder must not start at boot, restart, or join the pod", p)
				}
				continue
			}
			for _, key := range []string{"Pod=care.pod", "Restart=always", "WantedBy=care.target"} {
				if !strings.Contains(s, key) {
					t.Errorf("%s: %s missing", p, key)
				}
			}
			if strings.Contains(s, "Volume=/etc/care-pi/rendered/") && strings.Contains(s, ".conf:") {
				t.Errorf("%s: bind-mounts a single rendered file", p)
			}
		}
	}
}

func TestNoSecretInTree(t *testing.T) {
	tree, _ := fixture(t, "small-clinic")
	for p, f := range tree {
		for _, bad := range []string{"POSTGRES_PASSWORD=", "DJANGO_SECRET_KEY=", "JWKS_BASE64=", "BUCKET_SECRET=", "cipher-pass="} {
			if strings.Contains(string(f.Content), bad) {
				t.Errorf("%s contains %q", p, bad)
			}
		}
	}
}

func TestNoAddresses(t *testing.T) {
	cfg := config.Default()
	cfg.TLS.LocalCA.Enabled = false
	sol, err := solution.Load("care-clinic", "testdata/solutions")
	if err != nil {
		t.Fatal(err)
	}
	pl, _ := Desire(cfg, sol)
	lk, _ := lock.Resolver{Skopeo: fakeSkopeo{}, Git: fakeGit()}.Resolve(context.Background(), pl.Wants, nil, false)
	if _, err := NewModel(Input{Config: cfg, Solution: sol, Lock: lk}); err == nil {
		t.Fatal("want an error without hostnames and addresses")
	}
}

func TestBucketCollision(t *testing.T) {
	cfg := config.Default()
	cfg.Env.Backend = map[string]string{"FILE_UPLOAD_BUCKET": "api"}
	sol, _ := solution.Load("care-clinic", "testdata/solutions")
	pl, _ := Desire(cfg, sol)
	lk, _ := lock.Resolver{Skopeo: fakeSkopeo{}, Git: fakeGit()}.Resolve(context.Background(), pl.Wants, nil, false)
	_, err := NewModel(Input{Config: cfg, Solution: sol, Lock: lk, IPs: TestIPs})
	if err == nil || !strings.Contains(err.Error(), "collides") {
		t.Fatalf("want a collision error, got %v", err)
	}
}

func TestOwned(t *testing.T) {
	cases := map[string]bool{
		"/etc/containers/systemd/care.pod":             true,
		"/etc/containers/systemd/care-api.container":   true,
		"/etc/containers/systemd/care-x.volume":        true,
		"/etc/containers/systemd/other.container":      false,
		"/etc/containers/systemd/care-api.container.d": false,
		"/etc/systemd/system/care.target":              true,
		"/etc/systemd/system/care-start.service":       true,
		"/etc/systemd/system/care-pi.service":          false,
		"/etc/care-pi/rendered/caddy/Caddyfile":        true,
		"/etc/care-pi/config.yaml":                     false,
	}
	for p, want := range cases {
		if got := Owned(p); got != want {
			t.Errorf("Owned(%s) = %v, want %v", p, got, want)
		}
	}
}

// The fe-bundle workflow computes the build id from frontend.env ("care-pi fe build-id").
// The id must be the id that apply uses.
func TestFrontendEnvGivesTheBuildID(t *testing.T) {
	tree, lk := fixture(t, "custom")
	env, err := fe.ParseEnvFile(bytes.NewReader(tree["/etc/care-pi/rendered/fe/frontend.env"].Content))
	if err != nil {
		t.Fatal(err)
	}
	if got := fe.BuildID(lk.Git["care_fe"].SHA, env); got != lk.FE.BuildID {
		t.Fatalf("build id from frontend.env %s != %s", got, lk.FE.BuildID)
	}
	if _, ok := env["REACT_PUBLIC_URL"]; ok {
		t.Fatal("care-pi must not set REACT_PUBLIC_URL; the build id then depends on the host name")
	}
	script, err := FEBuildScript()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(script, tree["/etc/care-pi/rendered/fe/fe-build.sh"].Content) {
		t.Fatal("FEBuildScript differs from the rendered fe-build.sh")
	}
	file, err := FrontendEnvFile(env)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(file, tree["/etc/care-pi/rendered/fe/frontend.env"].Content) {
		t.Fatal("FrontendEnvFile differs from the rendered frontend.env")
	}
}

func TestGitHubSlug(t *testing.T) {
	for in, want := range map[string]string{
		"https://github.com/ohcnetwork/care_fe":      "ohcnetwork/care_fe",
		"https://github.com/ohcnetwork/care_fe.git/": "ohcnetwork/care_fe",
		"https://gitlab.com/x/y":                     "",
		"https://github.com/x":                       "",
	} {
		if got := GitHubSlug(in); got != want {
			t.Errorf("GitHubSlug(%s) = %q, want %q", in, got, want)
		}
	}
}
