package skillfile

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/benhoyt/goawk/interp"
	"github.com/benhoyt/goawk/parser"
	"github.com/bluekeyes/go-gitdiff/gitdiff"
)

// Build runs a Skillfile against a context directory and returns the final
// stage's tree.
//
// Pure by construction (SPEC.md 8.1): the only inputs are the bases, the
// Skillfile and the context, and nothing executes.
func Build(sf *Skillfile, contextDir string, buildArgs map[string]string,
	opts ...Option) (*Tree, *Report, error) {
	b := newBuilder(sf, contextDir, buildArgs, opts...)

	for _, inst := range sf.Instructions {
		if err := b.apply(inst); err != nil {
			return nil, nil, fmt.Errorf("line %d: %s: %w", inst.Line, inst.Op, err)
		}
	}

	if b.current == nil {
		return nil, nil, fmt.Errorf("no FROM instruction in the Skillfile")
	}
	b.report.Stages = b.finalOrigins()
	return b.current, b.report, nil
}

// finalOrigins is the provenance of the tree that actually shipped.
//
// Pruned against the tree rather than returned as it stands: a path can be
// copied from a stage and then removed by a later instruction that is not RM
// — a COPY over a directory prefix, say — and provenance for a file the
// artifact does not contain would be a claim about nothing.
//
// nil rather than an empty map when there is nothing to say, so a caller can
// tell "no stage contributed anything" without inspecting the length.
func (b *builder) finalOrigins() map[string]string {
	out := map[string]string{}
	for p, stage := range b.origins {
		if _, ok := b.current.Get(p); ok {
			out[p] = stage
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// Report is what the build wants to say afterwards.
type Report struct {
	// NoOpReplaces are REPLACE instructions that matched nothing. 8.2.2 makes
	// zero matches a warning rather than an error, so idempotent and defensive
	// edits are expressible — but a silent no-op is how a Skillfile rots
	// against a moving base, so they are counted and reported.
	NoOpReplaces []string
	// MissingUnsets are UNSET instructions whose key was already absent.
	MissingUnsets []string
	// GitBases are the pins of the git bases this build resolved, in
	// instruction order (8.3). A ref like `main` or `v1.2.0` is mutable, so the
	// commit and tree SHAs it resolved to are the only record of what was
	// actually built from — kept here so a later rebuild can be checked against
	// them and so provenance has something to state.
	GitBases []GitBase
	// OCIBases are the pins of the OCI bases this build resolved, in
	// instruction order (8.3). A tag is mutable — `1.2.0` can be re-pushed over
	// different content — so the manifest digest it resolved to is the only
	// record of what was actually built from, and the thing a later rebuild is
	// checked against.
	OCIBases []OCIBase
	// Stages maps a file in the built tree to the stage that contributed it,
	// for the files an explicit COPY --from named. 8.4 makes stage names the
	// values-scope keys at install time (10.3), and this is what carries them
	// past the build: without it the installer has one flat tree and no way to
	// tell two stages' `{{ .Values.title }}` apart.
	//
	// Only the final stage's imports appear. The artifact *is* the final
	// stage, so its own files are the top-level scope — the way a Helm chart's
	// own values are top level and a subchart's are nested under its name —
	// and a stage a COPY --from named is the analogue of that subchart.
	//
	// Empty for a single-stage build, which is every build that never says
	// COPY --from.
	Stages map[string]string
	// Base is the FROM the *final* stage started from — the one 2.3 records as
	// the artifact's base. A multi-stage Skillfile declares several, but only
	// the last FROM is what the result descends from; the earlier stages are
	// sources a COPY --from names, and naming one of them as the base would
	// misreport the lineage.
	Base Base
}

// Base is a resolved FROM: the reference as written, and the pin it resolved
// to.
type Base struct {
	// Ref is the reference exactly as the Skillfile wrote it, after ARG
	// expansion.
	Ref string
	// Digest is the pin: the manifest digest for an OCI base, and
	// `<commit>+<tree>` for a git one — the two SHAs 8.3 makes the pin of that
	// scheme. Empty for a local base, which 8.3 gives no pin at all: there is no
	// stable name for a directory on somebody's disk, and inventing one would
	// claim more than the build can know.
	Digest string
}

// Option configures a build.
//
// Variadic rather than a parameter, because everything a build needs to be
// pure — the bases, the Skillfile, the context (8.1) — is already an argument,
// and what is left is transport detail that the great majority of builds have
// no opinion about.
type Option func(*builder)

// WithPlainHTTP resolves OCI bases over HTTP rather than TLS.
//
// The same escape hatch `pull` and `verify` carry, and for the same reason: a
// registry on localhost — a test's, a mirror on a developer's machine — serves
// no certificate, and a build that could not reach one would make 8.3's OCI
// scheme untestable against a real registry.
func WithPlainHTTP(plain bool) Option {
	return func(b *builder) { b.plainHTTP = plain }
}

type builder struct {
	contextDir string
	args       map[string]string
	stages     map[string]*Tree
	// declared is every name the Skillfile binds with `FROM … AS <name>`,
	// including the ones no FROM has reached yet. Knowing them up front is what
	// lets a forward reference say so, instead of falling through to the
	// filesystem and reporting a missing directory for a stage that is right
	// there, three lines further down.
	declared map[string]bool
	current  *Tree
	// stage is the name the tree in current was declared under, empty while an
	// anonymous stage is being built. A stage is only entered into stages once
	// its instructions are over (see seal), so this is where its name waits.
	stage string
	// origins is Report.Stages under construction: the destination path of
	// every COPY --from in the stage being built now, mapped to the stage it
	// named. Reset by each FROM, because a FROM starts a stage whose inherited
	// content is its own — `FROM base` is a continuation, not an import, and
	// only the imports of the stage that survives are scopes.
	origins map[string]string
	report  *Report
	// plainHTTP resolves OCI bases over HTTP. See WithPlainHTTP.
	plainHTTP bool
}

// newBuilder prepares a build of sf.
func newBuilder(sf *Skillfile, contextDir string, buildArgs map[string]string,
	opts ...Option) *builder {
	b := &builder{
		contextDir: contextDir,
		args:       map[string]string{},
		stages:     map[string]*Tree{},
		declared:   map[string]bool{},
		origins:    map[string]string{},
		report:     &Report{},
	}
	for _, opt := range opts {
		opt(b)
	}
	for k, v := range buildArgs {
		b.args[k] = v
	}
	for _, inst := range sf.Instructions {
		if inst.Op != "FROM" || len(inst.Args) != 3 || !strings.EqualFold(inst.Args[1], "AS") {
			continue
		}
		b.declared[inst.Args[2]] = true
	}
	return b
}

func (b *builder) apply(inst Instruction) error {
	switch inst.Op {
	case "ARG":
		return b.arg(inst)
	case "FROM":
		return b.from(inst)
	case "COPY":
		return b.copy(inst)
	case "RM":
		return b.rm(inst)
	case "APPEND":
		return b.append(inst)
	case "REPLACE":
		return b.replace(inst)
	case "SET":
		return b.setKey(inst)
	case "UNSET":
		return b.unsetKey(inst)
	case "PATCH":
		return b.patch(inst)
	case "AWK":
		return b.awk(inst)
	default:
		return fmt.Errorf("unknown instruction")
	}
}

// arg declares a build argument, with an optional default.
//
// A value passed with --build-arg wins over the default, which is why the
// default is only applied when the name is unset.
func (b *builder) arg(inst Instruction) error {
	if len(inst.Args) != 1 {
		return fmt.Errorf("want <name>[=<default>]")
	}
	name, def, hasDefault := strings.Cut(inst.Args[0], "=")
	if name == "" {
		return fmt.Errorf("want <name>[=<default>]")
	}
	if _, given := b.args[name]; !given && hasDefault {
		b.args[name] = def
	}
	if _, known := b.args[name]; !known {
		b.args[name] = ""
	}
	return nil
}

// expand substitutes $NAME and ${NAME} from the build args.
//
// 8.6: build-time substitution is $VAR, never {{ }} — a SKILL.md containing
// {{ .Values.model }} must reach install untouched, so nothing here looks at
// braces.
//
// Only *declared* args are substituted; anything else is left as written.
// That is what keeps REPLACE working: `$1` in a replacement is a submatch
// reference, and expanding it here would blank it before the regex ever saw
// it. Leaving unknown names alone also means a typo'd `$lnag` survives into
// the output where it is visible, rather than silently becoming "".
func (b *builder) expand(s string) string {
	return os.Expand(s, func(name string) string {
		if v, ok := b.args[name]; ok {
			return v
		}
		// Put the reference back exactly as os.Expand found it. The braced
		// form cannot be distinguished from the bare one here, and ${name} is
		// the form a named capture group uses, so it is the safer default.
		return "${" + name + "}"
	})
}

func (b *builder) from(inst Instruction) error {
	args := inst.Args
	var stage string
	if len(args) == 3 && strings.EqualFold(args[1], "AS") {
		stage, args = args[2], args[:1]
	}
	if len(args) != 1 {
		return fmt.Errorf("want <ref> [AS <stage>]")
	}

	// A FROM ends the stage before it, so that stage is now what its own
	// instructions made of it. 8.4 follows Docker semantics, and Docker's
	// COPY --from reads the named stage's *final* filesystem — a stage recorded
	// at its FROM line would put its own edits out of reach of every later
	// stage, which leaves it able to serve as nobody's source but its own.
	//
	// Sealed before the new reference is resolved, so `FROM s` on the line
	// after s's last instruction finds s, and finds it finished.
	b.seal()

	ref := b.expand(args[0])
	tree, pin, err := b.resolve(ref)
	if err != nil {
		return err
	}

	// Overwritten by each FROM, so what survives is the last one — the base the
	// final stage, and therefore the artifact, descends from (2.3).
	b.report.Base = Base{Ref: ref, Digest: pin}

	b.current, b.stage = tree, stage
	b.origins = map[string]string{}
	return nil
}

// seal records the finished stage under the name it was declared with.
//
// No copy is taken here and none is needed: current is about to be replaced by
// a whole new tree, so nothing mutates the sealed one afterwards. The copy that
// does matter is resolve's — `FROM <stage>` clones, so a build descending from
// a stage cannot edit it retroactively — and COPY --from only ever reads.
func (b *builder) seal() {
	if b.stage != "" {
		b.stages[b.stage] = b.current
	}
	b.stage = ""
}

// resolve loads a FROM source, returning the tree and the source's pin — all
// three schemes of 8.3: local, git and OCI.
//
// A stage name is answered before any of them. 8.4's worked example writes
// `FROM base` after `FROM … AS base`, so a bare name that a previous FROM
// bound is that stage — checked first, as Docker checks it first, because
// otherwise a directory that happens to share the name would shadow the stage
// the Skillfile plainly meant.
func (b *builder) resolve(ref string) (*Tree, string, error) {
	if stage, ok := b.stages[ref]; ok {
		// The stage as its own instructions left it, and a copy of it: the copy
		// is what makes the instructions that follow mutate this build's tree
		// and not the stage, so a COPY --from naming the same stage afterwards
		// still sees the stage. Sharing the tree would let a stage be edited
		// retroactively by the build that descends from it.
		//
		// No pin (8.3): the stage is whatever its own FROM resolved to, and the
		// pin the report keeps is that FROM's.
		return stage.Clone(), "", nil
	}
	if b.declared[ref] {
		return nil, "", errStageNotYet(ref)
	}

	if strings.HasPrefix(ref, gitPrefix) {
		return b.resolveGit(ref)
	}
	if strings.Contains(ref, "://") {
		return nil, "", fmt.Errorf("%s: unsupported source", ref)
	}
	// An OCI reference carries no scheme, so it is told from a directory by
	// what precedes the first slash. See looksLikeOCIRef.
	if looksLikeOCIRef(ref) {
		return b.resolveOCI(ref)
	}
	// A local base has no pin (8.3): it is whatever is on disk right now.
	tree, err := LoadDir(filepath.Join(b.contextDir, filepath.FromSlash(ref)))
	return tree, "", err
}

// copy moves files between a stage and the current tree, or from the context.
//
// 8.4 composes by explicit enumeration rather than merge-by-default, so a COPY
// names what it wants.
func (b *builder) copy(inst Instruction) error {
	if len(inst.Args) < 2 {
		return fmt.Errorf("want <src>... <dest>")
	}
	if b.current == nil {
		return fmt.Errorf("no FROM yet")
	}

	srcs, dest := inst.Args[:len(inst.Args)-1], b.expand(inst.Args[len(inst.Args)-1])

	// The stage name travels with the copy: it is the values-scope key the
	// destination file will render under at install time (8.4, 10.3).
	var from *Tree
	var stage string
	if named, ok := inst.Flags["from"]; ok {
		stage = named
		from, ok = b.stages[stage]
		if !ok {
			return b.unusableStage(stage)
		}
	}

	for _, src := range srcs {
		src = b.expand(src)
		if err := b.copyOne(from, stage, src, dest); err != nil {
			return err
		}
	}
	return nil
}

// unusableStage says why a COPY --from names nothing it can read.
//
// A stage is entered into stages when its instructions are over, so the name
// missing there means one of three quite different mistakes, and a flat "no
// stage named" would send an author looking for a typo in the two cases where
// the name is right and the position is wrong.
func (b *builder) unusableStage(name string) error {
	switch {
	case name == b.stage:
		return fmt.Errorf(
			"stage %q is the stage being built; COPY --from reads a stage that has finished", name)
	case b.declared[name]:
		return errStageNotYet(name)
	default:
		return fmt.Errorf("no stage named %q", name)
	}
}

// errStageNotYet is what a forward reference to a stage gets.
func errStageNotYet(name string) error {
	return fmt.Errorf(
		"stage %q is declared later in the Skillfile; a stage can only be used after its instructions have run", name)
}

func (b *builder) copyOne(from *Tree, stage, src, dest string) error {
	// Without --from the source is the build context on disk, which is how a
	// Skillfile adds a file that exists in neither base.
	if from == nil {
		body, err := os.ReadFile(filepath.Join(b.contextDir, filepath.FromSlash(src)))
		if err != nil {
			return err
		}
		return b.write(destPath(dest, src), body, stage)
	}

	if body, ok := from.Get(src); ok {
		return b.write(destPath(dest, src), body, stage)
	}

	// A directory source copies everything under it, keeping the layout.
	prefix := strings.TrimSuffix(src, "/") + "/"
	copied := 0
	for _, p := range from.Paths() {
		if !strings.HasPrefix(p, prefix) {
			continue
		}
		body, _ := from.Get(p)
		target := path.Join(strings.TrimSuffix(dest, "/"), strings.TrimPrefix(p, prefix))
		if err := b.write(target, body, stage); err != nil {
			return err
		}
		copied++
	}
	if copied == 0 {
		return fmt.Errorf("%s: no such file in the source", src)
	}
	return nil
}

// write puts a file into the current tree and records where it came from.
//
// stage is the COPY --from that produced it, or "" for the build context —
// which clears any provenance the path had, because the bytes there are now
// the context's and not the stage's.
//
// Only COPY goes through here. An APPEND, REPLACE, PATCH, AWK or SET writes
// through Tree.Set and leaves the provenance alone: those edit a file in
// place, and where its content came from is not changed by amending it.
func (b *builder) write(p string, body []byte, stage string) error {
	if err := b.current.Set(p, body); err != nil {
		return err
	}
	if stage == "" {
		delete(b.origins, p)
		return nil
	}
	b.origins[p] = stage
	return nil
}

// forget drops the provenance of everything an RM took, whether it named one
// file or a directory prefix.
func (b *builder) forget(p string) {
	delete(b.origins, p)
	prefix := strings.TrimSuffix(p, "/") + "/"
	for existing := range b.origins {
		if strings.HasPrefix(existing, prefix) {
			delete(b.origins, existing)
		}
	}
}

// destPath resolves a destination, treating a trailing slash as a directory.
func destPath(dest, src string) string {
	if strings.HasSuffix(dest, "/") || dest == "." {
		return path.Join(strings.TrimSuffix(dest, "/"), path.Base(src))
	}
	return dest
}

func (b *builder) rm(inst Instruction) error {
	if len(inst.Args) == 0 {
		return fmt.Errorf("want <path> [<path>...]")
	}
	if b.current == nil {
		return fmt.Errorf("no FROM yet")
	}
	for _, p := range inst.Args {
		p = b.expand(p)
		if removed := b.current.Remove(p); removed == 0 {
			// Fatal, unlike the two instructions 8.2 lets off with a warning.
			// A zero-match REPLACE (8.2.2) and an absent-key UNSET (8.2.4) are
			// warnings because their end state is the state the author asked
			// for — the pattern is gone, the key is gone — which is what makes
			// them idempotent against a base that has already adopted the same
			// change. RM has no such reading: the spec deliberately leaves it
			// out of that list, because a path that is not there is a path the
			// author was wrong about, and continuing would ship an artifact
			// built from a Skillfile that no longer describes its base.
			return fmt.Errorf("%s: no such file", p)
		}
		b.forget(p)
	}
	return nil
}

func (b *builder) append(inst Instruction) error {
	if len(inst.Args) < 1 {
		return fmt.Errorf("want <path> (<<EOF … EOF | <file>)")
	}
	if b.current == nil {
		return fmt.Errorf("no FROM yet")
	}

	target := b.expand(inst.Args[0])
	payload, err := b.payload(inst, inst.Args[1:])
	if err != nil {
		return err
	}

	existing, _ := b.current.Get(target)
	if len(existing) > 0 && !strings.HasSuffix(string(existing), "\n") {
		existing = append(existing, '\n')
	}
	return b.current.Set(target, append(existing, payload...))
}

// payload returns an instruction's inline heredoc or the named file's
// contents, verbatim either way (8.6).
func (b *builder) payload(inst Instruction, rest []string) ([]byte, error) {
	if inst.Heredoc != "" {
		body := inst.Heredoc
		if !strings.HasSuffix(body, "\n") {
			body += "\n"
		}
		return []byte(body), nil
	}
	if len(rest) != 1 {
		return nil, fmt.Errorf("want an inline heredoc or exactly one file")
	}
	return os.ReadFile(filepath.Join(b.contextDir, filepath.FromSlash(b.expand(rest[0]))))
}

// patch applies a unified diff to a file in the tree (8.2.1).
//
// Strict by design, and stricter than `git apply`: go-gitdiff applies each hunk
// at the line its header records, with no offset search and no fuzz, so an
// unrelated upstream insertion above the hunk fails the build even though every
// context line still matches. That is the point of having both instructions —
// REPLACE is the one for edits that must survive line drift, PATCH is the one
// that says "the file I described is the file I got".
//
// Failure is fatal: no .rej, no warn-and-continue. The artifact is
// content-addressed, so a partially applied patch would quietly produce a
// different digest from the same inputs, which is the one outcome 2.4 cannot
// tolerate.
func (b *builder) patch(inst Instruction) error {
	if len(inst.Args) < 1 {
		return fmt.Errorf("want <path> (<<EOF … EOF | <diff-file>)")
	}
	if b.current == nil {
		return fmt.Errorf("no FROM yet")
	}

	target := b.expand(inst.Args[0])
	body, ok := b.current.Get(target)
	if !ok {
		return fmt.Errorf("%s: no such file", target)
	}

	diff, err := b.payload(inst, inst.Args[1:])
	if err != nil {
		return err
	}

	// Parse returns the leading prose of a `git format-patch` mail as the
	// preamble and drops it, so a payload that is not a diff at all comes back
	// as zero files and no error. Left alone that would be a silent no-op,
	// which is exactly what this instruction exists to rule out.
	files, _, err := gitdiff.Parse(bytes.NewReader(diff))
	if err != nil {
		return fmt.Errorf("%s: %w", target, err)
	}
	switch {
	case len(files) == 0:
		return fmt.Errorf("%s: the payload contains no file diff", target)
	case len(files) > 1:
		// The instruction names one file, so a multi-file diff is ambiguous
		// about which of its halves was meant.
		return fmt.Errorf("%s: the payload patches %d files, want exactly one", target, len(files))
	}

	var out bytes.Buffer
	if err := gitdiff.Apply(&out, bytes.NewReader(body), files[0]); err != nil {
		return fmt.Errorf("%s: %w", target, err)
	}
	return b.current.Set(target, out.Bytes())
}

// replace rewrites a file with a regular expression (8.2.2).
//
// RE2, so no backreferences or lookaround and no author-supplied ReDoS: a
// Skillfile from a third-party base cannot hang the build.
func (b *builder) replace(inst Instruction) error {
	if len(inst.Args) != 3 {
		return fmt.Errorf("want <path> <pattern> <replacement>")
	}
	if b.current == nil {
		return fmt.Errorf("no FROM yet")
	}

	target := b.expand(inst.Args[0])
	body, ok := b.current.Get(target)
	if !ok {
		return fmt.Errorf("%s: no such file", target)
	}

	re, err := regexp.Compile(inst.Args[1])
	if err != nil {
		return fmt.Errorf("pattern: %w", err)
	}
	repl := b.expand(inst.Args[2])

	limit := -1
	if raw, ok := inst.Flags["count"]; ok {
		if limit, err = strconv.Atoi(raw); err != nil || limit < 1 {
			return fmt.Errorf("--count needs a positive integer, got %q", raw)
		}
	}

	// FindAllSubmatchIndex with a negative n means every match, which is
	// exactly --count's default. Building the output by hand rather than with
	// ReplaceAllFunc is what makes $1 expansion correct: Expand needs the
	// submatch indices *into the source*, which a per-match callback does not
	// have.
	matched := re.FindAllSubmatchIndex(body, limit)

	if len(matched) == 0 {
		// 8.2.2: a warning, not an error — but counted, so a Skillfile that
		// has silently stopped doing anything is visible.
		b.report.NoOpReplaces = append(b.report.NoOpReplaces,
			fmt.Sprintf("line %d: %s: %q matched nothing", inst.Line, target, inst.Args[1]))
		return nil
	}

	var out []byte
	last := 0
	for _, m := range matched {
		out = append(out, body[last:m[0]]...)
		out = re.Expand(out, []byte(repl), body, m)
		last = m[1]
	}
	out = append(out, body[last:]...)

	return b.current.Set(target, out)
}

// awkTimeout is how long one AWK instruction may run before the build gives up
// (8.2.3). AWK is Turing-complete, so a deadline is the only thing standing
// between a `while (1)` in a third-party base and a hung build.
const awkTimeout = 10 * time.Second

// awk filters a file through a sandboxed AWK program (8.2.3).
//
// REPLACE handles single-line substitution; this is for the structural edits a
// regex cannot express — multi-line, conditional, section-scoped — without
// reintroducing arbitrary command execution, which 8.1 rules out.
//
// The file's current content is the program's stdin and its stdout replaces the
// file, so the instruction is a pure filter over one file.
//
// Output is LF-terminated whatever the input used. goawk's record reader strips
// a trailing CR before the script ever sees $0, so a CRLF file filtered through
// AWK comes back LF — by the time anything is printed there is nothing left to
// preserve, and the only goawk mode that would re-emit CRLF re-emits it for
// every line, which would corrupt an LF file instead.
func (b *builder) awk(inst Instruction) error {
	if len(inst.Args) < 1 {
		return fmt.Errorf("want <path> (<<EOF … EOF | <script-file>)")
	}
	if b.current == nil {
		return fmt.Errorf("no FROM yet")
	}

	target := b.expand(inst.Args[0])
	body, ok := b.current.Get(target)
	if !ok {
		return fmt.Errorf("%s: no such file", target)
	}

	// Verbatim, like every other payload: an AWK script is full of $1 and {}
	// and is not Skillfile syntax, and 8.6 requires a {{ }} in it to survive.
	script, err := b.payload(inst, inst.Args[1:])
	if err != nil {
		return err
	}

	timeout := awkTimeout
	if raw, ok := inst.Flags["timeout"]; ok {
		if timeout, err = time.ParseDuration(raw); err != nil || timeout <= 0 {
			return fmt.Errorf("--timeout needs a positive duration, got %q", raw)
		}
	}

	out, err := runAWK(script, body, timeout)
	if err != nil {
		return fmt.Errorf("%s: %w", target, err)
	}
	return b.current.Set(target, out)
}

// runAWK executes script with input as stdin and returns its stdout.
func runAWK(script, input []byte, timeout time.Duration) ([]byte, error) {
	prog, parseErr := parser.ParseProgram(script, nil)

	// Post-parse, so a script that is both malformed and nondeterministic
	// reports the problem that will still be there once the syntax is fixed.
	if err := checkAWKDeterminism(prog, parseErr); err != nil {
		return nil, err
	}
	if parseErr != nil {
		return nil, parseErr
	}

	in, err := interp.New(prog)
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	var out, errs bytes.Buffer
	// The sandbox is mandatory and not configurable (8.2.3). With all four set
	// the program is a pure stdin→stdout function: it cannot spawn a process,
	// touch the filesystem or read the environment, which is what makes AWK
	// compatible with 8.1's no-RUN rule. Environ must be a non-nil empty slice
	// — nil means "inherit os.Environ()".
	//
	// Error is captured rather than left to default to os.Stderr, so a script
	// printing to stderr cannot scribble over the build's own output.
	//
	// NewlineOutput must be Raw, and 2.4 is why. Its zero value is
	// SmartNewlineMode, under which goawk sets newlineOutputCRLF from
	// runtime.GOOS and rewrites every \n it prints as \r\n on Windows. That
	// would make the same Skillfile over the same base produce a different
	// layer — and so a different digest — depending on which machine ran the
	// build, which is the one thing a content-addressed format cannot allow.
	// Raw does no translation at all, so the output depends only on the script
	// and the input bytes.
	//
	// Do not replace this with a \r\n → \n pass over the captured output. Raw
	// mode still lets a script emit a CR it asked for by name (printf "a\r\n"),
	// and a blanket rewrite would silently eat that too.
	status, err := in.ExecuteContext(ctx, &interp.Config{
		Stdin:         bytes.NewReader(input),
		Output:        &out,
		Error:         &errs,
		Args:          []string{},
		Environ:       []string{},
		NoExec:        true,
		NoFileWrites:  true,
		NoFileReads:   true,
		NewlineOutput: interp.RawNewlineMode,
	})
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return nil, fmt.Errorf("the script did not finish within %s", timeout)
	case err != nil:
		return nil, err
	case status != 0:
		return nil, fmt.Errorf("the script exited with status %d", status)
	}
	return out.Bytes(), nil
}

// nondeterministicAWK maps the compiled opcode of each rejected builtin to the
// name to complain about. srand() compiles to two opcodes depending on whether
// it was given a seed.
var nondeterministicAWK = map[string]string{
	"BuiltinRand":      "rand()",
	"BuiltinSrand":     "srand()",
	"BuiltinSrandSeed": "srand()",
}

// undefinedAWKFunc pulls the name out of goawk's "undefined function" error.
var undefinedAWKFunc = regexp.MustCompile(`undefined function "([^"]+)"`)

// checkAWKDeterminism rejects systime(), srand() and rand() (8.2.3).
//
// They would make the digest vary across builds with identical inputs, which
// breaks 2.4 — and because the artifact is content-addressed, that is not a
// cosmetic difference but a different artifact.
//
// The check reads the compiled program rather than the source text. goawk's AST
// lives in an internal package and cannot be walked from outside the module,
// but the instruction stream Disassemble writes is derived from it and is the
// closest exported equivalent: unlike a scan of the script, it cannot mistake
// the same words inside a string literal or a /rand/ regex for a call.
//
// systime() is the odd one out. It is a gawk extension goawk does not
// implement, so it never reaches an AST at all — the parser rejects it as an
// undefined function. Recognising that here is what tells the author it is
// refused on purpose, rather than leaving them to conclude the engine is
// merely incomplete and go looking for a way around it.
func checkAWKDeterminism(prog *parser.Program, parseErr error) error {
	if parseErr != nil {
		if m := undefinedAWKFunc.FindStringSubmatch(parseErr.Error()); m != nil && m[1] == "systime" {
			return errNondeterministicAWK("systime()")
		}
		return nil
	}

	var asm bytes.Buffer
	if err := prog.Disassemble(&asm); err != nil {
		return err
	}
	for line := range strings.SplitSeq(asm.String(), "\n") {
		// "0000    CallBuiltin BuiltinRand": the opcode is the second field, so
		// a literal that happens to contain the same words is not a call.
		fields := strings.Fields(line)
		if len(fields) < 3 || fields[1] != "CallBuiltin" {
			continue
		}
		if name, rejected := nondeterministicAWK[fields[2]]; rejected {
			return errNondeterministicAWK(name)
		}
	}
	return nil
}

func errNondeterministicAWK(name string) error {
	return fmt.Errorf(
		"%s is rejected: it would make the output differ between builds of identical inputs", name)
}

// setKey writes a YAML key (8.2.4).
//
// Structure-aware, so it cannot produce invalid YAML the way a byte-oriented
// edit can: quoting styles, block scalars and list indentation all defeat
// regex surgery, and SKILL.md's frontmatter holds the fields that decide
// whether an agent loads the skill at all.
func (b *builder) setKey(inst Instruction) error {
	if len(inst.Args) != 2 {
		return fmt.Errorf("want [--file=<path>] <key> <value>")
	}
	// 8.2.4: the value is a YAML scalar, and quoting forces a string. The
	// tokenizer resolves the quotes but records that they were there, which is
	// what keeps `SET version "1.2"` from writing the float 1.2. Read before
	// expansion, because it is the argument that was quoted, not whatever an
	// ARG puts inside it.
	forceString := inst.quoted(1)
	return b.editYAML(inst, func(doc *yamlDoc) error {
		return doc.set(b.expand(inst.Args[0]), b.expand(inst.Args[1]), forceString)
	})
}

// unsetKey removes a YAML key. An absent key warns and continues (8.2.4).
func (b *builder) unsetKey(inst Instruction) error {
	if len(inst.Args) != 1 {
		return fmt.Errorf("want [--file=<path>] <key>")
	}
	return b.editYAML(inst, func(doc *yamlDoc) error {
		key := b.expand(inst.Args[0])
		if !doc.unset(key) {
			b.report.MissingUnsets = append(b.report.MissingUnsets,
				fmt.Sprintf("line %d: %s: key %q was already absent", inst.Line, doc.path, key))
		}
		return nil
	})
}

// editYAML applies fn to the instruction's target document.
//
// The default target is SKILL.md's frontmatter block; --file names any YAML
// file in the tree. Untargeted files are never re-serialised, so they stay
// byte-identical (8.2.4).
func (b *builder) editYAML(inst Instruction, fn func(*yamlDoc) error) error {
	if b.current == nil {
		return fmt.Errorf("no FROM yet")
	}

	target := b.expand(inst.Flags["file"])
	if target == "" {
		target = "SKILL.md"
	}

	body, ok := b.current.Get(target)
	if !ok {
		return fmt.Errorf("%s: no such file", target)
	}

	doc, err := openYAML(target, body)
	if err != nil {
		return err
	}
	if err := fn(doc); err != nil {
		return err
	}

	out, err := doc.bytes()
	if err != nil {
		return err
	}
	return b.current.Set(target, out)
}
