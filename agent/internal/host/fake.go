package host

import (
	"context"
	"encoding/base64"
	"encoding/json"
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
	// Hook, when it is set, answers a command before Script. handled false falls through to Script.
	// It gets the stdin of the command. Tests of Phase 4 use it for the plug_config script.
	Hook func(c Cmd, stdin []byte) (r Response, handled bool)
	pos  map[string]int
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

// Set replaces the responses of a command line, and starts them again at the first one.
func (f *Fake) Set(cmdline string, rs ...Response) *Fake {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Script[cmdline] = rs
	if f.pos != nil {
		delete(f.pos, cmdline)
	}
	return f
}

// Run implements Runner.
func (f *Fake) Run(_ context.Context, c Cmd) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	line := c.String()
	f.Calls = append(f.Calls, line)
	var in []byte
	if c.Stdin != nil {
		in, _ = io.ReadAll(c.Stdin)
	}
	f.StdinLen = append(f.StdinLen, len(in))
	if f.Hook != nil {
		if r, ok := f.Hook(c, in); ok {
			if r.Code != 0 {
				return []byte(r.Out), &ExitError{Cmd: line, Code: r.Code, Stderr: r.Out}
			}
			return []byte(r.Out), r.Err
		}
	}
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
		// A command that fails writes its message to stderr: Out is the stderr of the error, as with Hook.
		return []byte(r.Out), &ExitError{Cmd: line, Code: r.Code, Stderr: r.Out}
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

// PlugConfigMarker starts the line of the plug_config script (package plugcfg) with the rows in Care.
const PlugConfigMarker = "CARE_PI_ROWS="

// PlugConfigHook answers the plug_config script of package plugcfg ("podman exec -i care-api python
// manage.py shell"): it prints the rows that the script writes, as Care would after the write.
// Tests and the dev host use it as the Hook of a Fake.
func PlugConfigHook(c Cmd, stdin []byte) (Response, bool) {
	if c.String() != "podman exec -i care-api python manage.py shell" {
		return Response{}, false
	}
	return Response{Out: PlugConfigEcho(stdin)}, true
}

// PlugConfigEcho decodes the payload of the plug_config script and returns the output line of the
// script for the rows that it writes.
func PlugConfigEcho(script []byte) string {
	s := string(script)
	const open = `b64decode("`
	i := strings.Index(s, open)
	if i < 0 {
		return PlugConfigMarker + "[]\n"
	}
	rest := s[i+len(open):]
	j := strings.Index(rest, `"`)
	if j < 0 {
		return PlugConfigMarker + "[]\n"
	}
	raw, err := base64.StdEncoding.DecodeString(rest[:j])
	if err != nil {
		return PlugConfigMarker + "[]\n"
	}
	var payload struct {
		Upsert json.RawMessage `json:"upsert"`
	}
	if json.Unmarshal(raw, &payload) != nil || len(payload.Upsert) == 0 {
		return PlugConfigMarker + "[]\n"
	}
	return PlugConfigMarker + string(payload.Upsert) + "\n"
}
