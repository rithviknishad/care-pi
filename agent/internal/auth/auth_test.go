package auth

import (
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/rithviknishad/care-pi/agent/internal/paths"
	"github.com/rithviknishad/care-pi/agent/internal/problem"
)

var cheap = Argon2Params{MemoryKiB: 64, Time: 1, Threads: 1}

type clock struct{ t time.Time }

func (c *clock) now() time.Time       { return c.t }
func (c *clock) add(d time.Duration)  { c.t = c.t.Add(d) }
func newClock() *clock                { return &clock{t: time.Date(2026, 10, 30, 10, 0, 0, 0, time.UTC)} }
func slug(err error) string           { return problem.From(err).Slug }
func client(addr string) Client       { return Client{Addr: addr, UA: "test"} }
func mustNil(t *testing.T, err error) { t.Helper(); mustNilf(t, err, "") }
func mustNilf(t *testing.T, err error, m string) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s: %v", m, err)
	}
}

type fakeOS struct{ pw string }

func (f fakeOS) Available() bool           { return f.pw != "" }
func (f fakeOS) Verify(secret string) bool { return f.pw != "" && secret == f.pw }

func newManager(t *testing.T, c *clock) (*Manager, paths.Paths) {
	t.Helper()
	p := paths.New(t.TempDir())
	return New(Options{Paths: p, Now: c.now, Params: cheap, OS: fakeOS{}}), p
}

const pw = "a long passphrase 2026"

func claim(t *testing.T, m *Manager) *Grant {
	t.Helper()
	code, made, err := m.EnsureClaimCode()
	if err != nil || !made || len(code) != 14 {
		t.Fatalf("claim code %q made=%v err=%v", code, made, err)
	}
	g, err := m.Claim(client("192.168.1.23"), ClaimCode, strings.ToLower(strings.ReplaceAll(code, "-", " ")), pw)
	mustNilf(t, err, "claim")
	return g
}

func TestClaimFlow(t *testing.T) {
	c := newClock()
	m, p := newManager(t, c)
	if got := m.ClaimMethods(); len(got) != 1 || got[0] != ClaimCode {
		t.Fatalf("methods %v", got)
	}
	if _, err := m.Claim(client("a"), ClaimOSPassword, "x", pw); slug(err) != problem.InvalidRequest {
		t.Fatalf("os-password without Imager user: %v", err)
	}
	if _, err := m.Claim(client("a"), ClaimCode, "AAAA-BBBB-CCCC", "short"); slug(err) != problem.WeakPassword {
		t.Fatalf("weak: %v", err)
	}
	g := claim(t, m)
	if len(g.Codes) != 8 || g.Session.Method != MethodClaim || len(g.ID) != 43 {
		t.Fatalf("grant %+v", g)
	}
	for _, code := range g.Codes {
		if len(code) != 11 || code[5] != '-' {
			t.Fatalf("code %q", code)
		}
	}
	if _, err := os.Stat(p.Abs(ClaimCodeFile)); !os.IsNotExist(err) {
		t.Fatal("the claim must delete the claim code")
	}
	if _, err := m.Claim(client("a"), ClaimCode, "x", pw); slug(err) != problem.AlreadyClaimed {
		t.Fatalf("second claim: %v", err)
	}
	if !m.CheckRecoveryCode(strings.ToLower(g.Codes[3])) {
		t.Fatal("a recovery code typed in lower case must match")
	}
	if s, ok := m.Authenticate(g.ID); !ok || s.CSRF == "" {
		t.Fatal("the claim session must work")
	}
	st, err := m.Security()
	mustNil(t, err)
	if st.RecoveryCodes.Remaining != 8 || st.Claim.Method != ClaimCode || st.Claim.Address != "192.168.1.23" {
		t.Fatalf("security %+v", st)
	}
}

func TestClaimLockAfterFiveFailures(t *testing.T) {
	c := newClock()
	m, _ := newManager(t, c)
	_, _, err := m.EnsureClaimCode()
	mustNil(t, err)
	for i := range 5 {
		_, err := m.Claim(client("10.0.0."+string(rune('1'+i))), ClaimCode, "0000-0000-0000", pw)
		if slug(err) != problem.BadCredentials {
			t.Fatalf("attempt %d: %v", i, err)
		}
	}
	code, _ := m.ClaimCode()
	_, err = m.Claim(client("10.0.0.9"), ClaimCode, code, pw)
	p := problem.From(err)
	if p.Slug != problem.LockedOut || p.RetryAfter != 900 {
		t.Fatalf("claim lock: %+v", p)
	}
	c.add(ClaimLock)
	if _, err := m.Claim(client("10.0.0.9"), ClaimCode, code, pw); err != nil {
		t.Fatalf("after the lock: %v", err)
	}
}

func TestLoginLimitsAndLockouts(t *testing.T) {
	c := newClock()
	m, _ := newManager(t, c)
	claim(t, m)
	if _, err := m.Login(client("a"), pw, ""); err != nil {
		t.Fatalf("login: %v", err)
	}
	// 5 failures from one address lock the address for 15 minutes.
	for i := range 5 {
		if _, err := m.Login(client("b"), "wrong password", ""); slug(err) != problem.BadCredentials {
			t.Fatalf("failure %d: %v", i, err)
		}
	}
	_, err := m.Login(client("b"), pw, "")
	if p := problem.From(err); p.Slug != problem.LockedOut || p.RetryAfter != 900 {
		t.Fatalf("address lock: %+v", p)
	}
	if _, err := m.Login(client("c"), pw, ""); err != nil {
		t.Fatalf("another address: %v", err)
	}
	// 10 credential requests each minute for an address.
	for i := range 10 {
		if _, err := m.Login(client("d"), pw, ""); err != nil {
			t.Fatalf("request %d: %v", i, err)
		}
	}
	_, err = m.Login(client("d"), pw, "")
	if p := problem.From(err); p.Slug != problem.RateLimited || p.RetryAfter < 1 || p.RetryAfter > 6 {
		t.Fatalf("rate limit: %+v", p)
	}
	c.add(time.Minute)

	// 10 failures in a row from many addresses lock the account for 1 minute, then 2 minutes.
	for i := range 10 {
		addr := "e" + string(rune('a'+i))
		if _, err := m.Login(client(addr), "wrong password", ""); slug(err) != problem.BadCredentials {
			t.Fatalf("account failure %d: %v", i, err)
		}
	}
	_, err = m.Login(client("f"), pw, "")
	if p := problem.From(err); p.Slug != problem.LockedOut || p.RetryAfter != 60 {
		t.Fatalf("account lock: %+v", p)
	}
	// The account lock does not block recover.
	st, _ := m.Security()
	if st.Lockout.LockedUntil == nil || st.Lockout.ConsecutiveFailures != 10 {
		t.Fatalf("lockout %+v", st.Lockout)
	}
	c.add(time.Minute)
	if _, err := m.Login(client("g"), "wrong password", ""); slug(err) != problem.BadCredentials {
		t.Fatal(err)
	}
	_, err = m.Login(client("h"), pw, "")
	if p := problem.From(err); p.Slug != problem.LockedOut || p.RetryAfter != 120 {
		t.Fatalf("doubled lock: %+v", p)
	}
	mustNil(t, m.Unlock())
	if _, err := m.Login(client("b"), pw, ""); err != nil {
		t.Fatalf("after unlock: %v", err)
	}
	if st, _ := m.Security(); st.Lockout.ConsecutiveFailures != 0 {
		t.Fatalf("a success resets the count: %+v", st.Lockout)
	}
}

func TestRecoverAndTOTP(t *testing.T) {
	c := newClock()
	m, _ := newManager(t, c)
	g := claim(t, m)
	enr, err := m.StartTOTP("care.local")
	mustNil(t, err)
	if len(enr.Secret) != 32 || !strings.HasPrefix(enr.URI, "otpauth://totp/care-pi:care.local?") {
		t.Fatalf("enrolment %+v", enr)
	}
	if err := m.VerifyTOTPEnrolment(client("a"), "000000"); slug(err) != problem.InvalidRequest {
		code, _ := TOTPCode(enr.Secret, c.now())
		if code != "000000" {
			t.Fatalf("wrong code: %v", err)
		}
	}
	code, _ := TOTPCode(enr.Secret, c.now())
	mustNil(t, m.VerifyTOTPEnrolment(client("a"), code))
	if _, err := m.StartTOTP("care.local"); slug(err) != problem.StateConflict {
		t.Fatalf("second enrolment: %v", err)
	}
	if _, err := m.Login(client("a"), pw, ""); slug(err) != problem.TOTPRequired {
		t.Fatalf("totp required: %v", err)
	}
	if _, err := m.Login(client("a"), pw, code); slug(err) != problem.BadCredentials {
		t.Fatalf("a replayed code must fail: %v", err)
	}
	c.add(30 * time.Second)
	next, _ := TOTPCode(enr.Secret, c.now())
	if _, err := m.Login(client("a"), pw, next); err != nil {
		t.Fatalf("login with totp: %v", err)
	}

	r, err := m.Recover(client("b"), g.Codes[0], "another long passphrase")
	mustNilf(t, err, "recover")
	if r.Remaining != 7 || r.Session.Method != MethodRecovery {
		t.Fatalf("recover grant %+v", r)
	}
	if _, ok := m.Authenticate(g.ID); ok {
		t.Fatal("recover must delete the other sessions")
	}
	if _, err := m.Recover(client("b"), g.Codes[0], "another long passphrase"); slug(err) != problem.BadCredentials {
		t.Fatalf("a used code: %v", err)
	}
	if m.Status().TOTPEnabled {
		t.Fatal("recover turns TOTP off")
	}
	if _, err := m.Login(client("c"), "another long passphrase", ""); err != nil {
		t.Fatalf("new password: %v", err)
	}
}

func TestSessionsTimeoutsAndLimits(t *testing.T) {
	c := newClock()
	m, p := newManager(t, c)
	g := claim(t, m)
	c.add(IdleTimeout - time.Minute)
	if _, ok := m.Authenticate(g.ID); !ok {
		t.Fatal("a request before the idle timeout must work")
	}
	c.add(IdleTimeout - time.Minute)
	if _, ok := m.Authenticate(g.ID); !ok {
		t.Fatal("each request moves the idle timeout")
	}
	c.add(IdleTimeout)
	if _, ok := m.Authenticate(g.ID); ok {
		t.Fatal("the idle timeout ends the session")
	}

	// The absolute timeout.
	l, err := m.Login(client("a"), pw, "")
	mustNil(t, err)
	for range 14 {
		c.add(110 * time.Minute)
		if _, ok := m.Authenticate(l.ID); !ok {
			break
		}
	}
	if _, ok := m.Authenticate(l.ID); ok {
		t.Fatal("the absolute timeout ends the session")
	}

	// 10 sessions at most; a restart (a new manager) keeps them.
	var ids []string
	for range 12 {
		c.add(time.Second)
		l, err := m.Login(client("x"+c.now().String()), pw, "")
		mustNil(t, err)
		ids = append(ids, l.ID)
	}
	if n := len(m.Sessions("")); n != MaxSessions {
		t.Fatalf("%d sessions", n)
	}
	m2 := New(Options{Paths: p, Now: c.now, Params: cheap})
	if _, ok := m2.Authenticate(ids[0]); ok {
		t.Fatal("the oldest session must go")
	}
	if _, ok := m2.Authenticate(ids[11]); !ok {
		t.Fatal("a restart keeps the sessions")
	}
	list := m.Sessions(ids[11])
	if !list[0].Current || len(list[0].Ref) != 16 {
		t.Fatalf("list %+v", list[0])
	}
	mustNil(t, m2.Revoke(list[1].Ref))
	if _, ok := m.Authenticate(ids[10]); ok {
		t.Fatal("the revoke of another process must end the session here")
	}
	if err := m.Revoke("0000000000000000"); !errors.Is(err, ErrNoSession) {
		t.Fatalf("unknown ref: %v", err)
	}
}

func TestChangePasswordRotatesTheSession(t *testing.T) {
	c := newClock()
	m, _ := newManager(t, c)
	g := claim(t, m)
	other, err := m.Login(client("b"), pw, "")
	mustNil(t, err)
	if _, err := m.ChangePassword(g.ID, client("a"), "wrong password", "a newer long passphrase"); slug(err) != problem.BadCredentials {
		t.Fatalf("bad current: %v", err)
	}
	n, err := m.ChangePassword(g.ID, client("a"), pw, "a newer long passphrase")
	mustNil(t, err)
	if n.ID == g.ID {
		t.Fatal("the session needs a new id")
	}
	for _, id := range []string{g.ID, other.ID} {
		if _, ok := m.Authenticate(id); ok {
			t.Fatal("the old ids must end")
		}
	}
	if s, ok := m.Authenticate(n.ID); !ok || s.CSRF != g.Session.CSRF {
		t.Fatal("the new id must work and keep the CSRF token")
	}
	if !m.VerifyConsolePassword("a newer long passphrase") {
		t.Fatal("the new password must match")
	}
}

func TestSetPasswordClaimsAndReset(t *testing.T) {
	c := newClock()
	m, _ := newManager(t, c)
	if _, _, err := m.EnsureClaimCode(); err != nil {
		t.Fatal(err)
	}
	codes, claimed, err := m.SetPassword(pw)
	mustNil(t, err)
	if !claimed || len(codes) != 8 || !m.Claimed() {
		t.Fatal("set-password must claim an unclaimed device")
	}
	if _, _, err := m.EnsureClaimCode(); !errors.Is(err, ErrClaimed) {
		t.Fatal("a claimed device has no claim code")
	}
	g, err := m.Login(client("a"), pw, "")
	mustNil(t, err)
	fresh, err := m.Reset("a reset long passphrase")
	mustNil(t, err)
	if len(fresh) != 8 || m.CheckRecoveryCode(codes[0]) {
		t.Fatal("reset makes new codes")
	}
	if _, ok := m.Authenticate(g.ID); ok {
		t.Fatal("reset deletes each session")
	}
}

func TestCheckPassword(t *testing.T) {
	for pw, want := range map[string]string{
		"short":                        "minLength",
		"Password1234":                 "common-password",
		"aaaaaaaaaaaaaaa":              "common-password",
		"a long passphrase 2026":       "",
		strings.Repeat("ab cd ", 22):   "maxLength",
		"çàé çàé long pass 9":          "",
		"correct horse battery staple": "common-password",
	} {
		if _, code := CheckPassword(pw); code != want {
			t.Errorf("%q: code %q, want %q", pw, code, want)
		}
	}
}

func TestPasswordHashFormat(t *testing.T) {
	h, err := HashPassword("e\u0301 long passphrase", DefaultParams)
	mustNil(t, err)
	if !strings.HasPrefix(h, "$argon2id$v=19$m=19456,t=2,p=1$") {
		t.Fatalf("hash %s", h)
	}
	ok, err := VerifyPassword("\u00e9 long passphrase", h)
	if err != nil || !ok {
		t.Fatal("the NFC form must match")
	}
}

func TestShadowVerifier(t *testing.T) {
	p := paths.New(t.TempDir())
	s := Shadow{Paths: p}
	if s.Available() {
		t.Fatal("no user-data: not available")
	}
	write := func(path, data string) {
		t.Helper()
		abs := p.Abs(path)
		mustNil(t, os.MkdirAll(abs[:strings.LastIndex(abs, "/")], 0o755))
		mustNil(t, os.WriteFile(abs, []byte(data), 0o600))
	}
	write("/boot/firmware/user-data", "#cloud-config\nusers:\n- default\n- name: clinic\n  passwd: x\n")
	write("/etc/shadow", "root:*:1::::::\nclinic:$5$saltstring$5B8vYYiY.CVt1RlTTf8KbXBH3hsxY/GNooZaBBGWEc5:20000:0:99999:7:::\n")
	if !s.Available() || !s.Verify("Hello world!") || s.Verify("hello world!") {
		t.Fatal("the Imager user must verify")
	}
	write("/etc/shadow", "clinic:$y$j9T$abc$def:20000::::::\n")
	if s.Available() {
		t.Fatal("yescrypt is not available")
	}
}
