// Package auth holds the operator account of the console: the claim, the console password
// (argon2id), the recovery codes, TOTP, the server-side sessions, and the rate limits
// (docs/api/README.md, sections 2 to 4).
//
// The account is /etc/care-pi/secrets/agent.json, the claim code /etc/care-pi/secrets/claim_code,
// and the sessions /var/lib/care-pi/state/sessions.json. The agent (care-pi serve) and the CLI
// (care-pi auth) change these files under one file lock, and each re-reads a file that changed.
package auth

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/rithviknishad/care-pi/agent/internal/lock"
	"github.com/rithviknishad/care-pi/agent/internal/paths"
	"github.com/rithviknishad/care-pi/agent/internal/problem"
)

// Files of the account.
const (
	AccountFile   = paths.SecretsDir + "/agent.json"
	ClaimCodeFile = paths.SecretsDir + "/claim_code"
	UnlockFile    = paths.StateDir + "/auth-unlock"
	// LocksFile keeps the address and claim locks and failures over a restart of the agent.
	LocksFile = paths.StateDir + "/auth-locks.json"
	// ClaimHintFile holds claim_hint: the claim method that the operator has (claim-code when
	// "care-pi bootstrap" made the claim code and printed it). It is next to setup.json and not
	// in it: a new setup record would change the state machine of a Phase 1 host.
	ClaimHintFile = paths.StateDir + "/claim-hint"
)

// Claim methods.
const (
	ClaimOSPassword = "os-password"
	ClaimCode       = "claim-code"
)

// ClaimRecord tells who claimed the device (schema ClaimRecord).
type ClaimRecord struct {
	ClaimedAt time.Time `json:"claimed_at"`
	Method    string    `json:"method"`
	Address   string    `json:"address"`
	UserAgent string    `json:"user_agent"`
}

type recoveryCode struct {
	Hash   string     `json:"hash"`
	UsedAt *time.Time `json:"used_at,omitempty"`
}

type totpState struct {
	Secret     string    `json:"secret"`
	EnrolledAt time.Time `json:"enrolled_at"`
	LastStep   int64     `json:"last_step"`
}

type lockout struct {
	ConsecutiveFailures int        `json:"consecutive_failures"`
	LockedUntil         *time.Time `json:"locked_until,omitempty"`
}

// Account is the operator account file. It holds hashes and the TOTP secret: root 0600.
type Account struct {
	Version             int            `json:"version"`
	PasswordHash        string         `json:"password_hash"`
	PasswordSetAt       time.Time      `json:"password_set_at"`
	RecoveryCodes       []recoveryCode `json:"recovery_codes"`
	RecoveryGeneratedAt time.Time      `json:"recovery_generated_at"`
	TOTP                *totpState     `json:"totp,omitempty"`
	Lockout             lockout        `json:"lockout"`
	Claim               ClaimRecord    `json:"claim"`
}

func (a *Account) remaining() int {
	n := 0
	for _, c := range a.RecoveryCodes {
		if c.UsedAt == nil {
			n++
		}
	}
	return n
}

// Client is the address and the user agent of a request.
type Client struct {
	Addr string
	UA   string
}

// Options configures a Manager.
type Options struct {
	Paths  paths.Paths
	Now    func() time.Time
	Params Argon2Params
	OS     OSPassword
	Log    *slog.Logger
}

// Manager runs the auth flows.
type Manager struct {
	p      paths.Paths
	now    func() time.Time
	params Argon2Params
	os     OSPassword
	log    *slog.Logger
	// Limits holds the in-memory rate limits and locks.
	Limits *Limiter

	mu        sync.Mutex
	acct      *Account
	acctStamp stamp
	sess      map[string]*Session
	sessStamp stamp
	seen      map[string]time.Time
	pending   *pendingTOTP
}

type pendingTOTP struct {
	secret  string
	expires time.Time
}

// New returns a Manager.
func New(o Options) *Manager {
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Params == (Argon2Params{}) {
		o.Params = DefaultParams
	}
	if o.OS == nil {
		o.OS = NoOSPassword{}
	}
	if o.Log == nil {
		o.Log = slog.New(slog.DiscardHandler)
	}
	m := &Manager{p: o.Paths, params: o.Params, os: o.OS, log: o.Log, seen: map[string]time.Time{}}
	m.now = func() time.Time { return o.Now().UTC().Truncate(time.Millisecond) }
	m.Limits = NewLimiter(m.now, m.unlockedAt)
	m.Limits.restore(m.readLocks())
	m.Limits.store = m.writeLocks
	return m
}

// readLocks reads LocksFile. A missing or damaged file gives no lock.
func (m *Manager) readLocks() LockState {
	var st LockState
	data, err := os.ReadFile(m.p.Abs(LocksFile))
	if err != nil {
		if !os.IsNotExist(err) {
			m.log.Warn("auth: cannot read the locks", "error", err)
		}
		return st
	}
	if err := json.Unmarshal(data, &st); err != nil {
		m.log.Warn("auth: the locks file is not valid; the agent ignores it", "file", LocksFile, "error", err)
		return LockState{}
	}
	return st
}

// writeLocks writes LocksFile under the file lock of the account and the sessions.
func (m *Manager) writeLocks(snap func() LockState) error {
	err := m.withFileLock(func() error {
		data, err := json.MarshalIndent(snap(), "", "  ")
		if err != nil {
			return err
		}
		return lock.WriteFileAtomic(m.p.Abs(LocksFile), data, 0o600)
	})
	if err != nil {
		m.log.Warn("auth: cannot write the locks", "error", err)
	}
	return err
}

func (m *Manager) unlockedAt() time.Time {
	st, err := os.Stat(m.p.Abs(UnlockFile))
	if err != nil {
		return time.Time{}
	}
	return st.ModTime()
}

// Now returns the clock of the manager.
func (m *Manager) Now() time.Time { return m.now() }

func (m *Manager) accountPath() string { return m.p.Abs(AccountFile) }

// loadAccount reads agent.json again when it changed. The caller holds m.mu.
func (m *Manager) loadAccount(force bool) error {
	path := m.accountPath()
	st := stampOf(path)
	if !force && st == m.acctStamp {
		return nil
	}
	m.acctStamp = st
	m.acct = nil
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("cannot read the console account: %w", err)
	}
	var a Account
	if err := json.Unmarshal(data, &a); err != nil {
		return fmt.Errorf("the console account %s is not valid: %w", AccountFile, err)
	}
	m.acct = &a
	return nil
}

func (m *Manager) saveAccount() error {
	data, err := json.MarshalIndent(m.acct, "", "  ")
	if err != nil {
		return err
	}
	path := m.accountPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	if err := lock.WriteFileAtomic(path, data, 0o600); err != nil {
		return fmt.Errorf("cannot write the console account: %w", err)
	}
	m.acctStamp = stampOf(path)
	return nil
}

// account returns a copy of the account, or nil before the claim.
func (m *Manager) account() (*Account, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.loadAccount(false); err != nil {
		return nil, err
	}
	if m.acct == nil {
		return nil, nil
	}
	cp := *m.acct
	return &cp, nil
}

// Claimed reports whether the account exists.
func (m *Manager) Claimed() bool {
	a, err := m.account()
	return err == nil && a != nil
}

// ClaimMethods lists the methods that the device accepts now. It is empty after the claim.
// The console shows the field of the first method. os-password is first, except when
// "care-pi bootstrap" made the claim code and printed it (ClaimHintFile).
func (m *Manager) ClaimMethods() []string {
	if m.Claimed() {
		return []string{}
	}
	if !m.os.Available() {
		return []string{ClaimCode}
	}
	if m.ClaimHint() == ClaimCode {
		return []string{ClaimCode, ClaimOSPassword}
	}
	return []string{ClaimOSPassword, ClaimCode}
}

// ClaimHint returns the claim method of ClaimHintFile, or "".
func (m *Manager) ClaimHint() string {
	data, err := os.ReadFile(m.p.Abs(ClaimHintFile))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// SetClaimHint writes ClaimHintFile. claim_methods then lists method first.
func (m *Manager) SetClaimHint(method string) error {
	if method != ClaimCode && method != ClaimOSPassword {
		return fmt.Errorf("the claim method %q does not exist", method)
	}
	path := m.p.Abs(ClaimHintFile)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	if err := lock.WriteFileAtomic(path, []byte(method+"\n"), 0o600); err != nil {
		return fmt.Errorf("cannot write the claim hint: %w", err)
	}
	return nil
}

// Grant is a new session.
type Grant struct {
	ID      string
	Session *Session
	// Codes are new recovery codes (claim, reset).
	Codes []string
	// Remaining is the number of recovery codes that remain (recover).
	Remaining int
}

func secondsUp(d time.Duration) int { return int(math.Ceil(d.Seconds())) }

// gate applies the credential limits of the address.
func (m *Manager) gate(c Client) error {
	if ok, wait := m.Limits.Allow("cred:"+c.Addr, CredentialPerMinute); !ok {
		return problem.Limited(problem.RateLimited, secondsUp(wait),
			fmt.Sprintf("The limit is %d credential requests each minute for this address.", CredentialPerMinute))
	}
	if wait := m.Limits.AddressLocked(c.Addr); wait > 0 {
		return problem.Limited(problem.LockedOut, secondsUp(wait),
			"5 failed attempts from this address in 15 minutes. Try again later.")
	}
	return nil
}

func (m *Manager) audit(event string, c Client, args ...any) {
	m.log.Info("auth: "+event, append([]any{"address", c.Addr, "user_agent", cut(c.UA, 256)}, args...)...)
}

// newCodes makes 8 recovery codes. The caller stores the hashes.
func newCodes() ([]string, []recoveryCode, error) {
	var plain []string
	var stored []recoveryCode
	for range RecoveryCodeSets {
		c, err := NewRecoveryCode()
		if err != nil {
			return nil, nil, err
		}
		plain = append(plain, c)
		stored = append(stored, recoveryCode{Hash: hashCode(NormalizeCode(c))})
	}
	return plain, stored, nil
}

func weak(pointer, reason, code string) *problem.Problem {
	return problem.New(problem.WeakPassword, "The new password is too weak: "+reason+".").
		WithErrors(problem.FieldError{Pointer: pointer, Message: reason, Code: code})
}

// Claim checks the claim secret, sets the password, makes 8 recovery codes, deletes the
// claim code, and starts a session.
func (m *Manager) Claim(c Client, method, secret, newPassword string) (*Grant, error) {
	if err := m.gate(c); err != nil {
		return nil, err
	}
	if m.Claimed() {
		return nil, problem.New(problem.AlreadyClaimed, "The device is already claimed. Log in with the console password.")
	}
	if wait := m.Limits.ClaimLocked(); wait > 0 {
		return nil, problem.Limited(problem.LockedOut, secondsUp(wait), "5 failed claims. The claim is locked for 15 minutes.")
	}
	accepted := false
	for _, a := range m.ClaimMethods() {
		accepted = accepted || a == method
	}
	if !accepted {
		return nil, problem.Invalid("/method", "the device does not accept the claim method "+method, "enum")
	}
	if reason, code := CheckPassword(newPassword); reason != "" {
		return nil, weak("/new_password", reason, code)
	}
	ok := false
	switch method {
	case ClaimCode:
		want, err := m.ClaimCode()
		ok = err == nil && want != "" && equalStrings(NormalizeCode(secret), NormalizeCode(want))
	case ClaimOSPassword:
		ok = m.os.Verify(secret)
	}
	if !ok {
		m.Limits.Fail(c.Addr)
		m.Limits.FailClaim()
		m.audit("claim failed", c, "method", method)
		return nil, problem.New(problem.BadCredentials, "The claim secret is not correct.")
	}
	hash, err := HashPassword(newPassword, m.params)
	if err != nil {
		return nil, err
	}
	plain, stored, err := newCodes()
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	var g *Grant
	err = m.withFileLock(func() error {
		if err := m.loadAccount(true); err != nil {
			return err
		}
		if m.acct != nil {
			return problem.New(problem.AlreadyClaimed, "The device is already claimed. Log in with the console password.")
		}
		now := m.now()
		m.acct = &Account{Version: 1, PasswordHash: hash, PasswordSetAt: now, RecoveryCodes: stored,
			RecoveryGeneratedAt: now, Claim: ClaimRecord{ClaimedAt: now, Method: method, Address: c.Addr, UserAgent: cut(c.UA, 256)}}
		if err := m.saveAccount(); err != nil {
			return err
		}
		if err := os.Remove(m.p.Abs(ClaimCodeFile)); err != nil && !os.IsNotExist(err) {
			m.log.Warn("auth: cannot delete the claim code", "error", err)
		}
		_ = os.Remove(m.p.Abs(ClaimHintFile))
		m.loadSessions(true)
		m.sess = map[string]*Session{}
		id, s, err := m.newSession(MethodClaim, c)
		if err != nil {
			return err
		}
		g = &Grant{ID: id, Session: s, Codes: plain}
		return m.saveSessions()
	})
	if err != nil {
		return nil, err
	}
	m.audit("claim", c, "method", method)
	return g, nil
}

// accountLocked returns the remaining account lock. The caller holds m.mu with a loaded account.
func (m *Manager) accountLocked() time.Duration {
	if m.acct == nil || m.acct.Lockout.LockedUntil == nil {
		return 0
	}
	until := *m.acct.Lockout.LockedUntil
	if until.Before(m.unlockedAt()) {
		return 0
	}
	return max(until.Sub(m.now()), 0)
}

// checkPassword verifies the console password with the account lock rules.
// It returns the problem for a lock or a failure.
func (m *Manager) checkPassword(c Client, password, pointer string) error {
	m.mu.Lock()
	if err := m.loadAccount(false); err != nil {
		m.mu.Unlock()
		return err
	}
	if m.acct == nil {
		m.mu.Unlock()
		return problem.New(problem.NotClaimed, "The device is not claimed. Claim it first.")
	}
	if wait := m.accountLocked(); wait > 0 {
		m.mu.Unlock()
		return problem.Limited(problem.LockedOut, secondsUp(wait), "Too many failed passwords for the account. Try again later.")
	}
	hash := m.acct.PasswordHash
	m.mu.Unlock()

	ok, err := VerifyPassword(password, hash)
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	err = m.withFileLock(func() error {
		if err := m.loadAccount(true); err != nil || m.acct == nil {
			return err
		}
		lo := &m.acct.Lockout
		if ok {
			if lo.ConsecutiveFailures == 0 && lo.LockedUntil == nil {
				return nil
			}
			*lo = lockout{}
			return m.saveAccount()
		}
		lo.ConsecutiveFailures++
		if d := accountLock(lo.ConsecutiveFailures); d > 0 {
			until := m.now().Add(d)
			lo.LockedUntil = &until
			m.audit("account locked", c, "failures", lo.ConsecutiveFailures, "until", until)
		}
		return m.saveAccount()
	})
	if err != nil {
		return err
	}
	if !ok {
		m.Limits.Fail(c.Addr)
		m.audit("password failed", c)
		return problem.New(problem.BadCredentials, "The password is not correct.").
			WithErrors(problem.FieldError{Pointer: pointer, Message: "the password is not correct", Code: "bad-credentials"})
	}
	return nil
}

// Login checks the password (and the TOTP code when TOTP is on) and starts a new session.
func (m *Manager) Login(c Client, password, totpCode string) (*Grant, error) {
	if err := m.gate(c); err != nil {
		return nil, err
	}
	if err := m.checkPassword(c, password, "/password"); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	var g *Grant
	// The limiter writes its file under the file lock, so a failure counts after withFileLock.
	failed := false
	err := m.withFileLock(func() error {
		if err := m.loadAccount(true); err != nil {
			return err
		}
		if t := m.acct.TOTP; t != nil {
			if totpCode == "" {
				return problem.New(problem.TOTPRequired, "TOTP is on. Send the password and the 6-digit code.")
			}
			step, ok := VerifyTOTP(t.Secret, totpCode, m.now(), t.LastStep)
			if !ok {
				failed = true
				return problem.New(problem.BadCredentials, "The TOTP code is not correct.")
			}
			t.LastStep = step
			if err := m.saveAccount(); err != nil {
				return err
			}
		}
		m.loadSessions(true)
		id, s, err := m.newSession(MethodPassword, c)
		if err != nil {
			return err
		}
		g = &Grant{ID: id, Session: s}
		return m.saveSessions()
	})
	if failed {
		m.Limits.Fail(c.Addr)
		m.audit("totp failed", c)
	}
	if err != nil {
		return nil, err
	}
	m.audit("login", c)
	return g, nil
}

// Recover uses a recovery code one time, sets the new password, turns TOTP off, deletes the
// other sessions, and starts a session. The account lock does not apply.
func (m *Manager) Recover(c Client, code, newPassword string) (*Grant, error) {
	if err := m.gate(c); err != nil {
		return nil, err
	}
	if !m.Claimed() {
		return nil, problem.New(problem.NotClaimed, "The device is not claimed. Claim it first.")
	}
	if reason, rc := CheckPassword(newPassword); reason != "" {
		return nil, weak("/new_password", reason, rc)
	}
	hash, err := HashPassword(newPassword, m.params)
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	var g *Grant
	failed := false
	err = m.withFileLock(func() error {
		if err := m.loadAccount(true); err != nil {
			return err
		}
		h := hashCode(NormalizeCode(code))
		idx := -1
		for i, rc := range m.acct.RecoveryCodes {
			if rc.UsedAt == nil && equalStrings(rc.Hash, h) {
				idx = i
			}
		}
		if idx < 0 || NormalizeCode(code) == "" {
			failed = true
			return problem.New(problem.BadCredentials, "The recovery code is not correct, or it was used before.")
		}
		now := m.now()
		m.acct.RecoveryCodes[idx].UsedAt = &now
		m.acct.PasswordHash, m.acct.PasswordSetAt = hash, now
		m.acct.TOTP = nil
		m.acct.Lockout = lockout{}
		if err := m.saveAccount(); err != nil {
			return err
		}
		m.sess = map[string]*Session{}
		id, s, err := m.newSession(MethodRecovery, c)
		if err != nil {
			return err
		}
		g = &Grant{ID: id, Session: s, Remaining: m.acct.remaining()}
		return m.saveSessions()
	})
	if failed {
		m.Limits.Fail(c.Addr)
		m.audit("recover failed", c)
	}
	if err != nil {
		return nil, err
	}
	m.audit("recover", c, "codes_remaining", g.Remaining)
	return g, nil
}

// Reauth checks the password and moves authenticated_at of the session to now.
func (m *Manager) Reauth(id string, c Client, password string) (*Session, error) {
	if err := m.gate(c); err != nil {
		return nil, err
	}
	if err := m.checkPassword(c, password, "/password"); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	var out *Session
	err := m.withFileLock(func() error {
		m.loadSessions(true)
		s, ok := m.sess[hashID(id)]
		if !ok || s.Expired(m.now()) {
			return problem.New(problem.NotAuthenticated, "The session ended.")
		}
		s.AuthenticatedAt = m.now()
		cp := *s
		out = &cp
		return m.saveSessions()
	})
	if err == nil {
		m.audit("reauth", c)
	}
	return out, err
}

// ChangePassword checks the current password, sets the new password, deletes the other
// sessions, and gives the current session a new id.
func (m *Manager) ChangePassword(id string, c Client, current, newPassword string) (*Grant, error) {
	if reason, code := CheckPassword(newPassword); reason != "" {
		return nil, weak("/new_password", reason, code)
	}
	if err := m.checkPassword(c, current, "/current_password"); err != nil {
		return nil, err
	}
	hash, err := HashPassword(newPassword, m.params)
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	var g *Grant
	err = m.withFileLock(func() error {
		if err := m.loadAccount(true); err != nil {
			return err
		}
		m.loadSessions(true)
		old, ok := m.sess[hashID(id)]
		if !ok || old.Expired(m.now()) {
			return problem.New(problem.NotAuthenticated, "The session ended.")
		}
		now := m.now()
		m.acct.PasswordHash, m.acct.PasswordSetAt = hash, now
		if err := m.saveAccount(); err != nil {
			return err
		}
		newID, err := Token()
		if err != nil {
			return err
		}
		s := *old
		s.Hash = hashID(newID)
		s.AuthenticatedAt = now
		s.LastSeenAt = now
		m.sess = map[string]*Session{s.Hash: &s}
		g = &Grant{ID: newID, Session: &s}
		return m.saveSessions()
	})
	if err != nil {
		return nil, err
	}
	m.audit("password changed", c)
	return g, nil
}

// VerifyConsolePassword reports whether password is the console password. The Care superuser
// password must differ from it. It does not count as a failed attempt.
func (m *Manager) VerifyConsolePassword(password string) bool {
	a, err := m.account()
	if err != nil || a == nil {
		return false
	}
	ok, err := VerifyPassword(password, a.PasswordHash)
	return err == nil && ok
}

// RegenerateCodes replaces the recovery codes with 8 new codes.
func (m *Manager) RegenerateCodes() ([]string, time.Time, error) {
	plain, stored, err := newCodes()
	if err != nil {
		return nil, time.Time{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	var at time.Time
	err = m.withFileLock(func() error {
		if err := m.loadAccount(true); err != nil {
			return err
		}
		if m.acct == nil {
			return problem.New(problem.NotClaimed, "The device is not claimed.")
		}
		at = m.now()
		m.acct.RecoveryCodes, m.acct.RecoveryGeneratedAt = stored, at
		return m.saveAccount()
	})
	if err != nil {
		return nil, time.Time{}, err
	}
	m.log.Info("auth: recovery codes regenerated")
	return plain, at, nil
}

// CheckRecoveryCode reports whether code is a recovery code that is not used. It does not use it.
func (m *Manager) CheckRecoveryCode(code string) bool {
	a, err := m.account()
	if err != nil || a == nil || NormalizeCode(code) == "" {
		return false
	}
	h := hashCode(NormalizeCode(code))
	ok := false
	for _, rc := range a.RecoveryCodes {
		if rc.UsedAt == nil && equalStrings(rc.Hash, h) {
			ok = true
		}
	}
	return ok
}

// TOTPEnrolment is the secret for the authenticator app (schema TotpEnrolment).
type TOTPEnrolment struct {
	Secret    string    `json:"secret"`
	URI       string    `json:"otpauth_uri"`
	Algorithm string    `json:"algorithm"`
	Digits    int       `json:"digits"`
	PeriodS   int       `json:"period_s"`
	ExpiresAt time.Time `json:"expires_at"`
}

// StartTOTP makes a TOTP secret and keeps it for 10 minutes.
func (m *Manager) StartTOTP(host string) (*TOTPEnrolment, error) {
	a, err := m.account()
	if err != nil {
		return nil, err
	}
	if a == nil {
		return nil, problem.New(problem.NotClaimed, "The device is not claimed.")
	}
	if a.TOTP != nil {
		return nil, problem.New(problem.StateConflict, "TOTP is already on. Turn it off first.")
	}
	secret, err := NewTOTPSecret()
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	exp := m.now().Add(10 * time.Minute)
	m.pending = &pendingTOTP{secret: secret, expires: exp}
	return &TOTPEnrolment{Secret: secret, URI: TOTPURI(secret, host), Algorithm: "SHA1", Digits: TOTPDigits,
		PeriodS: TOTPPeriod, ExpiresAt: exp}, nil
}

// VerifyTOTPEnrolment checks the first code and turns TOTP on.
func (m *Manager) VerifyTOTPEnrolment(c Client, code string) error {
	if err := m.gate(c); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.pending == nil || !m.now().Before(m.pending.expires) {
		m.pending = nil
		return problem.New(problem.StateConflict, "No TOTP enrolment runs, or it expired. Start the enrolment again.")
	}
	step, ok := VerifyTOTP(m.pending.secret, code, m.now(), 0)
	if !ok {
		m.Limits.Fail(c.Addr)
		m.audit("totp enrolment failed", c)
		return problem.Invalid("/code", "the code is not correct", "totp")
	}
	err := m.withFileLock(func() error {
		if err := m.loadAccount(true); err != nil {
			return err
		}
		if m.acct == nil {
			return problem.New(problem.NotClaimed, "The device is not claimed.")
		}
		if m.acct.TOTP != nil {
			return problem.New(problem.StateConflict, "TOTP is already on.")
		}
		m.acct.TOTP = &totpState{Secret: m.pending.secret, EnrolledAt: m.now(), LastStep: step}
		return m.saveAccount()
	})
	if err != nil {
		return err
	}
	m.pending = nil
	m.audit("totp enabled", c)
	return nil
}

// DisableTOTP turns TOTP off.
func (m *Manager) DisableTOTP() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.withFileLock(func() error {
		if err := m.loadAccount(true); err != nil {
			return err
		}
		if m.acct == nil {
			return problem.New(problem.NotClaimed, "The device is not claimed.")
		}
		if m.acct.TOTP == nil {
			return problem.New(problem.StateConflict, "TOTP is off.")
		}
		m.acct.TOTP = nil
		m.log.Info("auth: totp disabled")
		return m.saveAccount()
	})
}

// SecurityState is the security settings without a secret (schema SecurityState).
type SecurityState struct {
	Password struct {
		SetAt time.Time `json:"set_at"`
	} `json:"password"`
	RecoveryCodes struct {
		Remaining   int       `json:"remaining"`
		GeneratedAt time.Time `json:"generated_at"`
	} `json:"recovery_codes"`
	TOTP struct {
		Enabled    bool       `json:"enabled"`
		EnrolledAt *time.Time `json:"enrolled_at"`
	} `json:"totp"`
	Lockout struct {
		LockedUntil         *time.Time `json:"locked_until"`
		ConsecutiveFailures int        `json:"consecutive_failures"`
	} `json:"lockout"`
	Claim ClaimRecord `json:"claim"`
	SSH   any         `json:"ssh"`
}

// Security returns the security settings, or nil before the claim.
func (m *Manager) Security() (*SecurityState, error) {
	a, err := m.account()
	if err != nil || a == nil {
		return nil, err
	}
	var s SecurityState
	s.Password.SetAt = a.PasswordSetAt
	s.RecoveryCodes.Remaining = a.remaining()
	s.RecoveryCodes.GeneratedAt = a.RecoveryGeneratedAt
	if a.TOTP != nil {
		s.TOTP.Enabled = true
		t := a.TOTP.EnrolledAt
		s.TOTP.EnrolledAt = &t
	}
	m.mu.Lock()
	if wait := m.accountLocked(); wait > 0 && a.Lockout.LockedUntil != nil {
		t := *a.Lockout.LockedUntil
		s.Lockout.LockedUntil = &t
	}
	m.mu.Unlock()
	s.Lockout.ConsecutiveFailures = a.Lockout.ConsecutiveFailures
	s.Claim = a.Claim
	return &s, nil
}

// Status is the short state for a session response.
type Status struct {
	TOTPEnabled bool
	Remaining   int
}

// Status returns the TOTP state and the number of recovery codes.
func (m *Manager) Status() Status {
	a, err := m.account()
	if err != nil || a == nil {
		return Status{}
	}
	return Status{TOTPEnabled: a.TOTP != nil, Remaining: a.remaining()}
}

// ---- CLI operations (root on the host needs no session) ----

// SetPassword sets the console password. On an unclaimed device it also claims the device and
// returns 8 new recovery codes. It deletes each session.
func (m *Manager) SetPassword(password string) (codes []string, claimed bool, err error) {
	if reason, _ := CheckPassword(password); reason != "" {
		return nil, false, weak("", reason, "weak-password")
	}
	hash, err := HashPassword(password, m.params)
	if err != nil {
		return nil, false, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	err = m.withFileLock(func() error {
		if err := m.loadAccount(true); err != nil {
			return err
		}
		now := m.now()
		if m.acct == nil {
			plain, stored, err := newCodes()
			if err != nil {
				return err
			}
			codes, claimed = plain, true
			m.acct = &Account{Version: 1, RecoveryCodes: stored, RecoveryGeneratedAt: now,
				Claim: ClaimRecord{ClaimedAt: now, Method: ClaimCode, Address: "local", UserAgent: "care-pi CLI"}}
			_ = os.Remove(m.p.Abs(ClaimCodeFile))
			_ = os.Remove(m.p.Abs(ClaimHintFile))
		}
		m.acct.PasswordHash, m.acct.PasswordSetAt = hash, now
		m.acct.Lockout = lockout{}
		if err := m.saveAccount(); err != nil {
			return err
		}
		m.sess = map[string]*Session{}
		return m.saveSessions()
	})
	if err == nil {
		m.log.Info("auth: password set on the host", "claimed", claimed)
	}
	return codes, claimed, err
}

// Reset is the last fallback (S46): new password, 8 new codes, TOTP off, no session, no lock.
func (m *Manager) Reset(password string) ([]string, error) {
	if !m.Claimed() {
		return nil, problem.New(problem.NotClaimed, "The device is not claimed. Use care-pi auth set-password.")
	}
	if reason, _ := CheckPassword(password); reason != "" {
		return nil, weak("", reason, "weak-password")
	}
	hash, err := HashPassword(password, m.params)
	if err != nil {
		return nil, err
	}
	plain, stored, err := newCodes()
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	err = m.withFileLock(func() error {
		if err := m.loadAccount(true); err != nil {
			return err
		}
		now := m.now()
		m.acct.PasswordHash, m.acct.PasswordSetAt = hash, now
		m.acct.RecoveryCodes, m.acct.RecoveryGeneratedAt = stored, now
		m.acct.TOTP = nil
		m.acct.Lockout = lockout{}
		if err := m.saveAccount(); err != nil {
			return err
		}
		m.sess = map[string]*Session{}
		if err := m.saveSessions(); err != nil {
			return err
		}
		return m.touchUnlock()
	})
	if err != nil {
		return nil, err
	}
	m.Limits.Reset()
	m.log.Info("auth: account reset on the host")
	return plain, nil
}

func (m *Manager) touchUnlock() error {
	path := m.p.Abs(UnlockFile)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	now := time.Now()
	if err := os.WriteFile(path, []byte(now.UTC().Format(time.RFC3339)+"\n"), 0o600); err != nil {
		return err
	}
	return os.Chtimes(path, now, now)
}

// Unlock removes the account lock and tells the agent to remove each address and claim lock.
func (m *Manager) Unlock() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	err := m.withFileLock(func() error {
		if err := m.loadAccount(true); err != nil {
			return err
		}
		if m.acct != nil {
			m.acct.Lockout = lockout{}
			if err := m.saveAccount(); err != nil {
				return err
			}
		}
		return m.touchUnlock()
	})
	if err == nil {
		m.Limits.Reset()
		m.log.Info("auth: locks removed on the host")
	}
	return err
}

// ClaimCode returns the claim code, or "" when no code exists.
func (m *Manager) ClaimCode() (string, error) {
	data, err := os.ReadFile(m.p.Abs(ClaimCodeFile))
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("cannot read the claim code: %w", err)
	}
	return strings.TrimSpace(string(data)), nil
}

// ErrClaimed tells that the device has an account, so it has no claim code.
var ErrClaimed = errors.New("the device is already claimed; it has no claim code")

// EnsureClaimCode makes a claim code when the device is not claimed and no code exists.
func (m *Manager) EnsureClaimCode() (code string, made bool, err error) {
	if m.Claimed() {
		return "", false, ErrClaimed
	}
	if c, err := m.ClaimCode(); err != nil || c != "" {
		return c, false, err
	}
	c, err := m.RotateClaimCode()
	return c, err == nil, err
}

// RotateClaimCode writes a new claim code.
func (m *Manager) RotateClaimCode() (string, error) {
	if m.Claimed() {
		return "", ErrClaimed
	}
	c, err := NewClaimCode()
	if err != nil {
		return "", err
	}
	path := m.p.Abs(ClaimCodeFile)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", err
	}
	if err := lock.WriteFileAtomic(path, []byte(c+"\n"), 0o600); err != nil {
		return "", fmt.Errorf("cannot write the claim code: %w", err)
	}
	m.log.Info("auth: new claim code")
	return c, nil
}
