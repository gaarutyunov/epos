package catalog

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/gaarutyunov/epos/internal/registry"
)

func exportFixture(t *testing.T, catalog Catalog, out string) error {
	t.Helper()
	renderer, err := NewRenderer(catalog, "/")
	require.NoError(t, err)

	ctrl := gomock.NewController(t)
	client := NewMockClient(ctrl)
	client.EXPECT().FetchContent(gomock.Any(), gomock.Any(), gomock.Any()).
		Return(registry.Content{Files: map[string][]byte{
			"SKILL.md": []byte("---\nname: x\n---\n\n# Hello\n"),
		}}, nil).AnyTimes()

	return Export(t.Context(), renderer, client, nil, out)
}

func TestExportWritesTheWholeSite(t *testing.T) {
	out := filepath.Join(t.TempDir(), "site")
	require.NoError(t, exportFixture(t, fixture(), out))

	for _, name := range []string{
		"index.html",
		"catalog/index.html",
		"tools/index.html",
		"skills/demo/agent-skills/pdf/index.html",
		"skills/demo/agent-skills/reviewer/index.html",
		"assets/app.css",
		"assets/app.js",
		"assets/ga-ui-kit.css",
		"assets/ga-ui-kit.min.js",
	} {
		_, err := os.Stat(filepath.Join(out, filepath.FromSlash(name)))
		assert.NoError(t, err, "%s was not written", name)
	}
}

// D12: route paths come from registry-supplied repository names, so containment
// is a security check and not a formality.
func TestAHostileRepositoryNameCannotWriteOutsideOut(t *testing.T) {
	root := t.TempDir()
	out := filepath.Join(root, "site")

	hostile := Catalog{
		Registry: "registry.example.com",
		Skills: []Skill{{
			Repository: "../../escaped",
			Name:       "escaped",
			Version:    "1.0.0",
		}},
	}

	err := exportFixture(t, hostile, out)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "resolves outside")

	_, statErr := os.Stat(filepath.Join(root, "escaped"))
	assert.True(t, os.IsNotExist(statErr), "nothing was written outside --out")
}

// 8.3a: an export prunes pages it did not write, so it must refuse a directory
// that is not recognisably a previous export's output. Never recursively delete
// a directory a human named.
func TestExportRefusesADirectoryItDidNotWrite(t *testing.T) {
	out := t.TempDir()
	precious := filepath.Join(out, "thesis.txt")
	require.NoError(t, os.WriteFile(precious, []byte("years of work"), 0o600))

	err := exportFixture(t, fixture(), out)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "is not a previous export")

	body, readErr := os.ReadFile(precious)
	require.NoError(t, readErr)
	assert.Equal(t, "years of work", string(body))
}

// The other half: a second export into its own output prunes what the first one
// wrote and this one did not, so a skill removed from the registry disappears
// from the site.
func TestASecondExportPrunesPagesItDidNotWrite(t *testing.T) {
	out := filepath.Join(t.TempDir(), "site")
	require.NoError(t, exportFixture(t, fixture(), out))

	shrunk := fixture()
	shrunk.Skills = shrunk.Skills[:1]
	require.NoError(t, exportFixture(t, shrunk, out))

	_, err := os.Stat(filepath.Join(out, "skills", "demo", "agent-skills", "reviewer", "index.html"))
	assert.True(t, os.IsNotExist(err), "the removed skill's page was pruned")
	_, err = os.Stat(filepath.Join(out, "skills", "demo", "agent-skills", "pdf", "index.html"))
	assert.NoError(t, err)
}

// 8.3c: pointed at the directory holding its own inputs, an export deletes
// them. The second run then fails for a reason with nothing to do with the
// first.
func TestExportRefusesAnOutThatHoldsItsOwnInputs(t *testing.T) {
	dir := t.TempDir()
	refs := filepath.Join(dir, "refs.txt")
	require.NoError(t, os.WriteFile(refs, []byte("demo/pdf:1.0.0\n"), 0o600))

	err := GuardInputs(dir, refs, "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "would delete its own input")

	require.NoError(t, GuardInputs(filepath.Join(dir, "site"), refs, ""))
}

// 8.3b: a statistics credential never lands in the exported output.
func TestNoCredentialReachesTheExportedOutput(t *testing.T) {
	out := filepath.Join(t.TempDir(), "site")
	require.NoError(t, exportFixture(t, fixture(), out))

	require.NoError(t, filepath.Walk(out, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		body, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		assert.NotContains(t, string(body), "stats-dsn")
		assert.NotContains(t, string(body), "clickhouse://")
		return nil
	}))
}
