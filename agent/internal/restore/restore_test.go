package restore

import (
	"context"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/rithviknishad/care-pi/agent/internal/backup"
	"github.com/rithviknishad/care-pi/agent/internal/config"
	"github.com/rithviknishad/care-pi/agent/internal/host"
	"github.com/rithviknishad/care-pi/agent/internal/jobs"
	"github.com/rithviknishad/care-pi/agent/internal/paths"
	"github.com/rithviknishad/care-pi/agent/internal/problem"
)

const (
	image   = "localhost/care-pi/postgres-pgbackrest:17"
	pgExec  = "podman exec --user postgres care-db "
	infoCmd = pgExec + "pgbackrest --stanza=main info --output=json"
	state   = pgExec + "psql -X -q -At -d care -c " + backup.StateQuery
	inRecov = pgExec + "psql -X -q -At -d care -c select pg_is_in_recovery()"
)

// info returns a pgbackrest info with the labels (full first).
func info(labels ...string) string {
	var b []string
	for i, l := range labels {
		typ := "full"
		if strings.Contains(l, "_") {
			typ = "incr"
		}
		b = append(b, `{"label":"`+l+`","type":"`+typ+`","timestamp":{"start":`+itoa(1793322000+i*100)+`,"stop":`+itoa(1793322005+i*100)+`},"info":{"size":48000000,"repository":{"delta":1000}}}`)
	}
	return `[{"name":"main","archive":[{"min":"000000010000000000000001","max":"000000010000000000000009"}],"backup":[` + strings.Join(b, ",") + `],"status":{"code":0,"message":"ok"}}]`
}

func itoa(n int) string { return strconv.Itoa(n) }

type tenv struct {
	e    Env
	fake *host.Fake
	p    paths.Paths
	rec  *jobs.Recorder
}

func newTenv(t *testing.T, kind string) *tenv {
	t.Helper()
	p := paths.New(t.TempDir())
	fake := host.NewFake()
	h := host.New(fake)
	rec, err := jobs.Create(jobs.CreateOptions{Paths: p, Kind: kind, Source: jobs.SourceCLI})
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Identity.Hostname = "care"
	fake.On(inRecov, host.Response{Out: "f\n"})
	fake.On(state, host.Response{Out: "1793322000||2|48000000|false\n"})
	e := Env{Env: backup.Env{Paths: p, Runner: fake, Systemd: h.Systemd, Podman: h.Podman}, Rec: rec, Config: cfg, Image: image,
		Poll: time.Millisecond, Timeout: time.Second, LockWait: time.Second,
		Healthy: func(context.Context) error { return nil }}
	return &tenv{e: e, fake: fake, p: p, rec: rec}
}

// actions returns the commands that change the host, in order.
func (te *tenv) actions() []string {
	var out []string
	for _, c := range te.fake.Calls {
		switch {
		case strings.HasPrefix(c, "systemctl show"), strings.Contains(c, "pg_isready"), c == infoCmd, c == state, c == inRecov:
			continue
		}
		out = append(out, c)
	}
	return out
}

func (te *tenv) steps() []string {
	var out []string
	for _, s := range te.rec.Job().Steps {
		out = append(out, *s.Action+":"+s.State)
	}
	return out
}

func check(t *testing.T, name string, got, want []string) {
	t.Helper()
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("%s\n--- got ---\n%s\n--- want ---\n%s", name, strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestPITR(t *testing.T) {
	te := newTenv(t, jobs.KindRestorePITR)
	cp := "20261030-010000F_20261030-011000I"
	te.fake.On(infoCmd, host.Response{Out: info("20261030-010000F", cp)}, host.Response{Out: info("20261030-010000F", cp)},
		host.Response{Out: info("20261030-010000F", cp, "20261030-012000F")})
	at := time.Date(2026, 10, 30, 9, 40, 0, 0, time.UTC)
	res, err := PITR(context.Background(), te.e, PITRRequest{Time: &at, Reason: "undo the import", Source: jobs.SourceConsole})
	if err != nil {
		t.Fatal(err)
	}
	restoreCmd := PodmanRun(image, "care-pi-restore", paths.PGData, false, "pgbackrest", RestoreArgs(&at, "", false)...).String()
	check(t, "commands", te.actions(), []string{
		"systemctl stop care-backup.target",
		"systemctl stop care-api.service care-worker.service care-beat.service",
		pgExec + "pgbackrest --stanza=main backup --type=incr --archive-check --archive-timeout=120 --annotation=care-pi-reason=undo the import",
		"systemctl stop care-db.service",
		restoreCmd,
		"systemctl reset-failed care-db.service",
		"systemctl start care-db.service",
		"systemctl reset-failed care-api.service care-worker.service care-beat.service care-db.service",
		"systemctl start care.target",
		pgExec + "pgbackrest --stanza=main backup --type=full --archive-check --archive-timeout=120 --annotation=care-pi-reason=after restore-pitr " + te.rec.ID(),
		pgExec + "pgbackrest --stanza=main check",
		"systemctl start care-backup.target",
	})
	if !strings.Contains(restoreCmd, "--type=time --target=2026-10-30 09:40:00.000000+00 --target-action=promote") ||
		!strings.Contains(restoreCmd, "--volume /var/lib/care-pi/pg:/var/lib/postgresql/data") {
		t.Fatal(restoreCmd)
	}
	check(t, "steps", te.steps(), []string{"pause-backups:succeeded", "maintenance-on:succeeded", "stop-app:succeeded", "checkpoint:succeeded",
		"stop-db:succeeded", "restore-db:succeeded", "start-db:succeeded", "wait-promote:succeeded", "start-app:succeeded",
		"wait-healthy:succeeded", "maintenance-off:succeeded", "backup-full:succeeded", "check-stanza:succeeded", "resume-backups:succeeded"})
	if res.Checkpoint.Label != cp || *res.Checkpoint.JobKind != jobs.KindRestorePITR || res.Timeline != 2 || *res.FullBackupLabel != "20261030-012000F" || !res.RestoredTo.Equal(at) {
		t.Fatalf("%+v", res)
	}
	if cps := te.e.State().Checkpoints(); len(cps) != 1 || cps[0].Label != cp {
		t.Fatalf("checkpoints %+v", cps)
	}
	if _, err := os.Stat(te.p.Abs(paths.ModeDir + "/maintenance")); !os.IsNotExist(err) {
		t.Fatal("the maintenance page stays on")
	}
}

// TestPITRRollback: the restore of the time fails, so the job restores its checkpoint, starts the
// stack, and fails with the first error.
func TestPITRRollback(t *testing.T) {
	te := newTenv(t, jobs.KindRestorePITR)
	cp := "20261030-010000F_20261030-011000I"
	te.fake.On(infoCmd, host.Response{Out: info("20261030-010000F", cp)})
	at := time.Date(2026, 10, 30, 9, 40, 0, 0, time.UTC)
	timeRestore := PodmanRun(image, "care-pi-restore", paths.PGData, false, "pgbackrest", RestoreArgs(&at, "", false)...)
	te.fake.On(timeRestore.String(), host.Response{Code: 56})
	rollback := PodmanRun(image, "care-pi-restore", paths.PGData, false, "pgbackrest", RestoreArgs(nil, cp, false)...)
	_, err := PITR(context.Background(), te.e, PITRRequest{Time: &at, Source: jobs.SourceConsole})
	if err == nil || !strings.Contains(err.Error(), "exit code 56") {
		t.Fatalf("want the error of the restore, got %v", err)
	}
	check(t, "commands", te.actions(), []string{
		"systemctl stop care-backup.target",
		"systemctl stop care-api.service care-worker.service care-beat.service",
		pgExec + "pgbackrest --stanza=main backup --type=incr --archive-check --archive-timeout=120 --annotation=care-pi-reason=before restore-pitr " + te.rec.ID(),
		"systemctl stop care-db.service",
		timeRestore.String(),
		"systemctl stop care-db.service",
		rollback.String(),
		"systemctl reset-failed care-db.service",
		"systemctl start care-db.service",
		"systemctl reset-failed care-api.service care-worker.service care-beat.service care-db.service",
		"systemctl start care.target",
		"systemctl start care-backup.target",
	})
	if !strings.Contains(rollback.String(), "--set="+cp+" --type=immediate --target-action=promote") {
		t.Fatal(rollback.String())
	}
	check(t, "steps", te.steps(), []string{"pause-backups:succeeded", "maintenance-on:succeeded", "stop-app:succeeded", "checkpoint:succeeded",
		"stop-db:succeeded", "restore-db:failed", "stop-db:succeeded", "restore-db:succeeded", "start-db:succeeded", "wait-promote:succeeded",
		"start-app:succeeded", "wait-healthy:succeeded", "maintenance-off:succeeded", "resume-backups:succeeded"})
}

// TestPITRNoCheckpoint: a failed checkpoint stops the job before a change of the database.
func TestPITRNoCheckpoint(t *testing.T) {
	te := newTenv(t, jobs.KindRestorePITR)
	te.fake.On(pgExec+"pgbackrest --stanza=main backup --type=incr --archive-check --archive-timeout=120 --annotation=care-pi-reason=x", host.Response{Code: 82})
	at := time.Now()
	_, err := PITR(context.Background(), te.e, PITRRequest{Time: &at, Reason: "x"})
	if err == nil || !strings.Contains(err.Error(), "no restore without a checkpoint") {
		t.Fatal(err)
	}
	for _, c := range te.actions() {
		if strings.Contains(c, "restore") || strings.Contains(c, "stop care-db") {
			t.Fatalf("a change after a failed checkpoint: %s", c)
		}
	}
}

func TestDrill(t *testing.T) {
	te := newTenv(t, jobs.KindRestoreDrill)
	te.fake.On(infoCmd, host.Response{Out: info("20261030-010000F")})
	te.fake.On(pgExec+"pg_controldata /var/lib/postgresql/data", host.Response{Out: "Database cluster state:               in production\n"})
	te.fake.On("podman exec --user postgres care-drill pg_controldata /var/lib/postgresql/data", host.Response{Out: "Database cluster state:               in production\n"})
	te.fake.On("podman exec --user postgres care-drill psql -X -At -d care -c select 1", host.Response{Out: "1\n"})
	res, err := Drill(context.Background(), te.e, false)
	if err != nil {
		t.Fatal(err)
	}
	if res.Outcome != "passed" || len(res.Checks) != 2 || *res.BackupLabel != "20261030-010000F" {
		t.Fatalf("%+v", res)
	}
	got := te.actions()
	if len(got) < 3 || !strings.Contains(got[0], "--stanza=main restore --set=20261030-010000F --type=immediate --target-action=promote --archive-mode=off") ||
		!strings.Contains(got[0], "/var/lib/care-pi/backup/pg:/var/lib/pgbackrest:ro") || !strings.Contains(got[1], "--unit=care-backup-drill-pg.service") ||
		!strings.Contains(got[1], "-c archive_mode=off") || strings.Contains(got[1], "--wait") {
		t.Fatalf("drill commands %q", got)
	}
	check(t, "steps", te.steps(), []string{"check-space:succeeded", "drill-restore:succeeded", "drill-start:succeeded", "drill-check:succeeded", "drill-cleanup:succeeded"})
	if d := te.e.State().Drills(); len(d) != 1 || d[0].Outcome != "passed" {
		t.Fatalf("%+v", d)
	}
	// A timer run with too little space records a skip.
	t.Setenv("CARE_PI_DRILL_FREE_BYTES", "1")
	te2 := newTenv(t, jobs.KindRestoreDrill)
	res, err = Drill(context.Background(), te2.e, true)
	if err != nil || res.Outcome != "skipped" || res.SkippedReason == nil {
		t.Fatalf("%+v %v", res, err)
	}
	te3 := newTenv(t, jobs.KindRestoreDrill)
	if _, err := Drill(context.Background(), te3.e, false); !problem.Is(err, problem.InsufficientSpace) {
		t.Fatal(err)
	}
}

func TestRotateKeepKey(t *testing.T) {
	te := newTenv(t, jobs.KindRecoveryKeyRotate)
	ks := te.e.Keys()
	k, _, _, err := ks.Create()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(te.p.Abs(paths.BackupPG+"/archive"), 0o700); err != nil {
		t.Fatal(err)
	}
	te.fake.On(infoCmd, host.Response{Out: info("20261030-010000F")})
	var pass string
	res, err := Rotate(context.Background(), te.e, RotateRequest{RemoveOld: true, Source: jobs.SourceCLI}, func(_ context.Context, p string) error {
		pass = p
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if pass != k.CipherPass() || !res.LocalRekeyed || res.NewKey || res.KeyID != k.ID() || len(res.RemovedRepositories) != 1 {
		t.Fatalf("%+v", res)
	}
	if _, err := os.Stat(te.p.Abs(LegacyDir)); !os.IsNotExist(err) {
		t.Fatal("pg.legacy stays")
	}
	check(t, "steps", te.steps(), []string{"check-destinations:succeeded", "pause-backups:succeeded", "maintenance-on:succeeded", "stop-app:succeeded",
		"stop-db:succeeded", "rekey-local:succeeded", "start-db:succeeded", "ensure-stanza:succeeded", "start-app:succeeded",
		"wait-healthy:succeeded", "maintenance-off:succeeded", "backup-full:succeeded", "remove-old-repositories:succeeded", "resume-backups:succeeded"})
}

func TestFilesSideDir(t *testing.T) {
	te := newTenv(t, jobs.KindRestoreFiles)
	ks := te.e.Keys()
	k, _, _, _ := ks.Create()
	d := config.BackupDestination{ID: "local", Type: backup.TypePOSIX, Path: "/srv/b"}
	te.e.Config.Backups.Destinations = []config.BackupDestination{d}
	snaps := `[{"id":"4f1e9a2c00","short_id":"4f1e9a2c","time":"2026-10-30T02:30:00Z","paths":["/etc/care-pi","/var/lib/care-pi/s3"],"hostname":"care","tags":["files"],"summary":{"total_bytes_processed":10}}]`
	te.fake.On("restic snapshots --json 4f1e9a2c", host.Response{Out: snaps})
	side := paths.RestoreDir + "/" + te.rec.ID()
	te.e.Runner = &stageRunner{Fake: te.fake, on: "restic restore 4f1e9a2c00:/var/lib/care-pi/s3 --target " + side + " --include /patient-bucket --verify",
		files: map[string]string{te.p.Abs(side + "/patient-bucket/patient/a.png"): "12345"}}
	res, err := Files(context.Background(), te.e, FilesRequest{Dest: d, SnapshotID: "4f1e9a2c", Paths: []string{"patient-bucket"}, Target: SideDir}, k)
	if err != nil {
		t.Fatal(err)
	}
	if res.Path != side || !res.Verified || res.Files != 1 || res.Bytes != 5 {
		t.Fatalf("%+v", res)
	}
	check(t, "commands", te.actions(), []string{"restic snapshots --json 4f1e9a2c",
		"restic restore 4f1e9a2c00:/var/lib/care-pi/s3 --target " + res.Path + " --include /patient-bucket --verify"})
	check(t, "steps", te.steps(), []string{"check-space:succeeded", "restore-files:succeeded", "verify-files:succeeded"})
}

func TestDR(t *testing.T) {
	te := newTenv(t, jobs.KindRestoreDR)
	k, _ := backup.ParseHex(strings.Repeat("ab", 32))
	d := config.BackupDestination{Type: backup.TypePOSIX, Path: "/media/b"}
	snaps := `[{"id":"aaaa111100","short_id":"aaaa1111","time":"2026-10-30T10:45:00Z","paths":["/etc/care-pi","/var/lib/care-pi/backup/pg","/var/lib/care-pi/caddy/caddy/pki/authorities/local"],"hostname":"care","tags":["db"]},
{"id":"bbbb222200","short_id":"bbbb2222","time":"2026-10-30T02:30:00Z","paths":["/etc/care-pi","/var/lib/care-pi/s3"],"hostname":"care","tags":["files"]}]`
	te.fake.On("restic snapshots --json", host.Response{Out: snaps})
	te.fake.On(infoCmd, host.Response{Out: info("20261030-110000F")})
	stage := te.p.Abs(paths.RestoreDir + "/" + te.rec.ID())
	// The restore of /etc/care-pi: the fake writes the files that restic would write.
	fetch := "restic restore aaaa111100:/etc/care-pi --target " + te.p.Host(stage)
	cfgYAML := "version: 1\nidentity: {display_name: Sunrise Clinic, hostname: care}\nimages: {postgres: " + image + "}\n"
	var calls []string
	deps := DRDeps{
		Apply: func(_ context.Context, noStart bool) error {
			calls = append(calls, map[bool]string{true: "apply restore", false: "apply full"}[noStart])
			lk := "version: 1\nimages:\n  postgres: {ref: " + image + ", digest: \"\"}\n"
			return os.WriteFile(te.p.Abs(paths.LockFile), []byte(lk), 0o600)
		},
		WritePaused:    func() error { calls = append(calls, "paused"); return nil },
		SetHostname:    func(_ context.Context, h string) error { calls = append(calls, "hostname "+h); return nil },
		RestoreAccount: func(data []byte) error { calls = append(calls, "account "+string(data)); return nil },
		Finish:         func() error { calls = append(calls, "finish"); return nil },
		URLs: func(*config.Config) (string, string, []string) {
			return "https://care.local/", "https://care.local:8443/", nil
		},
	}
	runner := &stageRunner{Fake: te.fake, on: fetch, files: map[string]string{
		stage + "/config.yaml": cfgYAML, stage + "/lock.yaml": "version: 1\n", stage + "/secrets/agent.json": `{"old":"account"}`,
		stage + "/secrets/postgres_password": "pw\n", stage + "/secrets/claim_code": "x\n"}}
	te.e.Runner = runner
	res, err := DR(context.Background(), te.e, DRRequest{Key: k, Dest: d, Files: true, Source: jobs.SourceCLI}, deps)
	if err != nil {
		t.Fatal(err)
	}
	check(t, "deps", calls, []string{"apply restore", "apply full", "hostname care", `account {"old":"account"}`, "finish"})
	got, _ := os.ReadFile(te.p.Abs(paths.ConfigFile))
	if string(got) != cfgYAML {
		t.Fatalf("config %q", got)
	}
	if _, err := os.Stat(te.p.Abs(paths.SecretsDir + "/claim_code")); !os.IsNotExist(err) {
		t.Fatal("the claim code of the old device must not come back")
	}
	if st := te.e.Keys().Status(); st.State != backup.KeyConfirmed || *st.KeyID != k.ID() {
		t.Fatalf("%+v", st)
	}
	if res.DBSnapshotID != "aaaa1111" || *res.FilesSnapshotID != "bbbb2222" || *res.SkewS != 29700 || res.ConsoleAccount != "restored" ||
		res.DisplayName != "Sunrise Clinic" || res.Hostname != "care" {
		t.Fatalf("%+v", res)
	}
	var restics []string
	for _, c := range te.actions() {
		if strings.HasPrefix(c, "restic") || strings.HasPrefix(c, "systemd-run") || strings.HasPrefix(c, "systemctl start") || strings.Contains(c, "backup --type") {
			restics = append(restics, c)
		}
	}
	check(t, "commands", restics, []string{
		"restic snapshots --json",
		"restic dump aaaa111100 /etc/care-pi/secrets/pgbackrest_repo_cipher_pass",
		"restic dump aaaa111100 /var/lib/care-pi/backup/pg/backup/main/backup.info",
		fetch,
		"restic restore aaaa111100:/var/lib/care-pi/caddy/caddy/pki/authorities/local --target " + te.p.Host(te.p.Abs(CAStage(te.rec.ID()))),
		"restic restore aaaa111100:/var/lib/care-pi/backup/pg --target /var/lib/care-pi/backup/pg",
		PodmanRun(image, "care-pi-restore", paths.PGData, false, "pgbackrest", RestoreArgs(nil, "", false)...).String(),
		"restic restore bbbb222200:/var/lib/care-pi/s3 --target /var/lib/care-pi/s3",
		"systemctl start care.target",
		pgExec + "pgbackrest --stanza=main backup --type=full --archive-check --archive-timeout=120 --annotation=care-pi-reason=after restore-dr " + te.rec.ID(),
	})
	check(t, "steps", te.steps(), []string{"check-key:succeeded", "check-space:succeeded", "fetch-config:succeeded", "fetch-db-repo:succeeded",
		"restore-db:succeeded", "fetch-files:succeeded", "start-target:succeeded", "wait-promote:succeeded", "wait-healthy:succeeded",
		"backup-full:succeeded", "set-hostname:succeeded", "restore-account:succeeded", "finish:succeeded"})
}

// TestDRWrongKey: a key that does not open the repository gives recovery-key-mismatch.
func TestDRWrongKey(t *testing.T) {
	te := newTenv(t, jobs.KindRestoreDR)
	te.fake.On("restic snapshots --json", host.Response{Err: &host.ExitError{Code: 12, Stderr: "Fatal: wrong password or no key found"}})
	k, _ := backup.ParseHex(strings.Repeat("cd", 32))
	_, err := DR(context.Background(), te.e, DRRequest{Key: k, Dest: config.BackupDestination{Type: backup.TypePOSIX, Path: "/x"}}, DRDeps{})
	if !problem.Is(err, problem.RecoveryKeyMismatch) {
		t.Fatal(err)
	}
}

// stageRunner writes files when a command runs (the restore of restic), then answers like the Fake.
type stageRunner struct {
	*host.Fake
	on    string
	files map[string]string
}

func (s *stageRunner) Run(ctx context.Context, c host.Cmd) ([]byte, error) {
	if c.String() == s.on {
		for p, data := range s.files {
			if err := os.MkdirAll(dirOf(p), 0o700); err != nil {
				return nil, err
			}
			if err := os.WriteFile(p, []byte(data), 0o600); err != nil {
				return nil, err
			}
		}
	}
	return s.Fake.Run(ctx, c)
}

func dirOf(p string) string { return p[:strings.LastIndex(p, "/")] }

// TestPruneOld: the side directories older than 7 days and the drill directories older than 1 day go.
func TestPruneOld(t *testing.T) {
	p := paths.New(t.TempDir())
	now := time.Now()
	mk := func(dir string, age time.Duration) string {
		full := p.Abs(dir)
		if err := os.MkdirAll(full+"/x", 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(full, now.Add(-age), now.Add(-age)); err != nil {
			t.Fatal(err)
		}
		return full
	}
	oldSide := mk(paths.RestoreDir+"/20261001T000000.000Z-restore-files", 8*24*time.Hour)
	newSide := mk(paths.RestoreDir+"/20261030T000000.000Z-restore-files", 6*24*time.Hour)
	oldDrill := mk(paths.DrillDir+"/20261029T000000.000Z-restore-drill", 25*time.Hour)
	got := PruneOld(p, now)
	if len(got) != 2 || got[0] != oldSide || got[1] != oldDrill {
		t.Fatal(got)
	}
	if _, err := os.Stat(newSide); err != nil {
		t.Fatal("a side directory of 6 days stays")
	}
}

// TestSwitchCA: the CA of the old device replaces the CA of the new device, the certificates of the
// new CA go, and care-caddy restarts. Without a stage nothing changes.
func TestSwitchCA(t *testing.T) {
	p := paths.New(t.TempDir())
	f := host.NewFake()
	if ok, err := SwitchCA(context.Background(), p, f, "j1"); ok || err != nil || len(f.Calls) != 0 {
		t.Fatalf("%v %v %v", ok, err, f.Calls)
	}
	write := func(path, data string) {
		if err := os.MkdirAll(dirOf(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for _, n := range []string{"root.crt", "root.key", "intermediate.crt", "intermediate.key"} {
		write(p.Abs(CAStage("j1")+"/"+n), "old "+n)
		write(p.Abs(paths.CaddyCA+"/"+n), "new "+n)
	}
	write(p.Abs(paths.CaddyLocalCerts+"/care.local/care.local.crt"), "leaf of the new CA")
	ok, err := SwitchCA(context.Background(), p, f, "j1")
	if !ok || err != nil {
		t.Fatal(ok, err)
	}
	if got, _ := os.ReadFile(p.Abs(paths.CaddyCA + "/root.key")); string(got) != "old root.key" {
		t.Fatalf("root.key %q", got)
	}
	for _, gone := range []string{paths.CaddyLocalCerts, CAStage("j1"), paths.CaddyCA + ".setup"} {
		if _, err := os.Stat(p.Abs(gone)); !os.IsNotExist(err) {
			t.Fatalf("%s must be gone: %v", gone, err)
		}
	}
	check(t, "commands", f.Calls, []string{"systemctl restart care-caddy.service"})
	// An incomplete stage changes nothing.
	write(p.Abs(CAStage("j2")+"/root.crt"), "x")
	if ok, err := SwitchCA(context.Background(), p, f, "j2"); ok || err == nil {
		t.Fatal("an incomplete CA must fail")
	}
}
