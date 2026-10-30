// Package contracttest validates requests and responses of the agent against docs/api/openapi.yaml
// with kin-openapi (openapi3filter), and checks the rules of docs/api/README.md section 9.1.
package contracttest

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/getkin/kin-openapi/routers"
)

const problemBase = "https://care-pi.rithviknishad.dev/problems/"

// General problems can come from each operation.
var general = []string{"malformed-request", "payload-too-large", "unsupported-media-type", "rate-limited", "internal", "unavailable", "csrf"}

// KnownGaps are problem types that the agent returns, but x-problems of the operation does not list.
// Each one is a spec change that the wave 4a report requests: a query parameter that fails its
// schema gives invalid-request (docs/api/README.md section 7), also for these operations.
var KnownGaps = map[string][]string{}

// publicPaths are the paths that the agent also serves below /_care-pi/.
var publicPaths = map[string]bool{"/health": true, "/tls/root-ca": true, "/tls/root-ca/mobileconfig": true, "/tls/instructions/{os}": true}

var (
	once    sync.Once
	doc     *openapi3.T
	loadErr error
)

func init() {
	for _, ct := range []string{"application/gzip", "application/x-x509-ca-cert", "application/x-pem-file", "application/x-apple-aspen-config"} {
		openapi3filter.RegisterBodyDecoder(ct, openapi3filter.FileBodyDecoder)
	}
	openapi3filter.RegisterBodyDecoder("text/event-stream", openapi3filter.PlainBodyDecoder)
}

// SpecPath returns the path of docs/api/openapi.yaml.
func SpecPath() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "..", "..", "..", "..", "docs", "api", "openapi.yaml")
}

// Load loads the contract once.
func Load() (*openapi3.T, error) {
	once.Do(func() {
		loader := openapi3.NewLoader()
		loader.IsExternalRefsAllowed = true
		doc, loadErr = loader.LoadFromFile(SpecPath())
	})
	return doc, loadErr
}

// Doc loads the contract once.
func Doc(t testing.TB) *openapi3.T {
	t.Helper()
	d, err := Load()
	if err != nil {
		t.Fatalf("load the contract: %v", err)
	}
	return d
}

// Coverage records the operations and statuses that the tests checked.
type Coverage struct {
	mu   sync.Mutex
	seen map[string]map[int]bool
}

// NewCoverage returns an empty record.
func NewCoverage() *Coverage { return &Coverage{seen: map[string]map[int]bool{}} }

func (c *Coverage) add(op string, status int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.seen[op] == nil {
		c.seen[op] = map[int]bool{}
	}
	c.seen[op][status] = true
}

// Statuses returns the statuses that the tests checked for an operation id.
func (c *Coverage) Statuses(op string) []int {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []int
	for s := range c.seen[op] {
		out = append(out, s)
	}
	sort.Ints(out)
	return out
}

// Operations returns the operation ids of the contract with their x-phase (2 when not set).
func Operations() (map[string]int, error) {
	d, err := Load()
	if err != nil {
		return nil, err
	}
	out := map[string]int{}
	for _, item := range d.Paths.Map() {
		for _, op := range item.Operations() {
			p := 2
			if v, ok := op.Extensions["x-phase"].(float64); ok {
				p = int(v)
			}
			out[op.OperationID] = p
		}
	}
	return out, nil
}

// Exchange is one request and its response.
type Exchange struct {
	Method string
	// URL is the path and the query, for example /api/v1/jobs?limit=5.
	URL     string
	Header  http.Header
	Body    []byte
	Status  int
	RHeader http.Header
	RBody   []byte
	// InvalidRequest skips the validation of a request that a test makes invalid on purpose.
	InvalidRequest bool
}

func match(template, path string) (map[string]string, bool) {
	a, b := strings.Split(strings.Trim(template, "/"), "/"), strings.Split(strings.Trim(path, "/"), "/")
	if len(a) != len(b) {
		return nil, false
	}
	params := map[string]string{}
	for i := range a {
		if strings.HasPrefix(a[i], "{") {
			params[strings.Trim(a[i], "{}")] = b[i]
		} else if a[i] != b[i] {
			return nil, false
		}
	}
	return params, true
}

// route finds the operation of the contract for a request path.
func route(d *openapi3.T, method, path string) (*routers.Route, map[string]string, error) {
	rest := path
	var public bool
	switch {
	case path == "/_care-pi/status":
	case strings.HasPrefix(path, "/api/v1/"):
		rest = strings.TrimPrefix(path, "/api/v1")
	case strings.HasPrefix(path, "/_care-pi/"):
		rest, public = strings.TrimPrefix(path, "/_care-pi"), true
	default:
		return nil, nil, fmt.Errorf("%s is not an API path", path)
	}
	var best string
	var params map[string]string
	for tmpl := range d.Paths.Map() {
		p, ok := match(tmpl, rest)
		if !ok || d.Paths.Value(tmpl).GetOperation(method) == nil {
			continue
		}
		if best == "" || strings.Count(tmpl, "{") < strings.Count(best, "{") {
			best, params = tmpl, p
		}
	}
	if best == "" {
		return nil, nil, fmt.Errorf("the contract has no operation %s %s", method, path)
	}
	if public && !publicPaths[best] {
		return nil, nil, fmt.Errorf("%s %s answers below /_care-pi/, but the contract has it only below /api/v1", method, path)
	}
	item := d.Paths.Value(best)
	return &routers.Route{Spec: d, Path: best, PathItem: item, Method: method, Operation: item.GetOperation(method)}, params, nil
}

// Check validates one exchange and records it. It returns the operation id.
func Check(t testing.TB, cov *Coverage, x Exchange) string {
	t.Helper()
	d := Doc(t)
	req, err := http.NewRequest(x.Method, "https://care.local:8443"+x.URL, bytes.NewReader(x.Body))
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range x.Header {
		req.Header[k] = v
	}
	rt, params, err := route(d, x.Method, req.URL.Path)
	if err != nil {
		t.Errorf("%s %s: %v", x.Method, x.URL, err)
		return ""
	}
	op := rt.Operation
	where := fmt.Sprintf("%s %s (%s) -> %d", x.Method, x.URL, op.OperationID, x.Status)
	in := &openapi3filter.RequestValidationInput{Request: req, PathParams: params, Route: rt,
		Options: &openapi3filter.Options{AuthenticationFunc: openapi3filter.NoopAuthenticationFunc, MultiError: true}}
	if !x.InvalidRequest {
		if err := openapi3filter.ValidateRequest(context.Background(), in); err != nil {
			t.Errorf("%s: the request does not match the contract: %v", where, err)
		}
	}
	out := &openapi3filter.ResponseValidationInput{RequestValidationInput: in, Status: x.Status, Header: x.RHeader,
		Body: io.NopCloser(bytes.NewReader(x.RBody)), Options: &openapi3filter.Options{IncludeResponseStatus: true, MultiError: true}}
	if err := openapi3filter.ValidateResponse(context.Background(), out); err != nil {
		t.Errorf("%s: the response does not match the contract: %v\nbody: %s", where, err, cut(x.RBody))
	}
	checkRules(t, d, where, op, x)
	if cov != nil {
		cov.add(op.OperationID, x.Status)
	}
	return op.OperationID
}

func cut(b []byte) string {
	if len(b) > 2000 {
		return string(b[:2000]) + "..."
	}
	return string(b)
}

func checkRules(t testing.TB, d *openapi3.T, where string, op *openapi3.Operation, x Exchange) {
	t.Helper()
	for k := range x.RHeader {
		if strings.HasPrefix(strings.ToLower(k), "access-control-") {
			t.Errorf("%s: CORS header %s (S48: no CORS)", where, k)
		}
	}
	cc := x.RHeader.Get("Cache-Control")
	rootCA := op.OperationID == "getRootCa" || op.OperationID == "getRootCaMobileconfig"
	switch {
	case rootCA && x.Status == http.StatusOK:
		if cc != "public, max-age=300" {
			t.Errorf("%s: Cache-Control %q, want public, max-age=300", where, cc)
		}
	case cc != "no-store":
		t.Errorf("%s: Cache-Control %q, want no-store", where, cc)
	}
	ct := x.RHeader.Get("Content-Type")
	if x.Status >= 400 {
		if !strings.HasPrefix(ct, "application/problem+json") {
			t.Errorf("%s: an error has the content type %q", where, ct)
			return
		}
		var p struct {
			Type       string `json:"type"`
			Status     int    `json:"status"`
			Instance   string `json:"instance"`
			RetryAfter int    `json:"retry_after_s"`
		}
		if err := json.Unmarshal(x.RBody, &p); err != nil {
			t.Errorf("%s: the problem is not JSON: %v", where, err)
			return
		}
		slug := strings.TrimPrefix(p.Type, problemBase)
		var allowed []string
		if raw, ok := op.Extensions["x-problems"].([]any); ok {
			for _, v := range raw {
				allowed = append(allowed, fmt.Sprint(v))
			}
		}
		allowed = append(allowed, KnownGaps[op.OperationID]...)
		if !slices.Contains(allowed, slug) && !slices.Contains(general, slug) {
			t.Errorf("%s: problem type %s is not in x-problems %v or the general types", where, slug, allowed)
		}
		if p.Status != x.Status {
			t.Errorf("%s: problem status %d differs from the HTTP status", where, p.Status)
		}
		path := strings.SplitN(x.URL, "?", 2)[0]
		if p.Instance != path {
			t.Errorf("%s: instance %q, want %q", where, p.Instance, path)
		}
		if x.Status == http.StatusTooManyRequests || x.Status == http.StatusServiceUnavailable {
			if ra := x.RHeader.Get("Retry-After"); ra == "" || ra != strconv.Itoa(p.RetryAfter) {
				t.Errorf("%s: Retry-After %q and retry_after_s %d must be the same", where, ra, p.RetryAfter)
			}
		}
	}
	if strings.HasPrefix(ct, "text/event-stream") {
		checkSSE(t, d, where, op.OperationID, x.RBody)
	}
}

// checkSSE validates each frame of an event stream.
func checkSSE(t testing.TB, d *openapi3.T, where, op string, body []byte) {
	t.Helper()
	if !bytes.HasPrefix(body, []byte("retry: 3000\n\n")) {
		t.Errorf("%s: the stream does not start with retry: 3000", where)
	}
	for _, f := range Frames(body) {
		var data any
		if err := json.Unmarshal([]byte(f.Data), &data); err != nil {
			t.Errorf("%s: frame %q has data that is not JSON: %v", where, f.Event, err)
			continue
		}
		var schema string
		var value any
		switch {
		case op == "getLogs" && f.Event == "log":
			schema, value = "LogLine", data
			if f.ID == "" {
				t.Errorf("%s: a log frame has no id", where)
			}
		case op == "getLogs" && f.Event == "error":
			schema, value = "Problem", data
		case f.Event == "heartbeat":
			schema, value = "HeartbeatEvent", map[string]any{"event": f.Event, "data": data}
			if f.ID != "" {
				t.Errorf("%s: a heartbeat has an id", where)
			}
		default:
			id, err := strconv.Atoi(f.ID)
			if err != nil {
				t.Errorf("%s: frame %q has no integer id", where, f.Event)
			}
			schema, value = "JobEvent", map[string]any{"id": id, "event": f.Event, "data": data}
		}
		s := d.Components.Schemas[schema]
		if err := s.Value.VisitJSON(value, openapi3.EnableJSONSchema2020()); err != nil {
			t.Errorf("%s: frame %s does not match %s: %v\n%s", where, f.Event, schema, err, f.Data)
		}
	}
}

// Frame is one SSE event.
type Frame struct {
	ID    string
	Event string
	Data  string
}

// Frames splits an event stream into its events. The retry line is not an event.
func Frames(body []byte) []Frame {
	var out []Frame
	var cur Frame
	have := false
	sc := bufio.NewScanner(bytes.NewReader(body))
	sc.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for sc.Scan() {
		l := sc.Text()
		switch {
		case l == "":
			if have {
				out = append(out, cur)
			}
			cur, have = Frame{}, false
		case strings.HasPrefix(l, "id: "):
			cur.ID, have = strings.TrimPrefix(l, "id: "), true
		case strings.HasPrefix(l, "event: "):
			cur.Event, have = strings.TrimPrefix(l, "event: "), true
		case strings.HasPrefix(l, "data: "):
			cur.Data, have = strings.TrimPrefix(l, "data: "), true
		}
	}
	if have {
		out = append(out, cur)
	}
	return out
}
