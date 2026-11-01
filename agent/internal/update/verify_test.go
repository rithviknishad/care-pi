package update

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"io"
	"log"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/rithviknishad/care-pi/agent/internal/ca"
	"github.com/rithviknishad/care-pi/agent/internal/paths"
	"github.com/rithviknishad/care-pi/agent/internal/render"
)

const verifyPublic = "sunrise-k3x9.care-pi.rithviknishad.dev"

// writeVerifyCert writes a certificate of the public name below the Caddy issuer directory dir.
func writeVerifyCert(t *testing.T, p paths.Paths, dir string, notAfter time.Time) {
	t.Helper()
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: verifyPublic}, DNSNames: []string{verifyPublic},
		NotBefore: notAfter.Add(-90 * 24 * time.Hour), NotAfter: notAfter}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	d := p.Abs(paths.CaddyCertsDir + "/" + dir + "/" + verifyPublic)
	if err := os.MkdirAll(d, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(d, verifyPublic+".crt"), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
}

// M1: the verify after an update connects to the public name only when its certificate is valid. Else it uses the
// primary local name (the local CA), so an update does not fail its verify while the certificate is pending.
func TestVerifyHost(t *testing.T) {
	now := time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC)
	public := &render.Model{Canonical: verifyPublic, PublicHost: verifyPublic, LocalHosts: []string{"care.local"}, PrimaryIP: "192.168.1.10"}
	local := &render.Model{Canonical: "care.local", LocalHosts: []string{"care.local"}, PrimaryIP: "192.168.1.10"}
	p := paths.New(t.TempDir())
	if got := verifyHost(p, local, now); got != "care.local" {
		t.Fatalf("local mode: %s", got)
	}
	if got := verifyHost(p, public, now); got != "care.local" {
		t.Fatalf("public name without a certificate: %s, want care.local", got)
	}
	noLocal := *public
	noLocal.LocalHosts = nil
	if got := verifyHost(p, &noLocal, now); got != "192.168.1.10" {
		t.Fatalf("public name without a certificate and without a local name: %s", got)
	}
	writeVerifyCert(t, p, "acme-staging-v02.api.letsencrypt.org-directory", now.Add(80*24*time.Hour))
	if got := verifyHost(p, public, now); got != "care.local" {
		t.Fatalf("a staging certificate: %s, want care.local", got)
	}
	writeVerifyCert(t, p, "acme-v02.api.letsencrypt.org-directory", now.Add(-time.Hour))
	if got := verifyHost(p, public, now); got != "care.local" {
		t.Fatalf("an expired certificate: %s, want care.local", got)
	}
	writeVerifyCert(t, p, "acme-v02.api.letsencrypt.org-directory", now.Add(60*24*time.Hour))
	if got := verifyHost(p, public, now); got != verifyPublic {
		t.Fatalf("a valid certificate: %s, want the public name", got)
	}
}

// M1: HTTPVerifier sends Host (SNI and the Host header), and trusts the local CA for it. Before the fix it sent the
// canonical host, the public name, which a leaf of the local CA does not have.
func TestHTTPVerifierUsesHost(t *testing.T) {
	p := paths.New(t.TempDir())
	if _, err := ca.Ensure(p, []string{"care.local"}, []string{"127.0.0.1"}, "", time.Now()); err != nil {
		t.Fatal(err)
	}
	root, err := ca.Load(p)
	if err != nil {
		t.Fatal(err)
	}
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	leaf := &x509.Certificate{SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "care.local"}, DNSNames: []string{"care.local"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(12 * time.Hour), KeyUsage: x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, leaf, root.Cert, &key.PublicKey, root.Key)
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var seen []string
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen = append(seen, r.TLS.ServerName+" "+r.Host)
		mu.Unlock()
		if r.URL.Path == "/api/v1/plug_config/" {
			_, _ = w.Write([]byte(`{"configs":[]}`))
		}
	}))
	srv.Config.ErrorLog = log.New(io.Discard, "", 0)
	srv.TLS = &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}, MinVersion: tls.VersionTLS12}
	srv.StartTLS()
	defer srv.Close()
	_, port, _ := net.SplitHostPort(srv.Listener.Addr().String())
	v := HTTPVerifier{Paths: p, Timeout: 5 * time.Second, port: port}
	tgt := VerifyTarget{Canonical: verifyPublic, Host: "care.local", Address: "127.0.0.1"}
	if err := v.Verify(context.Background(), tgt); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(seen) != 3 || seen[0] != "care.local care.local" {
		t.Fatalf("requests %q", seen)
	}
	// The public name with only a leaf of care.local: the handshake fails (the bug).
	tgt.Host = ""
	if err := v.Verify(context.Background(), tgt); err == nil {
		t.Fatal("the leaf of care.local must not verify for the public name")
	}
}
