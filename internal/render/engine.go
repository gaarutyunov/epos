// Package render implements Epos's templating engine: Go text/template plus the
// Sprig function library (matching Helm), with env/expandenv omitted for safety
// and the Helm-style include/required helpers plus Epos's includeReference
// (SPEC §3).
package render

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/template"

	"github.com/Masterminds/sprig/v3"
	"sigs.k8s.io/yaml"
)

// Engine renders a skill package's SKILL.md (and templates/) with values.
type Engine struct {
	// Used records the reference paths the render emitted via includeReference —
	// the render-time "used references" set (SPEC §3.4).
	Used map[string]bool
}

// Result is a render outcome: the rendered SKILL.md plus the set of supporting
// files the output references (for selective materialization, SPEC §3.4).
type Result struct {
	SkillMD string
	Used    []string
}

// New returns a fresh engine.
func New() *Engine { return &Engine{Used: map[string]bool{}} }

// RenderDir renders dir/SKILL.md with the merged values, loading named-template
// helpers from templates/ (files beginning with '_' never render to output).
func (e *Engine) RenderDir(dir string, values map[string]any) (*Result, error) {
	body, err := os.ReadFile(filepath.Join(dir, "SKILL.md"))
	if err != nil {
		return nil, fmt.Errorf("read SKILL.md: %w", err)
	}
	return e.Render(string(body), values, filepath.Join(dir, "templates"))
}

// Render renders a SKILL.md body string with values, loading any named-template
// helpers from helperDir (may be empty).
func (e *Engine) Render(body string, values map[string]any, helperDir string) (*Result, error) {
	var root *template.Template
	tmpl := template.New("SKILL.md").Funcs(e.funcs(&root))

	if helperDir != "" {
		helpers, _ := filepath.Glob(filepath.Join(helperDir, "*"))
		sort.Strings(helpers)
		for _, h := range helpers {
			data, err := os.ReadFile(h)
			if err != nil {
				return nil, err
			}
			if _, err := tmpl.New(filepath.Base(h)).Parse(string(data)); err != nil {
				return nil, fmt.Errorf("parse helper %s: %w", filepath.Base(h), err)
			}
		}
	}
	if _, err := tmpl.New("SKILL.md").Parse(body); err != nil {
		return nil, fmt.Errorf("parse SKILL.md: %w", err)
	}
	root = tmpl

	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "SKILL.md", map[string]any{"Values": values}); err != nil {
		return nil, fmt.Errorf("render SKILL.md: %w", err)
	}

	used := make([]string, 0, len(e.Used))
	for p := range e.Used {
		used = append(used, p)
	}
	sort.Strings(used)
	return &Result{SkillMD: buf.String(), Used: used}, nil
}

// funcs builds the Sprig function map minus env/expandenv, plus include,
// required, and includeReference (SPEC §3.1, §3.4). include closes over root,
// which the caller sets to the fully-parsed template before execution.
func (e *Engine) funcs(root **template.Template) template.FuncMap {
	fm := sprig.TxtFuncMap()
	delete(fm, "env")
	delete(fm, "expandenv")

	fm["include"] = func(name string, data any) (string, error) {
		if *root == nil {
			return "", fmt.Errorf("include: template set not ready")
		}
		var buf bytes.Buffer
		if err := (*root).ExecuteTemplate(&buf, name, data); err != nil {
			return "", err
		}
		return buf.String(), nil
	}
	fm["required"] = func(msg string, v any) (any, error) {
		if v == nil {
			return nil, fmt.Errorf("%s", msg)
		}
		if s, ok := v.(string); ok && s == "" {
			return nil, fmt.Errorf("%s", msg)
		}
		return v, nil
	}
	fm["includeReference"] = func(path string) string {
		e.Used[path] = true
		return fmt.Sprintf("[%s](%s)", referenceTitle(path), path)
	}
	return fm
}

func referenceTitle(path string) string {
	base := filepath.Base(path)
	base = strings.TrimSuffix(base, filepath.Ext(base))
	base = strings.ReplaceAll(base, "-", " ")
	base = strings.ReplaceAll(base, "_", " ")
	if base == "" {
		return path
	}
	return strings.Title(base) //nolint:staticcheck // simple ASCII title for labels
}

// Bundle renders a skill file set's SKILL.md with the package values.yaml
// overridden by `overrides` (already-merged -f/--set values, Helm precedence,
// SPEC §3.3), and returns the file set with SKILL.md rendered plus only the
// supporting files the output references (§3.4 selective materialization). It
// also returns the effective merged values that were applied (for the revision
// snapshot, §5.3). A file set without SKILL.md passes through unchanged.
func Bundle(files map[string][]byte, overrides map[string]any) (map[string][]byte, map[string]any, error) {
	base := map[string]any{}
	if v, ok := files["values.yaml"]; ok {
		if vals, err := LoadValuesFromBytes(v); err == nil {
			base = vals
		}
	}
	effective := MergeValues(base, overrides)
	if _, ok := files["SKILL.md"]; !ok {
		return files, effective, nil
	}

	tmp, err := os.MkdirTemp("", "epos-render-*")
	if err != nil {
		return nil, nil, err
	}
	defer os.RemoveAll(tmp)
	for rel, data := range files {
		full := filepath.Join(tmp, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			return nil, nil, err
		}
		if err := os.WriteFile(full, data, 0o644); err != nil {
			return nil, nil, err
		}
	}
	res, err := New().RenderDir(tmp, effective)
	if err != nil {
		return nil, nil, err
	}
	out := map[string][]byte{"SKILL.md": []byte(res.SkillMD)}
	for _, keep := range []string{"Epos.yaml", "values.yaml"} {
		if v, ok := files[keep]; ok {
			out[keep] = v
		}
	}
	for _, ref := range res.Used {
		if v, ok := files[ref]; ok {
			out[ref] = v
		}
	}
	return out, effective, nil
}

// MergeValues merges value layers with Helm precedence: base values.yaml, then
// each -f file in order, then --set overrides. Maps deep-merge; lists replace
// wholesale; a null value deletes the key (SPEC §3.3).
func MergeValues(base map[string]any, overlays ...map[string]any) map[string]any {
	out := deepCopyMap(base)
	for _, o := range overlays {
		out = mergeMap(out, o)
	}
	return out
}

// LoadValuesFile reads and parses a YAML values file into a map.
func LoadValuesFile(path string) (map[string]any, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return LoadValuesFromBytes(data)
}

// LoadValuesFromBytes parses YAML values bytes into a map.
func LoadValuesFromBytes(data []byte) (map[string]any, error) {
	var m map[string]any
	if err := yaml.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parse values: %w", err)
	}
	if m == nil {
		m = map[string]any{}
	}
	return m, nil
}

// SetOverride parses a dotted --set key=value into a nested map (SPEC §3.3).
func SetOverride(assignment string) (map[string]any, error) {
	i := strings.Index(assignment, "=")
	if i < 0 {
		return nil, fmt.Errorf("invalid --set %q: expected key=value", assignment)
	}
	key, val := assignment[:i], assignment[i+1:]
	parts := strings.Split(key, ".")
	root := map[string]any{}
	cur := root
	for j, p := range parts {
		if j == len(parts)-1 {
			cur[p] = coerce(val)
		} else {
			next := map[string]any{}
			cur[p] = next
			cur = next
		}
	}
	return root, nil
}

// SetStringOverride parses a dotted --set-string key=value, always treating the
// value as a string (no bool/number coercion) (SPEC §3.3).
func SetStringOverride(assignment string) (map[string]any, error) {
	i := strings.Index(assignment, "=")
	if i < 0 {
		return nil, fmt.Errorf("invalid --set-string %q: expected key=value", assignment)
	}
	return nestAssign(assignment[:i], assignment[i+1:]), nil
}

// SetFileOverride parses a dotted --set-file key=path, using the file's contents
// as the (string) value (SPEC §3.3).
func SetFileOverride(assignment string) (map[string]any, error) {
	i := strings.Index(assignment, "=")
	if i < 0 {
		return nil, fmt.Errorf("invalid --set-file %q: expected key=path", assignment)
	}
	data, err := os.ReadFile(assignment[i+1:])
	if err != nil {
		return nil, err
	}
	return nestAssign(assignment[:i], string(data)), nil
}

// nestAssign builds a nested map from a dotted key and a final value.
func nestAssign(key string, val any) map[string]any {
	parts := strings.Split(key, ".")
	root := map[string]any{}
	cur := root
	for j, p := range parts {
		if j == len(parts)-1 {
			cur[p] = val
		} else {
			next := map[string]any{}
			cur[p] = next
			cur = next
		}
	}
	return root
}

func coerce(s string) any {
	switch s {
	case "true":
		return true
	case "false":
		return false
	}
	return s
}

func mergeMap(dst, src map[string]any) map[string]any {
	if dst == nil {
		dst = map[string]any{}
	}
	for k, v := range src {
		if v == nil {
			delete(dst, k) // null deletes (SPEC §3.3)
			continue
		}
		if sm, ok := v.(map[string]any); ok {
			if dm, ok := dst[k].(map[string]any); ok {
				dst[k] = mergeMap(dm, sm)
				continue
			}
		}
		dst[k] = v // lists and scalars replace wholesale
	}
	return dst
}

func deepCopyMap(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		if sm, ok := v.(map[string]any); ok {
			out[k] = deepCopyMap(sm)
			continue
		}
		out[k] = v
	}
	return out
}
