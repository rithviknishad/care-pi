package auth

import (
	"math"
	"sort"
	"sync"
	"time"
)

// Limits of docs/api/README.md, section 4.
const (
	CredentialPerMinute = 10
	PublicPerMinute     = 60
	SessionPerMinute    = 300
	AddressFailures     = 5
	AddressWindow       = 15 * time.Minute
	AddressLock         = 15 * time.Minute
	ClaimFailures       = 5
	ClaimLock           = 15 * time.Minute
	AccountFailures     = 10
	AccountLockFirst    = time.Minute
	AccountLockMax      = 60 * time.Minute
)

type bucket struct {
	tokens float64
	last   time.Time
}

// Limiter holds the token buckets and the address and claim locks in memory.
// The account lock is in the account file (Account.Lockout). The Manager also writes the
// address and claim locks and failures to LocksFile, so a restart of the agent keeps them.
type Limiter struct {
	mu        sync.Mutex
	now       func() time.Time
	unlocked  func() time.Time
	buckets   map[string]*bucket
	failures  map[string][]time.Time
	addrLocks map[string]time.Time
	claimFail []time.Time
	claimLock time.Time

	// store writes the lock state. It calls snap under its file lock. nil keeps the state in memory only.
	store  func(snap func() LockState) error
	saveMu sync.Mutex
}

// LockState is the content of LocksFile: the failures and the locks that a restart must keep.
type LockState struct {
	Version          int                    `json:"version"`
	ClaimFailures    []time.Time            `json:"claim_failures"`
	ClaimLockedUntil *time.Time             `json:"claim_locked_until"`
	AddressFailures  map[string][]time.Time `json:"address_failures"`
	AddressLocks     map[string]time.Time   `json:"address_locks"`
}

// maxStoredAddresses limits the addresses in each map of LocksFile. The newest entries stay.
const maxStoredAddresses = 1024

// NewLimiter returns a limiter. unlocked returns the time of the last "care-pi auth unlock";
// locks and failures before that time do not count.
func NewLimiter(now func() time.Time, unlocked func() time.Time) *Limiter {
	if unlocked == nil {
		unlocked = func() time.Time { return time.Time{} }
	}
	return &Limiter{now: now, unlocked: unlocked, buckets: map[string]*bucket{},
		failures: map[string][]time.Time{}, addrLocks: map[string]time.Time{}}
}

// Allow takes one token of the bucket key with perMinute tokens each minute.
// When the bucket is empty, it returns the wait.
func (l *Limiter) Allow(key string, perMinute int) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	rate := float64(perMinute) / 60
	b := l.buckets[key]
	if b == nil {
		if len(l.buckets) > 4096 {
			l.sweep(now)
		}
		b = &bucket{tokens: float64(perMinute), last: now}
		l.buckets[key] = b
	}
	b.tokens = math.Min(float64(perMinute), b.tokens+now.Sub(b.last).Seconds()*rate)
	b.last = now
	if b.tokens >= 1 {
		b.tokens--
		return true, 0
	}
	return false, time.Duration((1 - b.tokens) / rate * float64(time.Second))
}

// sweep removes full buckets.
func (l *Limiter) sweep(now time.Time) {
	for k, b := range l.buckets {
		if now.Sub(b.last) > 10*time.Minute {
			delete(l.buckets, k)
		}
	}
}

// AddressLocked returns the remaining lock of the address.
func (l *Limiter) AddressLocked(addr string) time.Duration {
	l.mu.Lock()
	defer l.mu.Unlock()
	until, ok := l.addrLocks[addr]
	if !ok {
		return 0
	}
	now := l.now()
	if !until.After(now) || until.Add(-AddressLock).Before(l.unlocked()) {
		delete(l.addrLocks, addr)
		return 0
	}
	return until.Sub(now)
}

// Fail records a failed credential of the address. The fifth failure in 15 minutes locks it.
func (l *Limiter) Fail(addr string) {
	l.fail(addr)
	l.save()
}

func (l *Limiter) fail(addr string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	cut := now.Add(-AddressWindow)
	if u := l.unlocked(); u.After(cut) {
		cut = u
	}
	var keep []time.Time
	for _, t := range l.failures[addr] {
		if t.After(cut) {
			keep = append(keep, t)
		}
	}
	keep = append(keep, now)
	if len(keep) >= AddressFailures {
		l.addrLocks[addr] = now.Add(AddressLock)
		keep = nil
	}
	if keep == nil {
		delete(l.failures, addr)
	} else {
		l.failures[addr] = keep
	}
}

// ClaimLocked returns the remaining lock of the claim (all addresses together).
func (l *Limiter) ClaimLocked() time.Duration {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	if !l.claimLock.After(now) || l.claimLock.Add(-ClaimLock).Before(l.unlocked()) {
		return 0
	}
	return l.claimLock.Sub(now)
}

// FailClaim records a failed claim. The fifth failed claim locks the claim for 15 minutes.
func (l *Limiter) FailClaim() {
	l.failClaim()
	l.save()
}

func (l *Limiter) failClaim() {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	cut := now.Add(-ClaimLock)
	if u := l.unlocked(); u.After(cut) {
		cut = u
	}
	var keep []time.Time
	for _, t := range l.claimFail {
		if t.After(cut) {
			keep = append(keep, t)
		}
	}
	keep = append(keep, now)
	if len(keep) >= ClaimFailures {
		l.claimLock = now.Add(ClaimLock)
		keep = nil
	}
	l.claimFail = keep
}

// Reset removes each lock and failure.
func (l *Limiter) Reset() {
	l.mu.Lock()
	l.failures = map[string][]time.Time{}
	l.addrLocks = map[string]time.Time{}
	l.claimFail = nil
	l.claimLock = time.Time{}
	l.mu.Unlock()
	l.save()
}

// save writes the lock state through store. The caller must not hold l.mu or the file lock of store.
func (l *Limiter) save() {
	if l.store == nil {
		return
	}
	l.saveMu.Lock()
	defer l.saveMu.Unlock()
	_ = l.store(l.snapshot)
}

// snapshot returns the failures and the locks that did not expire and are after the last unlock.
func (l *Limiter) snapshot() LockState {
	l.mu.Lock()
	defer l.mu.Unlock()
	return pruneLocks(LockState{ClaimFailures: l.claimFail, ClaimLockedUntil: &l.claimLock,
		AddressFailures: l.failures, AddressLocks: l.addrLocks}, l.now(), l.unlocked())
}

// restore loads a lock state that a stopped agent wrote. It removes each expired entry.
func (l *Limiter) restore(st LockState) {
	l.mu.Lock()
	defer l.mu.Unlock()
	st = pruneLocks(st, l.now(), l.unlocked())
	l.claimFail = st.ClaimFailures
	if st.ClaimLockedUntil != nil {
		l.claimLock = *st.ClaimLockedUntil
	}
	l.failures, l.addrLocks = st.AddressFailures, st.AddressLocks
}

// pruneLocks returns a copy of st without the expired entries and the entries before unlocked.
func pruneLocks(st LockState, now, unlocked time.Time) LockState {
	out := LockState{Version: 1, AddressFailures: map[string][]time.Time{}, AddressLocks: map[string]time.Time{}}
	recent := func(ts []time.Time, window time.Duration) []time.Time {
		cut := now.Add(-window)
		if unlocked.After(cut) {
			cut = unlocked
		}
		var keep []time.Time
		for _, t := range ts {
			if t.After(cut) && !t.After(now) {
				keep = append(keep, t)
			}
		}
		return keep
	}
	// A lock ends at most one lock time after now, also after a clock change.
	live := func(until time.Time, lock time.Duration) bool {
		return until.After(now) && !until.After(now.Add(lock)) && !until.Add(-lock).Before(unlocked)
	}
	out.ClaimFailures = recent(st.ClaimFailures, ClaimLock)
	if st.ClaimLockedUntil != nil && live(*st.ClaimLockedUntil, ClaimLock) {
		u := *st.ClaimLockedUntil
		out.ClaimLockedUntil = &u
	}
	for addr, ts := range st.AddressFailures {
		if keep := recent(ts, AddressWindow); len(keep) > 0 {
			out.AddressFailures[addr] = keep
		}
	}
	for addr, until := range st.AddressLocks {
		if live(until, AddressLock) {
			out.AddressLocks[addr] = until
		}
	}
	capAddresses(out.AddressFailures, func(ts []time.Time) time.Time { return ts[len(ts)-1] })
	capAddresses(out.AddressLocks, func(t time.Time) time.Time { return t })
	return out
}

// capAddresses keeps the maxStoredAddresses newest entries of m.
func capAddresses[V any](m map[string]V, last func(V) time.Time) {
	if len(m) <= maxStoredAddresses {
		return
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return last(m[keys[i]]).After(last(m[keys[j]])) })
	for _, k := range keys[maxStoredAddresses:] {
		delete(m, k)
	}
}

// accountLock returns the lock after n consecutive failures: 1 minute at 10, doubled for each
// next failure, 60 minutes at most.
func accountLock(n int) time.Duration {
	if n < AccountFailures {
		return 0
	}
	d := AccountLockFirst
	for i := AccountFailures; i < n && d < AccountLockMax; i++ {
		d *= 2
	}
	return min(d, AccountLockMax)
}
