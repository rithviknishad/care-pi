package service

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/rithviknishad/care-pi/agent/internal/apply"
	"github.com/rithviknishad/care-pi/agent/internal/ca"
	"github.com/rithviknishad/care-pi/agent/internal/dnsclient"
	"github.com/rithviknishad/care-pi/agent/internal/jobs"
	"github.com/rithviknishad/care-pi/agent/internal/lock"
	"github.com/rithviknishad/care-pi/agent/internal/netinfo"
	"github.com/rithviknishad/care-pi/agent/internal/paths"
	"github.com/rithviknishad/care-pi/agent/internal/problem"
	"github.com/rithviknishad/care-pi/agent/internal/render"
	"github.com/rithviknishad/care-pi/agent/internal/tailscale"
	"github.com/rithviknishad/care-pi/agent/internal/wifi"
)

// Phase 6: the public name, Tailscale, and Wi-Fi (docs/connectivity.md, docs/api/README.md section 15).

// CertPageURL is the page of the DNS/ACME service that offers a root CA from the fragment of the URL (07 §4.4).
const CertPageURL = "https://" + ca.PublicZone + "/cert"

// Defaults of the waits of the enrol jobs. Tests change them on the Service.
const (
	defaultCertWait = 5 * time.Minute
	defaultLogin    = tailscale.UpTimeout
)

func (s *Service) dnsClient() (*dnsclient.Client, error) {
	keyFile := s.Paths.Abs(paths.DNSClientKeyFile)
	key, _, err := dnsclient.LoadOrCreateKey(keyFile)
	if err != nil {
		return nil, fmt.Errorf("cannot read the key of the DNS client: %w", err)
	}
	c := &dnsclient.Client{BaseURL: s.DNSURL, Key: key, Now: s.Now}
	if s.DNSBackoff > 0 {
		c.Backoff = s.DNSBackoff
	}
	// A rotation to a new key lost its answer: the service can have the new key (docs/connectivity.md 3.6).
	pending := s.Paths.Abs(paths.DNSClientPendingKeyFile)
	if pk, err := dnsclient.ReadKey(pending); err == nil {
		c.Pending = pk
	} else if !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("cannot read the pending key of the DNS client: %w", err)
	}
	c.Promote = func(k ed25519.PrivateKey) error {
		if err := dnsclient.WriteKey(keyFile, k); err != nil {
			return err
		}
		if err := os.Remove(pending); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		return nil
	}
	return c, nil
}

func (s *Service) tailscaleClient() tailscale.Client {
	return tailscale.Client{Runner: s.Host.Runner, RunDir: s.Paths.Abs(paths.RunDir), Poll: s.PollInterval}
}

func (s *Service) wifiClient() wifi.Client { return wifi.Client{Runner: s.Host.Runner} }

// dnsProblem maps an error of the DNS service to a problem.
func dnsProblem(err error) error {
	if err == nil {
		return nil
	}
	var se *dnsclient.Error
	if errors.As(err, &se) {
		if se.Temporary() {
			return problem.New(problem.DNSServiceUnreachable, se.Error())
		}
		return problem.New(problem.DNSServiceRejected, se.Error())
	}
	if errors.Is(err, dnsclient.ErrUnreachable) {
		return problem.New(problem.DNSServiceUnreachable, err.Error()+". Check the internet connection of the device.")
	}
	return err
}

// CertState is the certificate of a public name (schema PublicCertificate).
type CertState struct {
	// State is issued, pending, expired, or error.
	State    string     `json:"state"`
	NotAfter *time.Time `json:"not_after"`
	Issuer   *string    `json:"issuer"`
	Staging  bool       `json:"staging"`
}

// PublicTLS is the public mode in GET /tls (schema TlsPublic).
type PublicTLS struct {
	Enabled     bool       `json:"enabled"`
	ClinicID    *string    `json:"clinic_id"`
	Hostname    *string    `json:"hostname"`
	ACMECA      *string    `json:"acme_ca"`
	Certificate *CertState `json:"certificate"`
	DNS         struct {
		Address     *string    `json:"address"`
		UpdatedAt   *time.Time `json:"updated_at"`
		LastError   *string    `json:"last_error"`
		LastErrorAt *time.Time `json:"last_error_at"`
		Revoked     bool       `json:"revoked"`
	} `json:"dns"`
	// Primary is true when port 80 sends the browser to the public name (the certificate is issued).
	Primary bool `json:"primary"`
	// HealthURL is the URL that the console fetches to detect a DNS rebinding block of the router.
	HealthURL *string `json:"health_url"`
}

// TailscaleTLS is the Tailscale mode in GET /tls (schema TlsTailscale).
type TailscaleTLS struct {
	Enabled      bool    `json:"enabled"`
	Hostname     *string `json:"hostname"`
	Installed    bool    `json:"installed"`
	BackendState *string `json:"backend_state"`
	LoginURL     *string `json:"login_url"`
	Tailnet      *string `json:"tailnet"`
	IPv4         *string `json:"ipv4"`
	HTTPSEnabled bool    `json:"https_enabled"`
	// Published is true when the pod publishes on the Tailscale address (care.pod).
	Published bool `json:"published"`
}

// tailscaleState is state/tls-tailscale.json: what the enrol job saw last. The login URL is a capability:
// only GET /tls (with a session) shows it, never a job log.
type tailscaleState struct {
	BackendState string     `json:"backend_state"`
	LoginURL     string     `json:"login_url,omitempty"`
	DNSName      string     `json:"dns_name,omitempty"`
	Tailnet      string     `json:"tailnet,omitempty"`
	IPv4         string     `json:"ipv4,omitempty"`
	UpdatedAt    *time.Time `json:"updated_at,omitempty"`
}

func readJSONFile[T any](path string) (T, bool) {
	var v T
	data, err := os.ReadFile(path)
	if err != nil {
		return v, false
	}
	return v, json.Unmarshal(data, &v) == nil
}

func writeJSONFile(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return lock.WriteFileAtomic(path, data, 0o600)
}

func (s *Service) certState(host string, enrolledAt *time.Time) *CertState {
	cert, issuer := ca.IssuedCert(s.Paths, host)
	now := s.now()
	if cert == nil {
		st := &CertState{State: "pending"}
		if enrolledAt != nil && now.Sub(*enrolledAt) > 15*time.Minute {
			st.State = "error"
		}
		return st
	}
	na := cert.NotAfter.UTC()
	st := &CertState{State: "issued", NotAfter: &na, Issuer: strPtr(cert.Issuer.CommonName), Staging: ca.StagingIssuer(issuer)}
	if !now.Before(cert.NotAfter) {
		st.State = "expired"
	}
	return st
}

func (s *Service) publicTLS(ctx context.Context, enabled bool, clinicID, acmeCA string) PublicTLS {
	var out PublicTLS
	out.Enabled = enabled
	out.ClinicID = strPtr(clinicID)
	st := apply.ReadPublicState(s.Paths)
	out.DNS.Address = strPtr(st.Address)
	out.DNS.UpdatedAt = st.AddressAt
	out.DNS.LastError = strPtr(st.LastError)
	out.DNS.LastErrorAt = st.LastErrorAt
	out.DNS.Revoked = st.Revoked
	if !enabled || clinicID == "" {
		return out
	}
	host := clinicID + "." + ca.PublicZone
	out.Hostname = &host
	out.ACMECA = strPtr(acmeCA)
	out.Certificate = s.certState(host, st.EnrolledAt)
	// A staging certificate is issued, but browsers do not trust it: port 80 stays on the same host.
	out.Primary = out.Certificate.State == "issued" && !out.Certificate.Staging
	u := "https://" + host + "/_care-pi/health"
	out.HealthURL = &u
	_ = ctx
	return out
}

func (s *Service) tailscaleTLS(ctx context.Context, enabled bool, hostname string) TailscaleTLS {
	out := TailscaleTLS{Enabled: enabled, Hostname: strPtr(hostname)}
	st, _ := readJSONFile[tailscaleState](s.Paths.Abs(paths.TailscaleState))
	// Read tailscaled only when the mode is on, or when an enrol job ran.
	if enabled || st.BackendState != "" {
		if live, err := s.tailscaleClient().Status(ctx); err == nil {
			out.Installed = live.Installed
			if live.Installed {
				out.BackendState = strPtr(live.BackendState)
				out.Tailnet = strPtr(live.Tailnet)
				out.IPv4 = strPtr(live.IPv4)
				out.HTTPSEnabled = live.HTTPSEnabled()
				if live.BackendState == tailscale.StateNeedsLogin {
					out.LoginURL = strPtr(live.AuthURL)
				}
			}
		}
	}
	if out.LoginURL == nil && st.LoginURL != "" && st.BackendState == tailscale.StateNeedsLogin {
		out.LoginURL = strPtr(st.LoginURL)
	}
	if out.IPv4 != nil {
		for _, a := range s.Published() {
			out.Published = out.Published || a == *out.IPv4
		}
		if !out.Published {
			out.Published = podPublishes(s.Paths, *out.IPv4)
		}
	}
	return out
}

func podPublishes(p paths.Paths, ip string) bool {
	data, err := os.ReadFile(p.Abs(paths.QuadletDir + "/care.pod"))
	return err == nil && strings.Contains(string(data), "PublishPort="+ip+":")
}

// ---- the public mode ----

// PublicRequest is PUT /tls/public (schema PublicTlsRequest).
type PublicRequest struct {
	Enabled      bool
	HostnameHint string
	// ACMECA overrides the ACME directory (the staging directory of Let's Encrypt for a test). nil keeps it.
	ACMECA *string
	Reason string
}

var hintRE = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

// StartPublic starts the job tls-public-enrol: enable or disable the public name.
func (s *Service) StartPublic(ctx context.Context, req PublicRequest, source string) (*jobs.Job, error) {
	if err := s.requireReady(); err != nil {
		return nil, err
	}
	cfg, err := s.Config()
	if err != nil {
		return nil, err
	}
	if req.HostnameHint == "" {
		req.HostnameHint = cfg.Identity.Hostname
	}
	if req.Enabled && !hintRE.MatchString(req.HostnameHint) {
		return nil, problem.Invalid("/hostname_hint", "use 1 to 63 characters: a-z, 0-9, and a dash inside", "pattern")
	}
	if req.ACMECA != nil && *req.ACMECA != "" && !strings.HasPrefix(*req.ACMECA, "https://") {
		return nil, problem.Invalid("/acme_ca", "use an https:// URL", "pattern")
	}
	if !req.Enabled && !cfg.TLS.Public.Enabled && !fileExists(s.Paths.Abs(paths.ACMEDNSFile)) {
		return nil, problem.New(problem.StateConflict, "The public name is off.")
	}
	return s.Jobs.Start(ctx, jobs.Spec{Kind: jobs.KindTLSPublicEnrol, Source: source, Reason: req.Reason, Lock: true},
		func(ctx context.Context, rec *jobs.Recorder) (any, error) {
			if req.Enabled {
				return s.publicEnable(ctx, rec, req)
			}
			return s.publicDisable(ctx, rec)
		})
}

// step runs fn as one job step.
func step(rec *jobs.Recorder, name string, fn func() error) error {
	end := rec.Step(name)
	err := fn()
	end(err)
	return err
}

func (s *Service) runApply(ctx context.Context, rec *jobs.Recorder, reason string) error {
	o := s.applyOptions(apply.Options{Reason: reason, Out: rec.Writer(), Journal: rec, LockHeld: true, JobID: rec.ID(), JobKind: rec.Job().Kind})
	_, err := apply.Run(ctx, o)
	return applyProblem(err)
}

func (s *Service) publicEnable(ctx context.Context, rec *jobs.Recorder, req PublicRequest) (any, error) {
	st := apply.ReadPublicState(s.Paths)
	cli, err := s.dnsClient()
	if err != nil {
		return nil, err
	}
	// A registration that exists (a retry, or an enable after a failed apply) is used again: an id is never reused.
	reuse := st.ClinicID != "" && fileExists(s.Paths.Abs(paths.ACMEDNSFile))
	if reuse {
		if _, err := cli.Status(ctx, st.ClinicID); err != nil {
			var se *dnsclient.Error
			switch {
			case errors.As(err, &se) && se.Revoked():
				reuse = false
			case errors.As(err, &se) && se.Unauthorized():
				// The service has another key for the id (a lost key rotation): this device cannot use the id.
				rec.Logf("the DNS service refuses the key of this device for %s; register again", st.ClinicID)
				reuse = false
			default:
				return nil, dnsProblem(err)
			}
		}
	}
	if !reuse {
		if err := step(rec, "dns-register: register "+req.HostnameHint+" with the DNS service", func() error {
			reg, err := cli.Register(ctx, req.HostnameHint, false)
			if err != nil {
				return dnsProblem(err)
			}
			if want := reg.ID + "." + ca.PublicZone; reg.FullDomain != want {
				return fmt.Errorf("the DNS service gave the name %s, but care-pi expects %s", reg.FullDomain, want)
			}
			if err := dnsclient.WriteCredentials(s.Paths.Abs(paths.ACMEDNSFile), reg.ACME.Credentials()); err != nil {
				return err
			}
			now := s.now()
			st = apply.PublicState{ClinicID: reg.ID, FullDomain: reg.FullDomain, ServiceURL: cli.BaseURL, EnrolledAt: &now}
			rec.Logf("the DNS service gave the name %s", reg.FullDomain)
			return apply.WritePublicState(s.Paths, st)
		}); err != nil {
			return nil, err
		}
	}
	if err := step(rec, "dns-address: send the LAN address to the DNS service", func() error {
		return s.sendAddress(ctx, cli, st.ClinicID, true)
	}); err != nil {
		return nil, err
	}
	if err := step(rec, "write-config: tls.public", func() error {
		return s.updateConfig(func(doc map[string]any) {
			pub := section(section(doc, "tls"), "public")
			pub["enabled"] = true
			pub["clinic_id"] = st.ClinicID
			if req.ACMECA != nil {
				if *req.ACMECA == "" {
					delete(pub, "acme_ca")
				} else {
					pub["acme_ca"] = *req.ACMECA
				}
			}
		})
	}); err != nil {
		return nil, err
	}
	if err := s.runApply(ctx, rec, "public name on"); err != nil {
		return nil, err
	}
	host := st.ClinicID + "." + ca.PublicZone
	certificate := "pending"
	wait := s.CertWait
	if wait == 0 {
		wait = defaultCertWait
	}
	_ = step(rec, "wait-certificate: wait for the certificate of "+host, func() error {
		deadline := time.Now().Add(wait)
		for {
			// A staging certificate counts here (a test of the issuance), but port 80 does not use it (ValidCert).
			if c, _ := ca.IssuedCert(s.Paths, host); c != nil && s.now().Before(c.NotAfter) {
				certificate = "issued"
				return nil
			}
			if !time.Now().Before(deadline) {
				rec.Logf("warning: no certificate after %s; Caddy tries again. Check: care-pi logs caddy", wait)
				return nil
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(s.pollInterval()):
			}
		}
	})
	if certificate == "issued" {
		// The redirect of port 80 goes to the public name now (07 §6.1).
		if err := s.runApply(ctx, rec, "public certificate issued"); err != nil {
			return nil, err
		}
	}
	return jobs.TLSPublicResult{Kind: jobs.KindTLSPublicEnrol, Enabled: true, Hostname: &host, Certificate: certificate}, nil
}

func (s *Service) pollInterval() time.Duration {
	if s.PollInterval > 0 {
		return s.PollInterval
	}
	return 5 * time.Second
}

func (s *Service) publicDisable(ctx context.Context, rec *jobs.Recorder) (any, error) {
	st := apply.ReadPublicState(s.Paths)
	if err := step(rec, "write-config: tls.public off", func() error {
		return s.updateConfig(func(doc map[string]any) {
			pub := section(section(doc, "tls"), "public")
			pub["enabled"] = false
			delete(pub, "clinic_id")
		})
	}); err != nil {
		return nil, err
	}
	if err := s.runApply(ctx, rec, "public name off"); err != nil {
		return nil, err
	}
	if st.ClinicID != "" {
		// The name never comes back: the service revokes the id. A new enable gets a new name.
		if err := step(rec, "dns-delete: delete the registration "+st.ClinicID, func() error {
			cli, err := s.dnsClient()
			if err != nil {
				return err
			}
			err = cli.Delete(ctx, st.ClinicID)
			var se *dnsclient.Error
			if errors.As(err, &se) && se.Revoked() {
				err = nil
			}
			if errors.As(err, &se) && se.Unauthorized() {
				// The service has another key for the id: this device cannot delete it. Forget the registration.
				rec.Logf("warning: the DNS service refuses the key of this device; the agent forgets the registration %s", st.ClinicID)
				err = nil
			}
			return dnsProblem(err)
		}); err != nil {
			return nil, err
		}
	}
	for _, f := range []string{paths.ACMEDNSFile, paths.PublicState} {
		if err := os.Remove(s.Paths.Abs(f)); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return nil, err
		}
	}
	_, _ = s.Host.Runner.Run(ctx, hostCmd("podman", "secret", "rm", "--ignore", render.ACMEDNSSecret))
	return jobs.TLSPublicResult{Kind: jobs.KindTLSPublicEnrol, Enabled: false, Certificate: "none"}, nil
}

// sendAddress sends the primary LAN address to the DNS service and records it. force sends it also when the
// record has the same address.
func (s *Service) sendAddress(ctx context.Context, cli *dnsclient.Client, id string, force bool) error {
	ips := s.Addresses()
	if len(ips) == 0 {
		return errors.New("the device has no LAN IPv4 address")
	}
	st := apply.ReadPublicState(s.Paths)
	if !force && st.Address == ips[0] {
		return nil
	}
	_, err := cli.UpdateAddress(ctx, id, ips[0])
	now := s.now()
	if err != nil {
		var se *dnsclient.Error
		st.LastError, st.LastErrorAt, st.Revoked = err.Error(), &now, errors.As(err, &se) && se.Revoked()
		_ = apply.WritePublicState(s.Paths, st)
		return dnsProblem(err)
	}
	st.Address, st.AddressAt, st.LastError, st.LastErrorAt = ips[0], &now, "", nil
	return apply.WritePublicState(s.Paths, st)
}

// UpdateDNSAddress sends the LAN address when it changed (apply.Options.DNSAddress, the plan action update-dns).
func (s *Service) UpdateDNSAddress(ctx context.Context, ipv4 string) error {
	st := apply.ReadPublicState(s.Paths)
	if st.ClinicID == "" {
		return nil
	}
	cli, err := s.dnsClient()
	if err != nil {
		return err
	}
	_, err = cli.UpdateAddress(ctx, st.ClinicID, ipv4)
	now := s.now()
	if err != nil {
		st.LastError, st.LastErrorAt = err.Error(), &now
		var se *dnsclient.Error
		st.Revoked = errors.As(err, &se) && se.Revoked()
		_ = apply.WritePublicState(s.Paths, st)
		return err
	}
	st.Address, st.AddressAt, st.LastError, st.LastErrorAt, st.Revoked = ipv4, &now, "", nil, false
	return apply.WritePublicState(s.Paths, st)
}

// resendAfter is the age of the last update after which the agent sends the same address again
// (services/dns/API.md section 5.3: each 6 hours).
const resendAfter = 6 * time.Hour

// PublicUpdateResult is the result of "care-pi tls public update-address".
type PublicUpdateResult struct {
	Address string `json:"address"`
	Sent    bool   `json:"sent"`
	// Applied is true when the command ran apply: the certificate came, so port 80 sends to the public name now.
	Applied bool `json:"applied"`
}

// PublicUpdate is the work of care-dns-update.service: it sends the address when it changed, and it runs apply
// when the certificate came after the enrol job (the redirect of port 80).
func (s *Service) PublicUpdate(ctx context.Context, out func(string)) (*PublicUpdateResult, error) {
	cfg, err := s.Config()
	if err != nil {
		return nil, err
	}
	host := render.PublicHost(cfg)
	if host == "" {
		return nil, problem.New(problem.StateConflict, "The public name is off.")
	}
	st := apply.ReadPublicState(s.Paths)
	res := &PublicUpdateResult{}
	if ips := s.Addresses(); len(ips) > 0 {
		res.Address = ips[0]
		stale := st.AddressAt == nil || s.now().Sub(*st.AddressAt) > resendAfter
		if st.Revoked {
			return res, problem.New(problem.DNSServiceRejected, "The DNS service revoked the registration of "+st.ClinicID+". Turn the public name off and on again for a new name.")
		}
		if st.Address != ips[0] || stale {
			if err := s.UpdateDNSAddress(ctx, ips[0]); err != nil {
				return res, dnsProblem(err)
			}
			res.Sent = true
			out("sent the address " + ips[0] + " to the DNS service")
		}
	}
	// An expired or a staging certificate does not count: apply would render no redirect, and the timer would apply
	// each 5 minutes.
	if ca.ValidCert(s.Paths, host, s.now()) != nil && !s.port80Public(host) {
		out("the certificate of " + host + " exists; apply sends port 80 to it")
		release, err := s.Jobs.AcquireLock(ctx)
		if err != nil {
			return res, err
		}
		defer release()
		o := s.applyOptions(apply.Options{Reason: "public certificate issued", LockHeld: true, Out: writerFunc(out)})
		if _, err := apply.Run(ctx, o); err != nil {
			return res, applyProblem(err)
		}
		res.Applied = true
	}
	return res, nil
}

type writerFunc func(string)

func (w writerFunc) Write(b []byte) (int, error) {
	for _, l := range strings.Split(strings.TrimRight(string(b), "\n"), "\n") {
		if l != "" {
			w(l)
		}
	}
	return len(b), nil
}

// port80Public reports whether the rendered Caddyfile sends port 80 to the public name.
func (s *Service) port80Public(host string) bool {
	data, err := os.ReadFile(s.Paths.Abs(paths.RenderedCaddy + "/Caddyfile"))
	return err == nil && strings.Contains(string(data), "redir https://"+host+"{uri} 302")
}

// PublicRotate gets new acme-dns credentials from the DNS service (and, with newKey, a new signing key), writes
// secrets/acme_dns.json, and applies: care-caddy restarts with the new podman secret (services/dns/API.md 5.4).
func (s *Service) PublicRotate(ctx context.Context, newKey bool, out func(string)) error {
	st := apply.ReadPublicState(s.Paths)
	if st.ClinicID == "" {
		return problem.New(problem.StateConflict, "The public name is off.")
	}
	release, err := s.Jobs.AcquireLock(ctx)
	if err != nil {
		return err
	}
	defer release()
	cli, err := s.dnsClient()
	if err != nil {
		return err
	}
	pending := s.Paths.Abs(paths.DNSClientPendingKeyFile)
	if cli.Pending != nil {
		// An earlier rotation lost its answer. A signed request finds the key that the service has (and promotes the
		// pending key when the service has it); then the pending key of that rotation is not necessary any more.
		if _, err := cli.Status(ctx, st.ClinicID); err != nil {
			return dnsProblem(err)
		}
		if cli.Pending != nil {
			out("the earlier rotation did not reach the DNS service; its pending key is deleted")
			cli.Pending = nil
			if err := os.Remove(pending); err != nil && !errors.Is(err, fs.ErrNotExist) {
				return err
			}
		} else {
			out("the DNS service has the key of the earlier rotation; it is the key now")
		}
	}
	var nk ed25519.PrivateKey
	if newKey {
		if _, nk, err = ed25519.GenerateKey(rand.Reader); err != nil {
			return err
		}
		// Store the new key before the request: when the answer is lost, the service can have the new key, and the
		// old key then does not work (services/dns/API.md 5.4). Each later request tries the pending key.
		if err := dnsclient.WriteKey(pending, nk); err != nil {
			return fmt.Errorf("cannot write the pending key: %w", err)
		}
		cli.Pending = nk
	}
	reg, err := cli.Rotate(ctx, st.ClinicID, nk)
	if err != nil {
		var se *dnsclient.Error
		if nk != nil && errors.As(err, &se) && !se.Unauthorized() {
			// The service answered and refused the rotation: it keeps the old key.
			_ = os.Remove(pending)
		}
		if nk != nil && !errors.As(err, &se) {
			out("the DNS service did not answer; the new key stays in " + paths.DNSClientPendingKeyFile + " and the next request tries it. Run the command again later")
		}
		return dnsProblem(err)
	}
	if err := dnsclient.WriteCredentials(s.Paths.Abs(paths.ACMEDNSFile), reg.ACME.Credentials()); err != nil {
		return err
	}
	if nk != nil {
		if err := dnsclient.WriteKey(s.Paths.Abs(paths.DNSClientKeyFile), nk); err != nil {
			return fmt.Errorf("the service has the new key, but the agent cannot write it (%w); the key is in %s", err, paths.DNSClientPendingKeyFile)
		}
		if err := os.Remove(pending); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
	}
	out("new acme-dns credentials for " + reg.FullDomain)
	o := s.applyOptions(apply.Options{Reason: "acme-dns credentials rotated", LockHeld: true, Out: writerFunc(out)})
	_, err = apply.Run(ctx, o)
	return applyProblem(err)
}

// ---- Tailscale ----

// TailscaleRequest is PUT /tls/tailscale (schema TailscaleRequest).
type TailscaleRequest struct {
	Enabled bool
	// AuthKey is the auth key; "" starts the login in the browser (the login URL in GET /tls).
	AuthKey string
	Reason  string
}

// StartTailscale starts the job tls-tailscale-enrol: enable or disable the Tailscale mode (S37: opt-in).
func (s *Service) StartTailscale(ctx context.Context, req TailscaleRequest, source string) (*jobs.Job, error) {
	if err := s.requireReady(); err != nil {
		return nil, err
	}
	cfg, err := s.Config()
	if err != nil {
		return nil, err
	}
	if req.AuthKey != "" && !strings.HasPrefix(req.AuthKey, "tskey-") {
		return nil, problem.Invalid("/auth_key", "an auth key of Tailscale starts with tskey-", "pattern")
	}
	if !req.Enabled && !cfg.TLS.Tailscale.Enabled {
		st, _ := readJSONFile[tailscaleState](s.Paths.Abs(paths.TailscaleState))
		if st.BackendState == "" {
			return nil, problem.New(problem.StateConflict, "Tailscale is off.")
		}
	}
	return s.Jobs.Start(ctx, jobs.Spec{Kind: jobs.KindTLSTailscaleEnrol, Source: source, Reason: req.Reason, Lock: true, Cancellable: true},
		func(ctx context.Context, rec *jobs.Recorder) (any, error) {
			if req.Enabled {
				return s.tailscaleEnable(ctx, rec, req, cfg.Identity.Hostname)
			}
			return s.tailscaleDisable(ctx, rec)
		})
}

func (s *Service) saveTailscale(st *tailscale.Status) {
	now := s.now()
	ts := tailscaleState{BackendState: st.BackendState, DNSName: st.DNSName, Tailnet: st.Tailnet, IPv4: st.IPv4, UpdatedAt: &now}
	if st.BackendState == tailscale.StateNeedsLogin {
		ts.LoginURL = st.AuthURL
	}
	_ = writeJSONFile(s.Paths.Abs(paths.TailscaleState), ts)
}

func (s *Service) tailscaleEnable(ctx context.Context, rec *jobs.Recorder, req TailscaleRequest, hostname string) (any, error) {
	tc := s.tailscaleClient()
	if err := step(rec, "tailscale-install: install tailscale from the repository of Tailscale", func() error {
		did, err := tc.Install(ctx)
		if err != nil {
			return problem.New(problem.TailscaleInstallFailed, err.Error())
		}
		if did {
			rec.Logf("installed tailscale")
		} else {
			rec.Logf("tailscale is installed")
		}
		return nil
	}); err != nil {
		return nil, err
	}
	cur, _ := tc.Status(ctx)
	if cur == nil || cur.BackendState != tailscale.StateRunning {
		if err := step(rec, "tailscale-up: connect to the tailnet", func() error {
			if req.AuthKey != "" {
				return tc.UpWithKey(ctx, hostname, req.AuthKey)
			}
			return tc.StartLogin(ctx, hostname)
		}); err != nil {
			return nil, err
		}
	}
	timeout := s.LoginWait
	if timeout == 0 {
		timeout = defaultLogin
	}
	var st *tailscale.Status
	if err := step(rec, "tailscale-login: wait for the login", func() error {
		said := false
		var err error
		st, err = tc.WaitFor(ctx, timeout, func(x *tailscale.Status) {
			s.saveTailscale(x)
			if x.BackendState == tailscale.StateNeedsLogin && x.AuthURL != "" && !said {
				// The URL is in GET /tls only: it lets each person who has it add the device to a tailnet.
				rec.Logf("open the login URL in Settings > Connectivity, or run: care-pi tls tailscale status")
				said = true
			}
		}, func(x *tailscale.Status) bool { return x.BackendState == tailscale.StateRunning && x.DNSName != "" })
		if err != nil {
			if st != nil && st.BackendState == tailscale.StateNeedsLogin {
				return problem.New(problem.TailscaleLoginRequired, "Nobody logged in within "+timeout.String()+". Turn Tailscale on again for a new login URL.")
			}
			return err
		}
		if !st.HTTPSEnabled() {
			return problem.New(problem.TailscaleHTTPSDisabled, "Turn on MagicDNS and HTTPS Certificates in the DNS page of the Tailscale admin console, then turn Tailscale on again.")
		}
		rec.Logf("the device is %s in the tailnet %s", st.DNSName, st.Tailnet)
		return nil
	}); err != nil {
		return nil, err
	}
	if err := step(rec, "write-config: tls.tailscale", func() error {
		return s.updateConfig(func(doc map[string]any) {
			ts := section(section(doc, "tls"), "tailscale")
			ts["enabled"] = true
			ts["hostname"] = st.DNSName
		})
	}); err != nil {
		return nil, err
	}
	if err := s.runApply(ctx, rec, "tailscale on"); err != nil {
		return nil, err
	}
	return jobs.TLSTailscaleResult{Kind: jobs.KindTLSTailscaleEnrol, Enabled: true, Hostname: strPtr(st.DNSName), Tailnet: strPtr(st.Tailnet)}, nil
}

func (s *Service) tailscaleDisable(ctx context.Context, rec *jobs.Recorder) (any, error) {
	tc := s.tailscaleClient()
	if err := step(rec, "write-config: tls.tailscale off", func() error {
		return s.updateConfig(func(doc map[string]any) {
			ts := section(section(doc, "tls"), "tailscale")
			ts["enabled"] = false
			delete(ts, "hostname")
		})
	}); err != nil {
		return nil, err
	}
	if err := s.runApply(ctx, rec, "tailscale off"); err != nil {
		return nil, err
	}
	if st, _ := tc.Status(ctx); st != nil && st.Installed {
		if err := step(rec, "tailscale-down: disconnect from the tailnet", func() error { return tc.Down(ctx) }); err != nil {
			return nil, err
		}
		if err := step(rec, "tailscale-logout: remove the device from the tailnet", func() error { return tc.Logout(ctx) }); err != nil {
			return nil, err
		}
	}
	_ = os.Remove(s.Paths.Abs(paths.TailscaleState))
	return jobs.TLSTailscaleResult{Kind: jobs.KindTLSTailscaleEnrol, Enabled: false}, nil
}

// ---- Wi-Fi ----

// WiFiRequest is PUT /network/wifi (schema WifiRequest).
type WiFiRequest struct {
	SSID     string
	PSK      string
	Country  string
	Failover bool
	Reason   string
}

// wifiSecret is secrets/wifi.json. NetworkManager keeps its own copy in its keyfile (0600, root).
type wifiSecret struct {
	SSID string `json:"ssid"`
	PSK  string `json:"psk"`
}

// wifiState is state/wifi.json.
type wifiState struct {
	Fingerprint string     `json:"fingerprint"`
	AppliedAt   *time.Time `json:"applied_at,omitempty"`
	Error       string     `json:"error,omitempty"`
}

// StartWiFi starts the job network-wifi that adds or changes the Wi-Fi connection.
func (s *Service) StartWiFi(ctx context.Context, req WiFiRequest, source string) (*jobs.Job, error) {
	if err := s.requireReady(); err != nil {
		return nil, err
	}
	spec := wifi.Spec{SSID: req.SSID, PSK: req.PSK, Country: req.Country, Failover: req.Failover}
	if spec.Country == "" {
		if cfg, err := s.Config(); err == nil && cfg.Network.WiFi != nil {
			spec.Country = cfg.Network.WiFi.Country
		}
	}
	if err := spec.Validate(); err != nil {
		return nil, problem.New(problem.InvalidRequest, err.Error())
	}
	return s.Jobs.Start(ctx, jobs.Spec{Kind: jobs.KindNetworkWiFi, Source: source, Reason: req.Reason, Lock: true},
		func(ctx context.Context, rec *jobs.Recorder) (any, error) {
			return s.wifiSet(ctx, rec, spec)
		})
}

func (s *Service) wifiSet(ctx context.Context, rec *jobs.Recorder, spec wifi.Spec) (any, error) {
	wc := s.wifiClient()
	if err := writeJSONFile(s.Paths.Abs(paths.WiFiSecretFile), wifiSecret{SSID: spec.SSID, PSK: spec.PSK}); err != nil {
		return nil, err
	}
	err := wc.Apply(ctx, spec, rec.Step)
	switch {
	case errors.Is(err, wifi.ErrAuth):
		return nil, problem.New(problem.WiFiAuthFailed, "Check the passphrase of "+spec.SSID+". "+err.Error())
	case errors.Is(err, wifi.ErrNoDevice):
		return nil, problem.New(problem.WiFiNoDevice, err.Error())
	case err != nil:
		return nil, problem.New(problem.WiFiFailed, err.Error())
	}
	dev, _ := wc.Device(ctx)
	if err := step(rec, "wifi-verify: check the routes", func() error {
		err := wc.WaitRoutes(ctx, dev, spec.Failover, 30*time.Second, s.pollInterval())
		if err != nil {
			rec.Logf("warning: %v", err)
		}
		return nil
	}); err != nil {
		return nil, err
	}
	if err := step(rec, "write-config: network.wifi", func() error {
		return s.updateConfig(func(doc map[string]any) {
			w := map[string]any{"ssid": spec.SSID, "failover": spec.Failover}
			if spec.Country != "" {
				w["country"] = spec.Country
			}
			section(doc, "network")["wifi"] = w
		})
	}); err != nil {
		return nil, err
	}
	now := s.now()
	_ = writeJSONFile(s.Paths.Abs(paths.WiFiState), wifiState{Fingerprint: spec.Fingerprint(), AppliedAt: &now})
	// The pod publishes on the new address too (a full cycle when the address is new).
	_, _ = netinfo.WaitLANIPv4(ctx, s.IPs, 15*time.Second, s.pollInterval(), nil)
	if err := s.runApply(ctx, rec, "wi-fi changed"); err != nil {
		return nil, err
	}
	res := jobs.WiFiResult{Kind: jobs.KindNetworkWiFi, SSID: strPtr(spec.SSID)}
	if l, err := wc.LinkState(ctx); err == nil {
		res.Connected, res.Address = l.Connected, l.Address
	}
	return res, nil
}

// StartWiFiRemove starts the job network-wifi that removes the Wi-Fi connection of care-pi.
func (s *Service) StartWiFiRemove(ctx context.Context, reason, source string) (*jobs.Job, error) {
	if err := s.requireReady(); err != nil {
		return nil, err
	}
	return s.Jobs.Start(ctx, jobs.Spec{Kind: jobs.KindNetworkWiFi, Source: source, Reason: reason, Lock: true},
		func(ctx context.Context, rec *jobs.Recorder) (any, error) {
			if err := step(rec, "wifi-remove: delete the connection "+wifi.ConnName, func() error {
				return s.wifiClient().Remove(ctx)
			}); err != nil {
				return nil, problem.New(problem.WiFiFailed, err.Error())
			}
			for _, f := range []string{paths.WiFiSecretFile, paths.WiFiState} {
				if err := os.Remove(s.Paths.Abs(f)); err != nil && !errors.Is(err, fs.ErrNotExist) {
					return nil, err
				}
			}
			if err := step(rec, "write-config: network.wifi off", func() error {
				return s.updateConfig(func(doc map[string]any) { delete(section(doc, "network"), "wifi") })
			}); err != nil {
				return nil, err
			}
			if err := s.runApply(ctx, rec, "wi-fi removed"); err != nil {
				return nil, err
			}
			return jobs.WiFiResult{Kind: jobs.KindNetworkWiFi}, nil
		})
}

// WiFiScan lists the Wi-Fi networks in range (GET /network/wifi/scan).
type WiFiScan struct {
	Device   *string        `json:"device"`
	Networks []wifi.Network `json:"networks"`
}

// ScanWiFi scans for networks.
func (s *Service) ScanWiFi(ctx context.Context) (*WiFiScan, error) {
	wc := s.wifiClient()
	dev, err := wc.Device(ctx)
	if err != nil {
		return nil, problem.New(problem.WiFiFailed, err.Error())
	}
	if dev == "" {
		return nil, problem.New(problem.WiFiNoDevice, wifi.ErrNoDevice.Error())
	}
	nets, err := wc.Scan(ctx)
	if err != nil {
		return nil, problem.New(problem.WiFiFailed, err.Error())
	}
	if nets == nil {
		nets = []wifi.Network{}
	}
	return &WiFiScan{Device: &dev, Networks: nets}, nil
}

// WiFiStatus is the Wi-Fi state in GET /network (schema WifiState).
type WiFiStatus struct {
	SSID         *string        `json:"ssid"`
	Country      *string        `json:"country"`
	Failover     bool           `json:"failover"`
	Connected    bool           `json:"connected"`
	Device       *string        `json:"device"`
	Radio        bool           `json:"radio"`
	ActiveSSID   *string        `json:"active_ssid"`
	Signal       *int           `json:"signal"`
	Address      *string        `json:"address"`
	DefaultRoute bool           `json:"default_route"`
	Profiles     []wifi.Profile `json:"profiles"`
}

func (s *Service) wifiStatus(ctx context.Context, w *wifiCfg) *WiFiStatus {
	wc := s.wifiClient()
	l, err := wc.LinkState(ctx)
	if err != nil || l.Device == "" {
		if w == nil {
			return nil
		}
		return &WiFiStatus{SSID: strPtr(w.SSID), Country: strPtr(w.Country), Failover: w.Failover, Profiles: []wifi.Profile{}}
	}
	out := &WiFiStatus{Device: strPtr(l.Device), Radio: l.Radio, ActiveSSID: l.SSID, Signal: l.Signal, Address: l.Address,
		DefaultRoute: l.DefaultRoute, Profiles: []wifi.Profile{}}
	if w != nil {
		out.SSID, out.Country, out.Failover = strPtr(w.SSID), strPtr(w.Country), w.Failover
		out.Connected = l.Connected && l.SSID != nil && *l.SSID == w.SSID
	}
	if ps, err := wc.Profiles(ctx); err == nil && ps != nil {
		out.Profiles = ps
	}
	return out
}

type wifiCfg struct {
	SSID     string
	Country  string
	Failover bool
}
