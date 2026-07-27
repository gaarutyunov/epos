//go:build integration

package integration

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/gaarutyunov/epos/internal/skillfile"
	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/filemode"
	"github.com/go-git/go-git/v5/plumbing/object"
	githttp "github.com/go-git/go-git/v5/plumbing/transport/http"
	"github.com/go-git/go-git/v5/storage/memory"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcexec "github.com/testcontainers/testcontainers-go/exec"
	"github.com/testcontainers/testcontainers-go/wait"
)

// giteaImage is the git server SPEC.md 13.2 names: Go-native, real HTTP
// transport, which is what makes commit and tree SHA pinning testable at all.
// A file:// repository or a stubbed transport would prove nothing about either.
const giteaImage = "docker.io/gitea/gitea:1.24.6"

const (
	giteaUser     = "epos"
	giteaPassword = "epos-integration-pw"
	giteaEmail    = "epos@example.com"
)

// fixtureSig is the identity every fixture commit and tag carries.
//
// Fixed, and in UTC: a commit SHA covers its author and committer timestamps,
// so a clock-derived signature would give the pinning assertions a different
// SHA on every run and nothing to compare against.
var fixtureSig = object.Signature{
	Name:  "Epos Fixture",
	Email: "fixture@example.com",
	When:  time.Unix(1700000000, 0).UTC(),
}

// gitFile is one blob of a fixture repository.
type gitFile struct {
	path string
	body string
	mode filemode.FileMode
}

func regular(path, body string) gitFile {
	return gitFile{path: path, body: body, mode: filemode.Regular}
}

// gitFixture is the pushed repository and the hashes the tests pin against.
//
// The hashes come from the objects the test itself built, not from anything
// the resolver reported, so "the pin is right" is checked against an
// independent answer rather than against the resolver agreeing with itself.
type gitFixture struct {
	// url is the clone URL, without the git+ prefix.
	url string
	// mainCommit is the head of the default branch, mainTree the tree of
	// skills/pdf on it and mainRoot the commit's own root tree.
	mainCommit plumbing.Hash
	mainTree   plumbing.Hash
	mainRoot   plumbing.Hash
	// docsCommit is a child of mainCommit that edits only README.md, so it
	// shares mainTree: the one fixture that can tell the two halves of the 8.3
	// pin apart.
	docsCommit plumbing.Hash
	// tagCommit is the commit the annotated tag v1.0.0 points at once peeled,
	// tagObject the tag object's own hash, and tagTree the tree of skills/pdf
	// at that commit.
	tagCommit plumbing.Hash
	tagObject plumbing.Hash
	tagTree   plumbing.Hash
}

// TestFromGitBase covers SPEC.md 8.3's git scheme against a real Gitea.
//
// One container and one push serve every subtest: Gitea takes tens of seconds
// to come up, and nothing here mutates the repository.
func TestFromGitBase(t *testing.T) {
	ctx := context.Background()
	base := startGitea(ctx, t)
	fx := seedGitFixture(ctx, t, base)

	t.Run("a branch resolves to a commit and a tree SHA", func(t *testing.T) {
		_, report := buildFromGit(t, fx.url+"#main:skills/pdf")

		require.Len(t, report.GitBases, 1)
		pin := report.GitBases[0]
		assert.Equal(t, fx.mainCommit.String(), pin.Commit)
		assert.Equal(t, fx.mainTree.String(), pin.Tree)
		assert.Equal(t, "main", pin.Rev)
		assert.Equal(t, "skills/pdf", pin.Subpath)
	})

	t.Run("a tag resolves to its own commit, not the branch head", func(t *testing.T) {
		_, report := buildFromGit(t, fx.url+"#v1.0.0:skills/pdf")

		require.Len(t, report.GitBases, 1)
		pin := report.GitBases[0]
		assert.Equal(t, fx.tagCommit.String(), pin.Commit)
		assert.Equal(t, fx.tagTree.String(), pin.Tree)
		assert.NotEqual(t, fx.mainCommit.String(), pin.Commit,
			"the tag is an older commit than the branch head")
		assert.NotEqual(t, fx.tagObject.String(), pin.Commit,
			"an annotated tag is its own object; the pin is the commit it points at")
	})

	t.Run("the tree SHA pins the subpath and is not the commit SHA", func(t *testing.T) {
		_, report := buildFromGit(t, fx.url+"#main:skills/pdf")
		pin := report.GitBases[0]

		assert.NotEqual(t, pin.Commit, pin.Tree,
			"8.3 pins two distinct objects; a tree SHA that equalled the commit SHA would mean only one was recorded")
		assert.NotEqual(t, fx.mainRoot.String(), pin.Tree,
			"the pin is the subpath's tree, not the repository root's")
	})

	t.Run("a commit outside the subpath moves only the commit SHA", func(t *testing.T) {
		_, atMain := buildFromGit(t, fx.url+"#main:skills/pdf")
		_, atDocs := buildFromGit(t, fx.url+"#docs:skills/pdf")

		require.Equal(t, fx.docsCommit.String(), atDocs.GitBases[0].Commit)
		assert.NotEqual(t, atMain.GitBases[0].Commit, atDocs.GitBases[0].Commit,
			"a commit anywhere in the repository moves the commit SHA")
		assert.Equal(t, atMain.GitBases[0].Tree, atDocs.GitBases[0].Tree,
			"8.3 pins the subpath's tree, and an edit outside the subpath leaves it alone")
	})

	t.Run("the recorded commit SHA rebuilds the same base", func(t *testing.T) {
		fromRef, refReport := buildFromGit(t, fx.url+"#main:skills/pdf")
		pin := refReport.GitBases[0]

		// A bare SHA is not a ref and cannot be asked for by name, so this
		// takes the other resolution path entirely — and it is the path that
		// makes the pin worth recording at all, because rebuilding from it is
		// the only thing a pin is for.
		fromSHA, shaReport := buildFromGit(t, fx.url+"#"+pin.Commit+":skills/pdf")

		require.Len(t, shaReport.GitBases, 1)
		assert.Equal(t, pin.Commit, shaReport.GitBases[0].Commit)
		assert.Equal(t, pin.Tree, shaReport.GitBases[0].Tree,
			"the same commit yields the same subpath tree however it was named")
		require.Equal(t, fromRef.Paths(), fromSHA.Paths())
		for _, p := range fromRef.Paths() {
			byRef, _ := fromRef.Get(p)
			bySHA, _ := fromSHA.Get(p)
			assert.Equal(t, byRef, bySHA, "%s differs when the same commit is reached by SHA", p)
		}
	})

	t.Run("only the subpath enters the stage", func(t *testing.T) {
		tree, _ := buildFromGit(t, fx.url+"#main:skills/pdf")

		assert.Equal(t, []string{"SKILL.md", "extra.md", "references/style.md"}, tree.Paths())

		_, ok := tree.Get("README.md")
		assert.False(t, ok, "a file at the repository root is outside the subpath")
		for _, p := range tree.Paths() {
			assert.NotContains(t, p, "skills/", "paths are relative to the subpath")
		}
	})

	t.Run("no fragment takes the default branch and the whole repository", func(t *testing.T) {
		tree, report := buildFromGit(t, fx.url)

		require.Len(t, report.GitBases, 1)
		pin := report.GitBases[0]
		assert.Equal(t, fx.mainCommit.String(), pin.Commit)
		assert.Equal(t, fx.mainRoot.String(), pin.Tree, "an empty subpath pins the root tree")
		assert.Contains(t, tree.Paths(), "README.md")
		assert.Contains(t, tree.Paths(), "skills/pdf/SKILL.md")
	})

	t.Run("resolving the same ref twice gives identical pins", func(t *testing.T) {
		first, firstReport := buildFromGit(t, fx.url+"#main:skills/pdf")
		second, secondReport := buildFromGit(t, fx.url+"#main:skills/pdf")

		assert.Equal(t, firstReport.GitBases, secondReport.GitBases,
			"identical inputs must produce identical pins; SPEC 2.4 requires it")
		require.Equal(t, first.Paths(), second.Paths())
		for _, p := range first.Paths() {
			a, _ := first.Get(p)
			b, _ := second.Get(p)
			assert.Equal(t, a, b, "%s differs between two resolutions of the same ref", p)
		}
	})

	t.Run("a ref that does not exist fails", func(t *testing.T) {
		_, _, err := buildGit(t, fx.url+"#no-such-ref:skills/pdf")
		require.Error(t, err)
		assert.Contains(t, err.Error(), `no branch, tag or commit named "no-such-ref"`)
	})

	t.Run("a subpath that does not exist fails", func(t *testing.T) {
		_, _, err := buildGit(t, fx.url+"#main:skills/nope")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "skills/nope: no such directory at "+fx.mainCommit.String())
	})

	t.Run("a base whose paths violate 2.5 is rejected", func(t *testing.T) {
		// mode 120000 is an ordinary, legal git object, so this is a base a
		// real repository can hold — and 2.5 rejects symlinks outright.
		tree, _, err := buildGit(t, fx.url+"#symlink")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "symlinks are not allowed in a skill")
		assert.Nil(t, tree, "a rejected base contributes nothing to the stage")
	})
}

// buildGit runs a one-line Skillfile whose only instruction is a git FROM.
func buildGit(t *testing.T, ref string) (*skillfile.Tree, *skillfile.Report, error) {
	t.Helper()
	sf, err := skillfile.Parse([]byte("FROM git+" + ref + "\n"))
	require.NoError(t, err)
	return skillfile.Build(sf, t.TempDir(), nil)
}

// buildFromGit is buildGit for the cases that must succeed.
func buildFromGit(t *testing.T, ref string) (*skillfile.Tree, *skillfile.Report) {
	t.Helper()
	tree, report, err := buildGit(t, ref)
	require.NoError(t, err)
	return tree, report
}

// startGitea brings up Gitea and returns its base URL.
//
// Installed headlessly through GITEA__* environment overrides — the web
// installer would otherwise sit on the first request waiting for a form — and
// with push-to-create on, so seeding needs one authenticated push and no API
// calls at all.
func startGitea(ctx context.Context, t *testing.T) string {
	t.Helper()

	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:        giteaImage,
			ExposedPorts: []string{"3000/tcp"},
			Env: map[string]string{
				"GITEA__database__DB_TYPE":                       "sqlite3",
				"GITEA__database__PATH":                          "/data/gitea/gitea.db",
				"GITEA__security__INSTALL_LOCK":                  "true",
				"GITEA__server__HTTP_PORT":                       "3000",
				"GITEA__repository__DEFAULT_BRANCH":              "main",
				"GITEA__repository__ENABLE_PUSH_CREATE_USER":     "true",
				"GITEA__repository__DEFAULT_PUSH_CREATE_PRIVATE": "false",
				"GITEA__service__DISABLE_REGISTRATION":           "true",
				"GITEA__log__LEVEL":                              "Warn",
			},
			WaitingFor: wait.ForHTTP("/api/healthz").WithPort("3000/tcp").
				WithStartupTimeout(5 * time.Minute),
		},
		Started: true,
	})
	require.NoError(t, err, "start gitea")
	t.Cleanup(func() {
		if err := c.Terminate(context.Background()); err != nil {
			t.Logf("terminate gitea: %v", err)
		}
	})

	// The push needs an account to authenticate as, and Gitea's CLI is the only
	// way to make the first one without a token that does not exist yet. It
	// runs as the `git` user because that is who owns /data.
	code, out, err := c.Exec(ctx, []string{
		"gitea", "admin", "user", "create",
		"--username", giteaUser,
		"--password", giteaPassword,
		"--email", giteaEmail,
		"--admin", "--must-change-password=false",
	}, tcexec.WithUser("git"))
	require.NoError(t, err, "create gitea user")
	require.Zero(t, code, "create gitea user: %s", readAll(t, out))

	endpoint, err := c.PortEndpoint(ctx, "3000/tcp", "http")
	require.NoError(t, err, "gitea endpoint")
	return endpoint
}

// seedGitFixture pushes the fixture repository over HTTP and returns the
// hashes it was built from.
//
// The objects are written straight into the storer rather than through a
// worktree: a worktree cannot hold the symlink case on every platform, and a
// checkout is where line-ending conversion would creep into content that the
// pinning assertions need to stay byte-exact.
func seedGitFixture(ctx context.Context, t *testing.T, base string) gitFixture {
	t.Helper()

	st := memory.NewStorage()
	repo, err := git.Init(st, nil)
	require.NoError(t, err)

	const skillV1 = "---\nname: pdf\nversion: 1.0.0\n---\n\n# PDF\n"
	const skillV2 = "---\nname: pdf\nversion: 1.1.0\n---\n\n# PDF\n"

	v1 := []gitFile{
		regular("README.md", "# skills\n"),
		regular("skills/other/SKILL.md", "---\nname: other\n---\n"),
		regular("skills/pdf/SKILL.md", skillV1),
		regular("skills/pdf/references/style.md", "House style.\n"),
	}
	root1 := writeTree(t, st, v1)
	tagCommit := writeCommit(t, st, root1, "the tagged release")

	// An annotated tag, so the pin has something to peel: the tag is its own
	// object with its own SHA, and recording that would pin the label.
	tagObject := writeTag(t, st, tagCommit, "v1.0.0")

	v2 := append([]gitFile{regular("skills/pdf/extra.md", "Extra.\n")}, replaceFile(t, v1,
		regular("skills/pdf/SKILL.md", skillV2))...)
	root2 := writeTree(t, st, v2)
	mainCommit := writeCommit(t, st, root2, "the branch head", tagCommit)

	// A commit that touches only README.md. It is what makes the claim 8.3
	// rests on checkable rather than asserted: a commit anywhere in the
	// repository moves the commit SHA, and only a commit inside the subpath
	// moves the tree SHA, which is why the pin records both.
	v3 := replaceFile(t, v2, regular("README.md", "# skills, revised\n"))
	root3 := writeTree(t, st, v3)
	docsCommit := writeCommit(t, st, root3, "an edit outside the subpath", mainCommit)

	// A symlink is legal in a git tree and rejected by 2.5, so it lives on its
	// own branch — otherwise every subtest that takes the whole repository
	// would trip over it.
	symlinkRoot := writeTree(t, st, []gitFile{
		regular("SKILL.md", "---\nname: bad\n---\n"),
		{path: "escape", body: "../../../etc/passwd", mode: filemode.Symlink},
	})
	symlinkCommit := writeCommit(t, st, symlinkRoot, "a base with a symlink")

	url := base + "/" + giteaUser + "/skills.git"
	_, err = repo.CreateRemote(&config.RemoteConfig{Name: "origin", URLs: []string{url}})
	require.NoError(t, err)

	// One push per ref, main first: push-to-create makes the repository on the
	// first push and takes its default branch from it, and the subtest for a
	// FROM with no fragment asserts on the remote's HEAD being main.
	refs := []struct {
		name string
		hash plumbing.Hash
	}{
		{"refs/heads/main", mainCommit},
		{"refs/heads/docs", docsCommit},
		{"refs/heads/symlink", symlinkCommit},
		{"refs/tags/v1.0.0", tagObject},
	}
	auth := &githttp.BasicAuth{Username: giteaUser, Password: giteaPassword}
	for _, r := range refs {
		name := plumbing.ReferenceName(r.name)
		require.NoError(t, repo.Storer.SetReference(plumbing.NewHashReference(name, r.hash)))
		require.NoError(t, repo.PushContext(ctx, &git.PushOptions{
			RemoteName: "origin",
			RefSpecs:   []config.RefSpec{config.RefSpec("+" + r.name + ":" + r.name)},
			Auth:       auth,
		}), "push %s", r.name)
	}

	return gitFixture{
		url:        url,
		mainCommit: mainCommit,
		mainRoot:   root2,
		mainTree:   subtreeHash(t, st, root2, "skills/pdf"),
		docsCommit: docsCommit,
		tagCommit:  tagCommit,
		tagObject:  tagObject,
		tagTree:    subtreeHash(t, st, root1, "skills/pdf"),
	}
}

// replaceFile returns files with the entry at f's path swapped for f.
//
// By path rather than by index: the fixture's shape is what the assertions
// read, and an index into a slice built by append is the kind of thing that
// silently starts pointing at the wrong file when a line is added above it.
func replaceFile(t *testing.T, files []gitFile, f gitFile) []gitFile {
	t.Helper()
	out := make([]gitFile, len(files))
	copy(out, files)
	replaced := false
	for i := range out {
		if out[i].path == f.path {
			out[i], replaced = f, true
		}
	}
	require.True(t, replaced, "%s is not in the fixture", f.path)
	return out
}

// writeBlob stores a blob and returns its hash.
func writeBlob(t *testing.T, st *memory.Storage, body string) plumbing.Hash {
	t.Helper()
	obj := st.NewEncodedObject()
	obj.SetType(plumbing.BlobObject)
	w, err := obj.Writer()
	require.NoError(t, err)
	_, err = w.Write([]byte(body))
	require.NoError(t, err)
	require.NoError(t, w.Close())
	h, err := st.SetEncodedObject(obj)
	require.NoError(t, err)
	return h
}

// writeTree turns a flat list of slash-separated paths into nested tree
// objects and returns the root's hash.
func writeTree(t *testing.T, st *memory.Storage, files []gitFile) plumbing.Hash {
	t.Helper()

	var entries []object.TreeEntry
	dirs := map[string][]gitFile{}
	var order []string

	for _, f := range files {
		dir, rest, nested := strings.Cut(f.path, "/")
		if !nested {
			entries = append(entries, object.TreeEntry{
				Name: f.path, Mode: f.mode, Hash: writeBlob(t, st, f.body),
			})
			continue
		}
		if _, seen := dirs[dir]; !seen {
			order = append(order, dir)
		}
		dirs[dir] = append(dirs[dir], gitFile{path: rest, body: f.body, mode: f.mode})
	}
	for _, dir := range order {
		entries = append(entries, object.TreeEntry{
			Name: dir, Mode: filemode.Dir, Hash: writeTree(t, st, dirs[dir]),
		})
	}

	// Git records entries in its own order and go-git refuses to encode a tree
	// that is not in it, which is also what keeps the fixture's hashes stable
	// however the file list above is written.
	sort.Sort(object.TreeEntrySorter(entries))

	obj := st.NewEncodedObject()
	require.NoError(t, (&object.Tree{Entries: entries}).Encode(obj))
	h, err := st.SetEncodedObject(obj)
	require.NoError(t, err)
	return h
}

// writeCommit stores a commit and returns its hash.
func writeCommit(t *testing.T, st *memory.Storage, tree plumbing.Hash, msg string, parents ...plumbing.Hash) plumbing.Hash {
	t.Helper()
	obj := st.NewEncodedObject()
	require.NoError(t, (&object.Commit{
		Author:       fixtureSig,
		Committer:    fixtureSig,
		Message:      msg + "\n",
		TreeHash:     tree,
		ParentHashes: parents,
	}).Encode(obj))
	h, err := st.SetEncodedObject(obj)
	require.NoError(t, err)
	return h
}

// writeTag stores an annotated tag and returns the tag object's hash.
func writeTag(t *testing.T, st *memory.Storage, target plumbing.Hash, name string) plumbing.Hash {
	t.Helper()
	obj := st.NewEncodedObject()
	require.NoError(t, (&object.Tag{
		Name:       name,
		Tagger:     fixtureSig,
		Message:    name + "\n",
		TargetType: plumbing.CommitObject,
		Target:     target,
	}).Encode(obj))
	h, err := st.SetEncodedObject(obj)
	require.NoError(t, err)
	return h
}

// subtreeHash returns the hash of the tree at path under root.
func subtreeHash(t *testing.T, st *memory.Storage, root plumbing.Hash, p string) plumbing.Hash {
	t.Helper()
	tree, err := object.GetTree(st, root)
	require.NoError(t, err)
	sub, err := tree.Tree(p)
	require.NoError(t, err)
	return sub.Hash
}

// readAll drains a container exec's output for a failure message.
//
// Quoted, because the output is multiplexed Docker stream framing with its
// 8-byte headers still in it and pasting that raw into a test failure is how a
// terminal ends up in a state nobody asked for.
func readAll(t *testing.T, r io.Reader) string {
	t.Helper()
	out, err := io.ReadAll(r)
	require.NoError(t, err)
	return fmt.Sprintf("%q", out)
}
