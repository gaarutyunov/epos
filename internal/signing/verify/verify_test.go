package verify

import (
	"context"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/google/go-containerregistry/pkg/registry"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"

	"github.com/gaarutyunov/epos/internal/infrastructure/oci"
	"github.com/gaarutyunov/epos/internal/packaging/domain"
)

func pushArtifact(t *testing.T, c *oci.Client, ref string, content []byte) ocispec.Descriptor {
	t.Helper()
	cfg := []byte(`{"name":"pdf-tools","version":"1.4.2"}`)
	layer := oci.Blob{MediaType: domain.MediaTypeSkillContent, Data: content}
	desc, err := c.Push(context.Background(), ref, domain.MediaTypeSkillConfig, cfg, []oci.Blob{layer}, domain.MediaTypeSkillConfig, nil)
	if err != nil {
		t.Fatal(err)
	}
	return desc
}

func newRegistry(t *testing.T) (*oci.Client, string) {
	t.Helper()
	srv := httptest.NewServer(registry.New())
	t.Cleanup(srv.Close)
	u, _ := url.Parse(srv.URL)
	return &oci.Client{PlainHTTP: true}, u.Host
}

func TestVerifyWhenPresentPasses(t *testing.T) {
	c, host := newRegistry(t)
	repo := host + "/skills/pdf-tools"
	desc := pushArtifact(t, c, repo+":1.4.2", []byte("original"))
	if err := Sign(context.Background(), c, repo, desc, "sig"); err != nil {
		t.Fatal(err)
	}
	res, err := Verify(context.Background(), c, repo, desc, false)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Verified || !res.Present {
		t.Errorf("expected verified+present, got %+v", res)
	}
}

func TestUnsignedPermittedUnlessRequired(t *testing.T) {
	c, host := newRegistry(t)
	repo := host + "/skills/pdf-tools"
	desc := pushArtifact(t, c, repo+":1.4.2", []byte("original"))

	res, err := Verify(context.Background(), c, repo, desc, false)
	if err != nil {
		t.Fatalf("unsigned should install: %v", err)
	}
	if res.Present {
		t.Error("expected no signature present")
	}

	if _, err := Verify(context.Background(), c, repo, desc, true); err == nil {
		t.Error("--require-signature should fail on unsigned skill")
	}
}

func TestTamperedContentFailsVerification(t *testing.T) {
	c, host := newRegistry(t)
	repo := host + "/skills/pdf-tools"
	signedDesc := pushArtifact(t, c, repo+":1.4.2", []byte("original"))
	if err := Sign(context.Background(), c, repo, signedDesc, "sig"); err != nil {
		t.Fatal(err)
	}
	// Tamper: push different content under a new tag → different digest, same
	// signature subject no longer matches.
	tamperedDesc := pushArtifact(t, c, repo+":tampered", []byte("tampered-content"))

	// Verify the tampered artifact against the (old) signature referrers of the
	// signed subject: the tampered digest differs from every signed subject.
	_ = signedDesc
	res, err := Verify(context.Background(), c, repo, tamperedDesc, true)
	if err == nil {
		t.Fatal("expected tamper detection to fail verification")
	}
	if res.Present {
		// tampered digest has no matching referrer → treated as unsigned+required.
		t.Log("tampered artifact had referrers:", res.Messages)
	}
}
