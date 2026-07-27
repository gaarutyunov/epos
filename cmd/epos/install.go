package main

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"

	"github.com/gaarutyunov/epos/internal/install"
	"github.com/gaarutyunov/epos/internal/store"
)

// installOptions is what `epos install` was asked to do (SPEC.md 10.4).
type installOptions struct {
	valueFiles []string
	sets       []string
}

func newInstallCommand() *cobra.Command {
	var opts installOptions

	cmd := &cobra.Command{
		Use:   "install <ref|name:version>",
		Short: "Install a skill from the local store into this worktree",
		Long: "install renders the skill's templates with the values you supply and\n" +
			"writes the result into .claude/skills, plus any additionalBasePaths\n" +
			"skills.json names. The artifact carries its templates verbatim; this is\n" +
			"where they are rendered, and nothing upstream renders anything.\n\n" +
			"The resolved digest is pinned in skills.lock.json. The store is a cache\n" +
			"and the lock is the truth, so two worktrees can pin two different\n" +
			"versions out of one store at the same time.\n\n" +
			"The skill must already be in the local store: install resolves, it does\n" +
			"not fetch. Run epos pull or epos build first.",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := store.Default()
			if err != nil {
				return err
			}
			dir, err := worktree()
			if err != nil {
				return err
			}
			return runInstall(cmd.Context(), cmd.OutOrStdout(), cmd.ErrOrStderr(), s,
				install.Options{
					Dir:        dir,
					Ref:        args[0],
					ValueFiles: opts.valueFiles,
					Sets:       opts.sets,
				})
		},
	}
	cmd.Flags().StringArrayVarP(&opts.valueFiles, "values", "f", nil,
		"read values from a YAML file; repeatable, later files win")
	cmd.Flags().StringArrayVar(&opts.sets, "set", nil,
		"set one value, as k=v with dots naming nested keys; repeatable")
	return cmd
}

// runInstall installs and reports where the skill went.
//
// stdout carries one machine-readable line — the tag and the digest, the same
// shape pack and pull print — and the paths go to stderr, so a script reading
// the pin does not have to skip over them.
func runInstall(ctx context.Context, out, info io.Writer, s *store.Store,
	opts install.Options) error {
	res, err := install.Install(ctx, s, opts)
	if err != nil {
		return err
	}
	for _, base := range res.BasePaths {
		fmt.Fprintf(info, "installed into %s/%s\n", base, res.Name)
	}
	fmt.Fprintf(out, "%s:%s %s\n", res.Name, res.Version, res.Digest)
	return nil
}

func newUninstallCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "uninstall <name>",
		Short: "Remove an installed skill from this worktree",
		Long: "uninstall removes the skill from every base path it was installed into\n" +
			"and drops it from skills.json and skills.lock.json. The store keeps its\n" +
			"copy; use epos store prune to collect it.",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			dir, err := worktree()
			if err != nil {
				return err
			}
			removed, err := install.Uninstall(dir, args[0])
			if err != nil {
				return err
			}
			for _, p := range removed {
				fmt.Fprintf(cmd.OutOrStdout(), "removed %s\n", p)
			}
			return nil
		},
	}
}

func newLsCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "ls",
		Short: "List the skills this worktree has pinned",
		Long: "ls prints what skills.lock.json pins: the skill, the version and the\n" +
			"digest this worktree is on. It reads the lock and not the filesystem,\n" +
			"because the lock is what the worktree actually installed.\n\n" +
			"For what the local store holds, use epos store ls.",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			dir, err := worktree()
			if err != nil {
				return err
			}
			pinned, err := install.List(dir)
			if err != nil {
				return err
			}
			for _, entry := range pinned {
				fmt.Fprintf(cmd.OutOrStdout(), "%s:%s %s\n", entry.Name, entry.Version, entry.Digest)
			}
			return nil
		},
	}
}

// worktree is the directory the manifests live in: where the command was run.
//
// Not a flag. 10.2 makes skills.lock.json a per-worktree pin, and a worktree
// is where you are — the same convention npm, cargo and go have, where the
// project is the directory rather than something you name every time.
func worktree() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("locate the worktree: %w", err)
	}
	return dir, nil
}
