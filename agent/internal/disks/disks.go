// Package disks lists the block devices of the host and prepares a USB backup disk
// (docs/api/README.md section 13.5, docs/backups.md "USB disk").
package disks

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/rithviknishad/care-pi/agent/internal/host"
	"github.com/rithviknishad/care-pi/agent/internal/paths"
)

// Usages of a disk (schema BackupDisk.usage).
const (
	UsageSystem     = "system"
	UsageCareBackup = "care-backup"
	UsageEmpty      = "empty"
	UsageOther      = "other-data"
)

// LabelRE is the pattern of a care-pi file system label.
var LabelRE = regexp.MustCompile(`^care-[a-z0-9][a-z0-9-]{0,10}$`)

// Partition is one partition (schema BackupDiskPartition).
type Partition struct {
	Device    string  `json:"device"`
	SizeBytes int64   `json:"size_bytes"`
	FSType    *string `json:"fs_type"`
	Label     *string `json:"label"`
	Mount     *string `json:"mount"`
}

// Disk is one block device (schema BackupDisk).
type Disk struct {
	ID            string      `json:"id"`
	Device        string      `json:"device"`
	Model         *string     `json:"model"`
	Serial        *string     `json:"serial"`
	SizeBytes     int64       `json:"size_bytes"`
	Transport     string      `json:"transport"`
	Removable     bool        `json:"removable"`
	Usage         string      `json:"usage"`
	Mounted       bool        `json:"mounted"`
	Partitions    []Partition `json:"partitions"`
	CareLabel     *string     `json:"care_label"`
	DestinationID *string     `json:"destination_id"`
	// Name is the kernel name (sdb).
	Name string `json:"-"`
}

type lsblkDev struct {
	Name        string      `json:"name"`
	Path        string      `json:"path"`
	Size        json.Number `json:"size"`
	Type        string      `json:"type"`
	Tran        *string     `json:"tran"`
	RM          any         `json:"rm"`
	Model       *string     `json:"model"`
	Serial      *string     `json:"serial"`
	FSType      *string     `json:"fstype"`
	Label       *string     `json:"label"`
	Mountpoints []*string   `json:"mountpoints"`
	Mountpoint  *string     `json:"mountpoint"`
	Children    []lsblkDev  `json:"children"`
}

// LsblkArgs are the arguments of lsblk.
var LsblkArgs = []string{"--json", "--bytes", "--output", "NAME,PATH,SIZE,TYPE,TRAN,RM,MODEL,SERIAL,FSTYPE,LABEL,MOUNTPOINTS"}

func (d lsblkDev) mounts() []string {
	var out []string
	for _, m := range d.Mountpoints {
		if m != nil && *m != "" {
			out = append(out, *m)
		}
	}
	if d.Mountpoint != nil && *d.Mountpoint != "" {
		out = append(out, *d.Mountpoint)
	}
	return out
}

func trimPtr(s *string) *string {
	if s == nil {
		return nil
	}
	v := strings.TrimSpace(*s)
	if v == "" {
		return nil
	}
	return &v
}

func truthy(v any) bool {
	switch x := v.(type) {
	case bool:
		return x
	case string:
		return x == "1" || x == "true"
	case float64:
		return x != 0
	case json.Number:
		return x.String() != "0"
	}
	return false
}

var systemMounts = map[string]bool{"/": true, "/boot": true, "/boot/firmware": true, "/usr": true, "/var": true, "[SWAP]": true}

// skip are kernel names that are never a backup disk.
var skip = regexp.MustCompile(`^(zram|ram|sr|mmcblk[0-9]+boot|mmcblk[0-9]+rpmb|nbd|dm-)`)

// Parse reads the lsblk output. byID maps a kernel name (sdb) to its name in /dev/disk/by-id.
func Parse(out []byte, byID map[string]string) ([]Disk, error) {
	var doc struct {
		Blockdevices []lsblkDev `json:"blockdevices"`
	}
	dec := json.NewDecoder(bytes.NewReader(out))
	dec.UseNumber()
	if err := dec.Decode(&doc); err != nil {
		return nil, fmt.Errorf("cannot read the lsblk output: %w", err)
	}
	disks := []Disk{}
	for _, b := range doc.Blockdevices {
		if (b.Type != "disk" && b.Type != "loop") || skip.MatchString(b.Name) {
			continue
		}
		size, _ := b.Size.Int64()
		if size == 0 {
			continue
		}
		d := Disk{ID: byID[b.Name], Device: b.Path, Model: trimPtr(b.Model), Serial: trimPtr(b.Serial), SizeBytes: size,
			Transport: transport(b), Removable: truthy(b.RM), Partitions: []Partition{}, Name: b.Name}
		if d.ID == "" {
			d.ID = b.Name
		}
		if d.Device == "" {
			d.Device = "/dev/" + b.Name
		}
		system := false
		for _, m := range b.mounts() {
			d.Mounted = true
			system = system || systemMounts[m]
		}
		if b.FSType != nil && *b.FSType == "swap" {
			system = true
		}
		var care *string
		for _, c := range b.Children {
			ps, _ := c.Size.Int64()
			p := Partition{Device: c.Path, SizeBytes: ps, FSType: trimPtr(c.FSType), Label: trimPtr(c.Label)}
			if p.Device == "" {
				p.Device = "/dev/" + c.Name
			}
			if ms := c.mounts(); len(ms) > 0 {
				d.Mounted = true
				m := ms[len(ms)-1]
				for _, x := range ms {
					system = system || systemMounts[x]
					if x == "/" {
						m = x
					}
				}
				p.Mount = &m
			}
			if p.FSType != nil && *p.FSType == "swap" {
				system = true
			}
			if p.Label != nil && LabelRE.MatchString(*p.Label) && care == nil {
				l := *p.Label
				care = &l
			}
			d.Partitions = append(d.Partitions, p)
		}
		d.CareLabel = care
		switch {
		case system:
			d.Usage = UsageSystem
		case care != nil:
			d.Usage = UsageCareBackup
		case len(b.Children) == 0 && trimPtr(b.FSType) == nil:
			d.Usage = UsageEmpty
		default:
			d.Usage = UsageOther
		}
		disks = append(disks, d)
	}
	sort.SliceStable(disks, func(i, j int) bool {
		return disks[i].Usage == UsageSystem && disks[j].Usage != UsageSystem
	})
	return disks, nil
}

func transport(b lsblkDev) string {
	t := ""
	if b.Tran != nil {
		t = *b.Tran
	}
	switch {
	case t == "usb" || t == "nvme" || t == "sata" || t == "virtio":
		return t
	case strings.HasPrefix(b.Name, "mmcblk"):
		return "sd"
	case strings.HasPrefix(b.Name, "nvme"):
		return "nvme"
	case strings.HasPrefix(b.Name, "vd"):
		return "virtio"
	}
	return "other"
}

// ByID reads /dev/disk/by-id: the kernel name of each whole disk to its id. It prefers the usb-,
// nvme-, and ata- names over wwn-.
func ByID(p paths.Paths) map[string]string {
	out := map[string]string{}
	dir := p.Abs("/dev/disk/by-id")
	ents, err := os.ReadDir(dir)
	if err != nil {
		return out
	}
	var names []string
	for _, e := range ents {
		names = append(names, e.Name())
	}
	sort.Strings(names)
	for _, n := range names {
		if strings.Contains(n, "-part") {
			continue
		}
		target, err := os.Readlink(filepath.Join(dir, n))
		if err != nil {
			continue
		}
		k := filepath.Base(target)
		if cur, ok := out[k]; ok && !strings.HasPrefix(cur, "wwn-") {
			continue
		}
		out[k] = n
	}
	return out
}

// List runs lsblk and reads /dev/disk/by-id.
func List(ctx context.Context, r host.Runner, p paths.Paths) ([]Disk, error) {
	out, err := r.Run(ctx, host.Cmd{Name: "lsblk", Args: LsblkArgs})
	if err != nil {
		return nil, err
	}
	return Parse(out, ByID(p))
}

// Find returns the disk with the id (or the kernel name), or nil.
func Find(list []Disk, id string) *Disk {
	for i := range list {
		if list[i].ID == id || list[i].Name == id {
			return &list[i]
		}
	}
	return nil
}

// PartitionDevice returns the first partition of a disk: sdb1, nvme0n1p1, mmcblk0p1, loop1p1.
func PartitionDevice(dev string) string {
	if dev == "" {
		return ""
	}
	last := dev[len(dev)-1]
	if last >= '0' && last <= '9' {
		return dev + "p1"
	}
	return dev + "1"
}

// NextLabel returns care-usb<N> with the smallest N that no disk and no destination uses.
func NextLabel(used map[string]bool) string {
	for n := 1; ; n++ {
		l := "care-usb" + strconv.Itoa(n)
		if !used[l] {
			return l
		}
	}
}

// Commands returns the commands of the steps wipe-disk, partition, and format, in order.
// Each one runs as root through the host runner (a transient unit of the agent).
func Commands(d Disk, label string) (wipe, partition, format []host.Cmd) {
	for _, p := range d.Partitions {
		wipe = append(wipe, host.Cmd{Name: "wipefs", Args: []string{"--all", "--force", p.Device}})
	}
	wipe = append(wipe, host.Cmd{Name: "wipefs", Args: []string{"--all", "--force", d.Device}})
	partition = []host.Cmd{
		{Name: "sfdisk", Args: []string{"--wipe", "always", "--wipe-partitions", "always", "--label", "gpt", d.Device}, Stdin: strings.NewReader(",,L\n")},
		{Name: "partprobe", Args: []string{d.Device}},
		{Name: "udevadm", Args: []string{"settle", "--timeout=30"}},
	}
	part := PartitionDevice(d.Device)
	format = []host.Cmd{
		{Name: "mkfs.ext4", Args: []string{"-F", "-q", "-m", "0", "-L", label, part}},
		{Name: "udevadm", Args: []string{"settle", "--timeout=30"}},
	}
	return wipe, partition, format
}

// WaitDevice waits until a device node exists (after partprobe).
func WaitDevice(ctx context.Context, p paths.Paths, dev string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		if _, err := os.Stat(p.Abs(dev)); err == nil {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("%s did not appear in %s", dev, timeout)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
	}
}
