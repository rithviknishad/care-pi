// Package jobs records and runs the long operations of care-pi (docs/api/README.md, section 6).
//
// A job is one journal file /var/lib/care-pi/state/jobs/<id>.json (the Phase 1 format with
// members added) and an event file <id>.events.jsonl next to it. The CLI and the agent write the
// same files, so the console lists the jobs of both. A cancel request is the file <id>.cancel.
package jobs

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/rithviknishad/care-pi/agent/internal/lock"
	"github.com/rithviknishad/care-pi/agent/internal/paths"
	"github.com/rithviknishad/care-pi/agent/internal/problem"
)

// Kinds that Phase 2 makes.
const (
	KindApply         = "apply"
	KindSetup         = "setup"
	KindFEBuild       = "fe-build"
	KindFEPrune       = "fe-prune"
	KindSupportBundle = "support-bundle"
)

// Sources.
const (
	SourceConsole = "console"
	SourceCLI     = "cli"
	SourceTimer   = "timer"
)

// States of a job.
const (
	StateQueued    = "queued"
	StateRunning   = "running"
	StateSucceeded = "succeeded"
	StateFailed    = "failed"
	StateCancelled = "cancelled"
)

// Step states.
const (
	StepPending   = "pending"
	StepRunning   = "running"
	StepSucceeded = "succeeded"
	StepFailed    = "failed"
	StepSkipped   = "skipped"
	StepCancelled = "cancelled"
)

// Limits of the store.
const (
	MaxEvents    = 20000
	MaxPageItems = 1000
	KeepJobs     = 100
	KeepDays     = 30
	maxLine      = 4096
)

// LockKinds hold the apply lock.
var LockKinds = map[string]bool{KindApply: true, KindSetup: true, KindFEBuild: true, KindFEPrune: true,
	"restore": true, "update": true, "rollback": true, "tls": true}

// IDPattern is the pattern of a job id.
var IDPattern = regexp.MustCompile(`^[0-9]{8}T[0-9]{6}\.[0-9]{3}Z-[a-z][a-z-]*$`)

// actionKeys are the stable keys of steps (schema JobStep.action).
var actionKeys = map[string]bool{
	"ensure-dirs": true, "set-secrets": true, "write-files": true, "delete-files": true, "daemon-reload": true,
	"enable-target": true, "pull-images": true, "full-cycle": true, "start-target": true, "restart": true,
	"reload-caddy": true, "ensure-buckets": true, "ensure-stanza": true, "ensure-fe": true, "write-lock": true,
	"validate": true, "host-check": true, "resolve-lock": true, "render": true, "plan": true, "wait-healthy": true,
	"create-care-superuser": true, "collect": true, "archive": true, "prune": true, "set-hostname": true,
}

// ActionOf returns the action key of a step name ("restart: care-api.service" gives "restart"), or "".
func ActionOf(name string) string {
	w := strings.Fields(name)
	if len(w) == 0 {
		return ""
	}
	k := strings.TrimSuffix(w[0], ":")
	if actionKeys[k] {
		return k
	}
	return ""
}

// fileDoc is the journal file. The first members are the Phase 1 format.
type fileDoc struct {
	Kind     string     `json:"kind"`
	Reason   string     `json:"reason,omitempty"`
	Started  time.Time  `json:"started"`
	Finished *time.Time `json:"finished,omitempty"`
	// Result is running, ok, failed (Phase 1), or cancelled.
	Result string     `json:"result"`
	Error  string     `json:"error,omitempty"`
	Steps  []fileStep `json:"steps"`

	Source      string           `json:"source,omitempty"`
	Created     *time.Time       `json:"created,omitempty"`
	Cancellable bool             `json:"cancellable,omitempty"`
	PID         int              `json:"pid,omitempty"`
	BootID      string           `json:"boot_id,omitempty"`
	Problem     *problem.Problem `json:"problem,omitempty"`
	Output      json.RawMessage  `json:"output,omitempty"`
	LastEventID int              `json:"last_event_id,omitempty"`
}

type fileStep struct {
	Name     string     `json:"name"`
	Started  time.Time  `json:"started"`
	Finished *time.Time `json:"finished,omitempty"`
	Error    string     `json:"error,omitempty"`
	Action   string     `json:"action,omitempty"`
	State    string     `json:"state,omitempty"`
	Detail   string     `json:"detail,omitempty"`
}

// Step is one step of a job (schema JobStep).
type Step struct {
	Index      int        `json:"index"`
	Name       string     `json:"name"`
	Action     *string    `json:"action"`
	State      string     `json:"state"`
	StartedAt  *time.Time `json:"started_at"`
	FinishedAt *time.Time `json:"finished_at"`
	Detail     *string    `json:"detail"`
}

// Job is the API form of a job (schema Job).
type Job struct {
	ID          string           `json:"id"`
	Kind        string           `json:"kind"`
	State       string           `json:"state"`
	Source      string           `json:"source"`
	Reason      *string          `json:"reason"`
	CreatedAt   time.Time        `json:"created_at"`
	StartedAt   *time.Time       `json:"started_at"`
	FinishedAt  *time.Time       `json:"finished_at"`
	Cancellable bool             `json:"cancellable"`
	Steps       []Step           `json:"steps"`
	Error       *problem.Problem `json:"error"`
	Result      json.RawMessage  `json:"result"`
	LastEventID int              `json:"last_event_id"`
}

// Done reports whether the job ended.
func (j *Job) Done() bool {
	return j.State == StateSucceeded || j.State == StateFailed || j.State == StateCancelled
}

// Summary is the short form of a job (schema JobSummary).
type Summary struct {
	ID         string     `json:"id"`
	Kind       string     `json:"kind"`
	State      string     `json:"state"`
	Source     string     `json:"source"`
	CreatedAt  time.Time  `json:"created_at"`
	StartedAt  *time.Time `json:"started_at"`
	FinishedAt *time.Time `json:"finished_at"`
	Step       *string    `json:"step"`
	ErrorTitle *string    `json:"error_title"`
}

// Summary returns the short form.
func (j *Job) Summary() Summary {
	s := Summary{ID: j.ID, Kind: j.Kind, State: j.State, Source: j.Source, CreatedAt: j.CreatedAt,
		StartedAt: j.StartedAt, FinishedAt: j.FinishedAt}
	if j.State == StateRunning {
		for i := len(j.Steps) - 1; i >= 0; i-- {
			if j.Steps[i].State == StepRunning {
				n := j.Steps[i].Name
				s.Step = &n
				break
			}
		}
	}
	if j.Error != nil {
		t := j.Error.Title
		s.ErrorTitle = &t
	}
	return s
}

// Event is one event of a job (schema JobEvent without heartbeat).
type Event struct {
	ID    int             `json:"id"`
	Event string          `json:"event"`
	Data  json.RawMessage `json:"data"`
}

// LogLine is the data of a log event (schema JobLogLine).
type LogLine struct {
	Time      time.Time `json:"time"`
	StepIndex *int      `json:"step_index"`
	Stream    string    `json:"stream"`
	Line      string    `json:"line"`
}

// Progress is the data of a progress event (schema JobProgress).
type Progress struct {
	StepIndex int      `json:"step_index"`
	Current   float64  `json:"current"`
	Total     *float64 `json:"total"`
	Unit      string   `json:"unit"`
	Message   *string  `json:"message"`
}

// ErrCancelled ends a cancelled job.
var ErrCancelled = errors.New("the job was cancelled")

// NewID returns the id of a job that starts at t.
func NewID(t time.Time, kind string) string {
	return t.UTC().Format("20060102T150405.000Z") + "-" + kind
}

func docPath(p paths.Paths, id string) string { return p.Abs(paths.JobsDir + "/" + id + ".json") }
func eventsPath(p paths.Paths, id string) string {
	return p.Abs(paths.JobsDir + "/" + id + ".events.jsonl")
}
func cancelPath(p paths.Paths, id string) string { return p.Abs(paths.JobsDir + "/" + id + ".cancel") }

// Recorder writes one job. It is safe for concurrent use.
type Recorder struct {
	mu      sync.Mutex
	p       paths.Paths
	id      string
	doc     fileDoc
	now     func() time.Time
	events  *os.File
	last    int
	dropped int
	cur     int
	notify  func(id string)
	closed  bool
}

// CreateOptions describes a new job.
type CreateOptions struct {
	Paths       paths.Paths
	Kind        string
	Source      string
	Reason      string
	Cancellable bool
	Now         func() time.Time
	// Notify gets the job id after each event. The agent wakes its streams with it.
	Notify func(id string)
}

// Create writes the journal of a new running job.
func Create(o CreateOptions) (*Recorder, error) {
	if o.Now == nil {
		o.Now = time.Now
	}
	now := func() time.Time { return o.Now().UTC().Truncate(time.Millisecond) }
	dir := o.Paths.Abs(paths.JobsDir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("cannot create %s: %w", paths.JobsDir, err)
	}
	prune(o.Paths, now())
	t := now()
	id := NewID(t, o.Kind)
	for {
		f, err := os.OpenFile(docPath(o.Paths, id), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err == nil {
			_ = f.Close()
			break
		}
		if !os.IsExist(err) {
			return nil, fmt.Errorf("cannot create the job journal: %w", err)
		}
		t = t.Add(time.Millisecond)
		id = NewID(t, o.Kind)
	}
	ev, err := os.OpenFile(eventsPath(o.Paths, id), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, fmt.Errorf("cannot create the job events: %w", err)
	}
	created := t
	r := &Recorder{p: o.Paths, id: id, now: now, events: ev, cur: -1, notify: o.Notify}
	r.doc = fileDoc{Kind: o.Kind, Reason: o.Reason, Started: t, Result: "running", Steps: []fileStep{},
		Source: o.Source, Created: &created, Cancellable: o.Cancellable, PID: os.Getpid(), BootID: bootID(o.Paths)}
	r.save()
	return r, nil
}

// ID returns the job id.
func (r *Recorder) ID() string { return r.id }

// Job returns the API form of the job now.
func (r *Recorder) Job() *Job {
	r.mu.Lock()
	defer r.mu.Unlock()
	j := toJob(r.id, r.doc, false)
	return &j
}

func (r *Recorder) save() {
	r.doc.LastEventID = r.last
	data, err := json.MarshalIndent(r.doc, "", "  ")
	if err != nil {
		return
	}
	_ = lock.WriteFileAtomic(docPath(r.p, r.id), data, 0o600)
}

// emit appends an event. The caller holds r.mu.
func (r *Recorder) emit(kind string, data any) {
	if r.closed {
		return
	}
	if kind == "log" && r.last >= MaxEvents {
		r.dropped++
		return
	}
	r.write(kind, data)
}

// write appends an event without the limit. The caller holds r.mu.
func (r *Recorder) write(kind string, data any) {
	raw, err := json.Marshal(data)
	if err != nil {
		return
	}
	r.last++
	line, _ := json.Marshal(Event{ID: r.last, Event: kind, Data: raw})
	_, _ = r.events.Write(append(line, '\n'))
	if r.notify != nil {
		r.notify(r.id)
	}
}

func (r *Recorder) stepView(i int) Step {
	return toStep(i, r.doc.Steps[i], false)
}

// StepAction starts a step with an explicit action key. The returned function ends it.
func (r *Recorder) StepAction(name, action string) func(error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.doc.Steps = append(r.doc.Steps, fileStep{Name: name, Started: r.now(), Action: action, State: StepRunning})
	i := len(r.doc.Steps) - 1
	r.cur = i
	r.emit("step", r.stepView(i))
	r.save()
	return func(err error) { r.endStep(i, err) }
}

// Step starts a step. The action key comes from the name. The returned function ends it.
func (r *Recorder) Step(name string) func(error) { return r.StepAction(name, ActionOf(name)) }

func (r *Recorder) endStep(i int, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	s := &r.doc.Steps[i]
	if s.Finished != nil {
		return
	}
	t := r.now()
	s.Finished = &t
	s.State = StepSucceeded
	switch {
	case errors.Is(err, context.Canceled), errors.Is(err, ErrCancelled):
		s.State, s.Detail = StepCancelled, "cancelled"
	case err != nil:
		s.State, s.Error, s.Detail = StepFailed, cutLine(err.Error()), cutLine(err.Error())
	}
	if r.cur == i {
		r.cur = -1
	}
	r.emit("step", r.stepView(i))
	r.save()
}

// SetDetail sets the short result of the step that runs now.
func (r *Recorder) SetDetail(detail string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.cur >= 0 {
		r.doc.Steps[r.cur].Detail = cutLine(detail)
	}
}

func cutLine(s string) string {
	s = strings.TrimRight(s, "\r\n")
	if len(s) > maxLine {
		return s[:maxLine]
	}
	return s
}

// Log adds one output line of the given stream ("agent", "stdout", or "stderr").
func (r *Recorder) Log(stream, line string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var idx *int
	if r.cur >= 0 {
		i := r.cur
		idx = &i
	}
	r.emit("log", LogLine{Time: r.now(), StepIndex: idx, Stream: stream, Line: cutLine(line)})
}

// Logf adds one agent line.
func (r *Recorder) Logf(format string, a ...any) { r.Log("agent", fmt.Sprintf(format, a...)) }

// Progress adds a progress event of the step that runs now.
func (r *Recorder) Progress(current float64, total *float64, unit, message string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.cur < 0 {
		return
	}
	var msg *string
	if message != "" {
		msg = &message
	}
	r.emit("progress", Progress{StepIndex: r.cur, Current: current, Total: total, Unit: unit, Message: msg})
}

// Report adds an error event. The job continues.
func (r *Recorder) Report(p *problem.Problem) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if p.Instance == "" {
		p.Instance = "/api/v1/jobs/" + r.id
	}
	r.emit("error", p)
}

// Writer returns a writer that adds each line as an agent log line.
func (r *Recorder) Writer() io.Writer { return &lineWriter{fn: func(l string) { r.Log("agent", l) }} }

type lineWriter struct {
	mu  sync.Mutex
	buf []byte
	fn  func(string)
}

func (w *lineWriter) Write(b []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.buf = append(w.buf, b...)
	for {
		i := bytes.IndexByte(w.buf, '\n')
		if i < 0 {
			break
		}
		w.fn(strings.TrimRight(string(w.buf[:i]), "\r"))
		w.buf = w.buf[i+1:]
	}
	return len(b), nil
}

// CancelRequested reports whether a cancel file exists for the job.
func (r *Recorder) CancelRequested() bool {
	_, err := os.Stat(cancelPath(r.p, r.id))
	return err == nil
}

// Finish ends the job. err nil: succeeded with the result. A cancel error: cancelled. Else failed.
func (r *Recorder) Finish(err error, result any) *Job {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		j := toJob(r.id, r.doc, false)
		return &j
	}
	t := r.now()
	for i := range r.doc.Steps {
		if r.doc.Steps[i].Finished == nil {
			r.doc.Steps[i].Finished = &t
			r.doc.Steps[i].State = StepCancelled
			if err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, ErrCancelled) {
				r.doc.Steps[i].State = StepFailed
			}
		}
	}
	r.doc.Finished = &t
	switch {
	case err == nil:
		r.doc.Result = "ok"
		if result != nil {
			if raw, e := json.Marshal(result); e == nil {
				r.doc.Output = raw
			}
		}
	case errors.Is(err, context.Canceled), errors.Is(err, ErrCancelled):
		r.doc.Result = "cancelled"
	default:
		r.doc.Result = "failed"
		r.doc.Error = cutLine(err.Error())
		var p *problem.Problem
		if errors.As(err, &p) {
			cp := *p
			r.doc.Problem = &cp
		} else {
			r.doc.Problem = problem.New(problem.Internal, cutLine(err.Error()))
		}
		r.doc.Problem.Instance = "/api/v1/jobs/" + r.id
	}
	if r.dropped > 0 {
		dropped := r.dropped
		r.dropped = 0
		r.write("log", LogLine{Time: t, Stream: "agent", Line: fmt.Sprintf("the agent dropped %d log lines (the limit is %d events)", dropped, MaxEvents)})
	}
	r.last++
	r.doc.LastEventID = r.last
	j := toJob(r.id, r.doc, false)
	raw, _ := json.Marshal(j)
	line, _ := json.Marshal(Event{ID: r.last, Event: "done", Data: raw})
	_, _ = r.events.Write(append(line, '\n'))
	_ = r.events.Close()
	r.closed = true
	r.save()
	_ = os.Remove(cancelPath(r.p, r.id))
	if r.notify != nil {
		r.notify(r.id)
	}
	return &j
}

// toJob maps a journal to the API form. dead tells that no process runs the job any more.
func toJob(id string, d fileDoc, dead bool) Job {
	j := Job{ID: id, Kind: d.Kind, Source: d.Source, CreatedAt: d.Started, Steps: []Step{}, LastEventID: d.LastEventID}
	if j.Source == "" {
		j.Source = SourceCLI
	}
	if d.Created != nil {
		j.CreatedAt = *d.Created
	}
	if d.Reason != "" {
		r := d.Reason
		j.Reason = &r
	}
	if !d.Started.IsZero() {
		s := d.Started
		j.StartedAt = &s
	}
	j.FinishedAt = d.Finished
	switch d.Result {
	case "ok":
		j.State = StateSucceeded
		if len(d.Output) > 0 {
			j.Result = d.Output
		}
	case "failed":
		j.State = StateFailed
	case "cancelled":
		j.State = StateCancelled
	case "queued":
		j.State = StateQueued
	default:
		j.State = StateRunning
		if dead {
			j.State = StateFailed
		}
	}
	if j.State == StateFailed {
		switch {
		case d.Problem != nil:
			p := *d.Problem
			j.Error = &p
		case d.Error != "":
			j.Error = problem.New(problem.Internal, d.Error)
		default:
			j.Error = problem.New(problem.Internal, "interrupted")
		}
		if j.Error.Instance == "" {
			j.Error.Instance = "/api/v1/jobs/" + id
		}
	}
	for i, s := range d.Steps {
		j.Steps = append(j.Steps, toStep(i, s, dead && d.Result == "running"))
	}
	j.Cancellable = d.Cancellable && j.State == StateRunning
	return j
}

func toStep(i int, s fileStep, interrupted bool) Step {
	st := Step{Index: i, Name: s.Name, State: s.State}
	a := s.Action
	if a == "" {
		a = ActionOf(s.Name)
	}
	if a != "" {
		st.Action = &a
	}
	if !s.Started.IsZero() {
		t := s.Started
		st.StartedAt = &t
	}
	st.FinishedAt = s.Finished
	if st.State == "" {
		switch {
		case s.Finished == nil:
			st.State = StepRunning
		case s.Error != "":
			st.State = StepFailed
		default:
			st.State = StepSucceeded
		}
	}
	if st.State == StepRunning && interrupted {
		st.State = StepFailed
	}
	detail := s.Detail
	if detail == "" {
		detail = s.Error
	}
	if detail != "" {
		st.Detail = &detail
	}
	return st
}

// readDoc reads a journal file.
func readDoc(p paths.Paths, id string) (fileDoc, error) {
	var d fileDoc
	data, err := os.ReadFile(docPath(p, id))
	if err != nil {
		return d, err
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return d, fmt.Errorf("the journal of job %s is empty", id)
	}
	err = json.Unmarshal(data, &d)
	return d, err
}

// countEvents returns the id of the last event in the event file.
func countEvents(p paths.Paths, id string) int {
	f, err := os.Open(eventsPath(p, id))
	if err != nil {
		return 0
	}
	defer func() { _ = f.Close() }()
	last := 0
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for sc.Scan() {
		var e struct {
			ID int `json:"id"`
		}
		if json.Unmarshal(sc.Bytes(), &e) == nil && e.ID > last {
			last = e.ID
		}
	}
	return last
}
