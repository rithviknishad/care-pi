package jobs

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/rithviknishad/care-pi/agent/internal/applock"
	"github.com/rithviknishad/care-pi/agent/internal/paths"
	"github.com/rithviknishad/care-pi/agent/internal/problem"
)

// ErrNotFound tells that no job has the id.
var ErrNotFound = errors.New("the job does not exist")

func bootID(p paths.Paths) string {
	data, err := os.ReadFile(p.Abs("/proc/sys/kernel/random/boot_id"))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// alive reports whether a process still runs the job of a running journal.
func alive(p paths.Paths, d fileDoc) bool {
	if d.PID > 0 {
		if b := bootID(p); d.BootID != "" && b != "" && b != d.BootID {
			return false
		}
		err := syscall.Kill(d.PID, 0)
		return err == nil || errors.Is(err, syscall.EPERM)
	}
	// A Phase 1 journal has no pid: the job runs while a process holds the apply lock.
	return applock.Held(p)
}

// Load returns the job with the id.
func Load(p paths.Paths, id string) (*Job, error) {
	if !IDPattern.MatchString(id) {
		return nil, ErrNotFound
	}
	d, err := readDoc(p, id)
	if os.IsNotExist(err) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	dead := d.Result == "running" && !alive(p, d)
	j := toJob(id, d, dead)
	if j.State == StateRunning {
		if n := countEvents(p, id); n > j.LastEventID {
			j.LastEventID = n
		}
	}
	return &j, nil
}

// ids returns the job ids in the journal directory, newest first.
func ids(p paths.Paths) []string {
	ents, err := os.ReadDir(p.Abs(paths.JobsDir))
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range ents {
		id, ok := strings.CutSuffix(e.Name(), ".json")
		if ok && !e.IsDir() && IDPattern.MatchString(id) {
			out = append(out, id)
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(out)))
	return out
}

// Filter selects jobs for List.
type Filter struct {
	Kind   string
	State  string
	Limit  int
	Before string
}

// List returns one page of jobs, newest first, and the id for the next page ("" on the last page).
func List(p paths.Paths, f Filter) ([]Summary, string) {
	if f.Limit <= 0 {
		f.Limit = 20
	}
	out := []Summary{}
	next := ""
	for _, id := range ids(p) {
		if f.Before != "" && id >= f.Before {
			continue
		}
		j, err := Load(p, id)
		if err != nil {
			continue
		}
		if (f.Kind != "" && j.Kind != f.Kind) || (f.State != "" && j.State != f.State) {
			continue
		}
		if len(out) == f.Limit {
			next = out[len(out)-1].ID
			break
		}
		out = append(out, j.Summary())
	}
	return out, next
}

// Latest returns the newest job that matches keep, or nil.
func Latest(p paths.Paths, keep func(*Job) bool) *Job {
	for _, id := range ids(p) {
		j, err := Load(p, id)
		if err == nil && keep(j) {
			return j
		}
	}
	return nil
}

// RunningLockJob returns the id of the newest running job that holds the apply lock, or "".
func RunningLockJob(p paths.Paths) string {
	if j := Latest(p, func(j *Job) bool { return j.State == StateRunning && LockKinds[j.Kind] }); j != nil {
		return j.ID
	}
	return ""
}

// Events returns up to max events with an id greater than after.
func Events(p paths.Paths, id string, after, max int) ([]Event, error) {
	if !IDPattern.MatchString(id) {
		return nil, ErrNotFound
	}
	f, err := os.Open(eventsPath(p, id))
	if os.IsNotExist(err) {
		if _, err := os.Stat(docPath(p, id)); err != nil {
			return nil, ErrNotFound
		}
		return []Event{}, nil
	}
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	out := []Event{}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 || line[len(line)-1] != '}' {
			continue
		}
		var e Event
		if json.Unmarshal(line, &e) != nil || e.ID <= after {
			continue
		}
		out = append(out, e)
		if len(out) >= max {
			break
		}
	}
	return out, nil
}

// DoneEvent returns a done event for a journal without one (a Phase 1 journal or an interrupted job).
func DoneEvent(j *Job) Event {
	raw, _ := json.Marshal(j)
	return Event{ID: j.LastEventID + 1, Event: "done", Data: raw}
}

// RequestCancel writes the cancel file of a running cancellable job.
func RequestCancel(p paths.Paths, id string) (*Job, error) {
	j, err := Load(p, id)
	if err != nil {
		return nil, err
	}
	if !j.Cancellable {
		return j, problem.Newf(problem.JobNotCancellable, "Job %s cannot be cancelled: its state is %s and only fe-build and support-bundle jobs that run are cancellable.", id, j.State)
	}
	if err := os.WriteFile(cancelPath(p, id), []byte("cancel\n"), 0o600); err != nil {
		return nil, fmt.Errorf("cannot write the cancel request: %w", err)
	}
	return j, nil
}

// prune keeps the last KeepJobs jobs and each job of the last KeepDays days.
func prune(p paths.Paths, now time.Time) {
	cut := now.Add(-KeepDays * 24 * time.Hour).UTC().Format("20060102T150405.000Z")
	for i, id := range ids(p) {
		if i < KeepJobs || id >= cut {
			continue
		}
		d, err := readDoc(p, id)
		if err == nil && d.Result == "running" && alive(p, d) {
			continue
		}
		_ = os.Remove(docPath(p, id))
		_ = os.Remove(eventsPath(p, id))
		_ = os.Remove(cancelPath(p, id))
	}
}

// MarkInterrupted ends each running journal that no process runs: failed, "interrupted".
func MarkInterrupted(p paths.Paths, now time.Time) []string {
	var out []string
	for _, id := range ids(p) {
		d, err := readDoc(p, id)
		if err != nil || d.Result != "running" || alive(p, d) {
			continue
		}
		t := now.UTC().Truncate(time.Millisecond)
		for i := range d.Steps {
			if d.Steps[i].Finished == nil {
				d.Steps[i].Finished = &t
				d.Steps[i].State = StepFailed
				d.Steps[i].Detail = "interrupted"
			}
		}
		d.Result, d.Error, d.Finished = "failed", "interrupted", &t
		d.Problem = problem.New(problem.Internal, "interrupted: the process of the job stopped before the end")
		d.Problem.Instance = "/api/v1/jobs/" + id
		if n := countEvents(p, id); n > d.LastEventID {
			d.LastEventID = n
		}
		d.LastEventID++
		j := toJob(id, d, false)
		raw, _ := json.Marshal(j)
		line, _ := json.Marshal(Event{ID: d.LastEventID, Event: "done", Data: raw})
		if f, err := os.OpenFile(eventsPath(p, id), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600); err == nil {
			_, _ = f.Write(append(line, '\n'))
			_ = f.Close()
		}
		data, _ := json.MarshalIndent(d, "", "  ")
		_ = os.WriteFile(docPath(p, id), data, 0o600)
		out = append(out, id)
	}
	return out
}

// Spec describes a job for Manager.Start.
type Spec struct {
	Kind        string
	Source      string
	Reason      string
	Cancellable bool
	// Lock makes the job hold the apply lock for its run.
	Lock bool
	// OnStart runs with the id after the journal exists and before the job body starts.
	OnStart func(id string)
}

// Func is the body of a job. It returns the result of a job that succeeded.
type Func func(ctx context.Context, r *Recorder) (any, error)

// Manager runs jobs in the agent process.
type Manager struct {
	Paths paths.Paths
	Now   func() time.Time
	Log   *slog.Logger
	// LockWait is the longest wait for the apply lock (5 s).
	LockWait time.Duration
	// CancelPoll is the interval of the check for a cancel file.
	CancelPoll time.Duration

	mu      sync.Mutex
	running map[string]*run
	waiters map[string]chan struct{}
}

type run struct {
	cancel context.CancelFunc
	done   chan struct{}
}

func (m *Manager) init() {
	if m.running == nil {
		m.running = map[string]*run{}
		m.waiters = map[string]chan struct{}{}
	}
	if m.Now == nil {
		m.Now = time.Now
	}
	if m.Log == nil {
		m.Log = slog.New(slog.DiscardHandler)
	}
	if m.LockWait == 0 {
		m.LockWait = 5 * time.Second
	}
	if m.CancelPoll == 0 {
		m.CancelPoll = time.Second
	}
}

// Wake returns a channel that closes at the next event of the job.
func (m *Manager) Wake(id string) <-chan struct{} {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.init()
	ch, ok := m.waiters[id]
	if !ok {
		ch = make(chan struct{})
		m.waiters[id] = ch
	}
	return ch
}

func (m *Manager) notify(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if ch, ok := m.waiters[id]; ok {
		close(ch)
		delete(m.waiters, id)
	}
}

// AcquireLock takes the apply lock within LockWait. A timeout gives job-running.
func (m *Manager) AcquireLock(ctx context.Context) (func(), error) {
	m.mu.Lock()
	m.init()
	wait := m.LockWait
	m.mu.Unlock()
	release, err := applock.Acquire(ctx, m.Paths, wait, nil)
	if errors.Is(err, applock.ErrTimeout) {
		return nil, problem.Running(RunningLockJob(m.Paths))
	}
	return release, err
}

// Start runs fn as a new job in the background and returns the job.
func (m *Manager) Start(ctx context.Context, s Spec, fn Func) (*Job, error) {
	m.mu.Lock()
	m.init()
	m.mu.Unlock()
	release := func() {}
	if s.Lock {
		r, err := m.AcquireLock(ctx)
		if err != nil {
			return nil, err
		}
		release = r
	}
	rec, err := Create(CreateOptions{Paths: m.Paths, Kind: s.Kind, Source: s.Source, Reason: s.Reason,
		Cancellable: s.Cancellable, Now: m.Now, Notify: m.notify})
	if err != nil {
		release()
		return nil, err
	}
	jctx, cancel := context.WithCancel(context.Background())
	rn := &run{cancel: cancel, done: make(chan struct{})}
	m.mu.Lock()
	m.running[rec.ID()] = rn
	m.mu.Unlock()
	m.Log.Info("job started", "id", rec.ID(), "kind", s.Kind, "source", s.Source)
	if s.OnStart != nil {
		s.OnStart(rec.ID())
	}
	first := rec.Job()
	if s.Cancellable {
		go func() {
			t := time.NewTicker(m.CancelPoll)
			defer t.Stop()
			for {
				select {
				case <-rn.done:
					return
				case <-t.C:
					if rec.CancelRequested() {
						cancel()
						return
					}
				}
			}
		}()
	}
	go func() {
		defer func() {
			release()
			cancel()
			m.mu.Lock()
			delete(m.running, rec.ID())
			m.mu.Unlock()
			close(rn.done)
		}()
		var result any
		var err error
		func() {
			defer func() {
				if v := recover(); v != nil {
					err = fmt.Errorf("the job stopped with a panic: %v", v)
				}
			}()
			result, err = fn(jctx, rec)
		}()
		if err == nil && jctx.Err() != nil && s.Cancellable {
			err = ErrCancelled
		}
		j := rec.Finish(err, result)
		m.Log.Info("job ended", "id", j.ID, "kind", j.Kind, "state", j.State)
	}()
	return first, nil
}

// Cancel asks a running cancellable job to stop.
func (m *Manager) Cancel(id string) (*Job, error) {
	j, err := RequestCancel(m.Paths, id)
	if err != nil {
		return j, err
	}
	m.mu.Lock()
	m.init()
	rn := m.running[id]
	m.mu.Unlock()
	if rn != nil {
		rn.cancel()
	}
	return j, nil
}

// Wait blocks until the job of this manager ends or ctx ends.
func (m *Manager) Wait(ctx context.Context, id string) error {
	m.mu.Lock()
	m.init()
	rn := m.running[id]
	m.mu.Unlock()
	if rn == nil {
		return nil
	}
	select {
	case <-rn.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Shutdown cancels each job of this manager and waits until they end or ctx ends.
func (m *Manager) Shutdown(ctx context.Context) {
	m.mu.Lock()
	m.init()
	var runs []*run
	for _, r := range m.running {
		runs = append(runs, r)
	}
	m.mu.Unlock()
	for _, r := range runs {
		select {
		case <-r.done:
		case <-ctx.Done():
			return
		}
	}
}
