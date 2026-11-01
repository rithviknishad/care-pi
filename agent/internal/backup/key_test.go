package backup

import (
	"bytes"
	"crypto/ed25519"
	"encoding/hex"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/rithviknishad/care-pi/agent/internal/paths"
)

// The vectors of docs/backups.md, section "Key derivation". A Python HKDF (hmac + hashlib) made
// them independently. A later agent must derive the same values from the same key.
var vectors = []struct {
	name, hex, words, id, pass, restic, seed string
}{
	{"sequence", "000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f",
		"abandon amount liar amount expire adjust cage candy arch gather drum bullet absurd math era live bid rhythm alien crouch range attend journey unaware",
		"b7e88184", "8ehnRmqnBwlAHh3wHaHbhT0WDfRXdQl9wL2Ml-WxTx8", "TIH9b_g8wSQxca3ZUnSt9URb00TrkhURmbusKFYIRnA",
		"a0e91ddc24e8bb314d27dd70924937f5c5c5451d58fc7eca25183648e8d19238"},
	{"zero", strings.Repeat("00", 32),
		strings.Repeat("abandon ", 23) + "art",
		"9da6f546", "5LYWK2aQES1l8SaZ3Fb_YV1unqmX6teWmWWoOFU97f0", "RBb5bogfruDBWIq17yBBcELBlGmD1PX0PfVc4I3e0B8",
		"66c06c14a59e398b32ea510289b8ace60133ebd6b86613e00442a3f93ab06d71"},
	{"ff", strings.Repeat("ff", 32),
		strings.Repeat("zoo ", 23) + "vote",
		"5ec9d612", "OJ_BeB_RzCBsVZrGEy5OoLpYXe2jUgmCubde5CMaMrI", "8D4je885KN2LzuP8KLWiimPnP0S_HBXjO5eVFYPAZgo",
		"4ae31a05db820291944cf81850daa69ad184c765b87bd4fafe88890379912b04"},
}

func TestVectors(t *testing.T) {
	for _, v := range vectors {
		t.Run(v.name, func(t *testing.T) {
			k, err := ParseHex(v.hex)
			if err != nil {
				t.Fatal(err)
			}
			if got := strings.Join(k.Words(), " "); got != v.words {
				t.Fatalf("words %q", got)
			}
			back, err := ParseWords(strings.Fields(v.words))
			if err != nil || back != k {
				t.Fatalf("parse words: %v", err)
			}
			if k.ID() != v.id || k.CipherPass() != v.pass || k.ResticPassword() != v.restic {
				t.Fatalf("id %s pass %s restic %s", k.ID(), k.CipherPass(), k.ResticPassword())
			}
			if len(k.CipherPass()) != 43 {
				t.Fatalf("cipher pass length %d", len(k.CipherPass()))
			}
			if got := hex.EncodeToString(k.SSHKey().Seed()); got != v.seed {
				t.Fatalf("seed %s", got)
			}
		})
	}
}

// TestSSHVector pins the public key of the sequence key (docs/backups.md).
func TestSSHVector(t *testing.T) {
	k, _ := ParseHex(vectors[0].hex)
	line, fp := k.SSHPublic(SSHComment("care", k.ID()))
	if line != "ssh-ed25519 "+sshVectorBase64+" care-pi@care-b7e88184" || fp != sshVectorFingerprint {
		t.Fatalf("ssh public key %q %q", line, fp)
	}
	pemKey, err := k.SSHPrivatePEM("x")
	if err != nil || !bytes.Contains(pemKey, []byte("OPENSSH PRIVATE KEY")) {
		t.Fatalf("pem %v %s", err, pemKey)
	}
	if !ed25519.PublicKey(k.SSHKey().Public().(ed25519.PublicKey)).Equal(ed25519.NewKeyFromSeed(k.SSHKey().Seed()).Public()) {
		t.Fatal("public key")
	}
}

const (
	sshVectorBase64      = "AAAAC3NzaC1lZDI1NTE5AAAAIKyYwDUS5/0JpEGJNwaAVmFIddIgQ30zNhNrwWN8t/9T"
	sshVectorFingerprint = "SHA256:3uFNMWjcMszc+nqlp7ZyL4uK897yA66hAl+r4Qa300k"
)

func TestParseWordsErrors(t *testing.T) {
	k, _ := ParseHex(vectors[0].hex)
	w := k.Words()
	short := make([]string, 24)
	for i, x := range w {
		short[i] = x[:min(4, len(x))]
	}
	if got, err := ParseWords(short); err != nil || got != k {
		t.Fatalf("first 4 letters: %v", err)
	}
	if _, err := ParseWords(w[:23]); !errors.Is(err, ErrWordCount) {
		t.Fatalf("count: %v", err)
	}
	bad := append([]string(nil), w...)
	bad[4] = "notaword"
	var we *WordError
	if _, err := ParseWords(bad); !errors.As(err, &we) || we.Index != 5 {
		t.Fatalf("word error: %v", err)
	}
	swap := append([]string(nil), w...)
	swap[0], swap[1] = swap[1], swap[0]
	if _, err := ParseWords(swap); !errors.Is(err, ErrChecksum) {
		t.Fatalf("checksum: %v", err)
	}
}

func TestParseText(t *testing.T) {
	k, _ := ParseHex(vectors[0].hex)
	for name, s := range map[string]string{
		"qr":    k.QRPayload(),
		"words": "  " + strings.Join(k.Words(), "\n") + "\n",
		"file":  Text(k, "Sunrise Clinic", time.Date(2026, 10, 30, 0, 0, 0, 0, time.UTC)),
	} {
		got, err := ParseText(s)
		if err != nil || got != k {
			t.Fatalf("%s: %v", name, err)
		}
	}
	if !strings.HasPrefix(k.QRPayload(), "CARE-PI-RK1:ABANDON AMOUNT LIAR") {
		t.Fatal(k.QRPayload())
	}
}

func TestTextFile(t *testing.T) {
	k, _ := ParseHex(vectors[0].hex)
	txt := Text(k, "Sunrise Clinic", time.Date(2026, 10, 30, 10, 0, 0, 0, time.UTC))
	for _, want := range []string{"Clinic: Sunrise Clinic\n", "Key id: b7e88184\n", "Made: 2026-10-30\n", " 1 abandon", "24 unaware\n"} {
		if !strings.Contains(txt, want) {
			t.Fatalf("missing %q in\n%s", want, txt)
		}
	}
	for _, l := range strings.Split(txt, "\n") {
		if strings.HasSuffix(l, " ") {
			t.Fatalf("trailing space in %q", l)
		}
	}
	if FileName("Sunrise Clinic!") != "care-recovery-sunrise-clinic.txt" || FileName("") != "care-recovery-care.txt" {
		t.Fatal(FileName("Sunrise Clinic!"))
	}
}

func TestConfirmPositions(t *testing.T) {
	r := bytes.NewReader([]byte{250, 23, 23, 0, 47, 5})
	pos, err := ConfirmPositions(r)
	if err != nil || len(pos) != 3 || pos[0] != 1 || pos[1] != 6 || pos[2] != 24 {
		t.Fatalf("%v %v", pos, err)
	}
}

func TestKeyStore(t *testing.T) {
	p := paths.New(t.TempDir())
	now := time.Date(2026, 10, 30, 10, 0, 0, 0, time.UTC)
	ks := &KeyStore{Paths: p, Now: func() time.Time { return now }}
	if st := ks.Status(); st.State != KeyNone || st.KeyID != nil {
		t.Fatalf("%+v", st)
	}
	k, which, m, err := ks.Create()
	if err != nil || which != Active || len(m.ConfirmPositions) != 3 {
		t.Fatalf("%v %s %+v", err, which, m)
	}
	st := ks.Status()
	if st.State != KeyUnconfirmed || *st.KeyID != k.ID() || len(st.ConfirmPositions) != 3 {
		t.Fatalf("%+v", st)
	}
	words := k.Words()
	in := []WordInput{}
	for _, pos := range m.ConfirmPositions {
		in = append(in, WordInput{Index: pos, Word: words[pos-1]})
	}
	wrong := append([]WordInput(nil), in...)
	wrong[1].Word = "zzzz"
	var me *MismatchError
	if _, err := ks.Confirm(Active, wrong); !errors.As(err, &me) || len(me.Wrong) != 1 || me.Wrong[0] != 1 {
		t.Fatalf("mismatch: %v", err)
	}
	other := append([]WordInput(nil), in...)
	other[0].Index = other[0].Index%24 + 1
	if other[0].Index == in[1].Index {
		other[0].Index = other[0].Index%24 + 1
	}
	var pe *PositionsError
	if _, err := ks.Confirm(Active, other); !errors.As(err, &pe) {
		t.Fatalf("positions: %v", err)
	}
	if _, err := ks.Confirm(Active, in); err != nil {
		t.Fatal(err)
	}
	if st := ks.Status(); st.State != KeyConfirmed || st.ConfirmPositions != nil {
		t.Fatalf("%+v", st)
	}
	// A second create is the pending key.
	k2, which, _, err := ks.Create()
	if err != nil || which != Pending {
		t.Fatal(which, err)
	}
	if st := ks.Status(); st.Pending == nil || st.Pending.KeyID != k2.ID() {
		t.Fatalf("%+v", st)
	}
	if err := ks.Activate(); err != nil {
		t.Fatal(err)
	}
	got, _, err := ks.Get(Active)
	if err != nil || got != k2 || ks.Exists(Pending) {
		t.Fatalf("activate: %v", err)
	}
	if err := ks.SetActive(k); err != nil {
		t.Fatal(err)
	}
	if st := ks.Status(); st.State != KeyConfirmed || *st.KeyID != k.ID() {
		t.Fatalf("%+v", st)
	}
}
