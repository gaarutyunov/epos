//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2"
	"oras.land/oras-go/v2/content/memory"
	"oras.land/oras-go/v2/registry/remote"
)

// skillArtifactType is the conformant agent-skill artifact type (SPEC.md 2.1).
const skillArtifactType = "application/vnd.agentskills.skill.v1"

// pushSkill publishes a small artifact to the upstream registry with real oras,
// so the read path is exercised against content a real client produced.
func (w *world) pushSkill(ctx context.Context, name, version string) error {
	if w.upstreamURL == "" {
		return fmt.Errorf("upstream is not running")
	}

	store := memory.New()

	content := []byte("SKILL.md for " + name + " " + version)
	layer, err := oras.PushBytes(ctx, store,
		"application/vnd.agentskills.skill.layer.v1.tar", content)
	if err != nil {
		return fmt.Errorf("push layer: %w", err)
	}

	manifest, err := oras.PackManifest(ctx, store, oras.PackManifestVersion1_1,
		skillArtifactType, oras.PackManifestOptions{
			Layers: []ocispec.Descriptor{layer},
		})
	if err != nil {
		return fmt.Errorf("pack manifest: %w", err)
	}
	if err := store.Tag(ctx, manifest, version); err != nil {
		return fmt.Errorf("tag manifest: %w", err)
	}

	repo, err := w.upstreamRepo(name)
	if err != nil {
		return err
	}
	if _, err := oras.Copy(ctx, store, version, repo, version, oras.DefaultCopyOptions); err != nil {
		return fmt.Errorf("copy to upstream: %w", err)
	}

	w.pushed[name+":"+version] = manifest.Digest.String()
	w.layers[name+":"+version] = blob{digest: layer.Digest.String(), bytes: content}
	return nil
}

func (w *world) upstreamRepo(name string) (*remote.Repository, error) {
	host := strings.TrimPrefix(w.upstreamURL, "http://")
	repo, err := remote.NewRepository(host + "/" + name)
	if err != nil {
		return nil, fmt.Errorf("upstream repository: %w", err)
	}
	repo.PlainHTTP = true
	return repo, nil
}

// resolveManifest fetches a manifest through epos-registry.
func (w *world) resolveManifest(method, ref string) error {
	name, version, ok := strings.Cut(ref, ":")
	if !ok {
		return fmt.Errorf("reference %q is not <name>:<version>", ref)
	}
	return w.requestWithHeaders(method,
		"/v2/"+name+"/manifests/"+version,
		map[string]string{"Accept": ocispec.MediaTypeImageManifest})
}

func (w *world) listTags(name string) error {
	return w.request(http.MethodGet, "/v2/"+name+"/tags/list")
}

func (w *world) listReferrers(ref string) error {
	name, version, ok := strings.Cut(ref, ":")
	if !ok {
		return fmt.Errorf("reference %q is not <name>:<version>", ref)
	}
	digest, ok := w.pushed[name+":"+version]
	if !ok {
		return fmt.Errorf("no digest recorded for %s", ref)
	}
	return w.request(http.MethodGet, "/v2/"+name+"/referrers/"+digest)
}

// digestMatchesUpstream checks the manifest epos-registry returned is the one
// upstream holds. Docker-Content-Digest is the registry's own answer.
func (w *world) digestMatchesUpstream() error {
	if w.resp == nil {
		return fmt.Errorf("no response recorded")
	}

	got := w.resp.Header.Get("Docker-Content-Digest")
	if got == "" {
		return fmt.Errorf("response carries no Docker-Content-Digest")
	}

	for _, want := range w.pushed {
		if got == want {
			return nil
		}
	}
	return fmt.Errorf("digest %q matches nothing pushed upstream (%v)", got, w.pushed)
}

func (w *world) noBodyReturned() error {
	if len(w.respBody) != 0 {
		return fmt.Errorf("expected no body, got %d bytes", len(w.respBody))
	}
	return nil
}

func (w *world) tagListContains(a, b string) error {
	var payload struct {
		Tags []string `json:"tags"`
	}
	if err := json.Unmarshal(w.respBody, &payload); err != nil {
		return fmt.Errorf("decode tag list: %w (body %q)", err, string(w.respBody))
	}

	for _, want := range []string{a, b} {
		found := false
		for _, tag := range payload.Tags {
			if tag == want {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("tag %q missing from %v", want, payload.Tags)
		}
	}
	return nil
}
