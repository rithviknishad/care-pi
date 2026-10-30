package fe

import (
	"context"
	"errors"
	"os"
	"os/signal"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/rithviknishad/care-pi/agent/internal/config"
	"github.com/rithviknishad/care-pi/agent/internal/host"
	"github.com/rithviknishad/care-pi/agent/internal/paths"
)

// recorder wraps the Fake. It records the commands that change the host and the
// drop-in writes and removals, in order. A successful start of the builder creates the build.
type recorder struct {
	fake    *host.Fake
	p       paths.Paths
	buildID string
	fail    bool
	// hangup sends SIGHUP to the test process during the build, like an SSH disconnect.
	hangup bool

	mu     sync.Mutex
	events []string
	dropIn bool
	// pipeIgnored records signal.Ignored(SIGPIPE) during the build.
	pipeIgnored bool
}

// Write records the output of the provider as an event. It fails like a pipe without a reader.
func (r *recorder) Write(b []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, "print: "+strings.TrimSpace(string(b)))
	return 0, syscall.EPIPE
}

func (r *recorder) Run(ctx context.Context, c host.Cmd) ([]byte, error) {
	if r.hangup && c.String() == "systemctl start "+BuildUnit {
		r.mu.Lock()
		r.events = append(r.events, c.String())
		r.pipeIgnored = signal.Ignored(syscall.SIGPIPE)
		r.mu.Unlock()
		if err := syscall.Kill(os.Getpid(), syscall.SIGHUP); err != nil {
			return nil, err
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(10 * time.Second):
			return nil, errors.New("SIGHUP did not stop the build")
		}
	}
	r.mu.Lock()
	_, err := os.Stat(r.p.Abs(ScaleDownDropIn))
	if now := err == nil; now != r.dropIn {
		if now {
			r.events = append(r.events, "write drop-in")
		} else {
			r.events = append(r.events, "remove drop-in")
		}
		r.dropIn = now
	}
	line := c.String()
	switch {
	case strings.HasPrefix(line, "systemctl show"), strings.HasPrefix(line, "podman ps"), strings.HasPrefix(line, "podman image exists"):
	default:
		r.events = append(r.events, line)
	}
	if line == "systemctl start "+BuildUnit && !r.fail {
		dir := r.p.Abs(Dir(r.buildID))
		_ = os.MkdirAll(dir, 0o755)
		_ = os.WriteFile(filepath.Join(dir, "index.html"), []byte("<html>"), 0o644)
	}
	r.mu.Unlock()
	return r.fake.Run(ctx, c)
}

func localFor(t *testing.T, preset string) (*Local, *recorder) {
	t.Helper()
	p := paths.New(t.TempDir())
	cfg := config.Default()
	cfg.Size.Preset = preset
	cfg.FE.Provider = "local"
	const id = "0123456789abcdef"
	unit := "[Container]\nImage=" + testImage + "\nEnvironment=CARE_FE_REPO=ohcnetwork/care_fe CARE_FE_SHA=" + testSHA + " BUILD_ID=" + id + "\n"
	if err := os.MkdirAll(filepath.Dir(p.Abs(BuildUnitFile)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p.Abs(BuildUnitFile), []byte(unit), 0o644); err != nil {
		t.Fatal(err)
	}
	fake := host.NewFake()
	fake.On("systemctl show -p ActiveState care-api.service care-worker.service",
		host.Response{Out: "ActiveState=active\n\nActiveState=active\n"})
	fake.On("systemctl show -p ActiveState care-api.service", host.Response{Out: "ActiveState=activating\n"}, host.Response{Out: "ActiveState=active\n"})
	fake.On("podman ps --all --filter pod=care --format {{.Names}}\t{{.Status}}",
		host.Response{Out: "care-api\tUp 2 seconds (starting)\n"}, host.Response{Out: "care-api\tUp 40 seconds (healthy)\n"})
	r := &recorder{fake: fake, p: p, buildID: id}
	l := NewLocal(Env{Config: cfg, Paths: p, CareFESHA: testSHA, Host: host.New(r)})
	l.Poll = time.Millisecond
	t0 := time.Date(2026, 10, 30, 10, 0, 0, 0, time.UTC)
	n := 0
	l.Now = func() time.Time { n++; return t0.Add(time.Duration(n) * 30 * time.Second) }
	return l, r
}

func TestLocalSmallClinicScalesDownAndRestores(t *testing.T) {
	l, r := localFor(t, "small-clinic")
	b, err := l.Ensure(context.Background(), "0123456789abcdef")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"write drop-in",
		"systemctl daemon-reload",
		"systemctl restart care-api.service",
		"systemctl stop care-worker.service",
		"systemctl start care-fe-build.service",
		"remove drop-in",
		"systemctl daemon-reload",
		"systemctl restart care-api.service",
		"systemctl start care-worker.service",
	}
	if !reflect.DeepEqual(r.events, want) {
		t.Fatalf("commands:\n got  %q\n want %q", r.events, want)
	}
	if n := len(r.fake.Filter("podman ps")); n != 2 {
		t.Fatalf("want 2 health polls, got %d", n)
	}
	if b.Dir != "/var/lib/care-pi/fe/0123456789abcdef" || b.Record.BuiltAt == "" || b.Record.Duration == "" || b.Record.CareFESHA != testSHA {
		t.Fatalf("build %+v", b)
	}
	if _, err := os.Stat(l.env.Paths.Abs(ScaleDownDropIn)); err == nil {
		t.Fatal("the drop-in is still there")
	}
}

const testImage = "docker.io/library/node@sha256:0000000000000000000000000000000000000000000000000000000000000000"

func TestLocalPullsTheImageBeforeTheScaleDown(t *testing.T) {
	l, r := localFor(t, "solo")
	r.fake.On("podman image exists "+testImage, host.Response{Code: 1})
	if _, err := l.Ensure(context.Background(), "0123456789abcdef"); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"podman pull --quiet " + testImage,
		"systemctl stop care-worker.service",
		"systemctl start care-fe-build.service",
		"systemctl start care-worker.service",
	}
	if !reflect.DeepEqual(r.events, want) {
		t.Fatalf("commands:\n got  %q\n want %q", r.events, want)
	}
}

func TestLocalSoloStopsOnlyTheWorker(t *testing.T) {
	l, r := localFor(t, "solo")
	if _, err := l.Ensure(context.Background(), "0123456789abcdef"); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"systemctl stop care-worker.service",
		"systemctl start care-fe-build.service",
		"systemctl start care-worker.service",
	}
	if !reflect.DeepEqual(r.events, want) {
		t.Fatalf("commands:\n got  %q\n want %q", r.events, want)
	}
}

func TestLocalFailureRestores(t *testing.T) {
	l, r := localFor(t, "busy-counter")
	r.fail = true
	r.fake.On("systemctl start care-fe-build.service", host.Response{Code: 1})
	_, err := l.Ensure(context.Background(), "0123456789abcdef")
	if err == nil || !strings.Contains(err.Error(), "journalctl -u care-fe-build.service") {
		t.Fatalf("want a build error, got %v", err)
	}
	want := []string{
		"write drop-in",
		"systemctl daemon-reload",
		"systemctl restart care-api.service",
		"systemctl stop care-worker.service",
		"systemctl start care-fe-build.service",
		"systemctl stop care-fe-build.service",
		"remove drop-in",
		"systemctl daemon-reload",
		"systemctl restart care-api.service",
		"systemctl start care-worker.service",
	}
	if !reflect.DeepEqual(r.events, want) {
		t.Fatalf("commands:\n got  %q\n want %q", r.events, want)
	}
	if Exists(l.env.Paths, "0123456789abcdef") {
		t.Fatal("a failed build exists")
	}
}

func TestLocalRestoresWhenTheAPIIsNotHealthy(t *testing.T) {
	l, r := localFor(t, "small-clinic")
	l.HealthTimeout = 0
	r.fake.Script["podman ps --all --filter pod=care --format {{.Names}}\t{{.Status}}"] = []host.Response{{Out: "care-api\tUp (unhealthy)\n"}}
	_, err := l.Ensure(context.Background(), "0123456789abcdef")
	if err == nil || !strings.Contains(err.Error(), "healthy") {
		t.Fatalf("want a health error, got %v", err)
	}
	if got := r.events[len(r.events)-1]; got != "systemctl start care-worker.service" {
		t.Fatalf("no restore: %q", r.events)
	}
	for _, e := range r.events {
		if e == "systemctl start "+BuildUnit {
			t.Fatal("the build started with an unhealthy API")
		}
	}
}

func TestLocalAlreadyBuiltDoesNothing(t *testing.T) {
	l, r := localFor(t, "small-clinic")
	writeBuild(t, l.env.Paths, "0123456789abcdef")
	if _, err := l.Ensure(context.Background(), "0123456789abcdef"); err != nil {
		t.Fatal(err)
	}
	if len(r.fake.Calls) != 0 {
		t.Fatalf("ran %q", r.fake.Calls)
	}
}

func TestLocalStackStoppedNoScaleDown(t *testing.T) {
	l, r := localFor(t, "small-clinic")
	r.fake.Script["systemctl show -p ActiveState care-api.service care-worker.service"] = []host.Response{{Out: "ActiveState=inactive\n\nActiveState=inactive\n"}}
	if _, err := l.Ensure(context.Background(), "0123456789abcdef"); err != nil {
		t.Fatal(err)
	}
	if want := []string{"systemctl start care-fe-build.service"}; !reflect.DeepEqual(r.events, want) {
		t.Fatalf("commands: %q", r.events)
	}
}

func TestLocalNeedsTheUnitOfTheBuild(t *testing.T) {
	l, r := localFor(t, "small-clinic")
	_, err := l.Ensure(context.Background(), "fedcba9876543210")
	if err == nil || !strings.Contains(err.Error(), "run care-pi apply") {
		t.Fatalf("want a hint to run apply, got %v", err)
	}
	if len(r.fake.Calls) != 0 {
		t.Fatalf("ran %q", r.fake.Calls)
	}
}

func TestLocalTimeoutStopsTheUnitAndRestores(t *testing.T) {
	l, r := localFor(t, "solo")
	l.BuildTimeout = 0
	r.fail = true
	r.fake.On("systemctl start care-fe-build.service", host.Response{Code: 1})
	_, err := l.Ensure(context.Background(), "0123456789abcdef")
	if err == nil || !strings.Contains(err.Error(), "did not complete within") {
		t.Fatalf("want a timeout error, got %v", err)
	}
	want := []string{
		"systemctl stop care-worker.service",
		"systemctl start care-fe-build.service",
		"systemctl stop care-fe-build.service",
		"systemctl start care-worker.service",
	}
	if !reflect.DeepEqual(r.events, want) {
		t.Fatalf("commands:\n got  %q\n want %q", r.events, want)
	}
}

// An SSH disconnect sends SIGHUP. The build stops, and the restore runs. The provider ignores
// SIGPIPE, acts before it prints, and a print that fails does not stop the restore.
func TestLocalHangupRestores(t *testing.T) {
	l, r := localFor(t, "small-clinic")
	r.hangup = true
	l.env.Out = r
	_, err := l.Ensure(context.Background(), "0123456789abcdef")
	if err == nil || !strings.Contains(err.Error(), "care-pi stopped the FE build") {
		t.Fatalf("want a stop error, got %v", err)
	}
	var got []string
	for _, e := range r.events {
		if !strings.HasPrefix(e, "print: ") || strings.Contains(e, "restored") {
			got = append(got, e)
		}
	}
	want := []string{
		"write drop-in",
		"systemctl daemon-reload",
		"systemctl restart care-api.service",
		"systemctl stop care-worker.service",
		"systemctl start care-fe-build.service",
		"systemctl stop care-fe-build.service",
		"remove drop-in",
		"systemctl daemon-reload",
		"systemctl restart care-api.service",
		"systemctl start care-worker.service",
		"print: restored care-api and care-worker after the FE build",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("commands:\n got  %q\n want %q", got, want)
	}
	if !r.pipeIgnored {
		t.Fatal("SIGPIPE was not ignored during the build")
	}
	if ScaleDownLeft(l.env.Paths) {
		t.Fatal("the drop-in is still there")
	}
}

func TestRemoveScaleDown(t *testing.T) {
	for _, restart := range []bool{true, false} {
		p := paths.New(t.TempDir())
		l := NewLocal(Env{Paths: p})
		if err := l.writeDropIn(); err != nil {
			t.Fatal(err)
		}
		if !ScaleDownLeft(p) {
			t.Fatal("ScaleDownLeft is false after the write")
		}
		fake := host.NewFake()
		if err := RemoveScaleDown(context.Background(), p, host.New(fake), restart); err != nil {
			t.Fatal(err)
		}
		want := []string{"systemctl daemon-reload"}
		if restart {
			want = append(want, "systemctl restart care-api.service", "systemctl start care-worker.service")
		}
		if !reflect.DeepEqual(fake.Calls, want) || ScaleDownLeft(p) {
			t.Fatalf("restart %v: calls %q, drop-in left %v", restart, fake.Calls, ScaleDownLeft(p))
		}
	}
}
