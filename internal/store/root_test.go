package store

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// An explicit root wins over the environment, which is what lets a test root
// epos at a directory it owns without setting anything global.
func TestRootPrefersTheExplicitDirectory(t *testing.T) {
	t.Setenv(RootEnv, filepath.Join(t.TempDir(), "from-env"))
	want := filepath.Join(t.TempDir(), "explicit")

	got, err := Root(want)
	require.NoError(t, err)
	assert.Equal(t, want, got)
}

func TestRootUsesTheEnvironmentWhenNoneIsPassed(t *testing.T) {
	want := filepath.Join(t.TempDir(), "from-env")
	t.Setenv(RootEnv, want)

	got, err := Root("")
	require.NoError(t, err)
	assert.Equal(t, want, got)
}

// An empty EPOS_HOME is unset, not a root at the working directory: an
// exported-but-empty variable is a common shell accident, and honouring it
// would put the store somewhere nobody named.
func TestRootFallsBackToTheHomeDirectory(t *testing.T) {
	t.Setenv(RootEnv, "")

	home, err := os.UserHomeDir()
	require.NoError(t, err)

	got, err := Root("")
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(home, ".epos"), got)
}

// The default is derived from os.UserHomeDir, not from $HOME: on Windows the
// home directory is USERPROFILE, so a test that moved HOME would move nothing.
func TestRootIgnoresHOMEWhenEPOSHOMEIsSet(t *testing.T) {
	want := filepath.Join(t.TempDir(), "root")
	t.Setenv(RootEnv, want)

	got, err := Root("")
	require.NoError(t, err)

	home, err := os.UserHomeDir()
	require.NoError(t, err)
	assert.NotEqual(t, filepath.Join(home, ".epos"), got)
	assert.Equal(t, want, got)
}

// The layout sits inside the root, so two roots are two stores: what one holds
// the other cannot see.
func TestUnderPutsTheLayoutInsideTheRoot(t *testing.T) {
	root := t.TempDir()
	s := Under(root)

	assert.Equal(t, filepath.Join(root, "store"), s.Path())

	other := Under(t.TempDir())
	assert.NotEqual(t, s.Path(), other.Path())
}

// Under reads nothing: an EPOS_HOME pointing elsewhere must not reach a store
// the caller rooted itself.
func TestUnderIgnoresTheEnvironment(t *testing.T) {
	t.Setenv(RootEnv, filepath.Join(t.TempDir(), "from-env"))

	root := t.TempDir()
	assert.Equal(t, filepath.Join(root, "store"), Under(root).Path())
}

func TestDefaultResolvesThroughTheEnvironment(t *testing.T) {
	root := t.TempDir()
	t.Setenv(RootEnv, root)

	s, err := Default()
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(root, "store"), s.Path())
}
