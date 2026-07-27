package sign

import (
	"context"
	"crypto/ecdsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2/content"
)

//go:generate go tool mockgen -source=verify.go -destination=mocks_test.go -package=sign

// Source is the whole of what verification asks a registry for.
//
// Both halves are already served: Referrers is GET /v2/<name>/referrers/<digest>
// from SPEC.md 4.1, which epos-registry has relayed since A1, and Fetch is the
// manifest and blob GETs of the same read surface. SPEC.md 11 is explicit that
// verification uses what is there — this interface exists so that claim can be
// asserted over the calls actually made, not so a second transport can be
// substituted for the first.
type Source interface {
	// Referrers feeds fn each page of manifests referencing desc, filtered to
	// artifactType.
	Referrers(ctx context.Context, desc ocispec.Descriptor, artifactType string,
		fn func(referrers []ocispec.Descriptor) error) error
	// Fetch returns the content of one descriptor, manifest or blob.
	Fetch(ctx context.Context, target ocispec.Descriptor) (io.ReadCloser, error)
}

// ErrNoSignature reports a subject with no cosign signature attached at all.
//
// Distinguished from a signature that fails to verify because the two are
// different answers: nobody signed this, versus somebody's signature does not
// hold. A verification that could not tell them apart would pass a tampered
// artifact off as merely unsigned.
var ErrNoSignature = errors.New("no cosign signature is attached")

// Result is what verification found.
type Result struct {
	// Subject is the manifest the reference resolved to.
	Subject ocispec.Descriptor
	// Signatures are the signature referrers that verified.
	Signatures []ocispec.Descriptor
	// Attestations are the attestations that verified, every one of them.
	Attestations []Attestation
}

// Attestation is one verified attestation referrer.
type Attestation struct {
	Referrer      ocispec.Descriptor
	PredicateType string
}

// Verify checks the signatures and attestations attached to subject.
//
// At least one signature must verify against pub, which is cosign's rule and
// the one key rotation needs: a second signature by a new key sits alongside
// the first rather than replacing it. Attestations are held to the stricter
// rule — every one present must verify — because an attestation that does not
// hold is a claim somebody tried to make about this artifact, and skipping it
// is how it succeeds.
//
// ref is used only to say what failed.
func Verify(ctx context.Context, src Source, ref string,
	subject ocispec.Descriptor, pub *ecdsa.PublicKey) (Result, error) {
	result := Result{Subject: subject}

	signatures, err := referrers(ctx, src, subject, SignatureArtifactType)
	if err != nil {
		return Result{}, err
	}
	if len(signatures) == 0 {
		return Result{}, fmt.Errorf("%w to %s", ErrNoSignature, ref)
	}

	// The first failure is remembered rather than returned, so that a subject
	// signed by two keys still verifies against either. With a single
	// signature — the ordinary case, and the tampered one — it is the error
	// the user sees, and it says which check failed.
	var firstErr error
	for _, desc := range signatures {
		if err := verifySignature(ctx, src, desc, subject, ref, pub); err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		result.Signatures = append(result.Signatures, desc)
	}
	if len(result.Signatures) == 0 {
		return Result{}, firstErr
	}

	attestations, err := referrers(ctx, src, subject, AttestationArtifactType)
	if err != nil {
		return Result{}, err
	}
	for _, desc := range attestations {
		attestation, err := verifyAttestation(ctx, src, desc, subject, pub)
		if err != nil {
			return Result{}, err
		}
		result.Attestations = append(result.Attestations, attestation)
	}
	return result, nil
}

// referrers collects one artifact type's referrers of subject.
func referrers(ctx context.Context, src Source, subject ocispec.Descriptor,
	artifactType string) ([]ocispec.Descriptor, error) {
	var out []ocispec.Descriptor
	err := src.Referrers(ctx, subject, artifactType, func(page []ocispec.Descriptor) error {
		out = append(out, page...)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("list %s referrers: %w", artifactType, err)
	}

	// A registry is entitled to return referrers it did not filter, and
	// oras-go's fallback path reads an index that was never filtered at all.
	filtered := out[:0]
	for _, desc := range out {
		if desc.ArtifactType == "" || desc.ArtifactType == artifactType {
			filtered = append(filtered, desc)
		}
	}
	return filtered, nil
}

// verifySignature checks one signature referrer against pub and against the
// subject it claims to cover.
func verifySignature(ctx context.Context, src Source, desc, subject ocispec.Descriptor,
	ref string, pub *ecdsa.PublicKey) error {
	layer, err := singleLayer(ctx, src, desc, SimpleSigningMediaType)
	if err != nil {
		return err
	}

	encoded := layer.Annotations[SignatureAnnotation]
	if encoded == "" {
		return fmt.Errorf("signature %s carries no %s annotation", desc.Digest, SignatureAnnotation)
	}
	sig, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return fmt.Errorf("signature %s: decode the %s annotation: %w",
			desc.Digest, SignatureAnnotation, err)
	}

	// FetchAll verifies the layer against its descriptor, so a rewritten
	// payload that left the descriptor alone never reaches the signature
	// check — it fails here, as a content mismatch.
	body, err := content.FetchAll(ctx, src, layer)
	if err != nil {
		return fmt.Errorf("signature %s: read the signed payload: %w", desc.Digest, err)
	}
	if err := verifyBytes(pub, body, sig); err != nil {
		return fmt.Errorf("signature %s %w", desc.Digest, err)
	}

	var p payload
	if err := json.Unmarshal(body, &p); err != nil {
		return fmt.Errorf("signature %s: parse the signed payload: %w", desc.Digest, err)
	}
	if p.Critical.Type != simpleSigningType {
		return fmt.Errorf("signature %s: payload type is %q, want %q",
			desc.Digest, p.Critical.Type, simpleSigningType)
	}
	// The check the whole thing turns on. A signature is bytes over a payload,
	// and a payload names the one artifact it was made for; without this, a
	// signature copied from a legitimate artifact onto a tampered one verifies.
	if covered := p.Critical.Image.DockerManifestDigest; covered != subject.Digest.String() {
		return fmt.Errorf("signature %s covers %s, but %s is %s",
			desc.Digest, covered, ref, subject.Digest)
	}
	return nil
}

// verifyAttestation checks one DSSE envelope and the statement inside it.
func verifyAttestation(ctx context.Context, src Source, desc, subject ocispec.Descriptor,
	pub *ecdsa.PublicKey) (Attestation, error) {
	layer, err := singleLayer(ctx, src, desc, DSSEMediaType)
	if err != nil {
		return Attestation{}, err
	}

	body, err := content.FetchAll(ctx, src, layer)
	if err != nil {
		return Attestation{}, fmt.Errorf("attestation %s: read the envelope: %w", desc.Digest, err)
	}
	var envelope Envelope
	if err := json.Unmarshal(body, &envelope); err != nil {
		return Attestation{}, fmt.Errorf("attestation %s: parse the envelope: %w", desc.Digest, err)
	}
	if envelope.PayloadType != InTotoPayloadType {
		return Attestation{}, fmt.Errorf("attestation %s: payloadType is %q, want %q",
			desc.Digest, envelope.PayloadType, InTotoPayloadType)
	}
	statement, err := base64.StdEncoding.DecodeString(envelope.Payload)
	if err != nil {
		return Attestation{}, fmt.Errorf("attestation %s: decode the payload: %w", desc.Digest, err)
	}

	signed := pae(envelope.PayloadType, statement)
	verified := false
	for _, s := range envelope.Signatures {
		sig, err := base64.StdEncoding.DecodeString(s.Sig)
		if err != nil {
			continue
		}
		if verifyBytes(pub, signed, sig) == nil {
			verified = true
			break
		}
	}
	if !verified {
		return Attestation{}, fmt.Errorf("attestation %s %w", desc.Digest, errNotVerified)
	}

	var st Statement
	if err := json.Unmarshal(statement, &st); err != nil {
		return Attestation{}, fmt.Errorf("attestation %s: parse the statement: %w", desc.Digest, err)
	}
	if st.Type != StatementType {
		return Attestation{}, fmt.Errorf("attestation %s: statement _type is %q, want %q",
			desc.Digest, st.Type, StatementType)
	}
	// Same binding as a signature's docker-manifest-digest: an envelope
	// re-attached to another artifact says so here.
	if !namesSubject(st.Subject, subject) {
		return Attestation{}, fmt.Errorf("attestation %s does not name %s as its subject",
			desc.Digest, subject.Digest)
	}
	return Attestation{Referrer: desc, PredicateType: st.PredicateType}, nil
}

func namesSubject(subjects []Subject, subject ocispec.Descriptor) bool {
	algorithm := subject.Digest.Algorithm().String()
	for _, s := range subjects {
		if s.Digest[algorithm] == subject.Digest.Encoded() {
			return true
		}
	}
	return false
}

// singleLayer reads a referrer manifest and returns its one layer of the
// expected media type.
func singleLayer(ctx context.Context, src Source, desc ocispec.Descriptor,
	mediaType string) (ocispec.Descriptor, error) {
	body, err := content.FetchAll(ctx, src, desc)
	if err != nil {
		return ocispec.Descriptor{}, fmt.Errorf("read referrer %s: %w", desc.Digest, err)
	}
	var manifest ocispec.Manifest
	if err := json.Unmarshal(body, &manifest); err != nil {
		return ocispec.Descriptor{}, fmt.Errorf("parse referrer %s: %w", desc.Digest, err)
	}
	if len(manifest.Layers) != 1 {
		return ocispec.Descriptor{}, fmt.Errorf("referrer %s has %d layers, want exactly 1",
			desc.Digest, len(manifest.Layers))
	}
	if manifest.Layers[0].MediaType != mediaType {
		return ocispec.Descriptor{}, fmt.Errorf("referrer %s carries a %s layer, want %s",
			desc.Digest, manifest.Layers[0].MediaType, mediaType)
	}
	return manifest.Layers[0], nil
}
