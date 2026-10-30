package config

import (
	"bytes"
	"errors"
	"fmt"
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
func (c *Config) check() []string {
	var out []string
	seen := map[string]bool{}
	for i, p := range c.Solution.Custom.Plugs {
		if seen[p.ID] {
			out = append(out, fmt.Sprintf("/solution/custom/plugs/%d/id: plug %q occurs more than once", i, p.ID))
		}
		seen[p.ID] = true
	}
	dests := map[string]bool{}
	for i, d := range c.Backups.Destinations {
		if dests[d.ID] {
			out = append(out, fmt.Sprintf("/backups/destinations/%d/id: destination %q occurs more than once", i, d.ID))
		}
		dests[d.ID] = true
	}
	return out
}

// CheckImplemented returns an error for each valid setting that this version cannot apply.
// apply calls it. render and config validate do not.
func (c *Config) CheckImplemented() error {
	var errs []error
	if !c.TLS.LocalCA.Enabled {
		errs = append(errs, fmt.Errorf("tls.local_ca.enabled: false is %w; Phase 1 serves only the local CA mode", ErrNotImplemented))
	}
	if c.TLS.Public.Enabled {
		errs = append(errs, fmt.Errorf("tls.public is %w (Phase 6); set tls.public.enabled to false", ErrNotImplemented))
	}
	if c.TLS.Tailscale.Enabled {
		errs = append(errs, fmt.Errorf("tls.tailscale is %w (Phase 6); set tls.tailscale.enabled to false", ErrNotImplemented))
	}
	if c.Solution.Track == "pinned" {
		errs = append(errs, fmt.Errorf("solution.track pinned is %w (Phase 4); use stable or custom", ErrNotImplemented))
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
