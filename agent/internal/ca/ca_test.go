package ca

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/rithviknishad/care-pi/agent/internal/paths"
)

var now = time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC)

func TestDesired(t *testing.T) {
	c := Desired([]string{"care.local", "care-2.local", "clinic.lan", "abc.care-pi.rithviknishad.dev", "192.168.1.10"},
		[]string{"192.168.1.10", "192.168.1.77", "10.0.5.3", "fd00::1"})
	wantDNS := []string{".local", ".care-pi.rithviknishad.dev", "clinic.lan"}
	if !reflect.DeepEqual(c.DNS, wantDNS) {
		t.Fatalf("dns %v, want %v", c.DNS, wantDNS)
	}
	wantIPs := []string{"10.0.5.0/24", "192.168.1.0/24"}
	if !reflect.DeepEqual(c.IPs, wantIPs) {
		t.Fatalf("ips %v, want %v", c.IPs, wantIPs)
	}
	if m := c.Missing([]string{"care.local", "x.care-pi.rithviknishad.dev", "evil.example", "192.168.1.200", "192.168.2.1"},
		[]string{"10.0.5.99", "10.0.6.1"}); !reflect.DeepEqual(m, []string{"evil.example", "192.168.2.1", "10.0.6.1"}) {
		t.Fatalf("missing %v", m)
	}
}

func TestGenerateEncodesConstraints(t *testing.T) {
	c := Desired([]string{"care.local"}, []string{"192.168.1.10"})
	r, err := Generate(c, Options{CommonName: CommonName("care.local"), Now: now})
	if err != nil {
		t.Fatal(err)
	}
	// Parse the DER again: the extension is in the certificate, not only in the template.
	cert, err := x509.ParseCertificate(r.DER)
	if err != nil {
		t.Fatal(err)
	}
	if !cert.PermittedDNSDomainsCritical {
		t.Error("the name constraints extension is not critical")
	}
	if !reflect.DeepEqual(cert.PermittedDNSDomains, []string{".local", ".care-pi.rithviknishad.dev"}) {
		t.Errorf("permitted DNS %v", cert.PermittedDNSDomains)
	}
	if len(cert.PermittedIPRanges) != 1 || cert.PermittedIPRanges[0].String() != "192.168.1.0/24" {
		t.Errorf("permitted IP %v", cert.PermittedIPRanges)
	}
	if len(cert.ExcludedDNSDomains)+len(cert.ExcludedIPRanges) != 0 {
		t.Error("excluded subtrees are not expected")
	}
	if !cert.IsCA || cert.MaxPathLen != 1 || cert.KeyUsage&x509.KeyUsageCertSign == 0 {
		t.Errorf("CA flags: ca %v pathlen %d usage %v", cert.IsCA, cert.MaxPathLen, cert.KeyUsage)
	}
	if got := cert.NotAfter.Sub(cert.NotBefore); got < Lifetime || got > Lifetime+2*time.Hour {
		t.Errorf("lifetime %s", got)
	}
	if _, ok := cert.PublicKey.(*ecdsa.PublicKey); !ok || cert.PublicKey.(*ecdsa.PublicKey).Curve != elliptic.P256() {
		t.Error("the key is not ECDSA P-256")
	}
	if len(cert.SubjectKeyId) == 0 {
		t.Error("no subject key identifier")
	}
	got := ConstraintsOf(cert)
	if got == nil || !reflect.DeepEqual(*got, c) {
		t.Errorf("ConstraintsOf %+v, want %+v", got, c)
	}
}

func TestGenerateNeedsAnAddress(t *testing.T) {
	if _, err := Generate(Desired([]string{"care.local"}, nil), Options{Now: now}); err == nil {
		t.Fatal("a root without an IP range must fail: it would permit each IP address")
	}
}

// chain makes an intermediate (as Caddy does) and a leaf for the names and the IPs, and verifies the leaf.
func chain(t *testing.T, r *Root, dns []string, ips []string) error {
	t.Helper()
	ik, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	itmpl := &x509.Certificate{SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "Caddy Local Authority - ECC Intermediate"},
		NotBefore: now.Add(-time.Hour), NotAfter: now.Add(7 * 24 * time.Hour), IsCA: true, BasicConstraintsValid: true,
		MaxPathLenZero: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageCRLSign}
	ider, err := x509.CreateCertificate(rand.Reader, itmpl, r.Cert, &ik.PublicKey, r.Key)
	if err != nil {
		t.Fatal(err)
	}
	inter, _ := x509.ParseCertificate(ider)
	lk, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	ltmpl := &x509.Certificate{SerialNumber: big.NewInt(3), NotBefore: now.Add(-time.Minute), NotAfter: now.Add(12 * time.Hour),
		DNSNames: dns, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	for _, ip := range ips {
		ltmpl.IPAddresses = append(ltmpl.IPAddresses, net.ParseIP(ip))
	}
	lder, err := x509.CreateCertificate(rand.Reader, ltmpl, inter, &lk.PublicKey, ik)
	if err != nil {
		t.Fatal(err)
	}
	leaf, _ := x509.ParseCertificate(lder)
	roots, inters := x509.NewCertPool(), x509.NewCertPool()
	roots.AddCert(r.Cert)
	inters.AddCert(inter)
	_, err = leaf.Verify(x509.VerifyOptions{Roots: roots, Intermediates: inters, CurrentTime: now})
	return err
}

func TestLeafVerification(t *testing.T) {
	r, err := Generate(Desired([]string{"care.local"}, []string{"192.168.1.10"}), Options{Now: now})
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		dns  []string
		ips  []string
		ok   bool
	}{
		{"care.local", []string{"care.local"}, nil, true},
		{"renamed by avahi", []string{"care-2.local"}, nil, true},
		{"public name", []string{"sunrise-4f2a.care-pi.rithviknishad.dev"}, nil, true},
		{"evil.example", []string{"evil.example"}, nil, false},
		{"the zone itself", []string{"care-pi.rithviknishad.dev"}, nil, false},
		{"local itself", []string{"local"}, nil, false},
		{"ip in the /24", nil, []string{"192.168.1.10"}, true},
		{"other ip in the /24", nil, []string{"192.168.1.254"}, true},
		{"ip outside", nil, []string{"192.168.2.10"}, false},
		{"public ip", nil, []string{"8.8.8.8"}, false},
		{"ipv6", nil, []string{"fd00::1"}, false},
		{"mixed good and bad", []string{"care.local", "www.google.com"}, nil, false},
	}
	for _, c := range cases {
		err := chain(t, r, c.dns, c.ips)
		if c.ok && err != nil {
			t.Errorf("%s: %v", c.name, err)
		}
		if !c.ok {
			var ce x509.CertificateInvalidError
			if err == nil {
				t.Errorf("%s: verified, want a name constraint error", c.name)
			} else if !errors.As(err, &ce) || ce.Reason != x509.CANotAuthorizedForThisName {
				t.Errorf("%s: %v, want CANotAuthorizedForThisName", c.name, err)
			}
		}
	}
}

func TestEnsureMigratesCaddyRoot(t *testing.T) {
	p := paths.New(t.TempDir())
	// The root and the files of the local CA of Caddy (a host of Phase 1 to 5).
	old, err := Generate(Desired(nil, []string{"10.1.1.1"}), Options{Now: now})
	if err != nil {
		t.Fatal(err)
	}
	caDir := p.Abs(paths.CaddyCA)
	_ = os.MkdirAll(caDir, 0o700)
	_ = os.WriteFile(filepath.Join(caDir, "root.crt"), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: old.DER}), 0o644)
	for _, f := range []string{"root.key", "intermediate.crt", "intermediate.key"} {
		_ = os.WriteFile(filepath.Join(caDir, f), []byte("x"), 0o600)
	}
	leafDir := p.Abs(paths.CaddyLocalCerts + "/care.local")
	_ = os.MkdirAll(leafDir, 0o700)
	_ = os.WriteFile(filepath.Join(leafDir, "care.local.crt"), []byte("x"), 0o600)

	res, err := Ensure(p, []string{"care.local"}, []string{"192.168.1.10"}, CommonName("care.local"), now)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Created || !res.Replaced {
		t.Fatalf("created %v replaced %v", res.Created, res.Replaced)
	}
	if res.State.PreviousFingerprint != old.Fingerprint() || res.State.ChangedAt == nil || res.State.Fingerprint != res.Root.Fingerprint() {
		t.Fatalf("state %+v", res.State)
	}
	for _, f := range []string{"root.crt", "root.key", "intermediate.crt", "intermediate.key"} {
		if _, err := os.Stat(filepath.Join(caDir, f)); err == nil {
			t.Errorf("%s of Caddy is still there", f)
		}
	}
	if _, err := os.Stat(leafDir); err == nil {
		t.Error("the leaf certificates of the old CA are still there")
	}
	st, err := os.Stat(p.Abs(KeyPath()))
	if err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("root.key mode %v %v", st, err)
	}
	if st, _ := os.Stat(p.Abs(paths.CADir)); st.Mode().Perm() != 0o700 {
		t.Errorf("ca dir mode %v", st.Mode().Perm())
	}
	loaded, err := Load(p)
	if err != nil || loaded.Fingerprint() != res.Root.Fingerprint() {
		t.Fatalf("load: %v", err)
	}
	cert, _, agent, err := Trusted(p)
	if err != nil || !agent || !strings.Contains(cert.Subject.CommonName, "care.local") {
		t.Fatalf("trusted %v %v %v", cert, agent, err)
	}
	if ReadState(p).Fingerprint != res.Root.Fingerprint() {
		t.Error("state/ca.json was not written")
	}

	// A second Ensure keeps the root.
	again, err := Ensure(p, []string{"care.local"}, []string{"10.9.9.9"}, "x", now.Add(time.Hour))
	if err != nil || again.Created || again.Root.Fingerprint() != res.Root.Fingerprint() {
		t.Fatalf("second ensure %+v %v", again, err)
	}
}

func TestEnsureNewHost(t *testing.T) {
	p := paths.New(t.TempDir())
	res, err := Ensure(p, []string{"care.local"}, []string{"192.168.1.10"}, "", now)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Created || res.Replaced || res.State.ChangedAt != nil {
		t.Fatalf("new host: %+v", res)
	}
	if _, _, agent, err := Trusted(p); err != nil || !agent {
		t.Fatalf("trusted: agent %v, %v", agent, err)
	}
}

// writeACMECert writes a certificate of name as Caddy stores an ACME certificate of the issuer directory dir.
func writeACMECert(t *testing.T, p paths.Paths, dir, name string, notAfter time.Time) {
	t.Helper()
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(notAfter.Unix()), Subject: pkix.Name{CommonName: name},
		DNSNames: []string{name}, NotBefore: notAfter.Add(-90 * 24 * time.Hour), NotAfter: notAfter}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	d := p.Abs(paths.CaddyCertsDir + "/" + dir + "/" + name)
	if err := os.MkdirAll(d, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(d, name+".crt"), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
}

// m5: an expired certificate is issued (GET /tls shows it as expired), but it is not valid.
func TestValidCertNeedsNotAfter(t *testing.T) {
	p := paths.New(t.TempDir())
	const name = "sunrise-k3x9.care-pi.rithviknishad.dev"
	if ValidCert(p, name, now) != nil {
		t.Fatal("no certificate: want nil")
	}
	writeACMECert(t, p, "acme-v02.api.letsencrypt.org-directory", name, now.Add(-time.Hour))
	if c, _ := IssuedCert(p, name); c == nil {
		t.Fatal("IssuedCert must give the expired certificate")
	}
	if ValidCert(p, name, now) != nil {
		t.Fatal("an expired certificate is not valid")
	}
	if ValidCert(p, name, now.Add(-2*time.Hour)) == nil {
		t.Fatal("before its NotAfter the certificate is valid")
	}
}

// M2: browsers do not trust a certificate of a staging directory, so it is not valid. A production certificate next
// to it is.
func TestValidCertSkipsStaging(t *testing.T) {
	p := paths.New(t.TempDir())
	const name = "sunrise-k3x9.care-pi.rithviknishad.dev"
	writeACMECert(t, p, "acme-staging-v02.api.letsencrypt.org-directory", name, now.Add(80*24*time.Hour))
	if c, issuer := IssuedCert(p, name); c == nil || !StagingIssuer(issuer) {
		t.Fatalf("IssuedCert must give the staging certificate: %q", issuer)
	}
	if ValidCert(p, name, now) != nil {
		t.Fatal("a staging certificate is not valid")
	}
	writeACMECert(t, p, "acme-v02.api.letsencrypt.org-directory", name, now.Add(60*24*time.Hour))
	if c := ValidCert(p, name, now); c == nil || !c.NotAfter.Equal(now.Add(60*24*time.Hour)) {
		t.Fatalf("want the production certificate, got %v", c)
	}
}
