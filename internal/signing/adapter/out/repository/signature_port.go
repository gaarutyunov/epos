// Code scaffolded by sysgo; edit freely (not regenerated).

package repository

import (
	"context"

	godigest "github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"

	"github.com/gaarutyunov/epos/internal/infrastructure/oci"
	"github.com/gaarutyunov/epos/internal/signing/app/port/out"
	"github.com/gaarutyunov/epos/internal/signing/domain"
	"github.com/gaarutyunov/epos/internal/signing/verify"
)

// SignaturePortImpl is the driven adapter implementing the SignaturePort port
// via cosign/Sigstore over the OCI 1.1 referrers mechanism (SPEC §7). It uses
// the shared OCI client (Infrastructure.OciClient) to fetch referrers.
type SignaturePortImpl struct {
	client  *oci.Client
	repoRef string
}

var _ out.SignaturePort = (*SignaturePortImpl)(nil)

// NewSignaturePortImpl binds the adapter to an OCI client and the subject's
// repository reference (registry/repo).
func NewSignaturePortImpl(client *oci.Client, repoRef string) *SignaturePortImpl {
	return &SignaturePortImpl{client: client, repoRef: repoRef}
}

// Signature verifies a subject's signatures (verify-when-present; --require-
// signature enforces presence; tamper fails), returning the domain result.
func (s *SignaturePortImpl) Signature(request domain.VerifyRequest) (domain.VerifyResult, error) {
	subject := ocispec.Descriptor{Digest: godigest.Digest(request.SubjectDigest)}
	res, err := verify.Verify(context.Background(), s.client, s.repoRef, subject, request.Policy.RequireSignature)
	if res == nil {
		return domain.VerifyResult{}, err
	}
	return domain.VerifyResult{Verified: res.Verified, Present: res.Present, Messages: res.Messages}, err
}
