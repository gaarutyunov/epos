package skillfile

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// tarEntry is one member of a fixture content layer.
type tarEntry struct {
	name     string
	body     string
	typeflag byte
	linkname string
}

func reg(name, body string) tarEntry {
	return tarEntry{name: name, body: body, typeflag: tar.TypeReg}
}

// layerOf renders entries as the tar+gzip a skill artifact's content layer is
// (2.1). Built by hand rather than through internal/artifact, because the base
// under test is a third party's and the point is what Epos accepts from one.
func layerOf(t *testing.T, entries ...tarEntry) []byte {
	t.Helper()

	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)
	for _, e := range entries {
		typeflag := e.typeflag
		if typeflag == 0 {
			typeflag = tar.TypeReg
		}
		require.NoError(t, tw.WriteHeader(&tar.Header{
			Name:     e.name,
			Mode:     0o644,
			Typeflag: typeflag,
			Linkname: e.linkname,
			Size:     int64(len(e.body)),
			Format:   tar.FormatPAX,
		}))
		_, err := tw.Write([]byte(e.body))
		require.NoError(t, err)
	}
	require.NoError(t, tw.Close())
	require.NoError(t, gw.Close())
	return buf.Bytes()
}

// SPEC.md 8.3: an OCI reference can be pinned by tag or by digest, and the
// registry may carry a port. All three put a colon somewhere different.
func TestParseOCIRef(t *testing.T) {
	tests := []struct {
		name       string
		ref        string
		registry   string
		repository string
		reference  string
	}{
		{
			name:       "a tag",
			ref:        "ghcr.io/o/agent-skills/pdf:1.2.0",
			registry:   "ghcr.io",
			repository: "o/agent-skills/pdf",
			reference:  "1.2.0",
		},
		{
			name:       "a digest",
			ref:        "ghcr.io/o/agent-skills/pdf@sha256:" + zeroHex,
			registry:   "ghcr.io",
			repository: "o/agent-skills/pdf",
			reference:  "sha256:" + zeroHex,
		},
		{
			name:       "a registry with a port",
			ref:        "127.0.0.1:5000/demo/agent-skills/pdf:1.2.0",
			registry:   "127.0.0.1:5000",
			repository: "demo/agent-skills/pdf",
			reference:  "1.2.0",
		},
		{
			name:       "a registry with a port, pinned by digest",
			ref:        "127.0.0.1:5000/demo/agent-skills/pdf@sha256:" + zeroHex,
			registry:   "127.0.0.1:5000",
			repository: "demo/agent-skills/pdf",
			reference:  "sha256:" + zeroHex,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			parsed, err := ParseOCIRef(tc.ref)
			require.NoError(t, err)
			assert.Equal(t, tc.registry, parsed.Registry)
			assert.Equal(t, tc.repository, parsed.Repository)
			assert.Equal(t, tc.reference, parsed.Reference)
		})
	}
}

// zeroHex is a syntactically valid sha256 hex payload for reference-parsing
// fixtures. Nothing resolves it; only the shape of the reference is under test.
const zeroHex = "0000000000000000000000000000000000000000000000000000000000000000"

func TestParseOCIRefWithoutATagOrDigest(t *testing.T) {
	_, err := ParseOCIRef("ghcr.io/o/agent-skills/pdf")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "needs a tag or a digest")
}

// SPEC.md 8.3 gives the OCI scheme no prefix, so a FROM is told from a local
// directory by what precedes its first slash — Docker's own rule.
func TestLooksLikeOCIRef(t *testing.T) {
	oci := []string{
		"ghcr.io/o/agent-skills/pdf:1.2.0",
		"127.0.0.1:5000/demo/agent-skills/pdf:1.2.0",
		"localhost/demo/pdf:1.2.0",
		"ghcr.io/o/agent-skills/pdf@sha256:" + zeroHex,
	}
	for _, ref := range oci {
		assert.True(t, looksLikeOCIRef(ref), "%s names a registry", ref)
	}

	local := []string{
		"./skills/base",
		"../shared/base",
		"/absolute/base",
		"base",
		"bases/pdf",
		"skills/base",
		".",
	}
	for _, ref := range local {
		assert.False(t, looksLikeOCIRef(ref), "%s names a directory in the context", ref)
	}
}

// SPEC.md 2.1: the layer is rooted at `<skill-name>/`, and a base enters the
// stage as the skill it is rather than as a directory named after it.
func TestOCITreeStripsTheSkillRoot(t *testing.T) {
	tree, err := ociTreeFiles(layerOf(t,
		tarEntry{name: "pdf/", typeflag: tar.TypeDir},
		reg("pdf/SKILL.md", "---\nname: pdf\n---\n"),
		tarEntry{name: "pdf/references/", typeflag: tar.TypeDir},
		reg("pdf/references/style.md", "House style.\n"),
	))
	require.NoError(t, err)

	assert.Equal(t, []string{"SKILL.md", "references/style.md"}, tree.Paths())
	body, ok := tree.Get("SKILL.md")
	require.True(t, ok)
	assert.Equal(t, "---\nname: pdf\n---\n", string(body))
}

// SPEC.md 2.5 is deliberately permissive, and B2 is where that matters most: a
// third-party base can carry a name the consumer deriving from it cannot fix,
// and every one of these is legal on Linux and accepted by every other tool.
// Rejecting them at build would refuse a base that `oras pull` accepts.
func TestOCITreeAcceptsPathsTheConsumerCannotFix(t *testing.T) {
	awkward := []string{
		"aux.md",                  // a Windows reserved device name
		"references/con",          // and another, without an extension
		"references/a:b.md",       // one of < > : " \ | ? *
		`references/back\lash.md`, // a backslash inside a single entry name
		"references/trailing.",    // a trailing dot
		"references/trailing ",    // a trailing space
		"references/README.md",    // collides with readme.md only case-insensitively
		"references/readme.md",
	}

	entries := []tarEntry{reg("pdf/SKILL.md", "---\nname: pdf\n---\n")}
	for _, p := range awkward {
		entries = append(entries, reg("pdf/"+p, "content of "+p+"\n"))
	}

	tree, err := ociTreeFiles(layerOf(t, entries...))
	require.NoError(t, err, "2.5 must not reject a base over a portability question")

	for _, p := range awkward {
		body, ok := tree.Get(p)
		assert.True(t, ok, "%s did not survive into the stage", p)
		assert.Equal(t, "content of "+p+"\n", string(body))
	}
}

// The other half of 2.5: what it does reject is rejected, because each of these
// is a write outside the skill root rather than a portability question.
func TestOCITreeRejectsWhatEscapesTheSkillRoot(t *testing.T) {
	tests := []struct {
		name  string
		entry tarEntry
		want  string
	}{
		{
			name:  "a parent traversal",
			entry: reg("pdf/../../etc/passwd", "root:x:0:0\n"),
			want:  "escapes the skill root",
		},
		{
			name:  "a parent traversal in the middle",
			entry: reg("pdf/references/../../escape.md", "escaped\n"),
			want:  "escapes the skill root",
		},
		{
			name: "a symlink",
			entry: tarEntry{
				name: "pdf/escape", typeflag: tar.TypeSymlink, linkname: "../../../etc/passwd",
			},
			want: "symlinks are not allowed in a skill",
		},
		{
			name: "a hard link",
			entry: tarEntry{
				name: "pdf/escape", typeflag: tar.TypeLink, linkname: "../../../etc/passwd",
			},
			want: "symlinks are not allowed in a skill",
		},
		{
			name:  "a character device",
			entry: tarEntry{name: "pdf/null", typeflag: tar.TypeChar},
			want:  "only regular files can be built from",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ociTreeFiles(layerOf(t,
				reg("pdf/SKILL.md", "---\nname: pdf\n---\n"), tc.entry))
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
		})
	}
}

// An absolute entry name never reaches the skill-root strip: it has no root to
// strip, and accepting it would put the base's files outside the artifact.
func TestOCITreeRejectsALayerWithNoSkillRoot(t *testing.T) {
	_, err := ociTreeFiles(layerOf(t, reg("SKILL.md", "---\nname: pdf\n---\n")))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not rooted at a skill directory")
}

// Two roots would mean the artifact holds two skills, and stripping either one
// would silently merge them (2.1).
func TestOCITreeRejectsTwoRoots(t *testing.T) {
	_, err := ociTreeFiles(layerOf(t,
		reg("pdf/SKILL.md", "---\nname: pdf\n---\n"),
		reg("other/SKILL.md", "---\nname: other\n---\n"),
	))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "rooted at both")
}

func TestOCITreeRejectsAnEmptyLayer(t *testing.T) {
	_, err := ociTreeFiles(layerOf(t, tarEntry{name: "pdf/", typeflag: tar.TypeDir}))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "holds no files")
}

func TestOCITreeRejectsSomethingThatIsNotATarGzip(t *testing.T) {
	_, err := ociTreeFiles([]byte("not a gzip stream"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "read the content layer")
}

// A FROM that names a registry does not fall through to the filesystem: a build
// whose registry is unreachable must say so, not report a missing directory.
func TestFromAnOCIRefIsNotTreatedAsADirectory(t *testing.T) {
	sf, err := Parse([]byte("FROM 127.0.0.1:1/demo/agent-skills/pdf:1.2.0\n"))
	require.NoError(t, err)

	_, _, err = Build(sf, t.TempDir(), nil, WithPlainHTTP(true))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "127.0.0.1:1/demo/agent-skills/pdf:1.2.0")
	assert.NotContains(t, err.Error(), "no such file or directory")
}

// SPEC.md 8.3: an OCI FROM has to name a manifest to pin, and a bare repository
// names none.
func TestFromAnOCIRefWithoutATagFails(t *testing.T) {
	sf, err := Parse([]byte("FROM ghcr.io/o/agent-skills/pdf\n"))
	require.NoError(t, err)

	_, _, err = Build(sf, t.TempDir(), nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "needs a tag or a digest")
}
