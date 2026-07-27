package sign

import (
	"context"
	"crypto/ecdsa"
	"encoding/base64"
	"encoding/json"
	"testing"

	"github.com/opencontainers/image-spec/specs-go"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
	"oras.land/oras-go/v2/content"
	"oras.land/oras-go/v2/content/memory"
)

// pae is specified with worked examples; this is one of them, and it is the
// whole reason the encoding is written out here rather than depended on.
func TestPAEMatchesTheSpecifiedEncoding(t *testing.T) {
	got := pae("http://example.com/HelloWorld", []byte("hello world"))
	assert.Equal(t, "DSSEv1 29 http://example.com/HelloWorld 11 hello world", string(got))
}

func TestKeysRoundTripThroughPEM(t *testing.T) {
	key, err := GenerateKey()
	require.NoError(t, err)

	privatePEM, err := MarshalPrivateKey(key)
	require.NoError(t, err)
	publicPEM, err := MarshalPublicKey(&key.PublicKey)
	require.NoError(t, err)

	// The public half is a cosign.pub, and cosign.pub is a PKIX PEM block.
	assert.Contains(t, string(publicPEM), "-----BEGIN PUBLIC KEY-----")
	assert.Contains(t, string(privatePEM), "-----BEGIN PRIVATE KEY-----")

	gotPrivate, err := ParsePrivateKey(privatePEM)
	require.NoError(t, err)
	assert.True(t, key.Equal(gotPrivate))

	gotPublic, err := ParsePublicKey(publicPEM)
	require.NoError(t, err)
	assert.True(t, key.PublicKey.Equal(gotPublic))
}

// Handing sign the key cosign itself wrote is the mistake worth a real
// message, because the file is called cosign.key in both worlds.
func TestParsePrivateKeyNamesCosignsEncryptedFormat(t *testing.T) {
	encrypted := "-----BEGIN ENCRYPTED SIGSTORE PRIVATE KEY-----\nQUJD\n" +
		"-----END ENCRYPTED SIGSTORE PRIVATE KEY-----\n"

	_, err := ParsePrivateKey([]byte(encrypted))

	require.Error(t, err)
	assert.Contains(t, err.Error(), "encrypted key format is not supported")
}

func TestParsePublicKeyRejectsSomethingThatIsNotPEM(t *testing.T) {
	_, err := ParsePublicKey([]byte("not a key"))

	require.Error(t, err)
	assert.Contains(t, err.Error(), "no PEM block found")
}

// --- the referrer layout ----------------------------------------------------

// The layout is cosign's, and a standard tool reads it by artifact type, media
// type, annotation key and subject. All four are asserted, because getting any
// one of them wrong makes the signature invisible to everything but epos.
func TestSignWritesACosignShapedReferrer(t *testing.T) {
	ctx := t.Context()
	store := memory.New()
	key := testKey(t)
	subject := testSubject(t, store, "reviewer")

	desc, err := Sign(ctx, store, "registry.example/demo/reviewer", subject, key)
	require.NoError(t, err)

	assert.Equal(t, SignatureArtifactType, desc.ArtifactType)

	manifest := fetchManifest(t, store, desc)
	assert.Equal(t, ocispec.MediaTypeImageManifest, manifest.MediaType)
	assert.Equal(t, SignatureArtifactType, manifest.ArtifactType)
	assert.Equal(t, ocispec.MediaTypeEmptyJSON, manifest.Config.MediaType)
	require.NotNil(t, manifest.Subject, "a referrer without a subject is not a referrer")
	assert.Equal(t, subject.Digest, manifest.Subject.Digest)
	assert.Equal(t, subject.Size, manifest.Subject.Size)

	require.Len(t, manifest.Layers, 1)
	layer := manifest.Layers[0]
	assert.Equal(t, SimpleSigningMediaType, layer.MediaType)
	assert.NotEmpty(t, layer.Annotations[SignatureAnnotation])

	body, err := content.FetchAll(ctx, store, layer)
	require.NoError(t, err)
	var p payload
	require.NoError(t, json.Unmarshal(body, &p))
	assert.Equal(t, simpleSigningType, p.Critical.Type)
	assert.Equal(t, "registry.example/demo/reviewer", p.Critical.Identity.DockerReference)
	assert.Equal(t, subject.Digest.String(), p.Critical.Image.DockerManifestDigest)
}

func TestAttestWritesADSSEEnvelopeReferrer(t *testing.T) {
	ctx := t.Context()
	store := memory.New()
	key := testKey(t)
	subject := testSubject(t, store, "reviewer")

	desc, err := Attest(ctx, store, "registry.example/demo/reviewer", subject,
		"https://slsa.dev/provenance/v1", json.RawMessage(`{"buildType":"epos"}`), key)
	require.NoError(t, err)

	assert.Equal(t, AttestationArtifactType, desc.ArtifactType)

	manifest := fetchManifest(t, store, desc)
	require.NotNil(t, manifest.Subject)
	assert.Equal(t, subject.Digest, manifest.Subject.Digest)
	require.Len(t, manifest.Layers, 1)
	assert.Equal(t, DSSEMediaType, manifest.Layers[0].MediaType)

	body, err := content.FetchAll(ctx, store, manifest.Layers[0])
	require.NoError(t, err)
	var envelope Envelope
	require.NoError(t, json.Unmarshal(body, &envelope))
	assert.Equal(t, InTotoPayloadType, envelope.PayloadType)
	require.Len(t, envelope.Signatures, 1)

	statement, err := base64.StdEncoding.DecodeString(envelope.Payload)
	require.NoError(t, err)
	var st Statement
	require.NoError(t, json.Unmarshal(statement, &st))
	assert.Equal(t, StatementType, st.Type)
	assert.Equal(t, "https://slsa.dev/provenance/v1", st.PredicateType)
	require.Len(t, st.Subject, 1)
	assert.Equal(t, subject.Digest.Encoded(), st.Subject[0].Digest["sha256"])
}

// --- verification -----------------------------------------------------------

func TestVerifyAcceptsASignatureAndAnAttestation(t *testing.T) {
	ctx := t.Context()
	store := memory.New()
	key := testKey(t)
	subject := testSubject(t, store, "reviewer")

	signature, err := Sign(ctx, store, "registry.example/demo/reviewer", subject, key)
	require.NoError(t, err)
	attestation, err := Attest(ctx, store, "registry.example/demo/reviewer", subject,
		"https://slsa.dev/provenance/v1", nil, key)
	require.NoError(t, err)

	src := sourceOf(t, store, map[string][]ocispec.Descriptor{
		subject.Digest.String(): {signature, attestation},
	})

	result, err := Verify(ctx, src, "demo/reviewer:1.0.0", subject, &key.PublicKey)

	require.NoError(t, err)
	require.Len(t, result.Signatures, 1)
	assert.Equal(t, signature.Digest, result.Signatures[0].Digest)
	require.Len(t, result.Attestations, 1)
	assert.Equal(t, "https://slsa.dev/provenance/v1", result.Attestations[0].PredicateType)
}

func TestVerifyReportsAnUnsignedArtifactAsUnsigned(t *testing.T) {
	ctx := t.Context()
	store := memory.New()
	key := testKey(t)
	subject := testSubject(t, store, "reviewer")

	src := sourceOf(t, store, nil)

	_, err := Verify(ctx, src, "demo/reviewer:1.0.0", subject, &key.PublicKey)

	require.ErrorIs(t, err, ErrNoSignature)
}

// The transplant: the attacker rewrites the artifact, re-points the tag, and
// moves the author's untouched signature onto the new manifest. The signature
// still verifies cryptographically — it is the author's, over bytes nobody
// altered — and the only thing that catches it is the digest the payload
// names.
func TestVerifyRejectsASignatureTransplantedOntoAnotherArtifact(t *testing.T) {
	ctx := t.Context()
	store := memory.New()
	key := testKey(t)
	genuine := testSubject(t, store, "reviewer")
	tampered := testSubject(t, store, "reviewer-with-an-extra-instruction")

	signature, err := Sign(ctx, store, "registry.example/demo/reviewer", genuine, key)
	require.NoError(t, err)

	// The registry now answers referrers(tampered) with the author's signature.
	src := sourceOf(t, store, map[string][]ocispec.Descriptor{
		tampered.Digest.String(): {signature},
	})

	_, err = Verify(ctx, src, "demo/reviewer:1.0.0", tampered, &key.PublicKey)

	require.Error(t, err)
	assert.NotErrorIs(t, err, ErrNoSignature, "a transplant is not an absence")
	assert.Contains(t, err.Error(), "covers "+genuine.Digest.String())
	assert.Contains(t, err.Error(), "demo/reviewer:1.0.0 is "+tampered.Digest.String())
}

// The other half: the attacker rewrites the payload so it names the tampered
// artifact, and pushes it as a new blob so the layer descriptor stays honest.
// Now the digest check passes and the cryptography is what refuses.
func TestVerifyRejectsARewrittenPayload(t *testing.T) {
	ctx := t.Context()
	store := memory.New()
	key := testKey(t)
	genuine := testSubject(t, store, "reviewer")
	tampered := testSubject(t, store, "reviewer-with-an-extra-instruction")

	signature, err := Sign(ctx, store, "registry.example/demo/reviewer", genuine, key)
	require.NoError(t, err)
	manifest := fetchManifest(t, store, signature)

	forged, err := json.Marshal(payload{Critical: critical{
		Identity: identity{DockerReference: "registry.example/demo/reviewer"},
		Image:    image{DockerManifestDigest: tampered.Digest.String()},
		Type:     simpleSigningType,
	}})
	require.NoError(t, err)

	layer := content.NewDescriptorFromBytes(SimpleSigningMediaType, forged)
	layer.Annotations = manifest.Layers[0].Annotations // the author's signature, unaltered
	require.NoError(t, push(ctx, store, layer, forged))

	body, err := json.Marshal(ocispec.Manifest{
		Versioned:    specs.Versioned{SchemaVersion: 2},
		MediaType:    ocispec.MediaTypeImageManifest,
		ArtifactType: SignatureArtifactType,
		Config:       ocispec.DescriptorEmptyJSON,
		Layers:       []ocispec.Descriptor{layer},
		Subject:      &ocispec.Descriptor{MediaType: tampered.MediaType, Digest: tampered.Digest, Size: tampered.Size},
	})
	require.NoError(t, err)
	forgedSignature := content.NewDescriptorFromBytes(ocispec.MediaTypeImageManifest, body)
	forgedSignature.ArtifactType = SignatureArtifactType
	require.NoError(t, push(ctx, store, forgedSignature, body))

	src := sourceOf(t, store, map[string][]ocispec.Descriptor{
		tampered.Digest.String(): {forgedSignature},
	})

	_, err = Verify(ctx, src, "demo/reviewer:1.0.0", tampered, &key.PublicKey)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "does not verify against the public key")
}

func TestVerifyRejectsASignatureByAnotherKey(t *testing.T) {
	ctx := t.Context()
	store := memory.New()
	author := testKey(t)
	impostor := testKey(t)
	subject := testSubject(t, store, "reviewer")

	signature, err := Sign(ctx, store, "registry.example/demo/reviewer", subject, impostor)
	require.NoError(t, err)

	src := sourceOf(t, store, map[string][]ocispec.Descriptor{
		subject.Digest.String(): {signature},
	})

	_, err = Verify(ctx, src, "demo/reviewer:1.0.0", subject, &author.PublicKey)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "does not verify against the public key")
}

// A subject signed twice verifies against either key, which is what makes key
// rotation possible without invalidating what the old key signed.
func TestVerifyAcceptsOneOfTwoSignatures(t *testing.T) {
	ctx := t.Context()
	store := memory.New()
	old := testKey(t)
	current := testKey(t)
	subject := testSubject(t, store, "reviewer")

	first, err := Sign(ctx, store, "registry.example/demo/reviewer", subject, old)
	require.NoError(t, err)
	second, err := Sign(ctx, store, "registry.example/demo/reviewer", subject, current)
	require.NoError(t, err)

	src := sourceOf(t, store, map[string][]ocispec.Descriptor{
		subject.Digest.String(): {first, second},
	})

	result, err := Verify(ctx, src, "demo/reviewer:1.0.0", subject, &current.PublicKey)

	require.NoError(t, err)
	require.Len(t, result.Signatures, 1)
	assert.Equal(t, second.Digest, result.Signatures[0].Digest)
}

func TestVerifyRejectsAnAttestationAboutAnotherArtifact(t *testing.T) {
	ctx := t.Context()
	store := memory.New()
	key := testKey(t)
	genuine := testSubject(t, store, "reviewer")
	tampered := testSubject(t, store, "reviewer-with-an-extra-instruction")

	signature, err := Sign(ctx, store, "registry.example/demo/reviewer", tampered, key)
	require.NoError(t, err)
	attestation, err := Attest(ctx, store, "registry.example/demo/reviewer", genuine, "", nil, key)
	require.NoError(t, err)

	src := sourceOf(t, store, map[string][]ocispec.Descriptor{
		tampered.Digest.String(): {signature, attestation},
	})

	_, err = Verify(ctx, src, "demo/reviewer:1.0.0", tampered, &key.PublicKey)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "does not name "+tampered.Digest.String()+" as its subject")
}

// --- helpers ----------------------------------------------------------------

func testKey(t *testing.T) *ecdsa.PrivateKey {
	t.Helper()
	key, err := GenerateKey()
	require.NoError(t, err)
	return key
}

// testSubject writes a manifest into the store and returns its descriptor. The
// contents do not matter to signing — only the digest does — so body is
// whatever distinguishes one artifact from another in a test.
func testSubject(t *testing.T, store *memory.Store, body string) ocispec.Descriptor {
	t.Helper()
	raw := []byte(`{"skill":"` + body + `"}`)
	desc := content.NewDescriptorFromBytes(ocispec.MediaTypeImageManifest, raw)
	require.NoError(t, push(t.Context(), store, desc, raw))
	return desc
}

func fetchManifest(t *testing.T, store *memory.Store, desc ocispec.Descriptor) ocispec.Manifest {
	t.Helper()
	body, err := content.FetchAll(t.Context(), store, desc)
	require.NoError(t, err)
	var manifest ocispec.Manifest
	require.NoError(t, json.Unmarshal(body, &manifest))
	return manifest
}

// sourceOf programs the generated Source double: real content out of the
// memory store, and a referrers listing the test decides.
//
// The listing is the interesting half. A registry decides what referrers(D)
// answers, and every attack below is a registry answering it with something
// the author did not put there.
func sourceOf(t *testing.T, store *memory.Store,
	referrers map[string][]ocispec.Descriptor) *MockSource {
	t.Helper()

	src := NewMockSource(gomock.NewController(t))
	src.EXPECT().Fetch(gomock.Any(), gomock.Any()).DoAndReturn(store.Fetch).AnyTimes()
	src.EXPECT().
		Referrers(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, desc ocispec.Descriptor, artifactType string,
			fn func([]ocispec.Descriptor) error) error {
			var page []ocispec.Descriptor
			for _, d := range referrers[desc.Digest.String()] {
				if artifactType == "" || d.ArtifactType == artifactType {
					page = append(page, d)
				}
			}
			return fn(page)
		}).
		AnyTimes()
	return src
}
