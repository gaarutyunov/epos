package main

import (
	"fmt"
	"sort"

	"github.com/spf13/cobra"

	"github.com/gaarutyunov/epos/internal/store"
)

// newStoreCommand carries the local-store subcommands of SPEC.md 9.3.
//
// Collection is manual only, like the Go module cache, pnpm, Cargo and Bazel.
// There is no reference counting, no GC roots, no leases and no worktree
// liveness tracking: those exist to make *automatic* collection safe, and with
// explicit cleanup there is nothing to make safe.
func newStoreCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:          "store",
		Short:        "Inspect and maintain the local artifact store",
		SilenceUsage: true,
		Args:         cobra.NoArgs,
		RunE:         func(cmd *cobra.Command, _ []string) error { return cmd.Help() },
	}
	cmd.AddCommand(newStorePathCommand(), newStoreLsCommand())
	return cmd
}

func newStorePathCommand() *cobra.Command {
	return &cobra.Command{
		Use:          "path",
		Short:        "Print where the local store lives",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			s, err := store.Default()
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), s.Path())
			return nil
		},
	}
}

func newStoreLsCommand() *cobra.Command {
	return &cobra.Command{
		Use:          "ls",
		Short:        "List the skills the local store holds",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			s, err := store.Default()
			if err != nil {
				return err
			}
			tags, err := s.Tags(cmd.Context())
			if err != nil {
				return err
			}
			// Sorted so the output is stable enough to diff between runs.
			sort.Strings(tags)
			for _, tag := range tags {
				fmt.Fprintln(cmd.OutOrStdout(), tag)
			}
			return nil
		},
	}
}
