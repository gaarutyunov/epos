package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// basicAuthRegistry is a registry that really performs the handshake: it
// challenges, and it accepts exactly one credential. `credentials.Login` pings
// it before storing anything, so a login test needs something to ping.
func basicAuthRegistry(t *testing.T, username, password string) string {
	t.Helper()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()
		if !ok || user != username || pass != password {
			w.Header().Set("WWW-Authenticate", `Basic realm="epos"`)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	return strings.TrimPrefix(srv.URL, "http://")
}

// storedCredentials reads back what login wrote, so a test can assert on the
// file rather than on the API that produced it.
func storedCredentials(t *testing.T, path string) map[string]any {
	t.Helper()

	body, err := os.ReadFile(path)
	require.NoError(t, err)
	var config struct {
		Auths map[string]any `json:"auths"`
	}
	require.NoError(t, json.Unmarshal(body, &config))
	return config.Auths
}

// The whole of what the issue asks for: a machine with neither docker nor oras
// obtains a credential and can use it.
func TestLoginStoresACredentialThePushPathCanFind(t *testing.T) {
	const (
		user   = "alice"
		secret = "s3cret-token"
	)
	host := basicAuthRegistry(t, user, secret)
	path := filepath.Join(t.TempDir(), "config.json")
	opts := registryOptions{plainHTTP: true, registryConfig: path}

	var out, errOut bytes.Buffer
	err := runRegistryLogin(context.Background(), &out, &errOut,
		strings.NewReader(secret+"\n"), host, user, true, opts)

	require.NoError(t, err)
	assert.Contains(t, out.String(), host)
	assert.Contains(t, out.String(), user)
	assert.NotContains(t, out.String()+errOut.String(), secret,
		"a login must never echo the secret")

	assert.Contains(t, storedCredentials(t, path), host)
	assert.Equal(t, user, credentialFor(t, opts, host).Username,
		"the credential the push path resolves is the one login stored")
	assert.Equal(t, secret, credentialFor(t, opts, host).Password)
}

// 3.5: a bad credential fails at login rather than at the next push, because
// Login pings the registry before storing anything.
func TestLoginRefusesACredentialTheRegistryRejects(t *testing.T) {
	host := basicAuthRegistry(t, "alice", "the-right-one")
	path := filepath.Join(t.TempDir(), "config.json")

	var out, errOut bytes.Buffer
	err := runRegistryLogin(context.Background(), &out, &errOut,
		strings.NewReader("the-wrong-one\n"), host, "alice", true,
		registryOptions{plainHTTP: true, registryConfig: path})

	require.Error(t, err)
	assert.Contains(t, err.Error(), host)
	assert.NotContains(t, err.Error(), "the-wrong-one",
		"the failure must not report what was sent")
	assert.NoFileExists(t, path, "a rejected credential is not stored")
}

// D8's refusal of helm parity, asserted over the command's own flags: none of
// them takes a password or token as its value.
func TestLoginHasNoPasswordFlag(t *testing.T) {
	flags := newRegistryLoginCommand().Flags()

	assert.Nil(t, flags.Lookup("password"), "argv is world-readable; there is no --password")
	assert.Nil(t, flags.ShorthandLookup("p"))
	require.NotNil(t, flags.Lookup("password-stdin"))
	require.NotNil(t, flags.ShorthandLookup("u"))

	// And the reason is in the help, not only in a design document.
	assert.Contains(t, newRegistryLoginCommand().Long, "no --password")
}

func TestLoginRefusesToGuessWhatIsMissing(t *testing.T) {
	host := basicAuthRegistry(t, "alice", "s3cret")
	opts := registryOptions{plainHTTP: true,
		registryConfig: filepath.Join(t.TempDir(), "config.json")}

	// A stored credential that names no user cannot be matched to an account.
	t.Run("a login states whose credential it is", func(t *testing.T) {
		err := runRegistryLogin(context.Background(), &bytes.Buffer{}, &bytes.Buffer{},
			strings.NewReader("s3cret"), host, "", true, opts)

		require.Error(t, err)
		assert.Contains(t, err.Error(), "--username")
	})

	// Never prompt into a pipe: the question would hang on an answer nobody
	// can give. A bytes.Reader is not a terminal, which is exactly the case.
	t.Run("a non-interactive login with no secret says how to supply one", func(t *testing.T) {
		err := runRegistryLogin(context.Background(), &bytes.Buffer{}, &bytes.Buffer{},
			strings.NewReader(""), host, "alice", false, opts)

		require.Error(t, err)
		assert.Contains(t, err.Error(), "--password-stdin")
	})

	// And never store an empty secret, which would move the failure to the
	// next push.
	t.Run("an empty piped secret is refused", func(t *testing.T) {
		err := runRegistryLogin(context.Background(), &bytes.Buffer{}, &bytes.Buffer{},
			strings.NewReader("\n"), host, "alice", true, opts)

		require.Error(t, err)
		assert.Contains(t, err.Error(), "no secret")
	})
}

// The secret arrives on standard input and nowhere else. os.Args belongs to the
// test binary here, so this asserts the only thing a unit can: that the
// function reads it from the reader it was handed. The integration suite
// asserts the argument vector of a real `epos registry login`.
func TestLoginTakesThePipedSecretFromStandardInput(t *testing.T) {
	const secret = "ghp_piped_token"
	host := basicAuthRegistry(t, "alice", secret)
	path := filepath.Join(t.TempDir(), "config.json")
	opts := registryOptions{plainHTTP: true, registryConfig: path}

	// A trailing newline is the shell's, not the token's.
	err := runRegistryLogin(context.Background(), &bytes.Buffer{}, &bytes.Buffer{},
		strings.NewReader(secret+"\n"), host, "alice", true, opts)

	require.NoError(t, err)
	assert.Equal(t, secret, credentialFor(t, opts, host).Password)
	for _, arg := range os.Args {
		assert.NotContains(t, arg, secret)
	}
}

// 3.8: the credential file is owner-only, in an owner-only directory. Skipped
// on Windows, which has no POSIX mode bits.
//
// The configuration is seeded with an unrelated credential first: that is what
// stops oras-go detecting a platform native store, so a developer running this
// on a machine with docker-credential-osxkeychain installed exercises the file
// store the assertion is about rather than their keychain.
func TestAStoredCredentialIsOwnerOnly(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX file modes")
	}

	const secret = "s3cret"
	host := basicAuthRegistry(t, "alice", secret)
	dir := filepath.Join(t.TempDir(), "credentials")
	path := writeDockerConfig(t, dir, "unrelated.example", "bob", "another")
	opts := registryOptions{plainHTTP: true, registryConfig: path}

	require.NoError(t, runRegistryLogin(context.Background(), &bytes.Buffer{}, &bytes.Buffer{},
		strings.NewReader(secret), host, "alice", true, opts))

	file, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), file.Mode().Perm(),
		"a registry token must not be readable by other users")

	parent, err := os.Stat(dir)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o700), parent.Mode().Perm())

	// The previous credential is still there: the write replaced the file
	// through a rename rather than truncating it in place.
	assert.Contains(t, storedCredentials(t, path), "unrelated.example")
	assert.Contains(t, storedCredentials(t, path), host)
}

func TestLogout(t *testing.T) {
	const secret = "s3cret"
	host := basicAuthRegistry(t, "alice", secret)
	path := filepath.Join(t.TempDir(), "config.json")
	opts := registryOptions{plainHTTP: true, registryConfig: path}

	t.Run("a host with no stored credential succeeds quietly", func(t *testing.T) {
		var out bytes.Buffer
		require.NoError(t, runRegistryLogout(context.Background(), &out, host, opts))
		assert.Contains(t, out.String(), "no credential")
	})

	t.Run("logging out removes the credential", func(t *testing.T) {
		require.NoError(t, runRegistryLogin(context.Background(), &bytes.Buffer{},
			&bytes.Buffer{}, strings.NewReader(secret), host, "alice", true, opts))
		require.Equal(t, "alice", credentialFor(t, opts, host).Username)

		var out bytes.Buffer
		require.NoError(t, runRegistryLogout(context.Background(), &out, host, opts))

		assert.Contains(t, out.String(), host)
		assert.Empty(t, credentialFor(t, opts, host).Username,
			"a push after a logout is unauthenticated")
	})
}

// D7: the subcommands live under `registry`, because there is nothing called
// "epos" to log in to.
func TestTheCredentialCommandsSayWhatIsBeingLoggedInTo(t *testing.T) {
	cmd := newRegistryCommand()

	assert.Equal(t, "registry", cmd.Name())
	assert.NoError(t, cmd.Args(cmd, nil), "the parent is help-only, like store")
	assert.Error(t, cmd.Args(cmd, []string{"login"}))

	names := make([]string, 0, len(cmd.Commands()))
	for _, sub := range cmd.Commands() {
		names = append(names, sub.Name())
	}
	assert.ElementsMatch(t, []string{"login", "logout"}, names)

	// Storing without a helper is not presented as encryption.
	assert.Contains(t, cmd.Long, "not encryption")
}
