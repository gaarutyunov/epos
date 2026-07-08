package domain

import (
	"strings"
	"testing"
)

func TestThreeLayerPrecedence(t *testing.T) {
	origin := StackLayer{Name: "origin", Kind: KindSkill, Files: map[string][]byte{
		"references/a.md": []byte("a-origin"),
		"references/b.md": []byte("b-origin"),
		"references/c.md": []byte("c-origin"),
	}}
	intermediate := StackLayer{Name: "intermediate", Kind: KindOverlay, Operations: []Operation{
		{Op: OpAddFile, Target: "references/b.md", Content: "b-intermediate"},
	}}
	repo := StackLayer{Name: "my-repo", Kind: KindOverlay, Operations: []Operation{
		{Op: OpAddFile, Target: "references/a.md", Content: "a-repo"},
	}}

	m, err := Compose([]StackLayer{origin, intermediate, repo}, false)
	if err != nil {
		t.Fatal(err)
	}
	if string(m.Files["references/a.md"]) != "a-repo" || m.Provenance["references/a.md"] != "my-repo" {
		t.Errorf("a.md = %q from %q", m.Files["references/a.md"], m.Provenance["references/a.md"])
	}
	if string(m.Files["references/b.md"]) != "b-intermediate" || m.Provenance["references/b.md"] != "intermediate" {
		t.Errorf("b.md = %q from %q", m.Files["references/b.md"], m.Provenance["references/b.md"])
	}
	if string(m.Files["references/c.md"]) != "c-origin" || m.Provenance["references/c.md"] != "origin" {
		t.Errorf("c.md = %q from %q", m.Files["references/c.md"], m.Provenance["references/c.md"])
	}
}

func TestParseOverlayPathPayload(t *testing.T) {
	// Regression: the on-disk `path:` key must populate the operation's payload
	// reference (SPEC §9.4.1). Previously it silently parsed to an empty payload.
	data := []byte("apiVersion: epos/v1\nkind: Overlay\nname: team-refs\nversion: 0.2.0\n" +
		"operations:\n  - op: add-file\n    target: references/advanced.md\n    path: files/advanced.md\n")
	ov, err := ParseOverlay(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(ov.Operations) != 1 {
		t.Fatalf("want 1 op, got %d", len(ov.Operations))
	}
	if ov.Operations[0].PayloadPath != "files/advanced.md" {
		t.Errorf("path: not mapped to PayloadPath, got %q", ov.Operations[0].PayloadPath)
	}
	// And it composes the referenced payload content, not an empty file.
	layer := StackLayer{Name: "team-refs", Kind: KindOverlay, Operations: ov.Operations,
		PayloadFiles: map[string][]byte{"files/advanced.md": []byte("ADVANCED")}}
	m, err := Compose([]StackLayer{layer}, false)
	if err != nil {
		t.Fatal(err)
	}
	if string(m.Files["references/advanced.md"]) != "ADVANCED" {
		t.Errorf("add-file via path: composed %q, want ADVANCED", m.Files["references/advanced.md"])
	}
}

func TestParseOverlayRejectsDualPayload(t *testing.T) {
	data := []byte("apiVersion: epos/v1\nkind: Overlay\nname: x\nversion: 0.1.0\n" +
		"operations:\n  - op: append-to-file\n    target: SKILL.md\n    path: files/x\n    content: inline\n")
	ov, err := ParseOverlay(data)
	if err != nil {
		t.Fatal(err)
	}
	if msgs := ov.Validate(); len(msgs) == 0 {
		t.Error("expected validation error for both path: and content:")
	}
}

func TestSkillMarkdownOperationMerge(t *testing.T) {
	origin := StackLayer{Name: "origin", Kind: KindSkill, Files: map[string][]byte{
		"SKILL.md": []byte("# Title\n\n## Usage\nRun the tool.\n"),
	}}
	lower := StackLayer{Name: "lower", Kind: KindOverlay, Operations: []Operation{
		{Op: OpAppendToFile, Target: "SKILL.md", Content: "See also: [Advanced](references/advanced.md)"},
	}}
	repo := StackLayer{Name: "my-repo", Kind: KindOverlay, Operations: []Operation{
		{Op: OpReplaceIn, Target: "SKILL.md", Pattern: "Run the tool\\.", Replacement: "Run the tool (Team Edition)."},
	}}
	m, err := Compose([]StackLayer{origin, lower, repo}, false)
	if err != nil {
		t.Fatal(err)
	}
	got := string(m.Files["SKILL.md"])
	if !strings.Contains(got, "Team Edition") {
		t.Errorf("patched Usage missing: %q", got)
	}
	if !strings.Contains(got, "See also: [Advanced]") {
		t.Errorf("appended line missing: %q", got)
	}
}

func TestRequiredReplaceNoMatchFails(t *testing.T) {
	origin := StackLayer{Name: "origin", Kind: KindSkill, Files: map[string][]byte{
		"SKILL.md": []byte("nothing to see"),
	}}
	repo := StackLayer{Name: "my-repo", Kind: KindOverlay, Operations: []Operation{
		{Op: OpReplaceIn, Target: "SKILL.md", Pattern: "DOES-NOT-EXIST", Replacement: "x", Required: true},
	}}
	_, err := Compose([]StackLayer{origin, repo}, false)
	if err == nil {
		t.Fatal("expected required-operation error")
	}
	if !strings.Contains(err.Error(), "no match") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestPatchFile(t *testing.T) {
	origin := StackLayer{Name: "origin", Kind: KindSkill, Files: map[string][]byte{
		"values.yaml": []byte("titel: PDF\ncount: 1\n"),
	}}
	diff := "--- a/values.yaml\n+++ b/values.yaml\n@@ -1,2 +1,2 @@\n-titel: PDF\n+title: PDF\n count: 1\n"
	repo := StackLayer{Name: "my-repo", Kind: KindOverlay, Operations: []Operation{
		{Op: OpPatchFile, Target: "values.yaml", Content: diff, Required: true},
	}}
	m, err := Compose([]StackLayer{origin, repo}, false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(m.Files["values.yaml"]), "title: PDF") {
		t.Errorf("patch not applied: %q", m.Files["values.yaml"])
	}
}
