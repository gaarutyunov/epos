// Package cli builds the epos cobra command tree, wiring global flags into the
// application service (SPEC §4).
package cli

import (
	"context"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/gaarutyunov/epos/internal/app"
	"github.com/gaarutyunov/epos/internal/buildinfo"
)

// globals holds persistent flags shared across commands.
type globals struct {
	registry   string
	plainHTTP  bool
	username   string
	password   string
	workdir    string
	kubeconfig string

	out io.Writer
	err io.Writer
}

// NewRootCmd builds the root `epos` command.
func NewRootCmd() *cobra.Command {
	g := &globals{}
	root := &cobra.Command{
		Use:     "epos",
		Version: buildinfo.Version,
		Short:   "Helm for Agent Skills — package, distribute, compose, and install Skills",
		Long: "Epos packages, distributes, and installs AI-agent Skills as OCI artifacts.\n\n" +
			"Lifecycle verbs are reinterpreted for Skills: with --target=files (default) " +
			"install/upgrade/rollback/status/history concern materialized files and lockfile " +
			"revisions, not Kubernetes releases. With --target=configmap they write real " +
			"cluster objects with self-contained in-cluster revision records.",
		SilenceUsage:  true,
		SilenceErrors: true,
		PersistentPreRun: func(cmd *cobra.Command, args []string) {
			g.out = cmd.OutOrStdout()
			g.err = cmd.ErrOrStderr()
		},
	}

	root.SetVersionTemplate(
		"epos {{.Version}} (commit " + buildinfo.Commit + ", built " + buildinfo.Date + ")\n")

	pf := root.PersistentFlags()
	pf.StringVar(&g.registry, "registry", envOr("EPOS_DEFAULT_REGISTRY", ""), "default registry for bare skill names")
	pf.BoolVar(&g.plainHTTP, "plain-http", envBool("EPOS_PLAIN_HTTP"), "use http:// for registries (local/test)")
	pf.StringVar(&g.username, "username", os.Getenv("EPOS_USERNAME"), "client registry username (relayed, never stored)")
	pf.StringVar(&g.password, "password", os.Getenv("EPOS_PASSWORD"), "client registry password (relayed, never stored)")
	pf.StringVarP(&g.workdir, "workdir", "C", "", "project working directory")
	pf.StringVar(&g.kubeconfig, "kubeconfig", os.Getenv("KUBECONFIG"), "kubeconfig path (configmap target)")

	root.AddCommand(
		newCreateCmd(g),
		newPackageCmd(g),
		newLintCmd(g),
		newPushCmd(g),
		newPullCmd(g),
		newShowCmd(g),
		newSearchCmd(g),
		newTemplateCmd(g),
		newInstallCmd(g),
		newUpgradeCmd(g),
		newRollbackCmd(g),
		newUninstallCmd(g),
		newStatusCmd(g),
		newHistoryCmd(g),
		newOverlayCmd(g),
		newComposeCmd(g),
		newProxyCmd(g),
		newServeCmd(g),
		newRegistryCmd(g),
		newLockCmd(g),
	)
	return root
}

// newApp builds the application service from global flags.
func (g *globals) newApp() *app.App {
	wd := g.workdir
	if wd == "" {
		wd, _ = os.Getwd()
	}
	return app.New(app.Options{
		DefaultRegistry: g.registry,
		PlainHTTP:       g.plainHTTP,
		Username:        g.username,
		Password:        g.password,
		WorkDir:         wd,
		Kubeconfig:      g.kubeconfig,
		Out:             g.out,
		Err:             g.err,
	})
}

func ctx() context.Context { return context.Background() }

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envBool(key string) bool {
	v := strings.ToLower(os.Getenv(key))
	return v == "1" || v == "true" || v == "yes"
}
