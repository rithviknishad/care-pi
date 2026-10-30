package host

import (
	"context"
	"fmt"
	"io"
	"strings"
	"sync"
)

// Response is one scripted result of the Fake runner.
type Response struct {
	Out  string
	Err  error
	Code int // a non-zero Code returns an *ExitError
}

// Fake records commands and returns scripted responses.
//
// Script maps a full command line to responses. Each call takes the next response.
// The last response repeats. A command without a script returns empty output.
type Fake struct {
	mu     sync.Mutex
	Script map[string][]Response
	Calls  []string
	// Stdin holds the length of the stdin of each call, so tests can check that secrets
	// went through stdin and not through arguments.
	StdinLen []int
	pos      map[string]int
}

// NewFake returns an empty Fake.
func NewFake() *Fake {
	return &Fake{Script: map[string][]Response{}, pos: map[string]int{}}
}

// On adds responses for a command line.
func (f *Fake) On(cmdline string, rs ...Response) *Fake {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Script[cmdline] = append(f.Script[cmdline], rs...)
	return f
}

// Run implements Runner.
func (f *Fake) Run(_ context.Context, c Cmd) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	line := c.String()
	f.Calls = append(f.Calls, line)
	n := 0
	if c.Stdin != nil {
		b, _ := io.ReadAll(c.Stdin)
		n = len(b)
	}
	f.StdinLen = append(f.StdinLen, n)
	rs := f.Script[line]
	if len(rs) == 0 {
		return nil, nil
	}
	if f.pos == nil {
		f.pos = map[string]int{}
	}
	i := f.pos[line]
	if i >= len(rs) {
		i = len(rs) - 1
	} else {
		f.pos[line] = i + 1
	}
	r := rs[i]
	if r.Code != 0 {
		return []byte(r.Out), &ExitError{Cmd: line, Code: r.Code}
	}
	return []byte(r.Out), r.Err
}

// Filter returns the recorded calls that start with one of the prefixes.
func (f *Fake) Filter(prefixes ...string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, c := range f.Calls {
		for _, p := range prefixes {
			if strings.HasPrefix(c, p) {
				out = append(out, c)
				break
			}
		}
	}
	return out
}

// FakeGit returns fixed ref lists.
type FakeGit struct {
	Refs  map[string]map[string]string
	Calls []string
}

// LsRemote implements Git.
func (g *FakeGit) LsRemote(_ context.Context, repo string) (map[string]string, error) {
	g.Calls = append(g.Calls, repo)
	r, ok := g.Refs[repo]
	if !ok {
		return nil, fmt.Errorf("fake git: no refs for %s", repo)
	}
	return r, nil
}

// Stream implements Streamer: it gives each line of the scripted output to fn. For a command with
// --follow, it then waits until ctx ends.
func (f *Fake) Stream(ctx context.Context, c Cmd, fn func(line []byte) error) error {
	out, err := f.Run(ctx, c)
	for _, l := range strings.Split(strings.TrimRight(string(out), "\n"), "\n") {
		if l == "" {
			continue
		}
		if e := fn([]byte(l)); e != nil {
			return e
		}
	}
	if err != nil {
		return err
	}
	for _, a := range c.Args {
		if a == "--follow" {
			<-ctx.Done()
			return ctx.Err()
		}
	}
	return nil
}
