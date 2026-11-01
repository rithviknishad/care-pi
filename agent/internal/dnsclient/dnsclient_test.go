package dnsclient

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func client(t *testing.T, f *Fake) *Client {
	t.Helper()
	k, created, err := LoadOrCreateKey(filepath.Join(t.TempDir(), "secrets", "dns_client.key"))
	if err != nil || !created {
		t.Fatalf("key: %v %v", created, err)
	}
	return &Client{BaseURL: f.URL(), Key: k, Backoff: time.Millisecond}
}

func TestCanonical(t *testing.T) {
	got := Canonical("put", "/v1/clinics/x/address", "1793520000", []byte(`{"ipv4":"192.168.1.10"}`))
	want := "CARE-PI-DNS-V1\nPUT\n/v1/clinics/x/address\n1793520000\n"
	if !strings.HasPrefix(got, want) || len(got) != len(want)+64 {
		t.Fatalf("canonical %q", got)
	}
	empty := Canonical("GET", "/v1/clinics/x", "1", nil)
	if !strings.HasSuffix(empty, "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855") {
		t.Fatalf("an empty body hashes the empty string: %q", empty)
	}
}

// TestVectors checks the canonical string and the signatures with the test vectors of services/dns/API.md
// section 3.6 (RFC 8032 section 7.1 test keys; never use them for a clinic).
func TestVectors(t *testing.T) {
	seed, _ := hex.DecodeString("9d61b19deffd5a60ba844af492ec2cc44449c5697b326919703bac031cae7f60")
	key := ed25519.NewKeyFromSeed(seed)
	newSeed, _ := hex.DecodeString("4ccd089b28ff96da9db6c346ec114e0f5b8a319f35aba624da8cf6ed4fb8a6fb")
	newKey := ed25519.NewKeyFromSeed(newSeed)
	if got := (&Client{Key: key}).PublicKey(); got != "11qYAYKxCrfVS/7TyWQHOg7hcvPapiMlrwIaaPcHURo=" {
		t.Fatalf("public key %s", got)
	}
	sign := func(k ed25519.PrivateKey, canon string) string {
		return base64.StdEncoding.EncodeToString(ed25519.Sign(k, []byte(canon)))
	}
	cases := []struct {
		name, method, path, body, sig string
		key                           ed25519.PrivateKey
	}{
		{"address", "PUT", "/v1/clinics/lumine-k3x9/address", `{"ipv4":"192.168.1.23","ipv6":null}`,
			"Km62Ebr0YPOvKs+mD8HBbBIYGSIuH20JtOt/1AAsACiQ3NOFQL7vyYs6Dd78Byp8A/nzPDiPUrAgt37rAXfEBQ==", key},
		{"register", "POST", "/v1/register", `{"hostname_hint":"lumine","pubkey":"11qYAYKxCrfVS/7TyWQHOg7hcvPapiMlrwIaaPcHURo=","nonce":"AQIDBAUGBwgJCgsMDQ4PEBESExQVFhcYGRobHB0eHyA","listing_opt_in":false}`,
			"TRufeuMLWG2B8CDQNyp7GxuZ8upXWyj71NfrlqksXO49l965EEzaC7y55tUtIBsqlxcN9IQruS45n6W17V6NAQ==", key},
		{"rotate", "POST", "/v1/clinics/lumine-k3x9/rotate", `{"pubkey":"PUAXw+hDiVqStwqnTRt+vJyYLM8uxJaMwM1V8Sr0Zgw="}`,
			"ORZueO8mjAMAP6xS51h5zOlOHmVUSYHg20U8THZNalkU0hbReD0Z5g6SDbwBjX0qUDIEB/30IoNCsHr6x6rQBA==", key},
		{"rotate new key", "POST", "/v1/clinics/lumine-k3x9/rotate", `{"pubkey":"PUAXw+hDiVqStwqnTRt+vJyYLM8uxJaMwM1V8Sr0Zgw="}`,
			"WL8PBwqw6JAG+ozKSJcSdaIYQ9ibFAxv1vJD4W3JnhM7QiKpxEZDXtfaXj9i4zmsQi+BNiESGEdopRyWYClJBA==", newKey},
		{"delete", "DELETE", "/v1/clinics/lumine-k3x9", "",
			"bYxrkrbQZlJI4O2E0Vuefs1/hmz389RJTQzGAZc2DdUoBIHMgXhfulJ5zfIo0iuPPXS/aigxMmtx0on9jBLvDQ==", key},
	}
	for _, c := range cases {
		var body []byte
		if c.body != "" {
			body = []byte(c.body)
		}
		if got := sign(c.key, Canonical(c.method, c.path, "1798761600", body)); got != c.sig {
			t.Errorf("%s: signature %s, want %s", c.name, got, c.sig)
		}
	}
	// The bodies that the client makes are the bytes of the vectors.
	ip := "192.168.1.23"
	if b, _ := json.Marshal(addressBody{IPv4: &ip}); string(b) != cases[0].body {
		t.Errorf("address body %s", b)
	}
	if b, _ := json.Marshal(registerBody{Hint: "lumine", PubKey: "11qYAYKxCrfVS/7TyWQHOg7hcvPapiMlrwIaaPcHURo=", Nonce: "AQIDBAUGBwgJCgsMDQ4PEBESExQVFhcYGRobHB0eHyA"}); string(b) != cases[1].body {
		t.Errorf("register body %s", b)
	}
}

func TestRegisterUpdateRotateDelete(t *testing.T) {
	f := NewFake()
	defer f.Close()
	c := client(t, f)
	ctx := context.Background()
	reg, err := c.Register(ctx, "lumine", false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(reg.ID, "lumine-") || reg.FullDomain != reg.ID+".care-pi.rithviknishad.dev" ||
		reg.ACME.Subdomain != reg.ID || reg.ACME.Password == "" || reg.ACME.ServerURL != f.URL() {
		t.Fatalf("registration %+v", reg)
	}
	cred := reg.ACME.Credentials()
	path := filepath.Join(t.TempDir(), "acme_dns.json")
	if err := WriteCredentials(path, cred); err != nil {
		t.Fatal(err)
	}
	st, _ := os.Stat(path)
	if st.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v", st.Mode())
	}
	var back map[string]string
	data, _ := os.ReadFile(path)
	if json.Unmarshal(data, &back) != nil || len(back) != 4 || back["subdomain"] != reg.ID || back["password"] == "" {
		t.Fatalf("the single-account file of the acmedns module: %s", data)
	}

	addr, err := c.UpdateAddress(ctx, reg.ID, "192.168.1.10")
	if err != nil || !addr.Changed || *addr.IPv4 != "192.168.1.10" {
		t.Fatalf("address %+v %v", addr, err)
	}
	// The same address in the same second: the canonical string differs only by the timestamp. The client
	// signs each try again, so a second call later works and reports no change.
	time.Sleep(1100 * time.Millisecond)
	addr, err = c.UpdateAddress(ctx, reg.ID, "192.168.1.10")
	if err != nil || addr.Changed {
		t.Fatalf("second address %+v %v", addr, err)
	}
	stat, err := c.Status(ctx, reg.ID)
	if err != nil || stat.Address == nil || *stat.Address.IPv4 != "192.168.1.10" {
		t.Fatalf("status %+v %v", stat, err)
	}

	_, nk, _ := ed25519.GenerateKey(rand.Reader)
	rot, err := c.Rotate(ctx, reg.ID, nk)
	if err != nil || rot.ACME.Password == "" || rot.ACME.Password == reg.ACME.Password {
		t.Fatalf("rotate %+v %v", rot, err)
	}
	if _, err := c.Status(ctx, reg.ID); err == nil {
		t.Fatal("the old key still works after the rotation")
	}
	c.Key = nk
	if err := c.Delete(ctx, reg.ID); err != nil {
		t.Fatal(err)
	}
	_, err = c.UpdateAddress(ctx, reg.ID, "192.168.1.11")
	var se *Error
	if !errors.As(err, &se) || !se.Revoked() {
		t.Fatalf("after delete: %v", err)
	}
}

func TestRetriesAndErrors(t *testing.T) {
	f := NewFake()
	defer f.Close()
	c := client(t, f)
	ctx := context.Background()
	reg, err := c.Register(ctx, "care", false)
	if err != nil {
		t.Fatal(err)
	}
	f.FailNext = 2
	if _, err := c.UpdateAddress(ctx, reg.ID, "10.0.0.5"); err != nil {
		t.Fatalf("two 503 then a success: %v", err)
	}
	f.FailNext, f.FailStatus = 10, http.StatusServiceUnavailable
	_, err = c.UpdateAddress(ctx, reg.ID, "10.0.0.6")
	var se *Error
	if !errors.As(err, &se) || se.Status != 503 {
		t.Fatalf("all tries fail: %v", err)
	}
	f.FailNext = 0
	// A 4xx is not tried again.
	n := len(f.Requests)
	if _, err := c.UpdateAddress(ctx, "no-such-0001", "10.0.0.5"); err == nil || len(f.Requests) != n+1 {
		t.Fatalf("unknown clinic: %v, %d requests", err, len(f.Requests)-n)
	}
	// The service does not answer.
	dead := &Client{BaseURL: "http://127.0.0.1:1", Key: c.Key, Backoff: time.Millisecond, Attempts: 2}
	if err := dead.Health(ctx); !errors.Is(err, ErrUnreachable) {
		t.Fatalf("unreachable: %v", err)
	}
}

func TestClockSkew(t *testing.T) {
	f := NewFake()
	defer f.Close()
	c := client(t, f)
	// The device clock is 2 hours late (no NTP yet).
	c.Now = func() time.Time { return time.Now().Add(-2 * time.Hour) }
	reg, err := c.Register(context.Background(), "care", false)
	if err == nil {
		// Register has one try (the nonce is single-use), so it can fail; the next call then signs with the server time.
		t.Logf("registered %s", reg.ID)
	}
	if reg == nil {
		if reg, err = c.Register(context.Background(), "care", false); err != nil {
			t.Fatalf("second register after the skew fix: %v", err)
		}
	}
	if _, err := c.UpdateAddress(context.Background(), reg.ID, "10.0.0.5"); err != nil {
		t.Fatal(err)
	}
}

func TestLoadKeyAgain(t *testing.T) {
	path := filepath.Join(t.TempDir(), "k")
	k1, created, err := LoadOrCreateKey(path)
	if err != nil || !created {
		t.Fatal(err)
	}
	k2, created, err := LoadOrCreateKey(path)
	if err != nil || created || !k1.Equal(k2) {
		t.Fatalf("load again: %v %v", created, err)
	}
	if st, _ := os.Stat(path); st.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v", st.Mode())
	}
}

// m1: a rotation to a new key whose answer is lost. The service has the new key, so the old key gets 401
// bad_signature. The client signs again once with the pending key, and promotes it when the service takes it.
func TestPendingKeyAfterLostRotation(t *testing.T) {
	f := NewFake()
	defer f.Close()
	c := client(t, f)
	// Each signature has a new timestamp: the retries of the test come within milliseconds (the replay cache).
	var mu sync.Mutex
	tick := time.Now()
	c.Now = func() time.Time { mu.Lock(); defer mu.Unlock(); tick = tick.Add(time.Second); return tick }
	f.Now = func() time.Time { mu.Lock(); defer mu.Unlock(); return tick }
	ctx := context.Background()
	reg, err := c.Register(ctx, "care", false)
	if err != nil {
		t.Fatal(err)
	}
	var promoted []ed25519.PrivateKey
	c.Promote = func(k ed25519.PrivateKey) error { promoted = append(promoted, k); return nil }

	// The first try of Rotate arrives, and its answer is lost. The second try (old key) gets 401; the try with the
	// pending key rotates again and gives the credentials.
	_, nk, _ := ed25519.GenerateKey(rand.Reader)
	c.Pending = nk
	f.SetLose(1)
	rot, err := c.Rotate(ctx, reg.ID, nk)
	if err != nil {
		t.Fatalf("rotate after a lost answer: %v", err)
	}
	fc, _ := f.Clinic(reg.ID)
	if !c.Key.Equal(nk) || c.Pending != nil || len(promoted) != 1 || !promoted[0].Equal(nk) ||
		!fc.PubKey.Equal(nk.Public()) || rot.ACME.Password != fc.Password {
		t.Fatalf("key %v pending %v promoted %d; the credentials must be the last ones", c.Key.Equal(nk), c.Pending != nil, len(promoted))
	}

	// Each try is lost: Rotate fails, the pending key stays. The next request (the address timer) promotes it.
	_, nk2, _ := ed25519.GenerateKey(rand.Reader)
	c.Pending = nk2
	f.SetLose(3)
	if _, err := c.Rotate(ctx, reg.ID, nk2); !errors.Is(err, ErrUnreachable) {
		t.Fatalf("all answers lost: %v", err)
	}
	if !c.Key.Equal(nk) || c.Pending == nil {
		t.Fatal("the client must keep the old key and the pending key")
	}
	if _, err := c.UpdateAddress(ctx, reg.ID, "192.168.1.10"); err != nil {
		t.Fatalf("the address with the pending key: %v", err)
	}
	if !c.Key.Equal(nk2) || c.Pending != nil || len(promoted) != 2 || !promoted[1].Equal(nk2) {
		t.Fatal("the pending key is not promoted")
	}

	// A pending key of a rotation that never arrived: the key works, the pending key stays unused.
	_, nk3, _ := ed25519.GenerateKey(rand.Reader)
	c.Pending = nk3
	if _, err := c.Status(ctx, reg.ID); err != nil || !c.Key.Equal(nk2) || c.Pending == nil || len(promoted) != 2 {
		t.Fatalf("status %v", err)
	}
	// Neither key works: the error of the key, and nothing changes.
	_, other, _ := ed25519.GenerateKey(rand.Reader)
	f.SetKey(reg.ID, other.Public().(ed25519.PublicKey))
	_, err = c.Status(ctx, reg.ID)
	var se *Error
	if !errors.As(err, &se) || !se.Unauthorized() || se.Code != "bad_signature" || !c.Key.Equal(nk2) || len(promoted) != 2 {
		t.Fatalf("both keys refused: %v", err)
	}
}
