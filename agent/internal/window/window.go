// Package window computes the update window of config.yaml (updates.window) in the time zone of
// the host (docs/solutions.md, section 10.1).
//
// Each day in days opens one window at start. An end after start closes it the same day; an end
// before start closes it the next day; an end equal to start gives 24 hours. The agent computes in
// local wall time: a start or an end in a skipped hour (daylight saving time) moves one hour later,
// and in a repeated hour the agent uses the first occurrence.
package window

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Days are the day names of config.yaml, in the order of time.Weekday.
var Days = []string{"Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"}

// Window is updates.window.
type Window struct {
	Days  []string
	Start string
	End   string
}

// Span is one opening of the window.
type Span struct {
	Open  time.Time
	Close time.Time
}

func clock(s string) (h, m int, err error) {
	a, b, ok := strings.Cut(s, ":")
	if !ok {
		return 0, 0, fmt.Errorf("the time %q is not HH:MM", s)
	}
	h, err1 := strconv.Atoi(a)
	m, err2 := strconv.Atoi(b)
	if err1 != nil || err2 != nil || h < 0 || h > 23 || m < 0 || m > 59 {
		return 0, 0, fmt.Errorf("the time %q is not HH:MM", s)
	}
	return h, m, nil
}

// Check reports an error for a day or a time that is not valid.
func (w Window) Check() error {
	if len(w.Days) == 0 {
		return fmt.Errorf("updates.window.days is empty")
	}
	for _, d := range w.Days {
		if dayIndex(d) < 0 {
			return fmt.Errorf("updates.window.days has %q; use Mon, Tue, Wed, Thu, Fri, Sat, or Sun", d)
		}
	}
	if _, _, err := clock(w.Start); err != nil {
		return err
	}
	_, _, err := clock(w.End)
	return err
}

func dayIndex(d string) int {
	for i, n := range Days {
		if n == d {
			return i
		}
	}
	return -1
}

func (w Window) has(wd time.Weekday) bool {
	for _, d := range w.Days {
		if dayIndex(d) == int(wd) {
			return true
		}
	}
	return false
}

// Length returns the length of the window in wall time (without daylight saving time).
func (w Window) Length() time.Duration {
	sh, sm, err1 := clock(w.Start)
	eh, em, err2 := clock(w.End)
	if err1 != nil || err2 != nil {
		return 0
	}
	start, end := sh*60+sm, eh*60+em
	if end <= start {
		end += 24 * 60
	}
	return time.Duration(end-start) * time.Minute
}

// Wall returns the instant of the wall time y-m-d h:mi in loc. A wall time in a skipped hour moves
// one hour later. A wall time that occurs two times gives the first occurrence.
func Wall(y int, mo time.Month, d, h, mi int, loc *time.Location) time.Time {
	for extra := 0; extra < 3; extra++ {
		naive := time.Date(y, mo, d, h+extra, mi, 0, 0, time.UTC)
		var best time.Time
		for _, probe := range []time.Duration{-26 * time.Hour, -3 * time.Hour, 3 * time.Hour, 26 * time.Hour} {
			_, off := naive.Add(probe).In(loc).Zone()
			t := naive.Add(-time.Duration(off) * time.Second)
			lt := t.In(loc)
			if lt.Year() == naive.Year() && lt.Month() == naive.Month() && lt.Day() == naive.Day() &&
				lt.Hour() == naive.Hour() && lt.Minute() == naive.Minute() {
				if best.IsZero() || t.Before(best) {
					best = t
				}
			}
		}
		if !best.IsZero() {
			return best
		}
	}
	return time.Date(y, mo, d, h, mi, 0, 0, loc)
}

// spanOn returns the window that opens on the local date of day, and false when day is not a window day.
func (w Window) spanOn(day time.Time, loc *time.Location) (Span, bool) {
	if !w.has(day.Weekday()) {
		return Span{}, false
	}
	sh, sm, err1 := clock(w.Start)
	eh, em, err2 := clock(w.End)
	if err1 != nil || err2 != nil {
		return Span{}, false
	}
	y, mo, d := day.Date()
	open := Wall(y, mo, d, sh, sm, loc)
	closeDay := d
	if eh*60+em <= sh*60+sm {
		closeDay++
	}
	return Span{Open: open, Close: Wall(y, mo, closeDay, eh, em, loc)}, true
}

// Current returns the window that is open at now, and false when the window is closed.
// A time is open when it is in a window that opened today or yesterday (local dates).
func (w Window) Current(now time.Time, loc *time.Location) (Span, bool) {
	local := now.In(loc)
	for _, back := range []int{0, -1} {
		day := time.Date(local.Year(), local.Month(), local.Day()+back, 12, 0, 0, 0, loc)
		if s, ok := w.spanOn(day, loc); ok && !now.Before(s.Open) && now.Before(s.Close) {
			return s, true
		}
	}
	return Span{}, false
}

// Next returns the first window that opens after now.
func (w Window) Next(now time.Time, loc *time.Location) (Span, bool) {
	local := now.In(loc)
	for i := 0; i <= 8; i++ {
		day := time.Date(local.Year(), local.Month(), local.Day()+i, 12, 0, 0, 0, loc)
		if s, ok := w.spanOn(day, loc); ok && s.Open.After(now) {
			return s, true
		}
	}
	return Span{}, false
}

// Calendars returns one systemd OnCalendar value for each day of the window, Mon first,
// for example "Sat *-*-* 23:00:00".
func (w Window) Calendars() []string {
	var out []string
	for _, wd := range []time.Weekday{1, 2, 3, 4, 5, 6, 0} {
		if w.has(wd) {
			out = append(out, Days[wd]+" *-*-* "+w.Start+":00")
		}
	}
	return out
}

// RandomDelay is the RandomizedDelaySec of care-update-apply.timer: the smaller of 10 minutes and a quarter of the window.
func (w Window) RandomDelay() time.Duration {
	return min(10*time.Minute, w.Length()/4)
}

// Seconds formats a duration for a systemd setting, for example "600s".
func Seconds(d time.Duration) string { return strconv.Itoa(int(d.Seconds())) + "s" }
