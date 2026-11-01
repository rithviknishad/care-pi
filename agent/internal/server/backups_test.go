package server

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/rithviknishad/care-pi/agent/internal/backup"
	"github.com/rithviknishad/care-pi/agent/internal/config"
	"github.com/rithviknishad/care-pi/agent/internal/host"
	"github.com/rithviknishad/care-pi/agent/internal/jobs"
	"github.com/rithviknishad/care-pi/agent/internal/paths"
	"github.com/rithviknishad/care-pi/agent/internal/service"
	"github.com/rithviknishad/care-pi/agent/internal/setup"
)

const (
	pgExec    = "podman exec --user postgres care-db "
	infoCmd   = pgExec + "pgbackrest --stanza=main info --output=json"
	stateCmd  = pgExec + "psql -X -q -At -d care -c " + backup.StateQuery
	lsblkCmd  = "lsblk --json --bytes --output NAME,PATH,SIZE,TYPE,TRAN,RM,MODEL,SERIAL,FSTYPE,LABEL,MOUNTPOINTS"
	probeCmd  = paths.Binary + " backup destination probe "
	lsblkJSON = `{"blockdevices":[
 {"name":"sda","path":"/dev/sda","size":128035676160,"type":"disk","tran":"usb","rm":false,"model":"External","serial":"DD1","fstype":null,"label":null,"mountpoints":[null],
  "children":[{"name":"sda2","path":"/dev/sda2","size":127490416640,"type":"part","fstype":"ext4","label":"rootfs","mountpoints":["/"]}]},
 {"name":"sdb","path":"/dev/sdb","size":64000000000,"type":"disk","tran":"usb","rm":true,"model":"Cruzer","serial":"X1","fstype":null,"label":null,"mountpoints":[null]}]}`
)

func pgInfo(now time.Time) string {
	return fmt.Sprintf(`[{"name":"main","cipher":"aes-256-cbc","archive":[{"min":"000000010000000000000001","max":"000000010000000000000009"}],
"backup":[{"label":"20261030-010000F","type":"full","timestamp":{"start":%d,"stop":%d},"info":{"size":48000000,"repository":{"delta":4600000}}},
{"label":"20261030-010000F_20261030-020000I","type":"incr","timestamp":{"start":%d,"stop":%d},"info":{"size":48000000,"repository":{"delta":26000}}}],
"status":{"code":0,"message":"ok"}}]`, now.Add(-2*time.Hour).Unix(), now.Add(-2*time.Hour+4*time.Second).Unix(), now.Add(-time.Hour).Unix(), now.Add(-time.Hour+5*time.Second).Unix())
}

type fakeSSH struct {
	key     ssh.PublicKey
	refused bool
}

func (f *fakeSSH) HostKey(context.Context, string, []string) (ssh.PublicKey, error) {
	return f.key, nil
}
func (f *fakeSSH) Auth(context.Context, string, string, ssh.PublicKey, ssh.Signer) error {
	if f.refused {
		return backup.ErrAuthRefused
	}
	return nil
}

type fakeS3 struct {
	mu      sync.Mutex
	buckets map[string]map[string][]byte
}

func (f *fakeS3) BucketExists(_ context.Context, b string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, ok := f.buckets[b]
	return ok, nil
}
func (f *fakeS3) MakeBucket(_ context.Context, b, _ string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.buckets[b] = map[string][]byte{}
	return nil
}
func (f *fakeS3) Put(_ context.Context, b, k string, d []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.buckets[b][k] = d
	return nil
}
func (f *fakeS3) Get(_ context.Context, b, k string) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.buckets[b][k], nil
}
func (f *fakeS3) Remove(_ context.Context, b, k string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.buckets[b], k)
	return nil
}
func (f *fakeS3) ObjectLock(context.Context, string) (bool, error) { return false, nil }
func (f *fakeS3) Prefixes(context.Context, string, string) ([]string, error) {
	return []string{"care-pi-0badc0de/"}, nil
}

// backupEnv makes an env with the backup fakes: an SSH server, an S3 service, lsblk, pgbackrest info.
func backupEnv(t *testing.T) (*env, *fakeSSH) {
	e := newEnv(t)
	pub, _, err := ed25519.GenerateKey(nil)
	must(t, err)
	sk, err := ssh.NewPublicKey(pub)
	must(t, err)
	fs := &fakeSSH{key: sk, refused: true}
	s3 := &fakeS3{buckets: map[string]map[string][]byte{}}
	h := e.svc.Host
	e.svc.BackupTester = &backup.Tester{Paths: e.p, Runner: e.fake, Systemd: h.Systemd, SSH: fs,
		NewS3: func(config.BackupDestination, *backup.Credentials) (backup.S3Client, error) { return s3, nil }}
	e.fake.On(infoCmd, host.Response{Out: pgInfo(e.clock.now())})
	e.fake.On(stateCmd, host.Response{Out: fmt.Sprintf("%d||1|48000000|false\n", e.clock.now().Add(-time.Minute).Unix())})
	e.fake.On(lsblkCmd, host.Response{Out: lsblkJSON})
	probe, _ := json.Marshal(backup.PathProbe{WriteOK: true, Free: 100 << 30, Total: 200 << 30, RootDisk: true, Names: []string{"care-pi-0badc0de"}})
	e.fake.On(probeCmd+"/srv/care-backups", host.Response{Out: string(probe)})
	e.fake.On(probeCmd+"/var/lib/care-pi/disks/care-usb1", host.Response{Out: string(probe)})
	e.fake.On(probeCmd+"/var/lib/care-pi/disks/care-usb4", host.Response{Out: string(probe)})
	return e, fs
}

func (e *env) keyWords(t *testing.T) ([]string, []int) {
	t.Helper()
	k, m, err := (&backup.KeyStore{Paths: e.p}).Get(backup.Active)
	must(t, err)
	return k.Words(), m.ConfirmPositions
}

func confirmBody(words []string, pos []int, wrong int) []any {
	var out []any
	for i, p := range pos {
		w := words[p-1]
		if i == wrong {
			w = "zzzz"
		}
		out = append(out, map[string]any{"index": p, "word": w})
	}
	return out
}

// TestBackupsSetup: the key ceremony, the destination tests, the disks, the step backups, and the
// restore from backup in the setup states (docs/api/README.md section 13).
func TestBackupsSetup(t *testing.T) {
	e, fs := backupEnv(t)
	e.want(e.do("GET", "/api/v1/backups", nil), 401, "not-authenticated")
	e.claim()
	e.want(e.do("GET", "/api/v1/backups", nil), 200, "")
	e.want(e.do("GET", "/api/v1/backups/health", nil), 200, "")
	e.want(e.do("GET", "/api/v1/backups/destinations", nil), 200, "")
	r := e.do("GET", "/api/v1/backups/recovery-key/status", nil)
	e.want(r, 200, "")
	if r.json(t)["state"] != "none" {
		t.Fatal(string(r.body))
	}
	e.want(e.do("GET", "/api/v1/backups/ssh-key", nil), 409, "no-recovery-key")
	e.want(e.do("GET", "/api/v1/backups/recovery-key", nil), 409, "no-recovery-key")
	e.want(e.do("GET", "/api/v1/backups/recovery-key/download", nil), 409, "no-recovery-key")
	e.want(e.do("POST", "/api/v1/backups/recovery-key/confirm", map[string]any{"words": []any{map[string]any{"index": 1, "word": "able"},
		map[string]any{"index": 2, "word": "able"}, map[string]any{"index": 3, "word": "able"}}}), 409, "no-recovery-key")
	sftp := map[string]any{"id": "mac", "type": "sftp", "host": "mac.lan", "user": "care", "path": "/b"}
	e.want(e.do("POST", "/api/v1/backups/destinations/test", map[string]any{"destination": sftp, "credentials": nil}), 409, "no-recovery-key")
	e.want(e.do("PUT", "/api/v1/setup/steps/backups", map[string]any{"destinations": []any{map[string]any{"id": "local", "type": "posix", "path": "/srv/care-backups"}},
		"credentials": map[string]any{}, "recovery_key_words": nil}), 409, "no-recovery-key")

	r = e.do("POST", "/api/v1/backups/recovery-key", nil)
	e.want(r, 201, "")
	key := r.json(t)["key"].(map[string]any)
	if key["key"] != "active" || len(key["words"].([]any)) != 24 || !strings.HasPrefix(key["qr_payload"].(string), "CARE-PI-RK1:") {
		t.Fatal(string(r.body))
	}
	words, pos := e.keyWords(t)
	e.want(e.do("GET", "/api/v1/backups/recovery-key?key=pending", nil), 404, "not-found")
	e.want(e.do("GET", "/api/v1/backups/recovery-key?key=other", nil, invalid), 422, "invalid-request")
	e.want(e.do("GET", "/api/v1/backups/recovery-key", nil), 200, "")
	// The console asks for the file with Accept: text/plain (alignment note of the console, wave 5).
	r = e.do("GET", "/api/v1/backups/recovery-key/download", nil, header("Accept", "text/plain"))
	e.want(r, 200, "")
	if !strings.Contains(r.header.Get("Content-Disposition"), "care-recovery-sunrise-clinic.txt") || !strings.Contains(string(r.body), " 1 "+words[0]) ||
		!strings.HasPrefix(r.header.Get("Content-Type"), "text/plain") {
		t.Fatalf("%v %s", r.header, r.body)
	}
	r = e.do("GET", "/api/v1/backups/ssh-key", nil)
	e.want(r, 200, "")
	if !strings.HasPrefix(r.json(t)["authorized_keys_line"].(string), "restrict ssh-ed25519 ") {
		t.Fatal(string(r.body))
	}
	e.want(e.do("GET", "/api/v1/backups/ssh-key?key=pending", nil), 404, "not-found")
	r = e.do("POST", "/api/v1/backups/recovery-key/confirm", map[string]any{"words": confirmBody(words, pos, 1)})
	e.want(r, 422, "recovery-key-mismatch")
	if !strings.Contains(string(r.body), `"pointer":"/words/1/word"`) {
		t.Fatal(string(r.body))
	}
	bad := confirmBody(words, pos, -1)
	bad[0].(map[string]any)["index"] = pos[0]%24 + 1
	e.want(e.do("POST", "/api/v1/backups/recovery-key/confirm", map[string]any{"words": bad}), 422, "invalid-request")
	e.want(e.do("POST", "/api/v1/backups/recovery-key/confirm", map[string]any{"key": "pending", "words": confirmBody(words, pos, -1)}), 404, "not-found")

	// The destination tests.
	local := map[string]any{"id": "local", "type": "posix", "path": "/srv/care-backups"}
	r = e.do("POST", "/api/v1/backups/destinations/test", map[string]any{"destination": local, "credentials": nil})
	e.want(r, 200, "")
	if m := r.json(t); m["ok"] != true || m["next_action"] != "none" || !strings.Contains(string(r.body), `"name":"root-disk","level":"advised","ok":false`) {
		t.Fatal(string(r.body))
	}
	e.want(e.do("POST", "/api/v1/backups/destinations/test", map[string]any{"destination": map[string]any{"id": "x", "type": "posix"}, "credentials": nil}, invalid), 422, "invalid-request")
	r = e.do("POST", "/api/v1/backups/destinations/test", map[string]any{"destination": sftp, "credentials": nil})
	e.want(r, 200, "")
	hk := r.json(t)["host_key"].(map[string]any)
	if r.json(t)["next_action"] != "accept-host-key" || hk["trusted"] != false {
		t.Fatal(string(r.body))
	}
	r = e.do("POST", "/api/v1/backups/destinations/test", map[string]any{"destination": sftp, "credentials": nil,
		"accept_fingerprint": "SHA256:uNiVztksCsDhcc0u9e8BujQXVUpKZIDTMczCvj3tD2s"})
	e.want(r, 422, "destination-untrusted-hostkey")
	r = e.do("POST", "/api/v1/backups/destinations/test", map[string]any{"destination": sftp, "credentials": nil, "accept_fingerprint": hk["fingerprint_sha256"]})
	e.want(r, 200, "")
	if m := r.json(t); m["next_action"] != "install-ssh-key" || !strings.HasPrefix(m["public_key"].(string), "restrict ssh-ed25519") {
		t.Fatal(string(r.body))
	}
	fs.refused = false
	r = e.do("POST", "/api/v1/backups/destinations/test", map[string]any{"destination": sftp, "credentials": nil, "accept_fingerprint": hk["fingerprint_sha256"]})
	e.want(r, 200, "")
	s3 := map[string]any{"id": "b2", "type": "s3", "endpoint": "https://s3.example.org", "bucket": "sunrise-care", "region": "eu-central-003"}
	creds := map[string]any{"access_key_id": "AKIAEXAMPLE", "secret_access_key": "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY"}
	r = e.do("POST", "/api/v1/backups/destinations/test", map[string]any{"destination": s3, "credentials": creds})
	e.want(r, 200, "")
	if r.json(t)["next_action"] != "create-bucket" {
		t.Fatal(string(r.body))
	}
	r = e.do("POST", "/api/v1/backups/destinations/test", map[string]any{"destination": s3, "credentials": creds, "create_bucket": true})
	e.want(r, 200, "")
	if m := r.json(t); m["ok"] != true || !strings.Contains(string(r.body), `"other_key_ids":["0badc0de"]`) {
		t.Fatal(string(r.body))
	}

	// The disks.
	e.want(e.do("GET", "/api/v1/backups/disks", nil), 200, "")
	e.want(e.do("POST", "/api/v1/backups/disks/nope/prepare", map[string]any{"wipe": true, "label": nil}), 404, "not-found")
	e.want(e.do("POST", "/api/v1/backups/disks/sda/prepare", map[string]any{"wipe": true, "label": nil}), 409, "disk-in-use")
	e.want(e.do("POST", "/api/v1/backups/disks/sdb/prepare", map[string]any{"wipe": false}, invalid), 422, "invalid-request")
	must(t, os.MkdirAll(e.p.Abs("/dev/disk/by-label"), 0o755))
	must(t, os.WriteFile(e.p.Abs("/dev/sdb1"), nil, 0o600))
	must(t, os.MkdirAll(e.p.Abs(paths.SystemdDir), 0o755))
	e.want(e.do("POST", "/api/v1/backups/disks/sdb/prepare", map[string]any{"wipe": true, "label": "usb-1"}, invalid), 422, "invalid-request")
	e.want(e.do("POST", "/api/v1/backups/disks/sdb/prepare", map[string]any{"wipe": true, "label": "care-usb-too-long"}, invalid), 422, "invalid-request")
	// The console chooses the label (the next free care-usbN); the agent uses it (alignment note of the console, wave 5).
	r = e.do("POST", "/api/v1/backups/disks/sdb/prepare", map[string]any{"wipe": true, "label": "care-usb4"})
	e.want(r, 202, "")
	j := e.waitJob(r.json(t)["id"].(string))
	if j.State != jobs.StateSucceeded || !strings.Contains(strings.ReplaceAll(string(j.Result), " ", ""), `"label":"care-usb4"`) {
		t.Fatalf("%+v %s", j.Error, j.Result)
	}
	if got := strings.Join(e.fake.Filter("wipefs", "sfdisk", "mkfs.ext4", "systemctl start var-lib"), "\n"); !strings.Contains(got, "mkfs.ext4 -F -q -m 0 -L care-usb4 /dev/sdb1") ||
		!strings.Contains(got, `systemctl start var-lib-care\x2dpi-disks-care\x2dusb4.automount`) {
		t.Fatal(got)
	}
	// A label of another connected backup disk gives 409: two disks with one label mount at random.
	e.fake.On(lsblkCmd, host.Response{Out: strings.Replace(lsblkJSON, `"mountpoints":[null]}]}`,
		`"mountpoints":[null]},{"name":"sdc","path":"/dev/sdc","size":64000000000,"type":"disk","tran":"usb","rm":true,"model":"Other","serial":"X2","fstype":null,"label":null,"mountpoints":[null],"children":[{"name":"sdc1","path":"/dev/sdc1","size":63000000000,"type":"part","fstype":"ext4","label":"care-usb9","mountpoints":[null]}]}]}`, 1)})
	e.want(e.do("POST", "/api/v1/backups/disks/sdb/prepare", map[string]any{"wipe": true, "label": "care-usb9"}), 409, "disk-in-use")
	// A disk below LUKS (or LVM, or md RAID) is in use, also when nothing on it is mounted; the detail tells why.
	e.fake.On(lsblkCmd, host.Response{Out: strings.Replace(lsblkJSON, `"mountpoints":[null]}]}`,
		`"mountpoints":[null]},{"name":"sdd","path":"/dev/sdd","size":64000000000,"type":"disk","tran":"usb","rm":true,"model":"Vault","serial":"X3","fstype":null,"label":null,"mountpoints":[null],"children":[{"name":"sdd1","path":"/dev/sdd1","size":63000000000,"type":"part","fstype":"crypto_LUKS","label":null,"mountpoints":[null],"children":[{"name":"luks-1","path":"/dev/mapper/luks-1","size":62000000000,"type":"crypt","fstype":"ext4","label":null,"mountpoints":[null]}]}]}]}`, 1)})
	r = e.do("POST", "/api/v1/backups/disks/sdd/prepare", map[string]any{"wipe": true, "label": "care-usb7"})
	e.want(r, 409, "disk-in-use")
	if d, _ := r.json(t)["detail"].(string); !strings.Contains(d, "/dev/sdd1 is a LUKS encrypted volume (crypto_LUKS)") {
		t.Fatal(string(r.body))
	}
	e.fake.On(lsblkCmd, host.Response{Out: lsblkJSON})
	// Without a label, the agent chooses the next free care-usbN.
	r = e.do("POST", "/api/v1/backups/disks/sdb/prepare", map[string]any{"wipe": true})
	e.want(r, 202, "")
	if j := e.waitJob(r.json(t)["id"].(string)); j.State != jobs.StateSucceeded || !strings.Contains(strings.ReplaceAll(string(j.Result), " ", ""), `"label":"care-usb1"`) {
		t.Fatalf("%+v %s", j.Error, j.Result)
	}

	// The step backups: the secrets of the s3 destination and the words.
	dests := []any{local, s3, map[string]any{"id": "usb", "type": "posix", "disk_label": "care-usb1"}}
	e.want(e.do("PUT", "/api/v1/setup/steps/backups", map[string]any{"destinations": dests, "credentials": map[string]any{}, "recovery_key_words": nil}, invalid), 422, "invalid-request")
	e.want(e.do("PUT", "/api/v1/setup/steps/backups", map[string]any{"destinations": dests, "credentials": map[string]any{"b2": creds},
		"recovery_key_words": confirmBody(words, pos, 0)}), 422, "recovery-key-mismatch")
	r = e.do("GET", "/api/v1/setup", nil)
	if !strings.Contains(string(r.body), `"code":"recovery-key-unconfirmed"`) {
		t.Fatal(string(r.body))
	}
	r = e.do("PUT", "/api/v1/setup/steps/backups", map[string]any{"destinations": dests, "credentials": map[string]any{"b2": creds},
		"recovery_key_words": confirmBody(words, pos, -1)})
	e.want(r, 200, "")
	if !strings.Contains(string(r.body), `{"id":"backups","status":"done"`) || !strings.Contains(string(r.body), `"credentials":{"b2":"present"}`) {
		t.Fatal(string(r.body))
	}
	e.want(e.do("PUT", "/api/v1/backups/destinations/b2/secrets", creds), 200, "")
	e.want(e.do("PUT", "/api/v1/backups/destinations/local/secrets", creds), 422, "invalid-request")
	e.want(e.do("PUT", "/api/v1/backups/destinations/nope/secrets", creds), 404, "not-found")
	r = e.do("GET", "/api/v1/backups", nil)
	e.want(r, 200, "")
	if m := r.json(t); m["configured"] != true || m["recovery_key_confirmed"] != true {
		t.Fatal(string(r.body))
	}

	// Restore from backup, in the setup state.
	r = e.do("GET", "/api/v1/setup", nil)
	if !strings.Contains(string(r.body), `"restore":{"available":true,"job_id":null,"job_state":null}`) {
		t.Fatal(string(r.body))
	}
	rk := map[string]any{"words": words}
	// Swap 2 words so that the checksum fails. One swap in 256 keeps a valid checksum, so try pairs.
	sw := append([]string(nil), words...)
	for i := 1; i < len(words); i++ {
		sw[0], sw[i] = sw[i], sw[0]
		if _, err := backup.ParseWords(sw); err != nil {
			break
		}
		sw[0], sw[i] = sw[i], sw[0]
	}
	e.want(e.do("POST", "/api/v1/restore/dr/inspect", map[string]any{"recovery_key": map[string]any{"words": sw}, "destination": map[string]any{"type": "posix", "path": "/media/b"}}, invalid), 422, "invalid-request")
	e.want(e.do("POST", "/api/v1/restore/dr/inspect", map[string]any{"recovery_key": rk, "destination": map[string]any{"type": "sftp", "host": "mac.lan", "user": "care", "path": "/b"}}), 422, "destination-untrusted-hostkey")
	e.fake.On("restic snapshots --json", host.Response{Err: &host.ExitError{Code: 12, Stderr: "Fatal: wrong password or no key found"}}, host.Response{Out: `[
{"id":"aaaa111100","short_id":"aaaa1111","time":"2026-10-30T10:45:00Z","paths":["/etc/care-pi","/var/lib/care-pi/backup/pg"],"hostname":"care","tags":["db"],"summary":{"total_bytes_processed":5000000}},
{"id":"bbbb222200","short_id":"bbbb2222","time":"2026-10-30T02:30:00Z","paths":["/etc/care-pi","/var/lib/care-pi/s3"],"hostname":"care","tags":["files"],"summary":{"total_bytes_processed":1000}}]`})
	e.want(e.do("POST", "/api/v1/restore/dr/inspect", map[string]any{"recovery_key": rk, "destination": map[string]any{"type": "posix", "path": "/media/b"}}), 422, "recovery-key-mismatch")
	e.fake.On("restic dump aaaa111100 /etc/care-pi/config.yaml", host.Response{Out: "version: 1\nidentity: {display_name: Old Clinic, hostname: care}\nbackups: {destinations: [{id: local, type: posix, path: /media/b}]}\n"})
	r = e.do("POST", "/api/v1/restore/dr/inspect", map[string]any{"recovery_key": rk, "destination": map[string]any{"type": "posix", "path": "/media/b"}})
	e.want(r, 200, "")
	if m := r.json(t); m["source"].(map[string]any)["display_name"] != "Old Clinic" || m["newest_files_snapshot"] == nil {
		t.Fatal(string(r.body))
	}
	r = e.do("POST", "/api/v1/restore/dr", map[string]any{"recovery_key": rk, "destination": map[string]any{"type": "posix", "path": "/media/b"},
		"time": nil, "snapshot_id": nil, "files": true, "pause_backups": true})
	e.want(r, 202, "")
	id := r.json(t)["id"].(string)
	j = e.waitJob(id)
	if j.State != jobs.StateFailed {
		t.Fatalf("the restore of the fake host must fail at fetch-config: %s", j.State)
	}
	r = e.do("GET", "/api/v1/setup", nil)
	if m := r.json(t); m["state"] != "setup" || m["restore"].(map[string]any)["job_state"] != "failed" {
		t.Fatal(string(r.body))
	}
	// Before ready, the operations of a ready device give setup-incomplete.
	for _, c := range []struct {
		m, u string
		b    any
	}{
		{"POST", "/api/v1/backups/run", nil}, {"POST", "/api/v1/backups/verify", nil}, {"POST", "/api/v1/backups/drill", nil},
		{"POST", "/api/v1/backups/recovery-key/rotate", nil},
		{"POST", "/api/v1/restore", map[string]any{"kind": "pitr", "time": "2026-10-30T09:40:00Z", "checkpoint": nil}},
	} {
		e.want(e.do(c.m, c.u, c.b), 409, "setup-incomplete")
	}
}

// readyBackupEnv is a ready host (Phase 1 lock) with a confirmed key and 3 destinations.
func readyBackupEnv(t *testing.T) (*env, []string, []int) {
	e, _ := backupEnv(t)
	must(t, os.Remove(e.p.Abs(setup.File)))
	must(t, os.WriteFile(e.p.Abs(paths.LockFile), []byte("version: 1\ngenerated_at: \"2026-10-30T00:00:00Z\"\nagent_version: test\nsolution: {name: care-clinic, track: stable}\nimages:\n  postgres: {ref: localhost/care-pi/postgres-pgbackrest:17}\n"), 0o600))
	e.claim()
	if st := e.svc.SetupStateName(); st != setup.Ready {
		t.Fatal(st)
	}
	e.want(e.do("POST", "/api/v1/backups/run", map[string]any{"scope": "all"}), 409, "no-destination")
	e.want(e.do("POST", "/api/v1/backups/run", map[string]any{"scope": "local-db", "type": "incr", "reason": "before the update"}), 202, "")
	// A Phase 2 repository: the stanza exists with the random pass of bootstrap (legacy).
	must(t, os.MkdirAll(e.p.Abs(paths.BackupPG+"/archive/main"), 0o700))
	must(t, os.WriteFile(e.p.Abs(paths.BackupPG+"/archive/main/archive.info"), []byte("x"), 0o600))
	r := e.do("POST", "/api/v1/backups/recovery-key", nil)
	e.want(r, 201, "")
	words, pos := e.keyWords(t)
	e.want(e.do("POST", "/api/v1/backups/recovery-key/confirm", map[string]any{"words": confirmBody(words, pos, -1)}), 200, "")
	if st := e.svc.RecoveryKeyStatus(); st.LocalRepository != backup.RepoLegacy {
		t.Fatalf("%+v", st)
	}
	r = e.do("POST", "/api/v1/backups/recovery-key/rotate", map[string]any{"new_key": false, "remove_old_repositories": true, "reason": "re-key"})
	e.want(r, 202, "")
	if j := e.waitJob(r.json(t)["id"].(string)); j.State != jobs.StateSucceeded {
		t.Fatalf("%s %+v", j.State, j.Error)
	}
	k, _, _ := (&backup.KeyStore{Paths: e.p}).Get(backup.Active)
	if pass, _ := os.ReadFile(e.p.Abs(paths.Secret("pgbackrest_repo_cipher_pass"))); strings.TrimSpace(string(pass)) != k.CipherPass() {
		t.Fatal("the rotation writes the derived cipher pass")
	}
	_, _, err := e.svc.CreateRecoveryKey(context.Background())
	must(t, err)
	return e, words, pos
}

// TestTimerBackupNoCheckpoint: the incremental backup of the timer (reason "timer") is not a checkpoint in
// RestorePoints.checkpoints; a run with a reason is.
func TestTimerBackupNoCheckpoint(t *testing.T) {
	e, _, _ := readyBackupEnv(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	e.svc.Jobs.Shutdown(ctx)
	st := backup.State{Paths: e.p}
	n := len(st.Checkpoints())
	j, err := e.svc.StartBackupRun(ctx, service.BackupRunRequest{Scope: service.ScopeLocalDB, Type: "incr", Reason: "timer", FromTimer: true}, jobs.SourceTimer)
	must(t, err)
	if j = e.waitJob(j.ID); j.State != jobs.StateSucceeded {
		t.Fatalf("%s %+v", j.State, j.Error)
	}
	if cps := st.Checkpoints(); len(cps) != n {
		t.Fatalf("the timer run added a checkpoint: %+v", cps)
	}
	j, err = e.svc.StartBackupRun(ctx, service.BackupRunRequest{Scope: service.ScopeLocalDB, Type: "incr", Reason: "before the import"}, jobs.SourceConsole)
	must(t, err)
	if j = e.waitJob(j.ID); j.State != jobs.StateSucceeded {
		t.Fatalf("%s %+v", j.State, j.Error)
	}
	if cps := st.Checkpoints(); len(cps) != n+1 || cps[0].Reason == nil || *cps[0].Reason != "before the import" {
		t.Fatalf("checkpoints %+v", cps)
	}
}

// TestBackupsReady: the jobs of a ready device, the problems, and the reauthentication.
func TestBackupsReady(t *testing.T) {
	e, _, _ := readyBackupEnv(t)
	r := e.do("GET", "/api/v1/config", nil)
	etag := r.header.Get("ETag")
	dests := []any{map[string]any{"id": "local", "type": "posix", "path": "/srv/care-backups"},
		map[string]any{"id": "b2", "type": "s3", "endpoint": "https://s3.example.org", "bucket": "sunrise-care"}}
	e.want(e.do("PATCH", "/api/v1/config", map[string]any{"backups": map[string]any{"destinations": dests}},
		header("Content-Type", "application/merge-patch+json"), header("If-Match", etag)), 200, "")
	e.want(e.do("GET", "/api/v1/backups/destinations", nil), 200, "")
	r = e.do("GET", "/api/v1/backups", nil)
	if !strings.Contains(string(r.body), `"credentials":"missing"`) || !strings.Contains(string(r.body), `"code":"destination-unreachable"`) {
		t.Fatal(string(r.body))
	}
	r = e.do("GET", "/api/v1/status", nil)
	e.want(r, 200, "")
	if r.json(t)["backups"] == nil {
		t.Fatal("Status.backups")
	}
	e.want(e.do("POST", "/api/v1/backups/run", map[string]any{"destination": "nope"}), 404, "not-found")
	e.want(e.do("POST", "/api/v1/backups/run", map[string]any{"scope": "bogus"}, invalid), 422, "invalid-request")
	e.fake.On("restic backup --json --host care --tag db --exclude /etc/care-pi/secrets/recovery.key --exclude /etc/care-pi/secrets/recovery.key.pending --exclude *.pgbackrest.tmp /var/lib/care-pi/backup/pg /etc/care-pi",
		host.Response{Out: `{"message_type":"summary","data_added":10,"data_added_packed":8,"snapshot_id":"aaaa1111bbbb"}`})
	e.fake.On("restic backup --json --host care --tag files --exclude /etc/care-pi/secrets/recovery.key --exclude /etc/care-pi/secrets/recovery.key.pending --exclude *.pgbackrest.tmp /var/lib/care-pi/s3 /etc/care-pi",
		host.Response{Out: `{"message_type":"summary","data_added":10,"data_added_packed":8,"snapshot_id":"cccc2222dddd"}`})
	e.fake.On("restic snapshots --json", host.Response{Out: `[{"id":"aaaa1111bbbb","short_id":"aaaa1111","time":"2026-10-30T10:45:00Z","paths":["/etc/care-pi","/var/lib/care-pi/backup/pg"],"hostname":"care","tags":["db"]}]`})
	r = e.do("POST", "/api/v1/backups/run", map[string]any{"destination": "local", "reason": "now"})
	e.want(r, 202, "")
	j := e.waitJob(r.json(t)["id"].(string))
	if j.State != jobs.StateSucceeded || !strings.Contains(strings.ReplaceAll(string(j.Result), " ", ""), `"db_snapshot_id":"aaaa1111bbbb"`) {
		t.Fatalf("%s %+v %s", j.State, j.Error, j.Result)
	}
	// The s3 destination has no secret: the run fails with destination-unreachable.
	r = e.do("POST", "/api/v1/backups/run", map[string]any{"scope": "all", "type": "incr"})
	e.want(r, 202, "")
	j = e.waitJob(r.json(t)["id"].(string))
	if j.State != jobs.StateFailed || j.Error.Slug != "destination-unreachable" {
		t.Fatalf("%s %+v", j.State, j.Error)
	}
	before := len(e.fake.Filter("restic "))
	r = e.do("POST", "/api/v1/backups/verify", map[string]any{"scope": "destination", "destination": "local", "read_data_subset_pct": 10, "prune": true})
	e.want(r, 202, "")
	if j := e.waitJob(r.json(t)["id"].(string)); j.State != jobs.StateSucceeded {
		t.Fatalf("%+v", j.Error)
	}
	// A stale restic lock (a run that lost its connection) blocks forget, prune, and check: "restic unlock" comes first.
	var seq []string
	for _, c := range e.fake.Filter("restic ")[before:] {
		for _, p := range []string{"restic unlock", "restic forget", "restic prune", "restic check"} {
			if strings.HasPrefix(c, p) {
				seq = append(seq, p)
			}
		}
	}
	if strings.Join(seq, ",") != "restic unlock,restic forget,restic forget,restic prune,restic check" {
		t.Fatalf("restic calls of the verify %q", seq)
	}
	// The timer run of a destination (snapshot, then the retention) removes the stale locks before its forget too.
	before = len(e.fake.Filter("restic "))
	if err := e.svc.TimerSnapshot(context.Background(), "local", "db"); err != nil {
		t.Fatal(err)
	}
	seq = nil
	for _, c := range e.fake.Filter("restic ")[before:] {
		for _, p := range []string{"restic backup", "restic unlock", "restic forget"} {
			if strings.HasPrefix(c, p) {
				seq = append(seq, p)
			}
		}
	}
	if strings.Join(seq, ",") != "restic backup,restic unlock,restic forget" {
		t.Fatalf("restic calls of the timer run %q", seq)
	}
	e.want(e.do("POST", "/api/v1/backups/verify", map[string]any{"read_data_subset_pct": 0}, invalid), 422, "invalid-request")
	t.Setenv("CARE_PI_DRILL_FREE_BYTES", "1")
	e.want(e.do("POST", "/api/v1/backups/drill", map[string]any{"reason": nil}), 409, "insufficient-space")
	t.Setenv("CARE_PI_DRILL_FREE_BYTES", "999999999999")
	r = e.do("POST", "/api/v1/backups/drill", nil)
	e.want(r, 202, "")
	e.waitJob(r.json(t)["id"].(string))

	r = e.do("GET", "/api/v1/restore/points?refresh=true", nil)
	e.want(r, 200, "")
	if m := r.json(t); m["pitr"] == nil || len(m["db_backups"].([]any)) != 2 {
		t.Fatal(string(r.body))
	}
	e.want(e.do("GET", "/api/v1/restore/points?refresh=maybe", nil, invalid), 422, "invalid-request")
	old := e.clock.now().Add(-48 * time.Hour).Format(time.RFC3339)
	r = e.do("POST", "/api/v1/restore", map[string]any{"kind": "pitr", "time": old, "checkpoint": nil})
	e.want(r, 422, "restore-window-exceeded")
	if !strings.Contains(string(r.body), `"restore_window"`) {
		t.Fatal(string(r.body))
	}
	e.want(e.do("POST", "/api/v1/restore", map[string]any{"kind": "pitr", "time": nil, "checkpoint": "20261029-010000F"}), 404, "not-found")
	e.want(e.do("POST", "/api/v1/restore", map[string]any{"kind": "files", "destination": "nope", "snapshot_id": "aaaa1111"}), 404, "not-found")
	e.want(e.do("POST", "/api/v1/restore", map[string]any{"kind": "other"}, invalid), 422, "invalid-request")
	e.want(e.do("POST", "/api/v1/backups/recovery-key/rotate", map[string]any{"new_key": true}), 409, "state-conflict")
	e.want(e.do("POST", "/api/v1/backups/recovery-key/rotate", map[string]any{"new_key": false}), 409, "state-conflict")

	// A restore holds the apply lock: a backup gives job-running while it runs.
	release, err := e.svc.Jobs.AcquireLock(context.Background())
	must(t, err)
	rec, err := jobs.Create(jobs.CreateOptions{Paths: e.p, Kind: jobs.KindRestorePITR, Source: jobs.SourceCLI})
	must(t, err)
	e.want(e.do("POST", "/api/v1/backups/run", nil), 409, "job-running")
	e.want(e.do("POST", "/api/v1/restore", map[string]any{"kind": "pitr", "time": e.clock.now().Add(-time.Minute).Format(time.RFC3339), "checkpoint": nil}), 409, "job-running")
	e.want(e.do("POST", "/api/v1/backups/recovery-key", nil), 409, "job-running")
	rec.Finish(nil, nil)
	release()
	// A time after the end of the archived WAL (now - 1 min in the fake) and before the start of the
	// job is valid: the checkpoint of the job archives the WAL up to its start (alignment note, wave 5).
	future := e.clock.now().Add(time.Hour).Format(time.RFC3339)
	e.want(e.do("POST", "/api/v1/restore", map[string]any{"kind": "pitr", "time": future, "checkpoint": nil}), 422, "restore-window-exceeded")
	r = e.do("POST", "/api/v1/restore", map[string]any{"kind": "pitr", "time": e.clock.now().Add(-5 * time.Second).Format(time.RFC3339), "checkpoint": nil, "reason": "contract"})
	e.want(r, 202, "")
	e.waitJob(r.json(t)["id"].(string))
	e.want(e.do("POST", "/api/v1/restore/dr/inspect", map[string]any{"recovery_key": map[string]any{"words": strings.Fields(strings.Repeat("abandon ", 23) + "art")},
		"destination": map[string]any{"type": "posix", "path": "/media/b"}}), 409, "setup-finished")
	e.want(e.do("POST", "/api/v1/restore/dr", map[string]any{"recovery_key": map[string]any{"words": strings.Fields(strings.Repeat("abandon ", 23) + "art")},
		"destination": map[string]any{"type": "posix", "path": "/media/b"}}), 409, "setup-finished")

	// A pending key: show it, its SSH key, and a rotation needs its confirmation.
	e.want(e.do("GET", "/api/v1/backups/recovery-key?key=pending", nil), 200, "")
	e.want(e.do("GET", "/api/v1/backups/ssh-key?key=pending", nil), 200, "")
	e.want(e.do("GET", "/api/v1/backups/recovery-key/download?key=pending", nil), 200, "")
	e.want(e.do("POST", "/api/v1/backups/recovery-key/rotate", nil), 409, "state-conflict")

	// The operations with a recent authentication.
	e.clock.add(6 * time.Minute)
	for _, c := range []struct {
		m, u string
		b    any
	}{
		{"GET", "/api/v1/backups/recovery-key", nil},
		{"GET", "/api/v1/backups/recovery-key/download", nil},
		{"POST", "/api/v1/backups/recovery-key/rotate", nil},
		{"POST", "/api/v1/backups/disks/sdb/prepare", map[string]any{"wipe": true}},
		{"POST", "/api/v1/restore", map[string]any{"kind": "pitr", "time": "2026-10-30T09:40:00Z", "checkpoint": nil}},
	} {
		e.want(e.do(c.m, c.u, c.b), 403, "reauth-required")
	}
}

// TestDRSessionsEndAfterDone: the job restore-dr ends the sessions only after its open event
// stream sent the event done with the result (alignment note of the console, wave 5).
func TestDRSessionsEndAfterDone(t *testing.T) {
	e := newEnv(t)
	e.claim()
	opened := make(chan struct{})
	j, err := e.svc.Jobs.Start(t.Context(), jobs.Spec{Kind: jobs.KindRestoreDR, Source: jobs.SourceConsole,
		AfterDone: func(j *jobs.Job) { e.svc.EndSessionsAfterDone(j.ID, jobs.SourceConsole) }},
		func(ctx context.Context, rec *jobs.Recorder) (any, error) {
			select {
			case <-opened:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
			end := rec.StepAction("restore-account: the console account of the old device", "restore-account")
			end(nil)
			return map[string]any{"kind": "restore-dr", "display_name": "Old Clinic"}, nil
		})
	must(t, err)
	req, _ := http.NewRequestWithContext(t.Context(), "GET", e.hs.URL+"/api/v1/jobs/"+j.ID+"/events?after=0", nil)
	req.Host = "care.local:8443"
	req.Header.Set("Accept", "text/event-stream")
	req.AddCookie(&http.Cookie{Name: CookieName, Value: e.cookie})
	res, err := http.DefaultClient.Do(req)
	must(t, err)
	defer func() { _ = res.Body.Close() }()
	var buf []byte
	deadline := time.Now().Add(10 * time.Second)
	first := true
	for !strings.Contains(string(buf), "event: done") && time.Now().Before(deadline) {
		chunk := make([]byte, 4096)
		n, err := res.Body.Read(chunk)
		buf = append(buf, chunk[:n]...)
		if first {
			first = false
			close(opened)
		}
		if err != nil {
			break
		}
	}
	if !strings.Contains(string(buf), "event: done") || !strings.Contains(string(buf), `"display_name":"Old Clinic"`) {
		t.Fatalf("the stream must get the event done with the result:\n%s", buf)
	}
	must(t, e.svc.Jobs.Wait(t.Context(), j.ID))
	e.want(e.do("GET", "/api/v1/session", nil), 401, "not-authenticated")
}
