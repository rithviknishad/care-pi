package backup

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/rithviknishad/care-pi/agent/internal/lock"
	"github.com/rithviknishad/care-pi/agent/internal/paths"
)

// Which key.
const (
	Active  = "active"
	Pending = "pending"
)

// Key states (schema RecoveryKeyStatus.state).
const (
	KeyNone        = "none"
	KeyUnconfirmed = "unconfirmed"
	KeyConfirmed   = "confirmed"
)

// KeyMeta is the state of one key, without the secret. It is in state/backups/key.json.
type KeyMeta struct {
	KeyID            string     `json:"key_id"`
	CreatedAt        time.Time  `json:"created_at"`
	ConfirmedAt      *time.Time `json:"confirmed_at,omitempty"`
	ConfirmPositions []int      `json:"confirm_positions,omitempty"`
}

type keyFile struct {
	Active        *KeyMeta `json:"active,omitempty"`
	Pending       *KeyMeta `json:"pending,omitempty"`
	RotationJobID string   `json:"rotation_job_id,omitempty"`
}

// PendingStatus is the pending key of a rotation (schema RecoveryKeyStatus.pending).
type PendingStatus struct {
	KeyID            string     `json:"key_id"`
	CreatedAt        time.Time  `json:"created_at"`
	ConfirmedAt      *time.Time `json:"confirmed_at"`
	ConfirmPositions []int      `json:"confirm_positions"`
}

// KeyStatus is the state of the recovery key (schema RecoveryKeyStatus). The service fills
// LocalRepository.
type KeyStatus struct {
	State            string         `json:"state"`
	KeyID            *string        `json:"key_id"`
	CreatedAt        *time.Time     `json:"created_at"`
	ConfirmedAt      *time.Time     `json:"confirmed_at"`
	ConfirmPositions []int          `json:"confirm_positions"`
	Pending          *PendingStatus `json:"pending"`
	LocalRepository  string         `json:"local_repository"`
	RotationJobID    *string        `json:"rotation_job_id"`
}

// KeyStore reads and writes the recovery key files and their state.
type KeyStore struct {
	Paths paths.Paths
	// Rand is the random source. nil uses crypto/rand.
	Rand io.Reader
	Now  func() time.Time
	mu   sync.Mutex
}

// ErrNoKey tells that the key does not exist.
var ErrNoKey = errors.New("no recovery key")

func (s *KeyStore) rand() io.Reader {
	if s.Rand != nil {
		return s.Rand
	}
	return rand.Reader
}

func (s *KeyStore) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC().Truncate(time.Second)
	}
	return time.Now().UTC().Truncate(time.Second)
}

func (s *KeyStore) file(which string) string {
	if which == Pending {
		return s.Paths.Abs(paths.PendingKeyFile)
	}
	return s.Paths.Abs(paths.RecoveryKeyFile)
}

func (s *KeyStore) metaPath() string { return s.Paths.Abs(paths.BackupStateDir + "/key.json") }

func (s *KeyStore) readMeta() keyFile {
	var f keyFile
	if data, err := os.ReadFile(s.metaPath()); err == nil {
		_ = json.Unmarshal(data, &f)
	}
	return f
}

func (s *KeyStore) writeMeta(f keyFile) error {
	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.metaPath()), 0o700); err != nil {
		return err
	}
	return lock.WriteFileAtomic(s.metaPath(), data, 0o600)
}

func (s *KeyStore) readKey(which string) (Key, error) {
	data, err := os.ReadFile(s.file(which))
	if errors.Is(err, fs.ErrNotExist) {
		return Key{}, ErrNoKey
	}
	if err != nil {
		return Key{}, fmt.Errorf("cannot read the recovery key: %w", err)
	}
	return ParseHex(string(data))
}

func (s *KeyStore) writeKey(which string, k Key) error {
	dir := filepath.Dir(s.file(which))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	return lock.WriteFileAtomic(s.file(which), []byte(k.Hex()+"\n"), 0o600)
}

// Get returns the key and its state. A key file without a state (for example a copy by hand)
// gets a new unconfirmed state.
func (s *KeyStore) Get(which string) (Key, *KeyMeta, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.get(which)
}

func (s *KeyStore) get(which string) (Key, *KeyMeta, error) {
	k, err := s.readKey(which)
	if err != nil {
		return k, nil, err
	}
	f := s.readMeta()
	m := f.Active
	if which == Pending {
		m = f.Pending
	}
	if m == nil || m.KeyID != k.ID() {
		pos, err := ConfirmPositions(s.rand())
		if err != nil {
			return k, nil, err
		}
		created := s.now()
		if info, err := os.Stat(s.file(which)); err == nil {
			created = info.ModTime().UTC().Truncate(time.Second)
		}
		m = &KeyMeta{KeyID: k.ID(), CreatedAt: created, ConfirmPositions: pos}
		if which == Pending {
			f.Pending = m
		} else {
			f.Active = m
		}
		if err := s.writeMeta(f); err != nil {
			return k, nil, err
		}
	}
	return k, m, nil
}

// Exists reports whether the key file exists.
func (s *KeyStore) Exists(which string) bool {
	_, err := os.Stat(s.file(which))
	return err == nil
}

// Create makes a new key. Without an active key it is the active key; else it is the pending key
// of a rotation and replaces an older pending key. It returns the key, which key it is, and the state.
func (s *KeyStore) Create() (Key, string, *KeyMeta, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	k, err := NewKey(s.rand())
	if err != nil {
		return k, "", nil, err
	}
	pos, err := ConfirmPositions(s.rand())
	if err != nil {
		return k, "", nil, err
	}
	which := Active
	if s.Exists(Active) {
		which = Pending
	}
	m := &KeyMeta{KeyID: k.ID(), CreatedAt: s.now(), ConfirmPositions: pos}
	f := s.readMeta()
	if which == Pending {
		f.Pending = m
	} else {
		f.Active = m
	}
	if err := s.writeKey(which, k); err != nil {
		return k, "", nil, err
	}
	if err := s.writeMeta(f); err != nil {
		return k, "", nil, err
	}
	return k, which, m, nil
}

// WordInput is one typed word and its position (schema RecoveryKeyWord).
type WordInput struct {
	Index int    `json:"index"`
	Word  string `json:"word"`
}

// MismatchError lists the typed words that are not the words of the key (indexes into the request).
type MismatchError struct{ Wrong []int }

func (e *MismatchError) Error() string { return "a typed word is not the word of the recovery key" }

// PositionsError tells that the positions are not the confirm positions.
type PositionsError struct{ Want []int }

func (e *PositionsError) Error() string {
	return fmt.Sprintf("type the words at the positions %v", e.Want)
}

// Confirm checks the 3 typed words against the confirm positions of the key. Correct words make
// the key confirmed. A confirmed key needs no words (nil words is then not an error).
func (s *KeyStore) Confirm(which string, words []WordInput) (*KeyMeta, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	k, m, err := s.get(which)
	if err != nil {
		return nil, err
	}
	if m.ConfirmedAt != nil && words == nil {
		return m, nil
	}
	if len(words) != 3 {
		return nil, &PositionsError{Want: m.ConfirmPositions}
	}
	want := m.ConfirmPositions
	if m.ConfirmedAt != nil {
		want = nil
	}
	all := k.Words()
	var wrong []int
	for i, w := range words {
		if want != nil && (i >= len(want) || w.Index != want[i]) {
			return nil, &PositionsError{Want: want}
		}
		if w.Index < 1 || w.Index > 24 {
			return nil, &PositionsError{Want: want}
		}
		idx, _ := WordIndex(all[w.Index-1])
		if !SameWord(w.Word, idx) {
			wrong = append(wrong, i)
		}
	}
	if len(wrong) > 0 {
		return nil, &MismatchError{Wrong: wrong}
	}
	if m.ConfirmedAt == nil {
		t := s.now()
		m.ConfirmedAt = &t
		m.ConfirmPositions = nil
		f := s.readMeta()
		if which == Pending {
			f.Pending = m
		} else {
			f.Active = m
		}
		if err := s.writeMeta(f); err != nil {
			return nil, err
		}
	}
	return m, nil
}

// SetActive writes k as the confirmed active key (a restore from backup: the operator typed it).
// It removes a pending key.
func (s *KeyStore) SetActive(k Key) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.writeKey(Active, k); err != nil {
		return err
	}
	_ = os.Remove(s.file(Pending))
	t := s.now()
	f := s.readMeta()
	f.Active = &KeyMeta{KeyID: k.ID(), CreatedAt: t, ConfirmedAt: &t}
	f.Pending = nil
	return s.writeMeta(f)
}

// Activate makes the pending key the active key (the end of a rotation).
func (s *KeyStore) Activate() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	k, m, err := s.get(Pending)
	if err != nil {
		return err
	}
	if err := s.writeKey(Active, k); err != nil {
		return err
	}
	if err := os.Remove(s.file(Pending)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	f := s.readMeta()
	f.Active, f.Pending = m, nil
	return s.writeMeta(f)
}

// SetRotationJob records the last recovery-key-rotate job.
func (s *KeyStore) SetRotationJob(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	f := s.readMeta()
	f.RotationJobID = id
	return s.writeMeta(f)
}

// Status returns the state of the keys without a secret. LocalRepository is left empty.
func (s *KeyStore) Status() KeyStatus {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := KeyStatus{State: KeyNone}
	if _, m, err := s.get(Active); err == nil {
		id, created := m.KeyID, m.CreatedAt
		st.KeyID, st.CreatedAt, st.ConfirmedAt = &id, &created, m.ConfirmedAt
		st.State = KeyUnconfirmed
		if m.ConfirmedAt != nil {
			st.State = KeyConfirmed
		} else {
			st.ConfirmPositions = m.ConfirmPositions
		}
	}
	if _, m, err := s.get(Pending); err == nil {
		p := &PendingStatus{KeyID: m.KeyID, CreatedAt: m.CreatedAt, ConfirmedAt: m.ConfirmedAt}
		if m.ConfirmedAt == nil {
			p.ConfirmPositions = m.ConfirmPositions
		}
		st.Pending = p
	}
	if f := s.readMeta(); f.RotationJobID != "" {
		id := f.RotationJobID
		st.RotationJobID = &id
	}
	return st
}

// FileName returns the name of the text file: care-recovery-<clinic>.txt.
func FileName(displayName string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(displayName) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			dash = false
			continue
		}
		if !dash && b.Len() > 0 {
			b.WriteByte('-')
			dash = true
		}
	}
	name := strings.Trim(b.String(), "-")
	if len(name) > 64 {
		name = strings.Trim(name[:64], "-")
	}
	if name == "" {
		name = "care"
	}
	return "care-recovery-" + name + ".txt"
}

// Text returns the text file of the key: the clinic, the key id, the date, the 24 numbered words,
// and the instructions.
func Text(k Key, displayName string, created time.Time) string {
	w := k.Words()
	var b strings.Builder
	b.WriteString("care-pi recovery key\n")
	fmt.Fprintf(&b, "Clinic: %s\n", displayName)
	fmt.Fprintf(&b, "Key id: %s\n", k.ID())
	fmt.Fprintf(&b, "Made: %s\n\n", created.UTC().Format("2006-01-02"))
	for row := range 8 {
		for col := range 3 {
			i := col*8 + row
			fmt.Fprintf(&b, "%2d %-9s", i+1, w[i])
			if col < 2 {
				b.WriteString(" ")
			}
		}
		b.WriteString("\n")
	}
	b.WriteString("\nKeep this sheet in a safe place, away from the device.\n")
	b.WriteString("You need it to restore the backups on a new device.\n")
	lines := strings.Split(b.String(), "\n")
	for i, l := range lines {
		lines[i] = strings.TrimRight(l, " ")
	}
	return strings.Join(lines, "\n")
}
