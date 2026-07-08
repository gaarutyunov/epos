//go:build integration

package epos_test

import (
	"os"
	"testing"

	"github.com/cucumber/godog"
)

// TestFeatures runs the journey-style Gherkin feature files as-is via godog,
// exactly the source of truth for behavior (SPEC §15.2). The feature files are
// loaded directly from features/ — never duplicated or paraphrased here.
//
// The step definitions drive the real epos application service against real
// dependencies started via testcontainers: a real zot OCI registry, a real
// Gitea git server over HTTP, and a real k3s Kubernetes cluster (SPEC §15.3).
// The whole suite is gated by the `integration` build tag (//go:build
// integration); `go test ./...` without it runs only in-package unit tests.
func TestFeatures(t *testing.T) {
	suite := godog.TestSuite{
		ScenarioInitializer: InitializeScenario,
		Options: &godog.Options{
			Format:   formatFromEnv(),
			Paths:    []string{"features"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("one or more scenarios failed")
	}
}

func formatFromEnv() string {
	if f := os.Getenv("GODOG_FORMAT"); f != "" {
		return f
	}
	return "pretty"
}
