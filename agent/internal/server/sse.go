package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/rithviknishad/care-pi/agent/internal/jobs"
	"github.com/rithviknishad/care-pi/agent/internal/problem"
	"github.com/rithviknishad/care-pi/agent/internal/service"
)

var lastEventRE = regexp.MustCompile(`^[0-9]{1,9}$`)

func wantsSSE(r *http.Request) bool {
	return strings.Contains(r.Header.Get("Accept"), "text/event-stream")
}

// stream is one open SSE response.
type stream struct {
	mu sync.Mutex
	w  http.ResponseWriter
	rc *http.ResponseController
}

func (s *stream) write(id, event string, data any) error {
	raw, err := json.Marshal(data)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var b strings.Builder
	if id != "" {
		b.WriteString("id: " + id + "\n")
	}
	b.WriteString("event: " + event + "\ndata: ")
	b.Write(raw)
	b.WriteString("\n\n")
	if _, err := s.w.Write([]byte(b.String())); err != nil {
		return err
	}
	return s.rc.Flush()
}

func (s *stream) heartbeat(now time.Time) error {
	return s.write("", "heartbeat", map[string]any{"time": now.UTC().Truncate(time.Second)})
}

// openStream takes a stream slot of the session and starts the SSE response.
func (s *Server) openStream(c *reqCtx) (*stream, func(), error) {
	s.mu.Lock()
	if s.streams >= s.o.MaxStreams || s.bySess[c.session.Hash] >= s.o.MaxSessionStreams {
		s.mu.Unlock()
		return nil, nil, problem.Limited(problem.RateLimited, 5, "The limit is 4 open event streams for each session and 16 in total.")
	}
	s.streams++
	s.bySess[c.session.Hash]++
	s.mu.Unlock()
	release := func() {
		s.mu.Lock()
		s.streams--
		if s.bySess[c.session.Hash]--; s.bySess[c.session.Hash] <= 0 {
			delete(s.bySess, c.session.Hash)
		}
		s.mu.Unlock()
	}
	h := c.w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-store")
	h.Set("X-Accel-Buffering", "no")
	c.w.WriteHeader(http.StatusOK)
	st := &stream{w: c.w, rc: http.NewResponseController(c.w)}
	st.mu.Lock()
	_, err := c.w.Write([]byte("retry: 3000\n\n"))
	if err == nil {
		err = st.rc.Flush()
	}
	st.mu.Unlock()
	if err != nil {
		release()
		return nil, nil, err
	}
	return st, release, nil
}

// jobEvents serves GET /jobs/{job_id}/events as SSE or as one JSON page.
func (s *Server) jobEvents(c *reqCtx) error {
	id := c.param("job_id")
	p := s.svc.Paths
	j, err := jobs.Load(p, id)
	if errors.Is(err, jobs.ErrNotFound) {
		return jobNotFound(id)
	}
	if err != nil {
		return err
	}
	after := 0
	if v, ok := c.query("after"); ok {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			return problem.InvalidParam("after", "use an integer of 0 or more")
		}
		after = n
	}
	if v := c.r.Header.Get("Last-Event-ID"); v != "" {
		if !lastEventRE.MatchString(v) {
			return problem.InvalidParam("Last-Event-ID", "use the integer id of an event")
		}
		after, _ = strconv.Atoi(v)
	}
	if !wantsSSE(c.r) {
		evs, err := jobs.Events(p, id, after, jobs.MaxPageItems)
		if err != nil {
			return err
		}
		done := false
		for _, e := range evs {
			done = done || e.Event == "done"
		}
		if !done && j.Done() && len(evs) < jobs.MaxPageItems {
			if d := jobs.DoneEvent(j); d.ID > after {
				evs = append(evs, d)
				done = true
			}
		}
		last := after
		if n := len(evs); n > 0 {
			last = evs[n-1].ID
		}
		s.json(c.w, http.StatusOK, map[string]any{"job_id": id, "events": evs, "last_event_id": last, "done": done})
		return nil
	}

	st, release, err := s.openStream(c)
	if err != nil {
		return err
	}
	defer release()
	ctx := c.r.Context()
	beat := time.NewTicker(s.o.Heartbeat)
	defer beat.Stop()
	last := after
	for {
		wake := s.svc.Jobs.Wake(id)
		evs, err := jobs.Events(p, id, last, jobs.MaxPageItems)
		if err != nil {
			return nil
		}
		for _, e := range evs {
			if err := st.write(strconv.Itoa(e.ID), e.Event, e.Data); err != nil {
				return nil
			}
			last = e.ID
			if e.Event == "done" {
				return nil
			}
		}
		if len(evs) == 0 {
			if j, err := jobs.Load(p, id); err == nil && j.Done() {
				d := jobs.DoneEvent(j)
				if d.ID > last {
					_ = st.write(strconv.Itoa(d.ID), d.Event, d.Data)
				}
				return nil
			}
		}
		if len(evs) == jobs.MaxPageItems {
			continue
		}
		select {
		case <-ctx.Done():
			return nil
		case <-s.shutdown:
			return nil
		case <-wake:
		case <-time.After(s.o.Poll):
		case <-beat.C:
			if !s.a.Peek(c.sessionID) {
				return nil
			}
			if err := st.heartbeat(s.a.Now()); err != nil {
				return nil
			}
		}
	}
}

// logs serves GET /logs/{unit} as one JSON page or, with follow=true, as SSE.
func (s *Server) logs(c *reqCtx) error {
	q := service.LogQuery{N: 100}
	if v, ok := c.query("n"); ok {
		n, err := strconv.Atoi(v)
		if err != nil {
			return problem.InvalidParam("n", "use a number from 1 to 5000")
		}
		q.N = n
	}
	if v, ok := c.query("since"); ok {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			return problem.InvalidParam("since", "use an RFC 3339 time, for example 2026-10-30T10:15:00Z")
		}
		q.Since = &t
	}
	if v, ok := c.query("after"); ok {
		q.After = v
	}
	follow, err := c.boolQuery("follow")
	if err != nil {
		return err
	}
	unit := c.param("unit")
	if !follow || !wantsSSE(c.r) {
		page, err := s.svc.Logs(c.r.Context(), unit, q)
		if err != nil {
			return err
		}
		s.json(c.w, http.StatusOK, page)
		return nil
	}
	if v := c.r.Header.Get("Last-Event-ID"); v != "" {
		if !service.CursorRE.MatchString(v) || len(v) > 512 {
			return problem.InvalidParam("Last-Event-ID", "the value is not a journal cursor")
		}
		q.After = v
	}
	if err := s.svc.CheckUnit(c.r.Context(), unit); err != nil {
		return err
	}
	if q.N < 1 || q.N > 5000 {
		return problem.InvalidParam("n", "use a number from 1 to 5000")
	}
	st, release, err := s.openStream(c)
	if err != nil {
		return err
	}
	defer release()
	ctx, cancel := context.WithCancel(c.r.Context())
	var wg sync.WaitGroup
	defer func() {
		cancel()
		wg.Wait()
	}()
	wg.Add(1)
	go func() {
		defer wg.Done()
		t := time.NewTicker(s.o.Heartbeat)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-s.shutdown:
				cancel()
				return
			case <-t.C:
				if !s.a.Peek(c.sessionID) || st.heartbeat(s.a.Now()) != nil {
					cancel()
					return
				}
			}
		}
	}()
	err = s.svc.FollowLogs(ctx, unit, q, func(l service.LogLine) error {
		return st.write(l.Cursor, "log", l)
	})
	if err != nil && ctx.Err() == nil {
		s.log.Warn("log stream ended", "unit", unit, "error", err)
		_ = st.write("", "error", problem.New(problem.Internal, fmt.Sprintf("the journal of %s does not answer", unit)))
	}
	return nil
}
