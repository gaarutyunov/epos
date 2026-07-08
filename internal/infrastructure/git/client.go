// Package git is the shared, domain-free git client (the model's
// Infrastructure.GitClient, SPEC §15.1). It drives the system `git` binary to
// resolve a ref to a commit, capture the git tree object SHA of a subpath, and
// fetch the subtree files — the basis of git-source pin capture (SPEC §9.7).
package git

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Client resolves and fetches git sources.
type Client struct {
	// Auth, when set, is injected as basic-auth into the remote URL (Gitea
	// private-repo scenarios). Never persisted.
	Username string
	Password string
}

// Resolved is the pin-capture result for a git source (SPEC §9.7).
type Resolved struct {
	Source  string
	Ref     string
	Commit  string // full commit SHA
	TreeSha string // git tree object SHA of subpath@commit
	Subpath string
	Files   map[string][]byte
}

// Resolve clones remoteURL at ref, records the commit and the tree object SHA of
// subpath, and returns the subtree's files. Pin: {source, ref, commit, treeSha,
// subpath}; verification re-resolves and compares — any mismatch is a hard error.
func (c *Client) Resolve(remoteURL, ref, subpath string) (*Resolved, error) {
	tmp, err := os.MkdirTemp("", "epos-git-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmp)

	url := c.injectAuth(remoteURL)
	// Full clone (not shallow) so an arbitrary tag/branch/sha ref resolves and
	// tree addressing works against local/test git servers uniformly.
	if out, err := run(tmp, "git", "clone", "--quiet", url, "."); err != nil {
		return nil, fmt.Errorf("git clone: %w: %s", err, out)
	}
	if out, err := run(tmp, "git", "checkout", "--quiet", ref); err != nil {
		return nil, fmt.Errorf("git checkout %q: %w: %s", ref, err, out)
	}
	commit, err := run(tmp, "git", "rev-parse", "HEAD")
	if err != nil {
		return nil, fmt.Errorf("git rev-parse HEAD: %w", err)
	}
	commit = strings.TrimSpace(commit)

	sub := subpath
	if sub == "" {
		sub = "."
	}
	treeExpr := "HEAD^{tree}"
	if sub != "." {
		treeExpr = "HEAD:" + sub
	}
	treeSha, err := run(tmp, "git", "rev-parse", treeExpr)
	if err != nil {
		return nil, fmt.Errorf("git rev-parse %q: %w", treeExpr, err)
	}
	treeSha = strings.TrimSpace(treeSha)

	root := tmp
	if sub != "." {
		root = filepath.Join(tmp, filepath.FromSlash(sub))
	}
	files, err := readTree(root)
	if err != nil {
		return nil, err
	}

	return &Resolved{
		Source:  remoteURL,
		Ref:     ref,
		Commit:  commit,
		TreeSha: treeSha,
		Subpath: subpath,
		Files:   files,
	}, nil
}

func (c *Client) injectAuth(remoteURL string) string {
	if c.Username == "" || !strings.HasPrefix(remoteURL, "http") {
		return remoteURL
	}
	i := strings.Index(remoteURL, "://")
	if i < 0 {
		return remoteURL
	}
	return remoteURL[:i+3] + c.Username + ":" + c.Password + "@" + remoteURL[i+3:]
}

// readTree reads every regular file under root into a path→bytes map (git
// metadata excluded).
func readTree(root string) (map[string][]byte, error) {
	files := map[string][]byte{}
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			if info.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		files[filepath.ToSlash(rel)] = data
		return nil
	})
	return files, err
}

func run(dir, name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	// Deterministic, non-interactive git.
	cmd.Env = append(os.Environ(),
		"GIT_TERMINAL_PROMPT=0",
		"GIT_CONFIG_NOSYSTEM=1",
	)
	err := cmd.Run()
	return out.String(), err
}
