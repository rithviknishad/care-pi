package update

import (
	"context"
	"errors"
	"os"
	"reflect"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/rithviknishad/care-pi/agent/internal/apply"
	"github.com/rithviknishad/care-pi/agent/internal/host"
	"github.com/rithviknishad/care-pi/agent/internal/jobs"
	"github.com/rithviknishad/care-pi/agent/internal/problem"
)

// signalRunner sends SIGTERM to the test process when the command on runs, like systemd that stops
// care-update-apply.service. A command whose context has ended fails, like exec.CommandContext.
type signalRunner struct {
	*host.Fake
	on   string
	sent bool
}

func (r *signalRunner) Run(ctx context.Context, c host.Cmd) ([]byte, error) {
	if ctx.Err() != nil {
		_, _ = r.Fake.Run(ctx, c)
		return nil, ctx.Err()
	}
	if r.sent || c.String() != r.on {
		return r.Fake.Run(ctx, c)
	}
	r.sent = true
	_, _ = r.Fake.Run(ctx, c)
	if err := syscall.Kill(os.Getpid(), syscall.SIGTERM); err != nil {
		return nil, err
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-time.After(10 * time.Second):
		return nil, errors.New("SIGTERM did not end the context of the job")
	}
}

// TestSignalDuringCutoverRollsBack: SIGTERM inside migrate (systemd stops the unit of the window) runs the
// rollback to the previous lock, and the process does not stop.
func TestSignalDuringCutoverRollsBack(t *testing.T) {
	e := newTEnv(t, "pinned", "2026.11.0")
	e.install()
	e.writeConfig("stable", "")
	sr := &signalRunner{Fake: e.fake, on: "systemctl start care-beat.service"}
	e.u.Host = host.New(sr)
	e.u.StopOnSignal = true
	e.fake.Calls = nil
	rec := e.job(jobs.KindUpdateApply)
	_, err := e.u.Apply(context.Background(), rec, ApplyOptions{Now: true, Source: jobs.SourceTimer})
	var p *problem.Problem
	if !errors.As(err, &p) || p.Slug != problem.MigrationFailed || p.PitrOffer == nil || !strings.Contains(p.Detail, "interrupted") {
		t.Fatalf("want migration-failed (interrupted) with a PITR offer, got %v", err)
	}
	got := actions(rec)
	if want := []string{"cutover-maintenance-on", "stop", "checkpoint", "activate", "migrate", "rollback", "maintenance-off"}; !reflect.DeepEqual(got[6:], want) {
		t.Fatalf("steps %v", got)
	}
	if st := rec.Job().Steps; st[len(st)-3].State != jobs.StepFailed || st[len(st)-2].State != jobs.StepSucceeded {
		t.Fatalf("migrate, then the rollback: %+v %+v", st[len(st)-3], st[len(st)-2])
	}
	if starts := e.fake.Filter("systemctl start care-beat.service"); len(starts) != 2 {
		t.Fatalf("care-beat starts %q", starts)
	}
	if l := e.running(); l.Solution.Version != "2026.11.0" {
		t.Fatalf("lock %+v", l.Solution)
	}
	if _, on := apply.ReadMaintenance(e.p); on {
		t.Fatal("the maintenance page stays on")
	}
	if st := ReadState(e.p); st.Cutover != nil {
		t.Fatalf("the cutover record stays: %+v", st.Cutover)
	}
}

// TestSignalDuringOSUpdateHolds: SIGTERM during the install of the tested version holds the packages again,
// and the job starts no other step.
func TestSignalDuringOSUpdateHolds(t *testing.T) {
	e := newTEnv(t, "stable", "")
	e.osFixture()
	const aptGet = "systemd-run --wait --collect --pipe --quiet --unit=care-pi-apt -p MemoryMax=512M --setenv=DEBIAN_FRONTEND=noninteractive -- apt-get -o DPkg::Lock::Timeout=600 -o Dpkg::Options::=--force-confdef -o Dpkg::Options::=--force-confold "
	sr := &signalRunner{Fake: e.fake, on: aptGet + "-y install podman=5.4.2+ds1-2"}
	e.u.Host = host.New(sr)
	e.u.StopOnSignal = true
	e.fake.Calls = nil
	rec := e.job(jobs.KindOSUpdate)
	if _, err := e.u.OSUpdate(context.Background(), rec, OSOptions{}); !problem.Is(err, problem.OSUpdateFailed) {
		t.Fatalf("want os-update-failed, got %v", err)
	}
	if got := actions(rec); !reflect.DeepEqual(got, []string{"os-refresh", "os-upgrade", "os-unhold", "os-upgrade", "os-hold"}) {
		t.Fatalf("steps %v", got)
	}
	if st := rec.Job().Steps; st[3].State != jobs.StepFailed || st[4].State != jobs.StepSucceeded {
		t.Fatalf("steps %+v", st)
	}
	apt := e.fake.Filter("systemd-run")
	if last := apt[len(apt)-1]; !strings.HasSuffix(last, "apt-mark -o DPkg::Lock::Timeout=600 hold podman crun netavark aardvark-dns conmon skopeo") {
		t.Fatalf("no hold after the signal: %q", apt)
	}
}

// TestCutoverWindowMargin: a job of the window does not start the cutover when less than 10 minutes of the
// window remain; the target stays staged.
func TestCutoverWindowMargin(t *testing.T) {
	e := newTEnv(t, "pinned", "2026.11.0")
	e.install()
	e.writeConfig("stable", "")
	e.clock = time.Date(2026, 11, 22, 4, 51, 0, 0, time.UTC) // Sun; the window closes at 05:00
	rec := e.job(jobs.KindUpdateApply)
	_, err := e.u.Apply(context.Background(), rec, ApplyOptions{Source: jobs.SourceTimer})
	var p *problem.Problem
	if !errors.As(err, &p) || p.Slug != problem.UpdateWindowClosed || p.NextWindowAt == nil || !strings.Contains(p.Detail, "8m59s") {
		t.Fatalf("want update-window-closed, got %v", err)
	}
	if got := actions(rec); !reflect.DeepEqual(got, cutoverSteps[:6]) {
		t.Fatalf("steps %v", got)
	}
	if l := e.running(); l.Solution.Version != "2026.11.0" {
		t.Fatalf("lock %+v", l.Solution)
	}
	if st := ReadState(e.p); st.Staging == nil || st.Staging.State != "staged" || st.Cutover != nil {
		t.Fatalf("state %+v", st)
	}
	// 11 minutes are enough.
	e.clock = time.Date(2026, 11, 22, 4, 49, 0, 0, time.UTC)
	rec = e.job(jobs.KindUpdateApply)
	if _, err := e.u.Apply(context.Background(), rec, ApplyOptions{Source: jobs.SourceTimer}); err != nil {
		t.Fatal(err)
	}
	if l := e.running(); l.Solution.Version != "2026.11.1" {
		t.Fatalf("lock %+v", l.Solution)
	}
	e.clock = time.Date(2026, 11, 22, 5, 0, 0, 0, time.UTC)
	if err := e.u.CutoverWindow(mustConfig(t, e.cfg)); !problem.Is(err, problem.UpdateWindowClosed) {
		t.Fatalf("a closed window: %v", err)
	}
}
