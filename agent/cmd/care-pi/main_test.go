package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rithviknishad/care-pi/agent/internal/exitcode"
)

func execute(t *testing.T, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&out)
	rootCmd.SetArgs(args)
	err := rootCmd.Execute()
	return out.String(), err
}

func TestRenderCommand(t *testing.T) {
	out := t.TempDir()
	root := t.TempDir()
	cfg := "../../internal/render/testdata/config-small-clinic.yaml"
	stdout, err := execute(t, "render", "--config", cfg, "--out", out, "--root", root,
		"--ip", "192.168.1.10", "--ip", "10.0.0.5")
	if err != nil {
		t.Fatalf("%v\n%s", err, stdout)
	}
	for _, p := range []string{
		"etc/containers/systemd/care.pod",
		"etc/containers/systemd/care-api.container",
		"etc/systemd/system/care.target",
		"etc/care-pi/rendered/caddy/Caddyfile",
		"etc/care-pi/rendered/care/backend.env",
		"etc/care-pi/rendered/db/pgbackrest.conf",
		"etc/care-pi/lock.yaml",
	} {
		if _, err := os.Stat(filepath.Join(out, p)); err != nil {
			t.Errorf("%s: %v", p, err)
		}
	}
	pod, _ := os.ReadFile(filepath.Join(out, "etc/containers/systemd/care.pod"))
	if !strings.Contains(string(pod), "PublishPort=192.168.1.10:443:443") {
		t.Fatalf("care.pod:\n%s", pod)
	}
	if !strings.Contains(stdout, "uses the tag") {
		t.Fatalf("want a warning about the missing lock:\n%s", stdout)
	}
}

func TestConfigValidateExitCode(t *testing.T) {
	_, err := execute(t, "config", "validate", "--config", "../../internal/config/testdata/invalid-preset.yaml")
	if exitcode.Code(err) != exitcode.BadInput {
		t.Fatalf("want exit code 2, got %v", err)
	}
	_, err = execute(t, "config", "validate", "--config", "../../internal/config/testdata/valid-custom.yaml")
	if err != nil {
		t.Fatal(err)
	}
}

func TestUnitName(t *testing.T) {
	cases := map[string]string{
		"api": "care-api.service", "care-db": "care-db.service", "care-s3.service": "care-s3.service",
		"clock": "care-clock.service", "target": "care.target", "pod": "care-pod.service",
	}
	for in, want := range cases {
		if got, err := unitName(in); err != nil || got != want {
			t.Errorf("unitName(%s) = %s %v", in, got, err)
		}
	}
	if _, err := unitName("sshd"); err == nil {
		t.Error("want an error for sshd")
	}
}
