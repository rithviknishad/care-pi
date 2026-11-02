package metrics

import (
	"math"
	"os"
	"path/filepath"
	"testing"
)

func TestExposition(t *testing.T) {
	s := New()
	s.Gauge("care_pi_unit_memory_bytes", "Memory of a unit.", 104857600, "unit", "care-api.service")
	s.Gauge("care_pi_unit_memory_bytes", "Memory of a unit.", 1.5e9, "unit", `odd"\name`+"\n")
	s.Bool("care_pi_clock_trusted", "The clock is trusted.", true)
	s.Counter("care_pi_unit_restarts_total", "Restarts\nof a unit.", 3, "unit", "care-db.service")
	s.Gauge("care_pi_nan", "NaN.", math.NaN())
	want := `# HELP care_pi_unit_memory_bytes Memory of a unit.
# TYPE care_pi_unit_memory_bytes gauge
care_pi_unit_memory_bytes{unit="care-api.service"} 104857600
care_pi_unit_memory_bytes{unit="odd\"\\name\n"} 1500000000
# HELP care_pi_clock_trusted The clock is trusted.
# TYPE care_pi_clock_trusted gauge
care_pi_clock_trusted 1
# HELP care_pi_unit_restarts_total Restarts\nof a unit.
# TYPE care_pi_unit_restarts_total counter
care_pi_unit_restarts_total{unit="care-db.service"} 3
# HELP care_pi_nan NaN.
# TYPE care_pi_nan gauge
care_pi_nan NaN
`
	if got := string(s.Bytes()); got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
	f := filepath.Join(t.TempDir(), "x", "care_pi.prom")
	if err := s.WriteFile(f); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(f); string(data) != want {
		t.Fatal("file differs")
	}
}

func TestBadName(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("no panic")
		}
	}()
	New().Gauge("care-pi", "x", 1)
}
