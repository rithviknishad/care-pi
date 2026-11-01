package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/rithviknishad/care-pi/agent/internal/clockgate"
	"github.com/rithviknishad/care-pi/agent/internal/host"
	"github.com/rithviknishad/care-pi/agent/internal/paths"
)

func TestClockPassedReloadsCaddy(t *testing.T) {
	reload := "podman exec care-caddy " + strings.Join(clockgate.CaddyReload, " ")
	for name, c := range map[string]struct {
		fail bool
		want []string
	}{
		"reload":   {false, []string{reload}},
		"fallback": {true, []string{reload, "systemctl try-restart care-caddy.service"}},
	} {
		t.Run(name, func(t *testing.T) {
			flag := filepath.Join(t.TempDir(), "clock-wait")
			if err := os.WriteFile(flag, nil, 0o644); err != nil {
				t.Fatal(err)
			}
			fake := host.NewFake()
			if c.fail {
				fake.On(reload, host.Response{Code: 125})
			}
			var out bytes.Buffer
			clockPassed(context.Background(), host.New(fake), flag, &out)
			if !reflect.DeepEqual(fake.Calls, c.want) {
				t.Fatalf("commands:\n got  %q\n want %q", fake.Calls, c.want)
			}
			if _, err := os.Stat(flag); !os.IsNotExist(err) {
				t.Fatalf("the clock-wait flag still exists: %v", err)
			}
		})
	}
}

const ntpQuery = "timedatectl show --property NTPSynchronized --value"

func writeBrowserMarker(t *testing.T, p paths.Paths) {
	t.Helper()
	writeFile(t, p.Abs(paths.ClockTrusted), `{"trusted":false,"source":"browser","set_at":"2026-11-01T10:00:00Z","host_time_before":"2026-10-20T08:00:00Z"}`)
}

// S52: without NTP, the clock that the browser set at the claim passes the gate after the NTP grace time. The
// marker stays: the clock is not trusted.
func TestClockWaitPassesWithTheBrowserClock(t *testing.T) {
	p := paths.New(t.TempDir())
	writeBrowserMarker(t, p)
	fake := host.NewFake()
	fake.On(ntpQuery, host.Response{Out: "no\n"})
	var out bytes.Buffer
	start := time.Now()
	err := clockWait(context.Background(), clockWaitEnv{paths: p, host: host.New(fake), timeout: 5 * time.Second,
		grace: 50 * time.Millisecond, tick: 10 * time.Millisecond, out: &out, errw: &out})
	if err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	if time.Since(start) < 50*time.Millisecond || len(fake.Filter(ntpQuery)) < 2 {
		t.Fatal("the gate must try NTP for the grace time first")
	}
	if !strings.Contains(out.String(), "use the clock that the browser set") || len(fake.Filter("podman exec care-caddy caddy reload")) != 1 {
		t.Fatalf("output:\n%s\ncalls %q", out.String(), fake.Calls)
	}
	if _, ok := clockgate.Read(p); !ok {
		t.Fatal("the marker must stay until NTP synchronizes the clock")
	}
	if _, err := os.Stat(p.Abs(paths.ModeDir + "/" + paths.ModeClockWait)); !os.IsNotExist(err) {
		t.Fatal("the clock-wait flag is still there")
	}
}

// S52: NTP in the grace time wins, and a synchronized clock deletes the marker. Without a marker and NTP, the gate
// waits (here until the timeout).
func TestClockWaitNTPClearsTheMarker(t *testing.T) {
	p := paths.New(t.TempDir())
	writeBrowserMarker(t, p)
	fake := host.NewFake()
	fake.On(ntpQuery, host.Response{Out: "no\n"}, host.Response{Out: "yes\n"})
	var out bytes.Buffer
	if err := clockWait(context.Background(), clockWaitEnv{paths: p, host: host.New(fake), timeout: 5 * time.Second,
		grace: time.Hour, tick: 10 * time.Millisecond, out: &out, errw: &out}); err != nil {
		t.Fatal(err)
	}
	if _, ok := clockgate.Read(p); ok {
		t.Fatal("NTP synchronized the clock: the marker must go")
	}

	p = paths.New(t.TempDir())
	fake = host.NewFake()
	fake.On(ntpQuery, host.Response{Out: "no\n"})
	err := clockWait(context.Background(), clockWaitEnv{paths: p, host: host.New(fake), timeout: 100 * time.Millisecond,
		grace: time.Millisecond, tick: 10 * time.Millisecond, out: &out, errw: &out})
	if err == nil || len(fake.Filter("podman exec")) != 0 {
		t.Fatalf("no NTP and no browser clock: %v", err)
	}
}
