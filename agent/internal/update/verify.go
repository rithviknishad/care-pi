package update

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"time"

	"github.com/rithviknishad/care-pi/agent/internal/ca"
	"github.com/rithviknishad/care-pi/agent/internal/fe"
	"github.com/rithviknishad/care-pi/agent/internal/paths"
	"github.com/rithviknishad/care-pi/agent/internal/plugcfg"
)

// VerifyTarget is what the step verify checks.
type VerifyTarget struct {
	// Canonical is the first host name; Address is the LAN address that Caddy publishes.
	Canonical string
	Address   string
	// FEBuildID is the FE build that fe/current must point at ("" without an FE build).
	FEBuildID string
	// Rows are the plug_config rows of the FE plugs that are on.
	Rows []plugcfg.Row
}

// Verifier checks Care after a cutover (docs/solutions.md, section 8.1, step verify).
type Verifier interface {
	Verify(ctx context.Context, t VerifyTarget) error
}

// HTTPVerifier checks Care through Caddy: /ping/ and /health/ give 200, fe/current is the target build,
// /api/v1/plug_config/ lists each FE plug that is on with its meta.url, and each meta.url of the
// device gives 200. It trusts the root of the local CA of Caddy.
type HTTPVerifier struct {
	Paths   paths.Paths
	Timeout time.Duration
}

// Verify implements Verifier.
func (v HTTPVerifier) Verify(ctx context.Context, t VerifyTarget) error {
	if t.FEBuildID != "" && fe.Current(v.Paths) != t.FEBuildID {
		return fmt.Errorf("fe/current is %q, not the FE build %s", fe.Current(v.Paths), t.FEBuildID)
	}
	if t.Canonical == "" || t.Address == "" {
		return fmt.Errorf("no host name or LAN address to check")
	}
	client, err := v.client(t)
	if err != nil {
		return err
	}
	get := func(path string) (int, []byte, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://"+t.Canonical+path, nil)
		if err != nil {
			return 0, nil, err
		}
		resp, err := client.Do(req)
		if err != nil {
			return 0, nil, err
		}
		defer func() { _ = resp.Body.Close() }()
		data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		return resp.StatusCode, data, err
	}
	for _, p := range []string{"/ping/", "/health/"} {
		code, _, err := get(p)
		if err != nil {
			return fmt.Errorf("GET %s: %w", p, err)
		}
		if code != http.StatusOK {
			return fmt.Errorf("GET %s gave HTTP %d", p, code)
		}
	}
	code, body, err := get("/api/v1/plug_config/")
	if err != nil || code != http.StatusOK {
		return fmt.Errorf("GET /api/v1/plug_config/ gave HTTP %d: %v", code, err)
	}
	var list struct {
		Configs []plugcfg.Row `json:"configs"`
	}
	if err := json.Unmarshal(body, &list); err != nil {
		return fmt.Errorf("GET /api/v1/plug_config/: %w", err)
	}
	have := map[string]string{}
	for _, r := range list.Configs {
		have[r.Slug] = r.MetaURL()
	}
	for _, r := range t.Rows {
		if have[r.Slug] != r.MetaURL() {
			return fmt.Errorf("the plug_config row %s has meta.url %q, not %q", r.Slug, have[r.Slug], r.MetaURL())
		}
		u, err := url.Parse(r.MetaURL())
		if err != nil || u.Host != t.Canonical {
			continue // an external bundle: the browser loads it, not the device
		}
		code, _, err := get(u.Path)
		if err != nil || code != http.StatusOK {
			return fmt.Errorf("GET %s gave HTTP %d: %v", u.Path, code, err)
		}
	}
	return nil
}

func (v HTTPVerifier) client(t VerifyTarget) (*http.Client, error) {
	// The canonical host has a certificate of the local CA, or (Phase 6, the public name) of Let's Encrypt:
	// trust the system roots and the root of the local CA.
	pool, err := x509.SystemCertPool()
	if err != nil || pool == nil {
		pool = x509.NewCertPool()
	}
	pem, err := ca.TrustedPEM(v.Paths)
	if err != nil {
		return nil, fmt.Errorf("cannot read the root CA: %w", err)
	}
	pool.AppendCertsFromPEM(pem)
	timeout := v.Timeout
	if timeout == 0 {
		timeout = 30 * time.Second
	}
	dialer := &net.Dialer{Timeout: timeout}
	tr := &http.Transport{
		TLSClientConfig: &tls.Config{RootCAs: pool, ServerName: t.Canonical, MinVersion: tls.VersionTLS12},
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			_, port, _ := net.SplitHostPort(addr)
			return dialer.DialContext(ctx, network, net.JoinHostPort(t.Address, port))
		},
	}
	return &http.Client{Transport: tr, Timeout: timeout, CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}, nil
}

// FuncVerifier adapts a function (tests, the dev host).
type FuncVerifier func(ctx context.Context, t VerifyTarget) error

// Verify implements Verifier.
func (f FuncVerifier) Verify(ctx context.Context, t VerifyTarget) error { return f(ctx, t) }
