package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// TOTP parameters (RFC 6238): SHA-1, 6 digits, 30 s.
const (
	TOTPDigits = 6
	TOTPPeriod = 30
	totpBytes  = 20
)

var b32 = base32.StdEncoding.WithPadding(base32.NoPadding)

// NewTOTPSecret returns 160 random bits in base32 without padding (32 characters).
func NewTOTPSecret() (string, error) {
	b := make([]byte, totpBytes)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("cannot read random bytes: %w", err)
	}
	return b32.EncodeToString(b), nil
}

// TOTPURI returns the otpauth URI for a QR code.
func TOTPURI(secret, host string) string {
	label := url.PathEscape("care-pi:" + host)
	q := url.Values{}
	q.Set("secret", secret)
	q.Set("issuer", "care-pi")
	q.Set("algorithm", "SHA1")
	q.Set("digits", fmt.Sprint(TOTPDigits))
	q.Set("period", fmt.Sprint(TOTPPeriod))
	return "otpauth://totp/" + label + "?" + q.Encode()
}

// hotp returns the RFC 4226 code for the counter.
func hotp(key []byte, counter uint64) string {
	var msg [8]byte
	binary.BigEndian.PutUint64(msg[:], counter)
	m := hmac.New(sha1.New, key)
	m.Write(msg[:])
	sum := m.Sum(nil)
	off := sum[len(sum)-1] & 0x0f
	v := binary.BigEndian.Uint32(sum[off:off+4]) & 0x7fffffff
	return fmt.Sprintf("%06d", v%1000000)
}

// TOTPCode returns the code of the 30 s step at t.
func TOTPCode(secret string, t time.Time) (string, error) {
	key, err := b32.DecodeString(strings.ToUpper(secret))
	if err != nil {
		return "", fmt.Errorf("the TOTP secret is not valid base32: %w", err)
	}
	return hotp(key, uint64(t.Unix())/TOTPPeriod), nil
}

// VerifyTOTP accepts the code of the step at now and of the step before and after it.
// A step at or before lastStep is a replay. It returns the matched step.
func VerifyTOTP(secret, code string, now time.Time, lastStep int64) (int64, bool) {
	key, err := b32.DecodeString(strings.ToUpper(secret))
	if err != nil || len(code) != TOTPDigits {
		return 0, false
	}
	cur := now.Unix() / TOTPPeriod
	for _, s := range []int64{cur - 1, cur, cur + 1} {
		if s <= lastStep {
			continue
		}
		if equalStrings(hotp(key, uint64(s)), code) {
			return s, true
		}
	}
	return 0, false
}
