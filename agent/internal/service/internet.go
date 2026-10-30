package service

import (
	"context"
	"net/http"
	"sync"
	"time"
)

// Internet checks every few minutes that an HTTPS request to the internet works.
type Internet struct {
	// URL is the target of the check. Every is the interval (5 minutes).
	URL    string
	Every  time.Duration
	Client *http.Client

	mu        sync.Mutex
	reachable *bool
	checkedAt *time.Time
}

// State returns the last result, or nil before the first check.
func (i *Internet) State() (*bool, *time.Time) {
	i.mu.Lock()
	defer i.mu.Unlock()
	return i.reachable, i.checkedAt
}

// Check runs one check.
func (i *Internet) Check(ctx context.Context) {
	url := i.URL
	if url == "" {
		url = "https://github.com/"
	}
	c := i.Client
	if c == nil {
		c = &http.Client{Timeout: 10 * time.Second}
	}
	ok := false
	if req, err := http.NewRequestWithContext(ctx, http.MethodHead, url, nil); err == nil {
		if resp, err := c.Do(req); err == nil {
			_ = resp.Body.Close()
			ok = true
		}
	}
	now := time.Now().UTC().Truncate(time.Second)
	i.mu.Lock()
	i.reachable, i.checkedAt = &ok, &now
	i.mu.Unlock()
}

// Run checks at once and then at each interval until ctx ends.
func (i *Internet) Run(ctx context.Context) {
	every := i.Every
	if every == 0 {
		every = 5 * time.Minute
	}
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		i.Check(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
