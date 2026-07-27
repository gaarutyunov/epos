package sign

import (
	"bytes"
	"context"
	"crypto/ecdsa"

	// go-digest resolves algorithms through a registry each hash populates in
	// its init, so a binary that never imports sha256 elsewhere panics on the
	// first digest.
	_ "crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/opencontainers/image-spec/specs-go"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2/content"
	"oras.land/oras-go/v2/errdef"
)

// Sign attaches a cosign signature to subject and returns the referrer's
// descriptor (SPEC.md 11).
//
// dockerReference is the "<registry>/<repository>" the signature records as
// the identity it was made against. It is descriptive: the binding that
// matters is critical.image.docker-manifest-digest, which verification checks
// against the digest the reference resolves to.
//
// dst is the registry the skill itself lives in. Signatures are referrers of
// the skill manifest, so they can live nowhere else — and writing them is a
// plain push to the upstream registry, which is where SPEC.md 4.5 already
// sends every publish.
func Sign(ctx context.Context, dst content.Storage, dockerReference string,
	subject ocispec.Descriptor, key *ecdsa.PrivateKey) (ocispec.Descriptor, error) {
	body, err := json.Marshal(payload{
		Critical: critical{
			Identity: identity{DockerReference: dockerReference},
			Image:    image{DockerManifestDigest: subject.Digest.String()},
			Type:     simpleSigningType,
		},
	})
	if err != nil {
		return ocispec.Descriptor{}, fmt.Errorf("encode signing payload: %w", err)
	}

	sig, err := signBytes(key, body)
	if err != nil {
		return ocispec.Descriptor{}, err
	}

	layer := content.NewDescriptorFromBytes(SimpleSigningMediaType, body)
	layer.Annotations = map[string]string{
		SignatureAnnotation: base64.StdEncoding.EncodeToString(sig),
	}
	return attach(ctx, dst, SignatureArtifactType, subject, layer, body)
}

// Attest attaches an in-toto attestation, in a DSSE envelope, to subject.
//
// predicate is the caller's JSON document and predicateType names its schema.
// The statement's subject is the skill manifest's digest, so an envelope
// lifted onto another artifact fails the same check a lifted signature does.
func Attest(ctx context.Context, dst content.Storage, name string,
	subject ocispec.Descriptor, predicateType string, predicate json.RawMessage,
	key *ecdsa.PrivateKey) (ocispec.Descriptor, error) {
	if predicateType == "" {
		predicateType = DefaultPredicateType
	}
	if len(predicate) == 0 {
		predicate = json.RawMessage("{}")
	}
	if !json.Valid(predicate) {
		return ocispec.Descriptor{}, errors.New("predicate is not valid JSON")
	}

	statement, err := json.Marshal(Statement{
		Type: StatementType,
		Subject: []Subject{{
			Name:   name,
			Digest: map[string]string{subject.Digest.Algorithm().String(): subject.Digest.Encoded()},
		}},
		PredicateType: predicateType,
		Predicate:     predicate,
	})
	if err != nil {
		return ocispec.Descriptor{}, fmt.Errorf("encode statement: %w", err)
	}

	sig, err := signBytes(key, pae(InTotoPayloadType, statement))
	if err != nil {
		return ocispec.Descriptor{}, err
	}

	body, err := json.Marshal(Envelope{
		PayloadType: InTotoPayloadType,
		Payload:     base64.StdEncoding.EncodeToString(statement),
		Signatures:  []EnvelopeSignature{{Sig: base64.StdEncoding.EncodeToString(sig)}},
	})
	if err != nil {
		return ocispec.Descriptor{}, fmt.Errorf("encode envelope: %w", err)
	}

	layer := content.NewDescriptorFromBytes(DSSEMediaType, body)
	return attach(ctx, dst, AttestationArtifactType, subject, layer, body)
}

// attach writes the one-layer referrer manifest both kinds share.
//
// The manifest carries no org.opencontainers.image.created annotation. cosign
// stamps one; doing the same here would make two signatures of one artifact
// differ in a field that says nothing, and SPEC.md 2.4's objection to
// timestamps in manifests does not stop at the skill.
func attach(ctx context.Context, dst content.Storage, artifactType string,
	subject, layer ocispec.Descriptor, layerBody []byte) (ocispec.Descriptor, error) {
	if subject.Digest == "" {
		return ocispec.Descriptor{}, errors.New("subject has no digest")
	}

	if err := push(ctx, dst, ocispec.DescriptorEmptyJSON, ocispec.DescriptorEmptyJSON.Data); err != nil {
		return ocispec.Descriptor{}, fmt.Errorf("write empty config: %w", err)
	}
	if err := push(ctx, dst, layer, layerBody); err != nil {
		return ocispec.Descriptor{}, fmt.Errorf("write %s: %w", layer.MediaType, err)
	}

	// Reduced to the three fields a subject needs. Whatever the caller's
	// descriptor also carried — inlined data, annotations, a platform — is not
	// part of what is being referred to, and copying it into the manifest
	// makes the referrer's digest depend on how the subject was obtained.
	ref := ocispec.Descriptor{
		MediaType: subject.MediaType,
		Digest:    subject.Digest,
		Size:      subject.Size,
	}

	body, err := json.Marshal(ocispec.Manifest{
		Versioned:    specs.Versioned{SchemaVersion: 2},
		MediaType:    ocispec.MediaTypeImageManifest,
		ArtifactType: artifactType,
		Config:       ocispec.DescriptorEmptyJSON,
		Layers:       []ocispec.Descriptor{layer},
		Subject:      &ref,
	})
	if err != nil {
		return ocispec.Descriptor{}, fmt.Errorf("encode referrer manifest: %w", err)
	}

	desc := content.NewDescriptorFromBytes(ocispec.MediaTypeImageManifest, body)
	desc.ArtifactType = artifactType
	if err := push(ctx, dst, desc, body); err != nil {
		return ocispec.Descriptor{}, fmt.Errorf("write referrer manifest: %w", err)
	}
	return desc, nil
}

// push writes data unless the target already holds it. Signing the same
// artifact twice re-pushes the same payload blob and the same empty config, so
// ErrAlreadyExists is an expected outcome rather than a failure.
func push(ctx context.Context, dst content.Storage, desc ocispec.Descriptor, data []byte) error {
	if ok, err := dst.Exists(ctx, desc); err == nil && ok {
		return nil
	}
	err := dst.Push(ctx, desc, bytes.NewReader(data))
	if err != nil && !errors.Is(err, errdef.ErrAlreadyExists) {
		return err
	}
	return nil
}
