package auth

import (
	"crypto/sha256"
	"crypto/sha512"
	"crypto/subtle"
	"errors"
	"hash"
	"strconv"
	"strings"
)

// shaCrypt implements the SHA-crypt hashes of glibc ($5$ SHA-256 and $6$ SHA-512, Ulrich Drepper's
// specification). Raspberry Pi Imager writes a $5$ hash into the cloud-init user-data, and
// cloud-init copies it to /etc/shadow. yescrypt ($y$, the default of passwd on trixie) is not supported.

const (
	shaRoundsDefault = 5000
	shaRoundsMin     = 1000
	shaRoundsMax     = 999999999
	shaSaltMax       = 16
	cryptB64         = "./0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"
)

var perm256 = [][3]int{
	{0, 10, 20}, {21, 1, 11}, {12, 22, 2}, {3, 13, 23}, {24, 4, 14},
	{15, 25, 5}, {6, 16, 26}, {27, 7, 17}, {18, 28, 8}, {9, 19, 29},
}

var perm512 = [][3]int{
	{0, 21, 42}, {22, 43, 1}, {44, 2, 23}, {3, 24, 45}, {25, 46, 4}, {47, 5, 26}, {6, 27, 48},
	{28, 49, 7}, {50, 8, 29}, {9, 30, 51}, {31, 52, 10}, {53, 11, 32}, {12, 33, 54}, {34, 55, 13},
	{56, 14, 35}, {15, 36, 57}, {37, 58, 16}, {59, 17, 38}, {18, 39, 60}, {40, 61, 19}, {62, 20, 41},
}

// errUnsupportedHash tells that the shadow hash is not $5$ or $6$.
var errUnsupportedHash = errors.New("the password hash is not SHA-crypt ($5$ or $6$)")

// shaCrypt computes the hash of password with the setting (the hash or its "$5$[rounds=N$]salt" prefix).
func shaCrypt(password, setting string) (string, error) {
	var newHash func() hash.Hash
	var prefix string
	switch {
	case strings.HasPrefix(setting, "$5$"):
		newHash, prefix = sha256.New, "$5$"
	case strings.HasPrefix(setting, "$6$"):
		newHash, prefix = sha512.New, "$6$"
	default:
		return "", errUnsupportedHash
	}
	rest := setting[len(prefix):]
	rounds, custom := shaRoundsDefault, false
	if r, ok := strings.CutPrefix(rest, "rounds="); ok {
		num, after, found := strings.Cut(r, "$")
		if !found {
			return "", errors.New("the rounds field of the hash is not terminated")
		}
		n, err := strconv.Atoi(num)
		if err != nil {
			return "", errors.New("the rounds field of the hash is not a number")
		}
		rounds, custom, rest = min(max(n, shaRoundsMin), shaRoundsMax), true, after
	}
	salt, _, _ := strings.Cut(rest, "$")
	if len(salt) > shaSaltMax {
		salt = salt[:shaSaltMax]
	}
	p, s := []byte(password), []byte(salt)

	hb := newHash()
	hb.Write(p)
	hb.Write(s)
	hb.Write(p)
	b := hb.Sum(nil)
	size := len(b)

	ha := newHash()
	ha.Write(p)
	ha.Write(s)
	for n := len(p); n > 0; n -= size {
		ha.Write(b[:min(n, size)])
	}
	for n := len(p); n > 0; n >>= 1 {
		if n&1 != 0 {
			ha.Write(b)
		} else {
			ha.Write(p)
		}
	}
	a := ha.Sum(nil)

	hp := newHash()
	for range len(p) {
		hp.Write(p)
	}
	dp := hp.Sum(nil)
	pBytes := make([]byte, 0, len(p))
	for n := len(p); n > 0; n -= size {
		pBytes = append(pBytes, dp[:min(n, size)]...)
	}

	hs := newHash()
	for range 16 + int(a[0]) {
		hs.Write(s)
	}
	ds := hs.Sum(nil)
	sBytes := make([]byte, 0, len(s))
	for n := len(s); n > 0; n -= size {
		sBytes = append(sBytes, ds[:min(n, size)]...)
	}

	c := a
	for i := range rounds {
		h := newHash()
		if i&1 != 0 {
			h.Write(pBytes)
		} else {
			h.Write(c)
		}
		if i%3 != 0 {
			h.Write(sBytes)
		}
		if i%7 != 0 {
			h.Write(pBytes)
		}
		if i&1 != 0 {
			h.Write(c)
		} else {
			h.Write(pBytes)
		}
		c = h.Sum(nil)
	}

	var out strings.Builder
	out.WriteString(prefix)
	if custom {
		out.WriteString("rounds=" + strconv.Itoa(rounds) + "$")
	}
	out.WriteString(salt)
	out.WriteByte('$')
	enc := func(b2, b1, b0 byte, n int) {
		w := uint32(b2)<<16 | uint32(b1)<<8 | uint32(b0)
		for range n {
			out.WriteByte(cryptB64[w&0x3f])
			w >>= 6
		}
	}
	if size == sha256.Size {
		for _, t := range perm256 {
			enc(c[t[0]], c[t[1]], c[t[2]], 4)
		}
		enc(0, c[31], c[30], 3)
	} else {
		for _, t := range perm512 {
			enc(c[t[0]], c[t[1]], c[t[2]], 4)
		}
		enc(0, 0, c[63], 2)
	}
	return out.String(), nil
}

// verifyShaCrypt compares password with a $5$ or $6$ hash in constant time.
func verifyShaCrypt(password, hashed string) (bool, error) {
	got, err := shaCrypt(password, hashed)
	if err != nil {
		return false, err
	}
	return subtle.ConstantTimeCompare([]byte(got), []byte(hashed)) == 1, nil
}
