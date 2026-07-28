package skillfile

import "slices"

// This file is the Skillfile instruction table.
//
// It is a table rather than a switch because SPEC.md 14.1 requires the
// reference page to be generated from "the same source as the Skillfile
// instruction table", so it cannot drift from the parser. Two readers, one
// source: build.go's apply dispatches through instructionByOp, and
// internal/docsgen renders the exported half of the same rows. An instruction
// the builder understands but this table does not list cannot exist — apply
// would have nothing to call — and an instruction listed here without an
// example is one the reference page would document with an empty box.
//
// Every Example is executed by TestDocumentedExamplesBuild against the real
// builder, so a documented result that stopped being true fails the test suite
// rather than the reader.

// FlagDoc is one `--name=value` option of an instruction.
type FlagDoc struct {
	// Name is the flag without its leading dashes.
	Name string
	// Value names what the flag takes, for the syntax line.
	Value string
	// Summary is one sentence, with `code` spans allowed.
	Summary string
}

// ExampleFile is one file of an example: an input in the build context, or the
// output the example is about.
type ExampleFile struct {
	// Path is slash-separated and relative to the build context, or to the
	// skill root when the file is a result.
	Path string
	// Body is the file's exact contents.
	Body string
}

// Example is a worked example: a build context, a Skillfile, and what the
// build makes of them.
//
// Runnable by construction. The fields are exactly Build's inputs and outputs,
// so the test that executes every example needs no fixture of its own, and an
// example cannot describe behaviour the builder does not have.
type Example struct {
	// Context is the build context on disk. Paths are created relative to a
	// temporary directory.
	Context []ExampleFile
	// Skillfile is the recipe, verbatim.
	Skillfile string
	// Result is the built file the example is about. An empty Path asserts
	// nothing, for an example whose point is a removal.
	Result ExampleFile
	// Absent are paths the build must not have produced.
	Absent []string
	// Warnings are the build's warnings, exactly as Report records them —
	// without the `warning: ` prefix `epos build` prints them behind.
	Warnings []string
	// BuildArgs are the --build-arg values the example is run with.
	BuildArgs map[string]string
}

// InstructionDoc is one row of the instruction table: what the instruction is,
// and what the builder does with it.
type InstructionDoc struct {
	// Op is the instruction name, upper-cased, as Instruction.Op carries it.
	Op string
	// Syntax is the grammar line, in the notation SPEC.md 8.2 uses.
	Syntax string
	// Summary is one sentence.
	Summary string
	// Notes are the behaviours worth knowing before writing one, as
	// paragraphs. `code` spans are allowed.
	Notes []string
	// Flags are the instruction's options.
	Flags []FlagDoc
	// Example is a worked example, executed by the package's tests.
	Example Example

	// apply is the builder method this row dispatches to.
	//
	// Unexported so the table stays the only place an instruction is bound:
	// a caller outside the package reads the documentation and cannot invent
	// a row that the builder would then honour.
	apply func(*builder, Instruction) error
}

// SourceDoc is one scheme a FROM reference can be written in (SPEC.md 8.3).
type SourceDoc struct {
	// Scheme is the human name: Local, Git, OCI, Stage.
	Scheme string
	// Example is a reference written in that scheme.
	Example string
	// Pin is what the build records as the source's identity, or the reason
	// there is nothing to record.
	Pin string
	// Notes is one sentence about when to reach for it.
	Notes string
}

// TopicDoc is a section of the reference that is not one instruction.
type TopicDoc struct {
	// Title is the heading.
	Title string
	// Slug is the anchor, so a link into the page survives a re-render.
	Slug string
	// Body are the paragraphs, with `code` spans allowed.
	Body []string
	// Example is an optional worked example, executed like every other.
	Example *Example
}

// Reference is everything the generated reference page renders, in the order
// it renders it.
type Reference struct {
	// Syntax is the language-level prose that precedes any one instruction.
	Syntax []string
	// Sources are the FROM schemes of 8.3.
	Sources []SourceDoc
	// Instructions is the instruction table, in the order 8.2 lists it.
	Instructions []InstructionDoc
	// Topics are multi-stage composition and the values/templating model.
	Topics []TopicDoc
}

// NewReference returns the reference content.
//
// A copy, so a generator cannot mutate the table the builder dispatches
// through. slices.Clone is shallow, which is enough: every field below the top
// level is a string or a slice of strings the generator only reads.
func NewReference() Reference {
	return Reference{
		Syntax:       slices.Clone(syntaxNotes),
		Sources:      slices.Clone(fromSources),
		Instructions: slices.Clone(instructionTable),
		Topics:       slices.Clone(topics),
	}
}

// instructionByOp indexes the table for dispatch. Built once; nothing ranges
// it, so Go's randomised map order never reaches an output.
var instructionByOp = func() map[string]InstructionDoc {
	out := make(map[string]InstructionDoc, len(instructionTable))
	for _, doc := range instructionTable {
		out[doc.Op] = doc
	}
	return out
}()

// syntaxNotes are the rules that hold for every instruction.
var syntaxNotes = []string{
	"A Skillfile is read top to bottom, one instruction per line. " +
		"Instructions apply in file order, and when two of them touch the same bytes the later one wins.",
	"A line ending in `\\` continues on the next. A line whose first non-blank character is `#` is a comment; " +
		"a `#` anywhere else is ordinary text, so `git+https://host/o/r#v1.2.0` and a `# note` inside a regex both survive.",
	"Arguments are split on whitespace, and single or double quotes group an argument that contains spaces. " +
		"Quoting means nothing to any instruction except `SET`, where it forces the value to be a string.",
	"Flags are written `--name=value`. A bare `--name value` is rejected: " +
		"it would make `SET --file values.yaml model opus` ambiguous with a positional argument.",
	"`APPEND`, `PATCH` and `AWK` take their payload either inline as a heredoc — `<<EOF`, then the body, " +
		"then a line holding only `EOF` — or as the path of a file in the build context. " +
		"A heredoc body is taken verbatim: no comment stripping, no line joining, and no `$NAME` expansion, " +
		"so an AWK script's `$1` and a template's `{{ }}` both reach the artifact as written.",
	"`$NAME` and `${NAME}` in an argument expand to the value of an `ARG` of that name. " +
		"A name no `ARG` declared is left exactly as written, which is what keeps `REPLACE`'s `$1` submatch references intact.",
	"Nothing executes. There is no `RUN`, no `ENTRYPOINT` and no `CMD`, and a build is a pure function of " +
		"its bases, its Skillfile and its context — the same three inputs always produce the same digest.",
}

// fromSources is 8.3's table, plus the stage reference 8.4 adds to it.
var fromSources = []SourceDoc{
	{
		Scheme:  "Local",
		Example: "FROM ./skills/base",
		Pin:     "none",
		Notes: "A directory in the build context. There is no stable name for a directory on somebody's disk, " +
			"so the build records no pin for it.",
	},
	{
		Scheme:  "Git",
		Example: "FROM git+https://github.com/o/r#v1.2.0:skills/pdf",
		Pin:     "commit SHA + tree SHA",
		Notes: "The fragment carries the ref and, after a `:`, the subdirectory. " +
			"The ref may be a branch, a tag, a full `refs/…` name or a commit SHA; omit the fragment for the default branch. " +
			"An annotated tag is peeled to the commit it points at. `git+http://` reaches a server without TLS.",
	},
	{
		Scheme:  "OCI",
		Example: "FROM ghcr.io/o/agent-skills/pdf:1.2.0",
		Pin:     "manifest digest",
		Notes: "The only scheme that touches a registry. " +
			"A reference is told from a local directory the way Docker tells them apart: " +
			"what precedes the first slash is a registry only if it carries a dot or a port, or is `localhost`.",
	},
	{
		Scheme:  "Stage",
		Example: "FROM base",
		Pin:     "none",
		Notes: "A name a previous `FROM … AS base` bound. Checked before the filesystem, " +
			"so a directory that happens to share the name cannot shadow the stage.",
	},
}

// instructionTable is the instruction set: the documentation and the dispatch,
// written once.
//
// Ordered as SPEC.md 8.2 lists them, which is also the order a Skillfile
// tends to use them in. A slice rather than a map, so the generated page is
// byte-stable.
var instructionTable = []InstructionDoc{
	{
		Op:      "ARG",
		Syntax:  "ARG <name>[=<default>]",
		Summary: "Declare a build argument, optionally with a default.",
		Notes: []string{
			"`--build-arg <name>=<value>` on `epos build` wins over the default. " +
				"`--build-arg <name>=` sets it to the empty string, which is how a default is suppressed.",
			"Only declared names expand. `$1` in a `REPLACE` replacement, or a name nobody declared, " +
				"is left as written rather than blanked — a typo stays visible in the output instead of vanishing.",
			"Build arguments are the build-time substitution mechanism. " +
				"`{{ }}` is the install-time one and is never touched here (see Values and templating).",
		},
		Example: Example{
			Context: []ExampleFile{
				{Path: "base/SKILL.md", Body: "---\nname: reviewer\n---\nReviews changes.\n"},
			},
			Skillfile: "ARG language=Go\nFROM ./base\nSET language $language\n",
			Result: ExampleFile{
				Path: "SKILL.md",
				Body: "---\nname: reviewer\nlanguage: Go\n---\nReviews changes.\n",
			},
		},
		apply: (*builder).arg,
	},
	{
		Op:      "FROM",
		Syntax:  "FROM <ref> [AS <stage>]",
		Summary: "Start a stage from a base skill.",
		Notes: []string{
			"The base enters the stage at its root: `base/references/style.md` in the context becomes " +
				"`references/style.md` in the artifact, which is what every later instruction addresses.",
			"A Skillfile needs at least one `FROM`, and the last one is the stage that becomes the artifact. " +
				"Earlier stages are sources a `COPY --from` names.",
			"See FROM sources for the four things `<ref>` can be, and Multi-stage composition for `AS`.",
		},
		Example: Example{
			Context: []ExampleFile{
				{Path: "base/SKILL.md", Body: "---\nname: reviewer\n---\nReviews changes.\n"},
				{Path: "base/references/style.md", Body: "Two spaces after a full stop.\n"},
			},
			Skillfile: "FROM ./base\n",
			Result: ExampleFile{
				Path: "references/style.md",
				Body: "Two spaces after a full stop.\n",
			},
		},
		apply: (*builder).from,
	},
	{
		Op:      "COPY",
		Syntax:  "COPY [--from=<stage>] <src>... <dest>",
		Summary: "Copy files in from the build context, or from a finished stage.",
		Flags: []FlagDoc{
			{
				Name:  "from",
				Value: "<stage>",
				Summary: "Read the sources from the named stage's final tree instead of the build context. " +
					"The stage must already have finished.",
			},
		},
		Notes: []string{
			"A `<dest>` ending in `/`, or written as `.`, is a directory: the source keeps its base name under it. " +
				"Anything else is the destination path itself.",
			"A `<src>` naming no file is taken as a directory prefix, and everything under it is copied with its layout kept. " +
				"A source that matches neither a file nor a prefix fails the build.",
			"Composition is explicit enumeration, not merge-by-default: what you take, you name.",
		},
		Example: Example{
			Context: []ExampleFile{
				{Path: "base/SKILL.md", Body: "---\nname: reviewer\n---\nReviews changes.\n"},
				{Path: "checklist.md", Body: "- Table-driven tests for every exported function.\n"},
			},
			Skillfile: "FROM ./base\nCOPY checklist.md references/checklist.md\n",
			Result: ExampleFile{
				Path: "references/checklist.md",
				Body: "- Table-driven tests for every exported function.\n",
			},
		},
		apply: (*builder).copy,
	},
	{
		Op:      "RM",
		Syntax:  "RM <path>...",
		Summary: "Remove a file, or everything under a directory prefix.",
		Notes: []string{
			"**An absent path is fatal.** This is deliberately unlike the two instructions that warn: " +
				"a zero-match `REPLACE` and an absent-key `UNSET` end in the state the author asked for — the pattern is gone, " +
				"the key is gone — so they stay idempotent against a base that has already adopted the same change. " +
				"`RM` has no such reading: a path that is not there is a path the author was wrong about, " +
				"and continuing would ship an artifact built from a Skillfile that no longer describes its base.",
			"A directory is not a thing the tree holds, only files are, so `RM sections/` removes everything beneath it.",
		},
		Example: Example{
			Context: []ExampleFile{
				{Path: "base/SKILL.md", Body: "---\nname: reviewer\n---\nReviews changes.\n"},
				{Path: "base/sections/house-style.md", Body: "Oxford comma, always.\n"},
				{Path: "base/sections/checklist.md", Body: "- One assertion per test.\n"},
			},
			Skillfile: "FROM ./base\nRM sections/house-style.md\n",
			Result: ExampleFile{
				Path: "sections/checklist.md",
				Body: "- One assertion per test.\n",
			},
			Absent: []string{"sections/house-style.md"},
		},
		apply: (*builder).rm,
	},
	{
		Op:      "APPEND",
		Syntax:  "APPEND <path> (<<EOF … EOF | <file>)",
		Summary: "Add text to the end of a file, creating it if it is not there.",
		Notes: []string{
			"If the file does not end in a newline, one is added before the payload, " +
				"so an append never joins itself onto the last line of the base.",
			"The payload is verbatim (SPEC.md 8.6): a `{{ }}` in it reaches the artifact untouched and renders at install.",
		},
		Example: Example{
			Context: []ExampleFile{
				{Path: "base/SKILL.md", Body: "---\nname: reviewer\n---\nReviews changes.\n"},
			},
			Skillfile: "FROM ./base\nAPPEND SKILL.md <<EOF\nSee references/style.md for the house style.\nEOF\n",
			Result: ExampleFile{
				Path: "SKILL.md",
				Body: "---\nname: reviewer\n---\nReviews changes.\nSee references/style.md for the house style.\n",
			},
		},
		apply: (*builder).append,
	},
	{
		Op:      "REPLACE",
		Syntax:  "REPLACE [--count=<n>] <path> <pattern> <replacement>",
		Summary: "Rewrite a file with a regular expression.",
		Flags: []FlagDoc{
			{
				Name:  "count",
				Value: "<n>",
				Summary: "Apply to the first `n` matches only. Positional, so an upstream insertion of an earlier match " +
					"silently retargets the edit.",
			},
		},
		Notes: []string{
			"The engine is Go's `regexp`, which is RE2: no backreferences, no lookahead or lookbehind, " +
				"and a linear-time guarantee, so no pattern from a third-party base can hang a build.",
			"The replacement uses Go's `$1` and `${name}` expansion.",
			"**Zero matches is not an error.** A warning is emitted, the file is left alone, and the build continues — " +
				"which is what makes a defensive edit survive a base that has already adopted the same change. " +
				"`epos build` prints every no-op on stderr, so a Skillfile that has quietly stopped doing anything is still visible.",
			"`REPLACE` is the instruction for edits that must survive line drift. `PATCH` is the one that fails on it.",
		},
		Example: Example{
			Context: []ExampleFile{
				{Path: "base/SKILL.md", Body: "---\nname: reviewer\nmodel: sonnet\n---\nReviews changes.\n"},
			},
			Skillfile: "FROM ./base\n" +
				"REPLACE SKILL.md \"model: (sonnet|haiku)\" \"model: opus # was $1\"\n" +
				"REPLACE SKILL.md \"model: gpt-4\" \"model: opus\"\n",
			Result: ExampleFile{
				Path: "SKILL.md",
				Body: "---\nname: reviewer\nmodel: opus # was sonnet\n---\nReviews changes.\n",
			},
			Warnings: []string{`line 3: SKILL.md: "model: gpt-4" matched nothing`},
		},
		apply: (*builder).replace,
	},
	{
		Op:      "PATCH",
		Syntax:  "PATCH <path> (<<EOF … EOF | <diff-file>)",
		Summary: "Apply a unified diff to a file.",
		Notes: []string{
			"**Strict, and stricter than `git apply`.** Each hunk is applied at the line its header records. " +
				"There is no offset search and no fuzz factor, so a pure line-number shift caused by an unrelated " +
				"upstream insertion fails the build even when every context line still matches.",
			"**Failure is fatal.** No `.rej` file, no warn-and-continue, no partial application: " +
				"the artifact is content-addressed, so half a patch would silently produce a different digest from the same inputs.",
			"The payload is authored with `git diff`. `git show`, `format-patch`, GNU unified diffs and Git binary patches " +
				"are all accepted. It must describe exactly one file — a payload that patches several, or that is not a diff at all, " +
				"is refused rather than quietly doing nothing.",
			"Use `REPLACE` for an edit that has to survive the base moving under it.",
		},
		Example: Example{
			Context: []ExampleFile{
				{Path: "base/SKILL.md", Body: "---\nname: reviewer\n---\nReviews changes.\n"},
				{Path: "base/notes.md", Body: "alpha\nbeta\ngamma\n"},
			},
			Skillfile: "FROM ./base\n" +
				"PATCH notes.md <<EOF\n" +
				"--- a/notes.md\n" +
				"+++ b/notes.md\n" +
				"@@ -1,3 +1,3 @@\n" +
				" alpha\n" +
				"-beta\n" +
				"+BETA\n" +
				" gamma\n" +
				"EOF\n",
			Result: ExampleFile{Path: "notes.md", Body: "alpha\nBETA\ngamma\n"},
		},
		apply: (*builder).patch,
	},
	{
		Op:      "AWK",
		Syntax:  "AWK [--timeout=<duration>] <path> (<<EOF … EOF | <script-file>)",
		Summary: "Filter a file through a sandboxed AWK program.",
		Flags: []FlagDoc{
			{
				Name:    "timeout",
				Value:   "<duration>",
				Summary: "How long the program may run, as a Go duration such as `2s`. Defaults to `10s`.",
			},
		},
		Notes: []string{
			"The file's current content is the program's stdin, and its stdout replaces the file. " +
				"This is what `REPLACE` cannot do: multi-line, conditional and section-scoped edits.",
			"**The sandbox is mandatory and not configurable.** `NoExec`, `NoFileWrites`, `NoFileReads` and an empty " +
				"environment are all set, so the program is a pure stdin-to-stdout function — " +
				"it cannot spawn a process, touch the filesystem, or read the environment. " +
				"That is what keeps `AWK` compatible with the no-`RUN` rule.",
			"**`rand()`, `srand()` and `systime()` are rejected.** They would make the output digest vary between builds " +
				"of identical inputs. The check reads the compiled program, not the script text, " +
				"so the same words inside a string literal or a regex are left alone.",
			"AWK is Turing-complete, so execution is bound to a deadline. Exceeding it fails the build, " +
				"as do parse errors, runtime errors and a non-zero exit. There is no partial application.",
			"Output is LF-terminated whatever the input used, on every platform: a build must not produce " +
				"a different digest depending on the machine that ran it. A CR the script asks for by name still survives.",
		},
		Example: Example{
			Context: []ExampleFile{
				{
					Path: "base/SKILL.md",
					Body: "---\nname: reviewer\n---\n## Checklist\n- One assertion per test.\n## House style\n- Oxford comma, always.\n",
				},
			},
			Skillfile: "FROM ./base\n" +
				"AWK SKILL.md <<EOF\n" +
				"BEGIN { keep = 1 }\n" +
				"/^## House style/ { keep = 0 }\n" +
				"keep { print }\n" +
				"EOF\n",
			Result: ExampleFile{
				Path: "SKILL.md",
				Body: "---\nname: reviewer\n---\n## Checklist\n- One assertion per test.\n",
			},
		},
		apply: (*builder).awk,
	},
	{
		Op:      "SET",
		Syntax:  "SET [--file=<path>] <key> <value>",
		Summary: "Write a YAML key, structure-aware.",
		Flags: []FlagDoc{
			{
				Name:    "file",
				Value:   "<path>",
				Summary: "Edit this YAML file instead of `SKILL.md`'s frontmatter.",
			},
		},
		Notes: []string{
			"The default target is the YAML frontmatter block of `SKILL.md` — the block that holds `name`, " +
				"`description` and `allowed-tools`, the fields that decide whether an agent loads the skill at all. " +
				"`--file=<path>` targets any YAML file in the tree.",
			"Keys use dotted paths for nested mappings, and an intermediate key that is missing is written along with the leaf.",
			"**Quoting forces a string.** `SET version 1.2` writes the float `1.2`; `SET version \"1.2\"` writes the string `\"1.2\"`. " +
				"An unquoted value is parsed as a YAML scalar, so it gets the type it looks like.",
			"The edit goes through the document's syntax tree, so it cannot produce invalid YAML, " +
				"and key order, comments and the quoting style of every key it did not name survive it. " +
				"An existing key keeps its place and its trailing comment; a new key is appended. " +
				"Files no instruction targeted are never re-serialised and stay byte-identical.",
			"One known deviation: inline comment whitespace is normalised across the edited block, " +
				"so `- Read      # note` comes back as `- Read # note`.",
		},
		Example: Example{
			Context: []ExampleFile{
				{
					Path: "base/SKILL.md",
					Body: "---\n# The name an agent loads this skill by.\nname: reviewer\n" +
						"description: 'Reviews changes'\nversion: 1.0 # bumped by hand\n---\nReviews changes.\n",
				},
			},
			Skillfile: "FROM ./base\nSET version \"1.2\"\nSET metadata.author acme\n",
			Result: ExampleFile{
				Path: "SKILL.md",
				Body: "---\n# The name an agent loads this skill by.\nname: reviewer\n" +
					"description: 'Reviews changes'\nversion: \"1.2\" # bumped by hand\nmetadata:\n  author: acme\n" +
					"---\nReviews changes.\n",
			},
		},
		apply: (*builder).setKey,
	},
	{
		Op:      "UNSET",
		Syntax:  "UNSET [--file=<path>] <key>",
		Summary: "Remove a YAML key.",
		Flags: []FlagDoc{
			{
				Name:    "file",
				Value:   "<path>",
				Summary: "Edit this YAML file instead of `SKILL.md`'s frontmatter.",
			},
		},
		Notes: []string{
			"**An absent key warns and continues**, for the same reason a zero-match `REPLACE` does: " +
				"the end state is the one the author asked for, so the edit stays idempotent against a base " +
				"that has already dropped the key. `epos build` prints the warning on stderr.",
			"Only the key's own entry goes. A comment on a line of its own belongs to the key it sits above " +
				"and leaves with it; every other comment stays attached to the key it was written against.",
		},
		Example: Example{
			Context: []ExampleFile{
				{
					Path: "base/SKILL.md",
					Body: "---\nname: reviewer\nallowed-tools: Read, Grep\n---\nReviews changes.\n",
				},
			},
			Skillfile: "FROM ./base\nUNSET allowed-tools\nUNSET license\n",
			Result: ExampleFile{
				Path: "SKILL.md",
				Body: "---\nname: reviewer\n---\nReviews changes.\n",
			},
			Warnings: []string{`line 3: SKILL.md: key "license" was already absent`},
		},
		apply: (*builder).unsetKey,
	},
}

// topics are the sections that are not one instruction.
var topics = []TopicDoc{
	{
		Title: "Multi-stage composition",
		Slug:  "multi-stage",
		Body: []string{
			"Multi-stage follows Docker's semantics. `FROM <ref> AS <stage>` binds a name to the stage it starts, " +
				"`COPY --from=<stage>` takes named files out of a stage that has finished, and a bare `FROM <stage>` " +
				"continues from one.",
			"A stage finishes when the next `FROM` begins, so `COPY --from` reads the stage's **final** tree — " +
				"including everything the stage's own instructions did to it after its `FROM` line. " +
				"A stage that has not finished yet cannot be read: naming a stage declared further down, " +
				"or the stage currently being built, is an error rather than a silent miss.",
			"`FROM <stage>` takes a copy. Instructions in the derived stage cannot reach back and change the stage " +
				"they descended from, so a later `COPY --from` naming it still sees what it was.",
			"The **last** stage is the artifact. Earlier stages are sources, however much their own instructions edited them.",
			"Stage names are also the values-scope keys at install time: a file a `COPY --from=shared` brought in " +
				"renders against `.Values` scoped to `shared`, so two stages can both write `{{ .Values.title }}` " +
				"and mean two different things.",
		},
		Example: &Example{
			Context: []ExampleFile{
				{Path: "pdf/SKILL.md", Body: "---\nname: pdf\n---\nExtracts tables from PDFs.\n"},
				{Path: "shared/reference.md", Body: "Two spaces after a full stop.\n"},
			},
			Skillfile: "FROM ./shared AS shared\n\n" +
				"FROM ./pdf AS base\n" +
				"APPEND SKILL.md <<EOF\n" +
				"See references/shared.md for the house style.\n" +
				"EOF\n\n" +
				"FROM base\n" +
				"COPY --from=shared reference.md references/shared.md\n",
			Result: ExampleFile{
				Path: "SKILL.md",
				Body: "---\nname: pdf\n---\nExtracts tables from PDFs.\n" +
					"See references/shared.md for the house style.\n",
			},
		},
	},
	{
		Title: "Values and templating",
		Slug:  "values-and-templating",
		Body: []string{
			"A `{{ … }}` action is **not** a build-time construct. It passes through the build untouched and is rendered " +
				"only when the skill is installed, by `epos install`. Build-time substitution is `ARG` and `$NAME`; " +
				"the two never collide.",
			"Every payload is carried verbatim, so a `{{ }}` inside an `APPEND` heredoc, a `COPY`'d file, " +
				"a `PATCH` or an `AWK` script reaches the artifact as written.",
			"Rendering is Go `text/template` with **no custom functions**, against `.Values` and nothing else. " +
				"Values come from `epos install -f values.yaml` and `--set k=v`, with later files winning key by key " +
				"and `--set` winning over every file. A file with no `{{` in it is copied through byte for byte, " +
				"which is also what keeps binary assets out of the parser. A value a template needs and the user did not supply " +
				"is an error, not an empty string.",
			"Scoping follows Helm. The top level of `values.yaml` is the skill's own scope; a key named after a Skillfile stage " +
				"is that stage's scope, seen only by the files that stage contributed; and a `global` block is visible to all of them.",
			"**Quote a template in a frontmatter value.** YAML reads a bare `{` as the start of a flow mapping, " +
				"so `model: {{ .Values.model }}` is a YAML syntax error and the build fails when it reads the frontmatter. " +
				"Write `model: '{{ .Values.model }}'`. In the Markdown body below the frontmatter, no quoting is needed.",
		},
		Example: &Example{
			Context: []ExampleFile{
				{
					Path: "base/SKILL.md",
					Body: "---\nname: reviewer\nmodel: '{{ .Values.model }}'\n---\nReviews {{ .Values.language }} changes.\n",
				},
			},
			Skillfile: "FROM ./base\nAPPEND SKILL.md <<EOF\nHouse style: {{ .Values.style }}\nEOF\n",
			Result: ExampleFile{
				Path: "SKILL.md",
				Body: "---\nname: reviewer\nmodel: '{{ .Values.model }}'\n---\n" +
					"Reviews {{ .Values.language }} changes.\nHouse style: {{ .Values.style }}\n",
			},
		},
	},
}
