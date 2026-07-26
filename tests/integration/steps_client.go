//go:build integration

package integration

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2"
	"oras.land/oras-go/v2/content"
	"oras.land/oras-go/v2/content/memory"
	"oras.land/oras-go/v2/registry/remote"
)

// pulledArtifact is what a client got back through epos-registry.
type pulledArtifact struct {
	manifest string
	store    *memory.Store
}

// orasPullsThrough pulls a skill through epos-registry with a stock oras
// client, configured no differently from one pointed at a plain registry
// (SPEC.md 4.1 — pointing oras at epos-registry requires no client changes).
//
// "Real oras" here is oras-go, the same library the push side of this suite
// uses and the one SPEC.md 5.2 measures stock client behaviour against. No Epos
// header is set: this is the unmodified client.
func (w *world) orasPullsThrough(ref string) error {
	if w.registryURL == "" {
		return fmt.Errorf("epos-registry is not running")
	}

	name, version, ok := strings.Cut(ref, ":")
	if !ok {
		return fmt.Errorf("reference %q is not <name>:<version>", ref)
	}

	repo, err := remote.NewRepository(strings.TrimPrefix(w.registryURL, "http://") + "/" + name)
	if err != nil {
		return fmt.Errorf("epos-registry repository: %w", err)
	}
	repo.PlainHTTP = true

	ctx := context.Background()
	store := memory.New()
	desc, err := oras.Copy(ctx, repo, version, store, version, oras.DefaultCopyOptions)
	if err != nil {
		return fmt.Errorf("oras pull through epos-registry: %w", err)
	}

	w.pulled = &pulledArtifact{manifest: desc.Digest.String(), store: store}
	w.lastBlobRef = ref
	return nil
}

// pulledArtifactMatchesUpstream compares what came through epos-registry with
// what was pushed to the upstream: same manifest digest, and a content layer
// with the same bytes.
func (w *world) pulledArtifactMatchesUpstream() error {
	if w.pulled == nil {
		return fmt.Errorf("nothing was pulled")
	}

	wantManifest, ok := w.pushed[w.lastBlobRef]
	if !ok {
		return fmt.Errorf("no manifest digest recorded for %s", w.lastBlobRef)
	}
	if w.pulled.manifest != wantManifest {
		return fmt.Errorf("pulled manifest %s, want %s", w.pulled.manifest, wantManifest)
	}

	layer, ok := w.layers[w.lastBlobRef]
	if !ok {
		return fmt.Errorf("no content layer recorded for %s", w.lastBlobRef)
	}
	got, err := content.FetchAll(context.Background(), w.pulled.store, ocispec.Descriptor{
		MediaType: "application/vnd.agentskills.skill.layer.v1.tar",
		Digest:    digest.Digest(layer.digest),
		Size:      int64(len(layer.bytes)),
	})
	if err != nil {
		return fmt.Errorf("read the pulled content layer: %w", err)
	}
	if !bytes.Equal(got, layer.bytes) {
		return fmt.Errorf("pulled content = %q, want %q", got, layer.bytes)
	}
	return nil
}

// --- replicas (SPEC.md 4.4) -------------------------------------------------

// resolveManifestAgainst issues a manifest GET against one of the replicas.
func (w *world) resolveManifestAgainst(ref, which string) error {
	base, err := w.replica(which)
	if err != nil {
		return err
	}

	name, version, ok := strings.Cut(ref, ":")
	if !ok {
		return fmt.Errorf("reference %q is not <name>:<version>", ref)
	}
	return w.recordStatus(base, http.MethodGet, "/v2/"+name+"/manifests/"+version)
}

// fetchContentBlobAgainst issues a blob GET against one of the replicas.
func (w *world) fetchContentBlobAgainst(ref, which string) error {
	base, err := w.replica(which)
	if err != nil {
		return err
	}

	name, _, ok := strings.Cut(ref, ":")
	if !ok {
		return fmt.Errorf("reference %q is not <name>:<version>", ref)
	}
	layer, ok := w.layers[ref]
	if !ok {
		return fmt.Errorf("no content layer recorded for %s", ref)
	}
	return w.recordStatus(base, http.MethodGet, "/v2/"+name+"/blobs/"+layer.digest)
}

func (w *world) replica(which string) (string, error) {
	switch which {
	case "first":
		if w.registryURL == "" {
			return "", fmt.Errorf("the first replica is not running")
		}
		return w.registryURL, nil
	case "second":
		if w.replicaURL == "" {
			return "", fmt.Errorf("the second replica is not running")
		}
		return w.replicaURL, nil
	default:
		return "", fmt.Errorf("unknown replica %q", which)
	}
}

// recordStatus performs a request and remembers its status, so a scenario can
// assert over every request it made rather than only the last.
func (w *world) recordStatus(base, method, target string) error {
	req, err := http.NewRequest(method, base+target, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", ocispec.MediaTypeImageManifest)

	// Redirects are not followed: a 307 is epos-registry answering successfully
	// (SPEC.md 4.2), and chasing it would test the redirect target instead.
	client := &http.Client{
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	w.statuses = append(w.statuses, resp.StatusCode)
	return nil
}

// bothRequestsSucceed asserts every request the scenario made was answered.
func (w *world) bothRequestsSucceed() error {
	if len(w.statuses) < 2 {
		return fmt.Errorf("expected at least 2 requests, got %d", len(w.statuses))
	}
	for i, status := range w.statuses {
		if status != http.StatusOK && status != http.StatusTemporaryRedirect {
			return fmt.Errorf("request %d was answered %d, want 200 or 307", i+1, status)
		}
	}
	return nil
}
