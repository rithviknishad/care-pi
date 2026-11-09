package service

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeNet answers the requests of the internet check per host: an error, or a 200 response.
type fakeNet struct {
	mu    sync.Mutex
	errs  map[string]error
	calls []string
}

func (f *fakeNet) set(host string, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.errs == nil {
		f.errs = map[string]error{}
	}
	f.errs[host] = err
}

func (f *fakeNet) RoundTrip(r *http.Request) (*http.Response, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, r.URL.Host)
	if err := f.errs[r.URL.Host]; err != nil {
		return nil, err
	}
	return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("")), Request: r}, nil
}

var (
	errNoDNS   = &net.DNSError{Err: "no such host", Name: "github.com", IsNotFound: true}
	errNoRoute = &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connect: network is unreachable")}
)

func TestInternetDNSAndRoute(t *testing.T) {
	ctx := context.Background()
	for _, c := range []struct {
		name            string
		github, cf      error
		reachable       bool
		problem, detail string
	}{
		{"both answer", nil, nil, true, "", "the internet is reachable"},
		{"no DNS", errNoDNS, nil, false, InternetNoDNS, "the internet is reachable, but DNS does not work"},
		{"no route, DNS fails too", errNoDNS, errNoRoute, false, InternetNoRoute, "the internet is not reachable"},
		{"no route", errNoRoute, errNoRoute, false, InternetNoRoute, "the internet is not reachable"},
		{"github.com is blocked, the address answers", errNoRoute, nil, true, "", "the internet is reachable"},
		// A clock before NTP: the server answered, its certificate is "not yet valid" for the host (x509 gives the
		// reason Expired to it). http wraps the tls error in a url.Error.
		{"a wrong clock", tlsFailure(x509.CertificateInvalidError{Reason: x509.Expired}), nil, true, "", "the internet is reachable"},
		// A captive portal or a proxy answers 443 with its own certificate: that is not the internet.
		{"an unknown authority", tlsFailure(x509.UnknownAuthorityError{}), tlsFailure(x509.UnknownAuthorityError{}), false, InternetNoRoute, "the internet is not reachable"},
		{"a wrong name", tlsFailure(x509.HostnameError{Host: "github.com"}), tlsFailure(x509.HostnameError{Host: "1.1.1.1"}), false, InternetNoRoute, "the internet is not reachable"},
		{"another certificate error", tlsFailure(x509.CertificateInvalidError{Reason: x509.NotAuthorizedToSign}), errNoRoute, false, InternetNoRoute, "the internet is not reachable"},
		{"a portal on the name, the address answers", tlsFailure(x509.UnknownAuthorityError{}), nil, true, "", "the internet is reachable"},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := &fakeNet{}
			f.set("github.com", c.github)
			f.set("1.1.1.1", c.cf)
			i := &Internet{Transport: f}
			st := i.Check(ctx)
			if st.Reachable == nil || *st.Reachable != c.reachable || st.Problem != c.problem || st.Detail() != c.detail {
				t.Fatalf("got reachable %v problem %q detail %q", st.Reachable, st.Problem, st.Detail())
			}
			if st.CheckedAt == nil {
				t.Fatal("no check time")
			}
			if c.github == nil && len(f.calls) != 1 {
				t.Fatalf("a working name needs no second target: %v", f.calls)
			}
		})
	}
	if d := (InternetState{}).Detail(); d != "not checked yet" {
		t.Fatal(d)
	}
}

// tlsFailure is the error of http.Client for a certificate that fails the check: a url.Error around the
// tls.CertificateVerificationError around the x509 error (Go 1.20 and later).
func tlsFailure(x509Err error) error {
	return &url.Error{Op: "Head", URL: "https://github.com/", Err: &tls.CertificateVerificationError{Err: x509Err}}
}

// TestInternetRetrySchedule: after the start the check runs again after 15 s, 30 s, 60 s, then every 2 minutes until
// the first success, then every 5 minutes (S74).
func TestInternetRetrySchedule(t *testing.T) {
	f := &fakeNet{}
	f.set("github.com", errNoDNS)
	f.set("1.1.1.1", errNoRoute)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var waits []time.Duration
	i := &Internet{Transport: f}
	i.After = func(d time.Duration) <-chan time.Time {
		waits = append(waits, d)
		switch len(waits) {
		case 5:
			// The network comes after the fifth check.
			f.set("github.com", nil)
		case 8:
			cancel()
			return nil
		}
		ch := make(chan time.Time, 1)
		ch <- time.Time{}
		return ch
	}
	done := make(chan struct{})
	go func() {
		i.Run(ctx)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not end")
	}
	want := []time.Duration{15 * time.Second, 30 * time.Second, time.Minute, 2 * time.Minute, 2 * time.Minute,
		5 * time.Minute, 5 * time.Minute, 5 * time.Minute}
	if len(waits) != len(want) {
		t.Fatalf("waits %v, want %v", waits, want)
	}
	for k := range want {
		if waits[k] != want[k] {
			t.Fatalf("waits %v, want %v", waits, want)
		}
	}
	if r, _ := i.State(); r == nil || !*r {
		t.Fatalf("the last check works: %v", r)
	}
}

// TestInternetCheckOnce: a caller that comes while a check runs waits for it and does not start a second check.
func TestInternetCheckOnce(t *testing.T) {
	block := make(chan struct{})
	var n int
	var mu sync.Mutex
	rt := roundTrip(func(r *http.Request) (*http.Response, error) {
		mu.Lock()
		n++
		mu.Unlock()
		<-block
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("")), Request: r}, nil
	})
	i := &Internet{Transport: rt}
	var wg sync.WaitGroup
	for range 3 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			i.Check(context.Background())
		}()
	}
	time.Sleep(50 * time.Millisecond)
	close(block)
	wg.Wait()
	if n != 1 {
		t.Fatalf("%d requests, want 1", n)
	}
}

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// TestInternetCheckCancelledKeepsTheResult: a check whose context ends before a target answers (the 5 s limit of a
// host check on a slow link, or an aborted request) is not a failed check: the last result and its time stay, and the
// first check of the agent stays "not checked yet". The next check runs.
func TestInternetCheckCancelledKeepsTheResult(t *testing.T) {
	f := &fakeNet{}
	i := &Internet{Transport: f}
	first := i.Check(context.Background())
	if first.Reachable == nil || !*first.Reachable {
		t.Fatalf("the first check: %+v", first)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	slow := roundTrip(func(r *http.Request) (*http.Response, error) {
		return nil, &url.Error{Op: "Head", URL: r.URL.String(), Err: r.Context().Err()}
	})
	i.Transport = slow
	st := i.Check(ctx)
	if st.Reachable == nil || !*st.Reachable || st.Problem != "" || st.CheckedAt == nil || !st.CheckedAt.Equal(*first.CheckedAt) {
		t.Fatalf("a cancelled check changed the result: %+v", st)
	}
	if i.running != nil {
		t.Fatal("the check did not release running")
	}

	// Before the first result, a cancelled check keeps "not checked yet".
	j := &Internet{Transport: slow}
	if st := j.Check(ctx); st.Reachable != nil || st.CheckedAt != nil || st.Detail() != "not checked yet" {
		t.Fatalf("a cancelled first check gave a result: %+v", st)
	}
	// The same limit on a network that fails fast is a failed check.
	f.set("github.com", errNoRoute)
	f.set("1.1.1.1", errNoRoute)
	j.Transport = f
	if st := j.Check(context.Background()); st.Reachable == nil || *st.Reachable || st.Problem != InternetNoRoute {
		t.Fatalf("a failed check: %+v", st)
	}
}
