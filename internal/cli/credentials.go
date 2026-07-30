package cli

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/spf13/cobra"
	"oras.land/oras-go/v2/registry/remote/auth"
	"oras.land/oras-go/v2/registry/remote/credentials"
	"oras.land/oras-go/v2/registry/remote/errcode"
)

// registryOptions is what every command that contacts a registry needs: how to
// reach it, and where its credentials come from.
//
// One struct, built in one place, because the alternative is two answers to
// "where do credentials come from". `epos sign` and `epos attest` *write* a
// referrer manifest to the registry, and before this they did it through a
// repository whose Client was never set — that is, anonymously, which cannot
// work against any registry that requires authentication to write. Putting the
// credentials anywhere but here would have left that path on the broken answer.
//
// Nothing here reads an environment variable of its own. `DOCKER_CONFIG` is
// read by oras-go inside NewStoreFromDocker, so the CLI needs no configuration
// library and no provider chain: it has flags, and epos-registry's koanf config
// is a different program's problem.
type registryOptions struct {
	plainHTTP      bool
	registryConfig string
}

func (o *registryOptions) bind(cmd *cobra.Command) {
	flags := cmd.Flags()
	flags.BoolVar(&o.plainHTTP, "plain-http", false, "talk to the registry over HTTP")
	flags.StringVar(&o.registryConfig, "registry-config", "",
		"registry credentials file to use instead of Docker's "+
			"($DOCKER_CONFIG/config.json, or ~/.docker/config.json)")
}

// store resolves the credential store, first match winning:
//
//  1. --registry-config <path>, when given (helm's flag name, helm's meaning);
//  2. $DOCKER_CONFIG/config.json, when DOCKER_CONFIG is set;
//  3. ~/.docker/config.json together with the platform's native helpers —
//     osxkeychain, wincred, pass;
//  4. nothing, in which case requests are made anonymously. Finding no
//     credential is a normal outcome and never an error: credential resolution
//     is per registry, and reading a public registry must keep working for a
//     user who has credentials stored for a different one.
//
// Steps 2 and 3 are one call, because that is exactly what NewStoreFromDocker
// does. Nothing about the file format is epos's own: an epos-native credential
// file would be a fourth place a registry token lives on a developer's machine,
// would be invisible to docker and oras, and would put epos in charge of file
// modes, atomic replacement and keychain integration that oras-go already gets
// right.
func (o registryOptions) store() (*credentials.DynamicStore, error) {
	opts := credentials.StoreOptions{
		// Docker's format *is* a base64 blob in a file when no helper is
		// configured. The login command's help says so in one sentence rather
		// than implying the file is encrypted.
		AllowPlaintextPut: true,
		// Detection is for the ambient configuration only. A config file named
		// on the command line is the user saying "keep it here", and quietly
		// redirecting the credential into a platform helper they did not ask
		// for would make --registry-config mean something other than what it
		// says. A helper the file itself names (credsStore, credHelpers) is
		// still honoured either way.
		DetectDefaultNativeStore: o.registryConfig == "",
	}
	if o.registryConfig != "" {
		return credentials.NewStore(o.registryConfig, opts)
	}
	return credentials.NewStoreFromDocker(opts)
}

// client builds the credential-bearing client every registry command uses.
//
// transport is the client's inner RoundTripper, or nil for the default. The
// composition is the part worth stating: the auth client wraps the transport
// rather than the other way round, so `pull`'s Epos-Download header (SPEC.md
// 5.2) still reaches the wire on an authenticated pull. Replacing the client
// instead of nesting it is how a careless refactor silently stops a verified
// download from being counted.
func (o registryOptions) client(transport http.RoundTripper) (*auth.Client, error) {
	store, err := o.store()
	if err != nil {
		return nil, fmt.Errorf("read the registry credentials: %w", err)
	}
	return &auth.Client{
		// A nil Transport is http.DefaultTransport.
		Client:     &http.Client{Transport: transport},
		Cache:      auth.NewCache(),
		Credential: credentials.Credential(store),
	}, nil
}

// explainAuth turns a registry's refusal to serve an unauthenticated — or
// wrongly authenticated — request into a message that says what to do.
//
// Two shapes, because oras-go reports the same refusal in two ways, and the
// design's "read the status code" only covers the first:
//
//   - A registry that challenges with Bearer answers the request 401, and the
//     response arrives as an *errcode.ErrorResponse. That is read the way
//     discover.go's unsupported() reads one — errors.As, then the status code —
//     rather than through a second way of asking what a registry answered.
//   - A registry that challenges with Basic never gets a second request at all
//     when nothing is stored: the client has nothing to send and gives up with
//     auth.ErrBasicCredentialNotFound, so there is no 401 to match on. zot with
//     htpasswd is exactly this, which is how it was found.
//
// 403 is deliberately not folded in. A credential that is valid but
// unauthorised is a different problem from a missing one, and telling the user
// to log in again would send them at the wrong thing.
//
// The two messages differ because the causes do. A command that used to succeed
// anonymously and now sends a stale credential must say the *stored* credential
// was refused, or the change reads as an unexplained regression. Neither
// message contains the credential or the header it travelled in.
func (o registryOptions) explainAuth(ctx context.Context, host string, err error) error {
	var resp *errcode.ErrorResponse
	refused := errors.As(err, &resp) && resp.StatusCode == http.StatusUnauthorized
	if !refused && !errors.Is(err, auth.ErrBasicCredentialNotFound) {
		return err
	}
	if o.hasCredential(ctx, host) {
		return fmt.Errorf("authentication failed for %s: the stored credential was rejected; "+
			"replace it with: epos registry login %s", host, host)
	}
	return fmt.Errorf("authentication failed for %s: no credential is stored for it; "+
		"log in with: epos registry login %s", host, host)
}

// hasCredential reports whether the ambient store holds anything for host.
//
// Only ever used to choose between two messages, so a store that cannot be read
// answers "no": failing to explain an error is not a reason to replace it.
func (o registryOptions) hasCredential(ctx context.Context, host string) bool {
	store, err := o.store()
	if err != nil {
		return false
	}
	cred, err := store.Get(ctx, credentials.ServerAddressFromRegistry(host))
	return err == nil && cred != auth.EmptyCredential
}
