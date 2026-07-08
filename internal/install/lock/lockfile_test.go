package lock

import (
	"path/filepath"
	"testing"
)

func TestLockfileRevisionsAndRollback(t *testing.T) {
	path := filepath.Join(t.TempDir(), LockfileName)
	lf := New(path)

	r1 := Revision{Version: "1.4.2", Digest: "sha256:aaa", Registry: "reg/skills/pdf-tools"}
	r1.SetFiles(map[string][]byte{"SKILL.md": []byte("v1")})
	n1 := lf.AddRevision("pdf", r1)
	if n1 != 1 {
		t.Fatalf("first revision = %d", n1)
	}

	r2 := Revision{Version: "1.5.0", Digest: "sha256:bbb", Registry: "reg/skills/pdf-tools"}
	r2.SetFiles(map[string][]byte{"SKILL.md": []byte("v2")})
	n2 := lf.AddRevision("pdf", r2)
	if n2 != 2 {
		t.Fatalf("second revision = %d", n2)
	}
	if len(lf.History("pdf")) != 2 {
		t.Fatalf("history len = %d", len(lf.History("pdf")))
	}

	if err := lf.Save(); err != nil {
		t.Fatal(err)
	}
	reloaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}

	// Rollback to revision 1 restores the whole bundle and records a new
	// revision whose content equals revision 1 (SPEC §5.3).
	rev1, err := reloaded.Get("pdf", 1)
	if err != nil {
		t.Fatal(err)
	}
	restore := Revision{Version: rev1.Version, Digest: rev1.Digest, Registry: rev1.Registry, Files: rev1.Files}
	n3 := reloaded.AddRevision("pdf", restore)
	if n3 != 3 {
		t.Fatalf("rollback revision = %d", n3)
	}
	cur, _ := reloaded.Current("pdf")
	f, _ := cur.FileBytes()
	if string(f["SKILL.md"]) != "v1" {
		t.Errorf("rollback content = %q, want v1", f["SKILL.md"])
	}
	if cur.Version != "1.4.2" {
		t.Errorf("rollback version = %q", cur.Version)
	}
}

func TestRetentionTrim(t *testing.T) {
	lf := New(filepath.Join(t.TempDir(), LockfileName))
	lf.SetRetention(3)
	for i := 0; i < 6; i++ {
		lf.AddRevision("pdf", Revision{Version: "1.0.0", Digest: "sha256:x"})
	}
	h := lf.History("pdf")
	if len(h) != 3 {
		t.Fatalf("retained = %d, want 3", len(h))
	}
	if h[0].Revision != 4 || h[2].Revision != 6 {
		t.Errorf("retained window = %d..%d", h[0].Revision, h[2].Revision)
	}
}
