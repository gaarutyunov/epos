package install

import (
	"fmt"
	"os"
	"strings"

	"github.com/goccy/go-yaml"
)

// GlobalKey is the block every stage can see (SPEC.md 10.3).
//
// It is the deliberate cross-stage channel: everything else a stage sees is
// its own, which is what lets two stages both write `{{ .Values.title }}` and
// mean two different things.
const GlobalKey = "global"

// Values are the install-time parameters of 10.3, as one document.
//
// Scoping follows Helm. The top level belongs to the skill itself; a key whose
// name is a Skillfile stage (8.4) is that stage's scope, the way a Helm
// subchart's values nest under the subchart's name.
type Values struct {
	top map[string]any
}

// LoadValues reads -f files in order and then applies --set (10.3).
//
// Later files win over earlier ones, key by key rather than file by file, and
// --set wins over every file — the ordering Helm has, and the one a user gets
// wrong least often.
//
// YAML is read with goccy/go-yaml, the dependency 8.2.4 already uses. There is
// deliberately only one YAML implementation in this module: two would be two
// answers to what a document means.
func LoadValues(files []string, sets []string) (Values, error) {
	top := map[string]any{}

	for _, file := range files {
		src, err := os.ReadFile(file)
		if err != nil {
			return Values{}, fmt.Errorf("read %s: %w", file, err)
		}
		var doc map[string]any
		if err := yaml.Unmarshal(src, &doc); err != nil {
			return Values{}, fmt.Errorf("parse %s: %w", file, err)
		}
		merge(top, normaliseMap(doc))
	}

	for _, set := range sets {
		if err := applySet(top, set); err != nil {
			return Values{}, err
		}
	}
	return Values{top: top}, nil
}

// Scope is the `.Values` a file contributed by stage sees (10.3).
//
// The empty stage is the skill's own scope — the whole document, as a Helm
// chart sees its own values. A named stage sees that key's sub-document, plus
// the shared global block under its usual name, and nothing else: that is what
// keeps one stage's `title` out of another's.
//
// A stage with no values of its own still sees global. Rendering then fails on
// the first parameter it actually needs, which says more than an empty scope
// silently producing an empty document would.
func (v Values) Scope(stage string) map[string]any {
	if stage == "" {
		return v.top
	}

	out := map[string]any{}
	if sub, ok := v.top[stage].(map[string]any); ok {
		for k, val := range sub {
			out[k] = val
		}
	}
	// The top-level global wins over a stage-local key of the same name: a
	// stage that could shadow `global` could quietly cut itself off from the
	// one channel 10.3 gives every stage.
	if g, ok := v.top[GlobalKey]; ok {
		out[GlobalKey] = g
	}
	return out
}

// applySet applies one --set k=v, with dots naming nested keys.
//
// Values stay strings. Helm infers types here; this does not, because 10.3
// gives text/template no custom functions and a template that cannot convert
// is better served by a value that is what the user typed than by one silently
// promoted to a float.
func applySet(top map[string]any, set string) error {
	path, value, ok := strings.Cut(set, "=")
	if !ok || path == "" {
		return fmt.Errorf("--set %q is not k=v", set)
	}

	keys := strings.Split(path, ".")
	cursor := top
	for i, key := range keys {
		if key == "" {
			return fmt.Errorf("--set %q has an empty key", set)
		}
		if i == len(keys)-1 {
			cursor[key] = value
			return nil
		}
		next, ok := cursor[key].(map[string]any)
		if !ok {
			// Either nothing is there, or something that is not a map. Either
			// way --set is the later word and replaces it.
			next = map[string]any{}
			cursor[key] = next
		}
		cursor = next
	}
	return nil
}

// merge deep-merges src into dst, src winning at every leaf.
func merge(dst, src map[string]any) {
	for k, v := range src {
		if sub, ok := v.(map[string]any); ok {
			if existing, ok := dst[k].(map[string]any); ok {
				merge(existing, sub)
				continue
			}
		}
		dst[k] = v
	}
}

// normalise rewrites a decoded YAML document so every mapping is a
// map[string]any.
//
// goccy/go-yaml decodes into map[string]any where it can, but a document whose
// keys are not all strings — `1: one`, or a merge key — decodes to
// map[any]any, and text/template cannot index one of those with a field name.
// The failure is a rendering error about a type nobody wrote down, on one
// document out of a hundred, so the conversion happens on the way in rather
// than being left to be discovered.
func normalise(v any) any {
	switch typed := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))
		for k, val := range typed {
			out[k] = normalise(val)
		}
		return out
	case map[any]any:
		out := make(map[string]any, len(typed))
		for k, val := range typed {
			out[fmt.Sprint(k)] = normalise(val)
		}
		return out
	case []any:
		out := make([]any, len(typed))
		for i, val := range typed {
			out[i] = normalise(val)
		}
		return out
	default:
		return v
	}
}

// normaliseMap is normalise for a document known to be a mapping.
func normaliseMap(doc map[string]any) map[string]any {
	out, _ := normalise(doc).(map[string]any)
	if out == nil {
		return map[string]any{}
	}
	return out
}
