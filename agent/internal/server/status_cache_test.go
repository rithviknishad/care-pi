package server

import (
	"context"
	"testing"
)

// A status read whose request ends early (the browser closes or navigates away) kills its commands (exit code -1). The
// agent kept that result for 2 s, and the next client saw "cannot read the container health" and "A required host check
// failed" (the phone journey on the public name in the Phase 6 exit test). The agent must not keep such a result.
func TestStatusNotKeptAfterCancel(t *testing.T) {
	e, _ := readyConnEnv(t)
	reads := func() int { return len(e.fake.Filter("podman ps --all --filter pod=care")) }
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, _ = e.svc.Status(ctx)
	_ = e.svc.PublicStatus(ctx)
	n := reads()
	if _, err := e.svc.Status(t.Context()); err != nil {
		t.Fatal(err)
	}
	if reads() != n+1 {
		t.Fatal("the status of a cancelled request was kept: the next read did not run the commands")
	}
	if _, err := e.svc.Status(t.Context()); err != nil || reads() != n+1 {
		t.Fatalf("a good status is kept for 2 s (%v)", err)
	}
	healthy := len(e.fake.Calls)
	p := e.svc.PublicStatus(t.Context())
	if p == nil || len(e.fake.Calls) == healthy {
		t.Fatal("the public status of a cancelled request was kept")
	}
}
