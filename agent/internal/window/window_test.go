package window

import (
	"testing"
	"time"
)

func mustLoc(t *testing.T, name string) *time.Location {
	t.Helper()
	l, err := time.LoadLocation(name)
	if err != nil {
		t.Skipf("no time zone data for %s: %v", name, err)
	}
	return l
}

func at(t *testing.T, loc *time.Location, s string) time.Time {
	t.Helper()
	v, err := time.ParseInLocation("2006-01-02 15:04", s, loc)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestCurrentAndNext(t *testing.T) {
	ist := mustLoc(t, "Asia/Kolkata")
	cases := []struct {
		name     string
		w        Window
		now      string
		open     bool
		closesAt string // local, when open
		nextOpen string // local
	}{
		{"same day, inside", Window{[]string{"Sun"}, "02:00", "05:00"}, "2026-11-22 03:00", true, "2026-11-22 05:00", "2026-11-29 02:00"},
		{"same day, before", Window{[]string{"Sun"}, "02:00", "05:00"}, "2026-11-22 01:59", false, "", "2026-11-22 02:00"},
		{"same day, at the end", Window{[]string{"Sun"}, "02:00", "05:00"}, "2026-11-22 05:00", false, "", "2026-11-29 02:00"},
		{"over midnight, after midnight", Window{[]string{"Sat"}, "23:00", "02:00"}, "2026-11-22 01:30", true, "2026-11-22 02:00", "2026-11-28 23:00"},
		{"over midnight, before midnight", Window{[]string{"Sat"}, "23:00", "02:00"}, "2026-11-21 23:30", true, "2026-11-22 02:00", "2026-11-28 23:00"},
		{"over midnight, Sunday is not a window day", Window{[]string{"Sat"}, "23:00", "02:00"}, "2026-11-22 23:30", false, "", "2026-11-28 23:00"},
		{"24 hours", Window{[]string{"Wed"}, "06:00", "06:00"}, "2026-11-19 05:59", true, "2026-11-19 06:00", "2026-11-25 06:00"},
		{"two days", Window{[]string{"Sat", "Sun"}, "23:00", "02:00"}, "2026-11-22 12:00", false, "", "2026-11-22 23:00"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			now := at(t, ist, c.now)
			s, open := c.w.Current(now, ist)
			if open != c.open {
				t.Fatalf("open %v, want %v", open, c.open)
			}
			if open && !s.Close.Equal(at(t, ist, c.closesAt)) {
				t.Fatalf("closes at %s, want %s", s.Close.In(ist), c.closesAt)
			}
			n, ok := c.w.Next(now, ist)
			if !ok || !n.Open.Equal(at(t, ist, c.nextOpen)) {
				t.Fatalf("next %s, want %s", n.Open.In(ist), c.nextOpen)
			}
		})
	}
}

// TestDST: a start in the skipped hour moves one hour later; in the repeated hour, the first occurrence.
func TestDST(t *testing.T) {
	ny := mustLoc(t, "America/New_York")
	// 2026-03-08: 02:00 → 03:00 (skipped hour). 2026-11-01: 02:00 → 01:00 (repeated hour).
	spring := Window{[]string{"Sun"}, "02:30", "04:00"}
	s, ok := spring.Next(at(t, ny, "2026-03-07 12:00"), ny)
	if !ok || s.Open.UTC().Format(time.RFC3339) != "2026-03-08T07:30:00Z" { // 03:30 EDT
		t.Fatalf("spring open %s", s.Open.UTC())
	}
	if s.Close.Sub(s.Open) != 30*time.Minute {
		t.Fatalf("spring window is one hour shorter: %s", s.Close.Sub(s.Open))
	}
	fall := Window{[]string{"Sun"}, "01:30", "03:00"}
	s, ok = fall.Next(at(t, ny, "2026-10-31 12:00"), ny)
	if !ok || s.Open.UTC().Format(time.RFC3339) != "2026-11-01T05:30:00Z" { // first 01:30, EDT
		t.Fatalf("fall open %s", s.Open.UTC())
	}
	if s.Close.Sub(s.Open) != 150*time.Minute {
		t.Fatalf("fall window is one hour longer: %s", s.Close.Sub(s.Open))
	}
	// Inside the repeated hour (the second 01:45, EST) the window is still open.
	second := time.Date(2026, 11, 1, 6, 45, 0, 0, time.UTC)
	if _, open := fall.Current(second, ny); !open {
		t.Fatal("the second 01:45 is inside the window")
	}
}

func TestCalendarsAndDelay(t *testing.T) {
	w := Window{[]string{"Sun", "Sat"}, "23:00", "02:00"}
	got := w.Calendars()
	if len(got) != 2 || got[0] != "Sat *-*-* 23:00:00" || got[1] != "Sun *-*-* 23:00:00" {
		t.Fatalf("calendars %v", got)
	}
	if w.Length() != 3*time.Hour || w.RandomDelay() != 10*time.Minute {
		t.Fatalf("length %s delay %s", w.Length(), w.RandomDelay())
	}
	short := Window{[]string{"Mon"}, "02:00", "02:20"}
	if short.RandomDelay() != 5*time.Minute || Seconds(short.RandomDelay()) != "300s" {
		t.Fatalf("short delay %s", short.RandomDelay())
	}
	if err := (Window{[]string{"Funday"}, "02:00", "03:00"}).Check(); err == nil {
		t.Fatal("bad day")
	}
}
