package jobs

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/rithviknishad/care-pi/agent/internal/paths"
	"github.com/rithviknishad/care-pi/agent/internal/problem"
)

func TestRecorderAndRead(t *testing.T) {
	p := paths.New(t.TempDir())
	now := time.Date(2026, 10, 30, 10, 15, 0, 123e6, time.UTC)
	rec, err := Create(CreateOptions{Paths: p, Kind: KindApply, Source: SourceConsole, Reason: "test", Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	if rec.ID() != "20261030T101500.123Z-apply" {
		t.Fatalf("id %s", rec.ID())
	}
	end := rec.Step("restart: care-api.service")
	rec.Log("agent", "waiting")
	end(nil)
	end = rec.Step("ensure-fe: build")
	end(errors.New("the FE build failed"))
	rec.Report(problem.New(problem.Internal, "ensure-fe failed"))
	j := rec.Finish(nil, ApplyResult{Kind: KindApply, Changed: true})
	if j.State != StateSucceeded || len(j.Steps) != 2 || *j.Steps[0].Action != "restart" || j.Steps[1].State != StepFailed {
		t.Fatalf("job %+v", j)
	}
	evs, err := Events(p, j.ID, 0, 100)
	if err != nil || len(evs) != 7 || evs[6].Event != "done" || evs[6].ID != 7 {
		t.Fatalf("events %+v %v", evs, err)
	}
	if got, _ := Events(p, j.ID, 5, 100); len(got) != 2 {
		t.Fatalf("after 5: %d", len(got))
	}
	data, _ := os.ReadFile(p.Abs(paths.JobsDir + "/" + j.ID + ".json"))
	if !strings.Contains(string(data), `"result": "ok"`) || !strings.Contains(string(data), `"reason": "test"`) {
		t.Fatalf("the journal keeps the Phase 1 members: %s", data)
	}
}

func TestPhase1JournalAndInterrupted(t *testing.T) {
	p := paths.New(t.TempDir())
	dir := p.Abs(paths.JobsDir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	phase1 := `{"kind":"apply","reason":"bootstrap","started":"2026-10-30T07:41:11.505Z","finished":"2026-10-30T07:45:00Z","result":"failed","error":"apply failed at start-target: x","steps":[{"name":"write-files: 3 file(s)","started":"2026-10-30T07:41:12Z","finished":"2026-10-30T07:41:12Z"},{"name":"start-target: care.target","started":"2026-10-30T07:41:13Z","finished":"2026-10-30T07:45:00Z","error":"x"}]}`
	running := `{"kind":"setup","started":"2026-10-30T08:00:00Z","result":"running","steps":[{"name":"pull-images: a","started":"2026-10-30T08:00:01Z"}],"source":"console","pid":999999,"boot_id":"other-boot"}`
	if err := os.WriteFile(dir+"/20261030T074111.505Z-apply.json", []byte(phase1), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dir+"/20261030T080000.000Z-setup.json", []byte(running), 0o600); err != nil {
		t.Fatal(err)
	}
	j, err := Load(p, "20261030T074111.505Z-apply")
	if err != nil {
		t.Fatal(err)
	}
	if j.Source != "cli" || j.State != StateFailed || j.Error.Slug != problem.Internal || *j.Steps[0].Action != "write-files" || j.Steps[1].State != StepFailed {
		t.Fatalf("phase 1 job %+v", j)
	}
	j, _ = Load(p, "20261030T080000.000Z-setup")
	if j.State != StateFailed || j.Error.Detail != "interrupted" || j.Steps[0].State != StepFailed {
		t.Fatalf("a dead running job must read as failed: %+v", j)
	}
	if ids := MarkInterrupted(p, time.Now()); len(ids) != 1 {
		t.Fatalf("interrupted %v", ids)
	}
	evs, _ := Events(p, "20261030T080000.000Z-setup", 0, 10)
	if len(evs) != 1 || evs[0].Event != "done" {
		t.Fatalf("the interrupted job needs a done event: %+v", evs)
	}
	list, next := List(p, Filter{Limit: 1})
	if len(list) != 1 || next != "20261030T080000.000Z-setup" {
		t.Fatalf("page %+v next %s", list, next)
	}
	if _, err := Load(p, "../../etc/passwd"); !errors.Is(err, ErrNotFound) {
		t.Fatal("a bad id must not read a file")
	}
}

func TestManagerLockAndCancel(t *testing.T) {
	p := paths.New(t.TempDir())
	m := &Manager{Paths: p, LockWait: 50 * time.Millisecond, CancelPoll: 5 * time.Millisecond}
	block := make(chan struct{})
	j, err := m.Start(context.Background(), Spec{Kind: KindApply, Source: SourceConsole, Lock: true},
		func(ctx context.Context, r *Recorder) (any, error) { <-block; return ApplyResult{Kind: KindApply}, nil })
	if err != nil {
		t.Fatal(err)
	}
	_, err = m.Start(context.Background(), Spec{Kind: KindFEPrune, Lock: true}, func(context.Context, *Recorder) (any, error) { return nil, nil })
	var pr *problem.Problem
	if !errors.As(err, &pr) || pr.Slug != problem.JobRunning || pr.JobID != j.ID {
		t.Fatalf("a second lock job: %v", err)
	}
	if _, err := m.Cancel(j.ID); !problem.Is(err, problem.JobNotCancellable) {
		t.Fatalf("an apply is not cancellable: %v", err)
	}
	close(block)
	if err := m.Wait(context.Background(), j.ID); err != nil {
		t.Fatal(err)
	}
	c, err := m.Start(context.Background(), Spec{Kind: KindSupportBundle, Cancellable: true},
		func(ctx context.Context, r *Recorder) (any, error) { <-ctx.Done(); return nil, ctx.Err() })
	if err != nil {
		t.Fatal(err)
	}
	if _, err := RequestCancel(p, c.ID); err != nil {
		t.Fatal(err)
	}
	if err := m.Wait(context.Background(), c.ID); err != nil {
		t.Fatal(err)
	}
	if got, _ := Load(p, c.ID); got.State != StateCancelled {
		t.Fatalf("a cancel file stops the job: %s", got.State)
	}
}

func TestEventCap(t *testing.T) {
	p := paths.New(t.TempDir())
	rec, err := Create(CreateOptions{Paths: p, Kind: KindFEBuild})
	if err != nil {
		t.Fatal(err)
	}
	end := rec.Step("ensure-fe: build")
	for range MaxEvents + 50 {
		rec.Log("stdout", "line")
	}
	end(nil)
	j := rec.Finish(nil, nil)
	evs, _ := Events(p, j.ID, MaxEvents, 100)
	if len(evs) != 3 || evs[0].Event != "step" || !strings.Contains(string(evs[1].Data), "dropped 51 log lines") || evs[2].Event != "done" {
		t.Fatalf("the tail of a long job: %d events", len(evs))
	}
}
