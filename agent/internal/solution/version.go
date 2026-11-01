package solution

import (
	"fmt"
	"regexp"
	"strconv"
)

var (
	lockVersionRE = regexp.MustCompile(`^([0-9]{4})\.([0-9]{1,2})\.([0-9]+)$`)
	semverRE      = regexp.MustCompile(`^v?([0-9]+)\.([0-9]+)\.([0-9]+)`)
)

// ParseLockVersion splits a lock version YYYY.MM.N into its three numbers.
func ParseLockVersion(v string) ([3]int, bool) {
	m := lockVersionRE.FindStringSubmatch(v)
	if m == nil {
		return [3]int{}, false
	}
	var out [3]int
	for i := range 3 {
		out[i], _ = strconv.Atoi(m[i+1])
	}
	if out[1] < 1 || out[1] > 12 {
		return [3]int{}, false
	}
	return out, true
}

// NormalizeLockVersion writes the month with two digits: 2026.1.0 gives 2026.01.0.
func NormalizeLockVersion(v string) string {
	n, ok := ParseLockVersion(v)
	if !ok {
		return v
	}
	return fmt.Sprintf("%04d.%02d.%d", n[0], n[1], n[2])
}

// CompareLockVersions compares two lock versions as three numbers. A version that does not parse
// is older than each version that parses.
func CompareLockVersions(a, b string) int {
	x, okx := ParseLockVersion(a)
	y, oky := ParseLockVersion(b)
	switch {
	case !okx && !oky:
		return 0
	case !okx:
		return -1
	case !oky:
		return 1
	}
	for i := range 3 {
		if x[i] != y[i] {
			if x[i] < y[i] {
				return -1
			}
			return 1
		}
	}
	return 0
}

// ParseSemver reads vX.Y.Z at the start of v. A dev build (v0.4.0-12-gabcdef-dirty) gives its last tag.
func ParseSemver(v string) ([3]int, bool) {
	m := semverRE.FindStringSubmatch(v)
	if m == nil {
		return [3]int{}, false
	}
	var out [3]int
	for i := range 3 {
		out[i], _ = strconv.Atoi(m[i+1])
	}
	return out, true
}

// AgentTooOld reports whether the agent version is older than min (docs/solutions.md, section 7.4).
// A version without a tag passes; then warn tells it.
func AgentTooOld(agent, min string) (tooOld bool, warn string) {
	if min == "" {
		return false, ""
	}
	want, ok := ParseSemver(min)
	if !ok {
		return false, ""
	}
	have, ok := ParseSemver(agent)
	if !ok {
		return false, fmt.Sprintf("the agent version %q has no tag; it cannot compare with min_agent_version %s", agent, min)
	}
	for i := range 3 {
		if have[i] != want[i] {
			return have[i] < want[i], ""
		}
	}
	return false, ""
}
