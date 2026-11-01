package server

import (
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"io/fs"
	"math/big"
	"net/netip"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rithviknishad/care-pi/agent/internal/apply"
	"github.com/rithviknishad/care-pi/agent/internal/config"
	"github.com/rithviknishad/care-pi/agent/internal/dnsclient"
	"github.com/rithviknishad/care-pi/agent/internal/host"
	"github.com/rithviknishad/care-pi/agent/internal/jobs"
	"github.com/rithviknishad/care-pi/agent/internal/netinfo"
	"github.com/rithviknishad/care-pi/agent/internal/paths"
	"github.com/rithviknishad/care-pi/agent/internal/setup"
	"github.com/rithviknishad/care-pi/agent/internal/tailscale"
)

// readyConnEnv is a ready host (a Phase 1 lock) for the Phase 6 operations, with a fake DNS service and fake git.
func readyConnEnv(t *testing.T) (*env, *dnsclient.Fake) {
	t.Helper()
	e := newEnv(t)
	must(t, os.Remove(e.p.Abs(setup.File)))
	must(t, os.WriteFile(e.p.Abs(paths.LockFile), []byte("version: 1\ngenerated_at: \"2026-10-30T00:00:00Z\"\nagent_version: test\nsolution: {name: care-clinic, track: stable}\nimages:\n  postgres: {ref: localhost/care-pi/postgres-pgbackrest:17}\n"), 0o600))
	e.claim()
	if st := e.svc.SetupStateName(); st != setup.Ready {
		t.Fatal(st)
	}
	f := dnsclient.NewFake()
	t.Cleanup(f.Close)
	e.svc.DNSURL = f.URL()
	e.svc.DNSBackoff = time.Millisecond
	e.svc.CertWait = 50 * time.Millisecond
	e.svc.LoginWait = 2 * time.Second
	e.svc.Host.Git = &host.FakeGit{Refs: map[string]map[string]string{
		"https://github.com/ohcnetwork/care_fe":                     {"refs/heads/develop": strings.Repeat("57", 20)},
		"https://github.com/ohcnetwork/care_multihost_files_be.git": {"refs/heads/main": strings.Repeat("cc", 20)},
	}}
	ref := "ghcr.io/rithviknishad/care-pi/care:care-clinic-latest"
	e.fake.On("skopeo inspect --override-os linux --override-arch "+runtime.GOARCH+" --format {{.Digest}} docker://"+ref,
		host.Response{Out: fmt.Sprintf("sha256:%064x\n", len(ref))})
	e.fake.On("systemctl show --property ActiveState care.target", host.Response{Out: "ActiveState=active\n"})
	return e, f
}

// writeIssued writes a certificate of name as Caddy stores an ACME certificate.
func writeIssued(t *testing.T, p paths.Paths, name string) {
	t.Helper()
	writeACME(t, p, "acme-v02.api.letsencrypt.org-directory", name, time.Now().Add(90*24*time.Hour))
}

// writeACME writes a certificate of name below the issuer directory dir of Caddy, valid until notAfter.
func writeACME(t *testing.T, p paths.Paths, dir, name string, notAfter time.Time) {
	t.Helper()
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(7), Subject: pkix.Name{CommonName: name}, Issuer: pkix.Name{CommonName: "R11"},
		DNSNames: []string{name}, NotBefore: notAfter.Add(-90 * 24 * time.Hour), NotAfter: notAfter}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	must(t, err)
	d := p.Abs(paths.CaddyCertsDir + "/" + dir + "/" + name)
	must(t, os.MkdirAll(d, 0o700))
	must(t, os.WriteFile(filepath.Join(d, name+".crt"), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600))
}

// m5: an expired certificate of the public name does not count as issued. Before the fix, the timer saw the
// certificate, apply rendered no redirect, and the timer ran apply again each 5 minutes.
func TestPublicUpdateExpiredCertificate(t *testing.T) {
	e, f := readyConnEnv(t)
	writeRootCA(t, e.p)
	e.jobOK(e.do("PUT", "/api/v1/tls/public", map[string]any{"enabled": true, "hostname_hint": "sunrise"}))
	clinic, _ := f.Clinic("")
	host := clinic.ID + ".care-pi.rithviknishad.dev"
	writeACME(t, e.p, "acme-v02.api.letsencrypt.org-directory", host, e.clock.now().Add(-time.Hour))
	for i := 0; i < 2; i++ {
		res, err := e.svc.PublicUpdate(t.Context(), func(string) {})
		must(t, err)
		if res.Applied {
			t.Fatalf("run %d: an expired certificate made an apply", i)
		}
	}
	caddy, _ := os.ReadFile(e.p.Abs(paths.RenderedCaddy + "/Caddyfile"))
	if strings.Contains(string(caddy), "redir https://"+host) {
		t.Fatal("port 80 must not go to a name with an expired certificate")
	}
	r := e.do("GET", "/api/v1/tls", nil)
	if p := r.json(t)["public"].(map[string]any); p["primary"] != false || p["certificate"].(map[string]any)["state"] != "expired" {
		t.Fatalf("public %v", p)
	}
}

// TestPublicName: PUT /tls/public on and off with the fake DNS service (docs/api/README.md section 15.2).
func TestPublicName(t *testing.T) {
	e, f := readyConnEnv(t)
	writeRootCA(t, e.p)
	e.want(e.do("PUT", "/api/v1/tls/public", map[string]any{"enabled": true, "hostname_hint": "Bad Name"}, invalid), 422, "invalid-request")
	e.want(e.do("PUT", "/api/v1/tls/public", map[string]any{"enabled": false}), 409, "state-conflict")
	e.want(e.do("PUT", "/api/v1/tls/public", map[string]any{}, invalid), 422, "invalid-request")

	r := e.do("PUT", "/api/v1/tls/public", map[string]any{"enabled": true, "hostname_hint": "sunrise",
		"acme_ca": "https://acme-staging-v02.api.letsencrypt.org/directory"})
	id := e.jobOK(r)
	j, _ := jobs.Load(e.p, id)
	if !strings.Contains(strings.ReplaceAll(string(j.Result), " ", ""), `"certificate":"pending"`) {
		t.Fatalf("result %s", j.Result)
	}
	clinic, ok := f.Clinic("")
	if !ok {
		t.Fatal("no registration")
	}
	if clinic.IPv4 != "192.168.1.10" || !strings.HasPrefix(clinic.ID, "sunrise-") {
		t.Fatalf("the DNS service has %+v", clinic)
	}
	host := clinic.ID + ".care-pi.rithviknishad.dev"
	cfg, err := config.Load(e.p.Abs(paths.ConfigFile))
	must(t, err)
	if !cfg.TLS.Public.Enabled || cfg.TLS.Public.ClinicID != clinic.ID || cfg.TLS.Public.ACMECA == "" {
		t.Fatalf("config %+v", cfg.TLS.Public)
	}
	cred, err := os.ReadFile(e.p.Abs(paths.ACMEDNSFile))
	must(t, err)
	if st, _ := os.Stat(e.p.Abs(paths.ACMEDNSFile)); st.Mode().Perm() != 0o600 || !strings.Contains(string(cred), clinic.Password) {
		t.Fatalf("acme_dns.json %v", st.Mode())
	}
	caddy, err := os.ReadFile(e.p.Abs(paths.RenderedCaddy + "/Caddyfile"))
	must(t, err)
	for _, want := range []string{"https://" + host + " {", "dns acmedns /run/secrets/care-pi-acme-dns", "ca https://acme-staging-v02",
		`header Strict-Transport-Security "max-age=31536000"`, "pki {"} {
		if !strings.Contains(string(caddy), want) {
			t.Fatalf("Caddyfile lacks %q", want)
		}
	}
	if strings.Contains(string(caddy), "redir https://"+host) {
		t.Fatal("port 80 goes to the public name only after the certificate")
	}
	env, _ := os.ReadFile(e.p.Abs(paths.RenderedCare + "/backend.env"))
	if !strings.Contains(string(env), "BUCKET_EXTERNAL_ENDPOINT=https://"+host) {
		t.Fatal("the public name is the canonical host")
	}
	plugs, _ := os.ReadFile(e.p.Abs(paths.RenderedCare + "/additional-plugs.json"))
	if !strings.Contains(string(plugs), "care_multihost_files_be") {
		t.Fatal("two names turn on the multihost plug")
	}
	if len(e.fake.Filter("podman secret create --replace care-pi-acme-dns")) == 0 && !strings.Contains(strings.Join(e.fake.Calls, "\n"), "care-pi-acme-dns") {
		t.Fatalf("no podman secret care-pi-acme-dns: %v", e.fake.Filter("podman secret"))
	}
	for _, c := range e.fake.Calls {
		if strings.Contains(c, clinic.Password) {
			t.Fatalf("the acme-dns password is on a command line: %s", c)
		}
	}

	r = e.do("GET", "/api/v1/tls", nil)
	e.want(r, 200, "")
	pub := r.json(t)["public"].(map[string]any)
	if pub["hostname"] != host || pub["certificate"].(map[string]any)["state"] != "pending" || pub["primary"] != false ||
		pub["dns"].(map[string]any)["address"] != "192.168.1.10" {
		t.Fatalf("public %v", pub)
	}
	// The origin check accepts the console on the public name.
	e.want(e.do("GET", "/api/v1/tls", nil, header("Origin", "https://"+host+":8443")), 200, "")

	// The certificate comes: the timer sends port 80 to the public name.
	writeIssued(t, e.p, host)
	res, err := e.svc.PublicUpdate(t.Context(), func(string) {})
	must(t, err)
	if !res.Applied || res.Sent {
		t.Fatalf("update %+v", res)
	}
	caddy, _ = os.ReadFile(e.p.Abs(paths.RenderedCaddy + "/Caddyfile"))
	if !strings.Contains(string(caddy), "\theader Cache-Control \"no-store\"\n\tredir https://"+host+"{uri} 302\n") {
		t.Fatal("port 80 must send care.local to the public name with a 302 that the browser does not keep")
	}
	r = e.do("GET", "/api/v1/tls", nil)
	if p := r.json(t)["public"].(map[string]any); p["primary"] != true || p["certificate"].(map[string]any)["state"] != "issued" {
		t.Fatalf("public after the certificate %v", p)
	}
	// An address change: the plan action update-dns sends it.
	e.svc.IPs = netinfoStatic("192.168.1.77")
	r = e.do("POST", "/api/v1/apply", map[string]any{"reason": "address change"})
	e.jobOK(r)
	if c, _ := f.Clinic(clinic.ID); c.IPv4 != "192.168.1.77" {
		t.Fatalf("update-dns did not send the new address: %s", c.IPv4)
	}

	// Off: the registration goes, and the name never comes back.
	e.jobOK(e.do("PUT", "/api/v1/tls/public", map[string]any{"enabled": false}))
	if c, _ := f.Clinic(clinic.ID); !c.Revoked {
		t.Fatal("the registration is still there")
	}
	if _, err := os.Stat(e.p.Abs(paths.ACMEDNSFile)); err == nil {
		t.Fatal("acme_dns.json is still there")
	}
	caddy, _ = os.ReadFile(e.p.Abs(paths.RenderedCaddy + "/Caddyfile"))
	if strings.Contains(string(caddy), host) {
		t.Fatal("the public site is still in the Caddyfile")
	}
	if _, err := os.Stat(e.p.Abs(paths.SystemdDir + "/care-dns-update.timer")); err == nil {
		t.Fatal("the DNS timer is still there")
	}
}

// M2: a certificate of the staging directory is issued (the enrol job tells it), but browsers do not trust it: port 80
// stays on the same host, and the timer does not apply each 5 minutes.
func TestPublicUpdateStagingCertificate(t *testing.T) {
	e, f := readyConnEnv(t)
	writeRootCA(t, e.p)
	e.svc.CertWait = 10 * time.Second
	r := e.do("PUT", "/api/v1/tls/public", map[string]any{"enabled": true, "hostname_hint": "sunrise",
		"acme_ca": "https://acme-staging-v02.api.letsencrypt.org/directory"})
	// Caddy gets the staging certificate while the job waits for it.
	var clinic dnsclient.FakeClinic
	for ok := false; !ok; clinic, ok = f.Clinic("") {
		time.Sleep(5 * time.Millisecond)
	}
	host := clinic.ID + ".care-pi.rithviknishad.dev"
	writeACME(t, e.p, "acme-staging-v02.api.letsencrypt.org-directory", host, e.clock.now().Add(90*24*time.Hour))
	id := e.jobOK(r)
	if j, _ := jobs.Load(e.p, id); !strings.Contains(strings.ReplaceAll(string(j.Result), " ", ""), `"certificate":"issued"`) {
		t.Fatalf("the enrol job must tell that the staging certificate is issued: %s", j.Result)
	}
	res, err := e.svc.PublicUpdate(t.Context(), func(string) {})
	must(t, err)
	if res.Applied {
		t.Fatal("a staging certificate made an apply")
	}
	caddy, _ := os.ReadFile(e.p.Abs(paths.RenderedCaddy + "/Caddyfile"))
	if strings.Contains(string(caddy), "redir https://"+host) {
		t.Fatal("port 80 must not go to a name with a staging certificate")
	}
	r = e.do("GET", "/api/v1/tls", nil)
	p := r.json(t)["public"].(map[string]any)
	if c := p["certificate"].(map[string]any); p["primary"] != false || c["state"] != "issued" || c["staging"] != true {
		t.Fatalf("public %v", p)
	}
}

// m1: "care-pi tls public rotate --new-key" whose answer is lost. The agent stores the new key before the request
// (secrets/dns_client.key.pending), so it is not locked out: a later request signs with the pending key and promotes it.
func TestPublicRotateLostAnswer(t *testing.T) {
	e, f := readyConnEnv(t)
	writeRootCA(t, e.p)
	e.jobOK(e.do("PUT", "/api/v1/tls/public", map[string]any{"enabled": true, "hostname_hint": "sunrise"}))
	clinic, _ := f.Clinic("")
	// Each signature has a new timestamp: the retries of the test come within milliseconds (the replay cache).
	var mu sync.Mutex
	tick := e.clock.now()
	e.svc.Now = func() time.Time { mu.Lock(); defer mu.Unlock(); tick = tick.Add(time.Second); return tick }
	f.Now = func() time.Time { mu.Lock(); defer mu.Unlock(); return tick }
	keyFile, pending := e.p.Abs(paths.DNSClientKeyFile), e.p.Abs(paths.DNSClientPendingKeyFile)

	// Each try of the rotation arrives, and each answer is lost.
	f.SetLose(3)
	var out []string
	err := e.svc.PublicRotate(t.Context(), true, func(l string) { out = append(out, l) })
	if err == nil || !strings.Contains(strings.Join(out, "\n"), "dns_client.key.pending") {
		t.Fatalf("rotate: %v %q", err, out)
	}
	nk, err := dnsclient.ReadKey(pending)
	must(t, err)
	if c, _ := f.Clinic(clinic.ID); !c.PubKey.Equal(nk.Public()) {
		t.Fatal("the service must have the pending key")
	}
	// The timer: the old key gets 401; the pending key works and becomes the key.
	e.svc.IPs = netinfoStatic("192.168.1.78")
	if _, err := e.svc.PublicUpdate(t.Context(), func(string) {}); err != nil {
		t.Fatalf("update after the lost rotation: %v", err)
	}
	if k, err := dnsclient.ReadKey(keyFile); err != nil || !k.Equal(nk) {
		t.Fatalf("the pending key is not the key: %v", err)
	}
	if _, err := os.Stat(pending); !os.IsNotExist(err) {
		t.Fatal("the pending key file is still there")
	}
	if c, _ := f.Clinic(clinic.ID); c.IPv4 != "192.168.1.78" {
		t.Fatalf("the service has %s", c.IPv4)
	}

	// One lost answer: the rotation finishes in the same command, with the last acme-dns credentials.
	f.SetLose(1)
	out = nil
	must(t, e.svc.PublicRotate(t.Context(), true, func(l string) { out = append(out, l) }))
	c, _ := f.Clinic(clinic.ID)
	k, err := dnsclient.ReadKey(keyFile)
	must(t, err)
	cred, _ := os.ReadFile(e.p.Abs(paths.ACMEDNSFile))
	if !c.PubKey.Equal(k.Public()) || k.Equal(nk) || !strings.Contains(string(cred), c.Password) {
		t.Fatalf("after one lost answer: the key or the credentials differ from the service (%q)", out)
	}
	if _, err := os.Stat(pending); !os.IsNotExist(err) {
		t.Fatal("the pending key file is still there")
	}
}

// m1: the service refuses the key of the device (401). Enable registers again (a new id); disable forgets the
// registration.
func TestPublicNameKeyRefused(t *testing.T) {
	e, f := readyConnEnv(t)
	writeRootCA(t, e.p)
	e.jobOK(e.do("PUT", "/api/v1/tls/public", map[string]any{"enabled": true, "hostname_hint": "sunrise"}))
	first, _ := f.Clinic("")
	_, other, _ := ed25519.GenerateKey(rand.Reader)
	f.SetKey(first.ID, other.Public().(ed25519.PublicKey))
	// Enable again (for example after a failed apply): the id cannot be used, so the device registers again.
	e.jobOK(e.do("PUT", "/api/v1/tls/public", map[string]any{"enabled": true, "hostname_hint": "sunrise"}))
	st := apply.ReadPublicState(e.p)
	if st.ClinicID == "" || st.ClinicID == first.ID {
		t.Fatalf("enable with a refused key: id %q, want a new id", st.ClinicID)
	}
	f.SetKey(st.ClinicID, other.Public().(ed25519.PublicKey))
	e.jobOK(e.do("PUT", "/api/v1/tls/public", map[string]any{"enabled": false}))
	if _, err := os.Stat(e.p.Abs(paths.PublicState)); !os.IsNotExist(err) {
		t.Fatal("disable with a refused key must forget the registration")
	}
	if cfg, _ := config.Load(e.p.Abs(paths.ConfigFile)); cfg.TLS.Public.Enabled {
		t.Fatal("the public name is still on")
	}
}

// TestPublicNameServiceDown: the DNS service does not answer.
func TestPublicNameServiceDown(t *testing.T) {
	e, f := readyConnEnv(t)
	f.SetFail(100, 0)
	r := e.do("PUT", "/api/v1/tls/public", map[string]any{"enabled": true})
	e.want(r, 202, "")
	j := e.waitJob(r.json(t)["id"].(string))
	if j.State != jobs.StateFailed || j.Error == nil || !strings.HasSuffix(j.Error.Type(), "dns-service-unreachable") {
		t.Fatalf("job %s %+v", j.State, j.Error)
	}
	if cfg, _ := config.Load(e.p.Abs(paths.ConfigFile)); cfg.TLS.Public.Enabled {
		t.Fatal("a failed registration must not turn the mode on")
	}
}

// TestTailscale: PUT /tls/tailscale with the login URL flow and a fake tailscale (section 15.3).
func TestTailscale(t *testing.T) {
	e, _ := readyConnEnv(t)
	e.clock.add(6 * time.Minute)
	e.want(e.do("PUT", "/api/v1/tls/tailscale", map[string]any{"enabled": true, "auth_key": "nope"}), 403, "reauth-required")
	e.want(e.do("POST", "/api/v1/session/reauth", map[string]any{"password": password}), 200, "")
	e.want(e.do("PUT", "/api/v1/tls/tailscale", map[string]any{"enabled": true, "auth_key": "nope"}), 422, "invalid-request")
	e.want(e.do("PUT", "/api/v1/tls/tailscale", map[string]any{"enabled": false}), 409, "state-conflict")

	e.fake.On("tailscale version", host.Response{Code: 127}, host.Response{Out: "1.90.4\n"})
	e.fake.On("tailscale status --json",
		host.Response{Out: tailscale.StatusJSON(tailscale.StateNeedsLogin, ""), Code: 1},
		host.Response{Out: tailscale.StatusJSON(tailscale.StateNeedsLogin, "https://login.tailscale.com/a/abc"), Code: 1},
		host.Response{Out: tailscale.StatusJSON(tailscale.StateNeedsLogin, "https://login.tailscale.com/a/abc"), Code: 1},
		host.Response{Out: tailscale.StatusJSON(tailscale.StateRunning, "")})
	e.svc.IPs = netinfoTailscale("192.168.1.10", "100.101.102.103")
	r := e.do("PUT", "/api/v1/tls/tailscale", map[string]any{"enabled": true, "auth_key": nil})
	id := e.jobOK(r)
	if got := e.fake.Filter("systemd-run --wait --collect --pipe --quiet --unit=care-pi-tailscale-install"); len(got) != 1 {
		t.Fatalf("install %v", got)
	}
	if got := e.fake.Filter("systemd-run --collect --quiet --unit=care-pi-tailscale-up"); len(got) != 1 {
		t.Fatalf("login %v", got)
	}
	evs, _ := jobs.Events(e.p, id, 0, 1000)
	for _, ev := range evs {
		if strings.Contains(string(ev.Data), "login.tailscale.com") {
			t.Fatal("the login URL is in the job events")
		}
	}
	cfg, _ := config.Load(e.p.Abs(paths.ConfigFile))
	if !cfg.TLS.Tailscale.Enabled || cfg.TLS.Tailscale.Hostname != "care.tail1234.ts.net" {
		t.Fatalf("config %+v", cfg.TLS.Tailscale)
	}
	pod, _ := os.ReadFile(e.p.Abs(paths.QuadletDir + "/care.pod"))
	if !strings.Contains(string(pod), "PublishPort=100.101.102.103:443:443") {
		t.Fatalf("the pod does not publish on the Tailscale address:\n%s", pod)
	}
	if _, err := os.Stat(e.p.Abs(paths.FirewallDropIn)); err != nil {
		t.Fatal("no firewall drop-in for 41641/udp")
	}
	if len(e.fake.Filter("systemctl try-reload-or-restart care-pi-firewall.service")) == 0 {
		t.Fatal("the firewall did not reload")
	}
	r = e.do("GET", "/api/v1/tls", nil)
	ts := r.json(t)["tailscale"].(map[string]any)
	if ts["backend_state"] != "Running" || ts["https_enabled"] != true || ts["published"] != true || ts["hostname"] != "care.tail1234.ts.net" {
		t.Fatalf("tailscale %v", ts)
	}
	e.want(e.do("GET", "/api/v1/network", nil), 200, "")

	e.jobOK(e.do("PUT", "/api/v1/tls/tailscale", map[string]any{"enabled": false}))
	if got := e.fake.Filter("tailscale down", "tailscale logout"); len(got) != 2 {
		t.Fatalf("down and logout %v", got)
	}
	if _, err := os.Stat(e.p.Abs(paths.FirewallDropIn)); err == nil {
		t.Fatal("the firewall drop-in is still there")
	}
}

// TestTailscaleHTTPSOff: the tailnet gives no certificates.
func TestTailscaleHTTPSOff(t *testing.T) {
	e, _ := readyConnEnv(t)
	e.fake.On("tailscale version", host.Response{Out: "1.90.4\n"})
	st := strings.Replace(tailscale.StatusJSON(tailscale.StateRunning, ""), `"CertDomains":["care.tail1234.ts.net"]`, `"CertDomains":[]`, 1)
	e.fake.On("tailscale status --json", host.Response{Out: st})
	r := e.do("PUT", "/api/v1/tls/tailscale", map[string]any{"enabled": true, "auth_key": "tskey-auth-k123"})
	e.want(r, 202, "")
	j := e.waitJob(r.json(t)["id"].(string))
	if j.State != jobs.StateFailed || !strings.HasSuffix(j.Error.Type(), "tailscale-https-disabled") {
		t.Fatalf("job %s %+v", j.State, j.Error)
	}
	if len(e.fake.Filter("tailscale up")) != 0 {
		t.Fatal("a running tailscale needs no up")
	}
}

// TestWiFi: scan, set, and remove (section 15.4).
func TestWiFi(t *testing.T) {
	e, _ := readyConnEnv(t)
	e.fake.On("nmcli -t -f DEVICE,TYPE device status", host.Response{Out: "eth0:ethernet\nwlan0:wifi\n"})
	e.fake.On("nmcli -t -f IN-USE,SSID,SIGNAL,SECURITY,CHAN device wifi list --rescan yes", host.Response{Out: " :Sunrise-Staff:72:WPA2:6\n :Guest:30:WPA2:1\n"})
	r := e.do("GET", "/api/v1/network/wifi/scan", nil)
	e.want(r, 200, "")
	if nets := r.json(t)["networks"].([]any); len(nets) != 2 {
		t.Fatalf("scan %s", r.body)
	}
	e.want(e.do("PUT", "/api/v1/network/wifi", map[string]any{"ssid": "x", "psk": "short", "failover": true}, invalid), 422, "invalid-request")
	e.want(e.do("PUT", "/api/v1/network/wifi", map[string]any{"ssid": "x", "psk": "a wifi passphrase", "country": "in", "failover": true}, invalid), 422, "invalid-request")
	e.fake.On("sh -c command -v raspi-config", host.Response{Code: 1})
	e.fake.On("ip -json route show default", host.Response{Out: `[{"dev":"eth0","metric":100,"gateway":"192.168.1.1"},{"dev":"wlan0","metric":600,"gateway":"192.168.1.1"}]`})
	// The file of an older agent goes (m4).
	must(t, os.WriteFile(e.p.Abs(paths.WiFiSecretFile), []byte(`{"ssid":"Old","psk":"an old passphrase"}`), 0o600))
	r = e.do("PUT", "/api/v1/network/wifi", map[string]any{"ssid": "Sunrise-Staff", "psk": "a wifi passphrase", "country": "IN", "failover": true})
	e.jobOK(r)
	for _, c := range e.fake.Calls {
		if strings.Contains(c, "a wifi passphrase") {
			t.Fatalf("the PSK is on a command line: %s", c)
		}
	}
	cfgData, _ := os.ReadFile(e.p.Abs(paths.ConfigFile))
	if strings.Contains(string(cfgData), "passphrase") || !strings.Contains(string(cfgData), "Sunrise-Staff") {
		t.Fatalf("config.yaml:\n%s", cfgData)
	}
	// m4: the keyfile of NetworkManager is the only store of the passphrase. No file of care-pi has it, so no
	// backup has it (the backups copy /etc/care-pi).
	if _, err := os.Stat(e.p.Abs(paths.WiFiSecretFile)); !os.IsNotExist(err) {
		t.Fatalf("secrets/wifi.json: %v", err)
	}
	for _, dir := range []string{paths.EtcDir, paths.DataDir} {
		must(t, filepath.WalkDir(e.p.Abs(dir), func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			if data, _ := os.ReadFile(path); strings.Contains(string(data), "a wifi passphrase") {
				t.Errorf("%s has the passphrase", path)
			}
			return nil
		}))
	}
	r = e.do("GET", "/api/v1/network", nil)
	e.want(r, 200, "")
	w := r.json(t)["wifi"].(map[string]any)
	if w["ssid"] != "Sunrise-Staff" || w["failover"] != true || w["device"] != "wlan0" {
		t.Fatalf("wifi %v", w)
	}
	e.jobOK(e.do("DELETE", "/api/v1/network/wifi", nil))
	if len(e.fake.Filter("nmcli connection delete care-pi-wifi")) != 1 {
		t.Fatal("no delete")
	}

	e.fake.On("nmcli -t -f DEVICE,TYPE device status", host.Response{Out: "eth0:ethernet\n"})
	e.want(e.do("GET", "/api/v1/network/wifi/scan", nil), 409, "wifi-no-device")
}

type ifaceList []netinfo.Iface

func (l ifaceList) Interfaces() ([]netinfo.Iface, error) { return l, nil }

func netinfoStatic(ip string) netinfo.Source { return netinfo.Static{ip} }

// netinfoTailscale is eth0 with the LAN address and tailscale0 with the Tailscale address.
func netinfoTailscale(lan, ts string) netinfo.Source {
	return ifaceList{
		{Name: "eth0", Up: true, Addrs: []netip.Prefix{netip.MustParsePrefix(lan + "/24")}},
		{Name: "tailscale0", Up: true, Addrs: []netip.Prefix{netip.MustParsePrefix(ts + "/32")}},
	}
}
