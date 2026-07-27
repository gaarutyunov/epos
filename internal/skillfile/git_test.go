package skillfile

import (
	"testing"

	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/filemode"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/storage/memory"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseGitRef(t *testing.T) {
	cases := []struct {
		name string
		ref  string
		want GitRef
	}{{
		name: "the form 8.3 documents",
		ref:  "git+https://github.com/o/r#v1.2.0:skills/pdf",
		want: GitRef{URL: "https://github.com/o/r", Rev: "v1.2.0", Subpath: "skills/pdf"},
	}, {
		name: "a ref with no subpath takes the whole repository",
		ref:  "git+https://github.com/o/r#main",
		want: GitRef{URL: "https://github.com/o/r", Rev: "main"},
	}, {
		name: "no fragment at all means the default branch",
		ref:  "git+https://github.com/o/r",
		want: GitRef{URL: "https://github.com/o/r"},
	}, {
		// The colon that separates the subpath is not the first colon in the
		// reference, and on this one it is not the second either.
		name: "an authority with a port",
		ref:  "git+http://127.0.0.1:3000/epos/skills#main:skills/pdf",
		want: GitRef{URL: "http://127.0.0.1:3000/epos/skills", Rev: "main", Subpath: "skills/pdf"},
	}, {
		name: "a branch name containing a slash",
		ref:  "git+https://github.com/o/r#feature/pdf:skills/pdf",
		want: GitRef{URL: "https://github.com/o/r", Rev: "feature/pdf", Subpath: "skills/pdf"},
	}, {
		name: "a fully qualified ref name",
		ref:  "git+https://github.com/o/r#refs/tags/v1.2.0:skills/pdf",
		want: GitRef{URL: "https://github.com/o/r", Rev: "refs/tags/v1.2.0", Subpath: "skills/pdf"},
	}, {
		name: "a trailing slash on the subpath",
		ref:  "git+https://github.com/o/r#main:skills/pdf/",
		want: GitRef{URL: "https://github.com/o/r", Rev: "main", Subpath: "skills/pdf"},
	}, {
		name: "a .git suffix stays part of the URL",
		ref:  "git+https://github.com/o/r.git#main",
		want: GitRef{URL: "https://github.com/o/r.git", Rev: "main"},
	}, {
		name: "a commit SHA is just another ref",
		ref:  "git+https://github.com/o/r#0123456789abcdef0123456789abcdef01234567:x",
		want: GitRef{
			URL:     "https://github.com/o/r",
			Rev:     "0123456789abcdef0123456789abcdef01234567",
			Subpath: "x",
		},
	}}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := ParseGitRef(c.ref)
			require.NoError(t, err)
			assert.Equal(t, c.want, got)
		})
	}
}

func TestParseGitRefRejects(t *testing.T) {
	cases := []struct {
		name string
		ref  string
		msg  string
	}{{
		name: "no git+ prefix",
		ref:  "https://github.com/o/r#main",
		msg:  "a git source starts with git+",
	}, {
		name: "a transport that is not HTTP",
		ref:  "git+ssh://git@github.com/o/r#main",
		msg:  "want git+https://",
	}, {
		name: "no host",
		ref:  "git+https:///o/r#main",
		msg:  "no host",
	}, {
		name: "no repository path",
		ref:  "git+https://github.com#main",
		msg:  "no repository path",
	}, {
		name: "an empty subpath",
		ref:  "git+https://github.com/o/r#main:",
		msg:  "the subpath after : is empty",
	}, {
		name: "a subpath that escapes the repository",
		ref:  "git+https://github.com/o/r#main:../../etc",
		msg:  "escapes the skill root",
	}, {
		name: "an absolute subpath",
		ref:  "git+https://github.com/o/r#main:/etc/passwd",
		msg:  "absolute paths are not allowed",
	}, {
		name: "a subpath that is not canonical",
		ref:  "git+https://github.com/o/r#main:skills/./pdf",
		msg:  "not in canonical form",
	}}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := ParseGitRef(c.ref)
			require.Error(t, err)
			assert.Contains(t, err.Error(), c.msg)
		})
	}
}

// TestFromGitRejectsAMalformedRefWithoutTouchingTheNetwork keeps the reference
// check ahead of the fetch: a Skillfile with a typo in its base must fail on
// the typo, not on a DNS lookup of it.
func TestFromGitRejectsAMalformedRefWithoutTouchingTheNetwork(t *testing.T) {
	_, err := failedBuild(t, "FROM git+ssh://git@github.com/o/r#main\n", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "want git+https://")
}

// blobHash stores a blob and returns its hash.
func blobHash(t *testing.T, s *memory.Storage, body string) plumbing.Hash {
	t.Helper()
	obj := s.NewEncodedObject()
	obj.SetType(plumbing.BlobObject)
	w, err := obj.Writer()
	require.NoError(t, err)
	_, err = w.Write([]byte(body))
	require.NoError(t, err)
	require.NoError(t, w.Close())
	h, err := s.SetEncodedObject(obj)
	require.NoError(t, err)
	return h
}

// treeHash stores a tree and returns its hash.
//
// Written by hand rather than through a worktree because the point of the
// tests below is to build the trees git itself refuses to write.
func treeHash(t *testing.T, s *memory.Storage, entries ...object.TreeEntry) plumbing.Hash {
	t.Helper()
	obj := s.NewEncodedObject()
	require.NoError(t, (&object.Tree{Entries: entries}).Encode(obj))
	h, err := s.SetEncodedObject(obj)
	require.NoError(t, err)
	return h
}

// TestGitTreeRejectsAnEntryThatEscapesTheSkillRoot is the 2.5 check on the one
// input that is not the author's own: a base fetched from somebody else's
// server.
//
// `git mktree` will not write an entry named `..`, but nothing in the wire
// protocol stops a server from serving one, and a walker that joined it onto
// the destination path would write outside the skill.
//
// Two layers refuse it and the order matters only to the message: go-git's own
// tree walker validates entry names and gets there first, and Tree.Set's
// checkPath stands behind it for anything the walker ever stops validating.
// The assertion is therefore on the refusal, plus a direct check that the
// second layer is not merely assumed.
func TestGitTreeRejectsAnEntryThatEscapesTheSkillRoot(t *testing.T) {
	s := memory.NewStorage()
	blob := blobHash(t, s, "pwned\n")
	escaping := treeHash(t, s, object.TreeEntry{Name: "escaped.md", Mode: filemode.Regular, Hash: blob})
	// Entry order is git's, not the test's: a tree object records its entries
	// sorted by name, and go-git refuses to encode one that is not.
	root := treeHash(t, s,
		object.TreeEntry{Name: "..", Mode: filemode.Dir, Hash: escaping},
		object.TreeEntry{Name: "SKILL.md", Mode: filemode.Regular, Hash: blob},
	)

	tree, err := object.GetTree(s, root)
	require.NoError(t, err)

	_, err = gitTreeFiles(tree)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "..")
	assert.Error(t, NewTree().Set("../escaped.md", []byte("pwned\n")),
		"2.5 must also refuse the path on the way into the tree")
}

// TestGitTreeRejectsASymlink covers the other half of what 2.5 rejects. A
// symlink in a git tree is an ordinary, legal object — mode 120000 with the
// target as its content — so unlike the entry above this is a base a real
// repository can hold today.
func TestGitTreeRejectsASymlink(t *testing.T) {
	s := memory.NewStorage()
	root := treeHash(t, s,
		object.TreeEntry{Name: "SKILL.md", Mode: filemode.Regular, Hash: blobHash(t, s, "# skill\n")},
		object.TreeEntry{Name: "escape", Mode: filemode.Symlink, Hash: blobHash(t, s, "/etc/passwd")},
	)

	tree, err := object.GetTree(s, root)
	require.NoError(t, err)

	_, err = gitTreeFiles(tree)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "symlinks are not allowed")
}

// TestGitTreeKeepsBlobBytesVerbatim: no line-ending translation, on any host.
// A checkout would be free to apply core.autocrlf; reading the blob is not.
func TestGitTreeKeepsBlobBytesVerbatim(t *testing.T) {
	s := memory.NewStorage()
	root := treeHash(t, s,
		object.TreeEntry{Name: "crlf.md", Mode: filemode.Regular, Hash: blobHash(t, s, "a\r\nb\r\n")},
		object.TreeEntry{Name: "lf.md", Mode: filemode.Regular, Hash: blobHash(t, s, "a\nb\n")},
	)

	tree, err := object.GetTree(s, root)
	require.NoError(t, err)

	built, err := gitTreeFiles(tree)
	require.NoError(t, err)

	crlf, ok := built.Get("crlf.md")
	require.True(t, ok)
	assert.Equal(t, "a\r\nb\r\n", string(crlf))

	lf, ok := built.Get("lf.md")
	require.True(t, ok)
	assert.Equal(t, "a\nb\n", string(lf))
}

// TestGitTreeFlattensNestedDirectories checks the paths a subtree contributes:
// slash-separated and relative to the subpath, on every platform (2.5).
func TestGitTreeFlattensNestedDirectories(t *testing.T) {
	s := memory.NewStorage()
	blob := blobHash(t, s, "x\n")
	nested := treeHash(t, s, object.TreeEntry{Name: "style.md", Mode: filemode.Regular, Hash: blob})
	root := treeHash(t, s,
		object.TreeEntry{Name: "SKILL.md", Mode: filemode.Regular, Hash: blob},
		object.TreeEntry{Name: "references", Mode: filemode.Dir, Hash: nested},
	)

	tree, err := object.GetTree(s, root)
	require.NoError(t, err)

	built, err := gitTreeFiles(tree)
	require.NoError(t, err)
	assert.Equal(t, []string{"SKILL.md", "references/style.md"}, built.Paths())
}
