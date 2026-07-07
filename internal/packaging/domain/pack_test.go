package domain

import (
	"os"
	"path/filepath"
	"testing"
)

func writeSkill(t *testing.T, name string, files map[string]string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), name)
	for p, c := range files {
		full := filepath.Join(dir, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(c), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

const validEpos = `apiVersion: epos/v1
name: pdf-tools
version: 1.4.2
description: Extract and manipulate PDFs
`

func TestBuildArtifact(t *testing.T) {
	dir := writeSkill(t, "pdf-tools", map[string]string{
		"Epos.yaml":   validEpos,
		"SKILL.md":    "---\nname: pdf-tools\ndescription: x\n---\nbody\n",
		"values.yaml": "features: {}\n",
	})
	art, err := BuildArtifact(dir)
	if err != nil {
		t.Fatal(err)
	}
	if art.Config.MediaType != MediaTypeSkillConfig {
		t.Errorf("config media type = %q", art.Config.MediaType)
	}
	if art.Content.MediaType != MediaTypeSkillContent {
		t.Errorf("content media type = %q", art.Content.MediaType)
	}
	if art.Tag != "1.4.2" {
		t.Errorf("tag = %q", art.Tag)
	}
	if art.ManifestDigest() == "" {
		t.Error("empty manifest digest")
	}
	// Reproducible: rebuild yields the same manifest digest.
	art2, _ := BuildArtifact(dir)
	if art.ManifestDigest() != art2.ManifestDigest() {
		t.Error("build is not reproducible")
	}
}

func TestValidateManifestRejectsReservedName(t *testing.T) {
	m := &Manifest{Name: "Anthropic-PDF", Version: "1.0.0", Description: "x"}
	msgs := ValidateManifest(m, "Anthropic-PDF")
	var lower, reserved bool
	for _, s := range msgs {
		if contains(s, "lowercase") {
			lower = true
		}
		if contains(s, "anthropic") {
			reserved = true
		}
	}
	if !lower || !reserved {
		t.Errorf("expected lowercase + anthropic messages, got %v", msgs)
	}
}

func TestLintDanglingReference(t *testing.T) {
	dir := writeSkill(t, "pdf-tools", map[string]string{
		"Epos.yaml": validEpos,
		"SKILL.md":  "See also: [Advanced](references/missing.md)\n",
	})
	msgs, err := LintDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, s := range msgs {
		if contains(s, "references/missing.md") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected dangling reference message, got %v", msgs)
	}
}

func TestOCITagRewrite(t *testing.T) {
	if got := OCITag("1.4.2+build.5"); got != "1.4.2_build.5" {
		t.Errorf("OCITag = %q", got)
	}
	if got := VersionFromOCITag("1.4.2_build.5"); got != "1.4.2+build.5" {
		t.Errorf("VersionFromOCITag = %q", got)
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (func() bool {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	})()
}
