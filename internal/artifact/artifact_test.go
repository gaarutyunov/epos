package artifact

import (
	"strings"
	"testing"
)

// SPEC.md 2.1: the config blob mirrors SKILL.md frontmatter.
func TestParseFrontmatterMirrorsTheBlock(t *testing.T) {
	cfg, err := ParseFrontmatter([]byte(`---
name: reviewer
description: reviews Go code
allowed-tools:
  - Read
  - Grep
custom-field: kept
---

# Reviewer
`))
	if err != nil {
		t.Fatalf("ParseFrontmatter: %v", err)
	}

	if cfg.Name() != "reviewer" {
		t.Errorf("Name = %q, want reviewer", cfg.Name())
	}
	if cfg.Description() != "reviews Go code" {
		t.Errorf("Description = %q, want %q", cfg.Description(), "reviews Go code")
	}
	// A field Epos has never heard of still belongs in the blob: "mirrors"
	// means the artifact is not a lossy copy of the directory.
	if _, ok := cfg["custom-field"]; !ok {
		t.Errorf("custom-field was dropped; the config must mirror the frontmatter: %v", cfg)
	}
	if _, ok := cfg["allowed-tools"]; !ok {
		t.Errorf("allowed-tools was dropped: %v", cfg)
	}
}

// The blob feeds a digest, so its bytes must not depend on map iteration.
func TestConfigJSONIsStable(t *testing.T) {
	cfg := Config{"name": "x", "b": 1, "a": 2, "z": "last", "description": "d"}

	first, err := cfg.JSON()
	if err != nil {
		t.Fatalf("JSON: %v", err)
	}
	for i := 0; i < 50; i++ {
		next, err := cfg.JSON()
		if err != nil {
			t.Fatalf("JSON: %v", err)
		}
		if string(next) != string(first) {
			t.Fatalf("config JSON differs between calls:\n%s\n%s", first, next)
		}
	}
	if !strings.HasPrefix(string(first), `{"a":`) {
		t.Errorf("keys are not sorted: %s", first)
	}
}

func TestFrontmatterErrors(t *testing.T) {
	tests := []struct {
		name string
		src  string
	}{
		{"no fence", "# Just a heading\n"},
		{"unclosed fence", "---\nname: x\n"},
		{"no name", "---\ndescription: d\n---\n"},
		{"empty frontmatter", "---\n---\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := ParseFrontmatter([]byte(tt.src)); err == nil {
				t.Error("ParseFrontmatter accepted it, want an error")
			}
		})
	}
}

// CRLF files are common on Windows; the block must reach the YAML parser as
// authored.
func TestFrontmatterHandlesCRLF(t *testing.T) {
	cfg, err := ParseFrontmatter([]byte("---\r\nname: hello\r\ndescription: d\r\n---\r\n\r\n# Hello\r\n"))
	if err != nil {
		t.Fatalf("ParseFrontmatter: %v", err)
	}
	if cfg.Name() != "hello" {
		t.Errorf("Name = %q, want hello", cfg.Name())
	}
}
