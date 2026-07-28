package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gaarutyunov/epos/internal/sign"
)

func TestGenerateKeyPairWritesAMatchingPair(t *testing.T) {
	dir := t.TempDir()
	var out bytes.Buffer

	require.NoError(t, runGenerateKeyPair(&out, dir))

	privatePEM, err := os.ReadFile(filepath.Join(dir, "cosign.key"))
	require.NoError(t, err)
	publicPEM, err := os.ReadFile(filepath.Join(dir, "cosign.pub"))
	require.NoError(t, err)

	key, err := sign.ParsePrivateKey(privatePEM)
	require.NoError(t, err)
	pub, err := sign.ParsePublicKey(publicPEM)
	require.NoError(t, err)
	assert.True(t, key.PublicKey.Equal(pub), "cosign.pub is not the public half of cosign.key")

	assert.Contains(t, out.String(), "cosign.key")
	assert.Contains(t, out.String(), "cosign.pub")
}

// Replacing a signing key in place would leave every signature already made
// under it unverifiable, with nothing to say so.
func TestGenerateKeyPairRefusesToReplaceAnExistingKey(t *testing.T) {
	dir := t.TempDir()
	var out bytes.Buffer
	require.NoError(t, runGenerateKeyPair(&out, dir))

	err := runGenerateKeyPair(&out, dir)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "cosign.key")
}

// The key is read before anything is asked of the network, so a mistyped path
// is a mistyped path rather than a connection failure.
func TestVerifyNamesTheKeyFileItCouldNotRead(t *testing.T) {
	err := runVerify(context.Background(), &bytes.Buffer{},
		"registry.example/demo/reviewer:1.0.0", filepath.Join(t.TempDir(), "absent.pub"), false)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "absent.pub")
}

func TestVerifyNamesAKeyFileThatIsNotAKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cosign.pub")
	require.NoError(t, os.WriteFile(path, []byte("this is not a PEM block"), 0o600))

	err := runVerify(context.Background(), &bytes.Buffer{},
		"registry.example/demo/reviewer:1.0.0", path, false)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "no PEM block found")
}

// A registry may carry a port, and the tag separator is the last colon after
// the last slash. Signing and verifying reach the registry through the same
// parser pull does, so this is asserted where the new commands use it.
func TestSignParsesAReferenceWhoseHostCarriesAPort(t *testing.T) {
	repo, tag, err := newRepository("127.0.0.1:45100/demo/agent-skills/reviewer:1.0.0", true)

	require.NoError(t, err)
	assert.Equal(t, "1.0.0", tag)
	assert.Equal(t, "127.0.0.1:45100", repo.Reference.Registry)
	assert.Equal(t, "demo/agent-skills/reviewer", repo.Reference.Repository)
}

func TestSignNamesTheKeyFileItCouldNotRead(t *testing.T) {
	err := runSign(context.Background(), &bytes.Buffer{},
		"127.0.0.1:45100/demo/reviewer:1.0.0", filepath.Join(t.TempDir(), "absent.key"), true)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "absent.key")
}
