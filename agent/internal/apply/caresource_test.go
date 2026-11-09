package apply

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/rithviknishad/care-pi/agent/internal/exitcode"
	"github.com/rithviknishad/care-pi/agent/internal/host"
	"github.com/rithviknishad/care-pi/agent/internal/netinfo"
	"github.com/rithviknishad/care-pi/agent/internal/paths"
	"github.com/rithviknishad/care-pi/agent/internal/problem"
)

const (
	careGitSHA = "28211e29a359fde735fc1394226ff9382ce47f71"
	careBranch = "ENG-737-backup-backup"
	careBase   = "ghcr.io/rithviknishad/care-pi/care-base:28211e29a359"
)

// gitSolution writes a solution with care.source git (S71) and returns its directory.
func gitSolution(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	sol := `apiVersion: care-pi/v1
kind: Solution
name: care-clinic
title: CARE Clinic
description: Outpatient clinic.
care:
  source: git
  repo: https://github.com/ohcnetwork/care
  ref: ENG-737-backup-backup
care_fe:
  repo: https://github.com/ohcnetwork/care_fe
  ref: 3.1/base
`
	if err := os.MkdirAll(filepath.Join(dir, "care-clinic"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "care-clinic", "solution.yaml"), []byte(sol), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func (e *env) prepareGit(dir string, ramKiB int64, baseInRegistry bool) (*Prepared, error) {
	e.t.Helper()
	return e.prepareGitRegistry(dir, ramKiB, baseInRegistry, "manifest unknown")
}

// prepareGitRegistry is prepareGit with the stderr of skopeo when the registry has no image (baseInRegistry false).
func (e *env) prepareGitRegistry(dir string, ramKiB int64, baseInRegistry bool, skopeoErr string) (*Prepared, error) {
	e.t.Helper()
	if ramKiB > 0 {
		if err := os.MkdirAll(e.p.Abs("/proc"), 0o755); err != nil {
			e.t.Fatal(err)
		}
		if err := os.WriteFile(e.p.Abs("/proc/meminfo"), []byte("MemTotal:       "+strings.TrimSpace(itoa(ramKiB))+" kB\n"), 0o644); err != nil {
			e.t.Fatal(err)
		}
	}
	inspect := "skopeo inspect --override-os linux --override-arch " + runtime.GOARCH + " --format {{.Digest}} docker://" + careBase
	if baseInRegistry {
		e.fake.On(inspect, host.Response{Out: digest(careBase) + "\n"})
	} else {
		e.fake.On(inspect, host.Response{Code: 1, Out: skopeoErr})
	}
	h := host.New(e.fake)
	h.Git = &host.FakeGit{Refs: map[string]map[string]string{
		"https://github.com/ohcnetwork/care":    {"refs/heads/" + careBranch: careGitSHA},
		"https://github.com/ohcnetwork/care_fe": {"refs/heads/3.1/base": careFESHA},
	}}
	return Prepare(context.Background(), PrepareOptions{Paths: e.p, ConfigPath: e.cfg, SolutionsDir: dir, Host: h,
		IPs: netinfo.Static{"192.168.1.10"}, Phase: "full"})
}

func itoa(v int64) string {
	var b [20]byte
	i := len(b)
	for v >= 10 {
		i--
		b[i] = byte('0' + v%10)
		v /= 10
	}
	i--
	b[i] = byte('0' + v)
	return string(b[i:])
}

// S71: a git source resolves the branch to its commit and runs the image that CI built for the commit.
func TestGitCareUsesTheImageOfTheCommit(t *testing.T) {
	e := newEnv(t, smallClinic)
	pr, err := e.prepareGit(gitSolution(t), 2_000_000, true)
	if err != nil {
		t.Fatal(err)
	}
	img := pr.Lock.Images["care"]
	if img.Ref != careBase || img.Digest != digest(careBase) || pr.CareSource != nil {
		t.Fatalf("care image %+v, source %+v", img, pr.CareSource)
	}
	c := pr.Lock.Components.Care
	if c == nil || c.Ref != careBranch || c.GitSHA != careGitSHA || !c.Tracked || c.Image != "ghcr.io/rithviknishad/care-pi/care-base" {
		t.Fatalf("components.care %+v", c)
	}
}

// S71: a 2 GB host cannot build CARE: without the image of the commit, the apply stops with a clear problem.
func TestGitCareWithoutImageOn2GB(t *testing.T) {
	e := newEnv(t, smallClinic)
	_, err := e.prepareGit(gitSolution(t), 2_000_000, false)
	var p *problem.Problem
	if exitcode.Code(err) != exitcode.BadInput || !errors.As(err, &p) || p.Slug != problem.UnsupportedConfig ||
		!strings.Contains(p.Detail, "4 GB of RAM") || !strings.Contains(p.Detail, "28211e29a359") {
		t.Fatalf("%v", err)
	}
}

// S71: a registry that does not answer (no DNS, a timeout, a 5xx) is not a registry without the image: no refusal
// with unsupported-config on 2 GB, and no local build of 2 hours on 4 GB. The apply stops with
// build-required-offline, and the operator tries again.
func TestGitCareRegistryErrorIsNotAMissingImage(t *testing.T) {
	for _, stderr := range []string{"FATA[0000] ... dial tcp: lookup ghcr.io: no such host",
		"FATA[0000] ... received unexpected HTTP status: 503 Service Unavailable", "FATA[0000] ... context deadline exceeded"} {
		for _, ramKiB := range []int64{2_000_000, 8_000_000} {
			e := newEnv(t, smallClinic)
			pr, err := e.prepareGitRegistry(gitSolution(t), ramKiB, false, stderr)
			var p *problem.Problem
			if pr != nil || !errors.As(err, &p) || p.Slug != problem.BuildRequiredOffline || exitcode.Code(err) == exitcode.BadInput ||
				!strings.Contains(p.Detail, careBase) || !strings.Contains(p.Detail, "try again") {
				t.Fatalf("%s on %d kB: %+v %v", stderr, ramKiB, pr, err)
			}
			if len(e.fake.Filter("systemctl")) != 0 {
				t.Fatalf("%s on %d kB started a unit: %q", stderr, ramKiB, e.fake.Calls)
			}
		}
	}
	// The registry says that the name or the tag does not exist: the known paths.
	e := newEnv(t, smallClinic)
	if _, err := e.prepareGitRegistry(gitSolution(t), 2_000_000, false, "FATA[0000] ... name unknown"); err == nil || !problem.Is(err, problem.UnsupportedConfig) {
		t.Fatalf("name unknown on 2 GB: %v", err)
	}
	e = newEnv(t, smallClinic)
	pr, err := e.prepareGitRegistry(gitSolution(t), 8_000_000, false, "FATA[0000] ... unexpected status code 404 Not Found")
	if err != nil || pr.CareSource == nil {
		t.Fatalf("404 on 8 GB: %+v %v", pr, err)
	}
}

// S71: a host with 4 GB or more builds CARE from its git source: the build unit, the local image, and the download.
func TestGitCareLocalSourceBuild(t *testing.T) {
	e := newEnv(t, smallClinic)
	pr, err := e.prepareGit(gitSolution(t), 8_000_000, false)
	if err != nil {
		t.Fatal(err)
	}
	cs := pr.CareSource
	if cs == nil || cs.Image() != "localhost/care-pi/care-base:28211e29a359" || pr.Lock.Images["care"].Ref != cs.Image() {
		t.Fatalf("source %+v, care %+v", cs, pr.Lock.Images["care"])
	}
	unit := pr.Tree[paths.QuadletDir+"/care-build-care-clinic-base.build"]
	for _, want := range []string{"ImageTag=localhost/care-pi/care-base:28211e29a359", "File=/var/lib/care-pi/build/care-src/28211e29a359/docker/prod.Dockerfile",
		"--label=org.opencontainers.image.revision=" + careGitSHA, "MemoryMax=3G"} {
		if !strings.Contains(string(unit.Content), want) {
			t.Fatalf("the build unit lacks %q:\n%s", want, unit.Content)
		}
	}

	// The download of the source: the archive of GitHub has one directory <repo>-<sha>/.
	var hits []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits = append(hits, r.URL.Path)
		_, _ = w.Write(tarGz(t, map[string]string{
			"care-" + careGitSHA + "/docker/prod.Dockerfile": "FROM python:3.13-slim-bookworm\n",
			"care-" + careGitSHA + "/manage.py":              "print()\n",
			"care-" + careGitSHA + "/../evil":                "x",
		}))
	}))
	defer srv.Close()
	cs.Repo = srv.URL + "/ohcnetwork/care"
	e.fake.On("podman image exists "+cs.Image(), host.Response{Code: 1})
	e.targetState("active")
	_, err = ensureCareSource(context.Background(), Options{Paths: e.p, Host: host.New(e.fake), Out: &e.out, PollInterval: 1}, pr)
	if err == nil || !strings.Contains(err.Error(), "leaves the directory") {
		t.Fatalf("an entry outside the directory must stop the extraction: %v", err)
	}
	srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits = append(hits, r.URL.Path)
		_, _ = w.Write(tarGz(t, map[string]string{
			"care-" + careGitSHA + "/docker/prod.Dockerfile": "FROM python:3.13-slim-bookworm\n",
			"care-" + careGitSHA + "/manage.py":              "print()\n",
		}))
	})
	if _, err := ensureCareSource(context.Background(), Options{Paths: e.p, Host: host.New(e.fake), Out: &e.out, PollInterval: 1}, pr); err != nil {
		t.Fatalf("%v\n%s", err, e.out.String())
	}
	if len(hits) != 2 || hits[1] != "/ohcnetwork/care/archive/"+careGitSHA+".tar.gz" {
		t.Fatalf("downloads %v", hits)
	}
	if data, err := os.ReadFile(e.p.Abs(cs.Dir() + "/docker/prod.Dockerfile")); err != nil || !strings.HasPrefix(string(data), "FROM python") {
		t.Fatalf("the source: %v", err)
	}
	if len(e.fake.Filter("systemctl start care-build-care-clinic-base-build.service")) != 1 {
		t.Fatalf("calls %q", e.fake.Calls)
	}
}

func tarGz(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var b bytes.Buffer
	gz := gzip.NewWriter(&b)
	tw := tar.NewWriter(gz)
	for name, data := range files {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(data)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(data)); err != nil {
			t.Fatal(err)
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
