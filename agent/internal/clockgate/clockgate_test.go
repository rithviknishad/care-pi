package clockgate

import (
	"context"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/rithviknishad/care-pi/agent/internal/host"
	"github.com/rithviknishad/care-pi/agent/internal/paths"
)

const ntpQuery = "timedatectl show --property NTPSynchronized --value"

func TestPlausible(t *testing.T) {
	floor := Floor()
	for _, c := range []struct {
		t    time.Time
		want bool
	}{
		{floor.Add(-time.Second), false},
		{floor.Add(time.Hour), true},
		{floor.AddDate(9, 11, 0), true},
		{floor.AddDate(10, 0, 1), false},
	} {
		if got := Plausible(c.t); got != c.want {
			t.Errorf("Plausible(%s) = %v", c.t, got)
		}
	}
}

// S52: the browser sets the clock one time; State tells browser, then ntp after NTP (and the marker goes).
func TestFromBrowserAndState(t *testing.T) {
	ctx := context.Background()
	p := paths.New(t.TempDir())
	fake := host.NewFake().On(ntpQuery, host.Response{Out: "no\n"})
	h := host.New(fake)
	if src, trusted, _ := State(ctx, p, h.Clock); src != SourceNone || trusted {
		t.Fatalf("no NTP, no marker, no RTC: %s %v", src, trusted)
	}
	browser := Floor().Add(48 * time.Hour)
	res, err := FromBrowser(ctx, p, h.Clock, browser, Floor())
	if err != nil || !res.Set {
		t.Fatalf("%+v %v", res, err)
	}
	set := fake.Filter("systemd-run")
	if len(set) != 1 || !strings.Contains(set[0], "--unit="+host.SetClockUnit) || !strings.HasSuffix(set[0], " sh "+strconv.FormatInt(browser.Unix(), 10)) {
		t.Fatalf("commands %q", set)
	}
	// One time only.
	if res, _ := FromBrowser(ctx, p, h.Clock, browser.Add(time.Hour), Floor()); res.Set {
		t.Fatal("the browser set the clock a second time")
	}
	if src, trusted, synced := State(ctx, p, h.Clock); src != SourceBrowser || trusted || synced {
		t.Fatalf("after the claim: %s %v %v", src, trusted, synced)
	}
	fake.Set(ntpQuery, host.Response{Out: "yes\n"})
	if src, trusted, synced := State(ctx, p, h.Clock); src != SourceNTP || !trusted || !synced {
		t.Fatalf("after NTP: %s %v %v", src, trusted, synced)
	}
	if _, ok := Read(p); ok {
		t.Fatal("NTP synchronized the clock: the marker must go")
	}
}

func TestStateRTC(t *testing.T) {
	p := paths.New(t.TempDir())
	if err := os.MkdirAll(p.Abs("/dev"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p.Abs("/dev/rtc0"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	fake := host.NewFake().On(ntpQuery, host.Response{Out: "no\n"})
	if src, trusted, _ := State(context.Background(), p, host.New(fake).Clock); src != SourceRTC || trusted {
		t.Fatalf("an RTC without NTP: %s %v", src, trusted)
	}
}
