package service

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"time"
)

// The causes of a failed internet check (schema InternetProblem).
const (
	// InternetNoDNS: the host reaches an internet address, but cannot resolve a name.
	InternetNoDNS = "dns"
	// InternetNoRoute: the host reaches no internet address.
	InternetNoRoute = "route"
)

// DefaultInternetTargets are the targets of the check: a name, then an address. The address needs no DNS, so it tells
// a failed DNS from a missing route.
var DefaultInternetTargets = []string{"https://github.com/", "https://1.1.1.1/"}

// DefaultInternetRetry are the waits between the checks after the start until the first success (S74). The last wait
// repeats until a check succeeds.
var DefaultInternetRetry = []time.Duration{15 * time.Second, 30 * time.Second, 60 * time.Second, 2 * time.Minute}

// Internet checks that an HTTPS request to the internet works (S74). At the start of the agent the network is often
// not ready (on care.local, /etc/resolv.conf came 6 s after the agent): so it checks again after 15 s, 30 s, 60 s, and
// then every 2 minutes until the first success, and after that every 5 minutes.
type Internet struct {
	// Targets are the URLs of the check (DefaultInternetTargets). The first one has a name; the last one should be an
	// address, so that a failed name tells "no DNS".
	Targets []string
	// Every is the interval after the first success (5 minutes). Retry are the waits before it (DefaultInternetRetry).
	Every time.Duration
	Retry []time.Duration
	// Timeout is the limit of one request (10 s).
	Timeout time.Duration
	// Transport, when set, makes the requests (tests). Else each check makes a new transport, with no connection and
	// no state from an earlier check.
	Transport http.RoundTripper
	// Now and After are the clock (tests).
	Now   func() time.Time
	After func(time.Duration) <-chan time.Time
	// Log, when set, gets one line for each change of the result.
	Log *slog.Logger

	mu        sync.Mutex
	reachable *bool
	problem   string
	checkedAt *time.Time
	// running serializes the checks: a second caller waits for the check that runs and uses its result.
	running chan struct{}
}

// InternetState is the result of the last check.
type InternetState struct {
	// Reachable is nil before the first check.
	Reachable *bool
	// Problem is InternetNoDNS or InternetNoRoute when Reachable is false, else "".
	Problem   string
	CheckedAt *time.Time
}

// ProblemPtr returns Problem, or nil.
func (s InternetState) ProblemPtr() *string {
	if s.Problem == "" {
		return nil
	}
	p := s.Problem
	return &p
}

// Detail is the text of the check for a person.
func (s InternetState) Detail() string {
	switch {
	case s.Reachable == nil:
		return "not checked yet"
	case *s.Reachable:
		return "the internet is reachable"
	case s.Problem == InternetNoDNS:
		return "the internet is reachable, but DNS does not work"
	}
	return "the internet is not reachable"
}

// State returns the last result, or nil before the first check.
func (i *Internet) State() (*bool, *time.Time) {
	st := i.Snapshot()
	return st.Reachable, st.CheckedAt
}

// Snapshot returns the last result with its problem.
func (i *Internet) Snapshot() InternetState {
	if i == nil {
		return InternetState{}
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	return InternetState{Reachable: i.reachable, Problem: i.problem, CheckedAt: i.checkedAt}
}

func (i *Internet) now() time.Time {
	if i.Now != nil {
		return i.Now()
	}
	return time.Now()
}

func (i *Internet) after(d time.Duration) <-chan time.Time {
	if i.After != nil {
		return i.After(d)
	}
	return time.After(d)
}

func (i *Internet) client() *http.Client {
	timeout := i.Timeout
	if timeout == 0 {
		timeout = 10 * time.Second
	}
	rt := i.Transport
	if rt == nil {
		// A new transport and dialer for each check: no kept-alive connection and no state of an earlier check.
		rt = &http.Transport{Proxy: http.ProxyFromEnvironment, DialContext: (&net.Dialer{Timeout: timeout}).DialContext,
			TLSHandshakeTimeout: timeout, DisableKeepAlives: true, ForceAttemptHTTP2: true}
	}
	return &http.Client{Transport: rt, Timeout: timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

// probe sends one HEAD request. A response of any status, or a certificate that the clock of the host cannot check
// yet (a clock before NTP), tells that the network path works.
func probe(ctx context.Context, c *http.Client, url string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, url, nil)
	if err != nil {
		return err
	}
	resp, err := c.Do(req)
	if err == nil {
		_ = resp.Body.Close()
		return nil
	}
	var cert x509.CertificateInvalidError
	var verify *tls.CertificateVerificationError
	if errors.As(err, &cert) || errors.As(err, &verify) {
		return nil
	}
	return err
}

func isDNSError(err error) bool {
	var dns *net.DNSError
	return errors.As(err, &dns)
}

// Check runs one check now and returns its result. A caller that comes while a check runs waits for that check.
//
// A check whose context ends before a target answers (the 5 s limit of a host check on a slow link, or a caller that
// goes away) tells nothing about the network: the result stays as it was, and the check does not count as a check.
func (i *Internet) Check(ctx context.Context) InternetState {
	i.mu.Lock()
	if ch := i.running; ch != nil {
		i.mu.Unlock()
		select {
		case <-ch:
		case <-ctx.Done():
		}
		return i.Snapshot()
	}
	ch := make(chan struct{})
	i.running = ch
	i.mu.Unlock()

	targets := i.Targets
	if len(targets) == 0 {
		targets = DefaultInternetTargets
	}
	c := i.client()
	ok, problem, dnsFailed := false, InternetNoRoute, false
	for _, t := range targets {
		err := probe(ctx, c, t)
		if err == nil {
			ok = true
			break
		}
		dnsFailed = dnsFailed || isDNSError(err)
	}
	if !ok && ctx.Err() != nil {
		i.mu.Lock()
		i.running = nil
		i.mu.Unlock()
		close(ch)
		return i.Snapshot()
	}
	switch {
	case ok && dnsFailed:
		// An address answered, but a name did not resolve: the images and the updates need DNS.
		ok, problem = false, InternetNoDNS
	case ok:
		problem = ""
	}
	now := i.now().UTC().Truncate(time.Second)
	i.mu.Lock()
	changed := i.reachable == nil || *i.reachable != ok || i.problem != problem
	i.reachable, i.problem, i.checkedAt = &ok, problem, &now
	i.running = nil
	i.mu.Unlock()
	close(ch)
	st := i.Snapshot()
	if changed && i.Log != nil {
		i.Log.Info("internet check", "reachable", ok, "problem", problem, "detail", st.Detail())
	}
	return st
}

// CheckWithin runs one check with a time limit (the probe of the host check: 5 s).
func (i *Internet) CheckWithin(ctx context.Context, limit time.Duration) InternetState {
	ctx, cancel := context.WithTimeout(ctx, limit)
	defer cancel()
	return i.Check(ctx)
}

// Run checks at once, then after each wait of Retry until the first success, then at each interval, until ctx ends.
func (i *Internet) Run(ctx context.Context) {
	every := i.Every
	if every == 0 {
		every = 5 * time.Minute
	}
	retry := i.Retry
	if len(retry) == 0 {
		retry = DefaultInternetRetry
	}
	succeeded, n := false, 0
	for {
		st := i.Check(ctx)
		if st.Reachable != nil && *st.Reachable {
			succeeded = true
		}
		wait := every
		if !succeeded {
			wait = retry[min(n, len(retry)-1)]
			n++
		}
		select {
		case <-ctx.Done():
			return
		case <-i.after(wait):
		}
	}
}
