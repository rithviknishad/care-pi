package service

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/rithviknishad/care-pi/agent/internal/hostcheck"
	"github.com/rithviknishad/care-pi/agent/internal/netinfo"
	"github.com/rithviknishad/care-pi/agent/internal/paths"
	"github.com/rithviknishad/care-pi/agent/internal/units"
)

// DiskProbe is a write and read of the root disk (schema DiskProbe).
type DiskProbe struct {
	WriteMBs   float64   `json:"write_mb_s"`
	ReadMBs    float64   `json:"read_mb_s"`
	MeasuredAt time.Time `json:"measured_at"`
}

// RootDisk is the disk of the root file system.
type RootDisk struct {
	Device    string `json:"device"`
	Kind      string `json:"kind"`
	SizeBytes int64  `json:"size_bytes"`
}

// Ethernet is the first ethernet interface.
type Ethernet struct {
	Interface *string `json:"interface"`
	Link      bool    `json:"link"`
	SpeedMbps *int    `json:"speed_mbps"`
	Address   *string `json:"address"`
}

// Hardware holds the hardware facts of the setup wizard (schema Hardware).
type Hardware struct {
	Model             *string    `json:"model"`
	Arch              string     `json:"arch"`
	RAMBytes          int64      `json:"ram_bytes"`
	PageSizeBytes     int        `json:"page_size_bytes"`
	CPUCount          int        `json:"cpu_count"`
	RootDisk          RootDisk   `json:"root_disk"`
	RTCPresent        bool       `json:"rtc_present"`
	Ethernet          Ethernet   `json:"ethernet"`
	InternetReachable *bool      `json:"internet_reachable"`
	ClockSynchronized bool       `json:"clock_synchronized"`
	DiskProbe         *DiskProbe `json:"disk_probe"`
}

// HostCheck is the host check and the hardware facts (schema HostCheck).
type HostCheck struct {
	OK       bool              `json:"ok"`
	Checks   []hostcheck.Check `json:"checks"`
	Hardware Hardware          `json:"hardware"`
}

func readTrim(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(strings.TrimRight(string(data), "\x00\n"))
}

func meminfo(p paths.Paths) map[string]int64 {
	out := map[string]int64{}
	data, err := os.ReadFile(p.Abs("/proc/meminfo"))
	if err != nil {
		return out
	}
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) >= 2 {
			v, _ := strconv.ParseInt(f[1], 10, 64)
			out[strings.TrimSuffix(f[0], ":")] = v * 1024
		}
	}
	return out
}

func memTotal(p paths.Paths) int64 { return meminfo(p)["MemTotal"] }

func boardModel(p paths.Paths) *string {
	if m := readTrim(p.Abs("/proc/device-tree/model")); m != "" {
		return &m
	}
	return nil
}

// mountDevice returns the device of the mount point from /proc/mounts.
func mountDevice(p paths.Paths, mount string) string {
	data, err := os.ReadFile(p.Abs("/proc/mounts"))
	if err != nil {
		return ""
	}
	best, bestLen := "", -1
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) < 2 || !strings.HasPrefix(f[0], "/dev/") {
			continue
		}
		mp := f[1]
		if (mount == mp || strings.HasPrefix(mount, strings.TrimSuffix(mp, "/")+"/") || mp == "/") && len(mp) > bestLen {
			best, bestLen = f[0], len(mp)
		}
	}
	return best
}

// parentDisk maps a partition to its disk: mmcblk0p2 to mmcblk0, nvme0n1p2 to nvme0n1, sda2 to sda.
func parentDisk(dev string) string {
	name := strings.TrimPrefix(dev, "/dev/")
	if strings.HasPrefix(name, "mmcblk") || strings.HasPrefix(name, "nvme") {
		if i := strings.LastIndex(name, "p"); i > 0 && i < len(name)-1 {
			if _, err := strconv.Atoi(name[i+1:]); err == nil {
				return name[:i]
			}
		}
		return name
	}
	return strings.TrimRight(name, "0123456789")
}

func diskKind(dev string) string {
	name := strings.TrimPrefix(dev, "/dev/")
	switch {
	case strings.HasPrefix(name, "mmcblk"):
		return "sd"
	case strings.HasPrefix(name, "nvme"):
		return "nvme"
	case strings.HasPrefix(name, "sd"):
		return "usb"
	}
	return "other"
}

func (s *Service) rootDisk() RootDisk {
	dev := mountDevice(s.Paths, "/")
	if dev == "" {
		return RootDisk{Device: "unknown", Kind: "other"}
	}
	disk := parentDisk(dev)
	sectors, _ := strconv.ParseInt(readTrim(s.Paths.Abs("/sys/class/block/"+disk+"/size")), 10, 64)
	return RootDisk{Device: "/dev/" + disk, Kind: diskKind(dev), SizeBytes: sectors * 512}
}

func ifaceKind(name string) string {
	switch {
	case strings.HasPrefix(name, "eth"), strings.HasPrefix(name, "en"):
		return "ethernet"
	case strings.HasPrefix(name, "wl"):
		return "wifi"
	}
	return "other"
}

func (s *Service) ethernet() Ethernet {
	e := Ethernet{}
	ifs, err := s.IPs.Interfaces()
	if err != nil {
		return e
	}
	for _, i := range ifs {
		if ifaceKind(i.Name) != "ethernet" || netinfo.Excluded(i.Name) {
			continue
		}
		name := i.Name
		e.Interface = &name
		e.Link = i.Up && readTrim(s.Paths.Abs("/sys/class/net/"+name+"/carrier")) != "0"
		if v, err := strconv.Atoi(readTrim(s.Paths.Abs("/sys/class/net/" + name + "/speed"))); err == nil && v > 0 {
			e.SpeedMbps = &v
		}
		for _, a := range i.Addrs {
			if a.Addr().Is4() {
				ip := a.Addr().String()
				e.Address = &ip
				break
			}
		}
		break
	}
	return e
}

// Hardware returns the hardware facts.
func (s *Service) Hardware(ctx context.Context) Hardware {
	h := Hardware{Model: boardModel(s.Paths), Arch: Arch(), RAMBytes: memTotal(s.Paths), PageSizeBytes: os.Getpagesize(),
		CPUCount: runtime.NumCPU(), RootDisk: s.rootDisk(), RTCPresent: fileExists(s.Paths.Abs("/dev/rtc0")),
		Ethernet: s.ethernet()}
	if s.Internet != nil {
		h.InternetReachable, _ = s.Internet.State()
	}
	if s.Host != nil && s.Host.Clock != nil {
		h.ClockSynchronized, _ = s.Host.Clock.NTPSynchronized(ctx)
	}
	s.mu.Lock()
	if s.probe != nil && s.now().Sub(s.probe.MeasuredAt) < 24*time.Hour {
		p := *s.probe
		h.DiskProbe = &p
	}
	s.mu.Unlock()
	return h
}

// HostCheckView runs the host checks and adds the advised checks of the hardware.
// probe measures the root disk first (about 10 s).
func (s *Service) HostCheckView(ctx context.Context, probe bool) *HostCheck {
	if probe {
		if p, err := s.ProbeDisk(ctx); err == nil {
			s.mu.Lock()
			s.probe = p
			s.mu.Unlock()
		} else {
			s.log().Warn("disk probe failed", "error", err)
		}
	}
	r := s.hostCheck(ctx)
	hw := s.Hardware(ctx)
	checks := append([]hostcheck.Check{}, r.Checks...)
	add := func(name string, ok bool, detail, fix string) {
		c := hostcheck.Check{Name: name, Level: hostcheck.Advised, OK: ok, Detail: detail}
		if !ok {
			c.Fix = fix
		}
		checks = append(checks, c)
	}
	add("ram", hw.RAMBytes == 0 || hw.RAMBytes >= 1800<<20, "RAM "+units.Bytes(hw.RAMBytes), "use a host with 2 GB RAM or more")
	diskOK := hw.RootDisk.Kind != "sd" && (hw.RootDisk.SizeBytes == 0 || hw.RootDisk.SizeBytes >= 64e9)
	// The same format as the hardware facts of the console (wave 4c showed "119 GiB" there and "128 GB" here).
	diskDetail := fmt.Sprintf("%s (%s, %s)", hw.RootDisk.Device, hw.RootDisk.Kind, units.Bytes(hw.RootDisk.SizeBytes))
	if hw.RootDisk.Kind == "sd" {
		diskDetail = "the root disk is an SD card"
	}
	add("root disk", diskOK, diskDetail, "use a USB SSD or an NVMe drive of 64 GB or more")
	if hw.DiskProbe != nil {
		add("disk speed", hw.DiskProbe.WriteMBs >= 20, fmt.Sprintf("write %.1f MB/s, read %.1f MB/s", hw.DiskProbe.WriteMBs, hw.DiskProbe.ReadMBs), "use a faster disk")
	} else {
		add("disk speed", true, "not measured (use the probe)", "")
	}
	add("page size", true, fmt.Sprintf("page size %d", hw.PageSizeBytes), "")
	if hw.RTCPresent {
		add("rtc", true, "/dev/rtc0 exists", "")
	} else {
		add("rtc", false, "no real-time clock", "the clock policy ntp needs the internet at each boot")
	}
	switch {
	case hw.Ethernet.Interface == nil:
		add("ethernet", false, "no ethernet interface", "connect the host with an ethernet cable")
	case !hw.Ethernet.Link || hw.Ethernet.Address == nil:
		add("ethernet", false, *hw.Ethernet.Interface+" has no link or no IPv4 address", "connect the ethernet cable")
	default:
		add("ethernet", true, *hw.Ethernet.Interface+" "+*hw.Ethernet.Address, "")
	}
	switch {
	case hw.InternetReachable == nil:
		add("internet", true, "not checked yet", "")
	case *hw.InternetReachable:
		add("internet", true, "the internet is reachable", "")
	default:
		add("internet", false, "the internet is not reachable", "the first apply pulls the images; connect the router to the internet")
	}
	if hw.ClockSynchronized {
		add("clock", true, "the clock is synchronized", "")
	} else {
		add("clock", false, "the clock is not synchronized", "check the internet connection and systemd-timesyncd")
	}
	return &HostCheck{OK: r.OK(), Checks: checks, Hardware: hw}
}

// ProbeDisk writes and reads a file in /var/lib/care-pi for about 10 s.
func (s *Service) ProbeDisk(ctx context.Context) (*DiskProbe, error) {
	dir := s.Paths.Abs(paths.StateDir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, "disk-probe.tmp")
	defer func() { _ = os.Remove(path) }()
	f, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, err
	}
	buf := make([]byte, 1<<20)
	for i := range buf {
		buf[i] = byte(i * 7)
	}
	var written int64
	start := time.Now()
	for written < 256<<20 && time.Since(start) < 6*time.Second && ctx.Err() == nil {
		n, err := f.Write(buf)
		if err != nil {
			_ = f.Close()
			return nil, err
		}
		written += int64(n)
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return nil, err
	}
	wdur := time.Since(start)
	_ = f.Close()
	r, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = r.Close() }()
	var read int64
	start = time.Now()
	for time.Since(start) < 4*time.Second && ctx.Err() == nil {
		n, err := r.Read(buf)
		read += int64(n)
		if err != nil {
			break
		}
	}
	rdur := time.Since(start)
	mbs := func(b int64, d time.Duration) float64 {
		if d <= 0 {
			return 0
		}
		return float64(int(float64(b)/1e6/d.Seconds()*10)) / 10
	}
	return &DiskProbe{WriteMBs: mbs(written, wdur), ReadMBs: mbs(read, rdur), MeasuredAt: s.now()}, nil
}

// statfs returns the size and the free space for root of the file system of path.
func statfs(path string) (total, free int64, err error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, 0, err
	}
	bs := int64(st.Bsize) // Bsize is int64 on Linux and uint32 on macOS.
	return int64(st.Blocks) * bs, int64(st.Bfree) * bs, nil
}
