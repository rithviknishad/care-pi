package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rithviknishad/care-pi/agent/internal/config"
	"github.com/rithviknishad/care-pi/agent/internal/exitcode"
	"github.com/rithviknishad/care-pi/agent/internal/paths"
)

func TestAuthAndSetupCommands(t *testing.T) {
	root := t.TempDir()
	cfg := filepath.Join(root, "etc", "care-pi", "config.yaml")
	data, err := config.DefaultYAML("", "care", "", "none")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(cfg), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfg, data, 0o600); err != nil {
		t.Fatal(err)
	}
	g := []string{"--root", root, "--config", cfg}
	t.Cleanup(func() {
		rootCmd.PersistentFlags().Lookup("config").Changed = false
		global.config, global.root = paths.ConfigFile, "/"
		rootCmd.SetIn(nil)
	})
	out, err := execute(t, append([]string{"auth", "claim-code"}, g...)...)
	if err != nil || !strings.Contains(out, "Claim code: ") || !strings.Contains(out, "https://care.local:8443/") {
		t.Fatalf("claim-code: %v\n%s", err, out)
	}
	rootCmd.SetIn(strings.NewReader("a long console passphrase\n"))
	out, err = execute(t, append([]string{"auth", "set-password", "--password-stdin"}, g...)...)
	if err != nil || !strings.Contains(out, "The device is claimed") || strings.Count(out, "-") < 8 {
		t.Fatalf("set-password: %v\n%s", err, out)
	}
	out, err = execute(t, append([]string{"auth", "status", "--json"}, g...)...)
	if err != nil {
		t.Fatal(err)
	}
	var st struct {
		RecoveryCodes struct {
			Remaining int `json:"remaining"`
		} `json:"recovery_codes"`
	}
	if err := json.Unmarshal([]byte(out), &st); err != nil || st.RecoveryCodes.Remaining != 8 {
		t.Fatalf("auth status: %v\n%s", err, out)
	}
	if _, err := execute(t, append([]string{"auth", "claim-code"}, g...)...); exitcode.Code(err) != exitcode.Failed {
		t.Fatalf("a claimed device has no claim code: %v", err)
	}
	out, err = execute(t, append([]string{"setup", "status", "--json"}, g...)...)
	if err != nil || !strings.Contains(out, `"state": "claimed"`) {
		t.Fatalf("setup status: %v\n%s", err, out)
	}
	out, err = execute(t, append([]string{"setup", "skip", "hardware"}, g...)...)
	if exitcode.Code(err) != exitcode.Failed || !strings.Contains(err.Error(), "skipped") {
		t.Fatalf("skip hardware: %v\n%s", err, out)
	}
	if got, err := unitName("agent"); err != nil || got != "care-pi.service" {
		t.Fatalf("agent unit: %s %v", got, err)
	}
}

func TestClaimBox(t *testing.T) {
	var b strings.Builder
	printClaimBox(&b, "4F7K-Q2MX-9PDA", []string{"care.local"}, []string{"192.168.1.10"})
	lines := strings.Split(strings.TrimSpace(b.String()), "\n")
	for _, l := range lines {
		if len(l) != len(lines[0]) {
			t.Fatalf("the box is not aligned:\n%s", b.String())
		}
	}
	if !strings.Contains(b.String(), "https://192.168.1.10:8443/") {
		t.Fatal(b.String())
	}
}
