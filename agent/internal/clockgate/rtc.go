package clockgate

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/rithviknishad/care-pi/agent/internal/lock"
	"github.com/rithviknishad/care-pi/agent/internal/paths"
)

// The RTC clock policy (S75, SP9, plans/01-architecture.md section 7).
//
// A Raspberry Pi 5 has an RTC (/dev/rtc0). Without a battery, it starts at 1970-01-01 at each power-on: the kernel log
// of care.local shows "rpi-rtc ...: setting system clock to 1970-01-01T00:00:21 UTC (21)". With a battery, it keeps the
// time. "care-pi clock detect" reads the RTC time at boot from that kernel message: a time after the build time of the
// agent tells that the RTC kept the time, so the policy rtc is safe. A warm reboot keeps the RTC time also without a
// battery (the board keeps its power), so a Pi 5 that reports a battery voltage of 0 never gets rtc. fake-hwclock and
// the clock file of systemd-timesyncd restore the time of the last shutdown: they are never trusted.

// RTC files below the root.
const (
	RTCDevice      = "/dev/rtc0"
	RTCSysfs       = "/sys/class/rtc/rtc0"
	rtcSinceEpoch  = RTCSysfs + "/since_epoch"
	rtcBatteryUV   = RTCSysfs + "/battery_voltage"
	procUptime     = "/proc/uptime"
	minBatteryUV   = 1_000_000
	RTCMaxDrift    = 5 * time.Minute
	rtcCheckFile   = paths.StateDir + "/clock-rtc-check.json"
	detectFile     = paths.StateDir + "/clock-detect.json"
	rtcSourceBoot  = "kernel log"
	rtcSourceNow   = "RTC now (NTP did not synchronize the clock yet)"
	policyRTC      = "rtc"
	policyNTP      = "ntp"
	unknownBootRTC = "unknown"
)

// hctosysRE matches the kernel message of the RTC at boot: "setting system clock to 1970-01-01T00:00:21 UTC (21)".
var hctosysRE = regexp.MustCompile(`rtc.*: setting system clock to \S+ UTC \((\d+)\)`)

// RTCReading is what the host tells about its RTC.
type RTCReading struct {
	Present bool `json:"present"`
	// AtBoot is the RTC time at boot, or nil when it is not known. AtBootSource tells where it comes from.
	AtBoot       *time.Time `json:"at_boot"`
	AtBootSource string     `json:"at_boot_source,omitempty"`
	// BatteryMicrovolts is /sys/class/rtc/rtc0/battery_voltage (Raspberry Pi 5), or nil when the file does not exist.
	BatteryMicrovolts *int64 `json:"battery_microvolts"`
}

// ReadRTC reads the RTC of the host below the root of p. kernelLog is the kernel log of this boot (dmesg or
// "journalctl -k -b 0"); synchronized tells whether NTP synchronized the clock. Without the kernel message, the RTC
// time now is the time at boot only while NTP did not synchronize the clock: after that the kernel writes the RTC.
func ReadRTC(p paths.Paths, kernelLog []byte, synchronized bool) RTCReading {
	r := RTCReading{Present: exists(p.Abs(RTCDevice))}
	if !r.Present {
		return r
	}
	if v, err := readInt(p.Abs(rtcBatteryUV)); err == nil {
		r.BatteryMicrovolts = &v
	}
	if t, ok := ParseHctosys(kernelLog); ok {
		r.AtBoot, r.AtBootSource = &t, rtcSourceBoot
		return r
	}
	if !synchronized {
		if v, err := readInt(p.Abs(rtcSinceEpoch)); err == nil {
			t := time.Unix(v, 0).UTC()
			r.AtBoot, r.AtBootSource = &t, rtcSourceNow
		}
	}
	return r
}

// ParseHctosys returns the RTC time of the last kernel message "setting system clock to".
func ParseHctosys(kernelLog []byte) (time.Time, bool) {
	var out time.Time
	ok := false
	sc := bufio.NewScanner(bytes.NewReader(kernelLog))
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		if m := hctosysRE.FindSubmatch(sc.Bytes()); m != nil {
			if v, err := strconv.ParseInt(string(m[1]), 10, 64); err == nil {
				out, ok = time.Unix(v, 0).UTC(), true
			}
		}
	}
	return out, ok
}

// Detection is the result of "care-pi clock detect".
type Detection struct {
	Policy string      `json:"policy"`
	Reason string      `json:"reason"`
	RTC    RTCReading  `json:"rtc"`
	Floor  time.Time   `json:"build_time"`
	At     time.Time   `json:"detected_at"`
	Config *ConfigNote `json:"config,omitempty"`
}

// ConfigNote tells what detect did with clock.policy of config.yaml.
type ConfigNote struct {
	Before  string `json:"before"`
	After   string `json:"after"`
	Written bool   `json:"written"`
	Reason  string `json:"reason"`
}

// Detect chooses the clock policy for the RTC reading: rtc only when the RTC kept a time after floor (the build time of
// the agent) through the boot, and the host does not report a battery voltage of 0.
func Detect(r RTCReading, floor, now time.Time) Detection {
	d := Detection{Policy: policyNTP, RTC: r, Floor: floor, At: now.UTC().Truncate(time.Second)}
	switch {
	case !r.Present:
		d.Reason = "the host has no RTC (" + RTCDevice + ")"
	case r.BatteryMicrovolts != nil && *r.BatteryMicrovolts < minBatteryUV:
		d.Reason = fmt.Sprintf("the RTC has no battery (battery_voltage %s)", volts(*r.BatteryMicrovolts))
	case r.AtBoot == nil:
		d.Reason = "the RTC time at boot is not known (no kernel message, and NTP synchronized the RTC)"
	case !r.AtBoot.After(floor) || !r.AtBoot.Before(floor.AddDate(10, 0, 0)):
		d.Reason = fmt.Sprintf("the RTC read %s at boot (%s), not after the build time of the agent %s (or 10 years after it): the RTC did not keep the time (no battery)",
			r.AtBoot.Format(time.RFC3339), r.AtBootSource, floor.Format(time.RFC3339))
	default:
		d.Policy = policyRTC
		d.Reason = fmt.Sprintf("the RTC kept the time through the boot: %s (%s), after the build time of the agent %s",
			r.AtBoot.Format(time.RFC3339), r.AtBootSource, floor.Format(time.RFC3339))
		if r.BatteryMicrovolts != nil {
			d.Reason += fmt.Sprintf("; battery_voltage %s", volts(*r.BatteryMicrovolts))
		}
	}
	return d
}

func volts(uv int64) string { return fmt.Sprintf("%.3f V", float64(uv)/1e6) }

// LastDetection reads state/clock-detect.json.
func LastDetection(p paths.Paths) (Detection, bool) {
	var d Detection
	data, err := os.ReadFile(p.Abs(detectFile))
	if err != nil || json.Unmarshal(data, &d) != nil {
		return Detection{}, false
	}
	return d, true
}

// SaveDetection writes state/clock-detect.json.
func SaveDetection(p paths.Paths, d Detection) error {
	return writeJSON(p, detectFile, d)
}

// OperatorSet reports whether the operator chose clock.policy: the value is not the one of the last detection, or
// (without a detection) not the default ntp.
func OperatorSet(p paths.Paths, policy string) bool {
	if last, ok := LastDetection(p); ok && last.Config != nil {
		return policy != last.Config.After
	}
	return policy != policyNTP
}

// FromRTC is the gate of the policy rtc: it reads the RTC now and passes when the time is plausible (after the build
// time of the agent). It writes the marker (source rtc, not trusted) with the uptime, so that the first NTP
// synchronization can measure the drift of the RTC.
func FromRTC(p paths.Paths, now time.Time) (time.Time, bool, error) {
	v, err := readInt(p.Abs(rtcSinceEpoch))
	if err != nil {
		return time.Time{}, false, fmt.Errorf("cannot read the RTC: %w", err)
	}
	t := time.Unix(v, 0).UTC()
	if !Plausible(t) {
		return t, false, nil
	}
	m := Marker{Trusted: false, Source: SourceRTC, SetAt: t, HostBefore: now.UTC().Truncate(time.Second), Uptime: uptime(p)}
	return t, true, write(p, m)
}

// RTCCheck is state/clock-rtc-check.json: the drift of the RTC that the first NTP synchronization measured.
type RTCCheck struct {
	CheckedAt time.Time `json:"checked_at"`
	DriftS    float64   `json:"drift_s"`
	OK        bool      `json:"ok"`
}

// LastRTCCheck reads state/clock-rtc-check.json.
func LastRTCCheck(p paths.Paths) (RTCCheck, bool) {
	var c RTCCheck
	data, err := os.ReadFile(p.Abs(rtcCheckFile))
	if err != nil || json.Unmarshal(data, &c) != nil {
		return RTCCheck{}, false
	}
	return c, true
}

// ClearRTCCheck deletes state/clock-rtc-check.json: a new detection, or the policy ntp (the RTC does not set the clock
// then, so an old drift means nothing). Without the file, the doctor shows no drift warning.
func ClearRTCCheck(p paths.Paths) error {
	if err := os.Remove(p.Abs(rtcCheckFile)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

// verifyRTC compares the time of an rtc marker with the NTP time now: the RTC time plus the uptime since the gate.
// A drift of more than RTCMaxDrift gives a warning (the doctor check clock).
func verifyRTC(p paths.Paths, m Marker, now time.Time) {
	if m.Uptime <= 0 {
		return
	}
	up := uptime(p)
	if up <= 0 {
		return
	}
	expected := m.SetAt.Add(time.Duration((up - m.Uptime) * float64(time.Second)))
	drift := now.Sub(expected)
	c := RTCCheck{CheckedAt: now.UTC().Truncate(time.Second), DriftS: float64(drift.Round(time.Second) / time.Second),
		OK: drift <= RTCMaxDrift && drift >= -RTCMaxDrift}
	_ = writeJSON(p, rtcCheckFile, c)
}

func uptime(p paths.Paths) float64 {
	data, err := os.ReadFile(p.Abs(procUptime))
	if err != nil {
		return 0
	}
	f := strings.Fields(string(data))
	if len(f) == 0 {
		return 0
	}
	v, _ := strconv.ParseFloat(f[0], 64)
	return v
}

func writeJSON(p paths.Paths, rel string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(p.Abs(paths.StateDir), 0o755); err != nil {
		return err
	}
	return lock.WriteFileAtomic(p.Abs(rel), append(data, '\n'), 0o600)
}

func readInt(path string) (int64, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	return strconv.ParseInt(strings.TrimSpace(string(data)), 10, 64)
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
