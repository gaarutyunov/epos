package cli

import (
	"bytes"
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// unroutable is a destination nothing listens on. A test that reaches it has
// found a network request that should never have been made.
const unroutable = "127.0.0.1:1/demo/agent-skills"

// The destination names a namespace and the skill's name is appended, so the
// published repository identifies the skill without a manifest lookup (SPEC.md
// 2.1) and pull — which reads the name back out of the last path segment — is
// push's exact inverse.
func TestPushResolvesTheDestinationIntoARepositoryAndTag(t *testing.T) {
	tests := []struct {
		name        string
		destination string
		skill       string
		version     string
		repository  string
		resolved    string
	}{
		{
			name:        "the helm form, with oci://",
			destination: "oci://ghcr.io/acme/agent-skills",
			skill:       "reviewer",
			version:     "1.0.0",
			repository:  "acme/agent-skills/reviewer",
			resolved:    "ghcr.io/acme/agent-skills/reviewer:1.0.0",
		},
		{
			name:        "the epos form, the way every other reference is written",
			destination: "ghcr.io/acme/agent-skills",
			skill:       "reviewer",
			version:     "1.0.0",
			repository:  "acme/agent-skills/reviewer",
			resolved:    "ghcr.io/acme/agent-skills/reviewer:1.0.0",
		},
		{
			name:        "a host carrying a port",
			destination: "127.0.0.1:45100/demo/agent-skills",
			skill:       "reviewer",
			version:     "1.0.0",
			repository:  "demo/agent-skills/reviewer",
			resolved:    "127.0.0.1:45100/demo/agent-skills/reviewer:1.0.0",
		},
		{
			name:        "a host carrying a port, with oci://",
			destination: "oci://127.0.0.1:45100/demo/agent-skills",
			skill:       "reviewer",
			version:     "1.0.0",
			repository:  "demo/agent-skills/reviewer",
			resolved:    "127.0.0.1:45100/demo/agent-skills/reviewer:1.0.0",
		},
		{
			// Not de-duplicated: `…/reviewer/reviewer` is a legal repository
			// somebody may genuinely want, so the mistake is made visible by
			// the printed reference rather than guessed at.
			name:        "a namespace already ending in the skill's own name",
			destination: "oci://ghcr.io/acme/agent-skills/reviewer",
			skill:       "reviewer",
			version:     "1.0.0",
			repository:  "acme/agent-skills/reviewer/reviewer",
			resolved:    "ghcr.io/acme/agent-skills/reviewer/reviewer:1.0.0",
		},
		{
			name:        "a trailing slash",
			destination: "oci://ghcr.io/acme/agent-skills/",
			skill:       "reviewer",
			version:     "1.0.0",
			repository:  "acme/agent-skills/reviewer",
			resolved:    "ghcr.io/acme/agent-skills/reviewer:1.0.0",
		},
		{
			name:        "a registry with no namespace at all",
			destination: "ghcr.io",
			skill:       "reviewer",
			version:     "1.0.0",
			repository:  "reviewer",
			resolved:    "ghcr.io/reviewer:1.0.0",
		},
		{
			name:        "a version that is not a bare semver",
			destination: "ghcr.io/acme/agent-skills",
			skill:       "reviewer",
			version:     "2.0.0-rc.1",
			repository:  "acme/agent-skills/reviewer",
			resolved:    "ghcr.io/acme/agent-skills/reviewer:2.0.0-rc.1",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ref, err := pushReference(tc.destination, tc.skill, tc.version)

			require.NoError(t, err)
			assert.Equal(t, tc.repository, ref.Repository)
			// The remote tag is the version alone. Only the local store needs
			// <name>:<version>, because one flat layout holds many skills.
			assert.Equal(t, tc.version, ref.Reference)
			assert.Equal(t, tc.resolved, ref.String())
		})
	}
}

// The two forms name the same registry, which is the whole of what accepting
// oci:// is for.
func TestTheOciPrefixIsAcceptedButNotRequired(t *testing.T) {
	with, err := pushReference("oci://ghcr.io/acme/agent-skills", "reviewer", "1.0.0")
	require.NoError(t, err)
	without, err := pushReference("ghcr.io/acme/agent-skills", "reviewer", "1.0.0")
	require.NoError(t, err)

	assert.Equal(t, without, with)
}

func TestPushRejectsADestinationThatIsNotANamespace(t *testing.T) {
	tests := []struct {
		name        string
		destination string
	}{
		{name: "empty", destination: ""},
		{name: "nothing but the scheme", destination: "oci://"},
		{name: "carrying a tag", destination: "ghcr.io/acme/agent-skills:1.0.0"},
		{name: "carrying a digest", destination: "ghcr.io/acme/agent-skills@" + zeroDigest},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := pushReference(tc.destination, "reviewer", "1.0.0")

			require.Error(t, err)
			assert.Contains(t, err.Error(), "destination")
		})
	}
}

// The name and the version come from the artifact, so the first operand has to
// carry both.
func TestPushRefusesAFirstOperandThatIsNotAStoreTag(t *testing.T) {
	t.Run("a bare name points at the command that lists the store", func(t *testing.T) {
		_, _, err := splitStoreTag("reviewer")

		require.Error(t, err)
		assert.Contains(t, err.Error(), "<name>:<version>")
		assert.Contains(t, err.Error(), "epos store ls")
	})

	// The same shape of message `pull` uses, because the reason is the same:
	// the store is tag-addressed and `sha256:…` is not in the character set a
	// tag allows.
	t.Run("a digest is refused the way pull refuses one", func(t *testing.T) {
		for _, operand := range []string{"reviewer@" + zeroDigest, zeroDigest} {
			_, _, err := splitStoreTag(operand)

			require.Error(t, err)
			assert.Contains(t, err.Error(), "push needs a tag")
			assert.Contains(t, err.Error(), "names a digest")
		}
	})

	t.Run("a tag with an empty version", func(t *testing.T) {
		_, _, err := splitStoreTag("reviewer:")

		require.Error(t, err)
		assert.Contains(t, err.Error(), "<name>:<version>")
	})
}

// EPOS_HOME rather than HOME: HOME is read by everything else in the process
// too, and on Windows — where the unit matrix also runs — os.UserHomeDir
// consults USERPROFILE instead.
func TestPushRefusesATagTheStoreDoesNotHoldBeforeAnyRequest(t *testing.T) {
	t.Setenv("EPOS_HOME", t.TempDir())
	t.Setenv("DOCKER_CONFIG", t.TempDir())

	var out bytes.Buffer
	err := runPush(context.Background(), &out, "reviewer:9.9.9", "oci://"+unroutable,
		registryOptions{plainHTTP: true})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "reviewer:9.9.9")
	assert.Contains(t, err.Error(), "epos store ls")
	// A connection error would mean a request was made against a tag the store
	// never held.
	assert.NotContains(t, err.Error(), "connect")
	assert.NotContains(t, err.Error(), "refused")
	assert.Empty(t, out.String())
}

func TestPushRefusesABadOperandBeforeReachingTheStore(t *testing.T) {
	t.Setenv("EPOS_HOME", t.TempDir())

	for _, operand := range []string{"reviewer", "reviewer@" + zeroDigest} {
		var out bytes.Buffer
		err := runPush(context.Background(), &out, operand, "oci://"+unroutable,
			registryOptions{plainHTTP: true})

		require.Error(t, err)
		assert.Empty(t, out.String())
		assert.NotContains(t, err.Error(), "connect")
	}
}

// D2 and the operand shape: nothing on this command sets the name or the
// version, because both come from the artifact.
func TestPushHasNoNameOrVersionFlag(t *testing.T) {
	cmd := newPushCommand()

	assert.Nil(t, cmd.Flags().Lookup("version"))
	assert.Nil(t, cmd.Flags().Lookup("name"))
	assert.Nil(t, cmd.Flags().Lookup("repository"))
	require.NotNil(t, cmd.Flags().Lookup("plain-http"))
	require.NotNil(t, cmd.Flags().Lookup("registry-config"))

	// helm's order: the artifact, then where it goes.
	assert.Equal(t, "push <name>:<version> <destination>", cmd.Use)
	assert.Error(t, cmd.Args(cmd, []string{"reviewer:1.0.0"}))
	assert.NoError(t, cmd.Args(cmd, []string{"reviewer:1.0.0", "oci://ghcr.io/acme"}))
	assert.Error(t, cmd.Args(cmd, []string{"reviewer:1.0.0", "oci://ghcr.io/acme", "extra"}))
}
