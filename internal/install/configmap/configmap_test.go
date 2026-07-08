package configmap

import (
	"strings"
	"testing"

	"sigs.k8s.io/yaml"
)

func TestRenderSingleConfigMap(t *testing.T) {
	files := map[string][]byte{
		"SKILL.md":        []byte("# skill\n"),
		"references/a.md": []byte("a\n"),
	}
	r, err := Render("pdf", "skills", "", files)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.ConfigMaps) != 1 {
		t.Fatalf("expected 1 ConfigMap, got %d", len(r.ConfigMaps))
	}
	// Valid YAML round-trips.
	var cm map[string]any
	if err := yaml.Unmarshal([]byte(r.YAML), &cm); err != nil {
		t.Fatalf("invalid ConfigMap YAML: %v", err)
	}
	// Nested path reconstructed via items[].path.
	items := r.Items["pdf"]
	var found bool
	for _, it := range items {
		if it.Path == "references/a.md" && it.Key == "references_a.md" {
			found = true
		}
	}
	if !found {
		t.Errorf("items[].path mapping missing: %+v", items)
	}
	// No credentials leak into the YAML.
	if strings.Contains(r.YAML, "password") || strings.Contains(r.YAML, "token") {
		t.Error("credentials leaked into ConfigMap YAML")
	}
}

func TestAutoSplitPastCeiling(t *testing.T) {
	big := strings.Repeat("x", 700*1024)
	files := map[string][]byte{
		"SKILL.md":           []byte("# skill\n"),
		"references/big1.md": []byte(big),
		"scripts/big2.sh":    []byte(big),
	}
	r, err := Render("big", "skills", "", files)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.ConfigMaps) < 2 {
		t.Fatalf("expected auto-split into multiple ConfigMaps, got %d", len(r.ConfigMaps))
	}
	// Split names are suffixed from the handle.
	names := map[string]bool{}
	for _, cm := range r.ConfigMaps {
		names[cm.Metadata.Name] = true
	}
	if !names["big-references"] || !names["big-scripts"] {
		t.Errorf("expected subtree-suffixed names, got %v", names)
	}
}
