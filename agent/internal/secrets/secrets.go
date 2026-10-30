// Package secrets generates and reads the secret files in /etc/care-pi/secrets.
//
// Each secret is one file with mode 0600. care-pi never writes a secret into
// config.yaml, the lock, or a rendered file. apply copies the values into podman secrets.
package secrets

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"math/big"
	"os"
	"path/filepath"
	"strings"

	"github.com/rithviknishad/care-pi/agent/internal/paths"
)

// Names of the secret files.
const (
	DjangoSecretKey  = "django_secret_key"
	JWKSBase64       = "jwks_base64"
	PostgresPassword = "postgres_password"
	S3AccessKey      = "s3_access_key"
	S3SecretKey      = "s3_secret_key"
	PGBackRestCipher = "pgbackrest_repo_cipher_pass"
)

// Names lists every secret file that bootstrap generates.
var Names = []string{DjangoSecretKey, JWKSBase64, PostgresPassword, S3AccessKey, S3SecretKey, PGBackRestCipher}

const (
	alnum = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"
	upper = "ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
)

// Generate returns a new value for the secret name.
func Generate(name string) (string, error) {
	switch name {
	case DjangoSecretKey:
		return randomString(alnum, 64)
	case JWKSBase64:
		return GenerateJWKS()
	case PostgresPassword:
		return randomString(alnum, 32)
	case S3AccessKey:
		return randomString(upper, 20)
	case S3SecretKey:
		return randomString(alnum, 40)
	case PGBackRestCipher:
		return randomString(alnum, 64)
	default:
		return "", fmt.Errorf("unknown secret %q", name)
	}
}

func randomString(alphabet string, n int) (string, error) {
	var b strings.Builder
	max := big.NewInt(int64(len(alphabet)))
	for range n {
		i, err := rand.Int(rand.Reader, max)
		if err != nil {
			return "", fmt.Errorf("cannot read random bytes: %w", err)
		}
		b.WriteByte(alphabet[i.Int64()])
	}
	return b.String(), nil
}

// Store reads and writes secret files below a root.
type Store struct {
	Paths paths.Paths
}

// Path returns the file path of the secret below the root.
func (s Store) Path(name string) string {
	return s.Paths.Abs(paths.Secret(name))
}

// Read returns the value of the secret without surrounding white space.
func (s Store) Read(name string) (string, error) {
	data, err := os.ReadFile(s.Path(name))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", fmt.Errorf("secret %s is missing; run \"care-pi bootstrap\": %w", paths.Secret(name), err)
		}
		return "", fmt.Errorf("cannot read secret %s: %w", paths.Secret(name), err)
	}
	v := strings.TrimSpace(string(data))
	if v == "" {
		return "", fmt.Errorf("secret %s is empty", paths.Secret(name))
	}
	return v, nil
}

// ReadAll returns the value of every secret in Names.
func (s Store) ReadAll() (map[string]string, error) {
	out := map[string]string{}
	var errs []error
	for _, n := range Names {
		v, err := s.Read(n)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		out[n] = v
	}
	return out, errors.Join(errs...)
}

// Missing returns the names of the secrets that have no file.
func (s Store) Missing() []string {
	var out []string
	for _, n := range Names {
		if _, err := os.Stat(s.Path(n)); err != nil {
			out = append(out, n)
		}
	}
	return out
}

// EnsureAll generates every missing secret. It never replaces a file that exists.
// It returns the names of the generated secrets.
func (s Store) EnsureAll() ([]string, error) {
	dir := s.Paths.Abs(paths.SecretsDir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("cannot create %s: %w", paths.SecretsDir, err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return nil, fmt.Errorf("cannot set the mode of %s: %w", paths.SecretsDir, err)
	}
	var made []string
	for _, n := range s.Missing() {
		v, err := Generate(n)
		if err != nil {
			return made, err
		}
		if err := writeExclusive(s.Path(n), []byte(v+"\n")); err != nil {
			return made, fmt.Errorf("cannot write secret %s: %w", paths.Secret(n), err)
		}
		made = append(made, n)
	}
	return made, nil
}

func writeExclusive(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-"+filepath.Base(path)+"-")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	// Link fails when the target exists, so a parallel run cannot replace a secret.
	return os.Link(tmp.Name(), path)
}

// Hash returns a short fingerprint of a value. apply stores it to detect changes.
// The input is a random value with high entropy, so the fingerprint does not leak it.
func Hash(v string) string {
	sum := sha256.Sum256([]byte(v))
	return fmt.Sprintf("%x", sum[:8])
}

// GenerateJWKS returns a base64-encoded JWK set with one RSA 2048 private key.
// The shape is the same as care.utils.jwks.generate_jwk.generate_encoded_jwks in upstream Care:
// {"keys": [{"n","e","d","p","q","dp","dq","qi","kty":"RSA","kid","alg":"RS256"}]}.
// kid is the RFC 7638 thumbprint, as authlib computes it.
func GenerateJWKS() (string, error) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return "", fmt.Errorf("cannot generate an RSA key: %w", err)
	}
	return EncodeJWKS(key)
}

// EncodeJWKS encodes key as a base64 JWK set.
func EncodeJWKS(key *rsa.PrivateKey) (string, error) {
	key.Precompute()
	b64 := func(n *big.Int) string { return base64.RawURLEncoding.EncodeToString(n.Bytes()) }
	e := b64(big.NewInt(int64(key.E)))
	n := b64(key.N)
	thumb, err := json.Marshal(struct {
		E   string `json:"e"`
		Kty string `json:"kty"`
		N   string `json:"n"`
	}{e, "RSA", n})
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(thumb)
	jwk := struct {
		N   string `json:"n"`
		E   string `json:"e"`
		D   string `json:"d"`
		P   string `json:"p"`
		Q   string `json:"q"`
		DP  string `json:"dp"`
		DQ  string `json:"dq"`
		QI  string `json:"qi"`
		Kty string `json:"kty"`
		Kid string `json:"kid"`
		Alg string `json:"alg"`
	}{
		N:   n,
		E:   e,
		D:   b64(key.D),
		P:   b64(key.Primes[0]),
		Q:   b64(key.Primes[1]),
		DP:  b64(key.Precomputed.Dp),
		DQ:  b64(key.Precomputed.Dq),
		QI:  b64(key.Precomputed.Qinv),
		Kty: "RSA",
		Kid: base64.RawURLEncoding.EncodeToString(sum[:]),
		Alg: "RS256",
	}
	doc, err := json.Marshal(map[string]any{"keys": []any{jwk}})
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(doc), nil
}

// PodmanSecret is one podman secret that apply creates from the secret files.
type PodmanSecret struct {
	// Name is the podman secret name. Quadlet files reference it with Secret=.
	Name string
	// Value builds the value from the secret files.
	Value func(files map[string]string) string
	// Units lists the units that read the secret. apply restarts them when the value changes.
	Units []string
}

// Podman secret names.
const (
	PodmanDjangoSecretKey  = "care-pi-django-secret-key"
	PodmanJWKSBase64       = "care-pi-jwks-base64"
	PodmanPostgresPassword = "care-pi-postgres-password"
	PodmanDatabaseURL      = "care-pi-database-url"
	PodmanS3AccessKey      = "care-pi-s3-access-key"
	PodmanS3SecretKey      = "care-pi-s3-secret-key"
	PodmanPGBackRestCipher = "care-pi-pgbackrest-cipher-pass"
)

// Database settings that the units and DATABASE_URL share.
const (
	DBUser = "postgres"
	DBName = "care"
	DBHost = "localhost"
	DBPort = "5432"
)

var careUnits = []string{"care-beat", "care-worker", "care-api"}

// PodmanSecrets returns every podman secret that apply manages.
func PodmanSecrets() []PodmanSecret {
	file := func(n string) func(map[string]string) string {
		return func(f map[string]string) string { return f[n] }
	}
	return []PodmanSecret{
		{PodmanDjangoSecretKey, file(DjangoSecretKey), careUnits},
		{PodmanJWKSBase64, file(JWKSBase64), careUnits},
		{PodmanPostgresPassword, file(PostgresPassword), append([]string{"care-db"}, careUnits...)},
		{PodmanDatabaseURL, func(f map[string]string) string {
			return "postgres://" + DBUser + ":" + f[PostgresPassword] + "@" + DBHost + ":" + DBPort + "/" + DBName
		}, careUnits},
		{PodmanS3AccessKey, file(S3AccessKey), append([]string{"care-s3"}, careUnits...)},
		{PodmanS3SecretKey, file(S3SecretKey), append([]string{"care-s3"}, careUnits...)},
		{PodmanPGBackRestCipher, file(PGBackRestCipher), []string{"care-db"}},
	}
}
