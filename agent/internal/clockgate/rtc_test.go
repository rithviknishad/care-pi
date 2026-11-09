package clockgate

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/rithviknishad/care-pi/agent/internal/host"
	"github.com/rithviknishad/care-pi/agent/internal/paths"
)

// The kernel log of care.local (a Pi 5 without an RTC battery), 2026-11-09.
const careLocalKlog = `rpi-rtc soc@107c000000:rpi_rtc: registered as rtc0
rpi-rtc soc@107c000000:rpi_rtc: setting system clock to 1970-01-01T00:00:21 UTC (21)
clk: Disabling unused clocks
`

func put(t *testing.T, p paths.Paths, rel, data string) {
	t.Helper()
	path := p.Abs(rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestParseHctosys(t *testing.T) {
	got, ok := ParseHctosys([]byte(careLocalKlog))
	if !ok || !got.Equal(time.Unix(21, 0)) {
		t.Fatalf("%v %v", got, ok)
	}
	if _, ok := ParseHctosys([]byte("clk: Disabling unused clocks\n")); ok {
		t.Fatal("no message")
	}
	// dmesg format with a time stamp; a battery RTC.
	got, ok = ParseHctosys([]byte("[    3.536242] rtc-ds1307 1-0068: setting system clock to 2026-11-09T06:30:00 UTC (1794205800)\n"))
	if !ok || got.Unix() != 1794205800 {
		t.Fatalf("%v %v", got, ok)
	}
}

func TestDetect(t *testing.T) {
	floor := time.Date(2026, 11, 6, 11, 22, 2, 0, time.UTC)
	now := floor.Add(72 * time.Hour)
	at := func(t time.Time) *time.Time { return &t }
	uv := func(v int64) *int64 { return &v }
	for _, c := range []struct {
		name   string
		r      RTCReading
		policy string
		reason string
	}{
		{"no RTC", RTCReading{}, "ntp", "no RTC"},
		{"care.local: no battery, 1970 at boot", RTCReading{Present: true, AtBoot: at(time.Unix(21, 0).UTC()), AtBootSource: rtcSourceBoot, BatteryMicrovolts: uv(0)}, "ntp", "no battery (battery_voltage 0.000 V)"},
		{"a warm reboot without battery", RTCReading{Present: true, AtBoot: at(now), AtBootSource: rtcSourceBoot, BatteryMicrovolts: uv(0)}, "ntp", "no battery"},
		{"1970 at boot, no voltage file (a Pi 4 with DS3231)", RTCReading{Present: true, AtBoot: at(time.Unix(21, 0).UTC()), AtBootSource: rtcSourceBoot}, "ntp", "did not keep the time"},
		{"a battery", RTCReading{Present: true, AtBoot: at(now), AtBootSource: rtcSourceBoot, BatteryMicrovolts: uv(3_010_000)}, "rtc", "battery_voltage 3.010 V"},
		{"a DS3231 that kept the time", RTCReading{Present: true, AtBoot: at(now), AtBootSource: rtcSourceBoot}, "rtc", "kept the time"},
		{"unknown boot time", RTCReading{Present: true}, "ntp", "not known"},
		{"10 years after the build", RTCReading{Present: true, AtBoot: at(floor.AddDate(11, 0, 0)), AtBootSource: rtcSourceBoot}, "ntp", "did not keep the time"},
	} {
		t.Run(c.name, func(t *testing.T) {
			d := Detect(c.r, floor, now)
			if d.Policy != c.policy || !strings.Contains(d.Reason, c.reason) {
				t.Fatalf("%s: %s", d.Policy, d.Reason)
			}
		})
	}
}

func TestReadRTC(t *testing.T) {
	p := paths.New(t.TempDir())
	if r := ReadRTC(p, nil, false); r.Present {
		t.Fatal("no /dev/rtc0")
	}
	put(t, p, RTCDevice, "")
	put(t, p, rtcBatteryUV, "0\n")
	put(t, p, rtcSinceEpoch, "1794206119\n")
	r := ReadRTC(p, []byte(careLocalKlog), true)
	if !r.Present || r.AtBoot == nil || r.AtBoot.Unix() != 21 || r.AtBootSource != rtcSourceBoot || r.BatteryMicrovolts == nil || *r.BatteryMicrovolts != 0 {
		t.Fatalf("%+v", r)
	}
	// No kernel message: the RTC now is the time at boot only before NTP writes the RTC.
	if r := ReadRTC(p, nil, true); r.AtBoot != nil {
		t.Fatalf("after NTP the RTC time is not the time at boot: %+v", r)
	}
	if r := ReadRTC(p, nil, false); r.AtBoot == nil || r.AtBoot.Unix() != 1794206119 || r.AtBootSource != rtcSourceNow {
		t.Fatalf("%+v", r)
	}
}

type fakeClock struct{ synced bool }

func (f *fakeClock) NTPSynchronized(context.Context) (bool, error) { return f.synced, nil }
func (f *fakeClock) SetTime(context.Context, time.Time) error      { return nil }

var _ host.Clock = (*fakeClock)(nil)

// The gate of the policy rtc passes from the RTC, the clock is not trusted, and the first NTP synchronization records
// the drift: within 5 minutes is ok, more is a warning.
func TestFromRTCAndDrift(t *testing.T) {
	ctx := context.Background()
	for _, c := range []struct {
		name  string
		drift time.Duration
		ok    bool
	}{
		{"within 5 minutes", 90 * time.Second, true},
		{"drift", 7 * time.Minute, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			p := paths.New(t.TempDir())
			rtc := Floor().Add(48 * time.Hour)
			put(t, p, rtcSinceEpoch, strconv.FormatInt(rtc.Unix(), 10))
			put(t, p, procUptime, "12.50 40.00\n")
			got, pass, err := FromRTC(p, rtc)
			if err != nil || !pass || !got.Equal(rtc) {
				t.Fatalf("%v %v %v", got, pass, err)
			}
			clk := &fakeClock{}
			if src, trusted, synced := State(ctx, p, clk); src != SourceRTC || trusted || synced {
				t.Fatalf("before NTP: %s %v %v", src, trusted, synced)
			}
			// 600 s later NTP synchronizes the clock: the true time is the RTC time + 600 s + the drift of the RTC.
			put(t, p, procUptime, "612.50 40.00\n")
			if err := Synchronized(p, rtc.Add(600*time.Second+c.drift)); err != nil {
				t.Fatal(err)
			}
			chk, ok := LastRTCCheck(p)
			if !ok || chk.OK != c.ok || chk.DriftS != c.drift.Seconds() {
				t.Fatalf("%+v %v", chk, ok)
			}
			clk.synced = true
			if src, trusted, synced := State(ctx, p, clk); src != SourceNTP || !trusted || !synced {
				t.Fatalf("after NTP: %s %v %v", src, trusted, synced)
			}
			if _, ok := Read(p); ok {
				t.Fatal("the marker stays after NTP")
			}
			if err := ClearRTCCheck(p); err != nil {
				t.Fatal(err)
			}
			if _, ok := LastRTCCheck(p); ok {
				t.Fatal("the RTC check record stays after ClearRTCCheck")
			}
			if err := ClearRTCCheck(p); err != nil {
				t.Fatalf("a second clear: %v", err)
			}
		})
	}
	// An RTC without battery (1970) does not pass.
	p := paths.New(t.TempDir())
	put(t, p, rtcSinceEpoch, "21\n")
	if _, pass, err := FromRTC(p, time.Now()); pass || err != nil {
		t.Fatalf("%v %v", pass, err)
	}
	if _, ok := Read(p); ok {
		t.Fatal("no marker without a pass")
	}
}

func TestOperatorSet(t *testing.T) {
	p := paths.New(t.TempDir())
	if OperatorSet(p, "ntp") || !OperatorSet(p, "rtc") {
		t.Fatal("without a detection, only ntp is the default")
	}
	if err := SaveDetection(p, Detection{Policy: "rtc", Config: &ConfigNote{Before: "ntp", After: "rtc", Written: true}}); err != nil {
		t.Fatal(err)
	}
	if OperatorSet(p, "rtc") || !OperatorSet(p, "ntp") {
		t.Fatal("the value of the last detection is not the choice of the operator")
	}
}
