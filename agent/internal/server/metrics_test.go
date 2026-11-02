package server

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"strings"
	"testing"

	"github.com/rithviknishad/care-pi/agent/internal/host"
	"github.com/rithviknishad/care-pi/agent/internal/paths"
	"github.com/rithviknishad/care-pi/agent/internal/render"
	"github.com/rithviknishad/care-pi/agent/internal/service"
)

// TestMetrics: GET /api/v1/metrics gives the Prometheus text; the text file of node_exporter exists only while
// telemetry is on (Phase 7, docs/telemetry.md).
func TestMetrics(t *testing.T) {
	e, _ := readyConnEnv(t)
	for _, ref := range []string{render.ImageVMAgent, render.ImageNodeExporter} {
		e.fake.On("skopeo inspect --override-os linux --override-arch "+runtime.GOARCH+" --format {{.Digest}} docker://"+ref,
			host.Response{Out: fmt.Sprintf("sha256:%064x\n", len(ref))})
	}
	r := e.do("GET", "/api/v1/metrics", nil)
	e.want(r, 200, "")
	if ct := r.header.Get("Content-Type"); !strings.HasPrefix(ct, "text/plain; version=0.0.4") {
		t.Fatalf("content type %q", ct)
	}
	body := string(r.body)
	for _, want := range []string{"# TYPE care_pi_unit_active gauge", `care_pi_info{version="`, "care_pi_host_memory_total_bytes",
		"care_pi_clock_trusted", `care_pi_firewall_policy{policy="none"} 1`} {
		if !strings.Contains(body, want) {
			t.Errorf("no %q in:\n%s", want, body)
		}
	}
	f := e.p.Abs(paths.TelemetryTextfileDir + "/" + service.TextfileName)
	if err := e.svc.WriteTextfile(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(f); !os.IsNotExist(err) {
		t.Fatal("a text file while telemetry is off")
	}
	etag := e.do("GET", "/api/v1/config", nil).header.Get("ETag")
	e.want(e.do("PATCH", "/api/v1/config", map[string]any{"telemetry": map[string]any{"enabled": true, "remote_write_url": "https://metrics.example.org/api/v1/write"}},
		header("Content-Type", "application/merge-patch+json"), header("If-Match", etag)), 200, "")
	if err := e.svc.WriteTextfile(context.Background()); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(f)
	if err != nil || !strings.Contains(string(data), "care_pi_unit_active") {
		t.Fatalf("text file: %v\n%s", err, data)
	}
	etag = e.do("GET", "/api/v1/config", nil).header.Get("ETag")
	e.want(e.do("PATCH", "/api/v1/config", map[string]any{"telemetry": map[string]any{"enabled": false}},
		header("Content-Type", "application/merge-patch+json"), header("If-Match", etag)), 200, "")
	if err := e.svc.WriteTextfile(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(f); !os.IsNotExist(err) {
		t.Fatal("the text file stays after telemetry off")
	}
	// The schema requires remote_write_url with enabled.
	etag = e.do("GET", "/api/v1/config", nil).header.Get("ETag")
	e.want(e.do("PATCH", "/api/v1/config", map[string]any{"telemetry": map[string]any{"enabled": true, "remote_write_url": nil}},
		header("Content-Type", "application/merge-patch+json"), header("If-Match", etag), invalid), 422, "invalid-config")
}
