// Package dnsclient is the client of the care-pi DNS/ACME service (services/dns/API.md, docs/connectivity.md).
//
// The service gives each clinic a public name <id>.care-pi.rithviknishad.dev, keeps its A record at the LAN
// address that the agent sends (not the public address of the clinic), and answers the ACME DNS-01 challenge
// through an acme-dns compatible API (POST /update). The agent signs each /v1 request with its Ed25519 key
// (secrets/dns_client.key): the canonical string is
//
//	CARE-PI-DNS-V1 \n METHOD \n PATH \n TIMESTAMP \n hex(SHA-256(body))
//
// in the headers X-Care-Pi-Timestamp (Unix seconds) and X-Care-Pi-Signature (standard base64).
package dnsclient

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// DefaultBaseURL is the DNS/ACME service (S38).
const DefaultBaseURL = "https://care-pi.rithviknishad.dev"

// Scheme is the first line of the canonical string.
const Scheme = "CARE-PI-DNS-V1"

// Headers of a signed request.
const (
	HeaderTimestamp    = "X-Care-Pi-Timestamp"
	HeaderSignature    = "X-Care-Pi-Signature"
	HeaderSignatureNew = "X-Care-Pi-Signature-New"
)

// Canonical returns the string that the client signs.
func Canonical(method, path, timestamp string, body []byte) string {
	sum := sha256.Sum256(body)
	return strings.Join([]string{Scheme, strings.ToUpper(method), path, timestamp, hex.EncodeToString(sum[:])}, "\n")
}

// ACME holds the acme-dns credentials of a clinic. The Caddy module acmedns reads Credentials.
type ACME struct {
	Username   string   `json:"username"`
	Password   string   `json:"password,omitempty"`
	Subdomain  string   `json:"subdomain"`
	FullDomain string   `json:"fulldomain"`
	ServerURL  string   `json:"server_url"`
	AllowFrom  []string `json:"allowfrom,omitempty"`
}

// Credentials is the file secrets/acme_dns.json: the single-account format of the Caddy module acmedns.
type Credentials struct {
	Username  string `json:"username"`
	Password  string `json:"password"`
	Subdomain string `json:"subdomain"`
	ServerURL string `json:"server_url"`
}

// Credentials returns the file content for the Caddy module.
func (a ACME) Credentials() Credentials {
	return Credentials{Username: a.Username, Password: a.Password, Subdomain: a.Subdomain, ServerURL: a.ServerURL}
}

// Registration is the answer of POST /v1/register and POST /v1/clinics/{id}/rotate.
type Registration struct {
	ID           string `json:"id"`
	FullDomain   string `json:"fulldomain"`
	ACME         ACME   `json:"acme"`
	ListingOptIn bool   `json:"listing_opt_in"`
	CreatedAt    string `json:"created_at,omitempty"`
	PubKey       string `json:"pubkey,omitempty"`
}

// Address is the answer of PUT /v1/clinics/{id}/address.
type Address struct {
	ID         string  `json:"id"`
	FullDomain string  `json:"fulldomain"`
	IPv4       *string `json:"ipv4"`
	IPv6       *string `json:"ipv6"`
	TTL        int     `json:"ttl"`
	Changed    bool    `json:"changed"`
	UpdatedAt  string  `json:"updated_at"`
}

// Status is the answer of GET /v1/clinics/{id}.
type Status struct {
	ID           string `json:"id"`
	FullDomain   string `json:"fulldomain"`
	HostnameHint string `json:"hostname_hint"`
	CreatedAt    string `json:"created_at"`
	LastSeen     string `json:"last_seen"`
	ListingOptIn bool   `json:"listing_opt_in"`
	Address      *struct {
		IPv4      *string `json:"ipv4"`
		IPv6      *string `json:"ipv6"`
		TTL       int     `json:"ttl"`
		UpdatedAt string  `json:"updated_at"`
	} `json:"address"`
	ACME ACME `json:"acme"`
}

// Error is an error answer of the service: {"error": code, "message": text, ...}.
type Error struct {
	Status     int    `json:"-"`
	Code       string `json:"error"`
	Message    string `json:"message"`
	ServerTime int64  `json:"server_time,omitempty"`
	RetryAfter time.Duration
}

func (e *Error) Error() string {
	return fmt.Sprintf("DNS service: HTTP %d %s: %s", e.Status, e.Code, e.Message)
}

// Temporary reports whether a retry can work: HTTP 429 and 5xx.
func (e *Error) Temporary() bool { return e.Status == http.StatusTooManyRequests || e.Status >= 500 }

// Revoked reports whether the clinic id is revoked or unknown (register again).
func (e *Error) Revoked() bool { return e.Status == http.StatusGone || e.Code == "unknown_clinic" }

// Unauthorized reports whether the service refused the key of the client (HTTP 401): the service has another key
// for the id, so this device cannot use the id any more.
func (e *Error) Unauthorized() bool { return e.Status == http.StatusUnauthorized }

// ErrUnreachable wraps a network error: the service did not answer.
var ErrUnreachable = errors.New("the DNS service does not answer")

// Client talks to the service.
type Client struct {
	BaseURL string
	HTTP    *http.Client
	Key     ed25519.PrivateKey
	Now     func() time.Time
	// Attempts is the number of tries of a request (default 3). Backoff is the first wait (default 5 s); it doubles.
	// API.md section 7 asks for a backoff from 30 s to 15 min for 5xx: the timer care-dns-update.timer tries
	// again each 5 minutes, so one call tries only a few times.
	Attempts int
	Backoff  time.Duration
	// Pending is the new key of a rotation whose answer can be lost (secrets/dns_client.key.pending), or nil. The
	// service then has the new key, and refuses Key with 401 bad_signature. Such a request is signed again once with
	// Pending; when the service takes it, Pending becomes Key and Promote stores it.
	Pending ed25519.PrivateKey
	Promote func(ed25519.PrivateKey) error
	// skew is the server time minus the local time, from a stale_timestamp answer (a clock without NTP).
	skew time.Duration
}

func (c *Client) base() string {
	if c.BaseURL == "" {
		return DefaultBaseURL
	}
	return strings.TrimSuffix(c.BaseURL, "/")
}

func (c *Client) now() time.Time {
	if c.Now == nil {
		return time.Now()
	}
	return c.Now()
}

func (c *Client) httpClient() *http.Client {
	if c.HTTP == nil {
		return &http.Client{Timeout: 30 * time.Second}
	}
	return c.HTTP
}

// PublicKey returns the public key in standard base64 (the pubkey of the registration).
func (c *Client) PublicKey() string {
	return base64.StdEncoding.EncodeToString(c.Key.Public().(ed25519.PublicKey))
}

type request struct {
	method string
	path   string
	body   any
	signed bool
	// newKey signs the request a second time (rotation of the key).
	newKey ed25519.PrivateKey
}

// do sends the request with retries and decodes a 2xx answer into out.
func (c *Client) do(ctx context.Context, r request, out any) error {
	attempts := c.Attempts
	if attempts <= 0 {
		attempts = 3
	}
	wait := c.Backoff
	if wait <= 0 {
		wait = 5 * time.Second
	}
	var body []byte
	if r.body != nil {
		var err error
		if body, err = json.Marshal(r.body); err != nil {
			return err
		}
	}
	skewFixed := false
	var last error
	for i := 0; i < attempts; i++ {
		if i > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(wait):
			}
			wait *= 2
		}
		err := c.once(ctx, r, body, out)
		if err == nil {
			return nil
		}
		last = err
		var se *Error
		if errors.As(err, &se) {
			if se.Code == "stale_timestamp" && se.ServerTime > 0 && !skewFixed {
				// The clock of the device is wrong (no NTP yet): sign with the time of the server.
				c.skew = time.Unix(se.ServerTime, 0).Sub(c.now())
				skewFixed = true
				i--
				continue
			}
			if se.Status == http.StatusUnauthorized && se.Code == "bad_signature" && c.Pending != nil && r.signed && r.path != "/v1/register" {
				return c.withPending(ctx, r, body, out, err)
			}
			if !se.Temporary() {
				return err
			}
			if se.RetryAfter > wait {
				wait = se.RetryAfter
			}
			continue
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
	}
	return last
}

// withPending signs the request again with the pending key of a rotation (the answer of the rotation was lost, and the
// service has the new key). When the service takes it, the pending key becomes the key. Else refused is the error.
// A rotation to the pending key itself (a retry of Rotate after the lost answer) works too: the service checks both
// signatures with the new key, and gives new acme-dns credentials again.
func (c *Client) withPending(ctx context.Context, r request, body []byte, out any, refused error) error {
	old := c.Key
	c.Key = c.Pending
	if err := c.once(ctx, r, body, out); err != nil {
		c.Key = old
		var se *Error
		if errors.As(err, &se) && se.Unauthorized() {
			return refused
		}
		return err
	}
	c.Pending = nil
	if c.Promote != nil {
		if err := c.Promote(c.Key); err != nil {
			return fmt.Errorf("the DNS service has the new key, but the client cannot store it: %w", err)
		}
	}
	return nil
}

func (c *Client) once(ctx context.Context, r request, body []byte, out any) error {
	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, r.method, c.base()+r.path, rd)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if r.signed {
		if c.Key == nil {
			return errors.New("dnsclient: no key")
		}
		ts := strconv.FormatInt(c.now().Add(c.skew).Unix(), 10)
		canon := Canonical(r.method, r.path, ts, body)
		req.Header.Set(HeaderTimestamp, ts)
		req.Header.Set(HeaderSignature, base64.StdEncoding.EncodeToString(ed25519.Sign(c.Key, []byte(canon))))
		if r.newKey != nil {
			req.Header.Set(HeaderSignatureNew, base64.StdEncoding.EncodeToString(ed25519.Sign(r.newKey, []byte(canon))))
		}
	}
	res, err := c.httpClient().Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("%w: %v", ErrUnreachable, err)
	}
	defer func() { _ = res.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("%w: %v", ErrUnreachable, err)
	}
	if res.StatusCode < 200 || res.StatusCode > 299 {
		e := &Error{Status: res.StatusCode}
		if json.Unmarshal(data, e) != nil || e.Code == "" {
			e.Code, e.Message = "http_"+strconv.Itoa(res.StatusCode), strings.TrimSpace(string(data))
		}
		if s, err := strconv.Atoi(res.Header.Get("Retry-After")); err == nil && s > 0 {
			e.RetryAfter = time.Duration(s) * time.Second
		}
		return e
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("DNS service: the answer of %s %s is not valid JSON: %w", r.method, r.path, err)
	}
	return nil
}

// Health checks GET /health.
func (c *Client) Health(ctx context.Context) error {
	return c.do(ctx, request{method: http.MethodGet, path: "/health"}, nil)
}

// registerBody is the body of POST /v1/register in the field order of API.md section 3.6.
type registerBody struct {
	Hint    string `json:"hostname_hint"`
	PubKey  string `json:"pubkey"`
	Nonce   string `json:"nonce"`
	Listing bool   `json:"listing_opt_in"`
}

// addressBody is the body of PUT /v1/clinics/{id}/address. The request replaces both records: the agent publishes
// no IPv6 address yet (SP8), so ipv6 is null and the service has no AAAA record.
type addressBody struct {
	IPv4 *string `json:"ipv4"`
	IPv6 *string `json:"ipv6"`
}

// Register gets a nonce and registers the public key of the client. hint is the hostname hint (1 to 63 characters).
func (c *Client) Register(ctx context.Context, hint string, listing bool) (*Registration, error) {
	var n struct {
		Nonce string `json:"nonce"`
	}
	if err := c.do(ctx, request{method: http.MethodGet, path: "/v1/nonce"}, &n); err != nil {
		return nil, err
	}
	body := registerBody{Hint: hint, PubKey: c.PublicKey(), Nonce: n.Nonce, Listing: listing}
	var reg Registration
	// One try only: the nonce is single-use. The caller starts the enrol again.
	c2 := *c
	c2.Attempts = 1
	if err := c2.do(ctx, request{method: http.MethodPost, path: "/v1/register", body: body, signed: true}, &reg); err != nil {
		c.skew = c2.skew
		return nil, err
	}
	if reg.ID == "" || reg.ACME.Username == "" || reg.ACME.Password == "" {
		return nil, errors.New("DNS service: the registration has no id or no credentials")
	}
	return &reg, nil
}

// UpdateAddress sends the LAN IPv4 address of the clinic name.
func (c *Client) UpdateAddress(ctx context.Context, id, ipv4 string) (*Address, error) {
	var out Address
	err := c.do(ctx, request{method: http.MethodPut, path: "/v1/clinics/" + id + "/address", body: addressBody{IPv4: &ipv4}, signed: true}, &out)
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// Status reads GET /v1/clinics/{id}.
func (c *Client) Status(ctx context.Context, id string) (*Status, error) {
	var out Status
	if err := c.do(ctx, request{method: http.MethodGet, path: "/v1/clinics/" + id, signed: true}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Rotate gets new acme-dns credentials. A non-nil newKey also replaces the Ed25519 key of the clinic.
func (c *Client) Rotate(ctx context.Context, id string, newKey ed25519.PrivateKey) (*Registration, error) {
	body := map[string]any{}
	if newKey != nil {
		body["pubkey"] = base64.StdEncoding.EncodeToString(newKey.Public().(ed25519.PublicKey))
	}
	var out Registration
	if err := c.do(ctx, request{method: http.MethodPost, path: "/v1/clinics/" + id + "/rotate", body: body, signed: true, newKey: newKey}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Delete revokes the clinic id. The service deletes the DNS records of the name.
func (c *Client) Delete(ctx context.Context, id string) error {
	return c.do(ctx, request{method: http.MethodDelete, path: "/v1/clinics/" + id, signed: true}, nil)
}

// ReadKey reads the Ed25519 key (PKCS #8 PEM) at path. A missing file gives an error that is os.ErrNotExist.
func ReadKey(path string) (ed25519.PrivateKey, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, fmt.Errorf("%s has no PEM key", path)
	}
	k, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	ek, ok := k.(ed25519.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("%s is not an Ed25519 key", path)
	}
	return ek, nil
}

// LoadOrCreateKey reads the Ed25519 key (PKCS #8 PEM) at path, or makes one with mode 0600.
func LoadOrCreateKey(path string) (ed25519.PrivateKey, bool, error) {
	if k, err := ReadKey(path); err == nil {
		return k, false, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, false, err
	}
	_, k, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, false, err
	}
	der, err := x509.MarshalPKCS8PrivateKey(k)
	if err != nil {
		return nil, false, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, false, err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), 0o600); err != nil {
		return nil, false, err
	}
	if err := os.Rename(tmp, path); err != nil {
		return nil, false, err
	}
	return k, true, nil
}

// WriteKey writes an Ed25519 key (PKCS #8 PEM) with mode 0600.
func WriteKey(path string, k ed25519.PrivateKey) error {
	der, err := x509.MarshalPKCS8PrivateKey(k)
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// WriteCredentials writes secrets/acme_dns.json with mode 0600.
func WriteCredentials(path string, c Credentials) error {
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
