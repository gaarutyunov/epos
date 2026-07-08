package usecase_test

import (
	"context"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/go-containerregistry/pkg/registry"

	"github.com/gaarutyunov/epos/internal/infrastructure/oci"
	gw "github.com/gaarutyunov/epos/internal/install/adapter/out/gateway"
	iin "github.com/gaarutyunov/epos/internal/install/app/port/in"
	"github.com/gaarutyunov/epos/internal/install/app/usecase"
	idomain "github.com/gaarutyunov/epos/internal/install/domain"
	pkgdomain "github.com/gaarutyunov/epos/internal/packaging/domain"
)

// TestInstallFlowThroughPorts drives the InstallSkill / ReadHistory /
// RollbackSkill interactors end-to-end through the MaterializePort and
// RevisionStore driven ports — proving the sysgo-scaffolded hexagon is wired and
// functional (SPEC §5, §15.4), against a real in-process OCI registry.
func TestInstallFlowThroughPorts(t *testing.T) {
	// Publish a skill to an in-process registry.
	srv := httptest.NewServer(registry.New())
	t.Cleanup(srv.Close)
	host := mustHost(t, srv.URL)
	client := &oci.Client{PlainHTTP: true}

	work := t.TempDir()
	skillDir := filepath.Join(work, "pdf-tools")
	writeFiles(t, skillDir, map[string]string{
		"Epos.yaml": "apiVersion: epos/v1\nname: pdf-tools\nversion: 1.4.2\ndescription: x\n",
		"SKILL.md":  "---\nname: pdf-tools\ndescription: x\n---\nv1 body\n",
	})
	art, err := pkgdomain.BuildArtifact(skillDir)
	if err != nil {
		t.Fatal(err)
	}
	ref := host + "/pdf-tools:1.4.2"
	if _, err := client.Push(context.Background(), ref, pkgdomain.MediaTypeSkillConfig, art.Config.Data,
		[]oci.Blob{{MediaType: art.Content.MediaType, Data: art.Content.Data}}, pkgdomain.MediaTypeSkillConfig, nil); err != nil {
		t.Fatal(err)
	}

	// Wire the hexagon: driven adapters → use-case interactors.
	mat := gw.NewMaterializePortImpl(work, client, nil)
	store := gw.NewRevisionStoreImpl(work, nil)
	install := usecase.NewInstallSkillInteractor(mat, store)
	history := usecase.NewReadHistoryInteractor(store, "files", "")
	rollback := usecase.NewRollbackSkillInteractor(mat, store, "files", "")

	req := iin.InstallSkillInput{Request: idomain.InstallRequest{
		ReleaseName: "pdf", SkillID: ref, Target: idomain.Target{Value: "files"},
	}}

	// Install twice → revisions 1 and 2.
	out1, err := install.InstallSkill(req)
	if err != nil {
		t.Fatal(err)
	}
	if out1.Result.Revision != 1 {
		t.Fatalf("first install revision = %d", out1.Result.Revision)
	}
	if _, err := os.Stat(filepath.Join(work, "pdf", "SKILL.md")); err != nil {
		t.Fatalf("skill not materialized: %v", err)
	}
	out2, err := install.InstallSkill(req)
	if err != nil || out2.Result.Revision != 2 {
		t.Fatalf("second install revision = %d (%v)", out2.Result.Revision, err)
	}

	// History shows both.
	h, err := history.ReadHistory(iin.ReadHistoryInput{ReleaseName: "pdf"})
	if err != nil || h.Result.Revision != 2 {
		t.Fatalf("history current = %d (%v)", h.Result.Revision, err)
	}

	// Rollback to revision 1 records a new revision 3 restoring the bundle.
	rb, err := rollback.RollbackSkill(iin.RollbackSkillInput{Request: idomain.RollbackRequest{ReleaseName: "pdf", ToRevision: 1}})
	if err != nil {
		t.Fatal(err)
	}
	if rb.Result.Revision != 3 {
		t.Fatalf("rollback recorded revision = %d", rb.Result.Revision)
	}
}

func mustHost(t *testing.T, raw string) string {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return u.Host
}

func writeFiles(t *testing.T, dir string, files map[string]string) {
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
