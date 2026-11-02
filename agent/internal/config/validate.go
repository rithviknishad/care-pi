package config

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"text/template"
)

// ReservedBackendEnv lists the backend variables that care-pi sets.
// The operator cannot override them in env.backend. The schema has the same list.
var ReservedBackendEnv = []string{
	"ADDITIONAL_PLUGS",
	"BUCKET_ENDPOINT",
	"BUCKET_EXTERNAL_ENDPOINT",
	"BUCKET_KEY",
	"BUCKET_PROVIDER",
	"BUCKET_SECRET",
	"CELERY_BROKER_URL",
	"CELERY_WORKER_CONCURRENCY",
	"CSRF_TRUSTED_ORIGINS",
	"DATABASE_URL",
	"DJANGO_ALLOWED_HOSTS",
	"DJANGO_SECRET_KEY",
	"GUNICORN_WORKERS",
	"JWKS_BASE64",
	"POSTGRES_DB",
	"POSTGRES_HOST",
	"POSTGRES_PASSWORD",
	"POSTGRES_PORT",
	"POSTGRES_USER",
	"REDIS_URL",
}

// ReservedFrontendEnv lists the frontend variables that care-pi sets.
// REACT_CARE_API_URL is always empty: the API has the same origin as the FE.
var ReservedFrontendEnv = []string{"REACT_CARE_API_URL"}

// ErrNotImplemented marks a valid setting that this version cannot apply yet.
var ErrNotImplemented = errors.New("not implemented in this version")

// check runs the rules that JSON Schema cannot express.
func (c *Config) check() []Detail {
	var out []Detail
	seen := map[string]bool{}
	for i, p := range c.Solution.Custom.Plugs {
		if seen[p.ID] {
			out = append(out, Detail{Pointer: fmt.Sprintf("/solution/custom/plugs/%d/id", i), Message: fmt.Sprintf("plug %q occurs more than once", p.ID), Keyword: "uniqueItems"})
		}
		seen[p.ID] = true
	}
	// Without a control server of its own (Headscale, O4), the MagicDNS name is a name of Tailscale.
	if ts := c.TLS.Tailscale; ts.Hostname != "" && ts.ControlURL == "" && !strings.HasSuffix(ts.Hostname, ".ts.net") {
		out = append(out, Detail{Pointer: "/tls/tailscale/hostname", Message: "must end in .ts.net when tls.tailscale.control_url is not set", Keyword: "pattern"})
	}
	dests := map[string]bool{}
	for i, d := range c.Backups.Destinations {
		if dests[d.ID] {
			out = append(out, Detail{Pointer: fmt.Sprintf("/backups/destinations/%d/id", i), Message: fmt.Sprintf("destination %q occurs more than once", d.ID), Keyword: "uniqueItems"})
		}
		dests[d.ID] = true
	}
	return out
}

// CheckImplemented returns an error for each valid setting that this version cannot apply.
// apply calls it. render and config validate do not.
func (c *Config) CheckImplemented() error {
	var errs []error
	// The local CA mode is the offline fallback of the public mode (S36.3) and serves the LAN addresses.
	if !c.TLS.LocalCA.Enabled {
		errs = append(errs, fmt.Errorf("tls.local_ca.enabled: false is %w; the local CA mode serves the LAN addresses and the offline fallback (S36)", ErrNotImplemented))
	}
	// Phase 4 implements solution.track pinned and solution.plugs (docs/solutions.md, sections 7 and 12).
	if c.Solution.Track == "pinned" && c.Solution.Pin == "" {
		errs = append(errs, fmt.Errorf("solution.track pinned needs solution.pin (a lock version YYYY.MM.N)"))
	}
	return errors.Join(errs...)
}

var defaultTemplate = template.Must(template.New("config").Parse(`# care-pi desired state. Edit this file, then run "care-pi apply".
# Reference: docs/config.md
version: 1
identity:
  display_name: {{printf "%q" .DisplayName}}
  hostname: {{.Hostname}}                # The host name is {{.Hostname}}.local.
solution:
  name: {{.Solution}}
  track: stable
size:
  preset: {{.Preset}}            # solo | small-clinic | busy-counter | custom
tls:
  local_ca: {enabled: true}
clock:
  policy: ntp
backups:
  wal_archive_timeout_min: 5
env:
  backend: {}
  frontend: {}
fe:
  provider: {{.FEProvider}}            # local | artifact | none. none serves only the setup page (docs/fe.md).
uploads:
  max_body_mb: 200
`))

// DefaultYAML returns the commented config that bootstrap writes.
// An empty feProvider gives DefaultFEProvider.
func DefaultYAML(displayName, hostname, preset, feProvider string) ([]byte, error) {
	if displayName == "" {
		displayName = DefaultDisplayName
	}
	if hostname == "" {
		hostname = DefaultHostname
	}
	if preset == "" {
		preset = DefaultPreset
	}
	if feProvider == "" {
		feProvider = DefaultFEProvider
	}
	if feProvider != "none" && feProvider != "artifact" && feProvider != "local" {
		return nil, fmt.Errorf("fe provider %q is not valid; use none, artifact, or local", feProvider)
	}
	if preset == "custom" {
		return nil, fmt.Errorf("bootstrap cannot write preset custom; use solo, small-clinic, or busy-counter, then edit size.custom")
	}
	var b bytes.Buffer
	err := defaultTemplate.Execute(&b, map[string]string{
		"DisplayName": displayName,
		"Hostname":    hostname,
		"Preset":      preset,
		"Solution":    DefaultSolution,
		"FEProvider":  feProvider,
	})
	if err != nil {
		return nil, err
	}
	if _, err := Parse(b.Bytes(), "default config"); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

// WithFirewallPolicy adds security.firewall.policy to a config that DefaultYAML wrote (bootstrap --firewall-policy,
// install.sh --firewall-drop). The default policy accept adds nothing.
func WithFirewallPolicy(data []byte, policy string) ([]byte, error) {
	switch policy {
	case "", DefaultFirewallPolicy:
		return data, nil
	case "drop":
	default:
		return nil, fmt.Errorf("firewall policy %q is not valid; use accept or drop", policy)
	}
	out := append(append([]byte{}, data...), []byte(`security:
  firewall:
    policy: drop      # The first apply starts a rollback timer of 90 s: run "care-pi firewall confirm".
  ssh:
    allow: true
`)...)
	if _, err := Parse(out, "default config"); err != nil {
		return nil, err
	}
	return out, nil
}
