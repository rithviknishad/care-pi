package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"strings"
)

// crockford is the Crockford base32 alphabet (no I, L, O, U).
const crockford = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

// Code lengths: a recovery code has 10 characters (50 bits), a claim code 12 (60 bits).
const (
	RecoveryCodeLen  = 10
	ClaimCodeLen     = 12
	RecoveryCodeSets = 8
)

// randomCode returns n random Crockford base32 characters.
func randomCode(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("cannot read random bytes: %w", err)
	}
	out := make([]byte, n)
	for i, v := range b {
		out[i] = crockford[v&31]
	}
	return string(out), nil
}

// group puts a hyphen after each size characters.
func group(s string, size int) string {
	var parts []string
	for len(s) > size {
		parts = append(parts, s[:size])
		s = s[size:]
	}
	return strings.Join(append(parts, s), "-")
}

// NewRecoveryCode returns a code such as 7KQ2M-X9PDA.
func NewRecoveryCode() (string, error) {
	c, err := randomCode(RecoveryCodeLen)
	if err != nil {
		return "", err
	}
	return group(c, 5), nil
}

// NewClaimCode returns a code such as 4F7K-Q2MX-9PDA.
func NewClaimCode() (string, error) {
	c, err := randomCode(ClaimCodeLen)
	if err != nil {
		return "", err
	}
	return group(c, 4), nil
}

// NormalizeCode removes spaces and hyphens, uses upper case, and reads I and L as 1 and O as 0.
// It returns "" when a character is not Crockford base32.
func NormalizeCode(s string) string {
	var b strings.Builder
	for _, r := range strings.ToUpper(s) {
		switch r {
		case ' ', '-', '\t':
			continue
		case 'I', 'L':
			r = '1'
		case 'O':
			r = '0'
		}
		if !strings.ContainsRune(crockford, r) {
			return ""
		}
		b.WriteRune(r)
	}
	return b.String()
}

// hashCode returns the SHA-256 of a normalized code. The codes have 50 or more random bits,
// and the address limits apply, so a fast hash is enough.
func hashCode(normalized string) string {
	sum := sha256.Sum256([]byte("care-pi-code:" + normalized))
	return hex.EncodeToString(sum[:])
}

func equalStrings(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}
