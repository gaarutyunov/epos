package oci

import (
	"context"
	"encoding/json"
	"fmt"

	imgspec "github.com/opencontainers/image-spec/specs-go"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2/content"
	orasoci "oras.land/oras-go/v2/content/oci"
)

// WriteLayout writes a config blob + layers, wrapped in an OCI image manifest,
// to an OCI image layout directory (oci-layout + index.json + blobs), tagged
// with tag. This is the on-disk form produced by `epos package`.
func WriteLayout(ctx context.Context, dir, configMT string, config []byte, layers []Blob, artifactType, tag string, annotations map[string]string) (ocispec.Descriptor, error) {
	store, err := orasoci.New(dir)
	if err != nil {
		return ocispec.Descriptor{}, err
	}
	configDesc := content.NewDescriptorFromBytes(configMT, config)
	if err := pushIfAbsent(ctx, store, configDesc, config); err != nil {
		return ocispec.Descriptor{}, err
	}
	layerDescs := make([]ocispec.Descriptor, 0, len(layers))
	for _, l := range layers {
		d := content.NewDescriptorFromBytes(l.MediaType, l.Data)
		if err := pushIfAbsent(ctx, store, d, l.Data); err != nil {
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
	if err := pushIfAbsent(ctx, store, manDesc, manBytes); err != nil {
		return ocispec.Descriptor{}, err
	}
	if err := store.Tag(ctx, manDesc, tag); err != nil {
		return ocispec.Descriptor{}, err
	}
	return manDesc, nil
}

// ReadLayout reads the tagged manifest, config, and layers from an OCI layout.
func ReadLayout(ctx context.Context, dir, tag string) (*Manifest, error) {
	store, err := orasoci.New(dir)
	if err != nil {
		return nil, err
	}
	desc, err := store.Resolve(ctx, tag)
	if err != nil {
		return nil, err
	}
	manBytes, err := content.FetchAll(ctx, store, desc)
	if err != nil {
		return nil, err
	}
	var man ocispec.Manifest
	if err := json.Unmarshal(manBytes, &man); err != nil {
		return nil, fmt.Errorf("parse layout manifest: %w", err)
	}
	out := &Manifest{Digest: desc.Digest.String(), MediaType: man.MediaType, ArtifactType: man.ArtifactType, Raw: manBytes, Annotations: man.Annotations}
	cfg, err := content.FetchAll(ctx, store, man.Config)
	if err != nil {
		return nil, err
	}
	out.Config = Blob{MediaType: man.Config.MediaType, Data: cfg}
	for _, ld := range man.Layers {
		data, err := content.FetchAll(ctx, store, ld)
		if err != nil {
			return nil, err
		}
		out.Layers = append(out.Layers, Blob{MediaType: ld.MediaType, Data: data})
	}
	return out, nil
}

// DescriptorFor returns the descriptor of pre-built manifest bytes.
func DescriptorFor(mediaType string, data []byte) ocispec.Descriptor {
	return content.NewDescriptorFromBytes(mediaType, data)
}
