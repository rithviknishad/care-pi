package plan

import (
	"reflect"
	"strings"
	"testing"

	"github.com/rithviknishad/care-pi/agent/internal/render"
)

const (
	pod      = "/etc/containers/systemd/care.pod"
	api      = "/etc/containers/systemd/care-api.container"
	worker   = "/etc/containers/systemd/care-worker.container"
	beat     = "/etc/containers/systemd/care-beat.container"
	db       = "/etc/containers/systemd/care-db.container"
	caddy    = "/etc/containers/systemd/care-caddy.container"
	febuild  = "/etc/containers/systemd/care-fe-build.container"
	target   = "/etc/systemd/system/care.target"
	clock    = "/etc/systemd/system/care-clock.service"
	start    = "/etc/systemd/system/care-start.service"
	caddyCfg = "/etc/care-pi/rendered/caddy/Caddyfile"
	status   = "/etc/care-pi/rendered/caddy/status/setup.html"
	backend  = "/etc/care-pi/rendered/care/backend.env"
	pgbr     = "/etc/care-pi/rendered/db/pgbackrest.conf"
	feEnv    = "/etc/care-pi/rendered/fe/frontend.env"
)

func base() map[string]string {
	return map[string]string{
		pod: "pod v1", api: "api v1", worker: "worker v1", beat: "beat v1", db: "db v1", caddy: "caddy v1",
		target: "target v1", clock: "clock v1", caddyCfg: "caddyfile v1", status: "setup v1",
		backend: "env v1", pgbr: "pgbr v1",
	}
}

func tree(m map[string]string) render.Tree {
	t := render.Tree{}
	for p, c := range m {
		t[p] = render.File{Path: p, Content: []byte(c)}
	}
	return t
}

func current(m map[string]string) map[string][]byte {
	out := map[string][]byte{}
	for p, c := range m {
		out[p] = []byte(c)
	}
	return out
}

func with(m map[string]string, kv ...string) map[string]string {
	out := map[string]string{}
	for k, v := range m {
		out[k] = v
	}
	for i := 0; i < len(kv); i += 2 {
		if kv[i+1] == "" {
			delete(out, kv[i])
			continue
		}
		out[kv[i]] = kv[i+1]
	}
	return out
}

func kinds(p *Plan) []Kind {
	var out []Kind
	for _, a := range p.Actions {
		out = append(out, a.Kind)
	}
	return out
}

var careSecretUnits = map[string][]string{
	"care-pi-django-secret-key":      {"care-beat", "care-worker", "care-api"},
	"care-pi-postgres-password":      {"care-db", "care-beat", "care-worker", "care-api"},
	"care-pi-pgbackrest-cipher-pass": {"care-db"},
}

func TestPlanTable(t *testing.T) {
	cases := []struct {
		name     string
		in       Input
		want     []Kind
		restart  []string
		reasonIn string
	}{
		{
			name: "first apply writes everything and starts the target",
			in: Input{
				Desired: tree(base()), Current: map[string][]byte{},
				SecretsChanged: []string{"care-pi-django-secret-key"}, SecretUnits: careSecretUnits,
				EnsureStale: true, LockChanged: true,
			},
			want: []Kind{EnsureDirs, SetSecrets, WriteFiles, DaemonReload, EnableTarget, PullImages, StartTarget, EnsureBucket, EnsureStanza, WriteLock},
		},
		{
			name: "first apply with an FE build: buckets and stanza before the FE build",
			in: Input{
				Desired: tree(base()), Current: map[string][]byte{},
				SecretsChanged: []string{"care-pi-django-secret-key"}, SecretUnits: careSecretUnits,
				EnsureStale: true, LockChanged: true, FEWanted: true,
			},
			want: []Kind{EnsureDirs, SetSecrets, WriteFiles, DaemonReload, EnableTarget, PullImages, StartTarget, EnsureBucket, EnsureStanza, EnsureFE, WriteLock},
		},
		{
			name: "full cycle with an FE build: stanza before the FE build",
			in: Input{
				Desired: tree(with(base(), db, "db v2")), Current: current(base()), TargetActive: true,
				FEWanted: true, LockChanged: true,
			},
			want: []Kind{EnsureDirs, WriteFiles, DaemonReload, FullCycle, EnsureBucket, EnsureStanza, EnsureFE, WriteLock},
		},
		{
			name: "nothing changed",
			in:   Input{Desired: tree(base()), Current: current(base()), TargetActive: true},
			want: nil,
		},
		{
			name:    "backend env var: restart beat, worker, api (api last); no daemon-reload",
			in:      Input{Desired: tree(with(base(), backend, "env v2")), Current: current(base()), TargetActive: true},
			want:    []Kind{EnsureDirs, WriteFiles, Restart, EnsureBucket, EnsureStanza},
			restart: []string{"care-beat.service", "care-worker.service", "care-api.service"},
		},
		{
			name: "Caddyfile or status page only: reload Caddy, no restart",
			in:   Input{Desired: tree(with(base(), caddyCfg, "caddyfile v2", status, "setup v2")), Current: current(base()), TargetActive: true},
			want: []Kind{EnsureDirs, WriteFiles, ReloadCaddy},
		},
		{
			name:    "sizing preset: rewrite units, daemon-reload, restart affected containers",
			in:      Input{Desired: tree(with(base(), api, "api v2", worker, "worker v2")), Current: current(base()), TargetActive: true},
			want:    []Kind{EnsureDirs, WriteFiles, DaemonReload, Restart, EnsureBucket, EnsureStanza},
			restart: []string{"care-worker.service", "care-api.service"},
		},
		{
			name:    "beat unit: restart beat and its dependents",
			in:      Input{Desired: tree(with(base(), beat, "beat v2")), Current: current(base()), TargetActive: true},
			want:    []Kind{EnsureDirs, WriteFiles, DaemonReload, Restart, EnsureBucket, EnsureStanza},
			restart: []string{"care-beat.service", "care-worker.service", "care-api.service"},
		},
		{
			name:     "database unit (shared_buffers): full cycle through the target",
			in:       Input{Desired: tree(with(base(), db, "db v2", api, "api v2")), Current: current(base()), TargetActive: true},
			want:     []Kind{EnsureDirs, WriteFiles, DaemonReload, FullCycle, EnsureBucket, EnsureStanza},
			reasonIn: "care-db unit changed",
		},
		{
			name: "hostname or address: pod, Caddyfile, backend env change; full cycle covers all",
			in: Input{
				Desired: tree(with(base(), pod, "pod v2", caddyCfg, "caddyfile v2", backend, "env v2")),
				Current: current(base()), TargetActive: true,
			},
			want:     []Kind{EnsureDirs, WriteFiles, DaemonReload, FullCycle, EnsureBucket, EnsureStanza},
			reasonIn: "pod changed",
		},
		{
			name: "image digest change: pull first, then a full cycle",
			in: Input{
				Desired: tree(with(base(), api, "api v2", worker, "worker v2", beat, "beat v2")),
				Current: current(base()), TargetActive: true, ImagesChanged: []string{"care"}, LockChanged: true,
				StackImages: []string{"ghcr.io/ohcnetwork/care@sha256:new"},
			},
			want:     []Kind{EnsureDirs, WriteFiles, DaemonReload, PullImages, FullCycle, EnsureBucket, EnsureStanza, WriteLock},
			reasonIn: "image changed: care",
		},
		{
			name: "builder image change only: no cycle",
			in: Input{
				Desired: tree(with(base(), febuild, "fe v2")), Current: current(with(base(), febuild, "fe v1")),
				TargetActive: true, ImagesChanged: []string{"node"}, LockChanged: true,
			},
			want: []Kind{EnsureDirs, WriteFiles, DaemonReload, WriteLock},
		},
		{
			name: "frontend env change (local provider): new build and flip, no restart",
			in: Input{
				Desired:      tree(with(base(), febuild, "fe v2", feEnv, "env v2")),
				Current:      current(with(base(), febuild, "fe v1", feEnv, "env v1")),
				TargetActive: true, FEWanted: true, LockChanged: true,
			},
			want: []Kind{EnsureDirs, WriteFiles, DaemonReload, EnsureFE, WriteLock},
		},
		{
			name: "frontend env change (artifact provider): download and flip only",
			in:   Input{Desired: tree(base()), Current: current(base()), TargetActive: true, FEWanted: true, LockChanged: true},
			want: []Kind{EnsureFE, WriteLock},
		},
		{
			name: "pgbackrest.conf (backup settings): no restart, check the stanza",
			in:   Input{Desired: tree(with(base(), pgbr, "pgbr v2")), Current: current(base()), TargetActive: true},
			want: []Kind{EnsureDirs, WriteFiles, EnsureStanza},
		},
		{
			name: "care.target or the clock gate: daemon-reload and enable, no restart",
			in:   Input{Desired: tree(with(base(), target, "target v2", clock, "clock v2")), Current: current(base()), TargetActive: true},
			want: []Kind{EnsureDirs, WriteFiles, DaemonReload, EnableTarget},
		},
		{
			name: "care-start.service is new (an update from an older care-pi): daemon-reload and enable it",
			in:   Input{Desired: tree(with(base(), start, "start v1")), Current: current(base()), TargetActive: true},
			want: []Kind{EnsureDirs, WriteFiles, DaemonReload, EnableTarget},
		},
		{
			name: "stale owned file: delete it and reload systemd",
			in:   Input{Desired: tree(base()), Current: current(with(base(), febuild, "fe v1")), TargetActive: true},
			want: []Kind{EnsureDirs, DeleteFiles, DaemonReload},
		},
		{
			name: "target stopped, no change: start it",
			in:   Input{Desired: tree(base()), Current: current(base()), TargetActive: false},
			want: []Kind{EnsureDirs, StartTarget, EnsureBucket, EnsureStanza},
		},
		{
			name: "target stopped with a change: start instead of restart",
			in:   Input{Desired: tree(with(base(), backend, "env v2")), Current: current(base()), TargetActive: false},
			want: []Kind{EnsureDirs, WriteFiles, StartTarget, EnsureBucket, EnsureStanza},
		},
		{
			name: "care secret changed: set it and restart the readers",
			in: Input{
				Desired: tree(base()), Current: current(base()), TargetActive: true,
				SecretsChanged: []string{"care-pi-django-secret-key"}, SecretUnits: careSecretUnits,
			},
			want:    []Kind{EnsureDirs, SetSecrets, Restart, EnsureBucket, EnsureStanza},
			restart: []string{"care-beat.service", "care-worker.service", "care-api.service"},
		},
		{
			name: "database secret changed: full cycle",
			in: Input{
				Desired: tree(base()), Current: current(base()), TargetActive: true,
				SecretsChanged: []string{"care-pi-pgbackrest-cipher-pass"}, SecretUnits: careSecretUnits,
			},
			want:     []Kind{EnsureDirs, SetSecrets, FullCycle, EnsureBucket, EnsureStanza},
			reasonIn: "secret care-pi-pgbackrest-cipher-pass changed",
		},
		{
			name: "buckets not ensured yet: ensure only",
			in:   Input{Desired: tree(base()), Current: current(base()), TargetActive: true, EnsureStale: true},
			want: []Kind{EnsureBucket, EnsureStanza},
		},
		{
			name:    "caddy unit changed with Caddyfile: restart caddy, no reload",
			in:      Input{Desired: tree(with(base(), caddy, "caddy v2", caddyCfg, "caddyfile v2")), Current: current(base()), TargetActive: true},
			want:    []Kind{EnsureDirs, WriteFiles, DaemonReload, Restart, EnsureBucket, EnsureStanza},
			restart: []string{"care-caddy.service"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := Build(c.in)
			if got := kinds(p); !reflect.DeepEqual(got, c.want) {
				t.Fatalf("actions:\n got  %v\n want %v", got, c.want)
			}
			if c.restart != nil {
				a, _ := p.Get(Restart)
				if !reflect.DeepEqual(a.Units, c.restart) {
					t.Fatalf("restart units: got %v, want %v", a.Units, c.restart)
				}
			}
			if c.reasonIn != "" {
				a, _ := p.Get(FullCycle)
				if !strings.Contains(a.Reason, c.reasonIn) {
					t.Fatalf("full cycle reason %q does not contain %q", a.Reason, c.reasonIn)
				}
			}
		})
	}
}

func TestDiffIgnoresUnownedFiles(t *testing.T) {
	cur := current(with(base(), "/etc/systemd/system/care-pi.service", "x"))
	if got := Diff(tree(base()), cur); len(got) != 0 {
		t.Fatalf("want no change, got %+v", got)
	}
}

func TestUnifiedDiff(t *testing.T) {
	// The expected output is the output of GNU "diff -u" for the same input.
	a := []byte("a\nb\nc\nd\ne\nf\ng\nh\ni\nj\nk\nl\n")
	b := []byte("a\nb\nc\nD\ne\nf\ng\nh\ni\nj\nk\nl\nm\n")
	got := UnifiedDiff("x", a, b)
	want := "--- x\n+++ x\n@@ -1,7 +1,7 @@\n a\n b\n c\n-d\n+D\n e\n f\n g\n@@ -10,3 +10,4 @@\n j\n k\n l\n+m\n"
	if got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
	added := UnifiedDiff("y", nil, []byte("one\n"))
	if !strings.HasPrefix(added, "--- /dev/null\n+++ y\n@@ -0,0 +1,1 @@\n+one\n") {
		t.Fatalf("unexpected diff for a new file:\n%s", added)
	}
}
