package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rithviknishad/care-pi/agent/internal/paths"
)

func writeFirstbootError(t *testing.T, p paths.Paths) {
	t.Helper()
	must(t, os.MkdirAll(filepath.Dir(p.Abs(paths.FirstbootError)), 0o700))
	must(t, os.WriteFile(p.Abs(paths.FirstbootError), []byte(`{"at":"2026-11-01T10:00:00Z","file":"/etc/care-pi/preseed.yaml.rejected",
"error":"the pre-seed is not valid: identity.hostname: not a DNS label","action":"The device starts setup mode: claim it in the console."}`), 0o600))
}

// m3: GET /_care-pi/status tells why the first boot did not use the pre-seed, until the setup is finished.
func TestPublicStatusFirstbootError(t *testing.T) {
	e := newEnv(t)
	r := e.do("GET", "/_care-pi/status", nil)
	e.want(r, 200, "")
	if v, ok := r.json(t)["firstboot_error"]; !ok || v != nil {
		t.Fatalf("firstboot_error without an error: %v %v", v, ok)
	}
	writeFirstbootError(t, e.p)
	e.clock.add(3 * time.Second) // the cache of the public status
	r = e.do("GET", "/_care-pi/status", nil)
	e.want(r, 200, "")
	got, _ := r.json(t)["firstboot_error"].(string)
	if !strings.HasPrefix(got, "The first boot did not use the pre-seed: the pre-seed is not valid: identity.hostname") ||
		!strings.HasSuffix(got, "claim it in the console.") {
		t.Fatalf("firstboot_error %q", got)
	}

	ready, _ := readyConnEnv(t)
	writeFirstbootError(t, ready.p)
	r = ready.do("GET", "/_care-pi/status", nil)
	if v := r.json(t)["firstboot_error"]; v != nil {
		t.Fatalf("after the setup: %v", v)
	}
}
