package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/spf13/cobra"
	"oras.land/oras-go/v2/content/oci"

	"github.com/gaarutyunov/epos/internal/artifact"
	"github.com/gaarutyunov/epos/internal/store"
)

func newPackCommand() *cobra.Command {
	var tag string

	cmd := &cobra.Command{
		Use:   "pack <dir>",
		Short: "Pack a skill directory into the local store",
		Long: "pack derives the config blob from SKILL.md's frontmatter, builds the\n" +
			"deterministic content layer, and writes the artifact into the local\n" +
			"store. Packing the same directory twice produces the same digest.",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runPack(cmd.Context(), cmd.OutOrStdout(), args[0], tag)
		},
	}
	cmd.Flags().StringVarP(&tag, "tag", "t", "",
		"tag as <name>:<version> (default: the name and version from SKILL.md)")
	return cmd
}

func runPack(ctx context.Context, out io.Writer, dir, tagFlag string) error {
	// The tag is resolved before the push because Push tags under the same
	// lock that writes the blobs, so it has to know the tag going in. Reading
	// the frontmatter twice costs nothing next to packing the directory.
	src, err := os.ReadFile(filepath.Join(dir, artifact.SkillFile))
	if err != nil {
		return fmt.Errorf("read %s: %w", artifact.SkillFile, err)
	}
	cfg, err := artifact.ParseFrontmatter(src)
	if err != nil {
		return err
	}
	tag, err := resolveTag(tagFlag, cfg)
	if err != nil {
		return err
	}

	s, err := store.Default()
	if err != nil {
		return err
	}

	var packed artifact.Skill
	err = s.Push(ctx, tag, func(ctx context.Context, st *oci.Store) (ocispec.Descriptor, error) {
		packed, err = artifact.Build(ctx, st, dir)
		if err != nil {
			return ocispec.Descriptor{}, err
		}
		return packed.Manifest, nil
	})
	if err != nil {
		return err
	}

	fmt.Fprintf(out, "%s %s\n", tag, packed.Manifest.Digest)
	return nil
}

// resolveTag picks the tag to write, preferring what the user asked for.
func resolveTag(flag string, cfg artifact.Config) (string, error) {
	if flag != "" {
		name, version, ok := strings.Cut(flag, ":")
		if !ok || name == "" || version == "" {
			return "", fmt.Errorf("tag %q is not <name>:<version>", flag)
		}
		return flag, nil
	}

	version, _ := cfg["version"].(string)
	if version == "" {
		return "", fmt.Errorf("%s frontmatter has no version; pass -t <name>:<version>",
			artifact.SkillFile)
	}
	return cfg.Name() + ":" + version, nil
}
