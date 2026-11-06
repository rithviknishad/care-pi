package jobs

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/rithviknishad/care-pi/agent/internal/paths"
	"github.com/rithviknishad/care-pi/agent/internal/problem"
)

func newRec(t *testing.T, p paths.Paths, kind string, at time.Time) *Recorder {
	t.Helper()
	rec, err := Create(CreateOptions{Paths: p, Kind: kind, Source: SourceConsole, Reason: "private reason", Now: func() time.Time { return at }})
	if err != nil {
		t.Fatal(err)
	}
	return rec
}

// TestCursor: a cursor reads each line once, keeps a line without its line break for the next call, and gives a done
// event only for a file without one.
func TestCursor(t *testing.T) {
	p := paths.New(t.TempDir())
	rec := newRec(t, p, KindApply, time.Date(2026, 11, 6, 10, 0, 0, 0, time.UTC))
	end := rec.Step("write-files: 1 file(s)")
	rec.Log("agent", "one")
	c := NewCursor(p, rec.ID(), 0)
	evs, err := c.Next(100)
	if err != nil || len(evs) != 2 || c.Last != 2 {
		t.Fatalf("first read %+v %v last %d", evs, err, c.Last)
	}
	// A write in progress: the half line waits, then the whole line comes once.
	f, err := os.OpenFile(eventsPath(p, rec.ID()), os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	line, _ := json.Marshal(Event{ID: 3, Event: "log", Data: json.RawMessage(`{"time":"2026-11-06T10:00:00Z","step_index":0,"stream":"agent","line":"two"}`)})
	_, _ = f.Write(line[:10])
	if evs, _ := c.Next(100); len(evs) != 0 {
		t.Fatalf("a half line gave %+v", evs)
	}
	_, _ = f.Write(append(line[10:], '\n'))
	_ = f.Close()
	if evs, _ := c.Next(100); len(evs) != 1 || evs[0].ID != 3 {
		t.Fatalf("the whole line gave %+v", evs)
	}
	if evs, _ := c.Next(100); len(evs) != 0 {
		t.Fatalf("a second read gave %+v", evs)
	}
	j, _ := Load(p, rec.ID())
	if _, ok := c.Done(j, 0); ok {
		t.Fatal("done for a running job")
	}
	rec.last = 3 // the line that the test wrote
	end(nil)
	rec.Finish(nil, nil)
	evs, _ = c.Next(1)
	if len(evs) != 1 || evs[0].Event != "step" {
		t.Fatalf("max 1 gave %+v", evs)
	}
	evs, _ = c.Next(100)
	if len(evs) != 1 || evs[0].Event != "done" {
		t.Fatalf("done %+v", evs)
	}
	j, _ = Load(p, rec.ID())
	if _, ok := c.Done(j, 0); ok {
		t.Fatal("a second done for a file with done")
	}
	// A cursor after an id skips the events before it.
	c2 := NewCursor(p, rec.ID(), 3)
	if evs, _ := c2.Next(100); len(evs) != 2 || evs[0].ID != 4 {
		t.Fatalf("after 3 %+v", evs)
	}
	// No event file (Phase 1): the done event gets the next id.
	if err := os.Remove(eventsPath(p, rec.ID())); err != nil {
		t.Fatal(err)
	}
	c3 := NewCursor(p, rec.ID(), 0)
	if evs, err := c3.Next(100); len(evs) != 0 || err != nil {
		t.Fatalf("no file %+v %v", evs, err)
	}
	if d, ok := c3.Done(j, 0); !ok || d.Event != "done" || d.ID != j.LastEventID+1 {
		t.Fatalf("done without a file %+v %v", d, ok)
	}
	if _, ok := c3.Done(j, 999); ok {
		t.Fatal("a done before after")
	}
}

// TestTail: the last n log lines up to an event id, oldest first, and the count of each log line.
func TestTail(t *testing.T) {
	p := paths.New(t.TempDir())
	rec := newRec(t, p, KindFEBuild, time.Date(2026, 11, 6, 10, 0, 0, 0, time.UTC))
	end := rec.Step("ensure-fe: build")
	for i := range 250 {
		rec.Log("stdout", fmt.Sprintf("line %d", i+1))
	}
	end(nil)
	j := rec.Finish(nil, nil)
	tail, err := Tail(p, j.ID, j.LastEventID, MaxLogTail)
	if err != nil || len(tail.Lines) != MaxLogTail || tail.Total != 250 || tail.Lines[0].Line != "line 51" || tail.Lines[199].Line != "line 250" {
		t.Fatalf("tail %d lines, total %d, first %+v, %v", len(tail.Lines), tail.Total, tail.Lines[0], err)
	}
	// Up to event 11: the step and the first 10 lines.
	tail, _ = Tail(p, j.ID, 11, MaxLogTail)
	if len(tail.Lines) != 10 || tail.Total != 10 || tail.Lines[9].Line != "line 10" {
		t.Fatalf("tail up to 11: %+v", tail)
	}
	tail, _ = Tail(p, j.ID, j.LastEventID, 3)
	if len(tail.Lines) != 3 || tail.Lines[0].Line != "line 248" {
		t.Fatalf("tail of 3: %+v", tail)
	}
	if tail, err := Tail(p, "20991231T000000.000Z-apply", 10, 5); err != nil || tail.Total != 0 || tail.Lines == nil {
		t.Fatalf("no file: %+v %v", tail, err)
	}
	if _, err := Tail(p, "../x", 10, 5); !errors.Is(err, ErrNotFound) {
		t.Fatalf("a bad id: %v", err)
	}
}

// TestPublicEvent: the public form has no log, progress, detail, reason, source, or result.
func TestPublicEvent(t *testing.T) {
	p := paths.New(t.TempDir())
	rec := newRec(t, p, KindApply, time.Date(2026, 11, 6, 10, 0, 0, 0, time.UTC))
	end := rec.Step("restart: care-api.service")
	rec.Log("agent", "private log line")
	rec.Progress(1, nil, "items", "private progress")
	rec.Report(problem.New(problem.Internal, "private report detail"))
	end(errors.New("private step error"))
	rec.Finish(problem.New(problem.MigrationFailed, "private job detail"), nil)
	evs, err := Events(p, rec.ID(), 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	var kinds []string
	for _, e := range evs {
		pe, ok := PublicEvent(e)
		if !ok {
			if e.Event != "log" && e.Event != "progress" {
				t.Errorf("no public form of %s", e.Event)
			}
			continue
		}
		if pe.ID != e.ID || pe.Event != e.Event {
			t.Errorf("public event %d %s from %d %s", pe.ID, pe.Event, e.ID, e.Event)
		}
		kinds = append(kinds, pe.Event)
		if s := string(pe.Data); strings.Contains(s, "private") || strings.Contains(s, `"result"`) || strings.Contains(s, `"source"`) {
			t.Errorf("public %s: %s", pe.Event, s)
		}
	}
	if strings.Join(kinds, ",") != "step,error,step,done" {
		t.Fatalf("public kinds %v", kinds)
	}
	var d PublicJob
	_ = json.Unmarshal(mustPublic(t, evs[len(evs)-1]).Data, &d)
	if d.State != StateFailed || d.Error == nil || d.Error.Title != "A migration failed" || d.Error.Type != problem.Base+problem.MigrationFailed || len(d.Steps) != 1 {
		t.Fatalf("public job %+v", d)
	}
}

func mustPublic(t *testing.T, e Event) Event {
	t.Helper()
	pe, ok := PublicEvent(e)
	if !ok {
		t.Fatalf("no public form of %+v", e)
	}
	return pe
}

// TestPublicStepName: the public name of a step is its action key, else the first word of its name before ":".
func TestPublicStepName(t *testing.T) {
	key := func(k string) *string { return &k }
	for _, c := range []struct {
		step Step
		want string
	}{
		{Step{Name: "create-care-superuser: dr-asha", Action: key("create-care-superuser")}, "create-care-superuser"},
		{Step{Name: "restart: care-api.service", Action: key("restart")}, "restart"},
		{Step{Name: "daemon-reload", Action: key("daemon-reload")}, "daemon-reload"},
		{Step{Name: "scale-down: solo, care-worker stopped"}, "scale-down"},
		{Step{Name: "an old step name"}, "an"},
		{Step{Name: ""}, ""},
	} {
		if got := PublicStepName(c.step); got != c.want {
			t.Errorf("PublicStepName(%q) = %q, want %q", c.step.Name, got, c.want)
		}
	}
}

// TestStateGroups: the filter values active and recent of GET /jobs.
func TestStateGroups(t *testing.T) {
	for _, c := range []struct {
		filter, state string
		want          bool
	}{
		{"", StateFailed, true}, {StateRunning, StateRunning, true}, {StateRunning, StateQueued, false},
		{StateActive, StateQueued, true}, {StateActive, StateRunning, true}, {StateActive, StateSucceeded, false},
		{StateRecent, StateSucceeded, true}, {StateRecent, StateFailed, true}, {StateRecent, StateCancelled, true},
		{StateRecent, StateRunning, false},
	} {
		if got := StateMatches(c.filter, c.state); got != c.want {
			t.Errorf("StateMatches(%q, %q) = %v", c.filter, c.state, got)
		}
	}
	p := paths.New(t.TempDir())
	at := time.Date(2026, 11, 6, 10, 0, 0, 0, time.UTC)
	newRec(t, p, KindApply, at).Finish(nil, nil)
	running := newRec(t, p, KindSupportBundle, at.Add(time.Second))
	if got := Recent(p, 5); len(got) != 2 || got[0].ID != running.ID() {
		t.Fatalf("recent %+v", got)
	}
	if got, _ := List(p, Filter{State: StateActive}); len(got) != 1 || got[0].ID != running.ID() {
		t.Fatalf("active %+v", got)
	}
	if got, _ := List(p, Filter{State: StateRecent}); len(got) != 1 || got[0].Kind != KindApply {
		t.Fatalf("recent %+v", got)
	}
	running.Finish(nil, nil)
}

// TestPlanSteps: a job without a plan has no step count (null); PlanSteps records the steps that started and the
// remaining ones, the count stays while the planned steps run, and it counts a step after the plan (a rollback).
func TestPlanSteps(t *testing.T) {
	p := paths.New(t.TempDir())
	rec := newRec(t, p, KindBackupRun, time.Date(2026, 11, 6, 10, 0, 0, 0, time.UTC))
	rec.Step("snapshot: usb (db)")(nil)
	if n := rec.Job().StepCount(); n != nil {
		t.Fatalf("a job without a plan has the step count %d", *n)
	}

	rec = newRec(t, p, KindApply, time.Date(2026, 11, 6, 10, 0, 1, 0, time.UTC))
	rec.Step("resolve-lock: image digests and git refs")(nil)
	rec.PlanSteps(3)
	for i, name := range []string{"write-files: 2 file(s)", "daemon-reload", "restart: care-api.service"} {
		end := rec.Step(name)
		j, err := Load(p, rec.ID())
		if err != nil {
			t.Fatal(err)
		}
		if n := j.StepCount(); n == nil || *n != 4 || len(j.Steps) != i+2 {
			t.Fatalf("step %d of the plan: count %v, %d steps", i, n, len(j.Steps))
		}
		end(nil)
	}
	rec.Step("rollback: to care 2026.11.0")(nil)
	j := rec.Finish(nil, nil)
	if n := j.StepCount(); n == nil || *n != 5 {
		t.Fatalf("a step after the plan: count %v", n)
	}
	if raw, _ := json.Marshal(j); strings.Contains(string(raw), "planned") {
		t.Fatalf("the API form of the job has the plan: %s", raw)
	}
}
