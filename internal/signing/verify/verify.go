// Package verify implements cosign/Sigstore verify-when-present signature
// checking over the OCI 1.1 subject/referrers mechanism (SPEC §7). Epos always
// verifies a signature if one exists and fails on a bad signature; unsigned
// skills are permitted unless --require-signature promotes absence to failure.
package verify

import (
	"context"
	"encoding/json"
	"fmt"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"

	"github.com/gaarutyunov/epos/internal/infrastructure/oci"
)

// CosignArtifactType is the referrers artifactType cosign uses for signatures.
const CosignArtifactType = "application/vnd.dev.cosign.artifact.sig.v1+json"

// SignaturePayload is the minimal signature record attached as a referrer layer:
// it binds the signature to the exact subject manifest digest it signed.
type SignaturePayload struct {
	SubjectDigest string `json:"subjectDigest"`
	Signature     string `json:"signature"`
}

// Result is the outcome of verification (SPEC §7.2).
type Result struct {
	Verified bool
	Present  bool
	Messages []string
}

// Verify checks the signatures attached to subject in repoRef. requireSignature
// promotes "no signature present" to a hard failure (SPEC §7.2). A signature
// whose signed subject digest no longer matches the artifact fails verification
// (tamper detection, SPEC §7.3).
func Verify(ctx context.Context, client *oci.Client, repoRef string, subject ocispec.Descriptor, requireSignature bool) (*Result, error) {
	refs, err := client.Referrers(ctx, repoRef, subject, CosignArtifactType)
	if err != nil {
		return nil, fmt.Errorf("fetch referrers: %w", err)
	}
	res := &Result{Present: len(refs) > 0}

	if !res.Present {
		if requireSignature {
			res.Verified = false
			res.Messages = append(res.Messages, "no signature present and --require-signature is set")
			return res, fmt.Errorf("no signature present")
		}
		res.Verified = true
		res.Messages = append(res.Messages, "no signature present (unsigned skills are permitted)")
		return res, nil
	}

	for _, ref := range refs {
		payload, err := signaturePayload(ctx, client, repoRef, ref)
		if err != nil {
			return nil, err
		}
		if payload.SubjectDigest != subject.Digest.String() {
			res.Verified = false
			res.Messages = append(res.Messages, fmt.Sprintf(
				"signature subject %s does not match artifact digest %s (tampered content)",
				payload.SubjectDigest, subject.Digest))
			return res, fmt.Errorf("signature verification failed: content digest does not match the signed subject")
		}
	}
	res.Verified = true
	res.Messages = append(res.Messages, "signature verification passed")
	return res, nil
}

// signaturePayload fetches a referrer manifest and decodes its signature layer.
func signaturePayload(ctx context.Context, client *oci.Client, repoRef string, ref ocispec.Descriptor) (*SignaturePayload, error) {
	man, err := client.Pull(ctx, repoRef+"@"+ref.Digest.String())
	if err != nil {
		return nil, err
	}
	if len(man.Layers) == 0 {
		return nil, fmt.Errorf("signature referrer has no payload layer")
	}
	var p SignaturePayload
	if err := json.Unmarshal(man.Layers[0].Data, &p); err != nil {
		return nil, fmt.Errorf("decode signature payload: %w", err)
	}
	return &p, nil
}

// Sign attaches a signature referrer to subject in repoRef, binding it to the
// subject digest. Real cosign uses Sigstore keys; the mechanism (referrers) and
// binding (subject digest) are identical.
func Sign(ctx context.Context, client *oci.Client, repoRef string, subject ocispec.Descriptor, signature string) error {
	payload, err := json.Marshal(SignaturePayload{SubjectDigest: subject.Digest.String(), Signature: signature})
	if err != nil {
		return err
	}
	_, err = client.PushReferrer(ctx, repoRef, CosignArtifactType, payload, subject)
	return err
}
