package cli

import (
	"fmt"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/gaarutyunov/epos/internal/app"
)

func newCreateCmd(g *globals) *cobra.Command {
	return &cobra.Command{
		Use:   "create NAME",
		Short: "Scaffold a new Skill package directory",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return g.newApp().Create(args[0])
		},
	}
}

func newPackageCmd(g *globals) *cobra.Command {
	return &cobra.Command{
		Use:   "package PATH",
		Short: "Build the OCI artifact (tar+gzip content layer + config blob) from a package directory",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			_, _, err := g.newApp().Package(ctx(), args[0])
			return err
		},
	}
}

func newLintCmd(g *globals) *cobra.Command {
	return &cobra.Command{
		Use:   "lint PATH",
		Short: "Validate metadata, template, and dangling references",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			a := g.newApp()
			ok, msgs, err := a.Lint(args[0])
			if err != nil {
				return err
			}
			for _, m := range msgs {
				fmt.Fprintln(a.Opts.Out, "-", m)
			}
			if !ok {
				return fmt.Errorf("validation failed (%d issue(s))", len(msgs))
			}
			fmt.Fprintln(a.Opts.Out, "OK: package is valid")
			return nil
		},
	}
}

func newPushCmd(g *globals) *cobra.Command {
	return &cobra.Command{
		Use:   "push PATH REF",
		Short: "Push a packaged skill to an OCI registry",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			_, err := g.newApp().Push(ctx(), args[0], args[1])
			return err
		},
	}
}

func newPullCmd(g *globals) *cobra.Command {
	return &cobra.Command{
		Use:   "pull REF [DIR]",
		Short: "Pull an artifact by tag or digest",
		Args:  cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir := ""
			if len(args) == 2 {
				dir = args[1]
			}
			a := g.newApp()
			man, err := a.Pull(ctx(), args[0], dir)
			if err != nil {
				return err
			}
			fmt.Fprintf(a.Opts.Out, "Pulled %s (%s)\n", args[0], man.Digest)
			return nil
		},
	}
}

func newShowCmd(g *globals) *cobra.Command {
	return &cobra.Command{
		Use:   "show REF",
		Short: "Show metadata for a skill package",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			a := g.newApp()
			m, err := a.Show(ctx(), args[0])
			if err != nil {
				return err
			}
			fmt.Fprintf(a.Opts.Out, "name: %s\nversion: %s\ndescription: %s\n", m.Name, m.Version, m.Description)
			return nil
		},
	}
}

func newTemplateCmd(g *globals) *cobra.Command {
	var o installFlags
	cmd := &cobra.Command{
		Use:   "template NAME REF",
		Short: "Render SKILL.md + files (or ConfigMap YAML with --target=configmap)",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			a := g.newApp()
			out, err := a.Template(ctx(), args[0], args[1], o.toOpts())
			if err != nil {
				return err
			}
			fmt.Fprint(a.Opts.Out, out)
			return nil
		},
	}
	o.bind(cmd)
	return cmd
}

func newInstallCmd(g *globals) *cobra.Command {
	var o installFlags
	cmd := &cobra.Command{
		Use:   "install NAME REF",
		Short: "Resolve REF to a digest and materialize the Skill bundle",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			_, err := g.newApp().Install(ctx(), args[0], args[1], o.toOpts())
			return err
		},
	}
	o.bind(cmd)
	cmd.Flags().BoolVar(&o.frozen, "frozen", false, "install strictly from the lockfile; error on mismatch")
	cmd.Flags().BoolVar(&o.requireSig, "require-signature", false, "fail if no valid signature is present")
	return cmd
}

func newUpgradeCmd(g *globals) *cobra.Command {
	var o installFlags
	cmd := &cobra.Command{
		Use:   "upgrade NAME REF",
		Short: "Fetch a newer version, re-materialize, and append a new revision",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			_, err := g.newApp().Upgrade(ctx(), args[0], args[1], o.toOpts())
			return err
		},
	}
	o.bind(cmd)
	cmd.Flags().BoolVar(&o.requireSig, "require-signature", false, "fail if no valid signature is present")
	return cmd
}

func newRollbackCmd(g *globals) *cobra.Command {
	var o installFlags
	cmd := &cobra.Command{
		Use:   "rollback NAME REVISION",
		Short: "Restore a previous bundle in full and record it as a new revision",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			rev, err := strconv.Atoi(args[1])
			if err != nil {
				return fmt.Errorf("REVISION must be an integer: %w", err)
			}
			_, err = g.newApp().Rollback(ctx(), args[0], rev, o.toOpts())
			return err
		},
	}
	o.bind(cmd)
	return cmd
}

func newUninstallCmd(g *globals) *cobra.Command {
	var o installFlags
	var keepHistory bool
	cmd := &cobra.Command{
		Use:   "uninstall NAME",
		Short: "Remove a Skill's materialized files and its lockfile entry",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return g.newApp().Uninstall(ctx(), args[0], keepHistory, o.toOpts())
		},
	}
	o.bind(cmd)
	cmd.Flags().BoolVar(&keepHistory, "keep-history", false, "keep the lockfile revision history")
	return cmd
}

func newStatusCmd(g *globals) *cobra.Command {
	var o installFlags
	cmd := &cobra.Command{
		Use:   "status NAME",
		Short: "Report the currently installed version/digest and applied overlays",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			a := g.newApp()
			rev, err := a.Status(ctx(), args[0], o.toOpts())
			if err != nil {
				return err
			}
			fmt.Fprintf(a.Opts.Out, "release: %s\nrevision: %d\nversion: %s\ndigest: %s\n", args[0], rev.Number, rev.Version, rev.Digest)
			return nil
		},
	}
	o.bind(cmd)
	return cmd
}

func newHistoryCmd(g *globals) *cobra.Command {
	var o installFlags
	cmd := &cobra.Command{
		Use:   "history NAME",
		Short: "List retained revisions",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			a := g.newApp()
			revs, err := a.History(ctx(), args[0], o.toOpts())
			if err != nil {
				return err
			}
			for _, r := range revs {
				fmt.Fprintf(a.Opts.Out, "revision %d\tversion %s\tdigest %s\n", r.Number, r.Version, r.Digest)
			}
			return nil
		},
	}
	o.bind(cmd)
	return cmd
}

func newComposeCmd(g *globals) *cobra.Command {
	var strict bool
	var outDir string
	cmd := &cobra.Command{
		Use:   "compose PATH",
		Short: "Resolve a skill's layer stack into one merged skill (deps + overlays)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			a := g.newApp()
			res, err := a.Compose(ctx(), args[0], strict)
			if err != nil {
				return err
			}
			for _, line := range res.Merged.ProvenanceLines() {
				fmt.Fprintln(a.Opts.Out, line)
			}
			if outDir != "" {
				return writeMergedTree(outDir, res.Merged.Files)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&strict, "strict", false, "promote non-matching/failing operations to hard errors")
	cmd.Flags().StringVarP(&outDir, "output", "o", "", "write the merged skill to a directory")
	return cmd
}

// installFlags are the shared flags for install/upgrade/template/rollback/…
type installFlags struct {
	target     string
	namespace  string
	version    string
	mountPath  string
	frozen     bool
	requireSig bool
}

func (o *installFlags) bind(cmd *cobra.Command) {
	cmd.Flags().StringVar(&o.target, "target", app.TargetFiles, "materialization target: files | configmap")
	cmd.Flags().StringVarP(&o.namespace, "namespace", "n", "", "cluster namespace (configmap target)")
	cmd.Flags().StringVar(&o.version, "version", "", "explicit version to resolve")
	cmd.Flags().StringVar(&o.mountPath, "mount-path", "", "mount path for the configmap projection")
}

func (o *installFlags) toOpts() app.InstallOpts {
	return app.InstallOpts{
		Target:           o.target,
		Namespace:        o.namespace,
		Version:          o.version,
		MountPath:        o.mountPath,
		Frozen:           o.frozen,
		RequireSignature: o.requireSig,
	}
}
