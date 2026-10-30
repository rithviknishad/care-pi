// Package server is "care-pi serve": the console API (docs/api/openapi.yaml) and the console SPA
// on the unix socket /run/care-pi/api.sock. Caddy is the only client: it sends each request of
// port 8443, and /_care-pi/{status,health,tls/*} of port 443.
package server

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"mime"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/rithviknishad/care-pi/agent/internal/auth"
	"github.com/rithviknishad/care-pi/agent/internal/paths"
	"github.com/rithviknishad/care-pi/agent/internal/problem"
	"github.com/rithviknishad/care-pi/agent/internal/service"
)

// Prefixes of the API.
const (
	APIPrefix    = "/api/v1"
	PublicPrefix = "/_care-pi"
	// Body limits (docs/console-server.md, section 4). MaxBody (1 MiB) is only for the config
	// and the setup steps that hold a part of the config. CredentialBody (8 KiB) is for the
	// operations that check a credential. Each other unsafe operation has DefaultBody (64 KiB).
	MaxBody        = 1 << 20
	CredentialBody = 8 << 10
	DefaultBody    = 64 << 10
	// BodyTimeout is the time to read the body of an unsafe request.
	BodyTimeout = 10 * time.Second
	// CookieName is the session cookie (S49). The insecure dev listener uses DevCookieName.
	CookieName    = "__Host-care_pi_session"
	DevCookieName = "care_pi_session"
	cookieAttrs   = "; Path=/; Max-Age=86400; HttpOnly; Secure; SameSite=Strict"
)

// Options configures the server.
type Options struct {
	Service *service.Service
	Log     *slog.Logger
	// Console is the SPA build (dist/). nil or a tree without index.html gives the "not built" page.
	Console fs.FS
	// Dev is the insecure dev listener: plain HTTP, a cookie without Secure, Origin http://<Host>.
	Dev bool
	// AllowOrigins are more origins for unsafe requests (dev only).
	AllowOrigins []string
	// Heartbeat is the interval of SSE heartbeats (15 s). Poll is the interval of the job event poll.
	Heartbeat time.Duration
	Poll      time.Duration
	// MaxStreams and MaxSessionStreams limit the open SSE streams (16 and 4).
	MaxStreams        int
	MaxSessionStreams int
	// BodyTimeout overrides the time to read a body (tests).
	BodyTimeout time.Duration
}

// Server serves the API.
type Server struct {
	o   Options
	svc *service.Service
	a   *auth.Manager
	log *slog.Logger

	routes []*route

	mu       sync.Mutex
	streams  int
	bySess   map[string]int
	shutdown chan struct{}
	once     sync.Once
}

// New returns a server.
func New(o Options) *Server {
	if o.Log == nil {
		o.Log = slog.New(slog.DiscardHandler)
	}
	if o.Heartbeat == 0 {
		o.Heartbeat = 15 * time.Second
	}
	if o.Poll == 0 {
		o.Poll = 500 * time.Millisecond
	}
	if o.MaxStreams == 0 {
		o.MaxStreams = 16
	}
	if o.MaxSessionStreams == 0 {
		o.MaxSessionStreams = 4
	}
	if o.BodyTimeout == 0 {
		o.BodyTimeout = BodyTimeout
	}
	s := &Server{o: o, svc: o.Service, a: o.Service.Auth, log: o.Log, bySess: map[string]int{}, shutdown: make(chan struct{})}
	s.routes = s.table()
	return s
}

// Close ends the open event streams.
func (s *Server) Close() { s.once.Do(func() { close(s.shutdown) }) }

// reqCtx is one request.
type reqCtx struct {
	w         http.ResponseWriter
	r         *http.Request
	prefix    string
	client    auth.Client
	sessionID string
	session   *auth.Session
	params    map[string]string
	body      []byte
}

func (c *reqCtx) param(name string) string { return c.params[name] }

func (c *reqCtx) query(name string) (string, bool) {
	v, ok := c.r.URL.Query()[name]
	if !ok || len(v) == 0 {
		return "", false
	}
	return v[0], true
}

// boolQuery reads a boolean query parameter.
func (c *reqCtx) boolQuery(name string) (bool, error) {
	v, ok := c.query(name)
	if !ok {
		return false, nil
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return false, problem.InvalidParam(name, "use true or false")
	}
	return b, nil
}

type route struct {
	method string
	path   string
	segs   []string
	// public routes need no session. publicRead routes have the public read limit.
	public     bool
	publicRead bool
	// both routes are also below /_care-pi/. rootOnly routes are only there.
	both     bool
	rootOnly bool
	reauth   bool
	phase    int
	// mediaType is the content type of the body (application/json when empty).
	mediaType string
	// maxBody is the body limit (DefaultBody when 0).
	maxBody int64
	h       func(*reqCtx) error
}

func (rt *route) bodyLimit() int64 {
	if rt.maxBody > 0 {
		return rt.maxBody
	}
	return DefaultBody
}

func (rt *route) match(segs []string) (map[string]string, bool) {
	if len(segs) != len(rt.segs) {
		return nil, false
	}
	var params map[string]string
	for i, s := range rt.segs {
		if strings.HasPrefix(s, "{") {
			if segs[i] == "" {
				return nil, false
			}
			if params == nil {
				params = map[string]string{}
			}
			params[strings.Trim(s, "{}")] = segs[i]
			continue
		}
		if s != segs[i] {
			return nil, false
		}
	}
	return params, true
}

func split(p string) []string { return strings.Split(strings.Trim(p, "/"), "/") }

// find returns the route for the method and the path below the prefix.
func (s *Server) find(method, rest string, public bool) (*route, map[string]string) {
	segs := split(rest)
	var exact *route
	var params map[string]string
	for _, rt := range s.routes {
		if public && !rt.both && !rt.rootOnly {
			continue
		}
		if !public && rt.rootOnly {
			continue
		}
		p, ok := rt.match(segs)
		if !ok {
			continue
		}
		if rt.method == method || (method == http.MethodHead && rt.method == http.MethodGet) {
			// A literal segment wins over a parameter.
			if exact == nil || literals(rt) > literals(exact) {
				exact, params = rt, p
			}
		}
	}
	return exact, params
}

func literals(rt *route) int {
	n := 0
	for _, s := range rt.segs {
		if !strings.HasPrefix(s, "{") {
			n++
		}
	}
	return n
}

// ServeHTTP implements http.Handler.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	sw := &statusWriter{ResponseWriter: w}
	defer func() {
		if v := recover(); v != nil {
			s.log.Error("panic in a handler", "path", r.URL.Path, "panic", v)
			if sw.status == 0 {
				s.problem(sw, r, problem.From(nil))
			}
		}
		if !strings.HasPrefix(r.URL.Path, "/assets/") {
			s.log.Info("request", "method", r.Method, "path", r.URL.Path, "status", sw.status,
				"ms", time.Since(start).Milliseconds(), "address", s.clientAddr(r))
		}
	}()
	if unsafeMethod(r.Method) {
		// readBody reads the body within this time. The deadline also limits the time that
		// net/http reads a body that no handler read (for example after a 401).
		s.setReadDeadline(sw, time.Now().Add(s.o.BodyTimeout))
	}
	path := r.URL.Path
	switch {
	case path == APIPrefix || strings.HasPrefix(path, APIPrefix+"/"):
		s.api(sw, r, APIPrefix, strings.TrimPrefix(path, APIPrefix), false)
	case strings.HasPrefix(path, PublicPrefix+"/"):
		s.api(sw, r, PublicPrefix, strings.TrimPrefix(path, PublicPrefix), true)
	default:
		s.spa(sw, r)
	}
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) {
	if w.status == 0 {
		w.status = code
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.ResponseWriter.Write(b)
}

func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// clientAddr is the address that Caddy reports in X-Forwarded-For. Caddy replaces the header
// of a client, so the last item is the client. The dev listener uses the TCP peer.
func (s *Server) clientAddr(r *http.Request) string {
	if !s.o.Dev {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			parts := strings.Split(xff, ",")
			return strings.TrimSpace(parts[len(parts)-1])
		}
	}
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	if r.RemoteAddr == "" || r.RemoteAddr == "@" {
		return "local"
	}
	return r.RemoteAddr
}

func unsafeMethod(m string) bool {
	return m == http.MethodPost || m == http.MethodPut || m == http.MethodPatch || m == http.MethodDelete
}

// fetchSiteOK applies Sec-Fetch-Site to a request with a session. The console sends same-origin
// (also EventSource), and a URL that the operator types gives none. A page of another origin
// sends same-site or cross-site: port 443 and port 8443 are the same site, so SameSite=Strict
// does not stop these GET requests. A client without the header (curl, an old browser) passes.
func fetchSiteOK(r *http.Request) bool {
	switch r.Header.Get("Sec-Fetch-Site") {
	case "", "same-origin", "none":
		return true
	}
	return false
}

// originOK applies the Origin check of docs/api/README.md, section 3.
func (s *Server) originOK(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return false
	}
	for _, o := range s.o.AllowOrigins {
		if origin == o {
			return true
		}
	}
	if s.o.Dev {
		return origin == "http://"+r.Host
	}
	host, port, err := net.SplitHostPort(r.Host)
	if err != nil || port != strconv.Itoa(paths.ConsolePort) || origin != "https://"+r.Host {
		return false
	}
	return s.svc.ServedHosts()[host]
}

func (s *Server) cookieName() string {
	if s.o.Dev {
		return DevCookieName
	}
	return CookieName
}

func (s *Server) setCookie(w http.ResponseWriter, id string) {
	attrs := cookieAttrs
	if s.o.Dev {
		attrs = strings.Replace(attrs, "; Secure", "", 1)
	}
	w.Header().Add("Set-Cookie", s.cookieName()+"="+id+attrs)
}

func (s *Server) clearCookie(w http.ResponseWriter) {
	attrs := strings.Replace(cookieAttrs, "Max-Age=86400", "Max-Age=0", 1)
	if s.o.Dev {
		attrs = strings.Replace(attrs, "; Secure", "", 1)
	}
	w.Header().Add("Set-Cookie", s.cookieName()+"="+attrs)
}

func (s *Server) sessionCookie(r *http.Request) string {
	c, err := r.Cookie(s.cookieName())
	if err != nil {
		return ""
	}
	return c.Value
}

// api runs the middleware chain of the API, then the handler.
func (s *Server) api(w http.ResponseWriter, r *http.Request, prefix, rest string, public bool) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	c := &reqCtx{w: w, r: r, prefix: prefix, client: auth.Client{Addr: s.clientAddr(r), UA: r.UserAgent()}}
	rt, params := s.find(r.Method, rest, public)
	if rt == nil {
		s.problem(w, r, problem.Newf(problem.NotFound, "No operation has the method %s and the path %s.", r.Method, r.URL.Path))
		return
	}
	c.params = params
	if err := s.chain(c, rt); err != nil {
		s.fail(w, r, err)
	}
}

// chain checks a request in this order: Origin (unsafe), Sec-Fetch-Site and the session (not
// public), the CSRF token and the reauthentication, then the body (unsafe). Thus a request
// without a session never makes the agent read more than CredentialBody.
func (s *Server) chain(c *reqCtx, rt *route) error {
	r := c.r
	unsafe := unsafeMethod(r.Method)
	if unsafe && !s.originOK(r) {
		return problem.New(problem.CSRF, "The Origin header must be the origin of the console: https://<host>:8443.")
	}
	if rt.publicRead {
		if ok, wait := s.a.Limits.Allow("public:"+c.client.Addr, auth.PublicPerMinute); !ok {
			return problem.Limited(problem.RateLimited, int(wait.Seconds())+1, "The limit is 60 public requests each minute for this address.")
		}
	}
	if !rt.public {
		// Before Authenticate: a request that fails this check does not move the idle timeout.
		if !fetchSiteOK(r) {
			return problem.New(problem.CSRF, "The request comes from another origin (Sec-Fetch-Site). Open the console at https://<host>:8443/.")
		}
		id := s.sessionCookie(r)
		sess, ok := s.a.Authenticate(id)
		if !ok {
			return problem.New(problem.NotAuthenticated, "The session cookie is not there, or the session ended.")
		}
		c.sessionID, c.session = id, sess
		if ok, wait := s.a.Limits.Allow("session:"+sess.Hash, auth.SessionPerMinute); !ok {
			return problem.Limited(problem.RateLimited, int(wait.Seconds())+1, "The limit is 300 requests each minute for a session.")
		}
		if unsafe {
			tok := r.Header.Get("X-CSRF-Token")
			if tok == "" || subtle.ConstantTimeCompare([]byte(tok), []byte(sess.CSRF)) != 1 {
				return problem.New(problem.CSRF, "The X-CSRF-Token header is not there or not correct. Read it with GET /session.")
			}
		}
		if rt.reauth && !sess.Recent(s.a.Now()) {
			return problem.New(problem.ReauthRequired, "This operation needs an authentication in the last 5 minutes. Use POST /session/reauth.")
		}
	}
	if unsafe {
		if err := s.readBody(c, rt); err != nil {
			return err
		}
	}
	if rt.phase > 2 {
		return problem.Later(rt.phase, laterText[rt.phase])
	}
	return rt.h(c)
}

var laterText = map[int]string{
	3: "Backups and restore come in Phase 3.",
	4: "Solution updates and plugs come in Phase 4.",
	6: "Wi-Fi, public certificates, and Tailscale come in Phase 6.",
	7: "SSH and the rotation of the local CA come in Phase 7.",
}

// setReadDeadline sets the read deadline of the connection. A test recorder has no connection.
func (s *Server) setReadDeadline(w http.ResponseWriter, t time.Time) {
	if err := http.NewResponseController(w).SetReadDeadline(t); err != nil && !errors.Is(err, http.ErrNotSupported) {
		s.log.Warn("cannot set the read deadline", "error", err)
	}
}

func sizeText(n int64) string {
	if n >= 1<<20 {
		return strconv.FormatInt(n>>20, 10) + " MiB"
	}
	return strconv.FormatInt(n>>10, 10) + " KiB"
}

// readBody reads and checks the body of an unsafe request within the limit of the route and
// the body timeout. A body that does not arrive in time gives malformed-request (400): the
// catalogue has no problem type for status 408.
func (s *Server) readBody(c *reqCtx, rt *route) error {
	r := c.r
	limit := rt.bodyLimit()
	tooLarge := problem.New(problem.PayloadTooLarge, "The body is larger than "+sizeText(limit)+".")
	if r.ContentLength > limit {
		return tooLarge
	}
	data, err := io.ReadAll(http.MaxBytesReader(c.w, r.Body, limit))
	if err != nil {
		var mbe *http.MaxBytesError
		var ne net.Error
		switch {
		case errors.As(err, &mbe):
			return tooLarge
		case errors.As(err, &ne) && ne.Timeout():
			return problem.Newf(problem.MalformedRequest, "The body did not arrive in %s.", s.o.BodyTimeout)
		}
		return problem.New(problem.MalformedRequest, "The agent cannot read the body: "+err.Error())
	}
	// No deadline after the body: net/http reads the connection in the background to see a
	// closed connection, and a timeout there cancels the context of the handler.
	s.setReadDeadline(c.w, time.Time{})
	c.body = data
	if len(data) == 0 {
		return nil
	}
	want := rt.mediaType
	if want == "" {
		want = "application/json"
	}
	mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mt != want {
		return problem.Newf(problem.UnsupportedMediaType, "Send the body as %s.", want)
	}
	return nil
}

// decode reads the JSON body into v. A required body must not be empty.
func (c *reqCtx) decode(v any, required bool) error {
	if len(strings.TrimSpace(string(c.body))) == 0 {
		if required {
			return problem.Invalid("", "the request needs a body", "required")
		}
		return nil
	}
	dec := json.NewDecoder(strings.NewReader(string(c.body)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		var se *json.SyntaxError
		var te *json.UnmarshalTypeError
		switch {
		case errors.As(err, &se), errors.Is(err, io.ErrUnexpectedEOF):
			return problem.New(problem.MalformedRequest, "The body is not valid JSON: "+err.Error())
		case errors.As(err, &te):
			ptr := "/" + strings.ReplaceAll(te.Field, ".", "/")
			return problem.Invalid(ptr, "the value has the wrong type; want "+te.Type.String(), "type")
		case strings.HasPrefix(err.Error(), "json: unknown field "):
			f := strings.Trim(strings.TrimPrefix(err.Error(), "json: unknown field "), `"`)
			return problem.Invalid("/"+f, "the member "+f+" is not allowed", "additionalProperties")
		}
		return problem.New(problem.MalformedRequest, "The body is not valid JSON: "+err.Error())
	}
	if dec.More() {
		return problem.New(problem.MalformedRequest, "The body has more than one JSON value.")
	}
	return nil
}

func (s *Server) json(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func (s *Server) problem(w http.ResponseWriter, r *http.Request, p *problem.Problem) {
	cp := *p
	cp.Instance = r.URL.Path
	w.Header().Set("Content-Type", "application/problem+json")
	w.Header().Set("Cache-Control", "no-store")
	if cp.RetryAfter > 0 {
		w.Header().Set("Retry-After", strconv.Itoa(cp.RetryAfter))
	}
	w.WriteHeader(cp.Status)
	_ = json.NewEncoder(w).Encode(&cp)
}

// fail writes the problem of err. Another error is internal; its text goes only to the journal.
func (s *Server) fail(w http.ResponseWriter, r *http.Request, err error) {
	var p *problem.Problem
	if !errors.As(err, &p) {
		if errors.Is(err, context.Canceled) {
			return
		}
		s.log.Error("request failed", "method", r.Method, "path", r.URL.Path, "error", err)
		p = problem.From(err)
	}
	s.problem(w, r, p)
}
