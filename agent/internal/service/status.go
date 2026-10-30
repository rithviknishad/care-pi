package service

import (
	"context"
	"os"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/rithviknishad/care-pi/agent/internal/applock"
	"github.com/rithviknishad/care-pi/agent/internal/buildinfo"
	"github.com/rithviknishad/care-pi/agent/internal/config"
	"github.com/rithviknishad/care-pi/agent/internal/fe"
	"github.com/rithviknishad/care-pi/agent/internal/jobs"
	"github.com/rithviknishad/care-pi/agent/internal/netinfo"
	"github.com/rithviknishad/care-pi/agent/internal/paths"
	"github.com/rithviknishad/care-pi/agent/internal/setup"
	"github.com/rithviknishad/care-pi/agent/internal/status"
)

// UnitStatus is the state of one unit (schema UnitStatus).
type UnitStatus struct {
	Unit          string  `json:"unit"`
	ActiveState   string  `json:"active_state"`
	SubState      string  `json:"sub_state"`
	Health        *string `json:"health"`
	MemoryCurrent *uint64 `json:"memory_current_bytes"`
	MemoryPeak    *uint64 `json:"memory_peak_bytes"`
	MemoryMax     *uint64 `json:"memory_max_bytes"`
	Restarts      int     `json:"restarts"`
	LogUnit       *string `json:"log_unit"`
}

// Flags are the named conditions of the dashboard (schema StatusFlags).
type Flags struct {
	Degraded         bool `json:"degraded"`
	AddressChanged   bool `json:"address_changed"`
	ClockWait        bool `json:"clock_wait"`
	Maintenance      bool `json:"maintenance"`
	FEMissing        bool `json:"fe_missing"`
	FEScaleDownLeft  bool `json:"fe_scale_down_left"`
	LowDisk          bool `json:"low_disk"`
	LowMemory        bool `json:"low_memory"`
	MDNSCollision    bool `json:"mdns_collision"`
	HostnameMismatch bool `json:"hostname_mismatch"`
	LastJobFailed    bool `json:"last_job_failed"`
	RecoveryCodesLow bool `json:"recovery_codes_low"`
	HostCheckFailed  bool `json:"host_check_failed"`
}

// AgentInfo is the agent binary (schema AgentInfo).
type AgentInfo struct {
	Version   string    `json:"version"`
	Commit    string    `json:"commit"`
	Date      string    `json:"date"`
	DevBuild  bool      `json:"dev_build"`
	StartedAt time.Time `json:"started_at"`
}

// MemoryStatus is /proc/meminfo in bytes (schema MemoryStatus).
type MemoryStatus struct {
	Total     int64 `json:"total_bytes"`
	Available int64 `json:"available_bytes"`
	SwapTotal int64 `json:"swap_total_bytes"`
	SwapFree  int64 `json:"swap_free_bytes"`
}

// HostStatus is the host resources (schema HostStatus).
type HostStatus struct {
	Model         *string      `json:"model"`
	Arch          string       `json:"arch"`
	Kernel        string       `json:"kernel"`
	OS            string       `json:"os"`
	PageSizeBytes int          `json:"page_size_bytes"`
	CPUCount      int          `json:"cpu_count"`
	LoadAverage   [3]float64   `json:"load_average"`
	UptimeS       int64        `json:"uptime_s"`
	Memory        MemoryStatus `json:"memory"`
}

// DiskStatus is one file system (schema DiskStatus).
type DiskStatus struct {
	Mount      string `json:"mount"`
	Device     string `json:"device"`
	Kind       string `json:"kind"`
	TotalBytes int64  `json:"total_bytes"`
	FreeBytes  int64  `json:"free_bytes"`
}

// ClockStatus is the clock and the gate (schema ClockStatus).
type ClockStatus struct {
	Policy       string    `json:"policy"`
	Synchronized bool      `json:"synchronized"`
	Gate         string    `json:"gate"`
	Time         time.Time `json:"time"`
	Timezone     string    `json:"timezone"`
	RTCPresent   bool      `json:"rtc_present"`
}

// NetworkSummary is the network part of the status (schema NetworkSummary).
type NetworkSummary struct {
	MDNSConfigured    string  `json:"mdns_configured"`
	MDNSAdvertised    *string `json:"mdns_advertised"`
	InternetReachable *bool   `json:"internet_reachable"`
}

// TLSSummary is the TLS part of the status (schema TlsSummary).
type TLSSummary struct {
	LocalCA struct {
		Enabled     bool       `json:"enabled"`
		Fingerprint *string    `json:"fingerprint_sha256"`
		NotAfter    *time.Time `json:"not_after"`
	} `json:"local_ca"`
	Public struct {
		Enabled  bool    `json:"enabled"`
		Hostname *string `json:"hostname"`
	} `json:"public"`
	Tailscale struct {
		Enabled  bool    `json:"enabled"`
		Hostname *string `json:"hostname"`
	} `json:"tailscale"`
}

// FESummary is the FE part of the status (schema FeSummary).
type FESummary struct {
	Provider string  `json:"provider"`
	BuildID  *string `json:"build_id"`
	Current  *string `json:"current"`
	Building bool    `json:"building"`
}

// LockSummary is the header of lock.yaml (schema LockSummary).
type LockSummary struct {
	GeneratedAt  string `json:"generated_at"`
	AgentVersion string `json:"agent_version"`
	Solution     struct {
		Name  string `json:"name"`
		Track string `json:"track"`
	} `json:"solution"`
}

// JobsSummary is the running job and the last job (schema JobsSummary).
type JobsSummary struct {
	Running *jobs.Summary `json:"running"`
	Last    *jobs.Summary `json:"last"`
}

// Status is everything that the dashboard shows (schema Status). The first 11 members are
// the members of "care-pi status --json" of Phase 1; an empty member is null or [] now.
type Status struct {
	Hostnames  []string       `json:"hostnames"`
	Addresses  []string       `json:"addresses"`
	Published  []string       `json:"published"`
	Units      []UnitStatus   `json:"units"`
	Images     []status.Image `json:"images"`
	CareFESHA  *string        `json:"care_fe_sha"`
	FEBuildID  *string        `json:"fe_build_id"`
	LockedAt   *string        `json:"locked_at"`
	Degraded   bool           `json:"degraded"`
	Problems   []string       `json:"problems"`
	Mode       []string       `json:"mode"`
	Generated  time.Time      `json:"generated_at"`
	State      string         `json:"state"`
	Flags      Flags          `json:"flags"`
	Agent      AgentInfo      `json:"agent"`
	SetupState string         `json:"setup_state"`
	Host       HostStatus     `json:"host"`
	Disks      []DiskStatus   `json:"disks"`
	Clock      ClockStatus    `json:"clock"`
	Network    NetworkSummary `json:"network"`
	TLS        TLSSummary     `json:"tls"`
	FE         FESummary      `json:"fe"`
	Lock       *LockSummary   `json:"lock"`
	Jobs       JobsSummary    `json:"jobs"`
	Backups    any            `json:"backups"`
	Updates    any            `json:"updates"`
	// Report is the Phase 1 report. care-pi status prints it as text.
	Report *status.Report `json:"-"`
}

// logUnits maps a unit to its short name for GET /logs/{unit}.
func logUnit(unit string) *string {
	var n string
	switch unit {
	case "care.target":
		n = "target"
	case paths.AgentUnit:
		n = "agent"
	default:
		n = strings.TrimSuffix(strings.TrimPrefix(unit, "care-"), ".service")
	}
	for _, u := range LogUnits {
		if u.Name == n {
			return &n
		}
	}
	return nil
}

func nonZero(v uint64) *uint64 {
	if v == 0 {
		return nil
	}
	return &v
}

func healthOf(h string) *string {
	switch h {
	case "healthy", "unhealthy", "starting":
		return &h
	}
	return nil
}

// AgentInfo returns the version of the agent.
func (s *Service) AgentInfo() AgentInfo {
	return AgentInfo{Version: buildinfo.Version, Commit: buildinfo.Commit, Date: buildinfo.Date, DevBuild: DevBuild(),
		StartedAt: s.StartedAt.UTC().Truncate(time.Second)}
}

func (s *Service) hostStatus() HostStatus {
	h := HostStatus{Model: boardModel(s.Paths), Arch: Arch(), Kernel: readTrim(s.Paths.Abs("/proc/sys/kernel/osrelease")),
		PageSizeBytes: os.Getpagesize(), CPUCount: runtime.NumCPU()}
	if h.Kernel == "" {
		h.Kernel = runtime.GOOS
	}
	h.OS = runtime.GOOS
	if data, err := os.ReadFile(s.Paths.Abs("/etc/os-release")); err == nil {
		for _, l := range strings.Split(string(data), "\n") {
			if v, ok := strings.CutPrefix(l, "PRETTY_NAME="); ok {
				h.OS = strings.Trim(v, `"'`)
			}
		}
	}
	if f := strings.Fields(readTrim(s.Paths.Abs("/proc/loadavg"))); len(f) >= 3 {
		for i := range 3 {
			h.LoadAverage[i], _ = strconv.ParseFloat(f[i], 64)
		}
	}
	if f := strings.Fields(readTrim(s.Paths.Abs("/proc/uptime"))); len(f) >= 1 {
		up, _ := strconv.ParseFloat(f[0], 64)
		h.UptimeS = int64(up)
	}
	m := meminfo(s.Paths)
	h.Memory = MemoryStatus{Total: m["MemTotal"], Available: m["MemAvailable"], SwapTotal: m["SwapTotal"], SwapFree: m["SwapFree"]}
	return h
}

func (s *Service) disks() []DiskStatus {
	out := []DiskStatus{}
	var firstDev uint64
	for i, mount := range []string{"/", paths.DataDir} {
		abs := s.Paths.Abs(mount)
		var st syscall.Stat_t
		if err := syscall.Stat(abs, &st); err != nil {
			continue
		}
		if i > 0 && uint64(st.Dev) == firstDev { // Dev is int32 on macOS.
			continue
		}
		if i == 0 {
			firstDev = uint64(st.Dev) // Dev is int32 on macOS.
		}
		total, free, err := statfs(abs)
		if err != nil {
			continue
		}
		dev := mountDevice(s.Paths, mount)
		d := DiskStatus{Mount: mount, Device: dev, Kind: diskKind(dev), TotalBytes: total, FreeBytes: free}
		if d.Device == "" {
			d.Device = "unknown"
		}
		out = append(out, d)
	}
	return out
}

func timezone(p paths.Paths) string {
	if tz := readTrim(p.Abs("/etc/timezone")); tz != "" {
		return tz
	}
	if l, err := os.Readlink(p.Abs("/etc/localtime")); err == nil {
		if _, tz, ok := strings.Cut(l, "zoneinfo/"); ok {
			return tz
		}
	}
	return "UTC"
}

// mdnsAdvertised returns the name that avahi announces, from its journal, or nil.
func (s *Service) mdnsAdvertised(ctx context.Context) *string {
	out, err := s.Host.Runner.Run(ctx, hostCmd("journalctl", "--unit", "avahi-daemon.service", "--output", "cat",
		"--no-pager", "--lines", "300"))
	if err != nil {
		return nil
	}
	var name string
	for _, l := range strings.Split(string(out), "\n") {
		if _, rest, ok := strings.Cut(l, "Host name is "); ok {
			name = strings.TrimSuffix(strings.Fields(rest)[0], ".")
		}
	}
	if name == "" {
		return nil
	}
	return &name
}

func (s *Service) clockStatus(ctx context.Context, cfg *config.Config) ClockStatus {
	c := ClockStatus{Policy: cfg.Clock.Policy, Gate: "passed", Time: s.now().Truncate(time.Second), Timezone: timezone(s.Paths),
		RTCPresent: fileExists(s.Paths.Abs("/dev/rtc0"))}
	if s.Host != nil && s.Host.Clock != nil {
		c.Synchronized, _ = s.Host.Clock.NTPSynchronized(ctx)
	}
	if fileExists(s.Paths.Abs(paths.ModeDir + "/" + paths.ModeClockWait)) {
		c.Gate = "waiting"
	}
	return c
}

func (s *Service) tlsSummary(cfg *config.Config) TLSSummary {
	var t TLSSummary
	t.LocalCA.Enabled = cfg.TLS.LocalCA.Enabled
	if cert, der, err := s.rootCA(); err == nil {
		fp, _ := fingerprints(der)
		na := cert.NotAfter.UTC()
		t.LocalCA.Fingerprint, t.LocalCA.NotAfter = &fp, &na
	}
	t.Public.Enabled = cfg.TLS.Public.Enabled
	t.Tailscale.Enabled = cfg.TLS.Tailscale.Enabled
	return t
}

// FEBuilding reports whether an FE build runs now (its build lock is held).
func (s *Service) FEBuilding() bool {
	f, err := os.OpenFile(s.Paths.Abs(fe.BuildLockFile), os.O_RDWR, 0o600)
	if err != nil {
		return false
	}
	defer func() { _ = f.Close() }()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return true
	}
	_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	return false
}

func (s *Service) jobsSummary() JobsSummary {
	var out JobsSummary
	if j := jobs.Latest(s.Paths, func(j *jobs.Job) bool { return j.State == jobs.StateRunning }); j != nil {
		sum := j.Summary()
		out.Running = &sum
	}
	if j := jobs.Latest(s.Paths, func(j *jobs.Job) bool { return j.Done() }); j != nil {
		sum := j.Summary()
		out.Last = &sum
	}
	return out
}

// Status returns the full status. The agent keeps it for 2 s.
func (s *Service) Status(ctx context.Context) (*Status, error) {
	s.mu.Lock()
	if v, ok := s.statusCache.get(s.now(), 2*time.Second); ok {
		s.mu.Unlock()
		return v, nil
	}
	s.mu.Unlock()
	cfg, err := config.Load(s.configPath())
	if err != nil {
		cfg = config.Default()
	}
	rep, err := status.Collect(ctx, s.Host, s.Paths, s.Hostnames(), s.IPs)
	if err != nil {
		return nil, err
	}
	st := &Status{Hostnames: nonNil(rep.Hostnames), Addresses: nonNil(rep.Addresses), Published: nonNil(rep.Published),
		Units: []UnitStatus{}, Images: rep.Images, Degraded: rep.Degraded, Problems: nonNil(rep.Problems),
		Mode: nonNil(rep.Mode), Generated: s.now().Truncate(time.Second), Agent: s.AgentInfo(),
		SetupState: s.SetupStateName(), Host: s.hostStatus(), Disks: s.disks(), Report: rep}
	if st.Images == nil {
		st.Images = []status.Image{}
	}
	for _, u := range rep.Units {
		st.Units = append(st.Units, UnitStatus{Unit: u.Unit, ActiveState: u.ActiveState, SubState: u.SubState,
			Health: healthOf(u.Health), MemoryCurrent: nonZero(u.MemoryCurrent), MemoryPeak: nonZero(u.MemoryPeak),
			MemoryMax: nonZero(u.MemoryMax), Restarts: u.Restarts, LogUnit: logUnit(u.Unit)})
	}
	st.CareFESHA, st.FEBuildID, st.LockedAt = strPtr(rep.CareFESHA), strPtr(rep.FEBuildID), strPtr(rep.Locked)
	st.Clock = s.clockStatus(ctx, cfg)
	st.Network = NetworkSummary{MDNSConfigured: netinfo.MDNSName(cfg.Identity.Hostname), MDNSAdvertised: s.mdnsAdvertised(ctx)}
	if s.Internet != nil {
		st.Network.InternetReachable, _ = s.Internet.State()
	}
	st.TLS = s.tlsSummary(cfg)
	st.FE = FESummary{Provider: cfg.FE.Provider, BuildID: st.FEBuildID, Current: strPtr(fe.Current(s.Paths)), Building: s.FEBuilding()}
	if lk := rep.Lock; lk != nil {
		ls := &LockSummary{GeneratedAt: lk.GeneratedAt, AgentVersion: lk.AgentVersion}
		ls.Solution.Name, ls.Solution.Track = lk.Solution.Name, lk.Solution.Track
		st.Lock = ls
	}
	st.Jobs = s.jobsSummary()

	f := &st.Flags
	f.Degraded = st.Degraded
	f.AddressChanged = strings.Join(st.Addresses, " ") != strings.Join(st.Published, " ")
	for _, m := range st.Mode {
		f.ClockWait = f.ClockWait || m == paths.ModeClockWait
		f.Maintenance = f.Maintenance || m == paths.ModeMaintenance
	}
	f.FEMissing = cfg.FE.Provider != "none" && (st.FEBuildID == nil || st.FE.Current == nil || *st.FE.Current != *st.FEBuildID)
	f.FEScaleDownLeft = fe.ScaleDownLeft(s.Paths) && !st.FE.Building && !applock.Held(s.Paths)
	for _, d := range st.Disks {
		if d.Mount == paths.DataDir || len(st.Disks) == 1 {
			f.LowDisk = d.TotalBytes > 0 && (d.FreeBytes < 2<<30 || d.FreeBytes*10 < d.TotalBytes)
		}
	}
	f.LowMemory = st.Host.Memory.Total > 0 && st.Host.Memory.Available < 256<<20
	f.MDNSCollision = st.Network.MDNSAdvertised != nil && *st.Network.MDNSAdvertised != st.Network.MDNSConfigured
	if s.Paths.Root == "/" || s.Paths.Root == "" {
		if h, err := os.Hostname(); err == nil {
			f.HostnameMismatch = strings.SplitN(h, ".", 2)[0] != cfg.Identity.Hostname
		}
	}
	if j := jobs.Latest(s.Paths, func(j *jobs.Job) bool {
		return j.Done() && (j.Kind == jobs.KindApply || j.Kind == jobs.KindSetup || j.Kind == jobs.KindFEBuild)
	}); j != nil {
		f.LastJobFailed = j.State == jobs.StateFailed
	}
	if a := s.Auth.Status(); s.Auth.Claimed() {
		f.RecoveryCodesLow = a.Remaining <= 2
	}
	f.HostCheckFailed = !s.hostCheck(ctx).OK()

	units := map[string]string{}
	for _, u := range st.Units {
		units[u.Unit] = u.ActiveState
	}
	switch {
	case st.SetupState != setup.Ready:
		st.State = "setup"
	case f.Maintenance:
		st.State = "maintenance"
	case f.ClockWait:
		st.State = "clock-wait"
	case units["care.target"] == "activating":
		st.State = "starting"
	case st.Degraded:
		st.State = "degraded"
	default:
		st.State = "ok"
	}
	s.mu.Lock()
	s.statusCache.set(s.now(), st)
	s.mu.Unlock()
	return st, nil
}

// PublicJobProgress is the progress of a job that changes the host (schema PublicJobProgress).
type PublicJobProgress struct {
	Kind      string    `json:"kind"`
	Step      *string   `json:"step"`
	StepIndex *int      `json:"step_index"`
	StepCount *int      `json:"step_count"`
	StartedAt time.Time `json:"started_at"`
}

// PublicStatus is the minimal public status (schema PublicStatus).
type PublicStatus struct {
	Site         string   `json:"site"`
	SetupState   string   `json:"setup_state"`
	ClaimMethods []string `json:"claim_methods"`
	DisplayName  string   `json:"display_name"`
	ConsolePort  int      `json:"console_port"`
	Clock        struct {
		Policy       string `json:"policy"`
		Synchronized bool   `json:"synchronized"`
	} `json:"clock"`
	CareHealthy bool               `json:"care_healthy"`
	Job         *PublicJobProgress `json:"job"`
	Time        time.Time          `json:"time"`
}

// Site returns the page that Caddy serves at / now (docs/apply.md, "Status pages and modes").
func (s *Service) Site() string {
	switch {
	case fileExists(s.Paths.Abs(paths.ModeDir + "/" + paths.ModeClockWait)):
		return "clock-wait"
	case fileExists(s.Paths.Abs(paths.ModeDir + "/" + paths.ModeMaintenance)):
		return "maintenance"
	case fileExists(s.Paths.Abs(paths.FECurrent + "/index.html")):
		return "care"
	}
	return "setup"
}

// PublicStatus returns the public status. The agent keeps it for 2 s.
func (s *Service) PublicStatus(ctx context.Context) *PublicStatus {
	s.mu.Lock()
	if v, ok := s.publicCache.get(s.now(), 2*time.Second); ok {
		s.mu.Unlock()
		return v
	}
	s.mu.Unlock()
	cfg, err := config.Load(s.configPath())
	if err != nil {
		cfg = config.Default()
	}
	p := &PublicStatus{Site: s.Site(), SetupState: s.SetupStateName(), ClaimMethods: s.Auth.ClaimMethods(),
		DisplayName: cfg.Identity.DisplayName, ConsolePort: paths.ConsolePort, CareHealthy: s.careHealthy(ctx),
		Time: s.now().Truncate(time.Second)}
	p.Clock.Policy = cfg.Clock.Policy
	if s.Host != nil && s.Host.Clock != nil {
		p.Clock.Synchronized, _ = s.Host.Clock.NTPSynchronized(ctx)
	}
	if j := jobs.Latest(s.Paths, func(j *jobs.Job) bool { return j.State == jobs.StateRunning && jobs.LockKinds[j.Kind] }); j != nil {
		jp := &PublicJobProgress{Kind: j.Kind, StartedAt: j.CreatedAt}
		n := len(j.Steps)
		jp.StepCount = &n
		for i := len(j.Steps) - 1; i >= 0; i-- {
			if j.Steps[i].State == jobs.StepRunning {
				name, idx := j.Steps[i].Name, i
				jp.Step, jp.StepIndex = &name, &idx
				break
			}
		}
		if j.StartedAt != nil {
			jp.StartedAt = *j.StartedAt
		}
		p.Job = jp
	}
	s.mu.Lock()
	s.publicCache.set(s.now(), p)
	s.mu.Unlock()
	return p
}
