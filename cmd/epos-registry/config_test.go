package main

import (
	"testing"
	"time"

	"github.com/spf13/pflag"
)

// flagsFor builds the root command's flag set as the CLI would, then applies
// the given arguments.
func flagsFor(t *testing.T, args ...string) *pflag.FlagSet {
	t.Helper()
	cmd := newRootCommand()
	if err := cmd.ParseFlags(args); err != nil {
		t.Fatalf("parse %v: %v", args, err)
	}
	return cmd.Flags()
}

func TestConfigDefaults(t *testing.T) {
	cfg, err := loadConfig(flagsFor(t, "--upstream", "http://zot:5000"))
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}

	if cfg.addr != ":8080" {
		t.Errorf("addr = %q, want %q", cfg.addr, ":8080")
	}
	if cfg.exporter != "stdout" {
		t.Errorf("exporter = %q, want %q", cfg.exporter, "stdout")
	}
	if cfg.versionAttribute {
		t.Error("version attribute is on by default; SPEC.md 5.3 wants it off")
	}
}

func TestUpstreamIsRequired(t *testing.T) {
	if _, err := loadConfig(flagsFor(t)); err == nil {
		t.Error("loadConfig accepted a missing upstream, want an error")
	}
}

func TestConfigFromEnvironment(t *testing.T) {
	t.Setenv(envPrefix+"UPSTREAM", "http://zot:5000")
	t.Setenv(envPrefix+"ADDR", "127.0.0.1:9999")
	t.Setenv(envPrefix+"METRICS_EXPORTER", "none")
	t.Setenv(envPrefix+"METRICS_INTERVAL", "250ms")
	t.Setenv(envPrefix+"METRICS_VERSION_ATTRIBUTE", "true")

	cfg, err := loadConfig(flagsFor(t))
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}

	if cfg.upstreamURL != "http://zot:5000" {
		t.Errorf("upstream = %q, want %q", cfg.upstreamURL, "http://zot:5000")
	}
	if cfg.addr != "127.0.0.1:9999" {
		t.Errorf("addr = %q, want %q", cfg.addr, "127.0.0.1:9999")
	}
	if cfg.exporter != "none" {
		t.Errorf("exporter = %q, want %q", cfg.exporter, "none")
	}
	if cfg.interval != 250*time.Millisecond {
		t.Errorf("interval = %v, want %v", cfg.interval, 250*time.Millisecond)
	}
	if !cfg.versionAttribute {
		t.Error("version attribute = false, want true from the environment")
	}
}

// A flag the user actually typed beats the ambient environment; an untouched
// flag must not overwrite it with its default.
func TestFlagsBeatEnvironment(t *testing.T) {
	t.Setenv(envPrefix+"UPSTREAM", "http://from-env:5000")
	t.Setenv(envPrefix+"ADDR", "127.0.0.1:9999")

	cfg, err := loadConfig(flagsFor(t, "--upstream", "http://from-flag:5000"))
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}

	if cfg.upstreamURL != "http://from-flag:5000" {
		t.Errorf("upstream = %q, want the flag to win", cfg.upstreamURL)
	}
	if cfg.addr != "127.0.0.1:9999" {
		t.Errorf("addr = %q, want the environment to survive an untouched flag", cfg.addr)
	}
}
