package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"
	"oras.land/oras-go/v2/registry/remote"
	"oras.land/oras-go/v2/registry/remote/auth"
	"oras.land/oras-go/v2/registry/remote/credentials"
)

// newRegistryCommand carries the credential subcommands.
//
// `registry login` rather than `login`, for the reason helm reached the same
// shape: `epos login` reads as logging in to *epos*, and epos has no account,
// no service and no identity of its own — SPEC.md's non-goals are explicit that
// Epos does not issue credentials. `registry login` says what is being logged
// in to.
//
// The commands exist at all because without them `epos push` would still send
// the user to install `oras` or `docker` to get a credential into the store,
// which is the complaint the whole change is about, one command later.
func newRegistryCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "registry",
		Short: "Log in to and out of OCI registries",
		Long: "registry manages the credentials epos sends to a registry.\n\n" +
			"They are read from and written to Docker's configuration and the\n" +
			"platform's native credential helpers, so a login performed by epos,\n" +
			"docker, oras or helm is usable by all of them. Where no native helper\n" +
			"is configured the credential is stored in a configuration file — as\n" +
			"base64, which is not encryption.\n\n" +
			"Epos issues no credentials of its own; these commands only hold the\n" +
			"ones your registry gave you.",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE:         func(cmd *cobra.Command, _ []string) error { return cmd.Help() },
	}
	cmd.AddCommand(newRegistryLoginCommand(), newRegistryLogoutCommand())
	return cmd
}

func newRegistryLoginCommand() *cobra.Command {
	var (
		username      string
		passwordStdin bool
		opts          registryOptions
	)

	cmd := &cobra.Command{
		Use:   "login <host>",
		Short: "Log in to a registry",
		Long: "login verifies a credential against the registry and then stores it,\n" +
			"so a bad password fails here rather than at the next push.\n\n" +
			"The secret is read from standard input with --password-stdin, or typed\n" +
			"at a prompt that does not echo. There is deliberately no --password\n" +
			"flag: an argument vector is world-readable through /proc/<pid>/cmdline\n" +
			"and lands in your shell history.\n\n" +
			"Where the platform has no credential helper configured, the credential\n" +
			"is written to the configuration file as base64. That is Docker's\n" +
			"format and it is not encryption; the file is created readable only by\n" +
			"you.",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runRegistryLogin(cmd.Context(), cmd.OutOrStdout(), cmd.ErrOrStderr(),
				cmd.InOrStdin(), args[0], username, passwordStdin, opts)
		},
	}
	cmd.Flags().StringVarP(&username, "username", "u", "", "user to log in as (required)")
	cmd.Flags().BoolVar(&passwordStdin, "password-stdin", false,
		"read the password or token from standard input")
	opts.bind(cmd)
	return cmd
}

func runRegistryLogin(ctx context.Context, out, errOut io.Writer, in io.Reader,
	host, username string, passwordStdin bool, opts registryOptions) error {
	// A stored credential that names no user cannot be matched to a registry
	// account, so this is asked for rather than defaulted.
	if username == "" {
		return errors.New("a user is required: pass -u/--username")
	}

	secret, err := readSecret(errOut, in, passwordStdin)
	if err != nil {
		return err
	}

	store, err := opts.store()
	if err != nil {
		return fmt.Errorf("open the registry credentials: %w", err)
	}

	reg, err := remote.NewRegistry(host)
	if err != nil {
		return fmt.Errorf("registry %q: %w", host, err)
	}
	reg.PlainHTTP = opts.plainHTTP

	// Login pings the registry with the credential before storing it. Its own
	// error text names the registry and never the secret.
	if err := credentials.Login(ctx, store, reg,
		auth.Credential{Username: username, Password: secret}); err != nil {
		return fmt.Errorf("log in to %s: %w", host, err)
	}

	fmt.Fprintf(out, "logged in to %s as %s\n", host, username)
	return nil
}

// readSecret obtains the password or token without letting it reach argv.
//
// Three cases, and the third is a refusal rather than a prompt: prompting into
// a pipe hangs on a question nobody can answer, and storing an empty secret
// would make the next push fail somewhere else entirely.
func readSecret(errOut io.Writer, in io.Reader, passwordStdin bool) (string, error) {
	if passwordStdin {
		body, err := io.ReadAll(in)
		if err != nil {
			return "", fmt.Errorf("read the secret from standard input: %w", err)
		}
		// One trailing newline, because `echo $TOKEN | epos registry login` is
		// how this is written and the newline is the shell's, not the token's.
		secret := strings.TrimRight(string(body), "\r\n")
		if secret == "" {
			return "", errors.New("standard input carried no secret; " +
				"--password-stdin needs one piped to it")
		}
		return secret, nil
	}

	f, ok := in.(*os.File)
	if !ok || !term.IsTerminal(int(f.Fd())) {
		return "", errors.New("no secret was supplied: pipe one in and pass " +
			"--password-stdin, or run this from a terminal to be prompted. " +
			"There is deliberately no --password flag, because an argument " +
			"vector is world-readable")
	}

	fmt.Fprint(errOut, "Password: ")
	body, err := term.ReadPassword(int(f.Fd()))
	fmt.Fprintln(errOut)
	if err != nil {
		return "", fmt.Errorf("read the password: %w", err)
	}
	secret := strings.TrimRight(string(body), "\r\n")
	if secret == "" {
		return "", errors.New("an empty password is not stored")
	}
	return secret, nil
}

func newRegistryLogoutCommand() *cobra.Command {
	var opts registryOptions

	cmd := &cobra.Command{
		Use:   "logout <host>",
		Short: "Log out of a registry",
		Long: "logout removes the stored credential for a registry. Logging out of\n" +
			"one you are not logged in to succeeds and does nothing.\n\n" +
			"The credential is removed from wherever it was stored, which may be a\n" +
			"native credential helper shared with docker, oras and helm.",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runRegistryLogout(cmd.Context(), cmd.OutOrStdout(), args[0], opts)
		},
	}
	opts.bind(cmd)
	return cmd
}

func runRegistryLogout(ctx context.Context, out io.Writer, host string,
	opts registryOptions) error {
	store, err := opts.store()
	if err != nil {
		return fmt.Errorf("open the registry credentials: %w", err)
	}

	// Asked first so that logging out of a registry with nothing stored is a
	// quiet success rather than a helper's "credentials not found" surfacing as
	// a failure. A logout that finds nothing has achieved what it was asked for.
	address := credentials.ServerAddressFromRegistry(host)
	if cred, err := store.Get(ctx, address); err == nil && cred == auth.EmptyCredential {
		fmt.Fprintf(out, "no credential stored for %s\n", host)
		return nil
	}

	if err := credentials.Logout(ctx, store, host); err != nil {
		return fmt.Errorf("log out of %s: %w", host, err)
	}

	fmt.Fprintf(out, "logged out of %s\n", host)
	return nil
}
