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
