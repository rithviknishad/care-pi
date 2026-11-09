package apply

import (
	"archive/tar"
	"bytes"
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/klauspost/compress/zstd"

	"github.com/rithviknishad/care-pi/agent/internal/host"
	"github.com/rithviknishad/care-pi/agent/internal/lock"
	"github.com/rithviknishad/care-pi/agent/internal/paths"
)

const pgImageDigest = "ghcr.io/rithviknishad/care-pi/postgres-pgbackrest@sha256:2c350c353b6e10b86f8cb2cf1a661c7a2591983e4d6ae656c3068802df21d90d"

// writeSeed writes a seed pg-<sha12>.tar.zst with its .json into the seed directory of p.
func writeSeed(t *testing.T, p paths.Paths, sha string, extra map[string]string) string {
	t.Helper()
	files := map[string]string{"./PG_VERSION": "17\n", "./base/5/1259": "catalog", "./global/pg_control": "control"}
	for k, v := range extra {
		files[k] = v
	}
	var tb bytes.Buffer
	tw := tar.NewWriter(&tb)
	_ = tw.WriteHeader(&tar.Header{Name: "./", Typeflag: tar.TypeDir, Mode: 0o700})
	for _, d := range []string{"./base/", "./base/5/", "./global/"} {
		_ = tw.WriteHeader(&tar.Header{Name: d, Typeflag: tar.TypeDir, Mode: 0o700})
	}
	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}
	slices.Sort(names)
	for _, n := range names {
		_ = tw.WriteHeader(&tar.Header{Name: n, Typeflag: tar.TypeReg, Mode: 0o600, Size: int64(len(files[n])), Uid: 999, Gid: 999})
		_, _ = tw.Write([]byte(files[n]))
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	var zb bytes.Buffer
	zw, _ := zstd.NewWriter(&zb)
	_, _ = zw.Write(tb.Bytes())
	_ = zw.Close()
	dir := p.Abs(paths.SeedDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, "pg-"+sha[:12]+".tar.zst")
	if err := os.WriteFile(file, zb.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(strings.TrimSuffix(file, ".tar.zst")+".json", []byte(`{"care_git_sha":"`+sha+`"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	return file
}

func seedTarget(careSHA string) *Prepared {
	return &Prepared{Lock: &lock.Lock{Images: map[string]lock.Image{
		"postgres": {Ref: "ghcr.io/rithviknishad/care-pi/postgres-pgbackrest:17", Digest: strings.TrimPrefix(pgImageDigest, "ghcr.io/rithviknishad/care-pi/postgres-pgbackrest@")}},
		Components: lock.Components{Care: &lock.CareComponent{GitSHA: careSHA}}}}
}

// S72: the first start restores the seed into the empty data directory and sets the password of the device; a
// second start does nothing.
func TestSeedDBRestoresAndSetsThePassword(t *testing.T) {
	p := paths.New(t.TempDir())
	pg := p.Abs(paths.PGData)
	if err := os.MkdirAll(pg, 0o700); err != nil {
		t.Fatal(err)
	}
	writeSeed(t, p, careGitSHA, nil)
	fake := host.NewFake()
	var out bytes.Buffer
	o := Options{Paths: p, Host: host.New(fake), Out: &out}
	if err := seedDB(context.Background(), o, seedTarget(careGitSHA)); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(filepath.Join(pg, "PG_VERSION")); err != nil || string(data) != "17\n" {
		t.Fatalf("PG_VERSION %q %v", data, err)
	}
	if fi, err := os.Stat(pg); err != nil || fi.Mode().Perm() != 0o700 {
		t.Fatalf("the data directory: %v %v", fi.Mode(), err)
	}
	if _, err := os.Stat(pg + ".seed"); !os.IsNotExist(err) {
		t.Fatal("the temporary directory stays")
	}
	pw := SeedPasswordCmd(pgImageDigest).String()
	if len(fake.Filter(pw)) != 1 {
		t.Fatalf("calls %q", fake.Calls)
	}
	for _, want := range []string{"--network none", "--user postgres", "--secret care-pi-postgres-password,type=env,target=SEED_PASSWORD", "postgres --single"} {
		if !strings.Contains(pw, want) {
			t.Fatalf("the password command lacks %q: %s", want, pw)
		}
	}
	if strings.Contains(pw, "ALTER USER postgres PASSWORD '") && !strings.Contains(pw, "$SEED_PASSWORD") {
		t.Fatal("the password must come from the secret, not from the command line")
	}
	rec, ok := readSeedRecord(p)
	if !ok || !rec.PasswordSet || rec.CareGitSHA != careGitSHA || rec.File != "pg-28211e29a359.tar.zst" {
		t.Fatalf("record %+v", rec)
	}
	if !strings.Contains(out.String(), "restore the pre-migrated database of CARE 28211e29a359") || strings.Contains(out.String(), "migrate applies the difference") {
		t.Fatalf("output:\n%s", out.String())
	}
	// Idempotent: the database exists, the password is set.
	fake.Calls = nil
	if err := seedDB(context.Background(), o, seedTarget(careGitSHA)); err != nil || len(fake.Calls) != 0 {
		t.Fatalf("%v %q", err, fake.Calls)
	}
}

// S72: a seed of another CARE commit is used (migrations go forward) and logged; no seed does nothing.
func TestSeedDBOtherCommitAndNoSeed(t *testing.T) {
	p := paths.New(t.TempDir())
	fake := host.NewFake()
	var out bytes.Buffer
	o := Options{Paths: p, Host: host.New(fake), Out: &out}
	if err := os.MkdirAll(p.Abs(paths.PGData), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := seedDB(context.Background(), o, seedTarget(careGitSHA)); err != nil || len(fake.Calls) != 0 {
		t.Fatalf("no seed: %v %q", err, fake.Calls)
	}
	writeSeed(t, p, strings.Repeat("b", 40), nil)
	if err := seedDB(context.Background(), o, seedTarget(careGitSHA)); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "the seed is of CARE bbbbbbbbbbbb, the lock has CARE 28211e29a359: migrate applies the difference") {
		t.Fatalf("output:\n%s", out.String())
	}
}

// S72: a failed password change right after the restore removes the seed (care-db initializes an empty database);
// a pending change of an earlier run never removes data.
func TestSeedDBPasswordFailure(t *testing.T) {
	p := paths.New(t.TempDir())
	pg := p.Abs(paths.PGData)
	if err := os.MkdirAll(pg, 0o700); err != nil {
		t.Fatal(err)
	}
	writeSeed(t, p, careGitSHA, nil)
	fake := host.NewFake()
	fake.On(SeedPasswordCmd(pgImageDigest).String(), host.Response{Code: 1, Out: "ERROR: role does not exist"})
	o := Options{Paths: p, Host: host.New(fake), Out: &bytes.Buffer{}}
	if err := seedDB(context.Background(), o, seedTarget(careGitSHA)); err != nil {
		t.Fatal(err)
	}
	if entries, _ := os.ReadDir(pg); len(entries) != 0 {
		t.Fatalf("the restored data stays: %v", entries)
	}
	if _, ok := readSeedRecord(p); ok {
		t.Fatal("the record stays")
	}
	// An earlier run restored the seed and stopped before the password change: keep the data, fail.
	if err := os.WriteFile(filepath.Join(pg, "PG_VERSION"), []byte("17\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := writeSeedRecord(p, SeedRecord{File: "pg-x.tar.zst"}); err != nil {
		t.Fatal(err)
	}
	if err := seedDB(context.Background(), o, seedTarget(careGitSHA)); err == nil || !strings.Contains(err.Error(), "password change failed") {
		t.Fatalf("%v", err)
	}
	if _, err := os.Stat(filepath.Join(pg, "PG_VERSION")); err != nil {
		t.Fatal("a pending password change must not remove data")
	}
}

// S72: an entry that leaves the data directory stops the restore.
func TestExtractSeedRefusesEscape(t *testing.T) {
	p := paths.New(t.TempDir())
	file := writeSeed(t, p, careGitSHA, map[string]string{"../evil": "x"})
	dir := p.Abs(paths.PGData)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := extractSeed(file, dir, false); err == nil || !strings.Contains(err.Error(), "leaves the directory") {
		t.Fatalf("%v", err)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(dir), "evil")); !os.IsNotExist(err) {
		t.Fatal("the entry was written")
	}
}

// S72: the apply restores the seed before it starts care.target.
func TestApplySeedsBeforeTheStart(t *testing.T) {
	e := newEnv(t, smallClinic)
	e.targetState("inactive")
	writeSeed(t, e.p, strings.Repeat("c", 40), nil)
	if _, err := e.run(false); err != nil {
		t.Fatalf("%v\n%s", err, e.out.String())
	}
	seed, start := -1, -1
	for i, c := range e.fake.Calls {
		switch {
		case strings.Contains(c, "--name care-seed-password") && seed < 0:
			seed = i
		case c == "systemctl start care.target" && start < 0:
			start = i
		}
	}
	if seed < 0 || start < 0 || seed > start {
		t.Fatalf("seed %d, start %d: %q", seed, start, e.fake.Calls)
	}
	if _, err := os.Stat(e.p.Abs(paths.PGData + "/PG_VERSION")); err != nil {
		t.Fatal(err)
	}
}

// S72: an image in the storage (the pre-pulled images of the Raspberry Pi image) is not pulled again.
func TestEnsureImagesSkipsPresentImages(t *testing.T) {
	e := newEnv(t, smallClinic)
	pr, err := e.prepareGit(gitSolution(t), 2_000_000, true)
	if err != nil {
		t.Fatal(err)
	}
	e.fake.On("podman image exists docker.io/library/redis@"+pr.Lock.Images["redis"].Digest, host.Response{Code: 1})
	pulled, _, err := EnsureImages(context.Background(), Options{Paths: e.p, Host: host.New(e.fake), Out: &e.out}, pr)
	if err != nil {
		t.Fatal(err)
	}
	if len(pulled) != 1 || !strings.HasPrefix(pulled[0], "docker.io/library/redis@") || len(e.fake.Filter("podman pull")) != 1 {
		t.Fatalf("pulled %v, calls %q", pulled, e.fake.Calls)
	}
	if n := strings.Count(e.out.String(), "image present, no pull:"); n < 3 || !strings.Contains(e.out.String(), "image present, no pull: ghcr.io/rithviknishad/care-pi/care-base@") {
		t.Fatalf("output:\n%s", e.out.String())
	}
}
