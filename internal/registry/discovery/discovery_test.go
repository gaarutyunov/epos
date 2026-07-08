package discovery

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/google/go-containerregistry/pkg/registry"

	"github.com/gaarutyunov/epos/internal/config"
	"github.com/gaarutyunov/epos/internal/infrastructure/oci"
	"github.com/gaarutyunov/epos/internal/packaging/domain"
)

func startRegistry(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(registry.New())
	t.Cleanup(srv.Close)
	u, _ := url.Parse(srv.URL)
	return u.Host
}

func pushSkill(t *testing.T, c *oci.Client, ref string) {
	t.Helper()
	cfg := []byte(`{"name":"x","version":"1.0.0"}`)
	layer := oci.Blob{MediaType: domain.MediaTypeSkillContent, Data: []byte("tar")}
	if _, err := c.Push(context.Background(), ref, domain.MediaTypeSkillConfig, cfg, []oci.Blob{layer}, domain.MediaTypeSkillConfig, nil); err != nil {
		t.Fatal(err)
	}
}

func TestProbeCatalogMode(t *testing.T) {
	host := startRegistry(t)
	c := &oci.Client{PlainHTTP: true}
	pushSkill(t, c, host+"/skills/pdf-tools:1.0.0")

	d := &Discoverer{Client: c}
	mode := d.Probe(context.Background(), config.Registry{Name: "r", URL: "http://" + host})
	if mode != config.DiscoveryCatalog {
		t.Fatalf("mode = %q, want catalog", mode)
	}
}

func TestFallBackToRegisteredOn404(t *testing.T) {
	// A registry that returns 404 for _catalog forces registered fallback.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)

	d := &Discoverer{Client: &oci.Client{PlainHTTP: true}}
	entry := config.Registry{Name: "r", URL: srv.URL, Repositories: []string{"skills/only-this"}}
	res, err := d.Discover(context.Background(), entry)
	if err != nil {
		t.Fatal(err)
	}
	if res.Mode != config.DiscoveryRegistered {
		t.Fatalf("mode = %q, want registered", res.Mode)
	}
	if len(res.Repos) != 1 || res.Repos[0] != "skills/only-this" {
		t.Errorf("registered repos = %v", res.Repos)
	}
}

func TestDiscoverFiltersToSkills(t *testing.T) {
	host := startRegistry(t)
	c := &oci.Client{PlainHTTP: true}
	pushSkill(t, c, host+"/skills/pdf-tools:1.0.0")
	pushSkill(t, c, host+"/skills/csv-tools:1.0.0")
	// A non-skill artifact (different config media type) must be filtered out.
	if _, err := c.Push(context.Background(), host+"/other/not-a-skill:1.0.0",
		"application/vnd.oci.image.config.v1+json", []byte(`{}`),
		[]oci.Blob{{MediaType: "application/octet-stream", Data: []byte("x")}}, "", nil); err != nil {
		t.Fatal(err)
	}

	d := &Discoverer{Client: c}
	res, err := d.Discover(context.Background(), config.Registry{Name: "r", URL: "http://" + host})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, r := range res.Repos {
		got[r] = true
	}
	if !got["skills/pdf-tools"] || !got["skills/csv-tools"] {
		t.Errorf("expected skills discovered, got %v", res.Repos)
	}
	if got["other/not-a-skill"] {
		t.Error("non-skill artifact was not filtered out")
	}
}
