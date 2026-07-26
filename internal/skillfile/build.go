package skillfile

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// Build runs a Skillfile against a context directory and returns the final
// stage's tree.
//
// Pure by construction (SPEC.md 8.1): the only inputs are the bases, the
// Skillfile and the context, and nothing executes.
func Build(sf *Skillfile, contextDir string, buildArgs map[string]string) (*Tree, *Report, error) {
	b := &builder{
		contextDir: contextDir,
		args:       map[string]string{},
		stages:     map[string]*Tree{},
		report:     &Report{},
	}
	for k, v := range buildArgs {
		b.args[k] = v
	}

	for _, inst := range sf.Instructions {
		if err := b.apply(inst); err != nil {
			return nil, nil, fmt.Errorf("line %d: %s: %w", inst.Line, inst.Op, err)
		}
	}

	if b.current == nil {
		return nil, nil, fmt.Errorf("Skillfile has no FROM")
	}
	return b.current, b.report, nil
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
}

type builder struct {
	contextDir string
	args       map[string]string
	stages     map[string]*Tree
	current    *Tree
	report     *Report
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
	case "PATCH", "AWK":
		return fmt.Errorf("not implemented yet")
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

	ref := b.expand(args[0])
	tree, err := b.resolve(ref)
	if err != nil {
		return err
	}

	b.current = tree
	if stage != "" {
		// The stage keeps its own copy, so later instructions mutating the
		// current tree do not reach back into it (8.4).
		b.stages[stage] = tree.Clone()
	}
	return nil
}

// resolve loads a FROM source. B1 supports local paths; git arrives with the
// rest of 8.3.
func (b *builder) resolve(ref string) (*Tree, error) {
	if strings.HasPrefix(ref, "git+") {
		return nil, fmt.Errorf("git sources are not implemented yet")
	}
	if strings.Contains(ref, "://") {
		return nil, fmt.Errorf("%s: unsupported source", ref)
	}
	return LoadDir(filepath.Join(b.contextDir, filepath.FromSlash(ref)))
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

	var from *Tree
	if stage, ok := inst.Flags["from"]; ok {
		from, ok = b.stages[stage]
		if !ok {
			return fmt.Errorf("no stage named %q", stage)
		}
	}

	for _, src := range srcs {
		src = b.expand(src)
		if err := b.copyOne(from, src, dest); err != nil {
			return err
		}
	}
	return nil
}

func (b *builder) copyOne(from *Tree, src, dest string) error {
	// Without --from the source is the build context on disk, which is how a
	// Skillfile adds a file that exists in neither base.
	if from == nil {
		body, err := os.ReadFile(filepath.Join(b.contextDir, filepath.FromSlash(src)))
		if err != nil {
			return err
		}
		return b.current.Set(destPath(dest, src), body)
	}

	if body, ok := from.Get(src); ok {
		return b.current.Set(destPath(dest, src), body)
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
		if err := b.current.Set(target, body); err != nil {
			return err
		}
		copied++
	}
	if copied == 0 {
		return fmt.Errorf("%s: no such file in the source", src)
	}
	return nil
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
		return fmt.Errorf("want <path>...")
	}
	if b.current == nil {
		return fmt.Errorf("no FROM yet")
	}
	for _, p := range inst.Args {
		p = b.expand(p)
		if removed := b.current.Remove(p); removed == 0 {
			// Removing something already absent is the state the author asked
			// for, so it is not an error — but it is worth naming, because it
			// usually means the base moved underneath the Skillfile.
			return fmt.Errorf("%s: no such file", p)
		}
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
	return b.editYAML(inst, func(doc *yamlDoc) error {
		return doc.set(b.expand(inst.Args[0]), b.expand(inst.Args[1]))
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
