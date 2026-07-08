// Package oci is the shared, domain-free OCI distribution client (the model's
// Infrastructure.OciClient, SPEC §15.1). It wraps ORAS (oras.land/oras-go/v2)
// and is reused by the Packaging, Registry, Composition, and Signing adapters.
package oci

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"

	imgspec "github.com/opencontainers/image-spec/specs-go"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2"
	"oras.land/oras-go/v2/content"
	"oras.land/oras-go/v2/content/memory"
	"oras.land/oras-go/v2/registry/remote"
	"oras.land/oras-go/v2/registry/remote/auth"
	"oras.land/oras-go/v2/registry/remote/retry"
)

// Auth is a client credential relayed to the upstream registry. Epos stores no
// secrets of its own; these come from the client's Docker credential store or,
// for read-only listing, from env-referenced values (SPEC §6.2).
type Auth struct {
	Username string
	Password string
}

// Client is a reusable OCI client.
type Client struct {
	// PlainHTTP forces http:// (used for local/test registries).
	PlainHTTP bool
	// Auth, when set, is relayed to the upstream registry.
	Auth *Auth
}

// Blob is a raw OCI blob with its media type.
type Blob struct {
	MediaType string
	Data      []byte
}

// Manifest is a pulled artifact: its manifest digest, config, and layers.
type Manifest struct {
	Digest       string
	MediaType    string
	ArtifactType string
	Config       Blob
	Layers       []Blob
	Raw          []byte
	Annotations  map[string]string
}

// Repository constructs an ORAS remote repository handle for a full reference
// (registry/repo[:tag|@digest]).
func (c *Client) Repository(ref string) (*remote.Repository, error) {
	repo, err := remote.NewRepository(ref)
	if err != nil {
		return nil, fmt.Errorf("oci: parse ref %q: %w", ref, err)
	}
	repo.PlainHTTP = c.PlainHTTP
	if c.Auth != nil {
		repo.Client = &auth.Client{
			Client: retry.DefaultClient,
			Cache:  auth.NewCache(),
			Credential: auth.StaticCredential(repo.Reference.Registry, auth.Credential{
				Username: c.Auth.Username,
				Password: c.Auth.Password,
			}),
		}
	}
	return repo, nil
}

// Push uploads a config blob and layers wrapped in an OCI image manifest, tags
// it with the reference's tag, and returns the manifest descriptor. The
// resulting manifest digest is stable for identical inputs.
func (c *Client) Push(ctx context.Context, ref, configMT string, config []byte, layers []Blob, artifactType string, annotations map[string]string) (ocispec.Descriptor, error) {
	repo, err := c.Repository(ref)
	if err != nil {
		return ocispec.Descriptor{}, err
	}
	src := memory.New()

	configDesc := content.NewDescriptorFromBytes(configMT, config)
	if err := pushIfAbsent(ctx, src, configDesc, config); err != nil {
		return ocispec.Descriptor{}, err
	}
	layerDescs := make([]ocispec.Descriptor, 0, len(layers))
	for _, l := range layers {
		d := content.NewDescriptorFromBytes(l.MediaType, l.Data)
		if err := pushIfAbsent(ctx, src, d, l.Data); err != nil {
			return ocispec.Descriptor{}, err
		}
		layerDescs = append(layerDescs, d)
	}

	man := ocispec.Manifest{
		Versioned:    imgspec.Versioned{SchemaVersion: 2},
		MediaType:    ocispec.MediaTypeImageManifest,
		ArtifactType: artifactType,
		Config:       configDesc,
		Layers:       layerDescs,
		Annotations:  annotations,
	}
	manBytes, err := json.Marshal(man)
	if err != nil {
		return ocispec.Descriptor{}, err
	}
	manDesc := content.NewDescriptorFromBytes(ocispec.MediaTypeImageManifest, manBytes)
	manDesc.ArtifactType = artifactType
	manDesc.Annotations = annotations
	if err := pushIfAbsent(ctx, src, manDesc, manBytes); err != nil {
		return ocispec.Descriptor{}, err
	}

	tag := repo.Reference.Reference
	if tag == "" {
		tag = manDesc.Digest.String()
	}
	if err := src.Tag(ctx, manDesc, tag); err != nil {
		return ocispec.Descriptor{}, err
	}
	if _, err := oras.Copy(ctx, src, tag, repo, tag, oras.DefaultCopyOptions); err != nil {
		return ocispec.Descriptor{}, fmt.Errorf("oci: push %q: %w", ref, err)
	}
	return manDesc, nil
}

// Resolve returns the descriptor a reference (tag or digest) points at.
func (c *Client) Resolve(ctx context.Context, ref string) (ocispec.Descriptor, error) {
	repo, err := c.Repository(ref)
	if err != nil {
		return ocispec.Descriptor{}, err
	}
	r := repo.Reference.Reference
	return repo.Resolve(ctx, r)
}

// Pull fetches the manifest, config, and layers for a reference.
func (c *Client) Pull(ctx context.Context, ref string) (*Manifest, error) {
	repo, err := c.Repository(ref)
	if err != nil {
		return nil, err
	}
	r := repo.Reference.Reference
	desc, err := repo.Resolve(ctx, r)
	if err != nil {
		return nil, fmt.Errorf("oci: resolve %q: %w", ref, err)
	}
	manBytes, err := content.FetchAll(ctx, repo, desc)
	if err != nil {
		return nil, err
	}
	var man ocispec.Manifest
	if err := json.Unmarshal(manBytes, &man); err != nil {
		return nil, fmt.Errorf("oci: parse manifest: %w", err)
	}
	out := &Manifest{
		Digest:       desc.Digest.String(),
		MediaType:    man.MediaType,
		ArtifactType: man.ArtifactType,
		Raw:          manBytes,
		Annotations:  man.Annotations,
	}
	cfg, err := content.FetchAll(ctx, repo, man.Config)
	if err != nil {
		return nil, err
	}
	out.Config = Blob{MediaType: man.Config.MediaType, Data: cfg}
	for _, ld := range man.Layers {
		data, err := content.FetchAll(ctx, repo, ld)
		if err != nil {
			return nil, err
		}
		out.Layers = append(out.Layers, Blob{MediaType: ld.MediaType, Data: data})
	}
	return out, nil
}

// pushWithSubject uploads an artifact manifest carrying a `subject` reference,
// attaching it to the subject descriptor via the OCI 1.1 referrers mechanism.
func (c *Client) pushWithSubject(ctx context.Context, ref, artifactType string, payload []byte, subject ocispec.Descriptor) (ocispec.Descriptor, error) {
	repo, err := c.Repository(ref)
	if err != nil {
		return ocispec.Descriptor{}, err
	}
	src := memory.New()

	emptyCfg := []byte("{}")
	cfgDesc := content.NewDescriptorFromBytes(ocispec.MediaTypeEmptyJSON, emptyCfg)
	if err := pushIfAbsent(ctx, src, cfgDesc, emptyCfg); err != nil {
		return ocispec.Descriptor{}, err
	}
	layerDesc := content.NewDescriptorFromBytes(artifactType, payload)
	if err := pushIfAbsent(ctx, src, layerDesc, payload); err != nil {
		return ocispec.Descriptor{}, err
	}
	man := ocispec.Manifest{
		Versioned:    imgspec.Versioned{SchemaVersion: 2},
		MediaType:    ocispec.MediaTypeImageManifest,
		ArtifactType: artifactType,
		Config:       cfgDesc,
		Layers:       []ocispec.Descriptor{layerDesc},
		Subject:      &subject,
	}
	manBytes, err := json.Marshal(man)
	if err != nil {
		return ocispec.Descriptor{}, err
	}
	manDesc := content.NewDescriptorFromBytes(ocispec.MediaTypeImageManifest, manBytes)
	manDesc.ArtifactType = artifactType
	if err := pushIfAbsent(ctx, src, manDesc, manBytes); err != nil {
		return ocispec.Descriptor{}, err
	}
	if err := src.Tag(ctx, manDesc, manDesc.Digest.String()); err != nil {
		return ocispec.Descriptor{}, err
	}
	if _, err := oras.Copy(ctx, src, manDesc.Digest.String(), repo, manDesc.Digest.String(), oras.DefaultCopyOptions); err != nil {
		return ocispec.Descriptor{}, fmt.Errorf("oci: push referrer %q: %w", ref, err)
	}
	return manDesc, nil
}

// pushIfAbsent tolerates AlreadyExists so repeated pushes are idempotent.
func pushIfAbsent(ctx context.Context, store content.Storage, desc ocispec.Descriptor, data []byte) error {
	err := store.Push(ctx, desc, bytes.NewReader(data))
	if err != nil && !strings.Contains(err.Error(), "already exists") {
		return err
	}
	return nil
}
