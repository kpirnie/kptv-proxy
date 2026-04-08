package app

import "fmt"

// Version variables are set at build time via -ldflags injection.
// Defaults are provided for local development builds where ldflags are not passed.
var (
	// Version is the semantic or date-based release identifier (e.g. "v04012026.09")
	Version = "v0.1.0"

	// BuildDate is the UTC timestamp of the build (e.g. "2026-04-01T09:00:00Z")
	BuildDate = "unknown"

	// Commit is the short Git SHA of the HEAD commit at build time
	Commit = "unknown"
)

// VersionString returns a formatted string combining version, commit, and build date
// for use in log output, admin interfaces, and health endpoints.
func VersionString() string {
	return fmt.Sprintf("%s (commit: %s, built: %s)", Version, Commit, BuildDate)
}
