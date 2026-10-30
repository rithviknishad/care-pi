package auth

import (
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/rithviknishad/care-pi/agent/internal/problem"
)

// TestLocksSurviveARestart: a new Manager (a restart of the agent) reads the address and claim
// locks and failures of LocksFile. Expired entries go away.
func TestLocksSurviveARestart(t *testing.T) {
	c := newClock()
	m, p := newManager(t, c)
	code, _, err := m.EnsureClaimCode()
	mustNil(t, err)
	for i := range 4 {
		if _, err := m.Claim(client("10.0.0.1"), ClaimCode, "0000-0000-0000", pw); slug(err) != problem.BadCredentials {
			t.Fatalf("attempt %d: %v", i, err)
		}
	}
	// The fifth failure after a restart locks the address and the claim: the agent kept the 4 failures.
	m = New(Options{Paths: p, Now: c.now, Params: cheap, OS: fakeOS{}})
	if d := m.Limits.ClaimLocked(); d != 0 {
		t.Fatalf("4 failures must not lock the claim: %s", d)
	}
	if _, err := m.Claim(client("10.0.0.1"), ClaimCode, "0000-0000-0000", pw); slug(err) != problem.BadCredentials {
		t.Fatalf("attempt 5: %v", err)
	}
	st, err := os.Stat(p.Abs(LocksFile))
	mustNil(t, err)
	if st.Mode().Perm() != 0o600 {
		t.Fatalf("%s has the mode %s", LocksFile, st.Mode().Perm())
	}

	c.add(time.Minute)
	m = New(Options{Paths: p, Now: c.now, Params: cheap, OS: fakeOS{}})
	if d := m.Limits.ClaimLocked(); d != ClaimLock-time.Minute {
		t.Fatalf("claim lock after a restart: %s", d)
	}
	if d := m.Limits.AddressLocked("10.0.0.1"); d != AddressLock-time.Minute {
		t.Fatalf("address lock after a restart: %s", d)
	}
	_, err = m.Claim(client("10.0.0.2"), ClaimCode, code, pw)
	if p := problem.From(err); p.Slug != problem.LockedOut || p.RetryAfter != 840 {
		t.Fatalf("claim after a restart: %+v", p)
	}

	// The locks end on time, and the next restart removes them from memory.
	c.add(ClaimLock)
	m = New(Options{Paths: p, Now: c.now, Params: cheap, OS: fakeOS{}})
	if s := m.Limits.snapshot(); len(s.ClaimFailures) != 0 || s.ClaimLockedUntil != nil || len(s.AddressFailures) != 0 || len(s.AddressLocks) != 0 {
		t.Fatalf("expired entries: %+v", s)
	}
}

// TestUnlockClearsStoredLocks: "care-pi auth unlock" (another process) removes the stored locks.
func TestUnlockClearsStoredLocks(t *testing.T) {
	c := newClock()
	m, p := newManager(t, c)
	claim(t, m)
	for range 5 {
		if _, err := m.Login(client("10.0.0.3"), "wrong password", ""); slug(err) != problem.BadCredentials {
			t.Fatal(err)
		}
	}
	cli := New(Options{Paths: p, Now: c.now, Params: cheap, OS: fakeOS{}})
	if cli.Limits.AddressLocked("10.0.0.3") == 0 {
		t.Fatal("the CLI must see the stored address lock")
	}
	mustNil(t, cli.Unlock())
	data, err := os.ReadFile(p.Abs(LocksFile))
	mustNil(t, err)
	var st LockState
	mustNil(t, json.Unmarshal(data, &st))
	if len(st.AddressLocks) != 0 || len(st.AddressFailures) != 0 {
		t.Fatalf("stored locks after unlock: %s", data)
	}
	if _, err := m.Login(client("10.0.0.3"), pw, ""); err != nil {
		t.Fatalf("the agent after unlock: %v", err)
	}
	if _, err := New(Options{Paths: p, Now: c.now, Params: cheap, OS: fakeOS{}}).Login(client("10.0.0.3"), pw, ""); err != nil {
		t.Fatalf("a restart after unlock: %v", err)
	}
}

func TestPruneLocks(t *testing.T) {
	now := time.Date(2026, 10, 30, 10, 0, 0, 0, time.UTC)
	future := now.Add(2 * time.Hour)
	st := LockState{
		ClaimFailures:    []time.Time{now.Add(-20 * time.Minute), now.Add(-time.Minute), now.Add(time.Minute)},
		ClaimLockedUntil: &future,
		AddressFailures:  map[string][]time.Time{"a": {now.Add(-time.Hour)}, "b": {now.Add(-time.Minute)}},
		AddressLocks:     map[string]time.Time{"a": now.Add(-time.Second), "b": now.Add(10 * time.Minute)},
	}
	for i := range maxStoredAddresses + 5 {
		st.AddressLocks["x"+time.Duration(i).String()] = now.Add(time.Duration(i) * time.Millisecond)
	}
	out := pruneLocks(st, now, time.Time{})
	if len(out.ClaimFailures) != 1 || out.ClaimLockedUntil != nil {
		t.Fatalf("claim: %+v %v", out.ClaimFailures, out.ClaimLockedUntil)
	}
	if _, ok := out.AddressFailures["a"]; ok || len(out.AddressFailures["b"]) != 1 {
		t.Fatalf("address failures: %+v", out.AddressFailures)
	}
	if _, ok := out.AddressLocks["a"]; ok || len(out.AddressLocks) != maxStoredAddresses {
		t.Fatalf("address locks: %d", len(out.AddressLocks))
	}
	if _, ok := out.AddressLocks["b"]; !ok {
		t.Fatal("the newest lock must stay")
	}
	// An unlock removes the entries before it.
	out = pruneLocks(st, now, now.Add(-30*time.Second))
	if len(out.ClaimFailures) != 0 || len(out.AddressFailures) != 0 || len(out.AddressLocks) != 0 {
		t.Fatalf("after unlock: %+v", out)
	}
}

// TestClaimHint: claim_methods lists claim-code first when bootstrap printed the claim code.
func TestClaimHint(t *testing.T) {
	c := newClock()
	m, p := newManager(t, c)
	m.os = fakeOS{pw: "imager password"}
	if got := m.ClaimMethods(); len(got) != 2 || got[0] != ClaimOSPassword {
		t.Fatalf("without a hint: %v", got)
	}
	mustNil(t, m.SetClaimHint(ClaimCode))
	if got := m.ClaimMethods(); len(got) != 2 || got[0] != ClaimCode || got[1] != ClaimOSPassword {
		t.Fatalf("with the hint: %v", got)
	}
	if m.SetClaimHint("other") == nil {
		t.Fatal("an unknown method")
	}
	m.os = fakeOS{}
	if got := m.ClaimMethods(); len(got) != 1 || got[0] != ClaimCode {
		t.Fatalf("without Imager: %v", got)
	}
	claim(t, m)
	if _, err := os.Stat(p.Abs(ClaimHintFile)); !os.IsNotExist(err) {
		t.Fatal("the claim must delete the claim hint")
	}
	if got := m.ClaimMethods(); len(got) != 0 {
		t.Fatalf("after the claim: %v", got)
	}
}
