package host

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
	"time"
)

// Dev answers the commands of care-pi with plausible output and changes nothing on the host.
// "care-pi serve --insecure-dev --fake-host" uses it, so that a console developer can run the agent
// on a laptop without podman and systemd. Each unit is active and each container is healthy.
// The backup commands (pgbackrest, psql, restic, lsblk, the timers, the probe of a posix
// destination) also get plausible output: the local repository has one full and one incremental
// backup, each restic run succeeds, and a USB disk is attached (devBackup). The plug_config script of
// Phase 4 gets the rows that it writes (PlugConfigHook).
// A Dev of NewDev also keeps the podman secrets that apply sets and the stop of care.target,
// so a second apply finds no change and a full cycle ends.
type Dev struct{ st *devState }

type devState struct {
	mu      sync.Mutex
	secrets map[string]bool
	stopped bool
}

// NewDev returns a Dev with its own state.
func NewDev() Dev { return Dev{st: &devState{secrets: map[string]bool{}}} }

// record keeps the effect of a command on the state. It reports whether the units are stopped.
func (d Dev) record(c Cmd) (stopped bool, secrets []string) {
	if d.st == nil {
		return false, nil
	}
	d.st.mu.Lock()
	defer d.st.mu.Unlock()
	a := c.Args
	switch {
	case c.Name == "systemctl" && len(a) > 1 && a[0] == "stop" && a[1] == "care.target":
		d.st.stopped = true
	case c.Name == "systemctl" && len(a) > 1 && (a[0] == "start" || a[0] == "restart") && strings.HasPrefix(a[1], "care"):
		d.st.stopped = false
	case c.Name == "podman" && len(a) > 3 && a[0] == "secret" && a[1] == "create":
		d.st.secrets[a[len(a)-2]] = true
	}
	for n := range d.st.secrets {
		secrets = append(secrets, n)
	}
	sort.Strings(secrets)
	return d.st.stopped, secrets
}

// Run implements Runner.
func (d Dev) Run(_ context.Context, c Cmd) ([]byte, error) {
	stopped, secretNames := d.record(c)
	a := c.Args
	if out, ok := devBackup(c, time.Now()); ok {
		return []byte(out), nil
	}
	if c.Stdin != nil {
		in, _ := io.ReadAll(c.Stdin)
		if r, ok := PlugConfigHook(c, in); ok {
			return []byte(r.Out), nil
		}
	}
	switch c.Name {
	case "systemctl":
		if len(a) > 0 && a[0] == "show" {
			var units []string
			for i := 1; i < len(a); i++ {
				if a[i] == "-p" {
					i++
					continue
				}
				units = append(units, a[i])
			}
			var b strings.Builder
			for _, u := range units {
				if stopped && strings.HasPrefix(u, "care") {
					_, _ = fmt.Fprintf(&b, "Id=%s\nActiveState=inactive\nSubState=dead\nLoadState=loaded\nNRestarts=0\n\n", u)
					continue
				}
				_, _ = fmt.Fprintf(&b, "Id=%s\nActiveState=active\nSubState=running\nLoadState=loaded\nNRestarts=0\nMemoryCurrent=104857600\nMemoryPeak=157286400\nMemoryMax=536870912\n\n", u)
			}
			return []byte(b.String()), nil
		}
		return nil, nil
	case "podman":
		switch {
		case len(a) > 1 && a[0] == "secret" && a[1] == "ls":
			return []byte(strings.Join(secretNames, "\n") + "\n"), nil
		case len(a) > 0 && a[0] == "ps":
			return []byte("care-caddy\tUp 1 hour\ncare-db\tUp 1 hour (healthy)\ncare-redis\tUp 1 hour (healthy)\ncare-s3\tUp 1 hour (healthy)\ncare-beat\tUp 1 hour\ncare-worker\tUp 1 hour (healthy)\ncare-api\tUp 1 hour (healthy)\n"), nil
		case len(a) > 0 && a[0] == "version":
			return []byte("5.4.2\n"), nil
		}
		return nil, nil
	case "skopeo":
		ref := a[len(a)-1]
		return []byte(fmt.Sprintf("sha256:%x\n", sha256.Sum256([]byte(ref)))), nil
	case "timedatectl":
		return []byte("yes\n"), nil
	case "journalctl":
		for _, x := range a {
			if x == "json" {
				return []byte(devJournal(time.Now(), 1)), nil
			}
		}
		return []byte(time.Now().UTC().Format(time.RFC3339) + " care-pi dev host: no journal\n"), nil
	}
	return nil, nil
}

const devLsblk = `{"blockdevices":[
 {"name":"mmcblk0","path":"/dev/mmcblk0","size":63864569856,"type":"disk","tran":null,"rm":false,"model":null,"serial":"0x1234","fstype":null,"label":null,"mountpoints":[null],
  "children":[{"name":"mmcblk0p2","path":"/dev/mmcblk0p2","size":63327698944,"type":"part","fstype":"ext4","label":"rootfs","mountpoints":["/"]}]},
 {"name":"sda","path":"/dev/sda","size":128035676160,"type":"disk","tran":"usb","rm":true,"model":"Dev USB disk","serial":"DEV0001","fstype":null,"label":null,"mountpoints":[null]}]}`

// devResticCmds are the subcommands of restic that care-pi runs. The options of the repository
// come first, so the first of these in the arguments is the subcommand.
var devResticCmds = map[string]bool{"init": true, "cat": true, "backup": true, "snapshots": true, "forget": true,
	"check": true, "stats": true, "restore": true, "dump": true, "unlock": true, "prune": true}

// devBackup answers the commands of the backups. ok is false for other commands.
func devBackup(c Cmd, now time.Time) (out string, ok bool) {
	a := c.Args
	name := c.Name
	if i := strings.LastIndex(name, "/"); i >= 0 {
		name = name[i+1:]
	}
	has := func(s string) bool {
		for _, x := range a {
			if x == s {
				return true
			}
		}
		return false
	}
	switch {
	case name == "podman" && len(a) > 0 && a[0] == "exec" && has("pgbackrest"):
		if has("info") {
			full, incr := now.Add(-20*time.Hour).Unix(), now.Add(-50*time.Minute).Unix()
			return fmt.Sprintf(`[{"name":"main","cipher":"aes-256-cbc","archive":[{"min":"000000010000000000000001","max":"000000010000000000000009"}],`+
				`"backup":[{"label":"20260101-010000F","type":"full","timestamp":{"start":%d,"stop":%d},"info":{"size":48000000,"repository":{"delta":4600000}}},`+
				`{"label":"20260101-010000F_20260101-020000I","type":"incr","timestamp":{"start":%d,"stop":%d},"info":{"size":48000000,"repository":{"delta":26000}}}],`+
				`"status":{"code":0,"message":"ok"}}]`, full, full+4, incr, incr+3), true
		}
		return "", true
	case name == "podman" && len(a) > 0 && a[0] == "exec" && has("psql"):
		return fmt.Sprintf("%d||1|48000000|false\n", now.Add(-time.Minute).Unix()), true
	case name == "lsblk":
		return devLsblk, true
	case name == "systemctl" && len(a) > 0 && a[0] == "list-timers":
		next, last := now.Add(10*time.Minute).UnixMicro(), now.Add(-5*time.Minute).UnixMicro()
		var items []string
		for _, u := range []string{"care-backup-db-incr.timer", "care-backup-db-full.timer", "care-backup-verify.timer", "care-backup-drill.timer"} {
			items = append(items, fmt.Sprintf(`{"next":%d,"last":%d,"unit":%q,"activates":%q}`, next, last, u, strings.TrimSuffix(u, ".timer")+".service"))
		}
		return "[" + strings.Join(items, ",") + "]\n", true
	case name == "care-pi" && len(a) > 2 && a[0] == "backup" && a[1] == "destination" && a[2] == "probe":
		return `{"write_ok":true,"free_bytes":107374182400,"total_bytes":214748364800,"root_disk":true,"names":[]}` + "\n", true
	case name == "restic":
		sub := ""
		for _, x := range a {
			if devResticCmds[x] {
				sub = x
				break
			}
		}
		switch sub {
		case "backup":
			return fmt.Sprintf(`{"message_type":"summary","snapshot_id":"%x","data_added":26000,"data_added_packed":24000,"total_bytes_processed":48000000,"total_files_processed":12}`+"\n",
				sha256.Sum256([]byte(now.String()))), true
		case "snapshots":
			db, files := now.Add(-time.Hour).UTC().Format(time.RFC3339), now.Add(-9*time.Hour).UTC().Format(time.RFC3339)
			return fmt.Sprintf(`[{"id":"%x","short_id":"de0de0de","time":%q,"paths":["/var/lib/care-pi/backup/pg","/etc/care-pi"],"hostname":"care-dev","tags":["db"]},`+
				`{"id":"%x","short_id":"f11ef11e","time":%q,"paths":["/var/lib/care-pi/s3","/etc/care-pi"],"hostname":"care-dev","tags":["files"]}]`+"\n",
				sha256.Sum256([]byte("db")), db, sha256.Sum256([]byte("files")), files), true
		case "stats":
			return `{"total_size":52000000}` + "\n", true
		}
		return "", true
	}
	return "", false
}

func devJournal(t time.Time, n int) string {
	return fmt.Sprintf(`{"__CURSOR":"s=de1;i=%x;b=de2;m=1;t=%x;x=1","__REALTIME_TIMESTAMP":"%d","PRIORITY":"6","MESSAGE":"dev host line %d"}`+"\n",
		n, t.UnixMicro(), t.UnixMicro(), n)
}

// Stream implements Streamer. A follow command gets one line each 2 s.
func (d Dev) Stream(ctx context.Context, c Cmd, fn func([]byte) error) error {
	out, _ := d.Run(ctx, c)
	if err := fn([]byte(strings.TrimSpace(string(out)))); err != nil {
		return err
	}
	follow := false
	for _, a := range c.Args {
		follow = follow || a == "--follow"
	}
	if !follow {
		return nil
	}
	t := time.NewTicker(2 * time.Second)
	defer t.Stop()
	for n := 2; ; n++ {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case now := <-t.C:
			if err := fn([]byte(strings.TrimSpace(devJournal(now, n)))); err != nil {
				return err
			}
		}
	}
}

// DevGit returns the same sha for each ref of each repository.
type DevGit struct{}

// LsRemote implements Git.
func (DevGit) LsRemote(context.Context, string) (map[string]string, error) {
	sha := strings.Repeat("d", 40)
	return map[string]string{"HEAD": sha, "refs/heads/develop": sha, "refs/heads/main": sha, "refs/heads/master": sha}, nil
}
