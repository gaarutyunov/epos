package artifact

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
	require.NoError(t, err)

	assert.Equal(t, "reviewer", cfg.Name())
	assert.Equal(t, "reviews Go code", cfg.Description())
	// A field Epos has never heard of still belongs in the blob: "mirrors"
	// means the artifact is not a lossy copy of the directory.
	assert.Contains(t, cfg, "custom-field",
		"the config must mirror the frontmatter, not project it onto known fields")
	assert.Contains(t, cfg, "allowed-tools")
}

// The blob feeds a digest, so its bytes must not depend on map iteration.
func TestConfigJSONIsStable(t *testing.T) {
	cfg := Config{"name": "x", "b": 1, "a": 2, "z": "last", "description": "d"}

	first, err := cfg.JSON()
	require.NoError(t, err)

	for i := 0; i < 50; i++ {
		next, err := cfg.JSON()
		require.NoError(t, err)
		require.Equal(t, string(first), string(next),
			"config JSON differs between calls, so the digest would too")
	}
	assert.True(t, len(first) > 0)
	assert.Equal(t, byte('{'), first[0])
	assert.Contains(t, string(first), `"a":`)
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
			_, err := ParseFrontmatter([]byte(tt.src))
			assert.Error(t, err)
		})
	}
}

// CRLF files are common on Windows; the block must reach the YAML parser as
// authored.
func TestFrontmatterHandlesCRLF(t *testing.T) {
	cfg, err := ParseFrontmatter([]byte("---\r\nname: hello\r\ndescription: d\r\n---\r\n\r\n# Hello\r\n"))
	require.NoError(t, err)
	assert.Equal(t, "hello", cfg.Name())
}
