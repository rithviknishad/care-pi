package update

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rithviknishad/care-pi/agent/internal/minisign"
	"github.com/rithviknishad/care-pi/agent/internal/paths"
	"github.com/rithviknishad/care-pi/agent/internal/problem"
	"github.com/rithviknishad/care-pi/agent/internal/solution"
)

// TestRequireSigned: with updates.require_signed, the index and each lock need a valid .minisig of a trusted key
// (Phase 7, docs/solutions.md section 18).
func TestRequireSigned(t *testing.T) {
	e := newTEnv(t, "stable", "")
	index, err := os.ReadFile(e.fx.Path(e.fx.IndexURL))
	if err != nil {
		t.Fatal(err)
	}
	ix, err := solution.ParseIndex(index)
	if err != nil {
		t.Fatal(err)
	}
	entry := ix.Find("care-clinic").Locks[0]
	lockData, err := os.ReadFile(e.fx.Path(solution.ResolveURL(ix.DistBase, entry.URL)))
	if err != nil {
		t.Fatal(err)
	}
	pk, priv, _ := minisign.GenerateKey(rand.Reader)
	other, otherPriv, _ := minisign.GenerateKey(rand.Reader)
	files := map[string][]byte{}
	serve := func(name string, data []byte, sig []byte) {
		files["/"+name] = data
		if sig != nil {
			files["/"+name+SigSuffix] = sig
		}
	}
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, ok := files[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(data)
	}))
	defer srv.Close()
	hc := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}} //nolint:gosec // a test server
	c := Client{Paths: e.p, HTTP: hc, RequireSigned: true, Keys: minisign.Keys{pk}}
	url := srv.URL + "/index.json"

	serve("index.json", index, nil)
	if _, err := c.FetchIndex(context.Background(), url); !problem.Is(err, problem.LockSignatureInvalid) || !strings.Contains(err.Error(), "no signature") {
		t.Fatalf("no signature: %v", err)
	}
	serve("index.json", index, minisign.Sign(otherPriv, other.ID, index, "x"))
	if _, err := c.FetchIndex(context.Background(), url); !problem.Is(err, problem.LockSignatureInvalid) {
		t.Fatalf("other key: %v", err)
	}
	serve("index.json", index, minisign.Sign(priv, pk.ID, append([]byte("x"), index...), "x"))
	if _, err := c.FetchIndex(context.Background(), url); !problem.Is(err, problem.LockSignatureInvalid) {
		t.Fatalf("signature of another file: %v", err)
	}
	serve("index.json", index, minisign.Sign(priv, pk.ID, index, "timestamp:1 file:index.json"))
	got, err := c.FetchIndex(context.Background(), url)
	if err != nil {
		t.Fatal(err)
	}
	// Without the key the same files fail; without require_signed they pass.
	if _, err := (Client{Paths: e.p, HTTP: hc, RequireSigned: true}).FetchIndex(context.Background(), url); !problem.Is(err, problem.LockSignatureInvalid) ||
		!strings.Contains(err.Error(), "trusts no signing key") {
		t.Fatalf("no key: %v", err)
	}
	serve("index.json", index, nil)
	if _, err := (Client{Paths: e.p, HTTP: hc}).FetchIndex(context.Background(), url); err != nil {
		t.Fatal(err)
	}

	// The lock: the URL of the entry on the test server.
	got.DistBase = srv.URL + "/"
	entry.URL = "lock.yaml"
	serve("lock.yaml", lockData, nil)
	if _, err := c.FetchLock(context.Background(), got, "care-clinic", entry); !problem.Is(err, problem.LockSignatureInvalid) {
		t.Fatalf("lock without signature: %v", err)
	}
	serve("lock.yaml", lockData, minisign.Sign(priv, pk.ID, lockData, "file:lock.yaml"))
	if _, err := c.FetchLock(context.Background(), got, "care-clinic", entry); err != nil {
		t.Fatal(err)
	}

	// A local file (care-pi update --lock-file, the dist snapshot): the signature file is next to it.
	dir := t.TempDir()
	f := filepath.Join(dir, "x.lock.yaml")
	if err := os.WriteFile(f, lockData, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := c.verifyLocal(f, lockData); !problem.Is(err, problem.LockSignatureInvalid) {
		t.Fatalf("local without signature: %v", err)
	}
	if err := os.WriteFile(f+SigSuffix, minisign.Sign(priv, pk.ID, lockData, "x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := c.verifyLocal(f, lockData); err != nil {
		t.Fatal(err)
	}
}

// TestTrustedKeys: the keys of the build (none until the key ceremony) and the keys of the host.
func TestTrustedKeys(t *testing.T) {
	p := paths.New(t.TempDir())
	base, err := TrustedKeys(p)
	if err != nil {
		t.Fatal(err)
	}
	pk, _, _ := minisign.GenerateKey(rand.Reader)
	if err := os.MkdirAll(p.Abs(paths.KeysDir), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p.Abs(paths.KeysDir+"/local.pub"), pk.Encode(), 0o644); err != nil {
		t.Fatal(err)
	}
	keys, err := TrustedKeys(p)
	if err != nil || len(keys) != len(base)+1 || keys[len(keys)-1].KeyID() != pk.KeyID() {
		t.Fatalf("keys %v %v", keys.IDs(), err)
	}
	if err := os.WriteFile(p.Abs(paths.KeysDir+"/bad.pub"), []byte("nope"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := TrustedKeys(p); err == nil {
		t.Fatal("a bad key file must fail")
	}
}
