package service

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/rithviknishad/care-pi/agent/internal/config"
	"github.com/rithviknishad/care-pi/agent/internal/hostcheck"
	"github.com/rithviknishad/care-pi/agent/internal/paths"
	"github.com/rithviknishad/care-pi/agent/internal/setup"
)

// Doctor statuses of a check.
const (
	DoctorOK   = "ok"
	DoctorWarn = "warn"
	DoctorFail = "fail"
	DoctorSkip = "skip"
)

// DoctorCheck is one check of care-pi doctor (schema DoctorCheck, Phase 7).
type DoctorCheck struct {
	ID     string  `json:"id"`
	Title  string  `json:"title"`
	Status string  `json:"status"`
	Detail string  `json:"detail"`
	Fix    *string `json:"fix"`
}

// DoctorReport is the result of care-pi doctor and GET /api/v1/doctor (schema DoctorReport).
type DoctorReport struct {
	GeneratedAt time.Time     `json:"generated_at"`
	Status      string        `json:"status"`
	Checks      []DoctorCheck `json:"checks"`
}

// CertWarnDays is the time before the end of a certificate when doctor warns.
const CertWarnDays = 14

// conflictPorts are the ports that the pod publishes.
var conflictPorts = []int{80, 443, paths.ConsolePort}

// podListeners are the programs that hold the ports of the pod (rootful podman reserves the published ports in conmon).
var podListeners = map[string]bool{"conmon": true, "podman": true, "rootlessport": true, "netavark": true}

// Listener is one listening TCP socket of "ss -ltnpH".
type Listener struct {
	Addr    string
	Port    int
	Process string
}

// ParseSS reads the output of "ss -ltnpH": state, recv-q, send-q, local address:port, peer, process.
func ParseSS(out string) []Listener {
	var res []Listener
	sc := bufio.NewScanner(strings.NewReader(out))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) < 4 {
			continue
		}
		local := f[3]
		i := strings.LastIndex(local, ":")
		if i < 0 {
			continue
		}
		port, err := strconv.Atoi(local[i+1:])
		if err != nil {
			continue
		}
		addr := strings.Trim(local[:i], "[]")
		if j := strings.Index(addr, "%"); j >= 0 {
			addr = addr[:j]
		}
		proc := ""
		if k := strings.Index(sc.Text(), `users:(("`); k >= 0 {
			rest := sc.Text()[k+len(`users:(("`):]
			proc, _, _ = strings.Cut(rest, `"`)
		}
		res = append(res, Listener{Addr: addr, Port: port, Process: proc})
	}
	return res
}

// PortConflicts returns the listeners of other programs on a port of the pod, on a published address or on all
// addresses. tailscaled of "tailscale serve" on 443 of the Tailscale address is the case of SP5.
func PortConflicts(ls []Listener, published []string) []Listener {
	pub := map[string]bool{"0.0.0.0": true, "*": true, "::": true, "": true}
	for _, a := range published {
		pub[a] = true
	}
	var out []Listener
	for _, l := range ls {
		if podListeners[l.Process] || !pub[l.Addr] {
			continue
		}
		for _, p := range conflictPorts {
			if l.Port == p {
				out = append(out, l)
			}
		}
	}
	return out
}

func fix(s string) *string { return &s }

// Doctor runs the health checks of care-pi doctor (Phase 7, plans/09-handoff.md section 3): the host check, the ports
// of the pod, the mDNS name, the clock, the disk, the memory and zram, the units, the certificates, the backups, the
// firewall, the reboot, and the updates. Each check has a fix hint. It changes nothing.
func (s *Service) Doctor(ctx context.Context) (*DoctorReport, error) {
	st, err := s.Status(ctx)
	if err != nil {
		return nil, err
	}
	cfg, err := config.Load(s.configPath())
	if err != nil {
		cfg = config.Default()
	}
	now := s.now()
	rep := &DoctorReport{GeneratedAt: now.Truncate(time.Second), Checks: []DoctorCheck{}}
	add := func(c DoctorCheck) { rep.Checks = append(rep.Checks, c) }

	// The host check of the installer and bootstrap (cgroup memory controller, podman, …).
	for _, c := range s.hostCheck(ctx).Checks {
		d := DoctorCheck{ID: "host-" + c.Name, Title: "Host: " + c.Name, Status: DoctorOK, Detail: c.Detail}
		if !c.OK {
			d.Status = DoctorWarn
			if c.Level == hostcheck.Required {
				d.Status = DoctorFail
			}
			if c.Fix != "" {
				d.Fix = fix(c.Fix)
			}
		}
		add(d)
	}

	// Port conflicts on the published addresses (SP5: "tailscale serve" on 443).
	if out, err := s.Host.Runner.Run(ctx, hostCmd("ss", "-ltnpH")); err != nil {
		add(DoctorCheck{ID: "ports", Title: "Ports 80, 443, 8443", Status: DoctorSkip, Detail: "ss failed: " + err.Error()})
	} else {
		pubs := append(append([]string{}, st.Published...), st.Addresses...)
		conflicts := PortConflicts(ParseSS(string(out)), pubs)
		if len(conflicts) == 0 {
			add(DoctorCheck{ID: "ports", Title: "Ports 80, 443, 8443", Status: DoctorOK, Detail: "no other program listens on a port of the pod"})
		} else {
			var parts []string
			for _, c := range conflicts {
				parts = append(parts, fmt.Sprintf("%s on %s:%d", nonEmptyOr(c.Process, "a program"), c.Addr, c.Port))
			}
			add(DoctorCheck{ID: "ports", Title: "Ports 80, 443, 8443", Status: DoctorFail, Detail: strings.Join(parts, "; "),
				Fix: fix("Stop the other program on the port (for tailscaled: tailscale serve reset), then: sudo care-pi apply")})
		}
	}

	// mDNS: avahi announces another name (a collision with another device: care-2.local).
	if st.Network.MDNSAdvertised == nil {
		add(DoctorCheck{ID: "mdns", Title: "mDNS name", Status: DoctorWarn, Detail: "avahi announces no name for " + st.Network.MDNSConfigured,
			Fix: fix("sudo systemctl restart avahi-daemon; check: journalctl -u avahi-daemon")})
	} else if *st.Network.MDNSAdvertised != st.Network.MDNSConfigured {
		add(DoctorCheck{ID: "mdns", Title: "mDNS name", Status: DoctorWarn,
			Detail: fmt.Sprintf("avahi announces %s, not %s: another device on the LAN has the name", *st.Network.MDNSAdvertised, st.Network.MDNSConfigured),
			Fix:    fix("Give one of the devices another name: Settings > Identity, or care-pi config set identity.hostname=NAME --apply")})
	} else {
		add(DoctorCheck{ID: "mdns", Title: "mDNS name", Status: DoctorOK, Detail: st.Network.MDNSConfigured})
	}

	// The clock (S52).
	switch {
	case st.Clock.Synchronized:
		add(DoctorCheck{ID: "clock", Title: "Clock", Status: DoctorOK, Detail: "NTP synchronized"})
	case st.Clock.Source == "browser":
		add(DoctorCheck{ID: "clock", Title: "Clock", Status: DoctorWarn, Detail: "the browser set the clock at the claim; NTP did not synchronize it yet",
			Fix: fix("Connect the device to the internet, or give it an NTP server of the LAN")})
	default:
		add(DoctorCheck{ID: "clock", Title: "Clock", Status: DoctorFail, Detail: "the clock is not synchronized (" + st.Clock.Gate + ")",
			Fix: fix("Check the internet and timedatectl timesync-status")})
	}

	// Memory and zram.
	mem := st.Host.Memory
	switch {
	case mem.Total == 0:
		add(DoctorCheck{ID: "memory", Title: "Memory", Status: DoctorSkip, Detail: "/proc/meminfo is not readable"})
	case mem.Available < 256<<20:
		add(DoctorCheck{ID: "memory", Title: "Memory", Status: DoctorWarn, Detail: fmt.Sprintf("%d MiB available of %d MiB", mem.Available>>20, mem.Total>>20),
			Fix: fix("Use a smaller preset (Settings > Size), or stop a build")})
	default:
		add(DoctorCheck{ID: "memory", Title: "Memory", Status: DoctorOK, Detail: fmt.Sprintf("%d MiB available of %d MiB", mem.Available>>20, mem.Total>>20)})
	}
	if zram, err := s.zramSwap(); err == nil {
		if zram {
			add(DoctorCheck{ID: "zram", Title: "zram swap", Status: DoctorOK, Detail: fmt.Sprintf("swap %d MiB", mem.SwapTotal>>20)})
		} else {
			add(DoctorCheck{ID: "zram", Title: "zram swap", Status: DoctorWarn, Detail: "no zram device in /proc/swaps: a local FE build can run out of memory on 2 GB",
				Fix: fix("Install systemd-zram-generator (install.sh does it) and reboot")})
		}
	}

	// Disk.
	for _, d := range st.Disks {
		c := DoctorCheck{ID: "disk-" + strings.TrimPrefix(strings.ReplaceAll(d.Mount, "/", "-"), "-"), Title: "Disk " + d.Mount, Status: DoctorOK,
			Detail: fmt.Sprintf("%d GiB free of %d GiB", d.FreeBytes>>30, d.TotalBytes>>30)}
		if c.ID == "disk-" {
			c.ID = "disk-root"
		}
		if d.TotalBytes > 0 && (d.FreeBytes < 2<<30 || d.FreeBytes*10 < d.TotalBytes) {
			c.Status = DoctorWarn
			c.Fix = fix("Free space: sudo care-pi fe prune; sudo podman image prune; check the local backups")
		}
		add(c)
	}

	// Units.
	var failed, inactive []string
	for _, u := range st.Units {
		switch u.ActiveState {
		case "failed":
			failed = append(failed, u.Unit)
		case "inactive":
			inactive = append(inactive, u.Unit)
		}
	}
	switch {
	case len(failed) > 0:
		add(DoctorCheck{ID: "units", Title: "Units of Care", Status: DoctorFail, Detail: "failed: " + strings.Join(failed, " "),
			Fix: fix("sudo care-pi logs " + strings.TrimSuffix(strings.TrimPrefix(failed[0], "care-"), ".service") + "; then sudo care-pi apply")})
	case st.SetupState == setup.Ready && len(inactive) > 0:
		add(DoctorCheck{ID: "units", Title: "Units of Care", Status: DoctorWarn, Detail: "inactive: " + strings.Join(inactive, " "),
			Fix: fix("sudo care-pi apply")})
	default:
		add(DoctorCheck{ID: "units", Title: "Units of Care", Status: DoctorOK, Detail: fmt.Sprintf("%d units", len(st.Units))})
	}

	// Certificates.
	t := s.TLS(ctx)
	if r := t.LocalCA.Root; r != nil {
		left := r.NotAfter.Sub(now)
		c := DoctorCheck{ID: "cert-local", Title: "Local root certificate", Status: DoctorOK, Detail: "valid until " + r.NotAfter.Format("2006-01-02")}
		if left < CertWarnDays*24*time.Hour {
			c.Status, c.Fix = DoctorWarn, fix("Make a new root: sudo care-pi tls local-ca rotate (each device installs it again)")
		}
		if len(t.LocalCA.Uncovered) > 0 {
			c.Status = DoctorWarn
			c.Detail += "; outside the name limits: " + strings.Join(t.LocalCA.Uncovered, ", ")
			c.Fix = fix("sudo care-pi tls local-ca rotate (each device installs the new root)")
		}
		add(c)
	}
	if t.Public.Enabled && t.Public.Hostname != nil {
		c := DoctorCheck{ID: "cert-public", Title: "Certificate of " + *t.Public.Hostname, Status: DoctorWarn, Detail: "no certificate yet",
			Fix: fix("Check the DNS service and the internet: sudo care-pi tls public status")}
		if pc := t.Public.Certificate; pc != nil && pc.NotAfter != nil {
			left := pc.NotAfter.Sub(now)
			c.Detail = fmt.Sprintf("%s, valid until %s", pc.State, pc.NotAfter.Format("2006-01-02"))
			switch {
			case left <= 0:
				c.Status = DoctorFail
			case left < CertWarnDays*24*time.Hour:
				c.Status = DoctorWarn
				c.Fix = fix("Caddy renews 30 days before the end: check the internet and sudo care-pi logs caddy")
			default:
				c.Status, c.Fix = DoctorOK, nil
			}
		}
		add(c)
	}

	// Backups: the age of the last snapshot of each destination against backups.alert_after_hours.
	if st.SetupState == setup.Ready {
		if b, err := s.Backups(ctx); err == nil && b != nil {
			limit := time.Duration(cfg.Backups.AlertAfterHours) * time.Hour
			if len(b.Destinations) == 0 {
				add(DoctorCheck{ID: "backups", Title: "Backups", Status: DoctorFail, Detail: "no backup destination",
					Fix: fix("Add a destination: Backups > Add a destination")})
			}
			for _, d := range b.Destinations {
				c := DoctorCheck{ID: "backup-" + d.ID, Title: "Backup " + d.ID + " (" + d.Type + ")"}
				switch {
				case d.LastSnapshotAt == nil:
					c.Status, c.Detail = DoctorWarn, "no snapshot yet"
					c.Fix = fix("sudo care-pi backup run --destination " + d.ID)
				case now.Sub(*d.LastSnapshotAt) > limit:
					c.Status = DoctorFail
					c.Detail = fmt.Sprintf("last snapshot %s ago (alert after %d h)", now.Sub(*d.LastSnapshotAt).Truncate(time.Minute), cfg.Backups.AlertAfterHours)
					c.Fix = fix("sudo care-pi backup health; then sudo care-pi backup run --destination " + d.ID)
				default:
					c.Status = DoctorOK
					c.Detail = fmt.Sprintf("last snapshot %s ago", now.Sub(*d.LastSnapshotAt).Truncate(time.Minute))
				}
				add(c)
			}
		}
	}

	// Firewall.
	fw, _ := s.Firewall(ctx)
	switch {
	case fw == nil:
	case !fw.UnitActive:
		add(DoctorCheck{ID: "firewall", Title: "Firewall", Status: DoctorWarn, Detail: "care-pi-firewall.service is not active",
			Fix: fix("Run install.sh again, or allow 80, 443, 8443/tcp and 5353/udp in the firewall of the host")})
	case fw.PendingUntil != nil:
		add(DoctorCheck{ID: "firewall", Title: "Firewall", Status: DoctorWarn, Detail: "the policy drop waits for a confirmation until " + fw.PendingUntil.Format(time.RFC3339),
			Fix: fix("sudo care-pi firewall confirm")})
	default:
		live := "not loaded by care-pi"
		if fw.LivePolicy != nil {
			live = *fw.LivePolicy
		}
		c := DoctorCheck{ID: "firewall", Title: "Firewall", Status: DoctorOK, Detail: "policy " + live}
		if fw.LivePolicy != nil && *fw.LivePolicy != fw.Policy {
			c.Status, c.Detail = DoctorWarn, fmt.Sprintf("config.yaml has %s, the loaded policy is %s", fw.Policy, live)
			c.Fix = fix("sudo care-pi apply")
		}
		add(c)
	}

	// A pending reboot and the updates.
	if _, err := os.Stat(s.Paths.Abs(paths.RebootRequired)); err == nil {
		add(DoctorCheck{ID: "reboot", Title: "Reboot", Status: DoctorWarn, Detail: "/run/reboot-required exists (an OS update)",
			Fix: fix("The update window reboots with updates.os_reboot; or: sudo reboot")})
	} else {
		add(DoctorCheck{ID: "reboot", Title: "Reboot", Status: DoctorOK, Detail: "no reboot is required"})
	}
	if sum := s.UpdatesSummary(ctx); sum != nil {
		c := DoctorCheck{ID: "updates", Title: "Updates", Status: DoctorOK, Detail: "track " + sum.Track}
		if sum.UpdateAvailable && sum.LatestVersion != nil {
			c.Status, c.Detail = DoctorWarn, "version "+*sum.LatestVersion+" is available"
			c.Fix = fix("Updates > Install, or wait for the update window")
		}
		add(c)
	}

	sort.SliceStable(rep.Checks, func(i, j int) bool { return rank(rep.Checks[i].Status) > rank(rep.Checks[j].Status) })
	rep.Status = DoctorOK
	for _, c := range rep.Checks {
		if rank(c.Status) > rank(rep.Status) {
			rep.Status = c.Status
		}
	}
	return rep, nil
}

func rank(s string) int {
	switch s {
	case DoctorFail:
		return 3
	case DoctorWarn:
		return 2
	case DoctorOK:
		return 1
	}
	return 0
}

func nonEmptyOr(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

// zramSwap reports whether /proc/swaps has a zram device.
func (s *Service) zramSwap() (bool, error) {
	data, err := os.ReadFile(s.Paths.Abs("/proc/swaps"))
	if err != nil {
		return false, err
	}
	return strings.Contains(string(data), "/dev/zram"), nil
}
