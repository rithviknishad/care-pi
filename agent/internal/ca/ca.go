// Package ca makes and reads the root CA of the local CA mode (07 §4.1, S36).
//
// The agent, not Caddy, makes the root key and the root certificate. The certificate has the X.509
// name constraints extension (critical): Caddy can then only issue certificates for the permitted
// names and addresses, also if the root key leaks. Caddy gets the root through the global option
// pki › ca local › root (the Caddyfile), and makes its intermediate and the leaf certificates below it.
//
// The constraint set (Q-c, docs/connectivity.md):
//
//   - DNS ".local": each mDNS name (care.local, and care-2.local after an avahi rename).
//   - DNS ".care-pi.rithviknishad.dev": the subtree of the public names (S38).
//   - DNS <name> for each configured local name that is not below these two subtrees.
//   - IP <a.b.c.0/24> for each LAN IPv4 address: a DHCP change in the same /24 keeps the root valid.
//
// The Tailscale name and address are not in the set: Caddy gets the certificate of the MagicDNS name from
// tailscaled (a public certificate), and the Tailscale address is not a site of Caddy. Thus the root does not
// change when the operator turns Tailscale on.
package ca

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math/big"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/rithviknishad/care-pi/agent/internal/paths"
)

// PublicZone is the DNS zone of the public names (S38).
const PublicZone = "care-pi.rithviknishad.dev"

// Lifetime is the validity of the root: 10 years.
const Lifetime = 10 * 365 * 24 * time.Hour

// IPv4Bits is the prefix length of the permitted IP ranges.
const IPv4Bits = 24

// BaseDomains are the DNS subtrees that each root permits.
var BaseDomains = []string{".local", "." + PublicZone}

// Constraints are the permitted subtrees of a root (schema NameConstraints).
type Constraints struct {
	DNS []string `json:"permitted_dns_domains"`
	IPs []string `json:"permitted_ip_ranges"`
}

// Desired returns the constraint set for the local names and the LAN IPv4 addresses.
func Desired(hostnames, ips []string) Constraints {
	dns := append([]string{}, BaseDomains...)
	var extra []string
	for _, h := range hostnames {
		h = strings.ToLower(strings.TrimSuffix(h, "."))
		if h == "" || dnsCovered(dns, h) || contains(extra, h) {
			continue
		}
		if _, err := netip.ParseAddr(h); err == nil {
			continue
		}
		extra = append(extra, h)
	}
	sort.Strings(extra)
	var ranges []string
	for _, ip := range ips {
		a, err := netip.ParseAddr(ip)
		if err != nil || !a.Is4() {
			continue
		}
		p := netip.PrefixFrom(a, IPv4Bits).Masked().String()
		if !contains(ranges, p) {
			ranges = append(ranges, p)
		}
	}
	sort.Strings(ranges)
	return Constraints{DNS: append(dns, extra...), IPs: nonNil(ranges)}
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

// dnsCovered reports whether the name is in one of the DNS subtrees. A constraint with a leading dot
// permits only the names below it (the rule of Go, Chrome, NSS, and Apple). A constraint without it
// permits the name and the names below it (RFC 5280 section 4.2.1.10).
func dnsCovered(constraints []string, name string) bool {
	name = strings.ToLower(strings.TrimSuffix(name, "."))
	for _, c := range constraints {
		c = strings.ToLower(c)
		if strings.HasPrefix(c, ".") {
			if strings.HasSuffix(name, c) && len(name) > len(c) {
				return true
			}
			continue
		}
		if name == c || strings.HasSuffix(name, "."+c) {
			return true
		}
	}
	return false
}

func ipCovered(ranges []string, ip string) bool {
	a, err := netip.ParseAddr(ip)
	if err != nil {
		return false
	}
	for _, r := range ranges {
		if p, err := netip.ParsePrefix(r); err == nil && p.Contains(a) {
			return true
		}
	}
	return false
}

// Missing returns the names and the IPv4 addresses that the constraints do not permit.
func (c Constraints) Missing(hostnames, ips []string) []string {
	var out []string
	for _, h := range hostnames {
		if _, err := netip.ParseAddr(h); err == nil {
			if !ipCovered(c.IPs, h) {
				out = append(out, h)
			}
			continue
		}
		if !dnsCovered(c.DNS, h) {
			out = append(out, h)
		}
	}
	for _, ip := range ips {
		if a, err := netip.ParseAddr(ip); err == nil && a.Is4() && !ipCovered(c.IPs, ip) && !contains(out, ip) {
			out = append(out, ip)
		}
	}
	return out
}

// Root is a root CA: the certificate and, when it was loaded or made, the key.
type Root struct {
	Cert *x509.Certificate
	DER  []byte
	Key  *ecdsa.PrivateKey
}

// Fingerprint returns the SHA-256 of the DER certificate in lower-case hex.
func (r *Root) Fingerprint() string { return Fingerprint(r.DER) }

// Fingerprint returns the SHA-256 of a DER certificate in lower-case hex.
func Fingerprint(der []byte) string {
	sum := sha256.Sum256(der)
	return hex.EncodeToString(sum[:])
}

// ConstraintsOf returns the permitted subtrees of a certificate, or nil when it has no name constraints.
func ConstraintsOf(c *x509.Certificate) *Constraints {
	if c == nil || (len(c.PermittedDNSDomains) == 0 && len(c.PermittedIPRanges) == 0) {
		return nil
	}
	out := &Constraints{DNS: append([]string{}, c.PermittedDNSDomains...), IPs: []string{}}
	for _, n := range c.PermittedIPRanges {
		ones, _ := n.Mask.Size()
		a, ok := netip.AddrFromSlice(n.IP)
		if !ok {
			continue
		}
		out.IPs = append(out.IPs, netip.PrefixFrom(a.Unmap(), ones).String())
	}
	return out
}

// Options control Generate.
type Options struct {
	// CommonName is the subject CN, for example "care-pi care.local Root CA".
	CommonName string
	Now        time.Time
	// Rand is the source of the key and the serial number. nil uses crypto/rand.
	Rand io.Reader
}

// Generate makes a new ECDSA P-256 root with the name constraints c. It needs at least one IPv4 range:
// without an IP constraint, the root could sign each IP address.
func Generate(c Constraints, o Options) (*Root, error) {
	if len(c.IPs) == 0 {
		return nil, errors.New("ca: the root needs at least one permitted IPv4 range (no LAN address)")
	}
	if len(c.DNS) == 0 {
		return nil, errors.New("ca: the root needs at least one permitted DNS domain")
	}
	rnd := o.Rand
	if rnd == nil {
		rnd = rand.Reader
	}
	now := o.Now
	if now.IsZero() {
		now = time.Now()
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rnd)
	if err != nil {
		return nil, fmt.Errorf("ca: cannot make the key: %w", err)
	}
	serial, err := rand.Int(rnd, new(big.Int).Lsh(big.NewInt(1), 127))
	if err != nil {
		return nil, fmt.Errorf("ca: cannot make the serial number: %w", err)
	}
	var ranges []*net.IPNet
	for _, r := range c.IPs {
		_, n, err := net.ParseCIDR(r)
		if err != nil {
			return nil, fmt.Errorf("ca: permitted range %q: %w", r, err)
		}
		ranges = append(ranges, n)
	}
	cn := o.CommonName
	if cn == "" {
		cn = "care-pi Root CA"
	}
	// x509.CreateCertificate makes the subject key identifier of a CA template (SHA-1 of the key, RFC 5280).
	tmpl := &x509.Certificate{
		SerialNumber:                serial,
		Subject:                     pkix.Name{CommonName: cn, Organization: []string{"care-pi"}},
		NotBefore:                   now.Add(-time.Hour).UTC().Truncate(time.Second),
		NotAfter:                    now.Add(Lifetime).UTC().Truncate(time.Second),
		IsCA:                        true,
		BasicConstraintsValid:       true,
		MaxPathLen:                  1,
		KeyUsage:                    x509.KeyUsageCertSign | x509.KeyUsageCRLSign | x509.KeyUsageDigitalSignature,
		PermittedDNSDomainsCritical: true,
		PermittedDNSDomains:         append([]string{}, c.DNS...),
		PermittedIPRanges:           ranges,
	}
	der, err := x509.CreateCertificate(rnd, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, fmt.Errorf("ca: cannot make the certificate: %w", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, err
	}
	return &Root{Cert: cert, DER: der, Key: key}, nil
}

// Files of the root below paths.CADir.
const (
	CertFile = "root.crt"
	KeyFile  = "root.key"
)

// CertPath is the host path of the root certificate.
func CertPath() string { return paths.CADir + "/" + CertFile }

// KeyPath is the host path of the root key.
func KeyPath() string { return paths.CADir + "/" + KeyFile }

// Exists reports whether the agent root exists (certificate and key).
func Exists(p paths.Paths) bool {
	_, e1 := os.Stat(p.Abs(CertPath()))
	_, e2 := os.Stat(p.Abs(KeyPath()))
	return e1 == nil && e2 == nil
}

// Write stores the root: root.key 0600, root.crt 0644, in a 0700 directory.
func Write(p paths.Paths, r *Root) error {
	if r == nil || r.Key == nil {
		return errors.New("ca: no key to write")
	}
	dir := p.Abs(paths.CADir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return err
	}
	kb, err := x509.MarshalECPrivateKey(r.Key)
	if err != nil {
		return err
	}
	if err := writeAtomic(filepath.Join(dir, KeyFile), pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kb}), 0o600); err != nil {
		return err
	}
	return writeAtomic(filepath.Join(dir, CertFile), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: r.DER}), 0o644)
}

func writeAtomic(path string, data []byte, mode os.FileMode) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, mode); err != nil {
		return err
	}
	if err := os.Chmod(tmp, mode); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// ParseCertPEM reads the first certificate of a PEM file.
func ParseCertPEM(data []byte) (*x509.Certificate, []byte, error) {
	block, _ := pem.Decode(data)
	if block == nil || block.Type != "CERTIFICATE" {
		return nil, nil, errors.New("no PEM certificate")
	}
	c, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, nil, err
	}
	return c, block.Bytes, nil
}

// Load reads the agent root with its key.
func Load(p paths.Paths) (*Root, error) {
	r, err := loadFrom(p.Abs(CertPath()), p.Abs(KeyPath()))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", paths.CADir, err)
	}
	return r, nil
}

// Trusted returns the root that the clinic devices must trust: the agent root when it exists, else the
// root that Caddy made (a host before the migration). agent is true for the agent root.
func Trusted(p paths.Paths) (cert *x509.Certificate, der []byte, agent bool, err error) {
	if data, rerr := os.ReadFile(p.Abs(CertPath())); rerr == nil {
		c, d, perr := ParseCertPEM(data)
		if perr != nil {
			return nil, nil, false, fmt.Errorf("%s: %w", CertPath(), perr)
		}
		return c, d, true, nil
	}
	data, err := os.ReadFile(p.Abs(paths.CaddyRootCA))
	if err != nil {
		return nil, nil, false, err
	}
	c, d, err := ParseCertPEM(data)
	if err != nil {
		return nil, nil, false, fmt.Errorf("%s: %w", paths.CaddyRootCA, err)
	}
	return c, d, false, nil
}

// TrustedPEM returns the PEM of Trusted.
func TrustedPEM(p paths.Paths) ([]byte, error) {
	_, der, _, err := Trusted(p)
	if err != nil {
		return nil, err
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), nil
}

// State is state/ca.json: the record of the switch to the agent root (07 §4.1, the migration).
type State struct {
	Fingerprint         string    `json:"fingerprint_sha256"`
	CreatedAt           time.Time `json:"created_at"`
	PreviousFingerprint string    `json:"previous_fingerprint_sha256,omitempty"`
	// ChangedAt is set when the root replaced an older root that the clinic devices trusted.
	ChangedAt *time.Time `json:"changed_at,omitempty"`
	// Constraints are the constraints of the root.
	Constraints Constraints `json:"constraints"`
}

// ReadState reads state/ca.json. A missing file gives a zero State.
func ReadState(p paths.Paths) State {
	var st State
	if data, err := os.ReadFile(p.Abs(paths.CAState)); err == nil {
		_ = json.Unmarshal(data, &st)
	}
	return st
}

func writeState(p paths.Paths, st State) error {
	data, _ := json.MarshalIndent(st, "", "  ")
	if err := os.MkdirAll(filepath.Dir(p.Abs(paths.CAState)), 0o700); err != nil {
		return err
	}
	return writeAtomic(p.Abs(paths.CAState), data, 0o600)
}

// EnsureResult tells what Ensure did.
type EnsureResult struct {
	// Created is true when Ensure made a new root.
	Created bool
	// Replaced is true when the new root replaced the root that Caddy made (the migration):
	// each clinic device must install the new root.
	Replaced bool
	Root     *Root
	State    State
}

// Ensure makes the agent root when it does not exist. Before it writes the new root, it removes the
// root, the intermediate, and the certificates of the old local CA in the data directory of Caddy, so
// that Caddy makes a new intermediate below the new root at its next start. The caller restarts care-caddy.
func Ensure(p paths.Paths, hostnames, ips []string, cn string, now time.Time) (*EnsureResult, error) {
	if Exists(p) {
		r, err := Load(p)
		if err != nil {
			return nil, err
		}
		return &EnsureResult{Root: r, State: ReadState(p)}, nil
	}
	c := Desired(hostnames, ips)
	r, err := Generate(c, Options{CommonName: cn, Now: now})
	if err != nil {
		return nil, err
	}
	st := State{Fingerprint: r.Fingerprint(), CreatedAt: now.UTC().Truncate(time.Second), Constraints: c}
	res := &EnsureResult{Created: true, Root: r}
	if data, err := os.ReadFile(p.Abs(paths.CaddyRootCA)); err == nil {
		if _, der, perr := ParseCertPEM(data); perr == nil {
			st.PreviousFingerprint = Fingerprint(der)
			t := st.CreatedAt
			st.ChangedAt = &t
			res.Replaced = true
		}
	}
	if err := ClearCaddyCA(p); err != nil {
		return nil, err
	}
	if err := Write(p, r); err != nil {
		return nil, err
	}
	if err := writeState(p, st); err != nil {
		return nil, err
	}
	res.State = st
	return res, nil
}

// ClearCaddyCA removes the CA files of Caddy (root, intermediate) and the leaf certificates of the
// local CA. Caddy keeps no copy of a configured root in its storage.
func ClearCaddyCA(p paths.Paths) error {
	for _, f := range []string{"root.crt", "root.key", "intermediate.crt", "intermediate.key"} {
		if err := os.Remove(p.Abs(paths.CaddyCA + "/" + f)); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
	}
	return os.RemoveAll(p.Abs(paths.CaddyLocalCerts))
}

// CommonName returns the subject CN of a new root for the primary local name.
func CommonName(primary string) string {
	if primary == "" {
		return "care-pi Root CA"
	}
	return "care-pi " + primary + " Root CA"
}

// IssuedCert returns the certificate that Caddy holds for a name from an ACME issuer (the public mode):
// <caddy data>/caddy/certificates/<issuer>/<name>/<name>.crt, for each issuer except the local CA.
// It returns the certificate with the latest expiry, and the issuer directory, or nil.
// The certificate can be expired: ValidCert gives only a certificate that a browser trusts now.
func IssuedCert(p paths.Paths, name string) (*x509.Certificate, string) {
	var best *x509.Certificate
	var issuer string
	for _, c := range issuedCerts(p, name) {
		if best == nil || c.cert.NotAfter.After(best.NotAfter) {
			best, issuer = c.cert, c.issuer
		}
	}
	return best, issuer
}

// ValidCert returns the certificate of IssuedCert that a browser trusts at now: it is not expired, and it is not from
// a staging directory (StagingIssuer). It returns nil when there is none. Port 80 sends the browser to the public name
// only with such a certificate.
func ValidCert(p paths.Paths, name string, now time.Time) *x509.Certificate {
	var best *x509.Certificate
	for _, c := range issuedCerts(p, name) {
		if !now.Before(c.cert.NotAfter) || StagingIssuer(c.issuer) {
			continue
		}
		if best == nil || c.cert.NotAfter.After(best.NotAfter) {
			best = c.cert
		}
	}
	return best
}

// StagingIssuer reports whether a Caddy issuer directory is of a staging ACME directory (for example
// acme-staging-v02.api.letsencrypt.org-directory). Browsers do not trust its certificates.
func StagingIssuer(dir string) bool { return strings.Contains(dir, "staging") }

type issued struct {
	cert   *x509.Certificate
	issuer string
}

// issuedCerts reads the certificate of name from each ACME issuer directory of Caddy.
func issuedCerts(p paths.Paths, name string) []issued {
	if name == "" {
		return nil
	}
	dirs, err := os.ReadDir(p.Abs(paths.CaddyCertsDir))
	if err != nil {
		return nil
	}
	var out []issued
	for _, d := range dirs {
		if !d.IsDir() || d.Name() == "local" {
			continue
		}
		data, err := os.ReadFile(p.Abs(paths.CaddyCertsDir + "/" + d.Name() + "/" + name + "/" + name + ".crt"))
		if err != nil {
			continue
		}
		c, _, err := ParseCertPEM(data)
		if err != nil {
			continue
		}
		out = append(out, issued{c, d.Name()})
	}
	return out
}

// Adopt replaces the agent root with the root in dir (root.crt and root.key; a restore from backup, so the clinic
// devices keep the root that they trust). It clears the CA files of Caddy, so that Caddy makes a new intermediate
// below the adopted root. The caller restarts care-caddy.
func Adopt(p paths.Paths, dir string, now time.Time) (*Root, error) {
	src := paths.New(dir)
	r, err := loadFrom(src.Abs("/"+CertFile), src.Abs("/"+KeyFile))
	if err != nil {
		return nil, fmt.Errorf("the restored root is not complete: %w", err)
	}
	if err := ClearCaddyCA(p); err != nil {
		return nil, err
	}
	if err := Write(p, r); err != nil {
		return nil, err
	}
	st := State{Fingerprint: r.Fingerprint(), CreatedAt: now.UTC().Truncate(time.Second)}
	if c := ConstraintsOf(r.Cert); c != nil {
		st.Constraints = *c
	}
	if err := writeState(p, st); err != nil {
		return nil, err
	}
	return r, nil
}

// loadFrom reads a root and its key from two files.
func loadFrom(certPath, keyPath string) (*Root, error) {
	data, err := os.ReadFile(certPath)
	if err != nil {
		return nil, err
	}
	cert, der, err := ParseCertPEM(data)
	if err != nil {
		return nil, err
	}
	kdata, err := os.ReadFile(keyPath)
	if err != nil {
		return nil, err
	}
	block, _ := pem.Decode(kdata)
	if block == nil {
		return nil, errors.New("no PEM key")
	}
	key, err := x509.ParseECPrivateKey(block.Bytes)
	if err != nil {
		return nil, err
	}
	if !key.PublicKey.Equal(cert.PublicKey) {
		return nil, errors.New("the key does not match the certificate")
	}
	return &Root{Cert: cert, DER: der, Key: key}, nil
}
