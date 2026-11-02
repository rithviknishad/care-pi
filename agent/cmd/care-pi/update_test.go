package main

import (
	"testing"
	"time"

	"github.com/rithviknishad/care-pi/agent/internal/host"
	"github.com/rithviknishad/care-pi/agent/internal/paths"
	"github.com/rithviknishad/care-pi/agent/internal/service"
)

// TestOSRebootWaits: "care-pi update --os --reboot --now" returns from RebootForOS at once, and the command waits for
// the delayed "systemctl reboot" of its own process. Before, the process ended before the timer, and the host did not
// reboot (final acceptance on lumine).
func TestOSRebootWaits(t *testing.T) {
	fake := host.NewFake()
	svc := &service.Service{Paths: paths.New(t.TempDir()), Host: host.New(fake)}
	var pw powerWaiter
	svc.AfterPower = pw.after
	r, err := svc.RebootForOS(true, 50*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if r.Scheduled {
		t.Fatalf("want a reboot now, got %+v", r)
	}
	if got := fake.Filter("systemctl reboot"); len(got) != 0 {
		t.Fatalf("the reboot ran before the delay: %v", got)
	}
	pw.wait()
	if got := fake.Filter("systemctl reboot"); len(got) != 1 {
		t.Fatalf("want one systemctl reboot after wait, got %v", fake.Calls)
	}
}

// TestPowerWaiterNoAction: wait returns at once when no power action was scheduled (an OS update without a reboot).
func TestPowerWaiterNoAction(t *testing.T) {
	var pw powerWaiter
	done := make(chan struct{})
	go func() { pw.wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("wait blocked without a power action")
	}
}
