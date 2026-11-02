// Package minisign verifies minisign signatures (https://jedisct1.github.io/minisign/): the optional signatures of
// index.json and of the solution locks (Phase 7, docs/solutions.md section 18). It has no dependency outside the
// standard library and golang.org/x/crypto.
//
// Formats:
//
//	public key file:  "untrusted comment: …\n" + base64("Ed" || key id (8) || Ed25519 public key (32))
//	signature file:   "untrusted comment: …\n" + base64(alg (2) || key id (8) || signature (64)) + "\n" +
//	                  "trusted comment: …\n" + base64(global signature (64))
//
// alg "Ed" signs the file; alg "ED" (the default of minisign 0.11) signs the BLAKE2b-512 hash of the file. The global
// signature signs the signature and the trusted comment, so a changed comment fails too.
package minisign

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"

	"golang.org/x/crypto/blake2b"
)

// Algorithms of a signature.
const (
	algLegacy  = "Ed"
	algHashed  = "ED"
	commentPre = "untrusted comment:"
	trustedPre = "trusted comment: "
)

// PublicKey is a minisign public key.
type PublicKey struct {
	ID  [8]byte
	Key ed25519.PublicKey
}

// KeyID returns the key id as minisign prints it (16 upper-case hex digits of the little-endian number).
func (k PublicKey) KeyID() string { return keyID(k.ID) }

func keyID(id [8]byte) string {
	return strings.ToUpper(fmt.Sprintf("%016x", binary.LittleEndian.Uint64(id[:])))
}

// Signature is a minisign signature file.
type Signature struct {
	Algorithm      string
	KeyID          [8]byte
	Sig            []byte
	TrustedComment string
	GlobalSig      []byte
}

// lines returns the lines of a file without the empty lines and the carriage returns.
func lines(data []byte) []string {
	var out []string
	for _, l := range strings.Split(strings.ReplaceAll(string(data), "\r", ""), "\n") {
		if strings.TrimSpace(l) != "" {
			out = append(out, l)
		}
	}
	return out
}

// ParsePublicKey reads a public key file, or only its base64 line.
func ParsePublicKey(data []byte) (PublicKey, error) {
	var k PublicKey
	ls := lines(data)
	if len(ls) > 0 && strings.HasPrefix(ls[0], commentPre) {
		ls = ls[1:]
	}
	if len(ls) != 1 {
		return k, errors.New("minisign: a public key has one base64 line")
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(ls[0]))
	if err != nil {
		return k, fmt.Errorf("minisign: public key: %w", err)
	}
	if len(raw) != 2+8+ed25519.PublicKeySize || string(raw[:2]) != algLegacy {
		return k, errors.New("minisign: not an Ed25519 public key")
	}
	copy(k.ID[:], raw[2:10])
	k.Key = ed25519.PublicKey(append([]byte{}, raw[10:]...))
	return k, nil
}

// ParseSignature reads a signature file.
func ParseSignature(data []byte) (Signature, error) {
	var s Signature
	ls := lines(data)
	if len(ls) != 4 || !strings.HasPrefix(ls[0], commentPre) || !strings.HasPrefix(ls[2], trustedPre) {
		return s, errors.New("minisign: a signature file has 4 lines: the comment, the signature, the trusted comment, the global signature")
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(ls[1]))
	if err != nil {
		return s, fmt.Errorf("minisign: signature: %w", err)
	}
	if len(raw) != 2+8+ed25519.SignatureSize {
		return s, errors.New("minisign: the signature has a wrong length")
	}
	s.Algorithm = string(raw[:2])
	if s.Algorithm != algLegacy && s.Algorithm != algHashed {
		return s, fmt.Errorf("minisign: unknown algorithm %q", s.Algorithm)
	}
	copy(s.KeyID[:], raw[2:10])
	s.Sig = append([]byte{}, raw[10:]...)
	s.TrustedComment = strings.TrimPrefix(ls[2], trustedPre)
	if s.GlobalSig, err = base64.StdEncoding.DecodeString(strings.TrimSpace(ls[3])); err != nil || len(s.GlobalSig) != ed25519.SignatureSize {
		return s, errors.New("minisign: the global signature is not valid base64 of 64 bytes")
	}
	return s, nil
}

// ErrKeyID tells that the signature has the key id of another key.
var ErrKeyID = errors.New("minisign: the signature has the key id of another key")

// Verify checks the signature of msg with k.
func (k PublicKey) Verify(msg []byte, s Signature) error {
	if s.KeyID != k.ID {
		return fmt.Errorf("%w (%s, not %s)", ErrKeyID, keyID(s.KeyID), k.KeyID())
	}
	signed := msg
	if s.Algorithm == algHashed {
		h := blake2b.Sum512(msg)
		signed = h[:]
	}
	if !ed25519.Verify(k.Key, signed, s.Sig) {
		return errors.New("minisign: the signature does not match the file")
	}
	if !ed25519.Verify(k.Key, append(append([]byte{}, s.Sig...), []byte(s.TrustedComment)...), s.GlobalSig) {
		return errors.New("minisign: the trusted comment does not match its signature")
	}
	return nil
}

// Keys is a set of trusted public keys.
type Keys []PublicKey

// Verify checks sigFile for msg with the key of its key id. It returns the key that signed.
func (ks Keys) Verify(msg, sigFile []byte) (PublicKey, error) {
	s, err := ParseSignature(sigFile)
	if err != nil {
		return PublicKey{}, err
	}
	for _, k := range ks {
		if k.ID == s.KeyID {
			return k, k.Verify(msg, s)
		}
	}
	return PublicKey{}, fmt.Errorf("%w: no trusted key has the id %s", ErrKeyID, keyID(s.KeyID))
}

// IDs returns the key ids.
func (ks Keys) IDs() []string {
	out := make([]string, 0, len(ks))
	for _, k := range ks {
		out = append(out, k.KeyID())
	}
	return out
}

// GenerateKey makes a key pair (tests and the documentation of the key ceremony; CI signs with the minisign tool).
func GenerateKey(r io.Reader) (PublicKey, ed25519.PrivateKey, error) {
	pub, priv, err := ed25519.GenerateKey(r)
	if err != nil {
		return PublicKey{}, nil, err
	}
	var id [8]byte
	if _, err := io.ReadFull(r, id[:]); err != nil {
		return PublicKey{}, nil, err
	}
	return PublicKey{ID: id, Key: pub}, priv, nil
}

// Encode returns the public key file.
func (k PublicKey) Encode() []byte {
	raw := append(append([]byte(algLegacy), k.ID[:]...), k.Key...)
	return []byte(fmt.Sprintf("untrusted comment: minisign public key %s\n%s\n", k.KeyID(), base64.StdEncoding.EncodeToString(raw)))
}

// Sign makes a signature file of msg (alg ED), as "minisign -S -t trustedComment" does.
func Sign(priv ed25519.PrivateKey, id [8]byte, msg []byte, trustedComment string) []byte {
	h := blake2b.Sum512(msg)
	sig := ed25519.Sign(priv, h[:])
	global := ed25519.Sign(priv, append(append([]byte{}, sig...), []byte(trustedComment)...))
	var b bytes.Buffer
	_, _ = fmt.Fprintf(&b, "untrusted comment: signature from minisign secret key\n%s\n%s%s\n%s\n",
		base64.StdEncoding.EncodeToString(append(append([]byte(algHashed), id[:]...), sig...)),
		trustedPre, trustedComment, base64.StdEncoding.EncodeToString(global))
	return b.Bytes()
}

// HexID returns the key id in the hex of its bytes (for a log line).
func (k PublicKey) HexID() string { return hex.EncodeToString(k.ID[:]) }
