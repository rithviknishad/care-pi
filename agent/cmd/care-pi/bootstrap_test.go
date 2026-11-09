package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rithviknishad/care-pi/agent/internal/auth"
	"github.com/rithviknishad/care-pi/agent/internal/config"
	"github.com/rithviknishad/care-pi/agent/internal/paths"
	"github.com/rithviknishad/care-pi/agent/internal/solution"
	"github.com/rithviknishad/care-pi/agent/internal/update/updatetest"
)

func writeFile(t *testing.T, path, data string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestInstallPreseed(t *testing.T) {
	p := paths.New(t.TempDir())
	cfgPath := p.Abs(paths.ConfigFile)
	var w bytes.Buffer
	if used, err := installPreseed(&w, p, cfgPath); used || err != nil {
		t.Fatalf("no pre-seed: %v %v", used, err)
	}

	// m3: a pre-seed that bootstrap cannot use gives setup mode (not a silent device), and the error is recorded.
	for _, bad := range []struct{ name, data, want string }{
		{"not valid", "version: 1\nidentity: {hostname: Not_A_Label}\n", "the pre-seed is not valid"},
		{"not implemented", "version: 1\ntls:\n  local_ca: {enabled: false}\n", "tls.local_ca.enabled: false is not implemented"},
		{"needs a registration", "version: 1\ntls:\n  public: {enabled: true, clinic_id: hill-k3x9}\n", "no registration with the DNS service"},
	} {
		_ = os.Remove(p.Abs(paths.FirstbootError))
		writeFile(t, p.Abs(paths.PreseedConfig), bad.data)
		if used, err := installPreseed(&w, p, cfgPath); used || err != nil {
			t.Fatalf("%s: %v %v; want setup mode", bad.name, used, err)
		}
		if _, err := os.Stat(cfgPath); err == nil {
			t.Fatalf("%s: a bad pre-seed must not become config.yaml", bad.name)
		}
		var rec FirstbootError
		data, err := os.ReadFile(p.Abs(paths.FirstbootError))
		if err != nil || json.Unmarshal(data, &rec) != nil || !strings.Contains(rec.Error, bad.want) || rec.File != paths.PreseedConfig+".rejected" {
			t.Fatalf("%s: firstboot-error.json %s %v", bad.name, data, err)
		}
		if _, err := os.Stat(p.Abs(paths.PreseedConfig)); !os.IsNotExist(err) {
			t.Fatalf("%s: the rejected pre-seed is still at %s", bad.name, paths.PreseedConfig)
		}
		if got, _ := os.ReadFile(p.Abs(paths.PreseedConfig + ".rejected")); string(got) != bad.data {
			t.Fatalf("%s: preseed.yaml.rejected %q", bad.name, got)
		}
	}
	if !strings.Contains(w.String(), "start setup mode") {
		t.Fatalf("output:\n%s", w.String())
	}

	// A partial config: the defaults fill the rest.
	pre := "version: 1\nidentity: {display_name: Hill Clinic, hostname: hill}\ntls:\n  local_ca: {enabled: true}\n"
	if _, err := config.Parse([]byte(pre), "pre"); err != nil {
		t.Fatalf("the test pre-seed is not valid: %v", err)
	}
	writeFile(t, p.Abs(paths.PreseedConfig), pre)
	used, err := installPreseed(&w, p, cfgPath)
	if !used || err != nil {
		t.Fatalf("pre-seed: %v %v", used, err)
	}
	if data, err := os.ReadFile(cfgPath); err != nil || string(data) != pre {
		t.Fatalf("config.yaml %q: %v", data, err)
	}
	if fi, _ := os.Stat(cfgPath); fi.Mode().Perm() != 0o600 {
		t.Errorf("config.yaml mode %v", fi.Mode().Perm())
	}

	// A second run (after a failed apply) keeps config.yaml and still applies everything.
	writeFile(t, cfgPath, pre+"# edited\n")
	if used, err := installPreseed(&w, p, cfgPath); !used || err != nil {
		t.Fatalf("second run: %v %v", used, err)
	}
	if data, _ := os.ReadFile(cfgPath); !strings.HasSuffix(string(data), "# edited\n") {
		t.Error("the second run replaced config.yaml")
	}
}

func TestFinishFirstboot(t *testing.T) {
	p := paths.New(t.TempDir())
	writeFile(t, p.Abs(paths.ImageRelease), `{"version":"2026.11.0-1","solution":"care-clinic","lock_version":"2026.11.0","built_at":"2026-11-01T10:00:00Z","git_sha":"0123456789abcdef0123456789abcdef01234567"}`)
	writeFile(t, p.Abs(paths.PreseedConfig), "version: 1\n")
	var w bytes.Buffer
	imageInfo(&w, p)
	if !strings.Contains(w.String(), "image 2026.11.0-1: care-clinic 2026.11.0") {
		t.Errorf("image info: %q", w.String())
	}
	if err := finishFirstboot(&w, p, "bootstrap", true); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(p.Abs(paths.FirstbootDone))
	if err != nil {
		t.Fatal(err)
	}
	var rec struct {
		Phase        string `json:"phase"`
		Preseed      bool   `json:"preseed"`
		ImageRelease struct {
			LockVersion string `json:"lock_version"`
		} `json:"image_release"`
	}
	if err := json.Unmarshal(data, &rec); err != nil || rec.Phase != "bootstrap" || !rec.Preseed || rec.ImageRelease.LockVersion != "2026.11.0" {
		t.Fatalf("marker %s: %v", data, err)
	}
	if _, err := os.Stat(p.Abs(paths.PreseedConfig + ".applied")); err != nil {
		t.Errorf("the pre-seed is not renamed: %v", err)
	}

	// A host without the image: a warning, and the marker has image_release null.
	q := paths.New(t.TempDir())
	w.Reset()
	imageInfo(&w, q)
	if !strings.Contains(w.String(), "warning:") {
		t.Errorf("no image: %q", w.String())
	}
	if err := finishFirstboot(&w, q, "full", false); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(q.Abs(paths.FirstbootDone)); !strings.Contains(string(data), `"image_release": null`) {
		t.Errorf("marker without an image: %s", data)
	}
}

func TestImportLocalDistAtFirstBoot(t *testing.T) {
	p := paths.New(t.TempDir())
	var w bytes.Buffer
	importLocalDist(&w, p)
	if !strings.Contains(w.String(), "warning: the image has no dist snapshot") {
		t.Errorf("no snapshot: %q", w.String())
	}
	data, err := config.DefaultYAML("", "care", "", "")
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Parse(data, "config.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := updatetest.Build(p.Abs(paths.LocalDist), "../../../solutions/care-clinic/solution.yaml", cfg); err != nil {
		t.Fatal(err)
	}
	w.Reset()
	importLocalDist(&w, p)
	if !strings.Contains(w.String(), "2 solution lock(s) in the update cache") {
		t.Errorf("snapshot: %q", w.String())
	}
	if got := (solution.LockStore{Paths: p}).Versions("care-clinic"); len(got) < 2 {
		t.Errorf("versions %v", got)
	}
}

// TestFromImageClaimHint: the image makes the claim code, but the console asks for the Imager password first.
func TestFromImageClaimHint(t *testing.T) {
	p := paths.New(t.TempDir())
	a := auth.New(auth.Options{Paths: p, OS: imager{}})
	code, err := bootstrapClaimCode(a, false)
	if err != nil || len(code) != 14 {
		t.Fatalf("claim code %q: %v", code, err)
	}
	if got := a.ClaimMethods(); len(got) != 2 || got[0] != auth.ClaimOSPassword {
		t.Fatalf("claim methods %v, want os-password first", got)
	}
}
