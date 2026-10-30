package secrets

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"os"
	"strings"
	"testing"

	"github.com/rithviknishad/care-pi/agent/internal/paths"
)

func TestJWKSShape(t *testing.T) {
	enc, err := GenerateJWKS()
	if err != nil {
		t.Fatal(err)
	}
	raw, err := base64.StdEncoding.DecodeString(enc)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Keys []map[string]string `json:"keys"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.Keys) != 1 {
		t.Fatalf("want one key, got %d", len(doc.Keys))
	}
	k := doc.Keys[0]
	for _, f := range []string{"n", "e", "d", "p", "q", "dp", "dq", "qi", "kty", "kid", "alg"} {
		if k[f] == "" {
			t.Errorf("field %s is missing", f)
		}
	}
	if k["kty"] != "RSA" || k["alg"] != "RS256" || k["e"] != "AQAB" {
		t.Fatalf("unexpected key: kty=%s alg=%s e=%s", k["kty"], k["alg"], k["e"])
	}
	// Rebuild the key and sign: the private parts must match the public parts.
	dec := func(s string) *big.Int {
		b, err := base64.RawURLEncoding.DecodeString(s)
		if err != nil {
			t.Fatal(err)
		}
		return new(big.Int).SetBytes(b)
	}
	key := &rsa.PrivateKey{
		PublicKey: rsa.PublicKey{N: dec(k["n"]), E: 65537},
		D:         dec(k["d"]),
		Primes:    []*big.Int{dec(k["p"]), dec(k["q"])},
	}
	if err := key.Validate(); err != nil {
		t.Fatal(err)
	}
	if key.N.BitLen() != 2048 {
		t.Fatalf("key size %d", key.N.BitLen())
	}
	sum := sha256.Sum256([]byte("care"))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, sum[:])
	if err != nil {
		t.Fatal(err)
	}
	if err := rsa.VerifyPKCS1v15(&key.PublicKey, crypto.SHA256, sum[:], sig); err != nil {
		t.Fatal(err)
	}
	// kid is the RFC 7638 thumbprint.
	th, _ := json.Marshal(map[string]string{"e": k["e"], "kty": "RSA", "n": k["n"]})
	ts := sha256.Sum256(th)
	if k["kid"] != base64.RawURLEncoding.EncodeToString(ts[:]) {
		t.Fatal("kid is not the RFC 7638 thumbprint")
	}
}

func TestEnsureAllNeverReplaces(t *testing.T) {
	s := Store{Paths: paths.New(t.TempDir())}
	made, err := s.EnsureAll()
	if err != nil || len(made) != len(Names) {
		t.Fatalf("made %v, err %v", made, err)
	}
	first, _ := s.ReadAll()
	if len(first[DjangoSecretKey]) < 50 {
		t.Fatal("DJANGO_SECRET_KEY must have 50 or more characters")
	}
	made, err = s.EnsureAll()
	if err != nil || len(made) != 0 {
		t.Fatalf("second run made %v, err %v", made, err)
	}
	second, _ := s.ReadAll()
	for k, v := range first {
		if second[k] != v {
			t.Fatalf("secret %s changed", k)
		}
	}
	for _, n := range Names {
		st, err := os.Stat(s.Path(n))
		if err != nil || st.Mode().Perm() != 0o600 {
			t.Fatalf("%s: mode %v err %v", n, st.Mode(), err)
		}
	}
	st, _ := os.Stat(s.Paths.Abs(paths.SecretsDir))
	if st.Mode().Perm() != 0o700 {
		t.Fatalf("secrets dir mode %v", st.Mode())
	}
}

func TestDatabaseURL(t *testing.T) {
	files := map[string]string{PostgresPassword: "pw123"}
	for _, s := range PodmanSecrets() {
		if s.Name == PodmanDatabaseURL {
			if got := s.Value(files); got != "postgres://postgres:pw123@localhost:5432/care" {
				t.Fatalf("DATABASE_URL = %s", got)
			}
			return
		}
	}
	t.Fatal("no DATABASE_URL secret")
}

func TestGeneratedValuesAreSafe(t *testing.T) {
	for _, n := range Names {
		if n == JWKSBase64 {
			continue
		}
		v, err := Generate(n)
		if err != nil {
			t.Fatal(err)
		}
		if strings.ContainsAny(v, " \n$#'\"\\@:/") {
			t.Fatalf("%s has a character that needs quoting: %q", n, v)
		}
	}
}
