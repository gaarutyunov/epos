package domain

import (
	"bytes"
	"errors"
	"fmt"
	"regexp"
	"sort"

	"github.com/bluekeyes/go-gitdiff/gitdiff"
)

// StackLayer kinds and source kinds (string values of the LayerKind/SourceKind VOs).
const (
	KindSkill   = "skill"
	KindOverlay = "overlay"

	SourceLocal = "local"
	SourceOCI   = "oci"
	SourceGit   = "git"
)

// StackLayer is one layer in the composition stack. A skill layer contributes Files;
// an overlay layer contributes Operations applied to the layers below it.
type StackLayer struct {
	Name       string
	Kind       string // KindSkill | KindOverlay
	Source     string // SourceLocal | SourceOCI | SourceGit
	Files      map[string][]byte
	Operations []Operation
	// PayloadFiles holds sibling files (files/…) referenced by overlay ops via
	// PayloadPath, keyed by that path.
	PayloadFiles map[string][]byte
	Pin          *Pin
}

// SkillMarkdownPath is the operation-merge target that every layer contributes to.
const SkillMarkdownPath = "SKILL.md"

// Merged is the resolved composition: the final file set plus a per-file
// provenance report (which layer supplied each file) (SPEC §9.1).
type Merged struct {
	Files      map[string][]byte
	Provenance map[string]string
	Warnings   []string
}

// ProvenanceLines returns the per-file provenance sorted by path, as
// "path\tlayer" lines (fills MergedSkill.Provenance).
func (m *Merged) ProvenanceLines() []string {
	paths := make([]string, 0, len(m.Provenance))
	for p := range m.Provenance {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		out = append(out, fmt.Sprintf("%s\t%s", p, m.Provenance[p]))
	}
	return out
}

// Compose resolves an ordered stack (low→high precedence) into one merged skill
// using the single later-overrides-earlier rule (SPEC §9.5). strict promotes
// non-matching/failing operations to hard errors; otherwise they warn and skip.
func Compose(layers []StackLayer, strict bool) (*Merged, error) {
	m := &Merged{Files: map[string][]byte{}, Provenance: map[string]string{}}
	for _, layer := range layers {
		switch layer.Kind {
		case KindSkill:
			// Whole-file: the highest layer that supplies a path owns it.
			for path, content := range layer.Files {
				m.Files[path] = append([]byte(nil), content...)
				m.Provenance[path] = layer.Name
			}
		case KindOverlay:
			if err := m.applyOverlay(layer, strict); err != nil {
				return nil, err
			}
		default:
			return nil, fmt.Errorf("layer %q: unknown kind %q", layer.Name, layer.Kind)
		}
	}
	return m, nil
}

func (m *Merged) applyOverlay(layer StackLayer, strict bool) error {
	for i, op := range layer.Operations {
		if err := m.applyOp(layer, op, i, strict); err != nil {
			return err
		}
	}
	return nil
}

func (m *Merged) payload(layer StackLayer, op Operation) ([]byte, error) {
	if op.PayloadPath != "" {
		data, ok := layer.PayloadFiles[op.PayloadPath]
		if !ok {
			return nil, fmt.Errorf("overlay %q op %s: payload file %q not found", layer.Name, op.Op, op.PayloadPath)
		}
		return data, nil
	}
	return []byte(op.Content), nil
}

func (m *Merged) applyOp(layer StackLayer, op Operation, idx int, strict bool) error {
	required := op.Required || strict
	fail := func(format string, a ...any) error {
		msg := fmt.Sprintf("overlay %q op %d (%s on %s): ", layer.Name, idx, op.Op, op.Target) + fmt.Sprintf(format, a...)
		if required {
			return errors.New(msg)
		}
		m.Warnings = append(m.Warnings, msg)
		return nil
	}

	switch op.Op {
	case OpAddFile:
		data, err := m.payload(layer, op)
		if err != nil {
			return err
		}
		if _, exists := m.Files[op.Target]; exists {
			m.Warnings = append(m.Warnings, fmt.Sprintf("overlay %q: add-file onto existing path %q (overwriting)", layer.Name, op.Target))
		}
		m.Files[op.Target] = data
		m.Provenance[op.Target] = layer.Name

	case OpDeleteFile:
		if _, exists := m.Files[op.Target]; !exists {
			return fail("target does not exist")
		}
		delete(m.Files, op.Target)
		delete(m.Provenance, op.Target)

	case OpAppendToFile:
		data, err := m.payload(layer, op)
		if err != nil {
			return err
		}
		cur, exists := m.Files[op.Target]
		if !exists {
			return fail("target does not exist")
		}
		buf := append([]byte(nil), cur...)
		if len(buf) > 0 && buf[len(buf)-1] != '\n' {
			buf = append(buf, '\n')
		}
		buf = append(buf, data...)
		m.Files[op.Target] = buf
		m.Provenance[op.Target] = layer.Name

	case OpReplaceIn:
		cur, exists := m.Files[op.Target]
		if !exists {
			return fail("target does not exist")
		}
		pattern := op.Pattern
		repl := op.Replacement
		if op.PayloadPath != "" {
			// A path payload for replace supplies "pattern\n---\nreplacement".
			p, r, ok := splitReplacePayload(layer.PayloadFiles[op.PayloadPath])
			if !ok {
				return fail("payload file is not a valid replace spec")
			}
			pattern, repl = p, r
		}
		re, err := regexp.Compile(pattern)
		if err != nil {
			return fail("invalid regex: %v", err)
		}
		if !re.Match(cur) {
			return fail("pattern %q found no match", pattern)
		}
		m.Files[op.Target] = re.ReplaceAll(cur, []byte(repl))
		m.Provenance[op.Target] = layer.Name

	case OpPatchFile:
		cur, exists := m.Files[op.Target]
		if !exists {
			return fail("target does not exist")
		}
		data, err := m.payload(layer, op)
		if err != nil {
			return err
		}
		patched, err := applyUnifiedDiff(cur, data)
		if err != nil {
			return fail("patch failed to apply: %v", err)
		}
		m.Files[op.Target] = patched
		m.Provenance[op.Target] = layer.Name

	default:
		return fmt.Errorf("overlay %q: unknown op %q", layer.Name, op.Op)
	}
	return nil
}

// applyUnifiedDiff applies a single-file unified diff hunk to src.
func applyUnifiedDiff(src, diff []byte) ([]byte, error) {
	files, _, err := gitdiff.Parse(bytes.NewReader(diff))
	if err != nil {
		return nil, err
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("no file patch found in diff")
	}
	var out bytes.Buffer
	if err := gitdiff.Apply(&out, bytes.NewReader(src), files[0]); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// splitReplacePayload parses a "pattern\n---\nreplacement" replace spec file.
func splitReplacePayload(data []byte) (pattern, replacement string, ok bool) {
	parts := bytes.SplitN(data, []byte("\n---\n"), 2)
	if len(parts) != 2 {
		return "", "", false
	}
	return string(parts[0]), string(parts[1]), true
}
