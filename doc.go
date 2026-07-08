// Package epos is the module root. The runnable code lives under cmd/ (the epos
// CLI) and internal/ (the bounded-context implementation generated from
// model/epos.sysml by sysgo). This file exists so the repository root is a Go
// package, letting the godog BDD suite (bdd_test.go) load features/ as-is with a
// working directory at the repository root (SPEC §15.2).
package epos
