package auth

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"crypto/subtle"
	_ "embed"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"

	"golang.org/x/crypto/argon2"
	"golang.org/x/text/unicode/norm"
)

// Argon2Params are the argon2id parameters.
type Argon2Params struct {
	MemoryKiB uint32
	Time      uint32
	Threads   uint8
}

// DefaultParams is the OWASP minimum: m = 19 MiB, t = 2, p = 1 (docs/api/README.md, section 2.1).
var DefaultParams = Argon2Params{MemoryKiB: 19 * 1024, Time: 2, Threads: 1}

const (
	saltLen = 16
	keyLen  = 32
	// MinPasswordLen and MaxPasswordLen count Unicode characters after NFC.
	MinPasswordLen = 12
	MaxPasswordLen = 128
)

// hashMu makes the agent hash one password at a time: its memory cap is 64M.
var hashMu sync.Mutex

// HashPassword returns the PHC string of the argon2id hash of the NFC form of password.
func HashPassword(password string, p Argon2Params) (string, error) {
	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("cannot read random bytes: %w", err)
	}
	hashMu.Lock()
	key := argon2.IDKey([]byte(norm.NFC.String(password)), salt, p.Time, p.MemoryKiB, p.Threads, keyLen)
	hashMu.Unlock()
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s", argon2.Version, p.MemoryKiB, p.Time, p.Threads,
		base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(key)), nil
}

// VerifyPassword compares password with a PHC string of HashPassword in constant time.
func VerifyPassword(password, encoded string) (bool, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" || parts[2] != "v="+strconv.Itoa(argon2.Version) {
		return false, errors.New("the password hash has an unknown format")
	}
	var p Argon2Params
	var threads int
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &p.MemoryKiB, &p.Time, &threads); err != nil {
		return false, fmt.Errorf("the password hash has bad parameters: %w", err)
	}
	p.Threads = uint8(threads)
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return false, errors.New("the password hash has a bad salt")
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return false, errors.New("the password hash has a bad key")
	}
	hashMu.Lock()
	got := argon2.IDKey([]byte(norm.NFC.String(password)), salt, p.Time, p.MemoryKiB, p.Threads, uint32(len(want)))
	hashMu.Unlock()
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}

//go:embed common-passwords.txt
var commonList []byte

var common = func() map[string]bool {
	m := map[string]bool{}
	sc := bufio.NewScanner(bytes.NewReader(commonList))
	for sc.Scan() {
		l := strings.TrimSpace(sc.Text())
		if l != "" && !strings.HasPrefix(l, "#") {
			m[strings.ToLower(l)] = true
		}
	}
	return m
}()

// CheckPassword applies the password rules: 12 to 128 characters after NFC, not a common password.
// It returns a short English reason, or "".
func CheckPassword(password string) (reason, code string) {
	p := norm.NFC.String(password)
	n := utf8.RuneCountInString(p)
	switch {
	case n < MinPasswordLen:
		return fmt.Sprintf("use %d characters or more", MinPasswordLen), "minLength"
	case n > MaxPasswordLen:
		return fmt.Sprintf("use %d characters or fewer", MaxPasswordLen), "maxLength"
	}
	lower := strings.ToLower(p)
	if common[lower] || common[strings.Join(strings.Fields(lower), "")] {
		return "the password is in the list of common passwords", "common-password"
	}
	if distinct(p) < 4 {
		return "use more different characters", "common-password"
	}
	return "", ""
}

func distinct(s string) int {
	seen := map[rune]bool{}
	for _, r := range s {
		seen[r] = true
	}
	return len(seen)
}
