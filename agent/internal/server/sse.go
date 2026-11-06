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
	st, err := startStream(c.w)
	if err != nil {
		release()
		return nil, nil, err
	}
	return st, release, nil
}

// openPublicStream takes a public stream slot of the client address and starts the SSE response (S67). The public
// streams have their own slots, so a client without a session cannot take the slots of the console.
func (s *Server) openPublicStream(c *reqCtx) (*stream, func(), error) {
	addr := c.client.Addr
	perAddr := s.o.MaxAddressStreams
	if c.client.Shared {
		perAddr = s.o.MaxPublicStreams
	}
	s.mu.Lock()
	if s.pubStreams >= s.o.MaxPublicStreams || s.byAddr[addr] >= perAddr {
		s.mu.Unlock()
		return nil, nil, problem.Limited(problem.RateLimited, 5, fmt.Sprintf("The limit is %d open public job streams for each address and %d in total.",
			s.o.MaxAddressStreams, s.o.MaxPublicStreams))
	}
	s.pubStreams++
	s.byAddr[addr]++
	s.mu.Unlock()
	release := func() {
		s.mu.Lock()
		s.pubStreams--
		if s.byAddr[addr]--; s.byAddr[addr] <= 0 {
			delete(s.byAddr, addr)
		}
		s.mu.Unlock()
	}
	st, err := startStream(c.w)
	if err != nil {
		release()
		return nil, nil, err
	}
	return st, release, nil
}

// startStream writes the headers of an SSE response and the retry line.
func startStream(w http.ResponseWriter) (*stream, error) {
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-store")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	st := &stream{w: w, rc: http.NewResponseController(w)}
	st.mu.Lock()
	defer st.mu.Unlock()
	if _, err := w.Write([]byte("retry: 3000\n\n")); err != nil {
		return nil, err
	}
	if err := st.rc.Flush(); err != nil {
		return nil, err
	}
	return st, nil
}

// afterOf reads the event id to resume after: Last-Event-ID wins over the parameter after.
func afterOf(c *reqCtx) (int, error) {
	after := 0
	if v, ok := c.query("after"); ok {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			return 0, problem.InvalidParam("after", "use an integer of 0 or more")
		}
		after = n
	}
	if v := c.r.Header.Get("Last-Event-ID"); v != "" {
		if !lastEventRE.MatchString(v) {
			return 0, problem.InvalidParam("Last-Event-ID", "use the integer id of an event")
		}
		after, _ = strconv.Atoi(v)
	}
	return after, nil
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
	after, err := afterOf(c)
	if err != nil {
		return err
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
	// The job restore-dr ends the sessions only after each stream of the job sent the event done.
	defer s.svc.Jobs.Watch(id)()
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

// publicJobEvents serves GET /_care-pi/jobs/{job_id}/events (S67): the public form of the job events for the status
// pages of Caddy and a device without a session. It sends step events without the detail, error events with the
// type and the title, and done with the public job. It never sends a log or progress event, the result, or the
// reason (docs/api/README.md section 6.3). The ids are the ids of the job events, so they can have gaps.
func (s *Server) publicJobEvents(c *reqCtx) error {
	id := c.param("job_id")
	p := s.svc.Paths
	if _, err := jobs.Peek(p, id); errors.Is(err, jobs.ErrNotFound) {
		return jobNotFound(id)
	} else if err != nil {
		return err
	}
	after, err := afterOf(c)
	if err != nil {
		return err
	}
	cur := jobs.NewCursor(p, id, after)
	if !wantsSSE(c.r) {
		evs, err := cur.Next(jobs.MaxPageItems)
		if err != nil {
			return err
		}
		out := []jobs.Event{}
		done := false
		for _, e := range evs {
			if pe, ok := jobs.PublicEvent(e); ok {
				out = append(out, pe)
				done = done || e.Event == "done"
			}
		}
		if !done && len(evs) < jobs.MaxPageItems {
			if j, err := jobs.Peek(p, id); err == nil {
				if d, ok := cur.Done(j, after); ok {
					pe, _ := jobs.PublicEvent(d)
					out = append(out, pe)
					cur.Last, done = d.ID, true
				}
			}
		}
		s.json(c.w, http.StatusOK, map[string]any{"job_id": id, "events": out, "last_event_id": cur.Last, "done": done})
		return nil
	}

	st, release, err := s.openPublicStream(c)
	if err != nil {
		return err
	}
	defer release()
	ctx := c.r.Context()
	beat := time.NewTicker(s.o.Heartbeat)
	defer beat.Stop()
	for {
		wake := s.svc.Jobs.Wake(id)
		evs, err := cur.Next(jobs.MaxPageItems)
		if err != nil {
			return nil
		}
		for _, e := range evs {
			pe, ok := jobs.PublicEvent(e)
			if !ok {
				continue
			}
			if err := st.write(strconv.Itoa(pe.ID), pe.Event, pe.Data); err != nil || pe.Event == "done" {
				return nil
			}
		}
		if len(evs) == 0 {
			if j, err := jobs.Peek(p, id); err == nil && j.Done() {
				if d, ok := cur.Done(j, after); ok {
					pe, _ := jobs.PublicEvent(d)
					_ = st.write(strconv.Itoa(pe.ID), pe.Event, pe.Data)
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
			// The log events of a busy job wake the stream; one read takes each event of the gap.
			select {
			case <-ctx.Done():
				return nil
			case <-s.shutdown:
				return nil
			case <-time.After(s.o.PublicGap):
			}
		case <-time.After(s.o.Poll):
		case <-beat.C:
			if err := st.heartbeat(s.a.Now()); err != nil {
				return nil
			}
		}
	}
}

// logQuery reads the parameters n, since, and after of GET /logs/{unit} and GET /logs/{unit}/export.
func logQuery(c *reqCtx) (service.LogQuery, error) {
	q := service.LogQuery{N: 100}
	if v, ok := c.query("n"); ok {
		n, err := strconv.Atoi(v)
		if err != nil {
			return q, problem.InvalidParam("n", "use a number from 1 to 5000")
		}
		q.N = n
	}
	if v, ok := c.query("since"); ok {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			return q, problem.InvalidParam("since", "use an RFC 3339 time, for example 2026-10-30T10:15:00Z")
		}
		q.Since = &t
	}
	if v, ok := c.query("after"); ok {
		q.After = v
	}
	return q, nil
}

// exportLogs serves GET /logs/{unit}/export: one page of the journal as plain text for support (S68).
func (s *Server) exportLogs(c *reqCtx) error {
	q, err := logQuery(c)
	if err != nil {
		return err
	}
	text, err := s.svc.LogsText(c.r.Context(), c.param("unit"), q)
	if err != nil {
		return err
	}
	return s.text(c, text)
}

// logs serves GET /logs/{unit} as one JSON page or, with follow=true, as SSE.
func (s *Server) logs(c *reqCtx) error {
	q, err := logQuery(c)
	if err != nil {
		return err
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
