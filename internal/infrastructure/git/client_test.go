package git

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestResolvePinCapture builds a local git repo with a tagged subtree and
// verifies commit + tree-SHA capture (SPEC §9.7). No network: CI uses a real
// Gitea container (SPEC §15.3); the pin-capture logic is identical.
func TestResolvePinCapture(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	repo := t.TempDir()
	mustGit(t, repo, "init", "-q")
	mustGit(t, repo, "config", "user.email", "t@example.com")
	mustGit(t, repo, "config", "user.name", "t")
	writeFile(t, repo, "skills/shared/SKILL.md", "shared body")
	writeFile(t, repo, "skills/shared/references/x.md", "x")
	mustGit(t, repo, "add", "-A")
	mustGit(t, repo, "commit", "-q", "-m", "init")
	mustGit(t, repo, "tag", "v2.1.0")

	c := &Client{}
	got, err := c.Resolve(repo, "v2.1.0", "skills/shared")
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Commit) != 40 {
		t.Errorf("commit not a full SHA: %q", got.Commit)
	}
	if len(got.TreeSha) != 40 {
		t.Errorf("treeSha not captured: %q", got.TreeSha)
	}
	if string(got.Files["SKILL.md"]) != "shared body" {
		t.Errorf("subtree files wrong: %v", got.Files)
	}
	// Re-resolve is stable (verification compares digests).
	got2, _ := c.Resolve(repo, "v2.1.0", "skills/shared")
	if got.Commit != got2.Commit || got.TreeSha != got2.TreeSha {
		t.Error("pin capture is not stable")
	}
}

func mustGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	if out, err := run(dir, "git", args...); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
}

func writeFile(t *testing.T, root, rel, content string) {
	t.Helper()
	full := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
