package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/rithviknishad/care-pi/agent/internal/clockgate"
	"github.com/rithviknishad/care-pi/agent/internal/config"
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

func rtcHost(t *testing.T, root paths.Paths, since int64, battery string) {
	t.Helper()
	writeFile(t, root.Abs("/dev/rtc0"), "")
	writeFile(t, root.Abs("/sys/class/rtc/rtc0/since_epoch"), strconv.FormatInt(since, 10)+"\n")
	if battery != "" {
		writeFile(t, root.Abs("/sys/class/rtc/rtc0/battery_voltage"), battery+"\n")
	}
	writeFile(t, root.Abs("/proc/uptime"), "30.00 60.00\n")
}

// S75: detect sets clock.policy rtc when the RTC kept the time through the boot, keeps the comments of config.yaml,
// respects a value that the operator set, and writes nothing in a dry run.
func TestClockDetect(t *testing.T) {
	ctx := context.Background()
	p := paths.New(t.TempDir())
	cfgPath := p.Abs(paths.ConfigFile)
	data, err := config.DefaultYAML("Sunrise", "care", "small-clinic", "none")
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, cfgPath, string(data))
	boot := clockgate.Floor().Add(72 * time.Hour)
	rtcHost(t, p, boot.Unix()+30, "3010000")
	klog := func(context.Context) []byte {
		return []byte("rpi-rtc soc@107c000000:rpi_rtc: setting system clock to x UTC (" + strconv.FormatInt(boot.Unix(), 10) + ")\n")
	}
	fake := host.NewFake()
	fake.On(ntpQuery, host.Response{Out: "yes\n"})
	env := clockDetectEnv{paths: p, host: host.New(fake), configPath: cfgPath, out: &bytes.Buffer{}, kernelLog: klog}
	// The drift that an earlier boot measured: a new detection deletes it; a dry run keeps it.
	writeFile(t, p.Abs(paths.StateDir+"/clock-rtc-check.json"), `{"checked_at":"2026-11-08T06:00:00Z","drift_s":420,"ok":false}`)

	dry := env
	dry.dryRun = true
	d, err := clockDetect(ctx, dry, true)
	if err != nil || d.Policy != "rtc" || d.Config == nil || d.Config.After != "rtc" || d.Config.Written {
		t.Fatalf("dry run: %+v %v", d, err)
	}
	if got, _ := os.ReadFile(cfgPath); string(got) != string(data) {
		t.Fatal("a dry run wrote config.yaml")
	}
	if _, ok := clockgate.LastDetection(p); ok {
		t.Fatal("a dry run wrote the record")
	}
	if _, ok := clockgate.LastRTCCheck(p); !ok {
		t.Fatal("a dry run deleted the RTC check record")
	}

	d, err = clockDetect(ctx, env, true)
	if err != nil || !d.Config.Written {
		t.Fatalf("%+v %v", d, err)
	}
	if _, ok := clockgate.LastRTCCheck(p); ok {
		t.Fatal("detect kept the RTC check record of an earlier boot")
	}
	got, _ := os.ReadFile(cfgPath)
	cfg, err := config.Parse(got, "config.yaml")
	if err != nil || cfg.Clock.Policy != "rtc" || !strings.Contains(string(got), "# care-pi desired state") ||
		!strings.Contains(string(got), "# solo | small-clinic | busy-counter | custom") {
		t.Fatalf("config.yaml:\n%s\n%v", got, err)
	}

	// The operator sets ntp: detect keeps it, unless --force.
	writeFile(t, cfgPath, strings.Replace(string(got), "policy: rtc", "policy: ntp", 1))
	if d, err = clockDetect(ctx, env, true); err != nil || d.Config.Written || !strings.Contains(d.Config.Reason, "operator") {
		t.Fatalf("%+v %v", d.Config, err)
	}
	force := env
	force.force = true
	if d, err = clockDetect(ctx, force, true); err != nil || !d.Config.Written || d.Config.After != "rtc" {
		t.Fatalf("%+v %v", d.Config, err)
	}

	// care.local: no battery (1970 at boot) gives ntp, and a pre-seed value stays (keep).
	p2 := paths.New(t.TempDir())
	cfg2 := p2.Abs(paths.ConfigFile)
	writeFile(t, cfg2, string(data))
	rtcHost(t, p2, boot.Unix(), "0")
	env2 := clockDetectEnv{paths: p2, host: host.New(fake), configPath: cfg2, out: &bytes.Buffer{},
		kernelLog: func(context.Context) []byte { return []byte(careLocalKernelLog) }}
	if d, err = clockDetect(ctx, env2, true); err != nil || d.Policy != "ntp" || d.Config.Written || !strings.Contains(d.Reason, "no battery") {
		t.Fatalf("%+v %v", d, err)
	}
	writeFile(t, cfg2, "version: 1\nclock: {policy: ntp}\n")
	rtcHost(t, p2, boot.Unix(), "3000000")
	env2.kernelLog = klog
	env2.keep = preseedSetsClock(cfg2)
	if d, err = clockDetect(ctx, env2, true); err != nil || d.Policy != "rtc" || d.Config.Written {
		t.Fatalf("the pre-seed value stays: %+v %v", d.Config, err)
	}
}

const careLocalKernelLog = "rpi-rtc soc@107c000000:rpi_rtc: setting system clock to 1970-01-01T00:00:21 UTC (21)\n"

// S75: the policy rtc passes from the RTC without NTP and starts timesyncd; an RTC at 1970 waits as policy ntp.
func TestClockWaitRTC(t *testing.T) {
	p := paths.New(t.TempDir())
	rtcHost(t, p, clockgate.Floor().Add(24*time.Hour).Unix(), "")
	fake := host.NewFake()
	fake.On(ntpQuery, host.Response{Out: "no\n"})
	var out bytes.Buffer
	err := clockWait(context.Background(), clockWaitEnv{paths: p, host: host.New(fake), timeout: time.Second,
		grace: time.Second, tick: 10 * time.Millisecond, out: &out, errw: &out, policy: "rtc"})
	if err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	if len(fake.Filter("systemctl start systemd-timesyncd.service")) != 1 || !strings.Contains(out.String(), "policy rtc") {
		t.Fatalf("calls %q\n%s", fake.Calls, out.String())
	}
	if m, ok := clockgate.Read(p); !ok || m.Source != clockgate.SourceRTC || m.Trusted || m.Uptime != 30 {
		t.Fatalf("marker %+v %v", m, ok)
	}

	p = paths.New(t.TempDir())
	rtcHost(t, p, 21, "0")
	out.Reset()
	err = clockWait(context.Background(), clockWaitEnv{paths: p, host: host.New(fake), timeout: 100 * time.Millisecond,
		grace: time.Second, tick: 10 * time.Millisecond, out: &out, errw: &out, policy: "rtc"})
	if err == nil || !strings.Contains(out.String(), "wait as policy ntp") {
		t.Fatalf("an RTC at 1970 must wait for NTP: %v\n%s", err, out.String())
	}
}
