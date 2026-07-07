package cli

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/gaarutyunov/epos/internal/config"
	"github.com/gaarutyunov/epos/internal/frontend"
	"github.com/gaarutyunov/epos/internal/infrastructure/oci"
	"github.com/gaarutyunov/epos/internal/registry/discovery"
	"github.com/gaarutyunov/epos/internal/registry/proxy"
	"github.com/gaarutyunov/epos/internal/stats"
)

func newOverlayCmd(g *globals) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "overlay",
		Short: "Create, package, and push declarative overlays",
	}
	push := &cobra.Command{
		Use:   "push [DIR] REF",
		Short: "Publish an overlay as an OCI artifact",
		Args:  cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir, ref := ".", args[0]
			if len(args) == 2 {
				dir, ref = args[0], args[1]
			}
			if dir == "." {
				dir = g.workdirOrCwd()
			}
			_, err := g.newApp().OverlayPush(ctx(), dir, ref)
			return err
		},
	}
	cmd.AddCommand(push)
	return cmd
}

func newProxyCmd(g *globals) *cobra.Command {
	var upstream, listen string
	cmd := &cobra.Command{
		Use:   "proxy",
		Short: "Run the transparent pass-through registry proxy",
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := proxy.New(upstream, stats.New())
			if err != nil {
				return err
			}
			fmt.Fprintf(os.Stderr, "epos proxy: %s → %s\n", listen, upstream)
			return http.ListenAndServe(listen, p) //nolint:gosec // operator-provided listen addr
		},
	}
	cmd.Flags().StringVar(&upstream, "upstream", "", "upstream registry URL")
	cmd.Flags().StringVar(&listen, "listen", ":8080", "listen address")
	_ = cmd.MarkFlagRequired("upstream")
	return cmd
}

func newServeCmd(g *globals) *cobra.Command {
	var listen, registriesFile string
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Run the federated frontend",
		RunE: func(cmd *cobra.Command, args []string) error {
			client := &oci.Client{PlainHTTP: g.plainHTTP}
			var regs []config.Registry
			if registriesFile != "" {
				rc, err := config.LoadRegistries(registriesFile)
				if err != nil {
					return err
				}
				regs = rc.Registries
			}
			feed := &frontend.Feed{Registries: regs, Client: client, Stats: stats.New()}
			cat, err := feed.Gather(ctx())
			if err != nil {
				return err
			}
			fmt.Fprintf(os.Stderr, "epos serve: %s\n", listen)
			return http.ListenAndServe(listen, frontend.NewServer(cat).Handler()) //nolint:gosec
		},
	}
	cmd.Flags().StringVar(&listen, "listen", ":8080", "listen address")
	cmd.Flags().StringVar(&registriesFile, "registries", "", "registries.yaml path")
	return cmd
}

func newRegistryCmd(g *globals) *cobra.Command {
	cmd := &cobra.Command{Use: "registry", Short: "Authenticate the client to a registry"}
	login := &cobra.Command{
		Use:   "login HOST",
		Short: "Authenticate to a registry (reuses the Docker credential config)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Fprintf(os.Stderr, "epos registry login %s (uses the client's Docker credential store; Epos stores no secrets)\n", args[0])
			return nil
		},
	}
	logout := &cobra.Command{
		Use:   "logout HOST",
		Args:  cobra.ExactArgs(1),
		Short: "Remove client credentials for a registry",
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Fprintf(os.Stderr, "epos registry logout %s\n", args[0])
			return nil
		},
	}
	cmd.AddCommand(login, logout)
	return cmd
}

func newSearchCmd(g *globals) *cobra.Command {
	var registriesFile string
	cmd := &cobra.Command{
		Use:   "search TERM",
		Short: "Search discoverable Skills across configured registries",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			term := ""
			if len(args) == 1 {
				term = args[0]
			}
			a := g.newApp()
			client := &oci.Client{PlainHTTP: g.plainHTTP}
			var regs []config.Registry
			if registriesFile != "" {
				rc, err := config.LoadRegistries(registriesFile)
				if err != nil {
					return err
				}
				regs = rc.Registries
			} else if g.registry != "" {
				regs = []config.Registry{{Name: "default", URL: schemeFor(g.plainHTTP) + g.registry}}
			}
			d := &discovery.Discoverer{Client: client}
			for _, reg := range regs {
				res, err := d.Discover(ctx(), reg)
				if err != nil {
					continue
				}
				for _, repo := range res.Repos {
					if term == "" || contains(repo, term) {
						fmt.Fprintln(a.Opts.Out, reg.Name, repo)
					}
				}
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&registriesFile, "registries", "", "registries.yaml path")
	return cmd
}

func newLockCmd(g *globals) *cobra.Command {
	return &cobra.Command{
		Use:   "lock PATH",
		Short: "Resolve and write the lockfile without materializing",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			a := g.newApp()
			res, err := a.Compose(ctx(), args[0], false)
			if err != nil {
				return err
			}
			fmt.Fprintf(a.Opts.Out, "resolved %d pinned layer(s)\n", len(res.Pins))
			return nil
		},
	}
}

func (g *globals) workdirOrCwd() string {
	if g.workdir != "" {
		return g.workdir
	}
	wd, _ := os.Getwd()
	return wd
}

func writeMergedTree(dir string, files map[string][]byte) error {
	for rel, data := range files {
		full := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(full, data, 0o644); err != nil {
			return err
		}
	}
	return nil
}

func schemeFor(plainHTTP bool) string {
	if plainHTTP {
		return "http://"
	}
	return "https://"
}

func contains(s, sub string) bool {
	return len(sub) == 0 || (len(s) >= len(sub) && indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
