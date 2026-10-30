package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rithviknishad/care-pi/agent/internal/applock"
	"github.com/rithviknishad/care-pi/agent/internal/config"
	"github.com/rithviknishad/care-pi/agent/internal/exitcode"
	"github.com/rithviknishad/care-pi/agent/internal/fe"
	"github.com/rithviknishad/care-pi/agent/internal/lock"
	"github.com/rithviknishad/care-pi/agent/internal/paths"
	"github.com/rithviknishad/care-pi/agent/internal/render"
	"github.com/rithviknishad/care-pi/agent/internal/solution"
)

const (
	testSolutions = "../../internal/render/testdata/solutions"
	testFESHA     = "83b007b72cd0e692464d9ad61f7f3bfc0dbb317a"
)

func TestFEPlumbingAgreesWithApply(t *testing.T) {
	script, err := execute(t, "fe", "script")
	if err != nil || !strings.HasPrefix(script, "#!/bin/bash\n# "+render.Header) {
		t.Fatalf("fe script: %v\n%.200s", err, script)
	}
	env, err := execute(t, "fe", "env", "--solution", "care-clinic", "--solutions", testSolutions)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"\nREACT_CARE_API_URL=\n", "\nREACT_MFE_REGISTERED_COMPONENTS=UserDashboard\n", "\nREACT_ALLOWED_LOCALES=en,hi,ml\n"} {
		if !strings.Contains(env, want) {
			t.Fatalf("fe env lacks %q:\n%s", want, env)
		}
	}
	file := filepath.Join(t.TempDir(), "frontend.env")
	if err := os.WriteFile(file, []byte(env), 0o644); err != nil {
		t.Fatal(err)
	}
	id, err := execute(t, "fe", "build-id", "--care-fe-sha", testFESHA, "--env-file", file)
	if err != nil {
		t.Fatal(err)
	}
	if want := defaultBuildID(t); strings.TrimSpace(id) != want {
		t.Fatalf("fe build-id %q, apply computes %q", id, want)
	}
	_, err = execute(t, "fe", "build-id", "--care-fe-sha", "develop", "--env-file", file)
	if exitcode.Code(err) != exitcode.BadInput {
		t.Fatalf("want exit code 2 for a ref, got %v", err)
	}
}

// defaultBuildID is the build id that apply computes for the default config of the test solution.
func defaultBuildID(t *testing.T) string {
	t.Helper()
	sol, err := solution.Load("care-clinic", testSolutions)
	if err != nil {
		t.Fatal(err)
	}
	des, err := render.Desire(config.Default(), sol)
	if err != nil {
		t.Fatal(err)
	}
	return fe.BuildID(testFESHA, des.Frontend)
}

func testBundle(t *testing.T) string {
	t.Helper()
	var b bytes.Buffer
	gz := gzip.NewWriter(&b)
	tw := tar.NewWriter(gz)
	body := "<html>care</html>"
	_ = tw.WriteHeader(&tar.Header{Name: "index.html", Typeflag: tar.TypeReg, Mode: 0o644, Size: int64(len(body))})
	_, _ = tw.Write([]byte(body))
	_ = tw.Close()
	_ = gz.Close()
	file := filepath.Join(t.TempDir(), "care_fe.tar.gz")
	if err := os.WriteFile(file, b.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	return file
}

func TestFEBuildStatusPrune(t *testing.T) {
	root := t.TempDir()
	p := paths.New(root)
	cfgPath := p.Abs(paths.ConfigFile)
	_ = os.MkdirAll(filepath.Dir(cfgPath), 0o700)
	cfg := "version: 1\nfe: {provider: artifact, artifact_url: \"file://" + testBundle(t) + "\"}\n"
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	id := defaultBuildID(t)
	lk := &lock.Lock{
		Version: 1,
		Git:     map[string]lock.GitRef{"care_fe": {Repo: "https://github.com/ohcnetwork/care_fe", Ref: "develop", SHA: testFESHA}},
		FE:      lock.FE{Provider: "artifact", BuildID: id},
	}
	data, _ := lk.Marshal()
	if err := os.WriteFile(p.Abs(paths.LockFile), data, 0o600); err != nil {
		t.Fatal(err)
	}

	out, err := execute(t, "fe", "status", "--root", root)
	if exitcode.Code(err) != exitcode.Failed || !strings.Contains(out, "build id:   "+id) {
		t.Fatalf("want exit code 1 before the build: %v\n%s", err, out)
	}
	out, err = execute(t, "fe", "build", "--root", root, "--solutions", testSolutions)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if fe.Current(p) != id {
		t.Fatalf("current %q, want %s\n%s", fe.Current(p), id, out)
	}
	got, _ := lock.Read(p.Abs(paths.LockFile))
	if !strings.HasPrefix(got.FE.URL, "file://") || len(got.FE.SHA256) != 64 || got.Git["care_fe"].SHA != testFESHA {
		t.Fatalf("lock fe %+v", got.FE)
	}
	out, err = execute(t, "fe", "status", "--root", root)
	if err != nil || !strings.Contains(out, "* "+id) {
		t.Fatalf("status: %v\n%s", err, out)
	}

	// Two newer builds: prune keeps the newest one and the current build.
	for i, b := range []string{"new1", "new2"} {
		dir := p.Abs(fe.Dir(b))
		_ = os.MkdirAll(dir, 0o755)
		_ = os.WriteFile(filepath.Join(dir, "index.html"), []byte(b), 0o644)
		mt := time.Now().Add(time.Duration(i+1) * time.Hour)
		_ = os.Chtimes(dir, mt, mt)
	}
	if _, err := execute(t, "fe", "prune", "--root", root, "--keep", "0"); exitcode.Code(err) != exitcode.BadInput {
		t.Fatalf("want exit code 2 for --keep 0, got %v", err)
	}
	out, err = execute(t, "fe", "prune", "--root", root, "--keep", "1")
	if err != nil || out != "removed new1\n" || !fe.Exists(p, id) || !fe.Exists(p, "new2") {
		t.Fatalf("prune: %v\n%s", err, out)
	}

	// A config change after the last apply: fe build asks for apply.
	if err := os.WriteFile(cfgPath, []byte(cfg+"env:\n  frontend: {REACT_APP_TITLE: X}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if out, err = execute(t, "fe", "build", "--root", root, "--solutions", testSolutions); err == nil || !strings.Contains(err.Error(), "run care-pi apply") {
		t.Fatalf("want a hint to run apply: %v\n%s", err, out)
	}
	if err := os.WriteFile(cfgPath, []byte("version: 1\nfe: {provider: none}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err = execute(t, "fe", "build", "--root", root, "--solutions", testSolutions); exitcode.Code(err) != exitcode.BadInput {
		t.Fatalf("want exit code 2 for provider none, got %v", err)
	}
}

func TestLockTimeout(t *testing.T) {
	root := t.TempDir()
	release, err := applock.Acquire(context.Background(), paths.New(root), 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	t.Cleanup(func() { _ = fePruneCmd.Flags().Set("lock-timeout", applock.DefaultTimeout.String()) })
	out, err := execute(t, "fe", "prune", "--root", root, "--lock-timeout", "300ms")
	if !errors.Is(err, applock.ErrTimeout) || exitcode.Code(err) != exitcode.Failed {
		t.Fatalf("want a lock timeout with exit code 1, got %v\n%s", err, out)
	}
}
