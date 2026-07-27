package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/spf13/cobra"
	"oras.land/oras-go/v2/content/oci"

	"github.com/gaarutyunov/epos/internal/artifact"
	"github.com/gaarutyunov/epos/internal/skillfile"
	"github.com/gaarutyunov/epos/internal/store"
)

// defaultSkillfile is what -f defaults to, inside the context directory.
const defaultSkillfile = "Skillfile"

// skillfileDigestAnnotation records which recipe produced an artifact (SPEC.md
// 2.3). Provenance is descriptive, not traversable: the registry cannot be
// asked what derives from a skill, exactly as with Docker, where the recipe
// lives in git and the registry stores only results.
const skillfileDigestAnnotation = "dev.epos.skillfile.digest"

// buildOptions is what `epos build` was asked to do.
type buildOptions struct {
	contextDir string
	skillfile  string
	buildArgs  []string
	tag        string
}

func newBuildCommand() *cobra.Command {
	var opts buildOptions

	cmd := &cobra.Command{
		Use:   "build <context>",
		Short: "Build a skill from a Skillfile into the local store",
		Long: "build evaluates a Skillfile against a context directory and writes\n" +
			"one conformant artifact into the local store. Nothing executes and\n" +
			"nothing is fetched from a registry: with local and git bases the whole\n" +
			"workflow is standalone.\n\n" +
			"The build is a pure function of its bases, the Skillfile and the\n" +
			"context, so the same three inputs always produce the same digest.",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := store.Default()
			if err != nil {
				return err
			}
			opts.contextDir = args[0]
			return runBuild(cmd.Context(), cmd.OutOrStdout(), cmd.ErrOrStderr(), s, opts)
		},
	}
	cmd.Flags().StringVarP(&opts.skillfile, "file", "f", "",
		"Skillfile to build (default: <context>/"+defaultSkillfile+")")
	cmd.Flags().StringArrayVar(&opts.buildArgs, "build-arg", nil,
		"set a build argument, as k=v; repeatable")
	cmd.Flags().StringVarP(&opts.tag, "tag", "t", "",
		"tag as <name>:<version> (default: the name and version of the built SKILL.md)")
	return cmd
}

// runBuild evaluates the Skillfile and writes the result into s.
//
// The store is a parameter rather than looked up here so a test can build into
// a directory it owns — store.Under(t.TempDir()) — with nothing global
// involved. `epos build` is otherwise the one command whose whole output is a
// side effect on the local store, and a test that had to move an environment
// variable to observe it would be testing the environment as much as the build.
func runBuild(ctx context.Context, out, warn io.Writer, s *store.Store, opts buildOptions) error {
	path := opts.skillfile
	if path == "" {
		path = filepath.Join(opts.contextDir, defaultSkillfile)
	}
	src, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read the Skillfile: %w", err)
	}

	sf, err := skillfile.Parse(src)
	if err != nil {
		return err
	}
	args, err := parseBuildArgs(opts.buildArgs)
	if err != nil {
		return err
	}

	tree, report, err := skillfile.Build(sf, opts.contextDir, args)
	if err != nil {
		return err
	}
	writeReport(warn, report)

	files := tree.Files()
	skillMD, ok := files[artifact.SkillFile]
	if !ok {
		return fmt.Errorf("the build produced no %s", artifact.SkillFile)
	}
	cfg, err := artifact.ParseFrontmatter(skillMD)
	if err != nil {
		return err
	}
	tag, err := resolveTag(opts.tag, cfg)
	if err != nil {
		return err
	}

	// Packed through internal/artifact rather than by anything here: `build` and
	// `pack` must agree on what a skill hashes to, and two packing paths would
	// only agree until one of them was touched.
	provenance := provenanceFor(report, src)
	var built artifact.Skill
	err = s.Push(ctx, tag, func(ctx context.Context, st *oci.Store) (ocispec.Descriptor, error) {
		built, err = artifact.BuildFiles(ctx, st, files, provenance)
		if err != nil {
			return ocispec.Descriptor{}, err
		}
		return built.Manifest, nil
	})
	if err != nil {
		return err
	}

	fmt.Fprintf(out, "%s %s\n", tag, built.Manifest.Digest)
	return nil
}

// parseBuildArgs turns repeated --build-arg k=v into the map Build takes.
//
// `--build-arg k=` is a deliberate empty value, not an error: an ARG whose
// default the author wants suppressed has no other way to say so.
func parseBuildArgs(pairs []string) (map[string]string, error) {
	out := map[string]string{}
	for _, pair := range pairs {
		name, value, ok := strings.Cut(pair, "=")
		if !ok || name == "" {
			return nil, fmt.Errorf("build argument %q is not k=v", pair)
		}
		out[name] = value
	}
	return out, nil
}

// writeReport prints what the build has to say, on stderr.
//
// stderr because stdout carries the one machine-readable line — the tag and the
// digest — that a script reads, and a warning in the middle of it would be a
// warning some pipeline parses as a digest.
//
// The pins come first because they are the build's provenance, not a
// complaint: a ref like `main` or `v1.2.0` is mutable (8.3), so the commit and
// tree SHAs printed here are the only record of what this artifact actually
// descended from, and the thing a rebuild is checked against.
//
// The warnings are printed on every build rather than behind a flag. A zero-
// match REPLACE (8.2.2) and an absent-key UNSET (8.2.4) are warnings so that
// idempotent edits stay expressible — but a Skillfile that has silently stopped
// editing its base is exactly the failure those clauses trade an error for, and
// it is only visible if somebody is told.
func writeReport(w io.Writer, r *skillfile.Report) {
	for _, base := range r.GitBases {
		fmt.Fprintf(w, "%s\n  commit %s\n  tree   %s\n", base.Ref, base.Commit, base.Tree)
	}
	for _, warning := range r.NoOpReplaces {
		fmt.Fprintf(w, "warning: %s\n", warning)
	}
	for _, warning := range r.MissingUnsets {
		fmt.Fprintf(w, "warning: %s\n", warning)
	}
}

// provenanceFor renders the annotations 2.3 records on a derived skill.
func provenanceFor(r *skillfile.Report, skillfileSrc []byte) map[string]string {
	out := map[string]string{
		// The Skillfile is the recipe, so its digest is what says two artifacts
		// were built from the same one — which the artifact's own digest cannot,
		// since two different recipes can produce identical bytes.
		skillfileDigestAnnotation: digest.FromBytes(skillfileSrc).String(),
	}
	if r.Base.Ref != "" {
		out[ocispec.AnnotationBaseImageName] = r.Base.Ref
	}
	// A local base has no pin (8.3), and an annotation asserting one would be a
	// claim the build cannot back.
	if r.Base.Digest != "" {
		out[ocispec.AnnotationBaseImageDigest] = r.Base.Digest
	}
	return out
}
