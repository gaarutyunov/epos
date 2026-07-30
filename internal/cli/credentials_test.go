package cli

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"oras.land/oras-go/v2/registry/remote/auth"
	"oras.land/oras-go/v2/registry/remote/credentials"

	"github.com/gaarutyunov/epos/internal/artifact"
)

// writeDockerConfig writes a Docker configuration file holding one credential
// and returns its path.
//
// Docker's own format, because that is the whole point: a login performed by
// docker, oras or helm has to be usable by epos and the other way round.
func writeDockerConfig(t *testing.T, dir, host, username, password string) string {
	t.Helper()

	require.NoError(t, os.MkdirAll(dir, 0o700))
	path := filepath.Join(dir, "config.json")
	body, err := json.Marshal(map[string]any{
		"auths": map[string]any{
			host: map[string]any{
				"auth": base64.StdEncoding.EncodeToString(
					[]byte(username + ":" + password)),
			},
		},
	})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, body, 0o600))
	return path
}

func credentialFor(t *testing.T, opts registryOptions, host string) auth.Credential {
	t.Helper()

	store, err := opts.store()
	require.NoError(t, err)
	cred, err := credentials.Credential(store)(context.Background(), host)
	require.NoError(t, err)
	return cred
}

// The precedence of design D8, asserted one source at a time: an explicit
// --registry-config, then $DOCKER_CONFIG, then nothing at all.
//
// DOCKER_CONFIG is set rather than HOME. HOME is read by everything else in the
// process too, and on Windows — where the unit matrix also runs — it is not
// even the variable os.UserHomeDir consults.
func TestCredentialsResolveInTheStatedOrder(t *testing.T) {
	const host = "registry.example"

	t.Run("an explicit path wins over the environment", func(t *testing.T) {
		root := t.TempDir()
		explicit := writeDockerConfig(t, filepath.Join(root, "explicit"), host, "alice", "from-the-flag")
		ambient := filepath.Join(root, "ambient")
		writeDockerConfig(t, ambient, host, "bob", "from-the-environment")
		t.Setenv("DOCKER_CONFIG", ambient)

		cred := credentialFor(t, registryOptions{registryConfig: explicit}, host)
		assert.Equal(t, "alice", cred.Username)
		assert.Equal(t, "from-the-flag", cred.Password)
	})

	t.Run("the environment overrides the default location", func(t *testing.T) {
		ambient := filepath.Join(t.TempDir(), "ambient")
		writeDockerConfig(t, ambient, host, "bob", "from-the-environment")
		t.Setenv("DOCKER_CONFIG", ambient)

		cred := credentialFor(t, registryOptions{}, host)
		assert.Equal(t, "bob", cred.Username)
		assert.Equal(t, "from-the-environment", cred.Password)
	})

	// The compatibility case. pull, verify, list and search were
	// unconditionally anonymous before credentials existed, so finding none has
	// to stay a normal outcome rather than becoming an error.
	t.Run("anonymous is a valid outcome", func(t *testing.T) {
		t.Setenv("DOCKER_CONFIG", t.TempDir())

		cred := credentialFor(t, registryOptions{}, host)
		assert.Equal(t, auth.EmptyCredential, cred)
	})

	// Credential resolution is per registry: a user with a credential stored
	// for one registry still reads a different, public one anonymously.
	t.Run("a credential for one registry is not sent to another", func(t *testing.T) {
		path := writeDockerConfig(t, t.TempDir(), host, "alice", "s3cret")

		cred := credentialFor(t, registryOptions{registryConfig: path}, "public.example")
		assert.Equal(t, auth.EmptyCredential, cred)
	})
}

// The composition of D9, asserted against a server that really performs the
// handshake: the auth client wraps the Epos-Download transport, so an
// authenticated pull is still a counted one (SPEC.md 5.2).
//
// This is the one thing a careless refactor breaks silently — replacing
// repo.Client with an auth client rather than nesting the transport inside it
// leaves epos authenticated and its downloads uncounted, and nothing else fails.
func TestAnAuthenticatedRequestStillCarriesTheDownloadHeader(t *testing.T) {
	var downloads, authorizations []string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		downloads = append(downloads, r.Header.Get(artifact.DownloadHeader))
		authorizations = append(authorizations, r.Header.Get("Authorization"))
		if r.Header.Get("Authorization") == "" {
			w.Header().Set("WWW-Authenticate", `Basic realm="epos"`)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	host := strings.TrimPrefix(srv.URL, "http://")
	opts := registryOptions{
		plainHTTP:      true,
		registryConfig: writeDockerConfig(t, t.TempDir(), host, "alice", "s3cret"),
	}

	client, err := opts.downloadClient("reviewer@1.0.0")
	require.NoError(t, err)

	req, err := http.NewRequest(http.MethodGet, srv.URL+"/v2/", nil)
	require.NoError(t, err)
	resp, err := client.Do(req)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	require.Len(t, downloads, 2, "the challenge and the authenticated retry")
	for i, value := range downloads {
		assert.Equal(t, "reviewer@1.0.0", value,
			"request %d reached the wire without Epos-Download", i+1)
	}
	assert.NotEmpty(t, authorizations[1], "the retry carried no credential")
}

// A publish is not a download (SPEC.md 5.1), so push's client sends no header
// while still authenticating.
func TestPushesDoNotSendTheDownloadHeader(t *testing.T) {
	var downloads []string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		downloads = append(downloads, r.Header.Get(artifact.DownloadHeader))
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	host := strings.TrimPrefix(srv.URL, "http://")
	opts := registryOptions{
		plainHTTP:      true,
		registryConfig: writeDockerConfig(t, t.TempDir(), host, "alice", "s3cret"),
	}

	client, err := opts.client(nil)
	require.NoError(t, err)

	req, err := http.NewRequest(http.MethodGet, srv.URL+"/v2/", nil)
	require.NoError(t, err)
	resp, err := client.Do(req)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())

	require.Len(t, downloads, 1)
	assert.Empty(t, downloads[0], "a publish must not be counted as a download")
}

// The 401 messages of D8 and D10. Both name the registry; neither carries the
// credential or the header it travelled in; and they differ, because "you have
// no credential" and "the one you have was refused" are different problems with
// different fixes.
func TestAnAuthenticationFailureSaysWhatToDo(t *testing.T) {
	const host = "registry.example"
	ctx := context.Background()

	t.Run("with no credential stored, it names the login command", func(t *testing.T) {
		t.Setenv("DOCKER_CONFIG", t.TempDir())

		err := registryOptions{}.explainAuth(ctx, host, statusError(http.StatusUnauthorized))

		require.Error(t, err)
		assert.Contains(t, err.Error(), host)
		assert.Contains(t, err.Error(), "epos registry login "+host)
		assert.Contains(t, err.Error(), "no credential is stored")
	})

	// The compatibility case the proposal calls out: a command that used to
	// succeed anonymously now sends a stale docker login, and has to say so.
	t.Run("with a stored credential, it says the stored credential was rejected", func(t *testing.T) {
		opts := registryOptions{
			registryConfig: writeDockerConfig(t, t.TempDir(), host, "alice", "s3cret"),
		}

		err := opts.explainAuth(ctx, host, statusError(http.StatusUnauthorized))

		require.Error(t, err)
		assert.Contains(t, err.Error(), host)
		assert.Contains(t, err.Error(), "stored credential was rejected")
		assert.NotContains(t, err.Error(), "s3cret", "the credential must never be reported")
		assert.NotContains(t, err.Error(), "Authorization")
		assert.NotContains(t, err.Error(), "YWxpY2U=")
	})

	// 403 is not folded in: a credential that is valid but unauthorised is a
	// different problem, and telling the user to log in again aims them at the
	// wrong thing (design D10).
	t.Run("a 403 is left alone", func(t *testing.T) {
		original := statusError(http.StatusForbidden)

		err := registryOptions{}.explainAuth(ctx, host, original)

		assert.Same(t, original, err)
	})

	t.Run("an error that is not a registry response is left alone", func(t *testing.T) {
		original := fmt.Errorf("dial tcp: connection refused")

		err := registryOptions{}.explainAuth(ctx, host, original)

		assert.Same(t, original, err)
	})
}
