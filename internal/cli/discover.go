package cli

import (
	"errors"
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/gaarutyunov/epos/internal/registry"
)

// The pipeline itself — the client interface, discover, the namespace filter,
// the search predicate and the missing-capability error — lives in
// internal/registry now. It moved because cmd/epos-registry's catalog needs it
// and must not import internal/cli: importing the CLI would link the entire
// command tree into the registry binary. What stays here is the command's own
// surface: the flags, the credential-bearing client they resolve to, and the
// printing.
//
//go:generate go tool mockgen -destination=mocks_test.go -package=cli github.com/gaarutyunov/epos/internal/registry Client

// printSkills writes a listing, one skill per line.
//
// Tab-separated rather than aligned: the columns are a repository reference, a
// name and a description, and a listing that shifts its own layout as
// descriptions get longer is worse to diff and worse to pipe into cut.
func printSkills(out io.Writer, listing []registry.Skill) {
	for _, s := range listing {
		if s.Version == "" {
			fmt.Fprintln(out, s.Repository)
			continue
		}
		fmt.Fprintf(out, "%s:%s\t%s\t%s\n", s.Repository, s.Version, s.Name, s.Description)
	}
}

// discoveryFlags configure which registry the discovery commands enumerate.
//
// --registry names a registry to *read*; the `epos registry` command group
// names registry *credentials*. The echo is knowing: helm carries the same
// pair, and renaming either to avoid it would cost more familiarity than it
// buys.
type discoveryFlags struct {
	registry  string
	namespace string
	registryOptions
}

func (f *discoveryFlags) bind(cmd *cobra.Command) {
	flags := cmd.Flags()
	flags.StringVar(&f.registry, "registry", "",
		"registry to enumerate, as host[:port] (required)")
	flags.StringVar(&f.namespace, "namespace", "",
		"only enumerate repositories under this namespace (default: the whole registry)")
	f.registryOptions.bind(cmd)
}

// open resolves the flags into a client.
//
// The commands take the client as an argument rather than building it inside
// themselves, so a test can drive the whole of runList and runSearch — the
// laziness of 7.2 and the missing-capability message of 7.1 included — without
// a registry.
func (f *discoveryFlags) open() (registry.Client, error) {
	if f.registry == "" {
		return nil, errors.New("a registry is required: pass --registry")
	}
	opts, err := f.registryOptions.resolve()
	if err != nil {
		return nil, err
	}
	return registry.NewOCIRegistry(f.registry, opts)
}
