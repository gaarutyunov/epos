package registry

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"sort"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// These are the guards SPEC.md 2.5 puts on somebody else's artifact. They moved
// here with the routine they cover: a Skillfile FROM names a registry the author
// does not control, and a catalog points at arbitrary registries by definition,
// so both read untrusted layers through this one implementation.

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

// paths returns the unpacked file paths, sorted.
func paths(c Content) []string {
	out := make([]string, 0, len(c.Files))
	for p := range c.Files {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
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

// SPEC.md 2.1: the layer is rooted at `<skill-name>/`, and a base enters the
// stage as the skill it is rather than as a directory named after it.
func TestUnpackContentStripsTheSkillRoot(t *testing.T) {
	content, err := UnpackContent(layerOf(t,
		tarEntry{name: "pdf/", typeflag: tar.TypeDir},
		reg("pdf/SKILL.md", "---\nname: pdf\n---\n"),
		tarEntry{name: "pdf/references/", typeflag: tar.TypeDir},
		reg("pdf/references/style.md", "House style.\n"),
	))
	require.NoError(t, err)

	assert.Equal(t, "pdf", content.Root)
	assert.Equal(t, []string{"SKILL.md", "references/style.md"}, paths(content))
	assert.Equal(t, "---\nname: pdf\n---\n", string(content.Files["SKILL.md"]))
}

// SPEC.md 2.5 is deliberately permissive, and B2 is where that matters most: a
// third-party base can carry a name the consumer deriving from it cannot fix,
// and every one of these is legal on Linux and accepted by every other tool.
// Rejecting them at build would refuse a base that `oras pull` accepts.
func TestUnpackContentAcceptsPathsTheConsumerCannotFix(t *testing.T) {
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

	content, err := UnpackContent(layerOf(t, entries...))
	require.NoError(t, err, "2.5 must not reject a base over a portability question")

	for _, p := range awkward {
		body, ok := content.Files[p]
		assert.True(t, ok, "%s did not survive into the stage", p)
		assert.Equal(t, "content of "+p+"\n", string(body))
	}
}

// The other half of 2.5: what it does reject is rejected, because each of these
// is a write outside the skill root rather than a portability question.
func TestUnpackContentRejectsWhatEscapesTheSkillRoot(t *testing.T) {
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
			_, err := UnpackContent(layerOf(t,
				reg("pdf/SKILL.md", "---\nname: pdf\n---\n"), tc.entry))
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
		})
	}
}

// An absolute entry name never reaches the skill-root strip: it has no root to
// strip, and accepting it would put the base's files outside the artifact.
func TestUnpackContentRejectsALayerWithNoSkillRoot(t *testing.T) {
	_, err := UnpackContent(layerOf(t, reg("SKILL.md", "---\nname: pdf\n---\n")))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not rooted at a skill directory")
}

// Two roots would mean the artifact holds two skills, and stripping either one
// would silently merge them (2.1).
func TestUnpackContentRejectsTwoRoots(t *testing.T) {
	_, err := UnpackContent(layerOf(t,
		reg("pdf/SKILL.md", "---\nname: pdf\n---\n"),
		reg("other/SKILL.md", "---\nname: other\n---\n"),
	))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "rooted at both")
}

func TestUnpackContentRejectsAnEmptyLayer(t *testing.T) {
	_, err := UnpackContent(layerOf(t, tarEntry{name: "pdf/", typeflag: tar.TypeDir}))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "holds no files")
}

func TestUnpackContentRejectsSomethingThatIsNotATarGzip(t *testing.T) {
	_, err := UnpackContent([]byte("not a gzip stream"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "read the content layer")
}
