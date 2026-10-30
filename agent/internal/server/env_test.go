package server

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"flag"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rithviknishad/care-pi/agent/internal/applock"
	"github.com/rithviknishad/care-pi/agent/internal/apply"
	"github.com/rithviknishad/care-pi/agent/internal/auth"
	"github.com/rithviknishad/care-pi/agent/internal/config"
	"github.com/rithviknishad/care-pi/agent/internal/host"
	"github.com/rithviknishad/care-pi/agent/internal/hostcheck"
	"github.com/rithviknishad/care-pi/agent/internal/jobs"
	"github.com/rithviknishad/care-pi/agent/internal/netinfo"
	"github.com/rithviknishad/care-pi/agent/internal/paths"
	"github.com/rithviknishad/care-pi/agent/internal/s3admin"
	"github.com/rithviknishad/care-pi/agent/internal/secrets"
	"github.com/rithviknishad/care-pi/agent/internal/server/contracttest"
	"github.com/rithviknishad/care-pi/agent/internal/service"
	"github.com/rithviknishad/care-pi/agent/internal/setup"
)

var coverage = contracttest.NewCoverage()

const (
	origin   = "https://care.local:8443"
	password = "a long console passphrase"
)

var testImages = []string{
	"docker.io/library/node:22-bookworm-slim",
	"docker.io/library/redis:8-alpine",
	"docker.io/versity/versitygw:latest",
	"ghcr.io/ohcnetwork/care:latest",
	"ghcr.io/rithviknishad/care-pi/caddy:2",
	"ghcr.io/rithviknishad/care-pi/postgres-pgbackrest:17",
}

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *clock) add(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

type env struct {
	t     *testing.T
	p     paths.Paths
	fake  *host.Fake
	svc   *service.Service
	srv   *Server
	hs    *httptest.Server
	clock *clock
	// cookie and csrf are the session of the client.
	cookie string
	csrf   string
	addr   string
}

// newEnv makes a setup-mode host: config.yaml, secrets, the setup record, no lock.
func newEnv(t *testing.T) *env {
	t.Helper()
	root := t.TempDir()
	p := paths.New(root)
	e := &env{t: t, p: p, fake: host.NewFake(), clock: &clock{t: time.Now().UTC().Truncate(time.Second)}, addr: "192.168.1.23"}
	cfg, err := config.DefaultYAML("Sunrise Clinic", "care", "small-clinic", "none")
	must(t, err)
	must(t, os.MkdirAll(p.Abs(paths.EtcDir), 0o700))
	must(t, os.WriteFile(p.Abs(paths.ConfigFile), cfg, 0o600))
	_, err = secrets.Store{Paths: p}.EnsureAll()
	must(t, err)
	_, err = (&setup.Store{Paths: p}).Update(func(r *setup.Record) error { r.ApplyPhase = setup.PhaseBootstrap; return nil })
	must(t, err)
	for _, ref := range testImages {
		e.fake.On("skopeo inspect --override-os linux --override-arch "+runtime.GOARCH+" --format {{.Digest}} docker://"+ref,
			host.Response{Out: fmt.Sprintf("sha256:%064x\n", len(ref))})
	}
	e.fake.On("podman ps --all --filter pod=care --format {{.Names}}\t{{.Status}}",
		host.Response{Out: "care-caddy\tUp 1 minute\ncare-api\tUp 5 minutes (healthy)\n"})
	e.fake.On("timedatectl show --property NTPSynchronized --value", host.Response{Out: "yes\n"})
	h := host.New(e.fake)
	am := auth.New(auth.Options{Paths: p, Now: e.clock.now, Params: auth.Argon2Params{MemoryKiB: 64, Time: 1, Threads: 1}})
	jm := &jobs.Manager{Paths: p, Now: e.clock.now, LockWait: 200 * time.Millisecond, CancelPoll: 20 * time.Millisecond}
	e.svc = &service.Service{Paths: p, SolutionsDir: "../render/testdata/solutions", Host: h,
		IPs: netinfo.Static{"192.168.1.10"}, S3: &s3admin.Fake{}, Auth: am, Setup: &setup.Store{Paths: p}, Jobs: jm,
		Now: e.clock.now, StartedAt: e.clock.now(), HealthTimeout: time.Second, PollInterval: 5 * time.Millisecond,
		HostCheck: func(context.Context) *hostcheck.Report {
			return &hostcheck.Report{Checks: []hostcheck.Check{{Name: "podman", Level: hostcheck.Required, OK: true, Detail: "podman 5.4.2"}}}
		},
		ApplyHook: func(o *apply.Options) {
			o.PollInterval = time.Millisecond
			o.DBReadyTimeout = time.Second
		},
		AfterPower: func(time.Duration, func()) {},
	}
	e.srv = New(Options{Service: e.svc, Heartbeat: 50 * time.Millisecond, Poll: 10 * time.Millisecond})
	e.hs = httptest.NewServer(e.srv)
	t.Cleanup(func() {
		e.srv.Close()
		e.hs.Close()
	})
	return e
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

type resp struct {
	status int
	header http.Header
	body   []byte
	op     string
}

func (r *resp) json(t *testing.T) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(r.body, &m); err != nil {
		t.Fatalf("not JSON (%d): %s", r.status, r.body)
	}
	return m
}

func (r *resp) slug() string {
	var p struct {
		Type string `json:"type"`
	}
	_ = json.Unmarshal(r.body, &p)
	return strings.TrimPrefix(p.Type, "https://care-pi.rithviknishad.dev/problems/")
}

type reqOpt func(*http.Request, *contracttest.Exchange)

func header(k, v string) reqOpt {
	return func(r *http.Request, _ *contracttest.Exchange) { r.Header.Set(k, v) }
}

func noHeader(k string) reqOpt {
	return func(r *http.Request, _ *contracttest.Exchange) { r.Header.Del(k) }
}

func invalid(_ *http.Request, x *contracttest.Exchange) { x.InvalidRequest = true }

func rawBody(b []byte) reqOpt {
	return func(r *http.Request, x *contracttest.Exchange) {
		r.Body = io.NopCloser(bytes.NewReader(b))
		r.ContentLength = int64(len(b))
		x.Body = b
	}
}

// do sends a request through the test server and checks it against the contract.
func (e *env) do(method, url string, body any, opts ...reqOpt) *resp {
	e.t.Helper()
	var data []byte
	if body != nil {
		var err error
		data, err = json.Marshal(body)
		must(e.t, err)
	}
	req, err := http.NewRequest(method, e.hs.URL+url, bytes.NewReader(data))
	must(e.t, err)
	req.Host = "care.local:8443"
	req.Header.Set("X-Forwarded-For", e.addr)
	req.Header.Set("User-Agent", "contract-test")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if unsafeMethod(method) {
		req.Header.Set("Origin", origin)
		if e.csrf != "" {
			req.Header.Set("X-CSRF-Token", e.csrf)
		}
	}
	if e.cookie != "" {
		req.AddCookie(&http.Cookie{Name: CookieName, Value: e.cookie})
	}
	x := contracttest.Exchange{Method: method, URL: url, Body: data}
	for _, o := range opts {
		o(req, &x)
	}
	x.Header = req.Header.Clone()
	res, err := http.DefaultClient.Do(req)
	must(e.t, err)
	defer func() { _ = res.Body.Close() }()
	rb, err := io.ReadAll(res.Body)
	must(e.t, err)
	x.Status, x.RHeader, x.RBody = res.StatusCode, res.Header, rb
	r := &resp{status: res.StatusCode, header: res.Header, body: rb}
	if strings.HasPrefix(url, "/api/") || strings.HasPrefix(url, "/_care-pi/") {
		r.op = contracttest.Check(e.t, coverage, x)
	}
	for _, c := range res.Cookies() {
		if c.Name == CookieName {
			e.cookie = c.Value
		}
	}
	return r
}

func (e *env) want(r *resp, status int, slug string) {
	e.t.Helper()
	if r.status != status || (slug != "" && r.slug() != slug) {
		e.t.Fatalf("%s: status %d (%s), want %d %s\n%s", r.op, r.status, r.slug(), status, slug, r.body)
	}
}

// claim claims the device and keeps the session.
func (e *env) claim() []string {
	e.t.Helper()
	code, _, err := e.svc.Auth.EnsureClaimCode()
	must(e.t, err)
	r := e.do("POST", "/api/v1/session/claim", map[string]any{"method": "claim-code", "secret": code, "new_password": password})
	e.want(r, 201, "")
	m := r.json(e.t)
	e.csrf = m["session"].(map[string]any)["csrf_token"].(string)
	var codes []string
	for _, c := range m["recovery_codes"].([]any) {
		codes = append(codes, c.(string))
	}
	return codes
}

// waitJob waits until the job ends and returns it.
func (e *env) waitJob(id string) *jobs.Job {
	e.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	must(e.t, e.svc.Jobs.Wait(ctx, id))
	j, err := jobs.Load(e.p, id)
	must(e.t, err)
	return j
}

// writeRootCA writes a test root CA where Caddy keeps its local root.
func writeRootCA(t *testing.T, p paths.Paths) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	must(t, err)
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Caddy Local Authority - 2026 ECC Root"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(10 * 365 * 24 * time.Hour), IsCA: true,
		BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	must(t, err)
	path := p.Abs(paths.CaddyRootCA)
	must(t, os.MkdirAll(filepath.Dir(path), 0o700))
	must(t, os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600))
}

func TestMain(m *testing.M) {
	applock.NoticeAfter = time.Hour
	code := m.Run()
	if code == 0 {
		code = checkCoverage()
	}
	os.Exit(code)
}

// checkCoverage makes sure that the tests checked each operation of the contract at least once.
// It does nothing for a run with -run (a subset of the tests). With -v it prints the statuses
// that the tests checked for each operation.
func checkCoverage() int {
	if f := flag.Lookup("test.run"); f != nil && f.Value.String() != "" {
		return 0
	}
	ops, err := contracttest.Operations()
	if err != nil {
		fmt.Fprintln(os.Stderr, "contract coverage:", err)
		return 1
	}
	names := make([]string, 0, len(ops))
	for op := range ops {
		names = append(names, op)
	}
	sort.Strings(names)
	missing := 0
	for _, op := range names {
		st := coverage.Statuses(op)
		if len(st) == 0 {
			fmt.Fprintf(os.Stderr, "contract coverage: no test checked the operation %s\n", op)
			missing++
		}
		if testing.Verbose() {
			fmt.Printf("contract coverage: %-28s phase %d  %v\n", op, ops[op], st)
		}
	}
	if missing > 0 {
		return 1
	}
	return 0
}
