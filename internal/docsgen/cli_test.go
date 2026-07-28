package main

import (
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gaarutyunov/epos/internal/cli"
	"github.com/gaarutyunov/epos/internal/store"
)

// TestEveryCommandHasASection is the coverage the issue asks for, checked the
// only way that survives a command being added: against the tree itself rather
// than a list written here, which would go stale the same day the page would.
func TestEveryCommandHasASection(t *testing.T) {
	out := string(renderCLI())

	for _, cmd := range walk(cli.NewRootCommand(docsVersion)) {
		path := cmd.CommandPath()
		assert.Contains(t, out, `<section id="`+anchor(cmd)+`">`, "%s has no section", path)
		assert.Contains(t, out, `<h2><code>`+escape(path)+`</code></h2>`,
			"%s has no heading", path)
		assert.Contains(t, out, `<pre class="syntax">`+escape(cmd.UseLine())+`</pre>`,
			"%s has no usage line", path)
		assert.Contains(t, out, escape(cmd.Short), "%s has no summary", path)
		assert.Contains(t, out, `<a href="#`+anchor(cmd)+`">`, "%s is not in the contents", path)
	}
}

// TestEveryFlagIsDocumented is the other half of the checklist item: every flag
// of every command, with the type it takes and the default it carries.
func TestEveryFlagIsDocumented(t *testing.T) {
	out := string(renderCLI())

	for _, cmd := range walk(root()) {
		cmd.LocalFlags().VisitAll(func(f *pflag.Flag) {
			term, usage := flagEntry(f)
			assert.Contains(t, out, `<dt><code>`+escape(term)+`</code></dt>`,
				"%s has no entry for --%s", cmd.CommandPath(), f.Name)
			assert.Contains(t, out, `<dd>`+escape(usage)+`</dd>`,
				"%s documents --%s without its usage", cmd.CommandPath(), f.Name)
		})
	}
}

// TestSectionCountMatchesTheTree catches the failure the two tests above cannot:
// a section for a command that no longer exists. Contains-assertions only ever
// notice what is missing.
func TestSectionCountMatchesTheTree(t *testing.T) {
	out := string(renderCLI())

	// The two sections that are not commands.
	want := len(walk(cli.NewRootCommand(docsVersion))) + 2
	assert.Equal(t, want, strings.Count(out, `  <section id="`))
}

// TestAnchorsAreUnique is why the anchor is the whole command path: `epos ls`
// and `epos store ls` are different commands with the same name, and two
// sections sharing an id would send half the contents to the wrong place.
func TestAnchorsAreUnique(t *testing.T) {
	seen := map[string]bool{}
	for _, cmd := range walk(cli.NewRootCommand(docsVersion)) {
		id := anchor(cmd)
		assert.False(t, seen[id], "%s reuses the anchor %q", cmd.CommandPath(), id)
		seen[id] = true
	}
	assert.True(t, seen["epos-ls"] && seen["epos-store-ls"],
		"the two ls commands are the case this protects; both should be present")
}

// TestEveryInPageLinkResolves pins the hand-written cross-links inside the page
// — the ones to `epos store path` — against the generated ids they point at, so
// renaming a command breaks the test rather than the page.
func TestEveryInPageLinkResolves(t *testing.T) {
	out := string(renderCLI())

	ids := map[string]bool{}
	for _, m := range regexp.MustCompile(`id="([^"]+)"`).FindAllStringSubmatch(out, -1) {
		ids[m[1]] = true
	}
	for _, m := range regexp.MustCompile(`href="#([^"]+)"`).FindAllStringSubmatch(out, -1) {
		assert.True(t, ids[m[1]], "the page links to #%s, which nothing on it defines", m[1])
	}
}

// TestTheOtherPagesAreLinked is the cross-linking the checklist asks for: the
// reference is reachable from, and reaches, the quick start and the Skillfile
// reference.
func TestTheOtherPagesAreLinked(t *testing.T) {
	out := string(renderCLI())

	assert.Contains(t, out, `href={href("quickstart")}`)
	assert.Contains(t, out, `href={href("skillfile")}`)
}

// TestWithdrawnCommandsAreAbsent pins the scope correction. push was withdrawn
// with the write path (SPEC.md 4.5, 5.4), and a reference that documented it
// would be describing a command the binary does not have.
func TestWithdrawnCommandsAreAbsent(t *testing.T) {
	out := string(renderCLI())

	assert.NotContains(t, out, `<section id="epos-push">`)
	assert.NotContains(t, out, `<h2><code>epos push</code></h2>`)
	// Absence is not enough on its own: a reader who came looking for it needs
	// to be told what to do instead.
	assert.Contains(t, out, `<section id="publishing">`)
}

// TestEposHomeIsDocumented pins the checklist item a walk of the command tree
// cannot reach on its own: EPOS_HOME is not a flag, so nothing in cobra knows
// about it.
func TestEposHomeIsDocumented(t *testing.T) {
	out := string(renderCLI())

	assert.Contains(t, out, `<section id="environment">`)
	assert.Contains(t, out, store.RootEnv)
}

// TestTheDocumentedRootResolutionIsWhatHappens is what keeps the one section
// with hand-written prose honest. The order the page states is asserted against
// the resolution itself, so prose and behaviour cannot part company silently.
func TestTheDocumentedRootResolutionIsWhatHappens(t *testing.T) {
	fromEnv := t.TempDir()
	t.Setenv(store.RootEnv, fromEnv)

	got, err := store.Root("")
	require.NoError(t, err)
	assert.Equal(t, fromEnv, got, "EPOS_HOME should beat the home directory")

	explicit := filepath.Join(t.TempDir(), "elsewhere")
	got, err = store.Root(explicit)
	require.NoError(t, err)
	assert.Equal(t, explicit, got, "an explicit root should beat EPOS_HOME")

	t.Setenv(store.RootEnv, "")
	got, err = store.Root("")
	require.NoError(t, err)
	assert.True(t, strings.HasSuffix(got, ".epos"),
		"with nothing set the root should be the documented default, got %q", got)
}

// TestParagraphsUnwrapsTerminalLineBreaks pins the reading of a cobra Long:
// hard wrapping is for a terminal, and a blank line is the only break that
// means anything.
func TestParagraphsUnwrapsTerminalLineBreaks(t *testing.T) {
	assert.Equal(t, []string{"one long sentence wrapped for a terminal", "and a second paragraph"},
		paragraphs("one long sentence\nwrapped for a terminal\n\nand a second paragraph\n"))
	assert.Empty(t, paragraphs(""))
}

// TestFlagEntryMatchesHelpOutput pins the rendering against the shapes pflag's
// own usage printer produces: no type for a bool, a quoted default for a
// string, a bare one otherwise, and nothing at all for a zero value.
func TestFlagEntryMatchesHelpOutput(t *testing.T) {
	set := pflag.NewFlagSet("epos", pflag.ContinueOnError)
	set.StringP("tag", "t", "", "tag as <name>:<version>")
	set.String("key", "cosign.key", "private key to sign with")
	set.Bool("plain-http", false, "talk to the registry over HTTP")
	set.StringArray("set", nil, "set one value")
	set.Duration("timeout", 5, "how long to wait")

	for _, tc := range []struct {
		flag  string
		term  string
		usage string
	}{
		{"tag", "-t, --tag string", "tag as <name>:<version>"},
		{"key", "--key string", `private key to sign with (default "cosign.key")`},
		{"plain-http", "--plain-http", "talk to the registry over HTTP"},
		{"set", "--set stringArray", "set one value"},
		{"timeout", "--timeout duration", "how long to wait (default 5ns)"},
	} {
		t.Run(tc.flag, func(t *testing.T) {
			term, usage := flagEntry(set.Lookup(tc.flag))
			assert.Equal(t, tc.term, term)
			assert.Equal(t, tc.usage, usage)
		})
	}
}

// TestWalkLeavesCobrasOwnCommandsOut keeps `help` and `completion` off the
// page: they are cobra's, generated for every binary built on it, and
// documenting them would say more about the framework than about epos.
func TestWalkLeavesCobrasOwnCommandsOut(t *testing.T) {
	cmd := cli.NewRootCommand(docsVersion)
	cmd.InitDefaultHelpCmd()
	cmd.InitDefaultCompletionCmd()
	cmd.AddCommand(&cobra.Command{Use: "secret", Hidden: true})

	for _, c := range walk(cmd) {
		assert.NotContains(t, []string{"help", "completion", "secret"}, c.Name())
	}
}
