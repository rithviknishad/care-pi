package config

import (
	"strings"
	"testing"
)

func TestWithClockPolicy(t *testing.T) {
	in := "# the head comment\nversion: 1\nclock:\n  policy: ntp   # the boot gate\nsize: {preset: solo}\n"
	out, err := WithClockPolicy([]byte(in), "rtc")
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	if !strings.Contains(s, "# the head comment") || !strings.Contains(s, "policy: rtc # the boot gate") || !strings.Contains(s, "size: {preset: solo}") {
		t.Fatalf("got:\n%s", s)
	}
	out, err = WithClockPolicy([]byte("version: 1\n"), "rtc")
	if err != nil || !strings.Contains(string(out), "clock:\n  policy: rtc") {
		t.Fatalf("a missing key: %v\n%s", err, out)
	}
	if _, err := WithClockPolicy([]byte("version: 1\n"), "gps"); err == nil {
		t.Fatal("gps is not a policy")
	}
}
