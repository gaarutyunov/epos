// Package sign implements SPEC.md 11: cosign signatures and attestations
// stored as OCI referrers, with subject pointing at the skill manifest.
//
// # The layout is cosign's, not Epos's
//
// Nothing here is an Epos invention. A signature is the manifest cosign writes
// in OCI 1.1 referrers mode: artifactType
// application/vnd.dev.cosign.artifact.sig.v1+json, an empty config, one
// simple-signing layer whose annotation carries the base64 signature, and a
// subject descriptor naming the skill manifest. An attestation is the same
// shape with a DSSE envelope over an in-toto statement in the layer. The point
// of following the convention rather than inventing one is that cosign, or any
// other tool that reads referrers, can read what Epos writes.
//
// # No new dependency, and therefore no cgo
//
// SPEC.md 1.2 makes pure Go a hard constraint, so what signing costs in
// dependencies was measured rather than assumed:
//
//   - github.com/sigstore/sigstore, the obvious choice, resolves to 82 modules.
//     It drags in go-containerregistry, the Docker CLI, letsencrypt/boulder,
//     go-tuf and go-rod — a headless-browser driver that downloads and spawns
//     a browser binary. None of that is reachable from ECDSA signing, but all
//     of it lands in go.sum and in govulncheck's surface.
//   - github.com/secure-systems-lab/go-securesystemslib, for its DSSE helpers
//     alone, links golang.org/x/crypto/ssh into the binary for the sake of a
//     six-line pre-authentication encoding.
//
// So this package adds nothing. Keys, hashing and ECDSA are crypto/ecdsa,
// crypto/x509 and encoding/pem; DSSE's PAE is written out below against its
// specification. The signing and verification path is standard library and the
// OCI packages epos already depended on, which is why CGO_ENABLED=0 builds it
// unchanged.
//
// # Keys
//
// P-256 ECDSA over SHA-256, which is cosign's default and what the wire format
// above assumes. The public key is written as a PKIX PEM block — byte-for-byte
// what `cosign generate-key-pair` writes to cosign.pub, so `cosign verify
// --key` accepts an Epos public key. The private key is a PKCS#8 PEM block and
// deliberately *not* cosign's password-encrypted ENCRYPTED SIGSTORE PRIVATE
// KEY: reading that format needs scrypt and NaCl secretbox from a dependency
// this package exists to avoid. Epos signs with its own keys; it verifies
// anything cosign-shaped.
//
// Keyless signing is out of scope. Fulcio needs an OIDC identity at signing
// time, which no unattended run has, and SPEC.md 11 asks for neither.
package sign

import (
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
)

// Artifact types of the two referrer kinds (SPEC.md 11). These are the values
// cosign sets on the manifest in OCI 1.1 referrers mode, and they are what a
// referrers listing is filtered by.
const (
	SignatureArtifactType   = "application/vnd.dev.cosign.artifact.sig.v1+json"
	AttestationArtifactType = "application/vnd.dev.cosign.artifact.attestation.v1+json"
)

// Layer media types carried by each referrer kind.
const (
	// SimpleSigningMediaType is the payload a cosign signature signs over.
	SimpleSigningMediaType = "application/vnd.dev.cosign.simplesigning.v1+json"
	// DSSEMediaType is the envelope an attestation is delivered in.
	DSSEMediaType = "application/vnd.dsse.envelope.v1+json"
)

// SignatureAnnotation carries the base64 signature on the simple-signing layer
// descriptor.
//
// The key is spelled dev.cosignproject.cosign, not dev.sigstore.cosign: cosign
// renamed its other annotations and left this one alone for compatibility, and
// a verifier that looks for the tidier spelling finds nothing.
const SignatureAnnotation = "dev.cosignproject.cosign/signature"

// simpleSigningType is the critical.type every cosign signature payload
// carries. It says nothing about containers in practice — cosign signs any OCI
// subject with it — but the string is fixed and a reader checks it.
const simpleSigningType = "cosign container image signature"

// InTotoPayloadType is the DSSE payloadType of an in-toto statement, and
// StatementType is the statement's own _type.
const (
	InTotoPayloadType = "application/vnd.in-toto+json"
	StatementType     = "https://in-toto.io/Statement/v1"
)

// DefaultPredicateType is what `epos attest` records when the caller names no
// other. Custom is in-toto's own escape hatch for a predicate with no
// registered schema, which is what an arbitrary JSON document supplied on the
// command line is.
const DefaultPredicateType = "https://in-toto.io/attestation/custom/v0.1"

// payload is the simple-signing document (the "critical" block of the
// Red Hat simple-signing format cosign adopted).
//
// The signature is over these bytes, and critical.image.docker-manifest-digest
// is what binds them to one artifact: without checking it, a signature lifted
// from a legitimate artifact and re-attached to a tampered one verifies
// perfectly well.
type payload struct {
	Critical critical       `json:"critical"`
	Optional map[string]any `json:"optional"`
}

type critical struct {
	Identity identity `json:"identity"`
	Image    image    `json:"image"`
	Type     string   `json:"type"`
}

type identity struct {
	DockerReference string `json:"docker-reference"`
}

type image struct {
	DockerManifestDigest string `json:"docker-manifest-digest"`
}

// Statement is an in-toto v1 statement, the payload of an attestation.
type Statement struct {
	Type          string          `json:"_type"`
	Subject       []Subject       `json:"subject"`
	PredicateType string          `json:"predicateType"`
	Predicate     json.RawMessage `json:"predicate"`
}

// Subject names what a statement is about, by digest.
type Subject struct {
	Name   string            `json:"name"`
	Digest map[string]string `json:"digest"`
}

// Envelope is a DSSE envelope.
type Envelope struct {
	PayloadType string              `json:"payloadType"`
	Payload     string              `json:"payload"`
	Signatures  []EnvelopeSignature `json:"signatures"`
}

// EnvelopeSignature is one signature over a DSSE envelope's payload.
type EnvelopeSignature struct {
	KeyID string `json:"keyid,omitempty"`
	Sig   string `json:"sig"`
}

// pae is DSSE's Pre-Authentication Encoding, the bytes an envelope's signature
// is actually over:
//
//	"DSSEv1" SP LEN(payloadType) SP payloadType SP LEN(payload) SP payload
//
// The lengths are what make it unambiguous: signing the concatenation directly
// would let a payload type and a payload be re-cut at a different boundary and
// still produce the same signed bytes.
func pae(payloadType string, body []byte) []byte {
	prefix := fmt.Sprintf("DSSEv1 %d %s %d ", len(payloadType), payloadType, len(body))
	out := make([]byte, 0, len(prefix)+len(body))
	out = append(out, prefix...)
	return append(out, body...)
}

// signBytes signs SHA-256 of msg, producing an ASN.1 DER ECDSA signature —
// the encoding cosign base64s into the layer annotation.
func signBytes(key *ecdsa.PrivateKey, msg []byte) ([]byte, error) {
	sum := sha256.Sum256(msg)
	sig, err := ecdsa.SignASN1(rand.Reader, key, sum[:])
	if err != nil {
		return nil, fmt.Errorf("sign: %w", err)
	}
	return sig, nil
}

// errNotVerified is what a cryptographic mismatch reduces to. It reads as a
// predicate because every caller wraps it with the referrer that failed —
// "signature sha256:… does not verify against the public key" — and nothing
// here knows which referrer that is.
var errNotVerified = errors.New("does not verify against the public key")

func verifyBytes(pub *ecdsa.PublicKey, msg, sig []byte) error {
	sum := sha256.Sum256(msg)
	if !ecdsa.VerifyASN1(pub, sum[:], sig) {
		return errNotVerified
	}
	return nil
}
