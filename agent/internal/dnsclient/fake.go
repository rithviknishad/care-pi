package dnsclient

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Fake is an in-memory DNS/ACME service for tests (services/dns/API.md). It checks the signatures,
// the nonces, the timestamps, and the replay cache like the real service.
type Fake struct {
	Server *httptest.Server
	// Zone is the base domain of the names.
	Zone string
	// Now is the time of the service. nil uses time.Now.
	Now func() time.Time
	// FailNext makes the next n requests fail with FailStatus (default 503).
	FailNext   int
	FailStatus int

	mu      sync.Mutex
	nonces  map[string]bool
	replay  map[string]bool
	Clinics map[string]*FakeClinic
	// Requests records "METHOD PATH" of each request.
	Requests []string
	seq      int
}

// FakeClinic is one registration of the Fake.
type FakeClinic struct {
	ID       string
	PubKey   ed25519.PublicKey
	Hint     string
	Username string
	Password string
	IPv4     string
	Revoked  bool
	TXT      []string
}

// NewFake starts a Fake. Close it with Server.Close.
func NewFake() *Fake {
	f := &Fake{Zone: "care-pi.rithviknishad.dev", nonces: map[string]bool{}, replay: map[string]bool{}, Clinics: map[string]*FakeClinic{}}
	f.Server = httptest.NewServer(http.HandlerFunc(f.serve))
	return f
}

// SetFail makes the next n requests fail with the status (0: 503).
func (f *Fake) SetFail(n, status int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.FailNext, f.FailStatus = n, status
}

// Clinic returns a copy of the registration with the id, or of the only registration when id is "".
func (f *Fake) Clinic(id string) (FakeClinic, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if id == "" && len(f.Clinics) == 1 {
		for _, c := range f.Clinics {
			return *c, true
		}
	}
	c, ok := f.Clinics[id]
	if !ok {
		return FakeClinic{}, false
	}
	return *c, true
}

// URL is the base URL of the Fake.
func (f *Fake) URL() string { return f.Server.URL }

// Close stops the Fake.
func (f *Fake) Close() { f.Server.Close() }

func (f *Fake) now() time.Time {
	if f.Now == nil {
		return time.Now()
	}
	return f.Now()
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func fail(w http.ResponseWriter, status int, code, msg string, extra ...any) {
	body := map[string]any{"error": code, "message": msg}
	for i := 0; i+1 < len(extra); i += 2 {
		body[extra[i].(string)] = extra[i+1]
	}
	writeJSON(w, status, body)
}

func randText(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

func (f *Fake) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Requests = append(f.Requests, r.Method+" "+r.URL.Path)
	if f.FailNext > 0 {
		f.FailNext--
		st := f.FailStatus
		if st == 0 {
			st = http.StatusServiceUnavailable
		}
		fail(w, st, "unavailable", "the fake fails this request")
		return
	}
	body, _ := io.ReadAll(r.Body)
	path := r.URL.Path
	switch {
	case r.Method == http.MethodGet && path == "/health":
		writeJSON(w, 200, map[string]string{"status": "ok"})
	case r.Method == http.MethodGet && path == "/v1/nonce":
		n := randText(32)
		f.nonces[n] = true
		writeJSON(w, 200, map[string]any{"nonce": n, "expires_in": 300, "server_time": f.now().Unix()})
	case r.Method == http.MethodPost && path == "/v1/register":
		var in struct {
			Hint   string `json:"hostname_hint"`
			PubKey string `json:"pubkey"`
			Nonce  string `json:"nonce"`
		}
		if json.Unmarshal(body, &in) != nil || in.Hint == "" {
			fail(w, 400, "bad_request", "bad body")
			return
		}
		pk, err := base64.StdEncoding.DecodeString(in.PubKey)
		if err != nil || len(pk) != ed25519.PublicKeySize {
			fail(w, 400, "bad_request", "pubkey")
			return
		}
		if !f.verify(w, r, body, ed25519.PublicKey(pk), "") {
			return
		}
		if !f.nonces[in.Nonce] {
			fail(w, 401, "bad_nonce", "the nonce is unknown, used, or expired")
			return
		}
		delete(f.nonces, in.Nonce)
		f.seq++
		slug := strings.ToLower(strings.Trim(in.Hint, "-"))
		id := fmt.Sprintf("%s-%04d", slug, f.seq)
		c := &FakeClinic{ID: id, PubKey: pk, Hint: in.Hint, Username: randText(12), Password: randText(30)}
		f.Clinics[id] = c
		writeJSON(w, 201, map[string]any{"id": id, "fulldomain": id + "." + f.Zone, "acme": f.acme(c, true),
			"listing_opt_in": false, "created_at": f.now().UTC().Format(time.RFC3339)})
	case r.Method == http.MethodPost && path == "/update":
		var in struct {
			Subdomain string `json:"subdomain"`
			TXT       string `json:"txt"`
		}
		_ = json.Unmarshal(body, &in)
		for _, c := range f.Clinics {
			if c.Username == r.Header.Get("X-Api-User") && c.Password == r.Header.Get("X-Api-Key") && c.ID == in.Subdomain && !c.Revoked {
				c.TXT = append(c.TXT, in.TXT)
				writeJSON(w, 200, map[string]string{"txt": in.TXT})
				return
			}
		}
		fail(w, 401, "forbidden", "no match")
	case strings.HasPrefix(path, "/v1/clinics/"):
		rest := strings.TrimPrefix(path, "/v1/clinics/")
		id, sub, _ := strings.Cut(rest, "/")
		c := f.Clinics[id]
		if c == nil {
			fail(w, 404, "unknown_clinic", "No clinic has this id.")
			return
		}
		if c.Revoked {
			fail(w, 410, "revoked", "This clinic id is revoked.")
			return
		}
		if !f.verify(w, r, body, c.PubKey, id) {
			return
		}
		switch {
		case r.Method == http.MethodGet && sub == "":
			var addr any
			if c.IPv4 != "" {
				addr = map[string]any{"ipv4": c.IPv4, "ipv6": nil, "ttl": 60, "updated_at": f.now().UTC().Format(time.RFC3339)}
			}
			writeJSON(w, 200, map[string]any{"id": id, "fulldomain": id + "." + f.Zone, "hostname_hint": c.Hint, "address": addr, "acme": f.acme(c, false)})
		case r.Method == http.MethodPut && sub == "address":
			var in struct {
				IPv4 *string `json:"ipv4"`
			}
			if json.Unmarshal(body, &in) != nil || in.IPv4 == nil {
				fail(w, 400, "bad_address", "Send ipv4, ipv6, or both.")
				return
			}
			changed := c.IPv4 != *in.IPv4
			c.IPv4 = *in.IPv4
			writeJSON(w, 200, map[string]any{"id": id, "fulldomain": id + "." + f.Zone, "ipv4": c.IPv4, "ipv6": nil, "ttl": 60,
				"changed": changed, "updated_at": f.now().UTC().Format(time.RFC3339)})
		case r.Method == http.MethodPost && sub == "rotate":
			var in struct {
				PubKey string `json:"pubkey"`
			}
			_ = json.Unmarshal(body, &in)
			if in.PubKey != "" {
				pk, _ := base64.StdEncoding.DecodeString(in.PubKey)
				sig, _ := base64.StdEncoding.DecodeString(r.Header.Get(HeaderSignatureNew))
				canon := Canonical(r.Method, path, r.Header.Get(HeaderTimestamp), body)
				if len(pk) != ed25519.PublicKeySize || !ed25519.Verify(pk, []byte(canon), sig) {
					fail(w, 401, "bad_new_signature", "new signature")
					return
				}
				c.PubKey = pk
			}
			c.Username, c.Password = randText(12), randText(30)
			writeJSON(w, 200, map[string]any{"id": id, "fulldomain": id + "." + f.Zone, "pubkey": base64.StdEncoding.EncodeToString(c.PubKey), "acme": f.acme(c, true)})
		case r.Method == http.MethodDelete && sub == "":
			c.Revoked = true
			c.IPv4 = ""
			writeJSON(w, 200, map[string]any{"id": id, "revoked_at": f.now().UTC().Format(time.RFC3339), "dns_cleanup": "done"})
		default:
			fail(w, 405, "method_not_allowed", "no route")
		}
	default:
		fail(w, 404, "not_found", "No such path.")
	}
}

func (f *Fake) acme(c *FakeClinic, withPassword bool) map[string]any {
	out := map[string]any{"username": c.Username, "subdomain": c.ID, "fulldomain": "_acme-challenge." + c.ID + "." + f.Zone,
		"server_url": f.Server.URL, "allowfrom": []string{}}
	if withPassword {
		out["password"] = c.Password
	}
	return out
}

func (f *Fake) verify(w http.ResponseWriter, r *http.Request, body []byte, pk ed25519.PublicKey, id string) bool {
	ts := r.Header.Get(HeaderTimestamp)
	sig, err := base64.StdEncoding.DecodeString(r.Header.Get(HeaderSignature))
	if ts == "" || err != nil || len(sig) != ed25519.SignatureSize {
		fail(w, 401, "missing_signature", "headers")
		return false
	}
	t, err := strconv.ParseInt(ts, 10, 64)
	if err != nil {
		fail(w, 401, "bad_timestamp", "timestamp")
		return false
	}
	now := f.now().Unix()
	if d := now - t; d > 300 || d < -300 {
		fail(w, 401, "stale_timestamp", "X-Care-Pi-Timestamp must be within 300 seconds of the server time.", "server_time", now)
		return false
	}
	canon := Canonical(r.Method, r.URL.Path, ts, body)
	if !ed25519.Verify(pk, []byte(canon), sig) {
		fail(w, 401, "bad_signature", "signature")
		return false
	}
	if id != "" {
		key := id + "\n" + hex.EncodeToString([]byte(canon))
		if f.replay[key] {
			fail(w, 401, "replayed", "replayed")
			return false
		}
		f.replay[key] = true
	}
	return true
}
