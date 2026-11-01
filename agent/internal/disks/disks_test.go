package disks

import (
	"strings"
	"testing"
)

const fixture = `{"blockdevices": [
 {"name":"loop0","path":"/dev/loop0","size":2103443456,"type":"loop","tran":null,"rm":false,"model":null,"serial":null,"fstype":"swap","label":"origin:rpi-swap","mountpoints":["[SWAP]"]},
 {"name":"loop1","path":"/dev/loop1","size":4294967296,"type":"loop","tran":null,"rm":false,"model":null,"serial":null,"fstype":null,"label":null,"mountpoints":[null]},
 {"name":"sda","path":"/dev/sda","size":128035676160,"type":"disk","tran":"usb","rm":false,"model":"External","serial":"DD0000000000001E","fstype":null,"label":null,"mountpoints":[null],
  "children":[
   {"name":"sda1","path":"/dev/sda1","size":536870912,"type":"part","fstype":"vfat","label":"bootfs","mountpoints":["/boot/firmware"]},
   {"name":"sda2","path":"/dev/sda2","size":127490416640,"type":"part","fstype":"ext4","label":"rootfs","mountpoints":["/var/lib/containers/storage/overlay","/"]}]},
 {"name":"sdb","path":"/dev/sdb","size":500107862016,"type":"disk","tran":"usb","rm":true,"model":"SanDisk Extreme 55AE ","serial":"323130364E","fstype":null,"label":null,"mountpoints":[null],
  "children":[{"name":"sdb1","path":"/dev/sdb1","size":500105764864,"type":"part","fstype":"exfat","label":"SANDISK","mountpoints":["/media/usb"]}]},
 {"name":"sdc","path":"/dev/sdc","size":64000000000,"type":"disk","tran":"usb","rm":true,"model":"Cruzer","serial":"X1","fstype":null,"label":null,"mountpoints":[null],
  "children":[{"name":"sdc1","path":"/dev/sdc1","size":63999000000,"type":"part","fstype":"ext4","label":"care-usb1","mountpoints":[null]}]},
 {"name":"mmcblk0boot0","path":"/dev/mmcblk0boot0","size":4194304,"type":"disk","tran":null,"rm":false}
]}`

func TestParse(t *testing.T) {
	byID := map[string]string{"sda": "usb-JMicron_External_DD0000000000001E-0:0", "sdb": "usb-SanDisk_Extreme_55AE_323130364E-0:0"}
	list, err := Parse([]byte(fixture), byID)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]Disk{}
	var order []string
	for _, d := range list {
		got[d.Name] = d
		order = append(order, d.Name+":"+d.Usage)
	}
	if strings.Join(order, " ") != "loop0:system sda:system loop1:empty sdb:other-data sdc:care-backup" {
		t.Fatalf("order %v", order)
	}
	if d := got["sda"]; d.ID != byID["sda"] || !d.Mounted || d.Transport != "usb" || *d.Partitions[1].Mount != "/" {
		t.Fatalf("sda %+v", d)
	}
	if d := got["sdb"]; !d.Mounted || !d.Removable || *d.Model != "SanDisk Extreme 55AE" {
		t.Fatalf("sdb %+v", d)
	}
	if d := got["sdc"]; d.ID != "sdc" || d.CareLabel == nil || *d.CareLabel != "care-usb1" || d.Mounted {
		t.Fatalf("sdc %+v", d)
	}
	if Find(list, "sdc") == nil || Find(list, byID["sdb"]) == nil || Find(list, "nope") != nil {
		t.Fatal("find")
	}
}

func TestCommands(t *testing.T) {
	list, _ := Parse([]byte(fixture), nil)
	d := Find(list, "sdb")
	wipe, part, format := Commands(*d, "care-usb2")
	var lines []string
	for _, c := range append(append(wipe, part...), format...) {
		lines = append(lines, c.String())
	}
	want := []string{
		"wipefs --all --force /dev/sdb1",
		"wipefs --all --force /dev/sdb",
		"sfdisk --wipe always --wipe-partitions always --label gpt /dev/sdb",
		"partprobe /dev/sdb",
		"udevadm settle --timeout=30",
		"mkfs.ext4 -F -q -m 0 -L care-usb2 /dev/sdb1",
		"udevadm settle --timeout=30",
	}
	if strings.Join(lines, "\n") != strings.Join(want, "\n") {
		t.Fatalf("commands\n%s", strings.Join(lines, "\n"))
	}
	for dev, p := range map[string]string{"/dev/sdb": "/dev/sdb1", "/dev/nvme0n1": "/dev/nvme0n1p1", "/dev/loop1": "/dev/loop1p1", "/dev/mmcblk0": "/dev/mmcblk0p1"} {
		if PartitionDevice(dev) != p {
			t.Fatal(dev)
		}
	}
	if NextLabel(map[string]bool{"care-usb1": true}) != "care-usb2" {
		t.Fatal("label")
	}
}

// layers are disks below LUKS, LVM, and md RAID: the mounts, the swap, and the holder of each descendant count.
const layers = `{"blockdevices": [
 {"name":"sdd","path":"/dev/sdd","size":64000000000,"type":"disk","tran":"usb","rm":true,"fstype":null,"label":null,"mountpoints":[null],
  "children":[{"name":"sdd1","path":"/dev/sdd1","size":63999000000,"type":"part","fstype":"crypto_LUKS","label":null,"mountpoints":[null],
   "children":[{"name":"luks-1","path":"/dev/mapper/luks-1","size":63990000000,"type":"crypt","fstype":"ext4","label":null,"mountpoints":["/mnt/secure"]}]}]},
 {"name":"sde","path":"/dev/sde","size":64000000000,"type":"disk","tran":"usb","rm":true,"fstype":null,"label":null,"mountpoints":[null],
  "children":[{"name":"sde1","path":"/dev/sde1","size":63999000000,"type":"part","fstype":"LVM2_member","label":null,"mountpoints":[null],
   "children":[{"name":"data-lv","path":"/dev/mapper/data-lv","size":63990000000,"type":"lvm","fstype":"ext4","label":null,"mountpoints":[null]}]}]},
 {"name":"sdf","path":"/dev/sdf","size":64000000000,"type":"disk","tran":"usb","rm":true,"fstype":null,"label":null,"mountpoints":[null],
  "children":[{"name":"sdf1","path":"/dev/sdf1","size":63999000000,"type":"part","fstype":"linux_raid_member","label":"pi:0","mountpoints":[null],
   "children":[{"name":"md0","path":"/dev/md0","size":63990000000,"type":"raid1","fstype":null,"label":null,"mountpoints":[null]}]}]},
 {"name":"sdg","path":"/dev/sdg","size":64000000000,"type":"disk","tran":"usb","rm":true,"fstype":null,"label":null,"mountpoints":[null],
  "children":[{"name":"sdg1","path":"/dev/sdg1","size":63999000000,"type":"part","fstype":"LVM2_member","label":null,"mountpoints":[null]}]},
 {"name":"sdh","path":"/dev/sdh","size":64000000000,"type":"disk","tran":"usb","rm":true,"fstype":null,"label":null,"mountpoints":[null],
  "children":[{"name":"sdh1","path":"/dev/sdh1","size":63999000000,"type":"part","fstype":"crypto_LUKS","label":null,"mountpoints":[null],
   "children":[{"name":"luks-2","path":"/dev/mapper/luks-2","size":63990000000,"type":"crypt","fstype":"LVM2_member","label":null,"mountpoints":[null],
    "children":[{"name":"vg-swap","path":"/dev/mapper/vg-swap","size":4000000000,"type":"lvm","fstype":"swap","label":null,"mountpoints":["[SWAP]"]},
                {"name":"vg-root","path":"/dev/mapper/vg-root","size":59000000000,"type":"lvm","fstype":"ext4","label":null,"mountpoints":["/"]}]}]}]},
 {"name":"sdi","path":"/dev/sdi","size":64000000000,"type":"disk","tran":"usb","rm":true,"fstype":"crypto_LUKS","label":null,"mountpoints":[null]},
 {"name":"sdj","path":"/dev/sdj","size":64000000000,"type":"disk","tran":"usb","rm":true,"fstype":null,"label":null,"mountpoints":[null],
  "children":[{"name":"sdj1","path":"/dev/sdj1","size":63999000000,"type":"part","fstype":"ext4","label":"photos","mountpoints":[null]}]}
]}`

// TestParseLayers: a disk whose partition is a LUKS, LVM, or md RAID member is held, and the mounts below it count.
func TestParseLayers(t *testing.T) {
	list, err := Parse([]byte(layers), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name, usage string
		mounted     bool
		mount, held string
	}{
		{"sdd", UsageOther, true, "/mnt/secure", "/dev/sdd1 is a LUKS encrypted volume (crypto_LUKS)"},
		{"sde", UsageOther, false, "", "/dev/sde1 is an LVM physical volume (LVM2_member)"},
		{"sdf", UsageOther, false, "", "/dev/sdf1 is a member of an md RAID array (linux_raid_member)"},
		{"sdg", UsageOther, false, "", "/dev/sdg1 is an LVM physical volume (LVM2_member)"},
		{"sdh", UsageSystem, true, "/", "/dev/sdh1 is a LUKS encrypted volume (crypto_LUKS)"},
		{"sdi", UsageOther, false, "", "/dev/sdi is a LUKS encrypted volume (crypto_LUKS)"},
		{"sdj", UsageOther, false, "", ""},
	} {
		d := Find(list, c.name)
		if d == nil {
			t.Fatalf("%s is missing", c.name)
		}
		mount := ""
		if len(d.Partitions) > 0 && d.Partitions[0].Mount != nil {
			mount = *d.Partitions[0].Mount
		}
		if d.Usage != c.usage || d.Mounted != c.mounted || mount != c.mount || d.Held != c.held {
			t.Errorf("%s: usage %s mounted %v mount %q held %q", c.name, d.Usage, d.Mounted, mount, d.Held)
		}
	}
	// A crypt, lvm, or raid device below a partition without a member signature still holds the disk.
	l, err := Parse([]byte(strings.Replace(layers, `"fstype":"linux_raid_member"`, `"fstype":null`, 1)), nil)
	if err != nil {
		t.Fatal(err)
	}
	if d := Find(l, "sdf"); d.Held != "/dev/sdf1 is in use by /dev/md0 (type raid1)" {
		t.Fatalf("sdf %q", d.Held)
	}
}
