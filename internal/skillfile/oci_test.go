package skillfile

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// SPEC.md 8.3: an OCI reference can be pinned by tag or by digest, and the
// registry may carry a port. All three put a colon somewhere different.
func TestParseOCIRef(t *testing.T) {
	tests := []struct {
		name       string
		ref        string
		registry   string
		repository string
		reference  string
	}{
		{
			name:       "a tag",
			ref:        "ghcr.io/o/agent-skills/pdf:1.2.0",
			registry:   "ghcr.io",
			repository: "o/agent-skills/pdf",
			reference:  "1.2.0",
		},
		{
			name:       "a digest",
			ref:        "ghcr.io/o/agent-skills/pdf@sha256:" + zeroHex,
			registry:   "ghcr.io",
			repository: "o/agent-skills/pdf",
			reference:  "sha256:" + zeroHex,
		},
		{
			name:       "a registry with a port",
			ref:        "127.0.0.1:5000/demo/agent-skills/pdf:1.2.0",
			registry:   "127.0.0.1:5000",
			repository: "demo/agent-skills/pdf",
			reference:  "1.2.0",
		},
		{
			name:       "a registry with a port, pinned by digest",
			ref:        "127.0.0.1:5000/demo/agent-skills/pdf@sha256:" + zeroHex,
			registry:   "127.0.0.1:5000",
			repository: "demo/agent-skills/pdf",
			reference:  "sha256:" + zeroHex,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			parsed, err := ParseOCIRef(tc.ref)
			require.NoError(t, err)
			assert.Equal(t, tc.registry, parsed.Registry)
			assert.Equal(t, tc.repository, parsed.Repository)
			assert.Equal(t, tc.reference, parsed.Reference)
		})
	}
}

// zeroHex is a syntactically valid sha256 hex payload for reference-parsing
// fixtures. Nothing resolves it; only the shape of the reference is under test.
const zeroHex = "0000000000000000000000000000000000000000000000000000000000000000"

func TestParseOCIRefWithoutATagOrDigest(t *testing.T) {
	_, err := ParseOCIRef("ghcr.io/o/agent-skills/pdf")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "needs a tag or a digest")
}

// SPEC.md 8.3 gives the OCI scheme no prefix, so a FROM is told from a local
// directory by what precedes its first slash — Docker's own rule.
func TestLooksLikeOCIRef(t *testing.T) {
	oci := []string{
		"ghcr.io/o/agent-skills/pdf:1.2.0",
		"127.0.0.1:5000/demo/agent-skills/pdf:1.2.0",
		"localhost/demo/pdf:1.2.0",
		"ghcr.io/o/agent-skills/pdf@sha256:" + zeroHex,
	}
	for _, ref := range oci {
		assert.True(t, looksLikeOCIRef(ref), "%s names a registry", ref)
	}

	local := []string{
		"./skills/base",
		"../shared/base",
		"/absolute/base",
		"base",
		"bases/pdf",
		"skills/base",
		".",
	}
	for _, ref := range local {
		assert.False(t, looksLikeOCIRef(ref), "%s names a directory in the context", ref)
	}
}

// A FROM that names a registry does not fall through to the filesystem: a build
// whose registry is unreachable must say so, not report a missing directory.
func TestFromAnOCIRefIsNotTreatedAsADirectory(t *testing.T) {
	sf, err := Parse([]byte("FROM 127.0.0.1:1/demo/agent-skills/pdf:1.2.0\n"))
	require.NoError(t, err)

	_, _, err = Build(sf, t.TempDir(), nil, WithPlainHTTP(true))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "127.0.0.1:1/demo/agent-skills/pdf:1.2.0")
	assert.NotContains(t, err.Error(), "no such file or directory")
}

// SPEC.md 8.3: an OCI FROM has to name a manifest to pin, and a bare repository
// names none.
func TestFromAnOCIRefWithoutATagFails(t *testing.T) {
	sf, err := Parse([]byte("FROM ghcr.io/o/agent-skills/pdf\n"))
	require.NoError(t, err)

	_, _, err = Build(sf, t.TempDir(), nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "needs a tag or a digest")
}
