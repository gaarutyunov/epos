package catalog

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gaarutyunov/epos/internal/artifact"
)

// demoDir is the checked-in demo, relative to this package.
const demoDir = "../../demo"

// The demo's three files have to agree with each other, and nothing else
// checks that they do.
//
// The deploy is the only thing that would otherwise notice a typo here, and it
// notices by publishing a broken page — the export resolves references against
// a registry the job just filled from these same paths, so a name that does not
// line up produces a site with a skill missing and no failure anywhere.
func TestTheDemoRefsResolveToCheckedInSkills(t *testing.T) {
	refs, err := ReadRefsFile(filepath.Join(demoDir, "refs.txt"))
	require.NoError(t, err)
	assert.GreaterOrEqual(t, len(refs), 2,
		"one row ranks nothing and gives the filter nothing to filter")

	parsed, err := resolveRefs(refs, "")
	require.NoError(t, err)

	for _, ref := range parsed {
		name := ref.repository[strings.LastIndex(ref.repository, "/")+1:]
		dir := filepath.Join(demoDir, "skills", name)

		source, err := os.ReadFile(filepath.Join(dir, artifact.SkillFile))
		require.NoError(t, err, "%s names %s, which has no skill directory", ref.repository, name)

		// The publish step derives the skill's name from the repository and
		// packs demo/skills/<name>; the frontmatter has to agree or `epos pack`
		// writes an artifact under a different name than the refs file expects.
		cfg, err := artifact.ParseFrontmatter(source)
		require.NoError(t, err)
		assert.Equal(t, name, cfg.Name(),
			"the frontmatter name and the directory name must match")
		assert.NotEmpty(t, cfg.Description(),
			"a skill with no description renders a blank row")
	}
}

// And the counts document has to name repositories that are actually in the
// catalog, or a row lands on a page with nothing to attach it to.
func TestTheDemoCountsMatchTheDemoRefs(t *testing.T) {
	refs, err := ReadRefsFile(filepath.Join(demoDir, "refs.txt"))
	require.NoError(t, err)
	parsed, err := resolveRefs(refs, "")
	require.NoError(t, err)

	repositories := make([]string, 0, len(parsed))
	for _, ref := range parsed {
		repositories = append(repositories, ref.repository)
	}

	// Unscoped first: this is what the file actually says.
	all, err := NewFileStats(filepath.Join(demoDir, "counts.json"), nil).Pulls(t.Context())
	require.NoError(t, err)

	// Then scoped to the catalog, which is how the export reads it. If the two
	// disagree the file names a repository the demo does not publish.
	scoped, err := NewFileStats(filepath.Join(demoDir, "counts.json"), repositories).
		Pulls(t.Context())
	require.NoError(t, err)
	assert.Equal(t, all.Rows, scoped.Rows,
		"every counted repository is one the demo publishes")

	assert.NotEmpty(t, all.Note,
		"12.5: the demo's numbers are not measured traffic, and the page has to say so")
	assert.False(t, all.CapturedAt.IsZero(), "and when they were captured")
}
