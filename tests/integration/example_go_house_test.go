//go:build integration

package integration

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"oras.land/oras-go/v2/content/oci"

	"github.com/gaarutyunov/epos/internal/artifact"
	"github.com/gaarutyunov/epos/internal/install"
	"github.com/gaarutyunov/epos/internal/skillfile"
	"github.com/gaarutyunov/epos/internal/store"
)

// exampleDir is the one place the go-house recipe lives. The quick start reads
// the same three files at build time, so there is no second copy to keep in
// step with this one.
const exampleDir = "../../examples/go-house"

const exampleTag = "go-house:0.1.0"

// TestExampleGoHouse builds examples/go-house with the real builder and
// installs the result under both profiles.
//
// Behind the integration tag because every FROM is a git clone: `go test ./...`
// stays offline. The point of executing it at all is that the quick start
// quotes this recipe — a tutorial nobody runs is a tutorial that describes a
// build which stopped working.
func TestExampleGoHouse(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	s := store.Under(root)

	files := buildExample(ctx, t, s)

	service := installExample(ctx, t, s, "values-service.yaml")
	library := installExample(ctx, t, s, "values-library.yaml")

	t.Run("the sources are pinned", func(t *testing.T) {
		src, err := os.ReadFile(filepath.Join(exampleDir, "Skillfile"))
		require.NoError(t, err)
		assert.NotContains(t, string(src), "#main:",
			"a moving branch is not a pin; name a tag or a commit")
	})

	t.Run("five sources, one artifact", func(t *testing.T) {
		// Every stage has to contribute something the artifact still carries,
		// or it is a clone the build pays for and throws away.
		assert.Contains(t, files, "SKILL.md")
		for _, p := range []string{
			"references/idiomatic-go.md",
			"references/cli.md",
			"references/generics.md",
			"references/testcontainers/postgres_container_test.go",
			"references/scaffold.md",
		} {
			assert.Contains(t, files, p)
		}
	})

	t.Run("the full profile turns the features on", func(t *testing.T) {
		skill := service["SKILL.md"]
		assert.Contains(t, skill, "oapi-codegen")
		assert.Contains(t, skill, "goforj/wire")
		assert.Contains(t, skill, "## Telemetry")
		assert.Contains(t, skill, "testcontainers-go")
		assert.Contains(t, service["references/testcontainers/postgres_container_test.go"],
			"postgres.Run")
	})

	t.Run("the lean profile turns them off", func(t *testing.T) {
		// Below the frontmatter: the description names the five skills the
		// artifact was derived from whichever way the switches are set, which
		// is provenance and not guidance.
		skill := prose(library["SKILL.md"])
		assert.NotContains(t, skill, "oapi-codegen")
		assert.NotContains(t, skill, "goforj/wire")
		assert.NotContains(t, skill, "## Telemetry")
		assert.NotContains(t, skill, "testcontainers-go")
		// epos renders content, it does not add or drop files, so the copied
		// example is emptied rather than removed.
		assert.Empty(t,
			strings.TrimSpace(library["references/testcontainers/postgres_container_test.go"]))
	})

	t.Run("the string parameter is rendered", func(t *testing.T) {
		assert.Contains(t, service["SKILL.md"], "github.com/gaarutyunov/report-service")
		assert.Contains(t, library["SKILL.md"], "github.com/gaarutyunov/parse")
	})

	t.Run("a value nobody supplied fails the install", func(t *testing.T) {
		empty := filepath.Join(t.TempDir(), "values.yaml")
		require.NoError(t, os.WriteFile(empty, []byte("{}\n"), 0o600))

		_, err := install.Install(ctx, s, install.Options{
			Dir:        t.TempDir(),
			Ref:        exampleTag,
			ValueFiles: []string{empty},
		})
		assert.Error(t, err, "the installer must refuse a skill with a hole in it")
	})

	// The two drops the owner named. An upstream bump that reintroduces either
	// has to fail here rather than ship.
	t.Run("the interface assertion is gone", func(t *testing.T) {
		for path, body := range service {
			for _, line := range strings.Split(body, "\n") {
				if !strings.Contains(line, "var _ ") {
					continue
				}
				assert.True(t, prohibition(line),
					"%s teaches the pattern rather than forbidding it: %s", path, line)
			}
		}
		assert.NotContains(t, service["references/interfaces.md"],
			"## Interface Satisfaction Verification")
	})

	t.Run("the prohibition survives the derivation", func(t *testing.T) {
		assert.Contains(t, service["SKILL.md"],
			"**Never** write `var _ Interface = (*Impl)(nil)`")
	})

	t.Run("the rejected configuration library is gone", func(t *testing.T) {
		for path, body := range service {
			assert.NotContains(t, body, "spf13/viper", path)
			assert.NotContains(t, body, "viper.", path)
			for _, line := range strings.Split(body, "\n") {
				if !strings.Contains(line, "Viper") {
					continue
				}
				assert.True(t, prohibition(line),
					"%s recommends it rather than forbidding it: %s", path, line)
			}
		}
	})

	t.Run("the layered project structure is gone", func(t *testing.T) {
		assert.NotContains(t, files, "references/project-structure.md")
		for path, body := range service {
			assert.NotContains(t, body, "envconfig", path)
			assert.NotContains(t, body, "golang/mock", path)
		}
	})

	t.Run("the generics reference is kept, minus two shapes", func(t *testing.T) {
		generics := service["references/generics.md"]
		require.NotEmpty(t, generics)
		assert.Contains(t, generics, "## Generic Data Structures")
		assert.Contains(t, generics, "func (p Pair[T, U]) Swap()")
		assert.NotContains(t, generics, "## Generic Interfaces")
		assert.NotContains(t, generics, "Result[T]")
		assert.NotContains(t, generics, "constraints.Ordered")
		assert.Contains(t, generics, "cmp.Ordered")
	})

	t.Run("the static worker pool is gone, the rest of concurrency stays",
		func(t *testing.T) {
			concurrency := service["references/concurrency.md"]
			assert.NotContains(t, concurrency, "WorkerPool")
			assert.Contains(t, concurrency, "## Rate Limiting and Backpressure")
			assert.Contains(t, concurrency, "## Pipeline Pattern")
		})

	t.Run("the rest of the CLI skill survives", func(t *testing.T) {
		cli := service["references/cli.md"]
		for _, kept := range []string{
			"### The Command-First Architecture",
			"## Cobra Best Practices",
			"### 1. Use `RunE` for Native Error Handling",
			"### 3. Context-Aware Commands",
			"## Testing CLI Commands",
		} {
			assert.Contains(t, cli, kept)
		}
		assert.NotContains(t, cli, "## Viper Configuration Patterns")
		assert.Contains(t, cli, "## Configuration: koanf")
		assert.Contains(t, cli, `k := koanf.New(".")`)
	})
}

// prose is a document without its frontmatter.
func prose(doc string) string {
	rest, ok := strings.CutPrefix(doc, "---\n")
	if !ok {
		return doc
	}
	_, body, ok := strings.Cut(rest, "\n---\n")
	if !ok {
		return doc
	}
	return body
}

// prohibition reports whether a line names a pattern in order to forbid it.
//
// The derived skill is supposed to *carry* the house standard's ban on the
// interface assertion and on the rejected configuration library, so a blanket
// "the string does not occur" assertion would fail on the two lines that are
// the point.
func prohibition(line string) bool {
	for _, marker := range []string{
		"Never", "never", "remove it", "should be koanf",
		"preferred over", "equivalent of",
	} {
		if strings.Contains(line, marker) {
			return true
		}
	}
	return false
}

// buildExample runs the real builder and writes the artifact into s.
func buildExample(ctx context.Context, t *testing.T, s *store.Store) map[string]string {
	t.Helper()

	src, err := os.ReadFile(filepath.Join(exampleDir, "Skillfile"))
	require.NoError(t, err)

	sf, err := skillfile.Parse(src)
	require.NoError(t, err)

	tree, report, err := skillfile.Build(sf, exampleDir, nil)
	require.NoError(t, err)

	// A REPLACE that matches nothing is a warning to the CLI and a failure
	// here: an edit that has silently stopped doing anything is exactly how
	// this example rots against a base it does not own.
	assert.Empty(t, report.NoOpReplaces)
	assert.Empty(t, report.MissingUnsets)

	files := tree.Files()
	err = s.Push(ctx, exampleTag,
		func(ctx context.Context, st *oci.Store) (ocispec.Descriptor, error) {
			built, err := artifact.BuildFiles(ctx, st, files, nil)
			if err != nil {
				return ocispec.Descriptor{}, err
			}
			return built.Manifest, nil
		})
	require.NoError(t, err)

	out := make(map[string]string, len(files))
	for path, body := range files {
		out[path] = string(body)
	}
	return out
}

// installExample installs the built artifact under one profile and returns what
// landed on disk.
//
// Both calls read the same artifact out of the same store: switching profiles
// is an install, never a rebuild, which is the claim the quick start makes.
func installExample(ctx context.Context, t *testing.T, s *store.Store,
	profile string) map[string]string {
	t.Helper()

	dir := t.TempDir()
	values, err := filepath.Abs(filepath.Join(exampleDir, profile))
	require.NoError(t, err)

	res, err := install.Install(ctx, s, install.Options{
		Dir:        dir,
		Ref:        exampleTag,
		ValueFiles: []string{values},
	})
	require.NoError(t, err)
	require.Equal(t, "go-house", res.Name)

	root := filepath.Join(dir, ".claude", "skills", res.Name)
	out := map[string]string{}
	require.NoError(t, filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		body, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		out[filepath.ToSlash(rel)] = string(body)
		return nil
	}))
	return out
}
