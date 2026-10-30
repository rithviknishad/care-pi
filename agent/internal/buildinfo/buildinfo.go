// Package buildinfo holds the build metadata of the care-pi binary.
// The linker sets these variables with -ldflags -X at build time.
package buildinfo

import "fmt"

var (
	// Version is the output of "git describe --tags --always --dirty".
	Version = "dev"
	// Commit is the full git commit hash of the build.
	Commit = "dev"
	// Date is the UTC build date in RFC 3339 format.
	Date = "dev"
)

// String returns the three fields in one line.
func String() string {
	return fmt.Sprintf("version=%s commit=%s date=%s", Version, Commit, Date)
}
