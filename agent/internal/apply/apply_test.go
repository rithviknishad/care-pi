package apply

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/rithviknishad/care-pi/agent/internal/applock"
	"github.com/rithviknishad/care-pi/agent/internal/exitcode"
	"github.com/rithviknishad/care-pi/agent/internal/fe"
	"github.com/rithviknishad/care-pi/agent/internal/host"
	"github.com/rithviknishad/care-pi/agent/internal/lock"
	"github.com/rithviknishad/care-pi/agent/internal/netinfo"
	"github.com/rithviknishad/care-pi/agent/internal/paths"
	"github.com/rithviknishad/care-pi/agent/internal/plan"
	"github.com/rithviknishad/care-pi/agent/internal/render"
	"github.com/rithviknishad/care-pi/agent/internal/s3admin"
	"github.com/rithviknishad/care-pi/agent/internal/secrets"
)

const solutionsDir = "../render/testdata/solutions"

var images = []string{
	"docker.io/library/redis:8-alpine",
	"docker.io/versity/versitygw:latest",
	"ghcr.io/ohcnetwork/care:latest",
	"ghcr.io/rithviknishad/care-pi/caddy:2",
	"ghcr.io/rithviknishad/care-pi/postgres-pgbackrest:17",
}

func digest(ref string) string { return fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(ref))) }

type env struct {
	t    *testing.T
	p    paths.Paths
	fake *host.Fake
	s3   *s3admin.Fake
	out  bytes.Buffer
	cfg  string
	// dbTimeout bounds the wait for care-db; 0 uses the default.
	dbTimeout time.Duration
	// ips replaces the two static LAN addresses; addressWait is Options.AddressWait.
	ips         netinfo.Source
	addressWait time.Duration
}

func newEnv(t *testing.T, config string) *env {
	t.Helper()
	root := t.TempDir()
	e := &env{t: t, p: paths.New(root), s3: &s3admin.Fake{}}
	e.cfg = e.p.Abs(paths.ConfigFile)
	if err := os.MkdirAll(filepath.Dir(e.cfg), 0o700); err != nil {
		t.Fatal(err)
	}
	e.writeConfig(config)
	if _, err := (secrets.Store{Paths: e.p}).EnsureAll(); err != nil {
		t.Fatal(err)
	}
	e.reset()
	return e
}

func (e *env) writeConfig(c string) {
	if err := os.WriteFile(e.cfg, []byte(c), 0o600); err != nil {
		e.t.Fatal(err)
	}
}

// reset gives a new Fake. active tells the state of care.target.
func (e *env) reset() {
	e.fake = host.NewFake()
	for _, ref := range images {
		e.fake.On("skopeo inspect --override-os linux --override-arch "+runtime.GOARCH+" --format {{.Digest}} docker://"+ref,
			host.Response{Out: digest(ref) + "\n"})
	}
	e.out.Reset()
}

func (e *env) targetState(s string) {
	e.fake.On("systemctl show -p ActiveState care.target", host.Response{Out: "ActiveState=" + s + "\n"})
}

func (e *env) secretsPresent() {
	var names []string
	for _, s := range secrets.PodmanSecrets() {
		names = append(names, s.Name)
	}
	e.fake.On("podman secret ls --format {{.Name}}", host.Response{Out: strings.Join(names, "\n") + "\n"})
}

// careFESHA is the sha that the fake git returns for care_fe develop.
const careFESHA = "573c5620573c5620573c5620573c5620573c5620"

func (e *env) run(dry bool) (*Result, error) {
	h := host.New(e.fake)
	h.Git = &host.FakeGit{Refs: map[string]map[string]string{
		"https://github.com/ohcnetwork/care_fe": {"refs/heads/develop": careFESHA},
	}}
	var ips netinfo.Source = netinfo.Static{"192.168.1.10", "10.0.0.5"}
	if e.ips != nil {
		ips = e.ips
	}
	return Run(context.Background(), Options{
		Paths:          e.p,
		ConfigPath:     e.cfg,
		SolutionsDir:   solutionsDir,
		DryRun:         dry,
		Reason:         "test",
		Out:            &e.out,
		Host:           h,
		IPs:            ips,
		S3:             e.s3,
		Now:            func() time.Time { return time.Date(2026, 10, 30, 10, 0, 0, 0, time.UTC) },
		PollInterval:   time.Millisecond,
		DBReadyTimeout: e.dbTimeout,
		AddressWait:    e.addressWait,
	})
}

// mutating returns the calls that change the host, in order.
func (e *env) mutating() []string {
	var out []string
	for _, c := range e.fake.Calls {
		switch {
		case strings.HasPrefix(c, "systemctl show"),
			strings.HasPrefix(c, "podman secret ls"),
			strings.HasPrefix(c, "skopeo inspect"),
			strings.HasPrefix(c, "podman image exists"),
			c == pgIsReady:
			continue
		}
		out = append(out, c)
	}
	return out
}

// smallClinicBase has no fe section. fe.provider defaults to local.
const smallClinicBase = `version: 1
identity: {display_name: Sunrise Clinic, hostname: care}
size: {preset: small-clinic}
`

// smallClinic sets fe.provider none: most tests check the stack steps, not the FE build.
const smallClinic = smallClinicBase + "fe: {provider: none}\n"

var unitList = strings.Join(render.Units, " ")

const (
	pgIsReady    = "podman exec --user postgres care-db pg_isready --quiet"
	stanzaCreate = "podman exec --user postgres care-db pgbackrest --stanza=main stanza-create"
)

func TestFirstApply(t *testing.T) {
	e := newEnv(t, smallClinic)
	e.targetState("inactive")
	if _, err := e.run(false); err != nil {
		t.Fatalf("%v\n%s", err, e.out.String())
	}
	var want []string
	for _, s := range secrets.PodmanSecrets() {
		want = append(want, "podman secret create --replace "+s.Name+" -")
	}
	want = append(want,
		"systemctl daemon-reload",
		"systemctl enable care-start.service",
		"systemctl reset-failed "+unitList,
		"systemctl start care.target",
		stanzaCreate,
	)
	if got := e.mutating(); !reflect.DeepEqual(got, want) {
		t.Fatalf("commands:\n got  %q\n want %q", got, want)
	}
	// The stanza step waits for the database first.
	if got := e.fake.Filter(pgIsReady, stanzaCreate); !reflect.DeepEqual(got, []string{pgIsReady, stanzaCreate}) {
		t.Fatalf("database wait and stanza: %q", got)
	}
	// Lock resolution prints one line for each image and git ref.
	for _, line := range []string{
		"resolve ghcr.io/ohcnetwork/care:latest … " + strings.TrimPrefix(digest("ghcr.io/ohcnetwork/care:latest"), "sha256:")[:12] + "\n",
		"resolve docker.io/library/redis:8-alpine … " + strings.TrimPrefix(digest("docker.io/library/redis:8-alpine"), "sha256:")[:12] + "\n",
	} {
		if !strings.Contains(e.out.String(), line) {
			t.Fatalf("output lacks %q:\n%s", line, e.out.String())
		}
	}
	// Secrets go through stdin, never through arguments.
	vals, _ := podmanSecretValues(e.p)
	for i, c := range e.fake.Calls {
		for _, v := range vals {
			if strings.Contains(c, v) {
				t.Fatalf("a secret value is in the command line %q", c)
			}
		}
		if strings.HasPrefix(c, "podman secret create") && e.fake.StdinLen[i] == 0 {
			t.Fatalf("%s: no stdin", c)
		}
	}
	for _, p := range []string{
		"/etc/containers/systemd/care-api.container",
		"/etc/systemd/system/care.target",
		"/etc/care-pi/rendered/caddy/Caddyfile",
		"/etc/care-pi/lock.yaml",
		"/var/lib/care-pi/state/ensure.json",
		"/var/lib/care-pi/pg",
	} {
		if _, err := os.Stat(e.p.Abs(p)); err != nil {
			t.Errorf("%s: %v", p, err)
		}
	}
	data, _ := os.ReadFile(e.p.Abs("/etc/containers/systemd/care-api.container"))
	if !strings.Contains(string(data), "Image=ghcr.io/ohcnetwork/care@"+digest("ghcr.io/ohcnetwork/care:latest")) {
		t.Fatalf("care-api does not use the locked digest:\n%s", data)
	}
	if len(e.s3.Calls) != 1 || e.s3.Calls[0].Region != "ap-south-1" || e.s3.Calls[0].PatientBucket != "patient-bucket" {
		t.Fatalf("bucket calls: %+v", e.s3.Calls)
	}
	jobs, _ := filepath.Glob(e.p.Abs(paths.JobsDir + "/*-apply.json"))
	if len(jobs) != 1 {
		t.Fatalf("want one journal file, got %v", jobs)
	}
	j, _ := os.ReadFile(jobs[0])
	if !strings.Contains(string(j), `"result": "ok"`) || !strings.Contains(string(j), `"reason": "test"`) {
		t.Fatalf("journal: %s", j)
	}

	// The second run finds nothing to do and uses no network.
	e.reset()
	e.targetState("active")
	e.secretsPresent()
	res, err := e.run(false)
	if err != nil {
		t.Fatal(err)
	}
	if res.Changed || !strings.Contains(e.out.String(), "no changes") {
		t.Fatalf("want no changes, got:\n%s", e.out.String())
	}
	if got := e.mutating(); len(got) != 0 {
		t.Fatalf("no-change run ran %q", got)
	}
	if got := e.fake.Filter("skopeo"); len(got) != 0 {
		t.Fatalf("no-change run resolved images again: %q", got)
	}
	if strings.Contains(e.out.String(), "resolve ") {
		t.Fatalf("no-change run printed a resolution:\n%s", e.out.String())
	}
}

// stanza-create needs the database, not the API. apply polls pg_isready until care-db
// accepts connections.
func TestStanzaWaitsForTheDatabase(t *testing.T) {
	e := applied(t)
	if err := os.Remove(e.p.Abs(paths.EnsureState)); err != nil {
		t.Fatal(err)
	}
	e.fake.On(pgIsReady, host.Response{Code: 2}, host.Response{Code: 1}, host.Response{})
	res, err := e.run(false)
	if err != nil {
		t.Fatalf("%v\n%s", err, e.out.String())
	}
	if got := kinds(res.Plan); !reflect.DeepEqual(got, []plan.Kind{plan.EnsureBucket, plan.EnsureStanza}) {
		t.Fatalf("plan %v", got)
	}
	want := []string{pgIsReady, pgIsReady, pgIsReady, stanzaCreate}
	if got := e.fake.Filter(pgIsReady, stanzaCreate); !reflect.DeepEqual(got, want) {
		t.Fatalf("commands:\n got  %q\n want %q", got, want)
	}
	if !strings.Contains(e.out.String(), "wait until care-db accepts connections") {
		t.Fatalf("output:\n%s", e.out.String())
	}

	// A database that never gets ready stops apply before stanza-create.
	e.reset()
	e.targetState("active")
	e.secretsPresent()
	e.dbTimeout = 20 * time.Millisecond
	_ = os.Remove(e.p.Abs(paths.EnsureState))
	e.fake.On(pgIsReady, host.Response{Code: 2})
	_, err = e.run(false)
	if err == nil || !strings.Contains(err.Error(), "care-db does not accept connections") {
		t.Fatalf("want a database timeout, got %v", err)
	}
	if got := e.fake.Filter(stanzaCreate); len(got) != 0 {
		t.Fatalf("stanza-create ran without a database: %q", got)
	}
}

func applied(t *testing.T) *env {
	t.Helper()
	e := newEnv(t, smallClinic)
	e.targetState("inactive")
	if _, err := e.run(false); err != nil {
		t.Fatalf("%v\n%s", err, e.out.String())
	}
	e.reset()
	e.targetState("active")
	e.secretsPresent()
	return e
}

func TestBackendEnvChangeRestartsCareUnitsOnly(t *testing.T) {
	e := applied(t)
	e.writeConfig(smallClinic + "env:\n  backend: {EMAIL_HOST: smtp.example.org}\n")
	if _, err := e.run(false); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"systemctl restart care-beat.service care-worker.service care-api.service",
		stanzaCreate,
	}
	if got := e.mutating(); !reflect.DeepEqual(got, want) {
		t.Fatalf("commands:\n got  %q\n want %q", got, want)
	}
}

func TestPresetChangeWithDatabaseIsFullCycle(t *testing.T) {
	e := applied(t)
	e.writeConfig(strings.Replace(smallClinic, "small-clinic", "busy-counter", 1))
	show := "systemctl show -p Id -p ActiveState " + unitList
	var deactivating, inactive strings.Builder
	for i, u := range render.Units {
		state := "inactive"
		if i == 2 {
			state = "deactivating"
		}
		fmt.Fprintf(&deactivating, "Id=%s\nActiveState=%s\n\n", u, state)
		fmt.Fprintf(&inactive, "Id=%s\nActiveState=inactive\n\n", u)
	}
	e.fake.On(show, host.Response{Out: deactivating.String()}, host.Response{Out: inactive.String()})
	if _, err := e.run(false); err != nil {
		t.Fatalf("%v\n%s", err, e.out.String())
	}
	want := []string{
		"systemctl daemon-reload",
		"systemctl stop care.target",
		"systemctl reset-failed " + unitList,
		"systemctl start care.target",
		stanzaCreate,
	}
	if got := e.mutating(); !reflect.DeepEqual(got, want) {
		t.Fatalf("commands:\n got  %q\n want %q", got, want)
	}
	if n := len(e.fake.Filter(show)); n != 2 {
		t.Fatalf("want 2 polls until inactive, got %d", n)
	}
}

func TestPresetChangeWithoutDatabaseRestartsUnits(t *testing.T) {
	e := applied(t)
	e.writeConfig(strings.Replace(smallClinic, "small-clinic", "solo", 1))
	if _, err := e.run(false); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"systemctl daemon-reload",
		"systemctl restart care-api.service",
		stanzaCreate,
	}
	if got := e.mutating(); !reflect.DeepEqual(got, want) {
		t.Fatalf("commands:\n got  %q\n want %q", got, want)
	}
}

func TestUploadLimitReloadsCaddy(t *testing.T) {
	e := applied(t)
	e.writeConfig(smallClinic + "uploads: {max_body_mb: 300}\n")
	if _, err := e.run(false); err != nil {
		t.Fatal(err)
	}
	want := []string{"podman exec care-caddy caddy reload --config /etc/caddy/Caddyfile --adapter caddyfile"}
	if got := e.mutating(); !reflect.DeepEqual(got, want) {
		t.Fatalf("commands:\n got  %q\n want %q", got, want)
	}
}

func TestDryRunWritesNothing(t *testing.T) {
	e := applied(t)
	before, _ := os.ReadFile(e.p.Abs(paths.RenderedCare + "/backend.env"))
	e.writeConfig(smallClinic + "env:\n  backend: {EMAIL_HOST: smtp.example.org}\n")
	if _, err := e.run(true); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(e.p.Abs(paths.RenderedCare + "/backend.env"))
	if !bytes.Equal(before, after) {
		t.Fatal("dry run changed backend.env")
	}
	if got := e.mutating(); len(got) != 0 {
		t.Fatalf("dry run ran %q", got)
	}
	out := e.out.String()
	if !strings.Contains(out, "+EMAIL_HOST=smtp.example.org") || !strings.Contains(out, "restart: care-beat.service") {
		t.Fatalf("dry run output lacks the diff or the plan:\n%s", out)
	}
}

func TestNotImplementedIsBadInput(t *testing.T) {
	for name, extra := range map[string]string{
		"public tls":   "tls:\n  public: {enabled: true, clinic_id: sunrise}\n",
		"rtc clock":    "clock: {policy: rtc}\n",
		"custom plugs": "solution:\n  track: custom\n  custom:\n    plugs:\n      - {id: care_filly, enabled: true}\n",
	} {
		t.Run(name, func(t *testing.T) {
			e := newEnv(t, smallClinic+extra)
			e.targetState("inactive")
			_, err := e.run(false)
			if exitcode.Code(err) != exitcode.BadInput {
				t.Fatalf("want exit code 2, got %d: %v", exitcode.Code(err), err)
			}
			if got := e.mutating(); len(got) != 0 {
				t.Fatalf("ran %q", got)
			}
		})
	}
}

func TestInvalidConfigIsBadInput(t *testing.T) {
	e := newEnv(t, "version: 1\nsize: {preset: huge}\n")
	_, err := e.run(false)
	if exitcode.Code(err) != exitcode.BadInput {
		t.Fatalf("want exit code 2, got %v", err)
	}
}

func TestMissingSecretsFail(t *testing.T) {
	e := newEnv(t, smallClinic)
	_ = os.Remove(e.p.Abs(paths.Secret(secrets.DjangoSecretKey)))
	e.targetState("inactive")
	_, err := e.run(false)
	if err == nil || !strings.Contains(err.Error(), "bootstrap") {
		t.Fatalf("want a hint to run bootstrap, got %v", err)
	}
}

// feBundle writes a care_fe bundle (tar.gz) and returns its path.
func feBundle(t *testing.T) string {
	t.Helper()
	var b bytes.Buffer
	gz := gzip.NewWriter(&b)
	tw := tar.NewWriter(gz)
	for name, body := range map[string]string{"index.html": "<html>care</html>", "assets/a.js": "1"} {
		_ = tw.WriteHeader(&tar.Header{Name: name, Typeflag: tar.TypeReg, Mode: 0o644, Size: int64(len(body))})
		_, _ = tw.Write([]byte(body))
	}
	_ = tw.Close()
	_ = gz.Close()
	file := filepath.Join(t.TempDir(), "care_fe.tar.gz")
	if err := os.WriteFile(file, b.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	return file
}

func TestFEArtifactBuildAndFlip(t *testing.T) {
	bundle := feBundle(t)
	feCfg := smallClinicBase + "fe: {provider: artifact, artifact_url: \"file://" + bundle + "\"}\n"
	e := newEnv(t, feCfg)
	e.targetState("inactive")
	if _, err := e.run(false); err != nil {
		t.Fatalf("%v\n%s", err, e.out.String())
	}
	lk, err := lock.Read(e.p.Abs(paths.LockFile))
	if err != nil {
		t.Fatal(err)
	}
	id := lk.FE.BuildID
	if id == "" || fe.Current(e.p) != id || lk.FE.URL != "file://"+bundle || len(lk.FE.SHA256) != 64 || lk.FE.CareFESHA != careFESHA {
		t.Fatalf("current %q, lock fe %+v", fe.Current(e.p), lk.FE)
	}
	if !strings.Contains(e.out.String(), "FE build "+id+" needed (provider artifact)") {
		t.Fatalf("output:\n%s", e.out.String())
	}
	// The buckets and the stanza come before the FE build (Phase 1 exit test, finding 7).
	out := e.out.String()
	if !strings.Contains(out, "resolve care_fe develop … "+careFESHA[:12]+"\n") {
		t.Fatalf("output lacks the care_fe resolution:\n%s", out)
	}
	bucket, stanza, build := strings.Index(out, "==> ensure-buckets"), strings.Index(out, "==> ensure-stanza"), strings.Index(out, "==> ensure-fe")
	if bucket < 0 || stanza < bucket || build < stanza {
		t.Fatalf("want ensure-buckets, ensure-stanza, ensure-fe in this order:\n%s", out)
	}

	// No change: no download, no lock write.
	e.reset()
	e.targetState("active")
	e.secretsPresent()
	if res, err := e.run(false); err != nil || res.Changed {
		t.Fatalf("want no changes: %v\n%s", err, e.out.String())
	}

	// A frontend env change gives a new build id: a new build and a flip, no restart.
	e.reset()
	e.targetState("active")
	e.secretsPresent()
	e.writeConfig(feCfg + "env:\n  frontend: {REACT_APP_TITLE: Sunrise}\n")
	if _, err := e.run(true); err != nil {
		t.Fatal(err)
	}
	if fe.Current(e.p) != id || !strings.Contains(e.out.String(), "needed (provider artifact)") {
		t.Fatalf("the dry run changed fe/current or did not report the build:\n%s", e.out.String())
	}
	e.reset()
	e.targetState("active")
	e.secretsPresent()
	res, err := e.run(false)
	if err != nil {
		t.Fatalf("%v\n%s", err, e.out.String())
	}
	if got := kinds(res.Plan); !reflect.DeepEqual(got, []plan.Kind{plan.EnsureFE, plan.WriteLock}) {
		t.Fatalf("plan %v", got)
	}
	if got := e.mutating(); len(got) != 0 {
		t.Fatalf("a frontend env change ran %q", got)
	}
	lk2, _ := lock.Read(e.p.Abs(paths.LockFile))
	if lk2.FE.BuildID == id || fe.Current(e.p) != lk2.FE.BuildID || !fe.Exists(e.p, id) {
		t.Fatalf("old %s, new %s, current %s", id, lk2.FE.BuildID, fe.Current(e.p))
	}

	// provider none leaves fe/current alone.
	e.reset()
	e.targetState("active")
	e.secretsPresent()
	e.writeConfig(smallClinic)
	if _, err := e.run(false); err != nil {
		t.Fatal(err)
	}
	if fe.Current(e.p) != lk2.FE.BuildID {
		t.Fatalf("provider none changed fe/current to %q", fe.Current(e.p))
	}
}

func TestFELocalDryRunReportsTheBuild(t *testing.T) {
	e := applied(t)
	// No fe section: fe.provider defaults to local (S8).
	e.writeConfig(smallClinicBase)
	e.fake.On("skopeo inspect --override-os linux --override-arch "+runtime.GOARCH+" --format {{.Digest}} docker://docker.io/library/node:22-bookworm-slim",
		host.Response{Out: digest("docker.io/library/node:22-bookworm-slim") + "\n"})
	if _, err := e.run(true); err != nil {
		t.Fatalf("%v\n%s", err, e.out.String())
	}
	out := e.out.String()
	if !strings.Contains(out, "needed (provider local)") || !strings.Contains(out, "care-fe-build.container") {
		t.Fatalf("output:\n%s", out)
	}
	if got := e.mutating(); len(got) != 0 {
		t.Fatalf("dry run ran %q", got)
	}
}

func kinds(p *plan.Plan) []plan.Kind {
	var out []plan.Kind
	for _, a := range p.Actions {
		out = append(out, a.Kind)
	}
	return out
}

func TestFEFailureDoesNotStopTheStackSteps(t *testing.T) {
	e := newEnv(t, smallClinicBase+"fe: {provider: artifact, artifact_url: \"file:///nonexistent/care_fe.tar.gz\"}\n")
	e.targetState("inactive")
	_, err := e.run(false)
	if err == nil || !strings.Contains(err.Error(), "ensure-fe") || exitcode.Code(err) != exitcode.Failed {
		t.Fatalf("want an ensure-fe error with exit code 1, got %v", err)
	}
	lk, _ := lock.Read(e.p.Abs(paths.LockFile))
	if lk == nil || lk.FE.BuildID == "" || fe.Current(e.p) != "" {
		t.Fatalf("want a lock with the build id and no current build: %+v", lk)
	}
	// The stack steps ran before the FE build; the lock write after it also ran (see above).
	if got := e.mutating(); got[len(got)-1] != stanzaCreate {
		t.Fatalf("the stanza step did not run: %q", got)
	}
	if out := e.out.String(); strings.Index(out, "==> ensure-stanza") > strings.Index(out, "==> ensure-fe") || !strings.Contains(out, "==> write-lock") {
		t.Fatalf("want ensure-stanza before ensure-fe, and write-lock after it:\n%s", out)
	}
	// The next apply tries the build again.
	e.reset()
	e.targetState("active")
	e.secretsPresent()
	res, _ := e.run(true)
	if !res.Plan.Has(plan.EnsureFE) {
		t.Fatalf("the next apply does not try the build again:\n%s", e.out.String())
	}
}

// A build that stopped before its restore (for example a power loss) leaves the drop-in.
// The next apply holds the lock, so no build runs: it removes the drop-in and restores the units.
func TestApplyRemovesLeftoverScaleDown(t *testing.T) {
	e := applied(t)
	dropIn := e.p.Abs(fe.ScaleDownDropIn)
	write := func() {
		if err := os.MkdirAll(filepath.Dir(dropIn), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(dropIn, []byte("[Container]\nEnvironment=GUNICORN_WORKERS=1\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write()
	if _, err := e.run(true); err != nil {
		t.Fatal(err)
	}
	if got := e.mutating(); len(got) != 0 || !fe.ScaleDownLeft(e.p) || !strings.Contains(e.out.String(), "apply removes it") {
		t.Fatalf("dry run ran %q:\n%s", got, e.out.String())
	}
	e.out.Reset()
	if _, err := e.run(false); err != nil {
		t.Fatalf("%v\n%s", err, e.out.String())
	}
	want := []string{
		"systemctl daemon-reload",
		"systemctl restart care-api.service",
		"systemctl start care-worker.service",
	}
	if got := e.mutating(); !reflect.DeepEqual(got, want) {
		t.Fatalf("commands:\n got  %q\n want %q", got, want)
	}
	if fe.ScaleDownLeft(e.p) || !strings.Contains(e.out.String(), "removed a leftover FE build drop-in") {
		t.Fatalf("drop-in left %v:\n%s", fe.ScaleDownLeft(e.p), e.out.String())
	}

	// When care.target is not active, apply only reloads; the start of the target follows.
	write()
	e.reset()
	e.targetState("inactive")
	e.secretsPresent()
	if _, err := e.run(false); err != nil {
		t.Fatalf("%v\n%s", err, e.out.String())
	}
	if got := e.mutating(); len(got) < 2 || got[0] != "systemctl daemon-reload" || strings.Contains(strings.Join(got, ","), "restart care-api") {
		t.Fatalf("commands: %q", got)
	}
}

// apply waits for the lock of another care-pi command.
func TestApplyWaitsForTheLock(t *testing.T) {
	e := applied(t)
	release, err := applock.Acquire(context.Background(), e.p, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	h := host.New(e.fake)
	_, err = Run(context.Background(), Options{
		Paths: e.p, ConfigPath: e.cfg, SolutionsDir: solutionsDir, Out: &e.out, Host: h,
		IPs: netinfo.Static{"192.168.1.10"}, S3: e.s3, LockTimeout: 200 * time.Millisecond,
	})
	if !errors.Is(err, applock.ErrTimeout) || len(e.fake.Calls) != 0 {
		t.Fatalf("want a lock timeout before any command, got %v, calls %q", err, e.fake.Calls)
	}
	release()
	if _, err := e.run(false); err != nil {
		t.Fatalf("after the release: %v\n%s", err, e.out.String())
	}
}

// An older care-pi enabled care.target, so multi-user.target waited for the whole stack.
// The new care.target has no [Install] section. apply disables it and enables care-start.service.
func TestLegacyTargetLinkIsDisabled(t *testing.T) {
	e := applied(t)
	link := e.p.Abs("/etc/systemd/system/multi-user.target.wants/care.target")
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/etc/systemd/system/care.target", link); err != nil {
		t.Fatal(err)
	}
	target := e.p.Abs("/etc/systemd/system/care.target")
	data, _ := os.ReadFile(target)
	if err := os.WriteFile(target, append(data, "\n[Install]\nWantedBy=multi-user.target\n"...), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := e.run(false); err != nil {
		t.Fatalf("%v\n%s", err, e.out.String())
	}
	want := []string{
		"systemctl daemon-reload",
		"systemctl disable care.target",
		"systemctl enable care-start.service",
	}
	if got := e.mutating(); !reflect.DeepEqual(got, want) {
		t.Fatalf("commands:\n got  %q\n want %q", got, want)
	}
	data, _ = os.ReadFile(target)
	if strings.Contains(string(data), "\n[Install]\n") {
		t.Fatalf("care.target has an [Install] section:\n%s", data)
	}
}

// lateAddress gives eth0 without an IPv4 address for the first n reads (only IPv6, as at the
// boot of lumine before the DHCP lease), then 192.168.1.10.
type lateAddress struct {
	n, reads int
}

func (l *lateAddress) Interfaces() ([]netinfo.Iface, error) {
	l.reads++
	eth := netinfo.Iface{Name: "eth0", Up: true, Addrs: []netip.Prefix{netip.MustParsePrefix("fd29::5/64")}}
	if l.reads > l.n {
		eth.Addrs = append(eth.Addrs, netip.MustParsePrefix("192.168.1.10/24"))
	}
	return []netinfo.Iface{
		{Name: "lo", Up: true, Loop: true, Addrs: []netip.Prefix{netip.MustParsePrefix("127.0.0.1/8")}},
		eth,
	}, nil
}

func TestApplyWaitsForTheAddress(t *testing.T) {
	e := newEnv(t, smallClinic)
	e.targetState("inactive")
	src := &lateAddress{n: 2}
	e.ips, e.addressWait = src, time.Minute
	res, err := e.run(false)
	if err != nil {
		t.Fatalf("%v\n%s", err, e.out.String())
	}
	out := e.out.String()
	if n := strings.Count(out, "wait for a network address"); n != 1 {
		t.Fatalf("the wait line shows %d times, want 1:\n%s", n, out)
	}
	if !strings.Contains(out, "wait for a network address (at most 60 s)\n") ||
		!strings.Contains(out, "network address 192.168.1.10 after ") || !strings.Contains(out, "addresses: 192.168.1.10\n") {
		t.Fatalf("output:\n%s", out)
	}
	if !reflect.DeepEqual(res.Addresses, []string{"192.168.1.10"}) || len(res.Warnings) != 0 {
		t.Fatalf("addresses %v, warnings %q", res.Addresses, res.Warnings)
	}
	pod, err := os.ReadFile(e.p.Abs("/etc/containers/systemd/care.pod"))
	if err != nil || !strings.Contains(string(pod), "PublishPort=192.168.1.10:443:443") {
		t.Fatalf("care.pod does not publish on the address: %v\n%s", err, pod)
	}
	// The wait comes before the render: 2 reads without the address, 1 with it, then Prepare.
	if src.reads != 4 {
		t.Fatalf("reads = %d, want 4", src.reads)
	}
}

func TestApplyWithoutAddressWarnsAndRenders(t *testing.T) {
	e := newEnv(t, smallClinic)
	e.targetState("inactive")
	e.ips, e.addressWait = &lateAddress{n: 1 << 30}, 200*time.Millisecond
	res, err := e.run(false)
	if err != nil {
		t.Fatalf("%v\n%s", err, e.out.String())
	}
	out := e.out.String()
	if strings.Count(out, "wait for a network address") != 1 || !strings.Contains(out, "wait for a network address (at most 0.2 s)") ||
		!strings.Contains(out, "warning: no LAN IPv4 address after 0.2 s") {
		t.Fatalf("output:\n%s", out)
	}
	if !strings.Contains(out, "addresses: (no LAN IPv4 address)") {
		t.Fatalf("apply did not render without the address:\n%s", out)
	}
	if len(res.Addresses) != 0 || len(res.Warnings) != 1 || !strings.Contains(res.Warnings[0], "no LAN IPv4 address") {
		t.Fatalf("addresses %v, warnings %q", res.Addresses, res.Warnings)
	}
}

func TestApplyWithAddressDoesNotWait(t *testing.T) {
	e := newEnv(t, smallClinic)
	e.targetState("inactive")
	e.addressWait = time.Minute
	if _, err := e.run(true); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(e.out.String(), "wait for a network address") {
		t.Fatalf("apply waited with an address:\n%s", e.out.String())
	}
}
