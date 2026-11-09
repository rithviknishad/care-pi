package service

import (
	"testing"
	"time"

	"github.com/rithviknishad/care-pi/agent/internal/paths"
	"github.com/rithviknishad/care-pi/agent/internal/setup"
)

// TestRefreshHardwareStepKeepsTheDiskWarnings: the finish refreshes only the internet warning of the hardware step.
// The disk warnings stay: the disk probe of the step is not in memory after a restart of the agent, and the disk did
// not change. Without a live internet check the record stays as it is.
func TestRefreshHardwareStepKeepsTheDiskWarnings(t *testing.T) {
	p := paths.New(t.TempDir())
	s := &Service{Paths: p, Setup: &setup.Store{Paths: p}}
	stored := []setup.Issue{{Code: "sd-card", Message: "the root disk is an SD card"},
		{Code: "slow-disk", Message: "the root disk writes less than 20 MB/s"},
		{Code: warnNoInternet, Message: "the device cannot reach the internet; the first apply needs it for the images"}}
	reset := func() {
		t.Helper()
		_, err := s.Setup.Update(func(r *setup.Record) error {
			r.Steps[setup.StepHardware] = &setup.StepRecord{Status: setup.Done, UpdatedAt: time.Now(), Warnings: append([]setup.Issue{}, stored...)}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	codes := func() []string {
		t.Helper()
		rec, _, err := s.Setup.Load()
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, w := range rec.Steps[setup.StepHardware].Warnings {
			out = append(out, w.Code+": "+w.Message)
		}
		return out
	}
	yes, no, dns := true, false, InternetNoDNS

	// After a restart: no disk probe in memory, the internet works now.
	reset()
	if err := s.refreshHardwareStep(Hardware{RootDisk: RootDisk{Kind: "sd"}, InternetReachable: &yes}); err != nil {
		t.Fatal(err)
	}
	if got := codes(); len(got) != 2 || got[0] != stored[0].Code+": "+stored[0].Message || got[1] != stored[1].Code+": "+stored[1].Message {
		t.Fatalf("warnings %q", got)
	}

	// The internet still fails, now without DNS: the internet warning is the live one.
	reset()
	if err := s.refreshHardwareStep(Hardware{InternetReachable: &no, InternetProblem: &dns}); err != nil {
		t.Fatal(err)
	}
	if got := codes(); len(got) != 3 || got[2] != "no-internet: the device reaches the internet, but DNS does not work; the first apply needs it for the images" {
		t.Fatalf("warnings %q", got)
	}

	// No live check: the record stays.
	reset()
	if err := s.refreshHardwareStep(Hardware{}); err != nil {
		t.Fatal(err)
	}
	if got := codes(); len(got) != 3 || got[2] != stored[2].Code+": "+stored[2].Message {
		t.Fatalf("warnings %q", got)
	}

	// No hardware step yet: nothing to do.
	if _, err := s.Setup.Update(func(r *setup.Record) error { delete(r.Steps, setup.StepHardware); return nil }); err != nil {
		t.Fatal(err)
	}
	if err := s.refreshHardwareStep(Hardware{InternetReachable: &yes}); err != nil {
		t.Fatal(err)
	}
}
