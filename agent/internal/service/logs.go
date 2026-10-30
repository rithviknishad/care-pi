package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/rithviknishad/care-pi/agent/internal/host"
	"github.com/rithviknishad/care-pi/agent/internal/paths"
	"github.com/rithviknishad/care-pi/agent/internal/problem"
)

func hostCmd(name string, args ...string) host.Cmd { return host.Cmd{Name: name, Args: args} }

// LogUnitInfo is one unit with a journal (schema LogUnitInfo).
type LogUnitInfo struct {
	Name string `json:"name"`
	Unit string `json:"unit"`
}

// LogUnits are the units of "care-pi logs", in the order of the schema LogUnit.
var LogUnits = []LogUnitInfo{
	{"api", "care-api.service"}, {"worker", "care-worker.service"}, {"beat", "care-beat.service"},
	{"db", "care-db.service"}, {"redis", "care-redis.service"}, {"s3", "care-s3.service"},
	{"caddy", "care-caddy.service"}, {"pod", "care-pod.service"}, {"clock", "care-clock.service"},
	{"fe-build", "care-fe-build.service"}, {"target", "care.target"}, {"agent", paths.AgentUnit},
}

// UnitOf returns the systemd unit of a short name. It also accepts care-api and care-api.service.
func UnitOf(name string) (string, bool) {
	n := strings.TrimSuffix(name, ".service")
	switch n {
	case "care", "care.target":
		n = "target"
	case "care-pi":
		n = "agent"
	}
	n = strings.TrimPrefix(n, "care-")
	for _, u := range LogUnits {
		if u.Name == n {
			return u.Unit, true
		}
	}
	return "", false
}

// LogLine is one journal line (schema LogLine).
type LogLine struct {
	Time     time.Time `json:"time"`
	Cursor   string    `json:"cursor"`
	Priority int       `json:"priority"`
	Unit     string    `json:"unit"`
	Message  string    `json:"message"`
}

// LogPage is one page of journal lines (schema LogPage).
type LogPage struct {
	Unit       string    `json:"unit"`
	Lines      []LogLine `json:"lines"`
	NextCursor *string   `json:"next_cursor"`
}

// CursorRE is the pattern of a journal cursor.
var CursorRE = regexp.MustCompile(`^[a-z]=[0-9a-f]+(;[a-z]=[0-9a-f]+)*$`)

const maxMessage = 16384

// parseJournal reads one line of "journalctl --output json".
func parseJournal(raw []byte, unit string) (LogLine, bool) {
	var m map[string]json.RawMessage
	if json.Unmarshal(raw, &m) != nil {
		return LogLine{}, false
	}
	str := func(k string) string {
		var s string
		_ = json.Unmarshal(m[k], &s)
		return s
	}
	l := LogLine{Cursor: str("__CURSOR"), Unit: unit, Priority: 6}
	if !CursorRE.MatchString(l.Cursor) || len(l.Cursor) > 512 {
		return LogLine{}, false
	}
	if us, err := strconv.ParseInt(str("__REALTIME_TIMESTAMP"), 10, 64); err == nil {
		l.Time = time.UnixMicro(us).UTC()
	}
	if p, err := strconv.Atoi(str("PRIORITY")); err == nil && p >= 0 && p <= 7 {
		l.Priority = p
	}
	if msg := m["MESSAGE"]; len(msg) > 0 {
		var s string
		if json.Unmarshal(msg, &s) == nil {
			l.Message = s
		} else {
			var b []byte
			var nums []int
			if json.Unmarshal(msg, &nums) == nil {
				for _, n := range nums {
					b = append(b, byte(n))
				}
				l.Message = strings.ToValidUTF8(string(b), "?")
			}
		}
	}
	if len(l.Message) > maxMessage {
		cut := maxMessage
		for cut > 0 && !utf8.RuneStart(l.Message[cut]) {
			cut--
		}
		l.Message = l.Message[:cut]
	}
	return l, true
}

// LogQuery selects journal lines.
type LogQuery struct {
	N     int
	Since *time.Time
	After string
}

func (q LogQuery) validate() error {
	if q.N < 1 || q.N > 5000 {
		return problem.InvalidParam("n", "use a number from 1 to 5000")
	}
	if q.After != "" && (!CursorRE.MatchString(q.After) || len(q.After) > 512) {
		return problem.InvalidParam("after", "the value is not a journal cursor")
	}
	return nil
}

func journalArgs(unit string, q LogQuery, follow bool) []string {
	args := []string{"--unit", unit, "--output", "json", "--no-pager"}
	if q.After != "" {
		args = append(args, "--after-cursor", q.After)
	} else {
		args = append(args, "--lines", strconv.Itoa(q.N))
	}
	if q.Since != nil {
		args = append(args, "--since", "@"+strconv.FormatInt(q.Since.Unix(), 10))
	}
	if follow {
		args = append(args, "--follow")
	}
	return args
}

func (s *Service) unitMissing(ctx context.Context, unit string) bool {
	props, err := s.Host.Systemd.Show(ctx, []string{unit}, "LoadState")
	return err == nil && len(props) == 1 && props[0]["LoadState"] == "not-found"
}

// Logs returns the last n lines of the journal of a unit (or the n lines after the cursor), oldest first.
func (s *Service) Logs(ctx context.Context, name string, q LogQuery) (*LogPage, error) {
	unit, ok := UnitOf(name)
	if !ok {
		return nil, problem.Newf(problem.UnitNotFound, "Unknown unit %q.", name)
	}
	if err := q.validate(); err != nil {
		return nil, err
	}
	out, err := s.Host.Runner.Run(ctx, host.Cmd{Name: "journalctl", Args: journalArgs(unit, q, false)})
	if err != nil {
		return nil, fmt.Errorf("journalctl failed for %s: %w", unit, err)
	}
	page := &LogPage{Unit: logName(unit), Lines: []LogLine{}}
	for _, raw := range strings.Split(string(out), "\n") {
		if l, ok := parseJournal([]byte(raw), unit); ok {
			page.Lines = append(page.Lines, l)
		}
	}
	if q.After != "" && len(page.Lines) > q.N {
		page.Lines = page.Lines[:q.N]
	}
	if len(page.Lines) == 0 && q.After == "" && q.Since == nil && s.unitMissing(ctx, unit) {
		return nil, problem.Newf(problem.UnitNotFound, "%s has no journal on this host.", unit)
	}
	if n := len(page.Lines); n > 0 {
		c := page.Lines[n-1].Cursor
		page.NextCursor = &c
	}
	return page, nil
}

func logName(unit string) string {
	for _, u := range LogUnits {
		if u.Unit == unit {
			return u.Name
		}
	}
	return unit
}

// ErrNoStream tells that the host runner cannot follow a command.
var ErrNoStream = errors.New("the host runner cannot follow the journal")

// FollowLogs gives the last n lines (or the lines after the cursor) to fn, then each new line, until ctx ends.
func (s *Service) FollowLogs(ctx context.Context, name string, q LogQuery, fn func(LogLine) error) error {
	unit, ok := UnitOf(name)
	if !ok {
		return problem.Newf(problem.UnitNotFound, "Unknown unit %q.", name)
	}
	if err := q.validate(); err != nil {
		return err
	}
	st, ok := s.Host.Runner.(host.Streamer)
	if !ok {
		return ErrNoStream
	}
	return st.Stream(ctx, host.Cmd{Name: "journalctl", Args: journalArgs(unit, q, true)}, func(raw []byte) error {
		if l, ok := parseJournal(raw, unit); ok {
			return fn(l)
		}
		return nil
	})
}

// CheckUnit returns unit-not-found for an unknown short name or a unit that systemd does not know.
func (s *Service) CheckUnit(ctx context.Context, name string) error {
	unit, ok := UnitOf(name)
	if !ok {
		return problem.Newf(problem.UnitNotFound, "Unknown unit %q.", name)
	}
	if s.unitMissing(ctx, unit) {
		return problem.Newf(problem.UnitNotFound, "%s has no journal on this host.", unit)
	}
	return nil
}
