// Package backup holds the backups of care-pi (docs/api/README.md section 13, docs/backups.md):
// the recovery key and the keys that come from it, the destinations, the restic runner,
// the pgBackRest repository, the state files, the health, and the locks.
package backup

import (
	"crypto/ed25519"
	"crypto/hkdf"
	"crypto/sha256"
	_ "embed"
	"encoding/base64"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"golang.org/x/crypto/ssh"
)

//go:embed bip39-english.txt
var wordlistText string

// Wordlist is the BIP-39 English word list (2048 words, SHA-256 2f5eed53…dbda).
var Wordlist = strings.Fields(wordlistText)

var (
	wordIndex   = map[string]int{}
	prefixIndex = map[string]int{}
)

func init() {
	if len(Wordlist) != 2048 {
		panic("backup: the BIP-39 word list must have 2048 words")
	}
	for i, w := range Wordlist {
		wordIndex[w] = i
		p := w
		if len(p) > 4 {
			p = p[:4]
		}
		prefixIndex[p] = i
	}
}

// Salt is the HKDF salt of the derivation v1 (docs/api/README.md section 13.3). A change of the
// derivation needs a new salt; the agent must keep v1 for a restore.
const Salt = "care-pi recovery key v1"

// HKDF info labels of the derivation v1.
const (
	InfoKeyID          = "key-id"
	InfoPGBackRestPass = "pgbackrest-repo-cipher-pass"
	InfoResticPassword = "restic-password"
	InfoSSHSeed        = "ssh-ed25519-seed"
)

// QRPrefix starts the QR payload of a key.
const QRPrefix = "CARE-PI-RK1:"

// KeySize is the size of a recovery key: 256 bits.
const KeySize = 32

// Key is a recovery key.
type Key [KeySize]byte

// NewKey reads a new key from r (crypto/rand.Reader).
func NewKey(r io.Reader) (Key, error) {
	var k Key
	if _, err := io.ReadFull(r, k[:]); err != nil {
		return k, fmt.Errorf("cannot read random bytes: %w", err)
	}
	return k, nil
}

// Hex returns the 64 hex digits of the stored file.
func (k Key) Hex() string { return hex.EncodeToString(k[:]) }

// ParseHex reads the 64 hex digits of the stored file.
func ParseHex(s string) (Key, error) {
	var k Key
	b, err := hex.DecodeString(strings.TrimSpace(s))
	if err != nil || len(b) != KeySize {
		return k, errors.New("the recovery key file must hold 64 hex digits")
	}
	copy(k[:], b)
	return k, nil
}

// Words returns the 24 BIP-39 words: 256 bits and an 8-bit checksum (the first byte of SHA-256).
func (k Key) Words() []string {
	sum := sha256.Sum256(k[:])
	bits := make([]byte, 0, KeySize+1)
	bits = append(bits, k[:]...)
	bits = append(bits, sum[0])
	out := make([]string, 24)
	for i := range 24 {
		idx := 0
		for j := range 11 {
			pos := i*11 + j
			bit := (bits[pos/8] >> (7 - uint(pos%8))) & 1
			idx = idx<<1 | int(bit)
		}
		out[i] = Wordlist[idx]
	}
	return out
}

// QRPayload returns the text of the QR code: CARE-PI-RK1: and the 24 words in upper case.
func (k Key) QRPayload() string {
	return QRPrefix + strings.ToUpper(strings.Join(k.Words(), " "))
}

// WordError tells which word of a word list is not a BIP-39 word.
type WordError struct {
	// Index is the position of the word, from 1.
	Index int
	Word  string
}

func (e *WordError) Error() string {
	return fmt.Sprintf("word %d (%q) is not a word of the recovery key list", e.Index, e.Word)
}

// ErrChecksum tells that the words have a wrong checksum: one word is wrong or the order is wrong.
var ErrChecksum = errors.New("the checksum of the recovery key is not correct: a word or the order of the words is wrong")

// ErrWordCount tells that the list does not have 24 words.
var ErrWordCount = errors.New("the recovery key has 24 words")

// WordIndex returns the index of a word in the list. It accepts the first 4 letters of a word.
func WordIndex(w string) (int, bool) {
	w = strings.ToLower(strings.TrimSpace(w))
	if i, ok := wordIndex[w]; ok {
		return i, true
	}
	if len(w) >= 4 {
		if i, ok := prefixIndex[w[:4]]; ok && strings.HasPrefix(Wordlist[i], w) {
			return i, true
		}
	}
	if len(w) == 3 {
		if i, ok := prefixIndex[w]; ok {
			return i, true
		}
	}
	return 0, false
}

// SameWord reports whether a typed word is the word at index i (the first 4 letters are enough).
func SameWord(typed string, i int) bool {
	j, ok := WordIndex(typed)
	return ok && j == i
}

// ParseWords reads the 24 words and checks the checksum.
func ParseWords(words []string) (Key, error) {
	var k Key
	if len(words) != 24 {
		return k, ErrWordCount
	}
	bits := make([]byte, KeySize+1)
	for i, w := range words {
		idx, ok := WordIndex(w)
		if !ok {
			return k, &WordError{Index: i + 1, Word: w}
		}
		for j := range 11 {
			if idx>>(10-j)&1 == 1 {
				pos := i*11 + j
				bits[pos/8] |= 1 << (7 - uint(pos%8))
			}
		}
	}
	copy(k[:], bits[:KeySize])
	sum := sha256.Sum256(k[:])
	if sum[0] != bits[KeySize] {
		return Key{}, ErrChecksum
	}
	return k, nil
}

var numberedWord = regexp.MustCompile(`(?m)(?:^|\s)([0-9]{1,2})\s+([A-Za-z]{3,8})\b`)

// ParseText reads a key from text: the 24 words separated by white space, the QR payload, or the
// text file of GET /backups/recovery-key/download (numbered words).
func ParseText(s string) (Key, error) {
	if i := strings.Index(s, QRPrefix); i >= 0 {
		return ParseWords(strings.Fields(s[i+len(QRPrefix):]))
	}
	if m := numberedWord.FindAllStringSubmatch(s, -1); len(m) >= 24 {
		byPos := map[int]string{}
		for _, x := range m {
			n, _ := strconv.Atoi(x[1])
			if n >= 1 && n <= 24 {
				if _, ok := WordIndex(x[2]); ok {
					byPos[n] = x[2]
				}
			}
		}
		if len(byPos) == 24 {
			words := make([]string, 24)
			for n, w := range byPos {
				words[n-1] = w
			}
			return ParseWords(words)
		}
	}
	return ParseWords(strings.Fields(s))
}

func (k Key) derive(info string, n int) []byte {
	out, err := hkdf.Key(sha256.New, k[:], []byte(Salt), info, n)
	if err != nil {
		panic("backup: hkdf: " + err.Error())
	}
	return out
}

// ID returns the key id: 4 bytes of HKDF as 8 lowercase hex digits. It is not secret.
func (k Key) ID() string { return hex.EncodeToString(k.derive(InfoKeyID, 4)) }

// CipherPass returns the pgBackRest repo1-cipher-pass (base64url without padding, 43 characters).
func (k Key) CipherPass() string {
	return base64.RawURLEncoding.EncodeToString(k.derive(InfoPGBackRestPass, 32))
}

// ResticPassword returns the restic password (base64url without padding).
func (k Key) ResticPassword() string {
	return base64.RawURLEncoding.EncodeToString(k.derive(InfoResticPassword, 32))
}

// SSHKey returns the ed25519 key of the agent for SFTP destinations (RFC 8032 seed from HKDF).
func (k Key) SSHKey() ed25519.PrivateKey {
	return ed25519.NewKeyFromSeed(k.derive(InfoSSHSeed, ed25519.SeedSize))
}

// SSHComment returns the comment of the SSH key: care-pi@<hostname>-<key id>.
func SSHComment(hostname, keyID string) string {
	h := strings.ToLower(hostname)
	if h == "" {
		h = "care"
	}
	return "care-pi@" + h + "-" + keyID
}

// SSHPublic returns the public key line "ssh-ed25519 <base64> <comment>" and its SHA-256 fingerprint.
func (k Key) SSHPublic(comment string) (line, fingerprint string) {
	pub, err := ssh.NewPublicKey(k.SSHKey().Public())
	if err != nil {
		panic("backup: ssh public key: " + err.Error())
	}
	line = strings.TrimSpace(string(ssh.MarshalAuthorizedKey(pub)))
	if comment != "" {
		line += " " + comment
	}
	return line, ssh.FingerprintSHA256(pub)
}

// SSHPrivatePEM returns the private key in the OpenSSH format for the ssh client.
func (k Key) SSHPrivatePEM(comment string) ([]byte, error) {
	block, err := ssh.MarshalPrivateKey(k.SSHKey(), comment)
	if err != nil {
		return nil, err
	}
	return pem.EncodeToMemory(block), nil
}

// ConfirmPositions returns 3 different positions from 1 to 24, in order, from the random source r.
func ConfirmPositions(r io.Reader) ([]int, error) {
	seen := map[int]bool{}
	var out []int
	buf := make([]byte, 1)
	for len(out) < 3 {
		if _, err := io.ReadFull(r, buf); err != nil {
			return nil, err
		}
		// 240 = 10 * 24: reject the rest, so each position has the same chance.
		if buf[0] >= 240 {
			continue
		}
		p := int(buf[0])%24 + 1
		if !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	sort.Ints(out)
	return out, nil
}
