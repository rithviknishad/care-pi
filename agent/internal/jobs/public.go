package jobs

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"os"
	"strings"
	"time"

	"github.com/rithviknishad/care-pi/agent/internal/paths"
	"github.com/rithviknishad/care-pi/agent/internal/problem"
)

// MaxLogTail is the largest number of lines in Job.log_tail (contract 0.9.0).
const MaxLogTail = 200

// LogTail is the last log lines of a job (schema JobLogTail).
type LogTail struct {
	// Lines are the last lines, oldest first.
	Lines []LogLine `json:"lines"`
	// Total is the number of log lines of the job up to the event id of the tail.
	Total int `json:"total"`
}

// Tail returns the last n log lines of the job with an event id of upTo or less, oldest first.
func Tail(p paths.Paths, id string, upTo, n int) (LogTail, error) {
	out := LogTail{Lines: []LogLine{}}
	if !IDPattern.MatchString(id) {
		return out, ErrNotFound
	}
	f, err := os.Open(eventsPath(p, id))
	if os.IsNotExist(err) {
		return out, nil
	}
	if err != nil {
		return out, err
	}
	defer func() { _ = f.Close() }()
	ring := make([]json.RawMessage, 0, n)
	next := 0
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for sc.Scan() {
		var e Event
		if json.Unmarshal(sc.Bytes(), &e) != nil || e.Event != "log" || e.ID > upTo {
			continue
		}
		out.Total++
		if n <= 0 {
			continue
		}
		data := append(json.RawMessage(nil), e.Data...)
		if len(ring) < n {
			ring = append(ring, data)
			continue
		}
		ring[next] = data
		next = (next + 1) % n
	}
	if err := sc.Err(); err != nil {
		return out, err
	}
	for i := range ring {
		var l LogLine
		if json.Unmarshal(ring[(next+i)%len(ring)], &l) == nil {
			out.Lines = append(out.Lines, l)
		}
	}
	return out, nil
}

// Cursor reads the event file of a job from a byte offset, so a long stream reads each line only once.
type Cursor struct {
	p   paths.Paths
	id  string
	off int64
	// Last is the id of the last event that Next read. Next returns only the events after it.
	Last int
	// max is the largest event id in the lines that Next read, also before Last. done tells that one was done.
	max  int
	done bool
}

// NewCursor returns a cursor at the start of the event file. It returns the events after the event id after.
func NewCursor(p paths.Paths, id string, after int) *Cursor {
	return &Cursor{p: p, id: id, Last: after}
}

// Next returns up to n events after Last, oldest first. A line without its line break (a write in progress)
// stays for the next call.
func (c *Cursor) Next(n int) ([]Event, error) {
	f, err := os.Open(eventsPath(c.p, c.id))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	if _, err := f.Seek(c.off, io.SeekStart); err != nil {
		return nil, err
	}
	r := bufio.NewReaderSize(f, 64*1024)
	var out []Event
	for len(out) < n {
		line, err := r.ReadBytes('\n')
		if err != nil {
			break
		}
		c.off += int64(len(line))
		var e Event
		if json.Unmarshal(bytes.TrimSpace(line), &e) != nil {
			continue
		}
		c.max = max(c.max, e.ID)
		c.done = c.done || e.Event == "done"
		if e.ID <= c.Last {
			continue
		}
		c.Last = e.ID
		out = append(out, e)
	}
	return out, nil
}

// Done returns the done event of a job that ended when the lines that Next read have none (a journal of Phase 1, or
// a job that a crash interrupted), with an id after each event of the file. false when the file has a done event,
// or when the event is not after after.
func (c *Cursor) Done(j *Job, after int) (Event, bool) {
	if c.done || !j.Done() {
		return Event{}, false
	}
	d := DoneEvent(j)
	d.ID = max(d.ID, c.max+1)
	return d, d.ID > after
}

// PublicProblem is the problem of a job without its detail (schema PublicJobProblem): the public event stream
// tells only what failed, not why.
type PublicProblem struct {
	Type   string `json:"type"`
	Title  string `json:"title"`
	Status int    `json:"status"`
}

func publicProblem(p *problem.Problem) *PublicProblem {
	if p == nil {
		return nil
	}
	return &PublicProblem{Type: p.Type(), Title: p.Title, Status: p.Status}
}

// PublicJob is a job without its reason, result, source, and error detail (schema PublicJob).
type PublicJob struct {
	ID          string         `json:"id"`
	Kind        string         `json:"kind"`
	State       string         `json:"state"`
	CreatedAt   time.Time      `json:"created_at"`
	StartedAt   *time.Time     `json:"started_at"`
	FinishedAt  *time.Time     `json:"finished_at"`
	Steps       []Step         `json:"steps"`
	Error       *PublicProblem `json:"error"`
	LastEventID int            `json:"last_event_id"`
}

// publicStep removes the detail of a step: a failed step has the error text there.
func publicStep(s Step) Step {
	s.Detail = nil
	return s
}

// Public returns the public form of the job.
func (j *Job) Public() PublicJob {
	out := PublicJob{ID: j.ID, Kind: j.Kind, State: j.State, CreatedAt: j.CreatedAt, StartedAt: j.StartedAt,
		FinishedAt: j.FinishedAt, Steps: make([]Step, 0, len(j.Steps)), Error: publicProblem(j.Error), LastEventID: j.LastEventID}
	for _, s := range j.Steps {
		out.Steps = append(out.Steps, publicStep(s))
	}
	return out
}

// PublicEvent returns the public form of an event (docs/api/README.md section 6.3): a step without its detail, an
// error without its detail, and done with the public job. A log or progress event has no public form (false).
func PublicEvent(e Event) (Event, bool) {
	var v any
	switch e.Event {
	case "step":
		var s Step
		if json.Unmarshal(e.Data, &s) != nil {
			return Event{}, false
		}
		v = publicStep(s)
	case "error":
		var p problem.Problem
		if json.Unmarshal(e.Data, &p) != nil {
			return Event{}, false
		}
		v = publicProblem(&p)
	case "done":
		var j Job
		if json.Unmarshal(e.Data, &j) != nil {
			return Event{}, false
		}
		v = j.Public()
	default:
		return Event{}, false
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return Event{}, false
	}
	return Event{ID: e.ID, Event: e.Event, Data: raw}, true
}

// Recent returns the n newest jobs, newest first.
func Recent(p paths.Paths, n int) []Summary {
	out, _ := List(p, Filter{Limit: n})
	return out
}

// StepText returns the text of a step for a transcript: the name, and the detail of a step that has one.
func StepText(s Step) string {
	t := s.Name
	if s.Detail != nil && *s.Detail != "" && !strings.Contains(t, *s.Detail) {
		t += ": " + *s.Detail
	}
	return t
}
