package oci

import (
	"context"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/google/go-containerregistry/pkg/registry"
)

// InProcessRegistry starts a pure-Go in-memory OCI registry (no docker) and
// returns its host:port. Used to verify push/pull round-trips locally; CI uses
// a real zot container via testcontainers (SPEC §15.3).
func InProcessRegistry(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(registry.New())
	t.Cleanup(srv.Close)
	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	return u.Host
}

func TestPushPullRoundTrip(t *testing.T) {
	host := InProcessRegistry(t)
	c := &Client{PlainHTTP: true}
	ctx := context.Background()
	ref := host + "/skills/pdf-tools:1.4.2"

	config := []byte(`{"name":"pdf-tools","version":"1.4.2"}`)
	layer := Blob{MediaType: "application/vnd.epos.skill.content.v1.tar+gzip", Data: []byte("tarball-bytes")}

	desc, err := c.Push(ctx, ref, "application/vnd.epos.skill.config.v1+json", config, []Blob{layer}, "application/vnd.epos.skill.config.v1+json", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(desc.Digest.String(), "sha256:") {
		t.Fatalf("bad digest %q", desc.Digest)
	}

	// Tag resolves to the pushed manifest digest.
	rd, err := c.Resolve(ctx, ref)
	if err != nil {
		t.Fatal(err)
	}
	if rd.Digest != desc.Digest {
		t.Errorf("tag digest %q != pushed digest %q", rd.Digest, desc.Digest)
	}

	man, err := c.Pull(ctx, ref)
	if err != nil {
		t.Fatal(err)
	}
	if string(man.Config.Data) != string(config) {
		t.Errorf("config mismatch: %q", man.Config.Data)
	}
	if len(man.Layers) != 1 || string(man.Layers[0].Data) != "tarball-bytes" {
		t.Errorf("layer mismatch: %+v", man.Layers)
	}
	if man.Config.MediaType != "application/vnd.epos.skill.config.v1+json" {
		t.Errorf("config media type %q", man.Config.MediaType)
	}
}
