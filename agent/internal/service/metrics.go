package service

import (
	"context"
	"log/slog"
	"os"
	"time"

	"github.com/rithviknishad/care-pi/agent/internal/buildinfo"
	"github.com/rithviknishad/care-pi/agent/internal/config"
	"github.com/rithviknishad/care-pi/agent/internal/firewall"
	"github.com/rithviknishad/care-pi/agent/internal/metrics"
	"github.com/rithviknishad/care-pi/agent/internal/paths"
)

// TextfileName is the file of the agent in the text file directory of node_exporter (telemetry, Phase 7).
const TextfileName = "care_pi.prom"

func unixOf(t *time.Time) float64 {
	if t == nil || t.IsZero() {
		return 0
	}
	return float64(t.Unix())
}

// Metrics returns the metrics of the agent in the Prometheus text format (GET /api/v1/metrics, Phase 7): the units
// of Care (state, memory, restarts), the host (memory, load, disks), the clock, the backups, the certificates, the
// updates, and the firewall. The telemetry sends the same set through node_exporter (docs/telemetry.md).
func (s *Service) Metrics(ctx context.Context) (*metrics.Set, error) {
	st, err := s.Status(ctx)
	if err != nil {
		return nil, err
	}
	cfg, err := config.Load(s.configPath())
	if err != nil {
		cfg = config.Default()
	}
	m := metrics.New()
	m.Gauge("care_pi_info", "The agent version, the solution, and its track.", 1,
		"version", buildinfo.Version, "solution", cfg.Solution.Name, "track", cfg.Solution.Track, "setup_state", st.SetupState)
	m.Gauge("care_pi_state", "1 for the state of the device (ok, degraded, starting, maintenance, clock-wait, setup).", 1, "state", st.State)
	m.Bool("care_pi_degraded", "A unit of Care is not active or not healthy.", st.Degraded)
	for _, u := range st.Units {
		m.Bool("care_pi_unit_active", "The unit is active.", u.ActiveState == "active", "unit", u.Unit)
	}
	for _, u := range st.Units {
		if u.MemoryCurrent != nil {
			m.Gauge("care_pi_unit_memory_bytes", "The memory of the cgroup of the unit (MemoryCurrent).", float64(*u.MemoryCurrent), "unit", u.Unit)
		}
	}
	for _, u := range st.Units {
		if u.MemoryPeak != nil {
			m.Gauge("care_pi_unit_memory_peak_bytes", "The memory peak of the cgroup since the start of the unit (MemoryPeak).", float64(*u.MemoryPeak), "unit", u.Unit)
		}
	}
	for _, u := range st.Units {
		if u.MemoryMax != nil {
			m.Gauge("care_pi_unit_memory_max_bytes", "The memory cap of the unit (MemoryMax).", float64(*u.MemoryMax), "unit", u.Unit)
		}
	}
	for _, u := range st.Units {
		m.Counter("care_pi_unit_restarts_total", "The automatic restarts of the unit (NRestarts).", float64(u.Restarts), "unit", u.Unit)
	}
	h := st.Host
	m.Gauge("care_pi_host_memory_total_bytes", "MemTotal of /proc/meminfo.", float64(h.Memory.Total))
	m.Gauge("care_pi_host_memory_available_bytes", "MemAvailable of /proc/meminfo.", float64(h.Memory.Available))
	m.Gauge("care_pi_host_swap_total_bytes", "SwapTotal of /proc/meminfo (zram).", float64(h.Memory.SwapTotal))
	m.Gauge("care_pi_host_swap_free_bytes", "SwapFree of /proc/meminfo.", float64(h.Memory.SwapFree))
	for i, w := range []string{"1m", "5m", "15m"} {
		m.Gauge("care_pi_host_load", "The load average.", h.LoadAverage[i], "window", w)
	}
	m.Gauge("care_pi_host_uptime_seconds", "The uptime of the host.", float64(h.UptimeS))
	m.Gauge("care_pi_host_cpus", "The CPU count.", float64(h.CPUCount))
	for _, d := range st.Disks {
		m.Gauge("care_pi_disk_total_bytes", "The size of a file system of care-pi.", float64(d.TotalBytes), "mount", d.Mount)
	}
	for _, d := range st.Disks {
		m.Gauge("care_pi_disk_free_bytes", "The free space of a file system of care-pi.", float64(d.FreeBytes), "mount", d.Mount)
	}
	m.Bool("care_pi_clock_synchronized", "NTP synchronized the clock.", st.Clock.Synchronized)
	m.Bool("care_pi_clock_trusted", "The clock is trusted (S52).", st.Clock.Trusted)
	if b, err := s.Backups(ctx); err == nil && b != nil {
		m.Gauge("care_pi_backup_local_last_full_timestamp_seconds", "The end of the last full backup of the local pgBackRest repository.", unixOf(b.Local.LastFullAt))
		m.Gauge("care_pi_backup_local_last_incr_timestamp_seconds", "The end of the last incremental backup.", unixOf(b.Local.LastIncrAt))
		m.Gauge("care_pi_backup_local_last_wal_timestamp_seconds", "The last archived WAL segment.", unixOf(b.Local.LastWALArchivedAt))
		for _, d := range b.Destinations {
			m.Gauge("care_pi_backup_last_snapshot_timestamp_seconds", "The last snapshot of a destination (0: none).", unixOf(d.LastSnapshotAt), "destination", d.ID, "type", d.Type)
		}
		for _, d := range b.Destinations {
			m.Bool("care_pi_backup_failing", "The last run of a destination failed.", d.FailingSince != nil, "destination", d.ID, "type", d.Type)
		}
		m.Gauge("care_pi_backup_alert_after_seconds", "backups.alert_after_hours.", float64(cfg.Backups.AlertAfterHours*3600))
	}
	t := s.TLS(ctx)
	if t.LocalCA.Root != nil {
		m.Gauge("care_pi_certificate_expiry_timestamp_seconds", "The end of a certificate.", float64(t.LocalCA.Root.NotAfter.Unix()), "kind", "local-root", "name", "local CA")
	}
	if t.Public.Certificate != nil && t.Public.Hostname != nil && t.Public.Certificate.NotAfter != nil {
		m.Gauge("care_pi_certificate_expiry_timestamp_seconds", "The end of a certificate.", float64(t.Public.Certificate.NotAfter.Unix()), "kind", "public", "name", *t.Public.Hostname)
	}
	if sum := s.UpdatesSummary(ctx); sum != nil {
		m.Bool("care_pi_update_available", "A newer lock is available on the track.", sum.UpdateAvailable)
		m.Bool("care_pi_os_reboot_required", "/run/reboot-required exists.", sum.OSRebootRequired)
	}
	live := firewall.Read(s.Paths).Policy
	if live == "" {
		live = "none"
	}
	m.Gauge("care_pi_firewall_policy", "1 for the loaded policy of the table inet care-pi (none: not loaded by the agent).", 1, "policy", live)
	return m, nil
}

// WriteTextfile writes the metrics into the text file directory of node_exporter while telemetry is on, and
// deletes the file while it is off. "care-pi serve" calls it at each interval.
func (s *Service) WriteTextfile(ctx context.Context) error {
	cfg, err := config.Load(s.configPath())
	f := s.Paths.Abs(paths.TelemetryTextfileDir + "/" + TextfileName)
	if err != nil || !cfg.Telemetry.Enabled {
		if rmErr := os.Remove(f); rmErr != nil && !os.IsNotExist(rmErr) {
			return rmErr
		}
		return nil
	}
	set, err := s.Metrics(ctx)
	if err != nil {
		return err
	}
	return set.WriteFile(f)
}

// RunTextfile writes the text file each interval until ctx ends.
func (s *Service) RunTextfile(ctx context.Context, interval time.Duration, log *slog.Logger) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		if err := s.WriteTextfile(ctx); err != nil && ctx.Err() == nil {
			log.Warn("telemetry text file", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
