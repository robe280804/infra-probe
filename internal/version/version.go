// Package version exposes build metadata injected at link time.
package version

import "fmt"

// These variables are overridden by the Makefile / release pipeline:
//
//	-ldflags "-X github.com/netseven/infra-probe/internal/version.Version=1.2.3"
var (
	Version = "dev"
	Commit  = "none"
	Date    = "unknown"
)

// String returns a human readable, single-line version description.
func String() string {
	return fmt.Sprintf("%s (commit %s, built %s)", Version, Commit, Date)
}
