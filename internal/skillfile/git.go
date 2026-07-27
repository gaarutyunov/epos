package skillfile

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strings"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/filemode"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/storage/memory"
)

// gitPrefix marks a git source. 8.3 spells it `git+https://`; the transport is
// whatever follows the plus, so `git+http://` reaches a server without TLS —
// which is what a local Gitea in a container is.
const gitPrefix = "git+"

// gitTimeout bounds one FROM's network work. Same reasoning as awkTimeout: a
// build must not hang forever on someone else's server.
const gitTimeout = 5 * time.Minute

// GitRef is a parsed `git+` FROM reference (8.3).
type GitRef struct {
	// URL is the transport URL, with the `git+` prefix and the fragment
	// removed: `git+https://host/o/r#v1.2.0:skills/pdf` yields
	// `https://host/o/r`.
	URL string
	// Rev is the fragment's ref — a branch, a tag, a full ref name or a commit
	// SHA. Empty means the remote's default branch.
	Rev string
	// Subpath is the directory of the tree the base is taken from, without a
	// trailing slash. Empty means the repository root.
	Subpath string
}

// GitBase is a resolved git base: what 8.3 calls the pin.
//
// A ref is mutable — a branch moves, a tag can be re-pointed — so recording it
// is not a pin at all. The commit SHA fixes the whole repository at a point in
// history and the tree SHA fixes the bytes of the subdirectory that actually
// entered the stage, which is the narrower and more useful of the two: an
// unrelated commit elsewhere in the repository moves the commit SHA and leaves
// the tree SHA alone.
type GitBase struct {
	// Ref is the reference exactly as the Skillfile wrote it, after ARG
	// expansion.
	Ref string
	// URL, Rev and Subpath are the parsed reference.
	URL     string
	Rev     string
	Subpath string
	// Commit is the full SHA the ref resolved to, with annotated tags peeled
	// to the commit they point at.
	Commit string
	// Tree is the SHA of Subpath's tree object — the root tree when Subpath is
	// empty. Never equal to Commit: they are different object types.
	Tree string
}

// ParseGitRef splits a `git+` FROM reference into URL, ref and subpath.
//
// The fragment is what carries the ref and the subpath, so the `:` separating
// them is looked for *inside the fragment* and nowhere else. Splitting the
// whole reference at its first colon is the bug this avoids twice over: it
// lands inside `git+https:` on every reference, and on a reference without a
// scheme-shaped prefix it would land inside a `host:port` authority —
// `git+http://127.0.0.1:3000/o/r#main:skills/pdf` has three colons and only
// the last one separates anything.
func ParseGitRef(ref string) (GitRef, error) {
	rest, ok := strings.CutPrefix(ref, gitPrefix)
	if !ok {
		return GitRef{}, fmt.Errorf("%s: a git source starts with %s", ref, gitPrefix)
	}

	base, fragment, _ := strings.Cut(rest, "#")

	u, err := url.Parse(base)
	switch {
	case err != nil:
		return GitRef{}, fmt.Errorf("%s: %w", ref, err)
	case u.Scheme != "http" && u.Scheme != "https":
		return GitRef{}, fmt.Errorf("%s: want %shttps://<host>/<path>", ref, gitPrefix)
	case u.Host == "":
		return GitRef{}, fmt.Errorf("%s: no host", ref)
	case u.Path == "" || u.Path == "/":
		return GitRef{}, fmt.Errorf("%s: no repository path", ref)
	}

	out := GitRef{URL: base}

	rev, subpath, hasSubpath := strings.Cut(fragment, ":")
	out.Rev = rev
	if !hasSubpath {
		return out, nil
	}

	out.Subpath = strings.TrimSuffix(subpath, "/")
	if out.Subpath == "" {
		return GitRef{}, fmt.Errorf("%s: the subpath after : is empty", ref)
	}
	// The subpath selects inside somebody else's repository, so it is checked
	// against the same 2.5 rules as everything else: no absolute path, no `..`,
	// canonical form only.
	if err := checkPath(out.Subpath); err != nil {
		return GitRef{}, fmt.Errorf("%s: subpath %w", ref, err)
	}
	return out, nil
}

// resolveGit fetches a git base and records its pin on the report.
func (b *builder) resolveGit(ref string) (*Tree, string, error) {
	parsed, err := ParseGitRef(ref)
	if err != nil {
		return nil, "", err
	}

	ctx, cancel := context.WithTimeout(context.Background(), gitTimeout)
	defer cancel()

	tree, base, err := fetchGitBase(ctx, parsed)
	if err != nil {
		return nil, "", err
	}
	base.Ref = ref

	// 8.3 makes the pin the point of the git scheme, so it is recorded rather
	// than discarded once the bytes are in hand: a rebuild is only verifiable
	// against the commit and tree SHAs the first build actually saw.
	b.report.GitBases = append(b.report.GitBases, base)
	return tree, base.Pin(), nil
}

// Pin renders the two SHAs 8.3 pins a git base with as one string, for the
// provenance annotation 2.3 defines as "commit+tree SHA".
func (g GitBase) Pin() string { return g.Commit + "+" + g.Tree }

// fetchGitBase resolves a reference to its pin and materialises the subpath.
//
// Everything happens in memory: no worktree is ever checked out. That is not
// only about speed — a checkout is where a git client would apply line-ending
// conversion and filesystem-dependent mode handling, and 2.4 needs the bytes
// that enter the layer to be the bytes in the blob, on every platform.
func fetchGitBase(ctx context.Context, r GitRef) (*Tree, GitBase, error) {
	repo, err := git.Init(memory.NewStorage(), nil)
	if err != nil {
		return nil, GitBase{}, err
	}
	remote, err := repo.CreateRemote(&config.RemoteConfig{Name: "origin", URLs: []string{r.URL}})
	if err != nil {
		return nil, GitBase{}, err
	}

	advertised, err := remote.ListContext(ctx, &git.ListOptions{})
	if err != nil {
		return nil, GitBase{}, fmt.Errorf("%s: %w", r.URL, err)
	}

	specs, err := fetchSpecs(advertised, r.Rev)
	if err != nil {
		return nil, GitBase{}, fmt.Errorf("%s: %w", r.URL, err)
	}

	// No Depth: a shallow fetch needs the server to advertise the capability,
	// and a base that resolves everywhere is worth more than one that is quick
	// on the servers that happen to support it.
	err = remote.FetchContext(ctx, &git.FetchOptions{RefSpecs: specs, Tags: git.NoTags})
	if err != nil && !errors.Is(err, git.NoErrAlreadyUpToDate) {
		return nil, GitBase{}, fmt.Errorf("%s: %w", r.URL, err)
	}

	commit, err := resolveCommit(repo, advertised, r.Rev)
	if err != nil {
		return nil, GitBase{}, err
	}

	root, err := commit.Tree()
	if err != nil {
		return nil, GitBase{}, err
	}
	sub := root
	if r.Subpath != "" {
		sub, err = root.Tree(r.Subpath)
		if err != nil {
			return nil, GitBase{}, fmt.Errorf("%s: no such directory at %s", r.Subpath, commit.Hash)
		}
	}

	tree, err := gitTreeFiles(sub)
	if err != nil {
		return nil, GitBase{}, err
	}

	return tree, GitBase{
		URL:     r.URL,
		Rev:     r.Rev,
		Subpath: r.Subpath,
		Commit:  commit.Hash.String(),
		Tree:    sub.Hash.String(),
	}, nil
}

// refCandidates lists the ref names rev could mean, most specific first.
//
// `main` is a branch before it is a tag, which is git's own precedence, and a
// fully qualified `refs/…` is taken as written.
func refCandidates(rev string) []plumbing.ReferenceName {
	if strings.HasPrefix(rev, "refs/") {
		return []plumbing.ReferenceName{plumbing.ReferenceName(rev)}
	}
	return []plumbing.ReferenceName{
		plumbing.NewBranchReferenceName(rev),
		plumbing.NewTagReferenceName(rev),
	}
}

// advertisedRef finds rev among the refs the remote advertised.
//
// An empty rev means the remote's HEAD, which is how `FROM git+https://…/r`
// with no fragment reaches the default branch.
func advertisedRef(advertised []*plumbing.Reference, rev string) (*plumbing.Reference, bool) {
	byName := map[plumbing.ReferenceName]*plumbing.Reference{}
	for _, ref := range advertised {
		byName[ref.Name()] = ref
	}

	wanted := refCandidates(rev)
	if rev == "" {
		wanted = []plumbing.ReferenceName{plumbing.HEAD}
	}

	for _, name := range wanted {
		ref, ok := byName[name]
		if !ok {
			continue
		}
		// A symbolic HEAD names the default branch rather than a hash, so
		// follow it to the branch the server actually advertised.
		if ref.Type() == plumbing.SymbolicReference {
			if target, ok := byName[ref.Target()]; ok {
				return target, true
			}
			continue
		}
		return ref, true
	}
	return nil, false
}

// fetchSpecs says what to pull down for rev.
//
// A named ref is fetched on its own. A bare commit SHA is not a ref and cannot
// be asked for by name — fetching a SHA directly needs `uploadpack.allowAnySHA1InWant`
// on the server, which is off by default — so every branch and tag comes down
// instead and the SHA is looked up locally afterwards.
func fetchSpecs(advertised []*plumbing.Reference, rev string) ([]config.RefSpec, error) {
	if ref, ok := advertisedRef(advertised, rev); ok {
		name := ref.Name()
		return []config.RefSpec{config.RefSpec("+" + name.String() + ":" + name.String())}, nil
	}
	if plumbing.IsHash(rev) {
		return []config.RefSpec{
			"+refs/heads/*:refs/heads/*",
			"+refs/tags/*:refs/tags/*",
		}, nil
	}
	return nil, fmt.Errorf("no branch, tag or commit named %q", rev)
}

// resolveCommit turns rev into the commit it names, peeling annotated tags.
//
// Peeling is what makes `#v1.2.0` a pin rather than a lucky guess: an
// annotated tag is its own object with its own SHA, and recording that SHA
// would pin the label instead of the content it labels.
func resolveCommit(repo *git.Repository, advertised []*plumbing.Reference, rev string) (*object.Commit, error) {
	var hash plumbing.Hash
	if ref, ok := advertisedRef(advertised, rev); ok {
		hash = ref.Hash()
	} else {
		hash = plumbing.NewHash(rev)
	}

	obj, err := repo.Object(plumbing.AnyObject, hash)
	if err != nil {
		return nil, fmt.Errorf("no branch, tag or commit named %q", rev)
	}
	for {
		switch o := obj.(type) {
		case *object.Commit:
			return o, nil
		case *object.Tag:
			if obj, err = o.Object(); err != nil {
				return nil, fmt.Errorf("%q: %w", rev, err)
			}
		default:
			return nil, fmt.Errorf("%q resolves to a %s, want a commit", rev, obj.Type())
		}
	}
}

// gitTreeFiles reads a git tree into a Tree.
//
// Every path goes through Tree.Set, and so through checkPath, because a git
// base is somebody else's input: a tree entry named `..` is not something git
// itself writes, but nothing stops a server from serving one, and it must not
// become a write outside the skill root (2.5).
//
// File modes are dropped on purpose. 2.4 fixes permissions at pack time, so
// carrying the executable bit through here would only be a way for the layer
// to disagree with itself.
func gitTreeFiles(t *object.Tree) (*Tree, error) {
	out := NewTree()
	// Files walks the tree recursively and skips directories and submodules —
	// a submodule's content is in another repository and never in this tree.
	err := t.Files().ForEach(func(f *object.File) error {
		switch f.Mode {
		case filemode.Regular, filemode.Executable:
		case filemode.Symlink:
			return fmt.Errorf("%s: symlinks are not allowed in a skill", f.Name)
		default:
			return fmt.Errorf("%s: only regular files can be built from", f.Name)
		}

		body, err := blobBytes(f)
		if err != nil {
			return err
		}
		return out.Set(f.Name, body)
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// blobBytes reads a blob verbatim.
func blobBytes(f *object.File) ([]byte, error) {
	r, err := f.Reader()
	if err != nil {
		return nil, err
	}
	defer func() { _ = r.Close() }()
	return io.ReadAll(r)
}
