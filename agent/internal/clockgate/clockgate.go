// Package clockgate is the clock without internet (S52, docs/api/README.md section 16).
//
// The clock gate (care-clock.service, "care-pi clock wait") lets Care start only with a correct clock. A Pi without
// internet has no NTP, and a Pi 4 has no RTC. So at the claim the console sends the time of the browser
// (client_time). When NTP is not synchronized, the agent sets the clock one time from it and writes the marker
// state/clock-trusted.json (trusted false, source browser). The gate then passes after a short NTP try. The clock is
// not trusted until NTP synchronizes it: then the marker goes, and the console hides its banner.
package clockgate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"time"

	"github.com/rithviknishad/care-pi/agent/internal/buildinfo"
	"github.com/rithviknishad/care-pi/agent/internal/host"
	"github.com/rithviknishad/care-pi/agent/internal/lock"
	"github.com/rithviknishad/care-pi/agent/internal/paths"
)

// The sources of the clock (schema ClockStatus.source).
const (
	SourceNTP     = "ntp"
	SourceBrowser = "browser"
	SourceRTC     = "rtc"
	SourceNone    = "none"
)

// NTPGrace is the time that "clock wait" still waits for NTP when the browser set the clock.
const NTPGrace = 20 * time.Second

// Marker is state/clock-trusted.json.
type Marker struct {
	Trusted bool   `json:"trusted"`
	Source  string `json:"source"`
	// SetAt is the time that the agent set (the time of the browser); HostBefore is the clock of the host before.
	SetAt      time.Time `json:"set_at"`
	HostBefore time.Time `json:"host_time_before"`
	// Uptime is /proc/uptime when the RTC gate passed (source rtc, S75): the first NTP synchronization measures the
	// drift of the RTC with it.
	Uptime float64 `json:"uptime_s,omitempty"`
}

// Read reads the marker. ok is false when it does not exist.
func Read(p paths.Paths) (Marker, bool) {
	var m Marker
	data, err := os.ReadFile(p.Abs(paths.ClockTrusted))
	if err != nil || json.Unmarshal(data, &m) != nil {
		return Marker{}, false
	}
	return m, true
}

func write(p paths.Paths, m Marker) error {
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(p.Abs(paths.StateDir), 0o755); err != nil {
		return err
	}
	return lock.WriteFileAtomic(p.Abs(paths.ClockTrusted), append(data, '\n'), 0o600)
}

// Clear deletes the marker: NTP synchronized the clock.
func Clear(p paths.Paths) error {
	if err := os.Remove(p.Abs(paths.ClockTrusted)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

// Synchronized is the end of the marker: NTP synchronized the clock. When the RTC gate set the marker (policy rtc,
// S75), it records the drift of the RTC first (state/clock-rtc-check.json).
func Synchronized(p paths.Paths, now time.Time) error {
	if m, ok := Read(p); ok && m.Source == SourceRTC {
		verifyRTC(p, m, now)
	}
	return Clear(p)
}

// fallbackFloor is the earliest time of a build without a build date (a dev build): the month of Phase 5.
var fallbackFloor = time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)

// Floor returns the earliest plausible time: the build time of the agent (buildinfo.Date).
func Floor() time.Time {
	if t, err := time.Parse(time.RFC3339, buildinfo.Date); err == nil {
		return t
	}
	return fallbackFloor
}

// Plausible reports whether t can be the time now: after the build time of the agent, and before 10 years after it.
func Plausible(t time.Time) bool {
	floor := Floor()
	return t.After(floor) && t.Before(floor.AddDate(10, 0, 0))
}

// Result is the result of FromBrowser.
type Result struct {
	// Set is true when the agent set the clock. Reason tells why not, else "".
	Set    bool
	Reason string
	Marker Marker
}

// FromBrowser sets the clock from the time of the browser at the claim, one time: only when NTP is not synchronized,
// no marker exists, and t is plausible. now is the clock of the host. It writes the marker after the clock is set.
func FromBrowser(ctx context.Context, p paths.Paths, clk host.Clock, t, now time.Time) (Result, error) {
	if synced, err := clk.NTPSynchronized(ctx); err == nil && synced {
		return Result{Reason: "NTP synchronized the clock"}, nil
	}
	if _, ok := Read(p); ok {
		return Result{Reason: "the browser set the clock already"}, nil
	}
	if !Plausible(t) {
		return Result{Reason: fmt.Sprintf("the time of the browser %s is not after the build time of the agent %s, or 10 years after it",
			t.UTC().Format(time.RFC3339), Floor().Format(time.RFC3339))}, nil
	}
	if err := clk.SetTime(ctx, t); err != nil {
		return Result{}, fmt.Errorf("cannot set the clock: %w", err)
	}
	m := Marker{Trusted: false, Source: SourceBrowser, SetAt: t.UTC().Truncate(time.Second), HostBefore: now.UTC().Truncate(time.Second)}
	if err := write(p, m); err != nil {
		return Result{Set: true, Marker: m}, fmt.Errorf("the clock is set, but the agent cannot write %s: %w", paths.ClockTrusted, err)
	}
	return Result{Set: true, Marker: m}, nil
}

// State returns the source of the clock and whether it is trusted. A clock that NTP synchronized is trusted, and the
// marker goes; when the RTC gate set the marker (policy rtc, S75), the drift of the RTC is recorded first. rtc means
// that the host has an RTC (/dev/rtc0) but no NTP: not trusted until NTP confirms it.
func State(ctx context.Context, p paths.Paths, clk host.Clock) (source string, trusted, synchronized bool) {
	if clk != nil {
		synchronized, _ = clk.NTPSynchronized(ctx)
	}
	if synchronized {
		_ = Synchronized(p, time.Now())
		return SourceNTP, true, true
	}
	if m, ok := Read(p); ok && (m.Source == SourceBrowser || m.Source == SourceRTC) {
		return m.Source, m.Trusted, false
	}
	if _, err := os.Stat(p.Abs("/dev/rtc0")); err == nil {
		return SourceRTC, false, false
	}
	return SourceNone, false, false
}

// CaddyReload makes Caddy read its config and check its certificates again. --force is necessary: Caddy skips the
// reload of a config that did not change.
var CaddyReload = []string{"caddy", "reload", "--config", "/etc/caddy/Caddyfile", "--adapter", "caddyfile", "--force"}

// ReloadCaddy reloads care-caddy after a clock jump: its local leaf certificates (12 h) and its intermediate were
// issued with the old clock. restart restarts care-caddy (when it runs) when the reload fails; the claim does not
// restart it, because the answer of the claim goes through Caddy.
func ReloadCaddy(ctx context.Context, h *host.Host, restart bool, w io.Writer) {
	if _, err := h.Podman.Exec(ctx, "care-caddy", "", CaddyReload...); err == nil {
		_, _ = fmt.Fprintln(w, "clock: reloaded Caddy")
		return
	} else if !restart {
		_, _ = fmt.Fprintf(w, "clock: warning: cannot reload Caddy: %v\n", err)
		return
	}
	// try-restart does not start a Caddy that does not run.
	if err := h.Systemd.TryRestart(ctx, "care-caddy.service"); err != nil {
		_, _ = fmt.Fprintf(w, "clock: warning: cannot restart care-caddy.service: %v\n", err)
	}
}
