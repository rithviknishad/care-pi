package apply

import (
	"context"
	"encoding/pem"
	"os"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/rithviknishad/care-pi/agent/internal/ca"
	"github.com/rithviknishad/care-pi/agent/internal/dnsclient"
	"github.com/rithviknishad/care-pi/agent/internal/host"
	"github.com/rithviknishad/care-pi/agent/internal/netinfo"
	"github.com/rithviknishad/care-pi/agent/internal/paths"
	"github.com/rithviknishad/care-pi/agent/internal/plan"
	"github.com/rithviknishad/care-pi/agent/internal/render"
)

// TestFirstApplyMakesTheConstrainedRoot: a new host gets the root of the agent before Caddy starts (07 §4.1).
func TestFirstApplyMakesTheConstrainedRoot(t *testing.T) {
	e := newEnv(t, smallClinic)
	e.targetState("inactive")
	res, err := e.run(false)
	if err != nil {
		t.Fatalf("%v\n%s", err, e.out.String())
	}
	if res.Plan.Actions[1].Kind != plan.EnsureCA {
		t.Fatalf("ensure-ca must come right after ensure-dirs: %v", res.Plan.Actions)
	}
	r, err := ca.Load(e.p)
	if err != nil {
		t.Fatal(err)
	}
	c := ca.ConstraintsOf(r.Cert)
	if c == nil || !reflect.DeepEqual(c.IPs, []string{"10.0.0.0/24", "192.168.1.0/24"}) || c.DNS[0] != ".local" {
		t.Fatalf("constraints %+v", c)
	}
	if st := ca.ReadState(e.p); st.ChangedAt != nil {
		t.Fatal("a new host has no switch")
	}
	caddy, _ := os.ReadFile(e.p.Abs(paths.RenderedCaddy + "/Caddyfile"))
	if !strings.Contains(string(caddy), "cert /etc/care-pi-ca/root.crt") {
		t.Fatal("the Caddyfile does not name the root of the agent")
	}
	unit, _ := os.ReadFile(e.p.Abs(paths.QuadletDir + "/care-caddy.container"))
	if !strings.Contains(string(unit), "Volume=/var/lib/care-pi/ca:/etc/care-pi-ca:ro") {
		t.Fatal("care-caddy does not mount the root")
	}
	// A second apply: no change.
	e.reset()
	e.targetState("active")
	e.secretsPresent()
	res, err = e.run(false)
	if err != nil || res.Plan.Has(plan.EnsureCA) {
		t.Fatalf("second apply %v %v", res.Plan.Actions, err)
	}
}

// TestCAMigration: a host with the root of Caddy (Phase 1 to 5) switches to the root of the agent at its next apply,
// one time, and care-caddy restarts (a reload keeps the old intermediate).
func TestCAMigration(t *testing.T) {
	e := applied(t)
	// Make the host look like a host of Phase 5: no root of the agent, the root of Caddy in its data directory.
	if err := os.RemoveAll(e.p.Abs(paths.CADir)); err != nil {
		t.Fatal(err)
	}
	_ = os.Remove(e.p.Abs(paths.CAState))
	old, err := ca.Generate(ca.Desired(nil, []string{"192.168.1.10"}), ca.Options{CommonName: "Caddy Local Authority - 2026 ECC Root"})
	if err != nil {
		t.Fatal(err)
	}
	must := func(err error) {
		if err != nil {
			t.Fatal(err)
		}
	}
	must(os.MkdirAll(e.p.Abs(paths.CaddyCA), 0o700))
	must(os.WriteFile(e.p.Abs(paths.CaddyRootCA), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: old.DER}), 0o644))
	must(os.WriteFile(e.p.Abs(paths.CaddyCA+"/intermediate.crt"), []byte("x"), 0o600))
	res, err := e.run(false)
	if err != nil {
		t.Fatalf("%v\n%s", err, e.out.String())
	}
	if !res.Plan.Has(plan.EnsureCA) {
		t.Fatalf("plan %v", res.Plan.Actions)
	}
	if got := e.fake.Filter("systemctl restart"); !reflect.DeepEqual(got, []string{"systemctl restart care-caddy.service"}) {
		t.Fatalf("restarts %v", got)
	}
	if len(e.fake.Filter("podman exec care-caddy caddy reload")) != 0 {
		t.Fatal("a reload keeps the intermediate of the old root")
	}
	st := ca.ReadState(e.p)
	if st.ChangedAt == nil || st.PreviousFingerprint != old.Fingerprint() {
		t.Fatalf("state %+v", st)
	}
	if _, err := os.Stat(e.p.Abs(paths.CaddyCA + "/intermediate.crt")); !os.IsNotExist(err) {
		t.Fatal("the intermediate of the old root is still there")
	}
	if !strings.Contains(e.out.String(), "the root CA changed") {
		t.Fatalf("no warning:\n%s", e.out.String())
	}
}

// TestPublicModeApply: with the registration, apply renders the public site, the podman secret of the acmedns
// module, the DNS timer, and sends a new address with the plan action update-dns.
func TestPublicModeApply(t *testing.T) {
	e := applied(t)
	if err := dnsclient.WriteCredentials(e.p.Abs(paths.ACMEDNSFile), dnsclient.Credentials{Username: "u", Password: "s3cret-acme",
		Subdomain: "sunrise-k3x9", ServerURL: "https://care-pi.rithviknishad.dev"}); err != nil {
		t.Fatal(err)
	}
	if err := WritePublicState(e.p, PublicState{ClinicID: "sunrise-k3x9", Address: "192.168.1.10"}); err != nil {
		t.Fatal(err)
	}
	e.writeConfig(smallClinic + "tls:\n  local_ca: {enabled: true}\n  public: {enabled: true, clinic_id: sunrise-k3x9}\n")
	e.fake.Hook = func(c host.Cmd, stdin []byte) (host.Response, bool) {
		if r, ok := host.PlugConfigHook(c, stdin); ok {
			return r, ok
		}
		return host.Response{}, false
	}
	gitRefs["https://github.com/ohcnetwork/care_multihost_files_be.git"] = map[string]string{"refs/heads/main": strings.Repeat("cc", 20)}
	defer delete(gitRefs, "https://github.com/ohcnetwork/care_multihost_files_be.git")
	ref := "ghcr.io/rithviknishad/care-pi/care:care-clinic-latest"
	e.fake.On("skopeo inspect --override-os linux --override-arch "+runtime.GOARCH+" --format {{.Digest}} docker://"+ref, host.Response{Out: digest(ref) + "\n"})
	var sent []string
	e.dnsAddress = func(_ context.Context, ip string) error { sent = append(sent, ip); return nil }
	res, err := e.run(false)
	if err != nil {
		t.Fatalf("%v\n%s", err, e.out.String())
	}
	if res.Plan.Has(plan.UpdateDNS) {
		t.Fatal("the DNS service has the address: no update")
	}
	if !res.Plan.Has(plan.Cutover) {
		t.Fatalf("two names turn on the multihost plug, a backend plug: %v", res.Plan.Actions)
	}
	if got := e.fake.Filter("podman secret create --replace " + render.ACMEDNSSecret); len(got) != 1 {
		t.Fatalf("secret %v", got)
	}
	if a, _ := res.Plan.Get(plan.EnableTarget); !strings.Contains(strings.Join(a.Units, " "), render.DNSUpdateTimer) {
		t.Fatalf("enable %v", a.Units)
	}
	for _, c := range e.fake.Calls {
		if strings.Contains(c, "s3cret-acme") {
			t.Fatalf("the acme-dns password is on a command line: %s", c)
		}
	}
	// A new address: update-dns sends it, and the failure of the service is a warning.
	e.reset()
	e.targetState("active")
	e.secretsPresent()
	e.fake.On("podman secret ls --format {{.Name}}", host.Response{Out: strings.Join(secretNames(), "\n")})
	e.fake.On("skopeo inspect --override-os linux --override-arch "+runtime.GOARCH+" --format {{.Digest}} docker://"+ref, host.Response{Out: digest(ref) + "\n"})
	e.ips = netinfo.Static{"192.168.1.77"}
	if _, err := e.run(false); err != nil {
		t.Fatalf("%v\n%s", err, e.out.String())
	}
	if !reflect.DeepEqual(sent, []string{"192.168.1.77"}) {
		t.Fatalf("sent %v", sent)
	}
	// The registration is gone: apply refuses tls.public.
	_ = os.Remove(e.p.Abs(paths.ACMEDNSFile))
	if _, err := e.run(true); err == nil || !strings.Contains(err.Error(), "care-pi tls public enable") {
		t.Fatalf("want the enrol hint: %v", err)
	}
}

// m2: the update and plug jobs install a target with Activate. Without a root of the agent (a host that had no LAN
// address at its last apply), Activate must make the root before it writes the Caddyfile that names it; else a
// restart of care-caddy fails on a missing file. The running care-caddy restarts for the new root.
func TestActivateMakesTheRoot(t *testing.T) {
	e := applied(t)
	if err := os.RemoveAll(e.p.Abs(paths.CADir)); err != nil {
		t.Fatal(err)
	}
	_ = os.Remove(e.p.Abs(paths.CAState))
	h := host.New(e.fake)
	h.Git = &host.FakeGit{Refs: gitRefs}
	pr, err := Prepare(context.Background(), PrepareOptions{Paths: e.p, ConfigPath: e.cfg, SolutionsDir: solutionsDir, Host: h,
		IPs: netinfo.Static{"192.168.1.10", "10.0.0.5"}, Offline: true, Phase: "full"})
	if err != nil {
		t.Fatal(err)
	}
	if !pr.EnsureCA {
		t.Fatal("the target must need the root")
	}
	p, err := Activate(context.Background(), Options{Paths: e.p, ConfigPath: e.cfg, Host: h, Out: &e.out}, pr)
	if err != nil {
		t.Fatalf("%v\n%s", err, e.out.String())
	}
	if !p.Has(plan.EnsureCA) || !ca.Exists(e.p) {
		t.Fatalf("no root after Activate: %v", p.Actions)
	}
	caddy, _ := os.ReadFile(e.p.Abs(paths.RenderedCaddy + "/Caddyfile"))
	if !strings.Contains(string(caddy), "cert /etc/care-pi-ca/root.crt") {
		t.Fatal("the Caddyfile does not name the root")
	}
	if got := e.fake.Filter("systemctl try-restart", "systemctl restart", "systemctl start"); !reflect.DeepEqual(got, []string{"systemctl try-restart care-caddy.service"}) {
		t.Fatalf("units: %q", got)
	}
}
