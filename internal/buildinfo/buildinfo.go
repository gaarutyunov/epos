// Package buildinfo carries version metadata stamped into the binary at
// release time. The default values apply to local (`go build`) builds; the
// goreleaser pipeline overrides them via -ldflags -X (see .goreleaser.yml).
package buildinfo

var (
	// Version is the released semantic version (e.g. "1.2.3") or "dev".
	Version = "dev"
	// Commit is the git commit the binary was built from.
	Commit = "none"
	// Date is the RFC3339 build timestamp.
	Date = "unknown"
)
