package backup

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rithviknishad/care-pi/agent/internal/config"
	"github.com/rithviknishad/care-pi/agent/internal/host"
	"github.com/rithviknishad/care-pi/agent/internal/paths"
	"github.com/rithviknishad/care-pi/agent/internal/problem"
)

func seqKey(t *testing.T) Key {
	t.Helper()
	k, err := ParseHex(vectors[0].hex)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func TestLocations(t *testing.T) {
	kid := "b7e88184"
	cases := []struct {
		d               config.BackupDestination
		loc, repository string
	}{
		{config.BackupDestination{ID: "usb", Type: TypePOSIX, DiskLabel: "care-usb1"},
			"/var/lib/care-pi/disks/care-usb1/care-pi-b7e88184", "/var/lib/care-pi/disks/care-usb1/care-pi-b7e88184"},
		{config.BackupDestination{ID: "local", Type: TypePOSIX, Path: "/srv/care/"},
			"/srv/care/care-pi-b7e88184", "/srv/care/care-pi-b7e88184"},
		{config.BackupDestination{ID: "nas", Type: TypeSFTP, Host: "nas.lan", User: "care", Path: "/volume1/care"},
			"sftp:care@nas.lan:/volume1/care/care-pi-b7e88184", "sftp:care-dest-nas:/volume1/care/care-pi-b7e88184"},
		{config.BackupDestination{ID: "b2", Type: TypeS3, Endpoint: "https://s3.example.org/", Bucket: "care", Prefix: "pi"},
			"s3:https://s3.example.org/care/pi/care-pi-b7e88184", "s3:https://s3.example.org/care/pi/care-pi-b7e88184"},
	}
	for _, c := range cases {
		if got := Location(c.d, kid); got != c.loc {
			t.Errorf("%s location %s", c.d.ID, got)
		}
		if got := Repository(c.d, kid); got != c.repository {
			t.Errorf("%s repository %s", c.d.ID, got)
		}
	}
}

func TestSnapshotAndForgetArgs(t *testing.T) {
	got := strings.Join(SnapshotArgs(TagDB, "care"), " ")
	want := "backup --json --host care --tag db --exclude /etc/care-pi/secrets/recovery.key --exclude /etc/care-pi/secrets/recovery.key.pending --exclude *.pgbackrest.tmp /var/lib/care-pi/backup/pg /etc/care-pi"
	if got != want {
		t.Fatalf("snapshot args\n got %s\nwant %s", got, want)
	}
	if got := strings.Join(SnapshotArgs(TagFiles, "care"), " "); !strings.HasSuffix(got, "--tag files --exclude /etc/care-pi/secrets/recovery.key --exclude /etc/care-pi/secrets/recovery.key.pending --exclude *.pgbackrest.tmp /var/lib/care-pi/s3 /etc/care-pi") {
		t.Fatal(got)
	}
	// The local CA of Caddy, when it exists (ops.backup adds it).
	if got := strings.Join(SnapshotArgs(TagDB, "care", "/var/lib/care-pi/caddy/caddy/pki/authorities/local"), " "); !strings.HasSuffix(got, "/var/lib/care-pi/backup/pg /etc/care-pi /var/lib/care-pi/caddy/caddy/pki/authorities/local") {
		t.Fatal(got)
	}
	b := config.Default().Backups
	r := RetentionOf(config.BackupDestination{}, b)
	if r != (Retention{KeepWithinDays: 14, KeepDaily: 14, KeepWeekly: 8, KeepMonthly: 6}) {
		t.Fatalf("defaults %+v", r)
	}
	// The db snapshots keep 48 hours (owner decision of wave 5a); the files keep the config.
	if got := strings.Join(ForgetArgs(TagDB, r), " "); got != "forget --tag db --group-by host,tags --keep-last 1 --keep-within 48h --keep-daily 14 --keep-weekly 8 --keep-monthly 6" {
		t.Fatal(got)
	}
	zero := 0
	d := config.BackupDestination{Retention: &config.DestinationRetention{KeepMonthly: &zero, KeepWithinDays: &zero}}
	if got := strings.Join(ForgetArgs(TagFiles, RetentionOf(d, b)), " "); got != "forget --tag files --group-by host,tags --keep-last 1 --keep-daily 14 --keep-weekly 8" {
		t.Fatal(got)
	}
	if got := strings.Join(CheckArgs(5), " "); got != "check --read-data-subset=5%" {
		t.Fatal(got)
	}
	if got := strings.Join(RestoreArgs("abc", "/var/lib/care-pi/s3", "/tmp/x", []string{"patient-bucket/2026"}, true), " "); got != "restore abc:/var/lib/care-pi/s3 --target /tmp/x --include /patient-bucket/2026 --verify" {
		t.Fatal(got)
	}
}

func TestSessionEnv(t *testing.T) {
	p := paths.New(t.TempDir())
	k := seqKey(t)
	for _, d := range []config.BackupDestination{
		{ID: "b2", Type: TypeS3, Endpoint: "http://127.0.0.1:7071", Bucket: "care", Region: "us-east-1"},
		{ID: "nas", Type: TypeSFTP, Host: "nas.lan", User: "care", Path: "/v", HostKey: "ssh-ed25519 AAAA"},
	} {
		s, err := Open(p, Repo{Dest: d, Key: k, S3: &Credentials{AccessKeyID: "AKIA", SecretAccessKey: "SECRET-VALUE"}, Hostname: "care"})
		if err != nil {
			t.Fatal(err)
		}
		c := s.Cmd("snapshots", "--json")
		line := c.String() + " " + strings.Join(c.Env, " ")
		for _, secret := range []string{"SECRET-VALUE", k.ResticPassword(), k.Hex()} {
			if strings.Contains(line, secret) {
				t.Fatalf("a secret in the command or the environment: %s", line)
			}
		}
		pw, _ := os.ReadFile(filepath.Join(s.Dir(), "password"))
		if strings.TrimSpace(string(pw)) != k.ResticPassword() {
			t.Fatal("password file")
		}
		switch d.Type {
		case TypeS3:
			if !strings.Contains(c.String(), "-o s3.bucket-lookup=path -o s3.region=us-east-1 snapshots --json") {
				t.Fatal(c.String())
			}
			cred, _ := os.ReadFile(filepath.Join(s.Dir(), "aws-credentials"))
			if !strings.Contains(string(cred), "aws_secret_access_key = SECRET-VALUE") {
				t.Fatal(string(cred))
			}
		case TypeSFTP:
			if !strings.Contains(c.String(), "-o sftp.args=-F ") || !strings.Contains(c.String(), "/ssh_config -i ") {
				t.Fatal(c.String())
			}
			cfg, _ := os.ReadFile(filepath.Join(s.Dir(), "ssh_config"))
			if !strings.Contains(string(cfg), "HostKeyAlgorithms ssh-ed25519") || !strings.Contains(string(cfg), "StrictHostKeyChecking yes") {
				t.Fatal(string(cfg))
			}
		}
		s.Close()
		if _, err := os.Stat(s.Dir()); !os.IsNotExist(err) {
			t.Fatal("the run directory stays after Close")
		}
	}
}

func TestClassify(t *testing.T) {
	cases := map[string]struct {
		err  error
		slug string
	}{
		"password": {&host.ExitError{Code: 12, Stderr: `{"message_type":"exit_error","code":12,"message":"Fatal: wrong password or no key found"}`}, problem.RecoveryKeyMismatch},
		"hostkey":  {&host.ExitError{Code: 1, Stderr: "Host key verification failed.\nFatal: unable to open repository"}, problem.DestinationUntrusted},
		"network":  {&host.ExitError{Code: 1, Stderr: "ssh: connect to host x port 2299: Connection refused"}, problem.DestinationUnreachable},
	}
	for name, c := range cases {
		if got := Classify("nas", c.err); !problem.Is(got, c.slug) {
			t.Errorf("%s: %v", name, got)
		}
	}
	var rm *RepoMissingError
	if !errors.As(Classify("nas", &host.ExitError{Code: 10}), &rm) || ErrorCode(rm) != "repository-missing" {
		t.Fatal("repository missing")
	}
	if got := errorText(`{"message_type":"exit_error","code":1,"message":"Fatal: boom"}`); got != "Fatal: boom" {
		t.Fatal(got)
	}
}

func TestParseOutputs(t *testing.T) {
	sum, err := ParseBackupSummary([]byte(`{"message_type":"status","percent_done":0.5}
{"message_type":"summary","files_new":3,"data_added":2048,"data_added_packed":1000,"total_bytes_processed":4096,"snapshot_id":"4f1e9a2c77"}`))
	if err != nil || sum.SnapshotID != "4f1e9a2c77" || sum.DataAddedPacked != 1000 {
		t.Fatalf("%+v %v", sum, err)
	}
	list, err := ParseSnapshots([]byte(`[{"id":"a1","short_id":"a1","time":"2026-10-30T10:00:00Z","paths":["/etc/care-pi","/var/lib/care-pi/backup/pg"],"hostname":"care","tags":["db"]},
{"id":"b2","short_id":"b2","time":"2026-10-30T11:00:00Z","paths":["/etc/care-pi","/var/lib/care-pi/s3"],"hostname":"care","tags":["files"],"summary":{"total_bytes_processed":10}}]`))
	if err != nil || list[0].ID != "b2" || strings.Join(list[0].Contents(), ",") != "files,config" || *list[0].Size() != 10 || list[1].Size() != nil {
		t.Fatalf("%+v %v", list, err)
	}
	ri, err := ParseInfo([]byte(`[{"name":"main","cipher":"aes-256-cbc","archive":[{"min":"000000010000000000000001","max":"000000010000000000000009"}],
"backup":[{"label":"20261030-010000F","type":"full","annotation":{"care-pi-reason":"setup"},"timestamp":{"start":1793322000,"stop":1793322004},"info":{"size":48000000,"repository":{"delta":4600000}}},
{"label":"20261030-010000F_20261030-020000I","type":"incr","timestamp":{"start":1793325600,"stop":1793325605},"info":{"size":48000000,"repository":{"delta":26000}}}],"status":{"code":0,"message":"ok"}}]`), "main")
	if err != nil || len(ri.Backups) != 2 || ri.Last().Type != "incr" || *ri.Backups[0].Reason != "setup" || ri.WindowFrom().Unix() != 1793322004 || FullSpaceNeeded(ri) != 5520000 {
		t.Fatalf("%+v %v", ri, err)
	}
	st, err := ParseState([]byte("1793325600||3|48000000|false\n"))
	if err != nil || st.Timeline != 3 || st.LastFailedAt != nil || st.LastArchivedAt.Unix() != 1793325600 || st.InRecovery {
		t.Fatalf("%+v %v", st, err)
	}
}

// TestDecryptInfo checks the decryption of a pgBackRest info file: testdata/backup.info.enc is the
// output of "openssl enc -aes-256-cbc -md sha1 -pass pass:secret" (the format of
// pgBackRest, checked on lumine with its backup.info).
func TestDecryptInfo(t *testing.T) {
	plain, err := os.ReadFile("testdata/backup.info.plain")
	if err != nil {
		t.Fatal(err)
	}
	enc, err := os.ReadFile("testdata/backup.info.enc")
	if err != nil {
		t.Fatal(err)
	}
	got, err := DecryptInfo(enc, "secret")
	if err != nil || string(got) != string(plain) {
		t.Fatalf("%q %v", got, err)
	}
	if _, err := DecryptInfo(enc, "wrong"); err == nil {
		t.Fatal("a wrong pass must fail")
	}
	b := ParseBackupInfo(got)
	if len(b) != 1 || b[0].Type != "full" || b[0].SizeBytes != 48000000 {
		t.Fatalf("%+v", b)
	}
}

func TestHealth(t *testing.T) {
	now := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
	at := func(h float64) *time.Time { t := now.Add(time.Duration(-h * float64(time.Hour))); return &t }
	code := func(c string) *string { return &c }
	b := config.Default().Backups
	b.Destinations = []config.BackupDestination{{ID: "usb", Type: TypePOSIX, DiskLabel: "care-usb1"}, {ID: "nas", Type: TypeSFTP, Host: "n", User: "u", Path: "/p", HostKey: "ssh-ed25519 AAAA"},
		{ID: "b2", Type: TypeS3, Endpoint: "https://x", Bucket: "bbb"}, {ID: "new", Type: TypePOSIX, Path: "/x"}}
	free := int64(1 << 30)
	in := HealthInput{Now: now, Config: b, Key: KeyStatus{State: KeyUnconfirmed, LocalRepository: RepoLegacy},
		Local: &LocalState{LastScheduledOK: new(bool), LastFullSize: 1 << 30}, LocalFree: &free,
		Dests: []DestInput{
			{Dest: b.Destinations[0], State: &DestState{LastAttemptAt: at(1), LastSuccessAt: at(2), FailingSince: at(1), LastErrorCode: code("destination-unreachable")}, Credentials: CredNotNeeded},
			{Dest: b.Destinations[1], State: &DestState{LastAttemptAt: at(1), LastSuccessAt: at(30), FailingSince: at(29)}, Credentials: CredNotNeeded},
			{Dest: b.Destinations[2], State: &DestState{}, Credentials: CredMissing},
			{Dest: b.Destinations[3], State: &DestState{}, Credentials: CredNotNeeded},
		}, Drill: &DrillOutcome{Outcome: "failed", FinishedAt: now}}
	states := []string{}
	for _, d := range in.Dests {
		s, _ := DestStatus(in, d)
		states = append(states, s)
	}
	if strings.Join(states, " ") != "degraded error error pending" {
		t.Fatalf("states %v", states)
	}
	h := ComputeHealth(in)
	var codes []string
	for _, x := range h.Banners {
		codes = append(codes, x.Code)
	}
	want := "destination-unreachable destination-unreachable recovery-key-unconfirmed local-disk-low local-backup-failed drill-failed local-repository-legacy-key"
	if strings.Join(codes, " ") != want || h.State != SevError {
		t.Fatalf("banners %s", strings.Join(codes, " "))
	}
	// The WAL archive: a failure that the agent saw 11 minutes ago with archive_timeout 5 minutes.
	in = HealthInput{Now: now, Config: b, Key: KeyStatus{State: KeyConfirmed, LocalRepository: RepoDerived},
		Local: &LocalState{WALFailingSince: at(11.0 / 60)}, PG: &PGState{LastArchivedAt: at(1), LastFailedAt: at(0.01)}}
	b2 := in.Config
	b2.Destinations = nil
	b2.Paused = true
	in.Config = b2
	h = ComputeHealth(in)
	codes = nil
	for _, x := range h.Banners {
		codes = append(codes, x.Code)
	}
	if strings.Join(codes, " ") != "no-destination wal-archive-failing backups-paused" {
		t.Fatalf("banners %v", codes)
	}
	in.PG.LastArchivedAt = at(0)
	if WALFailing(in.PG) {
		t.Fatal("a newer success ends the failure")
	}
}

func TestStateRuns(t *testing.T) {
	now := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
	st := State{Paths: paths.New(t.TempDir()), Now: func() time.Time { return now }}
	for i := range 12 {
		finish := st.AddRun("usb", DestinationRun{StartedAt: now.Add(time.Duration(i) * time.Minute), Kind: TagDB, Source: "timer"})
		var err error
		if i == 11 {
			err = problem.Unreachable("usb", "the backup disk care-usb1 is not connected")
		}
		finish(err == nil, "abcd1234", nil, err)
	}
	d := st.Dest("usb")
	if len(d.Runs) != MaxRuns || *d.Runs[0].OK || *d.Runs[0].ErrorCode != "destination-unreachable" || d.FailingSince == nil || d.LastSuccessAt == nil {
		t.Fatalf("%+v", d)
	}
	if !*d.Runs[1].OK || *d.Runs[1].SnapshotID != "abcd1234" {
		t.Fatalf("%+v", d.Runs[1])
	}
	release, err := Lock(context.Background(), st.Paths, "usb", 0, nil)
	if err != nil || !Running(st.Paths, "usb") {
		t.Fatal(err)
	}
	if _, err := Lock(context.Background(), st.Paths, "usb", 0, nil); !errors.Is(err, ErrLockTimeout) {
		t.Fatal(err)
	}
	release()
	if Running(st.Paths, "usb") {
		t.Fatal("released")
	}
}
