package update

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/rithviknishad/care-pi/agent/internal/apply"
	"github.com/rithviknishad/care-pi/agent/internal/config"
	"github.com/rithviknishad/care-pi/agent/internal/host"
	"github.com/rithviknishad/care-pi/agent/internal/jobs"
	"github.com/rithviknishad/care-pi/agent/internal/paths"
	"github.com/rithviknishad/care-pi/agent/internal/problem"
	"github.com/rithviknishad/care-pi/agent/internal/solution"
)

// HoldPackages are the held container packages (installer HOLD_PACKAGES).
var HoldPackages = []string{"podman", "crun", "netavark", "aardvark-dns", "conmon", "skopeo"}

// OSPackage is one package that apt can upgrade (schema OsPackage).
type OSPackage struct {
	Name      string `json:"name"`
	Installed string `json:"installed"`
	Candidate string `json:"candidate"`
	Security  bool   `json:"security"`
}

// HeldPackage is one held container package (schema HeldPackage).
type HeldPackage struct {
	Name      string  `json:"name"`
	Installed string  `json:"installed"`
	Candidate *string `json:"candidate"`
	Tested    *string `json:"tested"`
	Action    string  `json:"action"`
	versions  []string
}

// Distro is /etc/os-release.
type Distro struct {
	ID         string `json:"id"`
	Codename   string `json:"codename"`
	PrettyName string `json:"pretty_name"`
}

// OSUpdates is GET /updates/os (schema OsUpdates).
type OSUpdates struct {
	Distro                 Distro        `json:"distro"`
	SecurityAuto           bool          `json:"security_auto"`
	RebootOnWindow         bool          `json:"reboot_on_window"`
	FullOnWindow           bool          `json:"full_on_window"`
	UnattendedLastRunAt    *time.Time    `json:"unattended_last_run_at"`
	RebootRequired         bool          `json:"reboot_required"`
	RebootRequiredPackages []string      `json:"reboot_required_packages"`
	RebootScheduledAt      *time.Time    `json:"reboot_scheduled_at"`
	CheckedAt              *time.Time    `json:"checked_at"`
	Upgradable             []OSPackage   `json:"upgradable"`
	Held                   []HeldPackage `json:"held"`
	LastFullUpgrade        *HistoryEntry `json:"last_full_upgrade"`
}

// osCache is state/update-cache/os.json: the counts of the last GET /updates/os.
type osCache struct {
	Upgradable     int `json:"upgradable"`
	HeldUpgradable int `json:"held_upgradable"`
}

const osCacheFile = paths.UpdateCache + "/os.json"

// AptRun returns the command that runs apt-get in a transient unit: care-pi.service has
// ProtectSystem=strict and a memory cap of 128M (docs/solutions.md, section 11).
func AptRun(unit string, args ...string) host.Cmd {
	a := []string{"--wait", "--collect", "--pipe", "--quiet", "--unit=" + unit, "-p", "MemoryMax=512M",
		"--setenv=DEBIAN_FRONTEND=noninteractive", "--"}
	return host.Cmd{Name: "systemd-run", Args: append(a, args...)}
}

func aptGet(args ...string) host.Cmd {
	base := []string{"apt-get", "-o", "DPkg::Lock::Timeout=600", "-o", "Dpkg::Options::=--force-confdef", "-o", "Dpkg::Options::=--force-confold"}
	return AptRun("care-pi-apt", append(base, args...)...)
}

// aptMark returns "apt-mark hold|unhold PKG..." in a transient unit. It waits for the dpkg lock like
// apt-get: after a signal, an apt-get of the job can still run in its unit.
func aptMark(verb string, pkgs ...string) host.Cmd {
	return AptRun("care-pi-apt-mark", append([]string{"apt-mark", "-o", "DPkg::Lock::Timeout=600", verb}, pkgs...)...)
}

func readDistro(p paths.Paths) Distro {
	d := Distro{ID: "debian", Codename: "unknown", PrettyName: "unknown"}
	data, err := os.ReadFile(p.Abs("/etc/os-release"))
	if err != nil {
		return d
	}
	for _, l := range strings.Split(string(data), "\n") {
		k, v, ok := strings.Cut(l, "=")
		if !ok {
			continue
		}
		v = strings.Trim(v, `"`)
		switch k {
		case "ID":
			d.ID = v
		case "VERSION_CODENAME":
			d.Codename = v
		case "PRETTY_NAME":
			d.PrettyName = v
		}
	}
	return d
}

// parseUpgradable reads "apt list --upgradable": name/suite version arch [upgradable from: installed].
func parseUpgradable(out []byte) []OSPackage {
	var res []OSPackage
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		line := sc.Text()
		i := strings.Index(line, "[upgradable from: ")
		if i < 0 {
			continue
		}
		f := strings.Fields(line[:i])
		if len(f) < 2 {
			continue
		}
		name, suite, _ := strings.Cut(f[0], "/")
		inst := strings.TrimSuffix(strings.TrimPrefix(line[i:], "[upgradable from: "), "]")
		res = append(res, OSPackage{Name: name, Installed: inst, Candidate: f[1], Security: strings.Contains(suite, "security")})
	}
	return res
}

// parsePolicy reads "apt-cache policy PKG...": the candidate and the versions of each package.
func parsePolicy(out []byte) map[string][2]any {
	res := map[string][2]any{}
	var cur string
	var cand string
	var versions []string
	flush := func() {
		if cur != "" {
			res[cur] = [2]any{cand, versions}
		}
	}
	sc := bufio.NewScanner(bytes.NewReader(out))
	inTable := false
	for sc.Scan() {
		line := sc.Text()
		switch {
		case !strings.HasPrefix(line, " ") && strings.HasSuffix(line, ":"):
			flush()
			cur, cand, versions, inTable = strings.TrimSuffix(line, ":"), "", nil, false
		case strings.HasPrefix(strings.TrimSpace(line), "Candidate:"):
			cand = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "Candidate:"))
		case strings.HasPrefix(strings.TrimSpace(line), "Version table:"):
			inTable = true
		case inTable:
			f := strings.Fields(strings.TrimPrefix(strings.TrimSpace(line), "***"))
			if len(f) >= 2 && !strings.HasPrefix(line, "        ") {
				versions = append(versions, f[0])
			}
		}
	}
	flush()
	return res
}

func parseDpkg(out []byte) map[string]string {
	res := map[string]string{}
	for _, l := range strings.Split(string(out), "\n") {
		n, v, ok := strings.Cut(strings.TrimSpace(l), "\t")
		if ok && v != "" {
			res[n] = v
		}
	}
	return res
}

// held returns the state of the held packages and the tested versions of the cached index.
func (u *Updater) held(ctx context.Context, codename string) ([]HeldPackage, error) {
	r := u.Host.Runner
	out, err := r.Run(ctx, host.Cmd{Name: "dpkg-query", Args: append([]string{"-W", "-f=${Package}\t${Version}\n"}, HoldPackages...)})
	if err != nil && len(out) == 0 {
		return nil, fmt.Errorf("dpkg-query: %w", err)
	}
	inst := parseDpkg(out)
	pol, _ := r.Run(ctx, host.Cmd{Name: "apt-cache", Args: append([]string{"policy"}, HoldPackages...)})
	policy := parsePolicy(pol)
	var tested map[string]string
	if ix, _ := solution.CachedIndex(u.Paths); ix != nil && ix.OS != nil {
		tested = ix.OS.HeldPackages[codename]
	}
	var res []HeldPackage
	for _, n := range HoldPackages {
		v, ok := inst[n]
		if !ok {
			continue
		}
		hp := HeldPackage{Name: n, Installed: v, Action: "keep"}
		if p, ok := policy[n]; ok {
			if c, _ := p[0].(string); c != "" && c != "(none)" {
				hp.Candidate = &c
			}
			hp.versions, _ = p[1].([]string)
		}
		if t, ok := tested[n]; ok && t != "" {
			tt := t
			hp.Tested = &tt
			if t != v && !olderThan(hp.versions, t, v) {
				for _, av := range hp.versions {
					if av == t {
						hp.Action = "upgrade"
					}
				}
			}
		}
		res = append(res, hp)
	}
	return res, nil
}

// olderThan reports whether the version a comes after the version b in the version table of
// "apt-cache policy" (newest first): a is older. The agent never downgrades a held package, because
// index.json has no way to allow it (no --allow-downgrades).
func olderThan(table []string, a, b string) bool {
	ia, ib := -1, -1
	for i, v := range table {
		switch v {
		case a:
			ia = i
		case b:
			ib = i
		}
	}
	return ia >= 0 && ib >= 0 && ia > ib
}

// OS returns GET /updates/os. It does not run apt-get update.
func (u *Updater) OS(ctx context.Context) (*OSUpdates, error) {
	cfg, err := u.Config()
	if err != nil {
		return nil, err
	}
	st := ReadState(u.Paths)
	o := &OSUpdates{Distro: readDistro(u.Paths), SecurityAuto: cfg.Updates.OSSecurity, RebootOnWindow: cfg.Updates.OSReboot,
		FullOnWindow: cfg.Updates.OSFull, RebootScheduledAt: st.RebootScheduledAt, CheckedAt: st.OSCheckedAt,
		Upgradable: []OSPackage{}, Held: []HeldPackage{}, RebootRequiredPackages: []string{}}
	if fi, err := os.Stat(u.Paths.Abs("/var/log/unattended-upgrades/unattended-upgrades.log")); err == nil {
		o.UnattendedLastRunAt = timep(fi.ModTime().Truncate(time.Second))
	}
	if o.CheckedAt == nil {
		if fi, err := os.Stat(u.Paths.Abs("/var/lib/apt/lists")); err == nil {
			o.CheckedAt = timep(fi.ModTime().Truncate(time.Second))
		}
	}
	o.RebootRequired = fileExists(u.Paths.Abs(paths.RebootRequired))
	if data, err := os.ReadFile(u.Paths.Abs(paths.RebootRequired + ".pkgs")); err == nil {
		for _, l := range strings.Split(string(data), "\n") {
			if l = strings.TrimSpace(l); l != "" {
				o.RebootRequiredPackages = append(o.RebootRequiredPackages, l)
			}
		}
	}
	held := map[string]bool{}
	for _, n := range HoldPackages {
		held[n] = true
	}
	if out, err := u.Host.Runner.Run(ctx, host.Cmd{Name: "apt", Args: []string{"list", "--upgradable"}}); err == nil {
		for _, p := range parseUpgradable(out) {
			if !held[p.Name] {
				o.Upgradable = append(o.Upgradable, p)
			}
		}
	}
	if hp, err := u.held(ctx, o.Distro.Codename); err == nil && hp != nil {
		o.Held = hp
	}
	for _, e := range ReadHistory(u.Paths) {
		if e.Kind == HistOSUpdate {
			e := e
			o.LastFullUpgrade = &e
			break
		}
	}
	c := osCache{Upgradable: len(o.Upgradable)}
	for _, h := range o.Held {
		if h.Action == "upgrade" {
			c.HeldUpgradable++
		}
	}
	_ = writeJSON(u.Paths.Abs(osCacheFile), c)
	return o, nil
}

func (u *Updater) osSummary(cfg *config.Config, st State) OSSummary {
	var c osCache
	if data, err := os.ReadFile(u.Paths.Abs(osCacheFile)); err == nil {
		_ = jsonUnmarshal(data, &c)
	}
	s := OSSummary{SecurityAuto: cfg.Updates.OSSecurity, RebootRequired: fileExists(u.Paths.Abs(paths.RebootRequired)),
		RebootScheduledAt: st.RebootScheduledAt, UpgradableCount: c.Upgradable, HeldUpgradableCount: c.HeldUpgradable}
	for _, e := range ReadHistory(u.Paths) {
		if e.Kind == HistOSUpdate && e.Outcome == OutcomeSucceeded {
			t := e.FinishedAt
			s.LastFullUpgradeAt = &t
			break
		}
	}
	return s
}

// SecurityConf returns /etc/apt/apt.conf.d/52care-pi.conf for updates.os_security.
func SecurityConf(cfg *config.Config) string {
	on := "1"
	if !cfg.Updates.OSSecurity {
		on = "0"
	}
	return "// Managed by care-pi (updates.os_security in /etc/care-pi/config.yaml).\n" +
		"// Install security updates automatically. Never reboot by itself: Care reboots\n" +
		"// inside the update window that the operator sets.\n" +
		"Unattended-Upgrade::Automatic-Reboot \"false\";\n" +
		"APT::Periodic::Update-Package-Lists \"1\";\n" +
		"APT::Periodic::Unattended-Upgrade \"" + on + "\";\n"
}

// SyncSecurityConf writes 52care-pi.conf when it differs. The file is outside the sandbox of
// care-pi.service, so a transient unit writes it.
func (u *Updater) SyncSecurityConf(ctx context.Context, cfg *config.Config) (bool, error) {
	want := SecurityConf(cfg)
	if data, err := os.ReadFile(u.Paths.Abs(paths.AptConfCarePi)); err == nil && string(data) == want {
		return false, nil
	}
	c := AptRun("care-pi-apt-conf", "tee", paths.AptConfCarePi)
	c.Stdin = strings.NewReader(want)
	if _, err := u.Host.Runner.Run(ctx, c); err != nil {
		return false, fmt.Errorf("cannot write %s: %w", paths.AptConfCarePi, err)
	}
	return true, nil
}

// OSOptions controls OSUpdate.
type OSOptions struct {
	RebootIfRequired bool
	Source           string
	Reason           string
	// Reboot reboots the host after a delay. nil runs "systemctl reboot".
	Reboot func(delay time.Duration) error
}

// OSUpdate runs an os-update job (docs/solutions.md, section 11).
func (u *Updater) OSUpdate(ctx context.Context, rec *jobs.Recorder, o OSOptions) (res *OSUpdateResult, err error) {
	ctx, stop := u.signalContext(ctx)
	defer stop()
	start := u.now()
	cfg, err := u.Config()
	if err != nil {
		return nil, err
	}
	r := u.Host.Runner
	res = &OSUpdateResult{Kind: jobs.KindOSUpdate, Upgraded: []PackageChange{}, HeldChanged: []string{}}
	hist := HistoryEntry{JobID: rec.ID(), Kind: HistOSUpdate, Source: o.Source, Reason: strp(o.Reason), StartedAt: start}
	defer func() {
		hist.FinishedAt = u.now()
		hist.Outcome = OutcomeSucceeded
		if err != nil {
			hist.Outcome, hist.Problem = OutcomeFailed, errorsAs(err)
		}
		_ = AppendHistory(u.Paths, hist)
	}()
	failed := func(e error) error {
		if p := errorsAs(e); p != nil {
			return p
		}
		return problem.New(problem.OSUpdateFailed, e.Error())
	}
	if _, err := u.SyncSecurityConf(ctx, cfg); err != nil {
		rec.Logf("warning: %v", err)
	}
	before := u.installed(ctx)
	// ostep starts no step after a signal (StopOnSignal); os-hold runs anyway.
	ostep := func(action, name string, fn func() error) error {
		return step(rec, action, name, func() error {
			if err := interrupted(ctx); err != nil {
				return err
			}
			return stopped(ctx, fn())
		})
	}
	if err := ostep("os-refresh", "apt-get update", func() error {
		_, err := r.Run(ctx, aptGet("update"))
		if err == nil {
			now := u.now().Truncate(time.Second)
			_ = UpdateState(u.Paths, func(s *State) { s.OSCheckedAt = &now })
		}
		return err
	}); err != nil {
		return nil, failed(err)
	}
	// The full upgrade runs with the holds in place: it never moves a held package to an untested
	// candidate. Then each held package with a newer tested version goes to exactly that version.
	upErr := ostep("os-upgrade", "apt-get -y full-upgrade", func() error {
		_, err := r.Run(ctx, aptGet("-y", "full-upgrade"))
		return err
	})
	var unhold []HeldPackage
	if upErr == nil {
		upErr = ostep("os-unhold", "", func() error {
			held, err := u.held(ctx, readDistro(u.Paths).Codename)
			if err != nil {
				return err
			}
			var names []string
			for _, h := range held {
				if h.Action == "upgrade" {
					unhold = append(unhold, h)
					names = append(names, h.Name)
				} else if h.Tested != nil && *h.Tested != h.Installed {
					rec.Logf("%s stays held at %s: the tested version %s is older or not in the apt sources", h.Name, h.Installed, *h.Tested)
				}
			}
			if len(unhold) == 0 {
				rec.SetDetail("no held package has a new tested version")
				return nil
			}
			rec.SetDetail(strings.Join(names, " "))
			_, err = r.Run(ctx, aptMark("unhold", names...))
			return err
		})
	}
	if upErr == nil && len(unhold) > 0 {
		args := []string{"-y", "install"}
		for _, h := range unhold {
			args = append(args, h.Name+"="+*h.Tested)
		}
		upErr = ostep("os-upgrade", "apt-get "+strings.Join(args, " "), func() error {
			_, err := r.Run(ctx, aptGet(args...))
			return err
		})
	}
	// os-hold runs also after a failure and after a signal (StopOnSignal).
	holdErr := step(rec, "os-hold", strings.Join(HoldPackages, " "), func() error {
		_, err := r.Run(context.WithoutCancel(ctx), aptMark("hold", HoldPackages...))
		return err
	})
	if upErr != nil {
		return nil, failed(upErr)
	}
	if holdErr != nil {
		return nil, failed(holdErr)
	}
	if err := interrupted(ctx); err != nil {
		return nil, failed(err)
	}
	after := u.installed(ctx)
	for _, n := range sortedKeys(after) {
		if before[n] != after[n] {
			pc := PackageChange{Name: n, From: strp(before[n]), To: strp(after[n])}
			res.Upgraded = append(res.Upgraded, pc)
		}
	}
	for _, n := range sortedKeys(before) {
		if _, ok := after[n]; !ok {
			res.Upgraded = append(res.Upgraded, PackageChange{Name: n, From: strp(before[n])})
		}
	}
	for _, h := range unhold {
		if after[h.Name] != before[h.Name] {
			res.HeldChanged = append(res.HeldChanged, h.Name)
		}
	}
	if len(res.HeldChanged) > 0 {
		// The cycle is short (stop, start): once it starts, a signal does not leave Care stopped.
		if err := u.cycle(context.WithoutCancel(ctx), rec); err != nil {
			return nil, err
		}
		res.CareRestarted = true
	}
	res.RebootRequired = fileExists(u.Paths.Abs(paths.RebootRequired))
	if o.RebootIfRequired && res.RebootRequired {
		if err := step(rec, "reboot", "", func() error {
			if o.Reboot != nil {
				return o.Reboot(10 * time.Second)
			}
			_, err := r.Run(ctx, host.Cmd{Name: "systemctl", Args: []string{"reboot"}})
			return err
		}); err != nil {
			return nil, failed(err)
		}
		res.Rebooting = true
	}
	res.DurationS = float64(u.now().Sub(start).Milliseconds()) / 1000
	return res, nil
}

// installed returns each installed package and its version.
func (u *Updater) installed(ctx context.Context) map[string]string {
	out, _ := u.Host.Runner.Run(ctx, host.Cmd{Name: "dpkg-query", Args: []string{"-W", "-f=${Package}\t${Version}\n"}})
	return parseDpkg(out)
}

// cycle restarts care.target behind the maintenance page (a held package changed).
func (u *Updater) cycle(ctx context.Context, rec *jobs.Recorder) error {
	ao := u.applyOptions(rec.Writer())
	h := u.Host
	if err := step(rec, "cutover-maintenance-on", "", func() error {
		return apply.MaintenanceOn(u.Paths, apply.MaintenanceInfo{JobID: rec.ID(), Kind: jobs.KindOSUpdate, StartedAt: u.now().Truncate(time.Second)})
	}); err != nil {
		return err
	}
	if err := step(rec, "stop", "care.target", func() error {
		if err := h.Systemd.Stop(ctx, "care.target"); err != nil {
			return err
		}
		return apply.WaitInactive(ctx, ao, stackUnits())
	}); err != nil {
		return err
	}
	if err := step(rec, "start", "care.target", func() error { return apply.StartTarget(ctx, ao) }); err != nil {
		return err
	}
	if err := step(rec, "verify", "", func() error {
		props, err := h.Systemd.Show(ctx, []string{"care.target"}, "ActiveState")
		if err != nil {
			return err
		}
		if props[0]["ActiveState"] != "active" {
			return fmt.Errorf("care.target is %s", props[0]["ActiveState"])
		}
		return nil
	}); err != nil {
		return err
	}
	return step(rec, "maintenance-off", "", func() error { return apply.MaintenanceOff(u.Paths) })
}

// ScheduleReboot records a reboot at the next window opening (POST /updates/os/reboot with now false).
func (u *Updater) ScheduleReboot(cfg *config.Config) (time.Time, error) {
	_, _, next := u.WindowOpen(cfg)
	if next.IsZero() {
		return time.Time{}, fmt.Errorf("updates.window has no opening")
	}
	t := next.UTC()
	return t, UpdateState(u.Paths, func(s *State) { s.RebootScheduledAt = &t })
}

func jsonUnmarshal(data []byte, v any) error { return json.Unmarshal(data, v) }
