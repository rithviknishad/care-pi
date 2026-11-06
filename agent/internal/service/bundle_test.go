package service

import (
	"strings"
	"testing"
)

// TestRedactCredentials (M3 of the Phase 7 review): a bearer token and the session cookie of the console never reach a
// support bundle, also in an audit line of the WAF of an older Caddyfile (part B, the request headers).
func TestRedactCredentials(t *testing.T) {
	const jwt = "eyJhbGciOiJIUzI1NiJ9.eyJ1c2VyX2lkIjoxfQ.c2lnbmF0dXJlLXZhbHVl"
	const session = "s3cr3t-Session_value.42"
	cases := map[string]string{
		`{"transaction":{"request":{"headers":{"authorization":["Bearer ` + jwt + `"],"cookie":["__Host-care_pi_session=` + session + `; csrftoken=x"]}}}}`: `{"transaction":{"request":{"headers":{"authorization":["Bearer <redacted>"],"cookie":["__Host-care_pi_session=<redacted>; csrftoken=x"]}}}}`,
		"Authorization: bearer " + jwt + "\n":                      "Authorization: bearer <redacted>\n",
		"Cookie: a=1; __Host-care_pi_session=" + session + "; b=2": "Cookie: a=1; __Host-care_pi_session=<redacted>; b=2",
		`"Cookie":"__Host-care_pi_session=` + session + `","x":1`:  `"Cookie":"__Host-care_pi_session=<redacted>","x":1`,
		"GET /api/v1/patient/ 200 (no credential)":                 "GET /api/v1/patient/ 200 (no credential)",
	}
	for in, want := range cases {
		got := redactCredentials(in)
		if got != want {
			t.Errorf("redact(%q)\n got  %q\n want %q", in, got, want)
		}
		if strings.Contains(got, jwt) || strings.Contains(got, session) {
			t.Errorf("a credential stays: %q", got)
		}
	}
}

// TestRedactSecretAssignments (S68): the plain-text exports and the bundle remove the value of a pair with a secret
// name and a Tailscale auth key. A value that is not a secret (true, false, null, an object) stays, so JSON stays valid.
func TestRedactSecretAssignments(t *testing.T) {
	cases := map[string]string{
		"password=hunter2 user=admin":                  "password=<redacted> user=admin",
		`{"password":"hunter2","password_set":true}`:   `{"password":"<redacted>","password_set":true}`,
		`"token": "abc123", "csrftoken=x"`:             `"token": "<redacted>", "csrftoken=x"`,
		"DJANGO_SECRET_KEY=s3cr3t-v4lue\nDEBUG=false":  "DJANGO_SECRET_KEY=<redacted>\nDEBUG=false",
		"X-CSRF-Token: 0123456789abcdef":               "X-CSRF-Token: <redacted>",
		"GET /api/v1/x?token=abc&page=2":               "GET /api/v1/x?token=<redacted>&page=2",
		"tailscale up --auth-key tskey-auth-k1-ABCdef": "tailscale up --auth-key tskey-<redacted>",
		`"secret_key_ref": {"name": "x"}`:              `"secret_key_ref": {"name": "x"}`,
		"api_key: null":                                "api_key: null",
		"the token expired; log in again":              "the token expired; log in again",
		"set-secrets: 3 secrets":                       "set-secrets: 3 secrets",
	}
	for in, want := range cases {
		if got := redactCredentials(in); got != want {
			t.Errorf("redact(%q)\n got  %q\n want %q", in, got, want)
		}
	}
	r := &Redactor{secrets: []string{"a-long-secret-value"}}
	if got := r.Text("x a-long-secret-value y password=p"); got != "x <redacted> y password=<redacted>" {
		t.Errorf("Redactor.Text: %q", got)
	}
}
