package render

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRenderDirWithValuesAndIncludeReference(t *testing.T) {
	dir := t.TempDir()
	writeAll(t, dir, map[string]string{
		"SKILL.md": "# skill\n" +
			"{{- if .Values.features.advanced }}\n" +
			"See also: {{ includeReference \"references/advanced.md\" }}\n" +
			"{{- end }}\n" +
			"Title: {{ .Values.title | upper }}\n",
		"references/advanced.md": "# advanced\n",
	})

	// advanced=true → the reference is emitted and recorded as used.
	e := New()
	res, err := e.RenderDir(dir, map[string]any{
		"features": map[string]any{"advanced": true},
		"title":    "pdf tools",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.SkillMD, "references/advanced.md") {
		t.Errorf("reference not emitted: %q", res.SkillMD)
	}
	if !strings.Contains(res.SkillMD, "PDF TOOLS") {
		t.Errorf("sprig upper not applied: %q", res.SkillMD)
	}
	if len(res.Used) != 1 || res.Used[0] != "references/advanced.md" {
		t.Errorf("used references = %v", res.Used)
	}

	// advanced=false → the reference is gated out (unused).
	res2, err := New().RenderDir(dir, map[string]any{
		"features": map[string]any{"advanced": false},
		"title":    "x",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res2.Used) != 0 {
		t.Errorf("expected no used references, got %v", res2.Used)
	}
}

func TestBundleAppliesOverridesAndSelectsReferences(t *testing.T) {
	files := map[string][]byte{
		"Epos.yaml":              []byte("apiVersion: epos/v1\nname: demo\nversion: 0.1.0\ndescription: d\n"),
		"values.yaml":            []byte("features:\n  advanced: false\n"),
		"SKILL.md":               []byte("# demo\n{{- if .Values.features.advanced }}\nSee also: {{ includeReference \"references/advanced.md\" }}\n{{- end }}\n"),
		"references/advanced.md": []byte("# advanced\n"),
		"references/unused.md":   []byte("# unused\n"),
	}
	// Default values: advanced=false → reference not emitted, not materialized.
	out, eff, err := Bundle(files, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := out["references/advanced.md"]; ok {
		t.Error("advanced.md should not be materialized when advanced=false")
	}
	if eff["features"].(map[string]any)["advanced"] != false {
		t.Errorf("effective values wrong: %v", eff)
	}

	// Override advanced=true → reference emitted and materialized; unused omitted.
	out, eff, err = Bundle(files, map[string]any{"features": map[string]any{"advanced": true}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out["SKILL.md"]), "references/advanced.md") {
		t.Errorf("override not applied; SKILL.md=%q", out["SKILL.md"])
	}
	if _, ok := out["references/advanced.md"]; !ok {
		t.Error("advanced.md should be materialized when advanced=true")
	}
	if _, ok := out["references/unused.md"]; ok {
		t.Error("unused.md must never be materialized (unreferenced)")
	}
}

func TestMergeValuesPrecedence(t *testing.T) {
	base := map[string]any{"a": 1, "nested": map[string]any{"x": 1, "y": 2}}
	over := map[string]any{"nested": map[string]any{"y": 3}, "b": 2}
	got := MergeValues(base, over)
	nested := got["nested"].(map[string]any)
	if nested["x"] != 1 || nested["y"] != 3 || got["b"] != 2 {
		t.Errorf("merge precedence wrong: %+v", got)
	}
	// null deletes.
	got = MergeValues(got, map[string]any{"a": nil})
	if _, ok := got["a"]; ok {
		t.Errorf("null did not delete key: %+v", got)
	}
}

func writeAll(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for rel, content := range files {
		full := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}
