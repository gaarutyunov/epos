package lock

import (
	"path/filepath"
	"testing"
)

func TestSaveLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	pins := []LayerPin{
		{Name: "base-pdf", Kind: "skill", SourceType: "oci", Source: "reg/pdf", Version: "1.4.2", Digest: "sha256:abc"},
		{Name: "shared", Kind: "skill", SourceType: "git", Source: "git://x", Version: "v2", Commit: "c0ffee", TreeSha: "beef", Subpath: "skills/shared"},
	}
	if err := New(pins).Save(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := filepath.Glob(filepath.Join(dir, "Epos.lock")); err != nil {
		t.Fatal(err)
	}
	lf, err := Load(dir)
	if err != nil || lf == nil {
		t.Fatalf("load: %v", err)
	}
	if len(lf.Layers) != 2 || lf.Layers[0].Name != "base-pdf" {
		t.Fatalf("unexpected layers: %+v", lf.Layers)
	}
}

func TestVerifyDetectsMismatch(t *testing.T) {
	lf := New([]LayerPin{{Name: "a", Digest: "sha256:GOOD"}})
	if err := lf.Verify([]LayerPin{{Name: "a", Digest: "sha256:GOOD"}}); err != nil {
		t.Errorf("matching pins should verify: %v", err)
	}
	if err := lf.Verify([]LayerPin{{Name: "a", Digest: "sha256:BAD"}}); err == nil {
		t.Error("digest mismatch must be a hard error")
	}
	if err := lf.Verify([]LayerPin{{Name: "other", Digest: "sha256:GOOD"}}); err == nil {
		t.Error("missing locked layer must be a hard error")
	}
}
