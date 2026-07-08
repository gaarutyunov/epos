package cli

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/gaarutyunov/epos/internal/config"
	"github.com/gaarutyunov/epos/internal/frontend"
	"github.com/gaarutyunov/epos/internal/infrastructure/oci"
	reggw "github.com/gaarutyunov/epos/internal/registry/adapter/out/gateway"
	regin "github.com/gaarutyunov/epos/internal/registry/app/port/in"
	regusecase "github.com/gaarutyunov/epos/internal/registry/app/usecase"
	regdomain "github.com/gaarutyunov/epos/internal/registry/domain"
	"github.com/gaarutyunov/epos/internal/registry/proxy"
	"github.com/gaarutyunov/epos/internal/stats"
)

func newOverlayCmd(g *globals) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "overlay",
		Short: "Create, package, and push declarative overlays",
	}
	pkg := &cobra.Command{
		Use:   "package [DIR]",
		Short: "Build an overlay OCI artifact locally without pushing",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir := g.workdirOrCwd()
			if len(args) == 1 {
				dir = args[0]
			}
			_, err := g.newApp().OverlayPackage(ctx(), dir)
			return err
		},
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
	cmd.AddCommand(pkg, push)
	return cmd
}

// newDependencyCmd implements `epos dependency ...`: resolve, capture (pin), and
// compose skill dependencies (OCI + git) declared in Epos.yaml (SPEC §4.1, §9).
func newDependencyCmd(g *globals) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "dependency",
		Aliases: []string{"dep", "dependencies"},
		Short:   "Resolve, pin, and compose skill dependencies (OCI + git)",
	}
	lockCmd := &cobra.Command{
		Use:   "lock PATH",
		Short: "Resolve pulled-layer pins and write Epos.lock (parity with `epos lock`)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			a := g.newApp()
			pins, err := a.Lock(ctx(), args[0])
			if err != nil {
				return err
			}
			fmt.Fprintf(a.Opts.Out, "wrote Epos.lock with %d pinned layer(s)\n", len(pins))
			return nil
		},
	}
	listCmd := &cobra.Command{
		Use:   "list PATH",
		Short: "List the resolved dependency/overlay layer pins",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			a := g.newApp()
			pins, err := a.ResolvePins(ctx(), args[0])
			if err != nil {
				return err
			}
			for _, p := range pins {
				id := p.Digest
				if id == "" {
					id = p.Commit + ":" + p.TreeSha
				}
				fmt.Fprintf(a.Opts.Out, "%s\t%s\t%s\t%s\n", p.Name, p.Kind, p.SourceType, id)
			}
			return nil
		},
	}
	verifyCmd := &cobra.Command{
		Use:   "verify PATH",
		Short: "Verify resolved pulled-layer pins against Epos.lock (hard error on mismatch)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return g.newApp().VerifyLock(ctx(), args[0])
		},
	}
	cmd.AddCommand(lockCmd, listCmd, verifyCmd)
	return cmd
}

func newProxyCmd(g *globals) *cobra.Command {
	var upstream, listen, metricsListen string
	var perSkill bool
	cmd := &cobra.Command{
		Use:   "proxy",
		Short: "Run the transparent pass-through registry proxy",
		RunE: func(cmd *cobra.Command, args []string) error {
			counter := stats.New()
			p, err := proxy.New(upstream, counter)
			if err != nil {
				return err
			}
			// Expose Prometheus metrics: on a separate listener if configured,
			// else under /metrics on the proxy listener (SPEC §10.1).
			if metricsListen != "" && metricsListen != listen {
				go func() {
					mux := http.NewServeMux()
					mux.Handle("/metrics", stats.MetricsHandler(counter, perSkill))
					_ = http.ListenAndServe(metricsListen, mux) //nolint:gosec
				}()
				fmt.Fprintf(os.Stderr, "epos proxy: %s → %s (metrics %s)\n", listen, upstream, metricsListen)
				return http.ListenAndServe(listen, p) //nolint:gosec // operator-provided listen addr
			}
			mux := http.NewServeMux()
			mux.Handle("/metrics", stats.MetricsHandler(counter, perSkill))
			mux.Handle("/", p)
			fmt.Fprintf(os.Stderr, "epos proxy: %s → %s (metrics on /metrics)\n", listen, upstream)
			return http.ListenAndServe(listen, mux) //nolint:gosec // operator-provided listen addr
		},
	}
	cmd.Flags().StringVar(&upstream, "upstream", "", "upstream registry URL")
	cmd.Flags().StringVar(&listen, "listen", ":8080", "listen address")
	cmd.Flags().StringVar(&metricsListen, "metrics-listen", "", "separate Prometheus /metrics listen address (default: /metrics on --listen)")
	cmd.Flags().BoolVar(&perSkill, "per-skill-metrics", false, "export per-skill pull series (small catalogs only, SPEC §10.1)")
	_ = cmd.MarkFlagRequired("upstream")
	return cmd
}

func newServeCmd(g *globals) *cobra.Command {
	var listen, registriesFile, configFile string
	var refresh time.Duration
	var perSkill bool
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Run the federated frontend",
		RunE: func(cmd *cobra.Command, args []string) error {
			client := &oci.Client{PlainHTTP: g.plainHTTP}
			// Load epos.yaml (validated) for the stats per-skill export setting
			// (SPEC §8.3.1, §10.1). A bad backend type is a hard error, not a
			// silent no-op.
			if configFile != "" {
				cfg, err := config.LoadEposConfig(configFile)
				if err != nil {
					return err
				}
				if cfg.Stats.Prometheus.PerSkill {
					perSkill = true
				}
			}
			var regs []config.Registry
			if registriesFile != "" {
				rc, err := config.LoadRegistries(registriesFile)
				if err != nil {
					return err
				}
				regs = rc.Registries
				// Honor the configured re-probe/refresh interval unless overridden
				// by the flag (SPEC §8.1.1, §12.2).
				if refresh == 0 && rc.DiscoveryRefreshInterval != "" {
					if d, err := time.ParseDuration(rc.DiscoveryRefreshInterval); err == nil {
						refresh = d
					}
				}
			}
			counter := stats.New()
			feed := &frontend.Feed{Registries: regs, Client: client, Stats: counter}
			cat, err := feed.Gather(ctx())
			if err != nil {
				return err
			}
			srv := frontend.NewServer(cat)
			// Background refresh: periodically re-gather the federated index and
			// re-probe discovery capability, swapping the served catalog (§8.1.1,
			// §12.2). Disabled when refresh <= 0.
			if refresh > 0 {
				go func() {
					t := time.NewTicker(refresh)
					defer t.Stop()
					for range t.C {
						if updated, err := feed.Gather(ctx()); err == nil {
							srv.SetCatalog(updated)
						}
					}
				}()
			}
			mux := http.NewServeMux()
			mux.Handle("/metrics", stats.MetricsHandler(counter, perSkill))
			mux.Handle("/", srv.Handler())
			fmt.Fprintf(os.Stderr, "epos serve: %s (refresh %s)\n", listen, refresh)
			return http.ListenAndServe(listen, mux) //nolint:gosec
		},
	}
	cmd.Flags().StringVar(&listen, "listen", ":8080", "listen address")
	cmd.Flags().StringVar(&registriesFile, "registries", "", "registries.yaml path")
	cmd.Flags().StringVar(&configFile, "config", "", "epos.yaml path (stats/backend settings)")
	cmd.Flags().DurationVar(&refresh, "refresh", 0, "federated-index refresh interval (default: registries.yaml discoveryRefreshInterval)")
	cmd.Flags().BoolVar(&perSkill, "per-skill-metrics", false, "export per-skill pull series on /metrics")
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
			// Drive the DetectDiscovery use case through the CatalogProbe port.
			probe := reggw.NewCatalogProbeImpl(client)
			probe.Warn = a.Opts.Err
			detect := regusecase.NewDetectDiscoveryInteractor(probe)
			for _, reg := range regs {
				out, err := detect.DetectDiscovery(regin.DetectDiscoveryInput{Entry: regdomain.RegistryEntry{
					Name:         reg.Name,
					URL:          reg.URL,
					Discovery:    regdomain.DiscoveryMode{Value: reg.Discovery},
					Repositories: reg.Repositories,
					Namespaces:   reg.Namespaces,
				}})
				if err != nil {
					continue
				}
				for _, repo := range out.Result.Repos {
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
		Short: "Resolve pulled-layer pins and write Epos.lock without materializing",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			a := g.newApp()
			pins, err := a.Lock(ctx(), args[0])
			if err != nil {
				return err
			}
			fmt.Fprintf(a.Opts.Out, "wrote Epos.lock with %d pinned layer(s)\n", len(pins))
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
