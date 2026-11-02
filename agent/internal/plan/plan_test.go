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
			name: "Phase 4: care image change on a running stack is a cutover, not a full cycle",
			in: Input{
				Desired: tree(with(base(), api, "api v2", worker, "worker v2", beat, "beat v2")),
				Current: current(base()), TargetActive: true, ImagesChanged: []string{"care"}, LockChanged: true,
				StackImages: []string{"ghcr.io/rithviknishad/care-pi/care@sha256:new"}, Cutover: true, PlugConfig: true,
			},
			want:     []Kind{EnsureDirs, WriteFiles, DaemonReload, PullImages, Cutover, EnsureBucket, EnsureStanza, PlugConfig, WriteLock},
			reasonIn: "image changed: care",
		},
		{
			name: "Phase 4: backend plugs change (env only) is a cutover",
			in: Input{
				Desired: tree(with(base(), backend, "env v2")), Current: current(base()), TargetActive: true,
				LockChanged: true, Cutover: true,
			},
			want:     []Kind{EnsureDirs, WriteFiles, Cutover, EnsureBucket, EnsureStanza, WriteLock},
			reasonIn: "backend plugs changed",
		},
		{
			// Phase 6 exit test: the public name on changed the plug set, the Caddyfile, and the unit of care-caddy (the
			// secret of acme-dns). The cutover kept the old Caddy, so Caddy never served the new name.
			name: "Phase 6: a cutover of the plugs with a new caddy unit restarts care-caddy after the cutover",
			in: Input{
				Desired: tree(with(base(), backend, "env v2", caddy, "caddy v2", caddyCfg, "caddyfile v2")),
				Current: current(base()), TargetActive: true, LockChanged: true, Cutover: true,
				SecretsChanged: []string{"care-pi-acme-dns"}, SecretUnits: map[string][]string{"care-pi-acme-dns": {"care-caddy"}},
			},
			want:     []Kind{EnsureDirs, SetSecrets, WriteFiles, DaemonReload, Cutover, Restart, EnsureBucket, EnsureStanza, WriteLock},
			restart:  []string{"care-caddy.service"},
			reasonIn: "backend plugs changed",
		},
		{
			name: "Phase 6: a cutover of the plugs with a new Caddyfile only reloads Caddy after the cutover",
			in: Input{
				Desired: tree(with(base(), backend, "env v2", caddyCfg, "caddyfile v2")),
				Current: current(base()), TargetActive: true, LockChanged: true, Cutover: true,
			},
			want: []Kind{EnsureDirs, WriteFiles, Cutover, ReloadCaddy, EnsureBucket, EnsureStanza, WriteLock},
		},
		{
			name: "a cutover with a full stop (pod changed) restarts each unit: no extra restart",
			in: Input{
				Desired: tree(with(base(), pod, "pod v2", caddy, "caddy v2", caddyCfg, "caddyfile v2")),
				Current: current(base()), TargetActive: true, LockChanged: true, Cutover: true,
			},
			want:     []Kind{EnsureDirs, WriteFiles, DaemonReload, Cutover, EnsureBucket, EnsureStanza, WriteLock},
			reasonIn: "pod changed",
		},
		{
			name: "Phase 4: an FE plug change writes only the plug_config rows",
			in:   Input{Desired: tree(base()), Current: current(base()), TargetActive: true, PlugConfig: true, LockChanged: true},
			want: []Kind{PlugConfig, WriteLock},
		},
		{
			name: "Phase 4: cutover is ignored when the target is not active",
			in: Input{
				Desired: tree(with(base(), api, "api v2")), Current: current(base()), TargetActive: false, Cutover: true,
			},
			want: []Kind{EnsureDirs, WriteFiles, DaemonReload, StartTarget, EnsureBucket, EnsureStanza},
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
				a, ok := p.Get(FullCycle)
				if !ok {
					a, _ = p.Get(Cutover)
				}
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

// Phase 6 (docs/connectivity.md).

const livePod = `# header
[Unit]
After=network-online.target
After=tailscaled.service

[Pod]
PodName=care
# LAN
PublishPort=192.168.1.10:443:443
PublishPort=10.0.0.5:443:443
# Tailscale
PublishPort=100.101.102.103:443:443
PublishPort=127.0.0.1:19000:9000

[Service]
ExecStartPre=-/usr/local/bin/care-pi network wait-address --address 100.101.102.103 --timeout 30s`

func TestPodShrinkOnly(t *testing.T) {
	failover := `# header
[Unit]
After=network-online.target
After=tailscaled.service

[Pod]
PodName=care
# LAN
PublishPort=10.0.0.5:443:443
# Tailscale
PublishPort=100.101.102.103:443:443
PublishPort=127.0.0.1:19000:9000

[Service]
ExecStartPre=-/usr/local/bin/care-pi network wait-address --address 100.101.102.103 --timeout 30s`
	noTailscale := `# header
[Unit]
After=network-online.target

[Pod]
PodName=care
# LAN
PublishPort=192.168.1.10:443:443
PublishPort=10.0.0.5:443:443
PublishPort=127.0.0.1:19000:9000`
	newAddress := strings.Replace(failover, "10.0.0.5", "10.0.0.6", 1)
	newKey := strings.Replace(failover, "PodName=care", "PodName=care\nNetwork=host", 1)
	// The addresses of the host after a failover (no 192.168.1.10), and with the Tailscale address gone too.
	afterFailover := []string{"127.0.0.1", "10.0.0.5", "100.101.102.103"}
	noTailnet := []string{"127.0.0.1", "192.168.1.10", "10.0.0.5"}
	all := []string{"127.0.0.1", "192.168.1.10", "10.0.0.5", "100.101.102.103"}
	cases := []struct {
		name    string
		desired string
		live    string
		active  bool
		hosts   []string
		want    bool
	}{
		{"eth0 address went away", failover, livePod, true, afterFailover, true},
		{"the address came back", livePod, livePod, true, all, true},
		{"tailscale address went away", noTailscale, livePod, true, noTailnet, true},
		{"no list of the host addresses", noTailscale, livePod, true, nil, true},
		// wave 8-ts on lumine: the Tailscale mode off with manage_daemon false. tailscaled keeps 100.x, and the
		// running pod kept 100.x:80/443/8443 (the owner's "tailscale serve" could not bind 443 again).
		{"tailscale mode off, the address stays", noTailscale, livePod, true, all, false},
		{"a dropped LAN address that is still there", failover, livePod, true, all, false},
		{"a new address", newAddress, livePod, true, all, false},
		{"a new key", newKey, livePod, true, all, false},
		{"no record of the running pod", failover, "", true, afterFailover, false},
		{"stack not running", failover, livePod, false, afterFailover, false},
	}
	for _, c := range cases {
		var live []byte
		if c.live != "" {
			live = []byte(c.live)
		}
		if got := podShrinkOnly([]byte(c.desired), live, c.active, c.hosts); got != c.want {
			t.Errorf("%s: %v, want %v", c.name, got, c.want)
		}
	}
	// In a plan: the pod file is written and systemd reloads it, but nothing restarts.
	p := Build(Input{Desired: tree(with(base(), pod, failover)), Current: current(with(base(), pod, livePod)), TargetActive: true,
		PodActive: true, PodLive: []byte(livePod), HostAddrs: afterFailover})
	if got := kinds(p); !reflect.DeepEqual(got, []Kind{EnsureDirs, WriteFiles, DaemonReload}) {
		t.Fatalf("failover plan %v", got)
	}
	// The Tailscale mode off while tailscaled keeps its address: a full cycle frees the ports of 100.x.
	p = Build(Input{Desired: tree(with(base(), pod, noTailscale)), Current: current(with(base(), pod, livePod)), TargetActive: true,
		PodActive: true, PodLive: []byte(livePod), HostAddrs: all})
	if !p.Has(FullCycle) {
		t.Fatalf("tailscale off with the address on the host needs a full cycle: %v", kinds(p))
	}
	p = Build(Input{Desired: tree(with(base(), pod, newAddress)), Current: current(with(base(), pod, livePod)), TargetActive: true,
		PodActive: true, PodLive: []byte(livePod)})
	if !p.Has(FullCycle) {
		t.Fatalf("a new address needs a full cycle: %v", kinds(p))
	}
}

// B1: care.target stays active when care-pod.service fails (a boot without an address of the pod). The plan must
// start the stack again, and a pod that failed has no publishes to keep.
func TestFailedPodStartsTheTarget(t *testing.T) {
	failedPod := []string{"care-pod.service", "care-caddy.service"}
	// No file change: reset the failed units and start care.target.
	p := Build(Input{Desired: tree(base()), Current: current(base()), TargetActive: true, FailedUnits: failedPod})
	if got, want := kinds(p), []Kind{EnsureDirs, StartTarget, EnsureBucket, EnsureStanza}; !reflect.DeepEqual(got, want) {
		t.Fatalf("plan %v, want %v", got, want)
	}
	if a, _ := p.Get(StartTarget); !strings.Contains(a.Reason, "care-pod.service") {
		t.Fatalf("reason %q", a.Reason)
	}
	// The pod file lost an address (podShrinkOnly), but the pod failed: a full cycle, not "keep the running pod".
	live := "[Pod]\nPodName=care\nPublishPort=192.168.1.10:443:443\nPublishPort=100.101.102.103:443:443"
	shrunk := "[Pod]\nPodName=care\nPublishPort=192.168.1.10:443:443"
	p = Build(Input{Desired: tree(with(base(), pod, shrunk)), Current: current(with(base(), pod, live)), TargetActive: true,
		PodActive: false, FailedUnits: failedPod, PodLive: []byte(live)})
	if !p.Has(FullCycle) {
		t.Fatalf("a failed pod with a new pod file needs a full cycle: %v", kinds(p))
	}
	// A failed unit and a changed unit: start the target, then restart the changed unit.
	p = Build(Input{Desired: tree(with(base(), api, "api v2")), Current: current(base()), TargetActive: true, PodActive: true,
		FailedUnits: []string{"care-worker.service"}})
	if got, want := kinds(p), []Kind{EnsureDirs, WriteFiles, DaemonReload, StartTarget, Restart, EnsureBucket, EnsureStanza}; !reflect.DeepEqual(got, want) {
		t.Fatalf("plan %v, want %v", got, want)
	}
	// Nothing failed and nothing changed: no action.
	if p := Build(Input{Desired: tree(base()), Current: current(base()), TargetActive: true, PodActive: true}); !p.Empty() {
		t.Fatalf("plan %v, want none", kinds(p))
	}
}

func TestEnsureCARestartsCaddy(t *testing.T) {
	p := Build(Input{Desired: tree(with(base(), caddyCfg, "caddyfile v2 pki")), Current: current(base()), TargetActive: true, EnsureCA: true})
	want := []Kind{EnsureDirs, EnsureCA, WriteFiles, Restart, EnsureBucket, EnsureStanza}
	if got := kinds(p); !reflect.DeepEqual(got, want) {
		t.Fatalf("plan %v, want %v", got, want)
	}
	if a, _ := p.Get(Restart); !reflect.DeepEqual(a.Units, []string{"care-caddy.service"}) {
		t.Fatalf("restart %v: a reload keeps the old intermediate", a.Units)
	}
	// The first apply of a new host starts the stack after ensure-ca.
	p = Build(Input{Desired: tree(base()), Current: map[string][]byte{}, EnsureCA: true})
	if got := kinds(p); got[1] != EnsureCA || !p.Has(StartTarget) {
		t.Fatalf("first apply %v", got)
	}
}

func TestFirewallAndDNSTimer(t *testing.T) {
	const dropIn = "/etc/systemd/system/care-pi-firewall.service.d/50-care-pi-tailscale.conf"
	const dnsTimer = "/etc/systemd/system/care-dns-update.timer"
	const dnsSvc = "/etc/systemd/system/care-dns-update.service"
	on := with(base(), dropIn, "drop-in", dnsTimer, "timer", dnsSvc, "service")
	p := Build(Input{Desired: tree(on), Current: current(base()), TargetActive: true, DNSStale: true})
	want := []Kind{EnsureDirs, WriteFiles, DaemonReload, ReloadFirewall, EnableTarget, UpdateDNS}
	if got := kinds(p); !reflect.DeepEqual(got, want) {
		t.Fatalf("on: %v, want %v", got, want)
	}
	if a, _ := p.Get(EnableTarget); !strings.Contains(strings.Join(a.Units, " "), "care-dns-update.timer") {
		t.Fatalf("enable %v", a.Units)
	}
	p = Build(Input{Desired: tree(base()), Current: current(on), TargetActive: true})
	want = []Kind{EnsureDirs, DisableUnits, DeleteFiles, DaemonReload, ReloadFirewall}
	if got := kinds(p); !reflect.DeepEqual(got, want) {
		t.Fatalf("off: %v, want %v", got, want)
	}
	if a, _ := p.Get(DisableUnits); !reflect.DeepEqual(a.Units, []string{"care-dns-update.timer"}) {
		t.Fatalf("disable %v", a.Units)
	}
}

// TestTelemetry: the units of the opt-in telemetry (Phase 7) start, restart, and stop alone; the stack stays.
func TestTelemetry(t *testing.T) {
	const (
		tImg  = "/etc/containers/systemd/care-telemetry.image"
		tCtr  = "/etc/containers/systemd/care-telemetry.container"
		nImg  = "/etc/containers/systemd/care-node-exporter.image"
		nCtr  = "/etc/containers/systemd/care-node-exporter.container"
		tConf = "/etc/care-pi/rendered/telemetry/scrape.yml"
	)
	on := with(base(), tImg, "img", tCtr, "ctr", nImg, "img", nCtr, "ctr", tConf, "conf")
	find := func(p *Plan) *Action {
		for i := range p.Actions {
			if p.Actions[i].Kind == EnsureTelemetry {
				return &p.Actions[i]
			}
		}
		return nil
	}
	p := Build(Input{Desired: tree(on), Current: current(base()), TargetActive: true, PodActive: true,
		ImagesChanged: []string{"vmagent", "node-exporter"}})
	a := find(p)
	want := []string{"care-node-exporter-image.service", "care-telemetry-image.service", "care-node-exporter.service", "care-telemetry.service"}
	if a == nil || !reflect.DeepEqual(a.Units, want) || len(a.Names) != 0 {
		t.Fatalf("on: %v", p.Actions)
	}
	for _, k := range kinds(p) {
		if k == FullCycle || k == Restart || k == Cutover || k == PullImages {
			t.Fatalf("the stack changed: %v", kinds(p))
		}
	}
	// A new scrape configuration restarts vmagent only.
	p = Build(Input{Desired: tree(with(on, tConf, "conf v2")), Current: current(on), TargetActive: true, PodActive: true})
	if a := find(p); a == nil || !reflect.DeepEqual(a.Units, []string{"care-telemetry.service"}) {
		t.Fatalf("config: %v", p.Actions)
	}
	// A new password restarts vmagent only.
	p = Build(Input{Desired: tree(on), Current: current(on), TargetActive: true, PodActive: true,
		SecretsChanged: []string{render.TelemetrySecret}, SecretUnits: map[string][]string{render.TelemetrySecret: {"care-telemetry"}}})
	if a := find(p); a == nil || !reflect.DeepEqual(a.Units, []string{"care-telemetry.service"}) {
		t.Fatalf("secret: %v", p.Actions)
	}
	// Off: both stop.
	p = Build(Input{Desired: tree(base()), Current: current(on), TargetActive: true, PodActive: true})
	if a := find(p); a == nil || len(a.Units) != 0 || !reflect.DeepEqual(a.Names, []string{"care-node-exporter.service", "care-telemetry.service"}) {
		t.Fatalf("off: %v", p.Actions)
	}
}
