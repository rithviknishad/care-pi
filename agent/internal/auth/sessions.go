package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/rithviknishad/care-pi/agent/internal/lock"
	"github.com/rithviknishad/care-pi/agent/internal/paths"
)

// Session timeouts (S49) and the other session rules (docs/api/README.md, section 2.5).
const (
	IdleTimeout     = 2 * time.Hour
	AbsoluteTimeout = 24 * time.Hour
	ReauthWindow    = 5 * time.Minute
	MaxSessions     = 10
	// seenEvery limits the writes of last_seen_at.
	seenEvery = time.Minute
)

// Session methods.
const (
	MethodClaim    = "claim"
	MethodPassword = "password"
	MethodRecovery = "recovery-code"
)

// Session is one server-side session. The id itself is never stored, only its SHA-256.
type Session struct {
	Hash            string    `json:"hash"`
	CSRF            string    `json:"csrf"`
	Method          string    `json:"method"`
	CreatedAt       time.Time `json:"created_at"`
	AuthenticatedAt time.Time `json:"authenticated_at"`
	LastSeenAt      time.Time `json:"last_seen_at"`
	Address         string    `json:"address"`
	UserAgent       string    `json:"user_agent"`
}

// Ref is the public reference: the first 16 hex digits of the SHA-256 of the id.
func (s *Session) Ref() string { return s.Hash[:16] }

// IdleExpiresAt is the end of the session without a request.
func (s *Session) IdleExpiresAt() time.Time { return s.LastSeenAt.Add(IdleTimeout) }

// ExpiresAt is the absolute end of the session.
func (s *Session) ExpiresAt() time.Time { return s.CreatedAt.Add(AbsoluteTimeout) }

// ReauthUntil is the end of the reauthentication window.
func (s *Session) ReauthUntil() time.Time { return s.AuthenticatedAt.Add(ReauthWindow) }

// Expired reports whether the session ended at now.
func (s *Session) Expired(now time.Time) bool {
	return !now.Before(s.IdleExpiresAt()) || !now.Before(s.ExpiresAt())
}

// Recent reports whether the session checked a credential in the last 5 minutes.
func (s *Session) Recent(now time.Time) bool { return now.Before(s.ReauthUntil()) }

type sessionFile struct {
	Sessions []*Session `json:"sessions"`
}

func hashID(id string) string {
	sum := sha256.Sum256([]byte(id))
	return hex.EncodeToString(sum[:])
}

// Token returns 256 random bits in base64url without padding (43 characters).
func Token() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("cannot read random bytes: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// stamp identifies the version of a file on disk.
type stamp struct {
	mod    time.Time
	size   int64
	exists bool
}

func stampOf(path string) stamp {
	st, err := os.Stat(path)
	if err != nil {
		return stamp{}
	}
	return stamp{mod: st.ModTime(), size: st.Size(), exists: true}
}

func (m *Manager) sessionsPath() string { return m.p.Abs(paths.StateDir + "/sessions.json") }

// loadSessions reads sessions.json again when it changed on disk (a CLI command can change it).
func (m *Manager) loadSessions(force bool) {
	path := m.sessionsPath()
	st := stampOf(path)
	if !force && st == m.sessStamp && m.sess != nil {
		return
	}
	m.sess = map[string]*Session{}
	m.sessStamp = st
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var f sessionFile
	if json.Unmarshal(data, &f) != nil {
		m.log.Warn("auth: sessions.json is not valid; each session ends", "file", path)
		return
	}
	for _, s := range f.Sessions {
		if s != nil && len(s.Hash) == 64 {
			m.sess[s.Hash] = s
		}
	}
}

func (m *Manager) saveSessions() error {
	now := m.now()
	f := sessionFile{Sessions: []*Session{}}
	for h, s := range m.sess {
		if s.Expired(now) {
			delete(m.sess, h)
			continue
		}
		f.Sessions = append(f.Sessions, s)
	}
	sort.Slice(f.Sessions, func(i, j int) bool { return f.Sessions[i].CreatedAt.After(f.Sessions[j].CreatedAt) })
	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	path := m.sessionsPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	if err := lock.WriteFileAtomic(path, data, 0o600); err != nil {
		return fmt.Errorf("cannot write the sessions: %w", err)
	}
	m.sessStamp = stampOf(path)
	for _, s := range f.Sessions {
		m.seen[s.Hash] = s.LastSeenAt
	}
	return nil
}

// newSession adds a session and returns its id. The caller holds m.mu and saves.
func (m *Manager) newSession(method string, c Client) (string, *Session, error) {
	id, err := Token()
	if err != nil {
		return "", nil, err
	}
	csrf, err := Token()
	if err != nil {
		return "", nil, err
	}
	now := m.now()
	s := &Session{Hash: hashID(id), CSRF: csrf, Method: method, CreatedAt: now, AuthenticatedAt: now,
		LastSeenAt: now, Address: c.Addr, UserAgent: cut(c.UA, 256)}
	var live []*Session
	for h, o := range m.sess {
		if o.Expired(now) {
			delete(m.sess, h)
			continue
		}
		live = append(live, o)
	}
	sort.Slice(live, func(i, j int) bool { return live[i].CreatedAt.Before(live[j].CreatedAt) })
	for len(live) >= MaxSessions {
		delete(m.sess, live[0].Hash)
		live = live[1:]
	}
	m.sess[s.Hash] = s
	return id, s, nil
}

func cut(s string, n int) string {
	r := []rune(s)
	if len(r) > n {
		return string(r[:n])
	}
	return s
}

// Authenticate returns the session of the id and moves its idle timeout.
func (m *Manager) Authenticate(id string) (*Session, bool) {
	if id == "" {
		return nil, false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.loadSessions(false)
	h := hashID(id)
	s, ok := m.sess[h]
	now := m.now()
	if !ok || s.Expired(now) {
		return nil, false
	}
	s.LastSeenAt = now
	if now.Sub(m.seen[h]) >= seenEvery {
		if err := m.withFileLock(func() error {
			m.loadSessions(true)
			if cur, ok := m.sess[h]; ok {
				cur.LastSeenAt = now
				s = cur
			}
			return m.saveSessions()
		}); err != nil {
			m.log.Warn("auth: cannot write the sessions", "error", err)
		}
	}
	cp := *s
	return &cp, true
}

// Peek returns the session of the id without a change. SSE streams use it to end with the session.
func (m *Manager) Peek(id string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.loadSessions(false)
	s, ok := m.sess[hashID(id)]
	return ok && !s.Expired(m.now())
}

// Logout deletes the session of the id.
func (m *Manager) Logout(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.withFileLock(func() error {
		m.loadSessions(true)
		delete(m.sess, hashID(id))
		return m.saveSessions()
	})
}

// SessionInfo is one session in the list (schema SessionInfo).
type SessionInfo struct {
	Ref        string    `json:"ref"`
	Current    bool      `json:"current"`
	Method     string    `json:"method"`
	CreatedAt  time.Time `json:"created_at"`
	LastSeenAt time.Time `json:"last_seen_at"`
	Address    string    `json:"address"`
	UserAgent  string    `json:"user_agent"`
}

// Sessions lists the sessions, newest first. currentID marks the session of the request.
func (m *Manager) Sessions(currentID string) []SessionInfo {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.loadSessions(false)
	now := m.now()
	cur := ""
	if currentID != "" {
		cur = hashID(currentID)
	}
	out := []SessionInfo{}
	for _, s := range m.sess {
		if s.Expired(now) {
			continue
		}
		out = append(out, SessionInfo{Ref: s.Ref(), Current: s.Hash == cur, Method: s.Method, CreatedAt: s.CreatedAt,
			LastSeenAt: s.LastSeenAt, Address: s.Address, UserAgent: s.UserAgent})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out
}

// ErrNoSession tells that no session has the reference.
var ErrNoSession = errors.New("no session has this reference")

// Revoke deletes the session with the public reference.
func (m *Manager) Revoke(ref string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.withFileLock(func() error {
		m.loadSessions(true)
		for h, s := range m.sess {
			if s.Ref() == ref && !s.Expired(m.now()) {
				delete(m.sess, h)
				m.log.Info("auth: session revoked", "ref", ref)
				return m.saveSessions()
			}
		}
		return ErrNoSession
	})
}

// RevokeAll deletes each session.
func (m *Manager) RevokeAll() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.withFileLock(func() error {
		m.sess = map[string]*Session{}
		return m.saveSessions()
	})
}

// withFileLock serialises the changes of the account and the sessions between the agent and the CLI.
func (m *Manager) withFileLock(fn func() error) error {
	dir := m.p.Abs(paths.StateDir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(dir, "auth.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	if err := flock(f); err != nil {
		return err
	}
	return fn()
}
