// Package integration holds the godog runners.
//
// Every runner file carries the "integration" build tag (SPEC 13.5); this file
// is untagged so the package always has at least one file and "go build ./..."
// does not fail with "build constraints exclude all Go files".
package integration
