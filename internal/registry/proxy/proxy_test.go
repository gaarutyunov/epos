package proxy

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/google/go-containerregistry/pkg/registry"

	"github.com/gaarutyunov/epos/internal/infrastructure/oci"
	"github.com/gaarutyunov/epos/internal/packaging/domain"
	"github.com/gaarutyunov/epos/internal/stats"
)

func upstreamWithSkill(t *testing.T) (string, string) {
	t.Helper()
	srv := httptest.NewServer(registry.New())
	t.Cleanup(srv.Close)
	u, _ := url.Parse(srv.URL)
	c := &oci.Client{PlainHTTP: true}
	cfg := []byte(`{"name":"pdf-tools","version":"1.4.2"}`)
	layer := oci.Blob{MediaType: domain.MediaTypeSkillContent, Data: []byte("tar")}
	if _, err := c.Push(context.Background(), u.Host+"/skills/pdf-tools:1.4.2", domain.MediaTypeSkillConfig, cfg, []oci.Blob{layer}, domain.MediaTypeSkillConfig, nil); err != nil {
		t.Fatal(err)
	}
	return srv.URL, u.Host
}

func TestProxyRelaysCatalogAndCountsManifestGets(t *testing.T) {
	upstream, _ := upstreamWithSkill(t)
	counter := stats.New()
	p, err := New(upstream, counter)
	if err != nil {
		t.Fatal(err)
	}
	front := httptest.NewServer(p)
	t.Cleanup(front.Close)

	// Catalog listing relays through the proxy.
	body := get(t, front.URL+"/v2/_catalog")
	if !strings.Contains(body, "pdf-tools") {
		t.Errorf("catalog listing missing skill: %q", body)
	}

	// A manifest GET is counted; a HEAD is not.
	get(t, front.URL+"/v2/skills/pdf-tools/manifests/1.4.2")
	head(t, front.URL+"/v2/skills/pdf-tools/manifests/1.4.2")
	if counter.Total() != 1 {
		t.Errorf("counted %d manifest GETs, want 1", counter.Total())
	}

	// The proxy persists no credentials (transparent pass-through, SPEC §6.1).
	if p.PersistedCredentials() != 0 {
		t.Errorf("proxy persisted %d credentials, want 0", p.PersistedCredentials())
	}
}

func get(t *testing.T, url string) string {
	t.Helper()
	resp, err := http.Get(url) //nolint:gosec,noctx // test
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return string(b)
}

func head(t *testing.T, url string) {
	t.Helper()
	resp, err := http.Head(url) //nolint:gosec,noctx // test
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
}
