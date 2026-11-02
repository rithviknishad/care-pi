package minisign

import (
	"crypto/rand"
	"errors"
	"os"
	"strings"
	"testing"
)

func read(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// TestInterop: signatures of the minisign tool (0.12, testdata made in alpine:3.22): the hashed (default) and the
// legacy algorithm, the key id, and the trusted comment.
func TestInterop(t *testing.T) {
	pk, err := ParsePublicKey(read(t, "test.pub"))
	if err != nil {
		t.Fatal(err)
	}
	if pk.KeyID() != "E5991B6A506716FF" {
		t.Fatalf("key id %s", pk.KeyID())
	}
	keys := Keys{pk}
	for _, f := range []string{"index.json", "legacy.json"} {
		if _, err := keys.Verify(read(t, f), read(t, f+".minisig")); err != nil {
			t.Fatalf("%s: %v", f, err)
		}
	}
	s, _ := ParseSignature(read(t, "index.json.minisig"))
	if s.Algorithm != "ED" || s.TrustedComment != "care-pi test index" {
		t.Fatalf("signature %+v", s)
	}
	if s, _ := ParseSignature(read(t, "legacy.json.minisig")); s.Algorithm != "Ed" {
		t.Fatalf("legacy %s", s.Algorithm)
	}
}

// TestBad is a table of files that must fail.
func TestBad(t *testing.T) {
	pk, _ := ParsePublicKey(read(t, "test.pub"))
	keys := Keys{pk}
	msg, sig := read(t, "index.json"), string(read(t, "index.json.minisig"))
	other, _, _ := GenerateKey(rand.Reader)
	ls := strings.Split(sig, "\n")
	cases := map[string]struct {
		msg []byte
		sig string
		ks  Keys
	}{
		"changed file":            {append(append([]byte{}, msg...), ' '), sig, keys},
		"changed trusted comment": {msg, strings.Replace(sig, "care-pi test index", "care-pi test indey", 1), keys},
		"other key":               {msg, sig, Keys{other}},
		"no key":                  {msg, sig, nil},
		"three lines":             {msg, strings.Join(ls[:3], "\n"), keys},
		"not base64":              {msg, strings.Replace(sig, ls[1], "!!!", 1), keys},
		"short signature":         {msg, strings.Replace(sig, ls[1], ls[1][:40], 1), keys},
		"short global signature":  {msg, strings.Replace(sig, ls[3], ls[3][:40], 1), keys},
		"no trusted comment line": {msg, strings.Replace(sig, "trusted comment: ", "comment: ", 1), keys},
		"empty":                   {msg, "", keys},
	}
	for name, c := range cases {
		if _, err := c.ks.Verify(c.msg, []byte(c.sig)); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
	if _, err := (Keys{other}).Verify(msg, []byte(sig)); !errors.Is(err, ErrKeyID) {
		t.Errorf("other key: %v", err)
	}
	for name, k := range map[string]string{
		"two lines":  "a\nb\nc\n",
		"not Ed":     "untrusted comment: x\nWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFh\n",
		"not base64": "untrusted comment: x\n!!!\n",
	} {
		if _, err := ParsePublicKey([]byte(k)); err == nil {
			t.Errorf("public key %s: no error", name)
		}
	}
}

// TestSignRoundTrip: Sign writes what the minisign tool writes, and Encode the public key file.
func TestSignRoundTrip(t *testing.T) {
	pk, priv, err := GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	again, err := ParsePublicKey(pk.Encode())
	if err != nil || again.KeyID() != pk.KeyID() || !again.Key.Equal(pk.Key) {
		t.Fatalf("encode %v", err)
	}
	msg := []byte("version: 1\n")
	sig := Sign(priv, pk.ID, msg, "timestamp:1 file:x.lock.yaml")
	if k, err := (Keys{pk}).Verify(msg, sig); err != nil || k.KeyID() != pk.KeyID() {
		t.Fatalf("verify %v", err)
	}
}
