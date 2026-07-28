package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gaarutyunov/epos/internal/install"
	"github.com/gaarutyunov/epos/internal/store"
)

// parameterisedSkill is a two-stage build whose files are templates: the
// artifact carries them verbatim and the install renders them (SPEC.md 10.1).
//
// The templated frontmatter value is quoted because the config blob is derived
// from the frontmatter at pack time (2.1), and an unquoted `{{` opens a YAML
// flow mapping.
func parameterisedSkill() map[string]string {
	return map[string]string{
		"Skillfile": "FROM ./shared AS shared\nFROM ./base\n" +
			"COPY --from=shared reference.md references/shared.md\n",
		"base/SKILL.md": "---\nname: reviewer\nversion: 2.0.0\n" +
			"description: reviews code\nmodel: '{{ .Values.model }}'\n---\n\n" +
			"# {{ .Values.title }}\n",
		"shared/reference.md": "# {{ .Values.title }}\n",
	}
}

// installInto drives the command the way `epos install` does, with a store and
// a worktree the test owns rather than whatever the environment points at.
func installInto(t *testing.T, s *store.Store, opts install.Options) (string, string) {
	t.Helper()
	var out, info bytes.Buffer
	require.NoError(t, runInstall(context.Background(), &out, &info, s, opts))
	return out.String(), info.String()
}

func valuesIn(t *testing.T, dir, body string) string {
	t.Helper()
	path := filepath.Join(dir, "values.yaml")
	require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
	return path
}

// stdout carries the one machine-readable line — the tag and the digest, the
// same shape pack and pull print — and the paths go to stderr, so a script
// reading the pin does not have to skip over them.
func TestInstallPrintsThePinOnStdoutAndThePathsOnStderr(t *testing.T) {
	s := store.Under(t.TempDir())
	buildInto(t, s, buildOptions{contextDir: writeContext(t, parameterisedSkill(), false)})

	dir := t.TempDir()
	stdout, stderr := installInto(t, s, install.Options{
		Dir: dir,
		Ref: "reviewer:2.0.0",
		ValueFiles: []string{valuesIn(t, dir,
			"title: The reviewer\nmodel: opus\nshared:\n  title: The shared stage\n")},
	})

	fields := strings.Fields(stdout)
	require.Len(t, fields, 2)
	assert.Equal(t, "reviewer:2.0.0", fields[0])
	// The printed pin is the digest the store resolved, not something derived
	// from the rendered worktree: what is pinned is the artifact.
	desc, err := s.Resolve(context.Background(), "reviewer:2.0.0")
	require.NoError(t, err)
	assert.Equal(t, desc.Digest.String(), fields[1])

	assert.Contains(t, stderr, "installed into .claude/skills/reviewer")
	assert.NotContains(t, stdout, "installed into")
}

// End to end through the command: a build's stage provenance reaches the
// install, so two stages using the same key render differently (10.3).
func TestInstalledSkillIsRenderedPerStage(t *testing.T) {
	s := store.Under(t.TempDir())
	buildInto(t, s, buildOptions{contextDir: writeContext(t, parameterisedSkill(), false)})

	dir := t.TempDir()
	installInto(t, s, install.Options{
		Dir: dir,
		Ref: "reviewer:2.0.0",
		ValueFiles: []string{valuesIn(t, dir,
			"title: The final stage\nmodel: opus\nshared:\n  title: The shared stage\n")},
	})

	root := filepath.Join(dir, ".claude", "skills", "reviewer")
	skill, err := os.ReadFile(filepath.Join(root, "SKILL.md"))
	require.NoError(t, err)
	assert.Contains(t, string(skill), "# The final stage")
	assert.Contains(t, string(skill), "model: 'opus'")

	shared, err := os.ReadFile(filepath.Join(root, "references", "shared.md"))
	require.NoError(t, err)
	assert.Contains(t, string(shared), "# The shared stage")
}
