package cli

import (
	"bytes"
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// zeroDigest is a syntactically valid manifest digest. Nothing resolves it;
// these tests are about how a reference is split, not about what it names.
const zeroDigest = "sha256:0000000000000000000000000000000000000000000000000000000000000000"

// Three things in a reference are separated by a colon — the registry's port,
// the tag and the digest algorithm — and no single rule about which colon to
// cut at gets all of them right. `pull` and `verify` share this parser with the
// OCI FROM of SPEC.md 8.3, whose pin is written as a digest reference.
func TestNewRepositorySplitsEveryFormOfReference(t *testing.T) {
	tests := []struct {
		name       string
		ref        string
		repository string
		reference  string
	}{
		{
			name:       "a tag",
			ref:        "ghcr.io/demo/agent-skills/reviewer:1.0.0",
			repository: "demo/agent-skills/reviewer",
			reference:  "1.0.0",
		},
		{
			name:       "a registry with a port",
			ref:        "127.0.0.1:45100/demo/agent-skills/reviewer:1.0.0",
			repository: "demo/agent-skills/reviewer",
			reference:  "1.0.0",
		},
		{
			name:       "a digest",
			ref:        "ghcr.io/demo/agent-skills/reviewer@" + zeroDigest,
			repository: "demo/agent-skills/reviewer",
			reference:  zeroDigest,
		},
		{
			name:       "a registry with a port, pinned by digest",
			ref:        "127.0.0.1:45100/demo/agent-skills/reviewer@" + zeroDigest,
			repository: "demo/agent-skills/reviewer",
			reference:  zeroDigest,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			repo, reference, err := newRepository(tc.ref, true)
			require.NoError(t, err)
			assert.Equal(t, tc.repository, repo.Reference.Repository)
			assert.Equal(t, tc.reference, reference)
			assert.True(t, repo.PlainHTTP, "--plain-http reaches the client")
		})
	}
}

func TestNewRepositoryRejectsAReferenceThatNamesNothing(t *testing.T) {
	_, _, err := newRepository("ghcr.io/demo/agent-skills/reviewer", false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "has no tag or digest")
}

func TestNewRepositoryRejectsSomethingThatIsNotAReference(t *testing.T) {
	_, _, err := newRepository("reviewer:1.0.0", false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "reviewer:1.0.0")
}

// The local store tags what it holds (9.1) and a digest is not in the character
// set a tag is allowed, so `pull` says what it needs rather than inventing one.
func TestPullByDigestSaysItNeedsATag(t *testing.T) {
	var out bytes.Buffer
	err := runPull(context.Background(), &out,
		"127.0.0.1:1/demo/agent-skills/reviewer@"+zeroDigest, true)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "pull needs a tag")
	assert.Empty(t, out.String())
}
