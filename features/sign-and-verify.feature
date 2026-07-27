Feature: Sign a skill and verify it
  A signature is a referrer. It sits beside the skill in the same repository,
  its subject is the skill's manifest, and it is shaped the way cosign shapes
  one — so a tool that has never heard of Epos can read what Epos writes.
  Attestations are stored the same way, with an in-toto statement in a DSSE
  envelope instead of a simple-signing payload.

  Verification adds nothing to the registry. It resolves the manifest, lists
  the referrers through GET /v2/<name>/referrers/<digest> — the endpoint
  epos-registry has relayed since A1 — and fetches the signed payload with an
  ordinary blob GET. Signing goes to the upstream registry, because
  epos-registry serves no write path and a referrer can only live beside what
  it refers to.

  The consequence is counted, not hidden: that blob GET lands in the skill's
  own repository, so epos-registry counts it as an unverified download of the
  skill. Telling a signature blob from a content blob would mean remembering
  which digest is which, and epos-registry holds no state. The last scenario
  is that inflation, asserted rather than described.

  Background:
    Given an upstream registry
    And epos-registry is fronting it
    And a signing keypair
    And the skill "reviewer" version "1.0.0" is published upstream

  # SPEC 11 -- the layout is cosign's, and the subject is what makes it a
  # referrer of the skill rather than an artifact that merely sits nearby.
  Scenario: A signature is stored as a referrer of the skill manifest
    When the author signs it
    Then the registry lists a cosign signature referrer of the skill
    And the referrer's subject is the skill manifest
    And the referrer carries a simple-signing layer with a cosign signature annotation

  # SPEC 11 -- attestations are stored the same way.
  Scenario: An attestation is stored as a referrer too
    When the author attests it with predicate type "https://slsa.dev/provenance/v1"
    Then the registry lists a cosign attestation referrer of the skill
    And the referrer's subject is the skill manifest
    And the referrer carries a DSSE envelope layer

  # SPEC 11 and 4.1 -- read through epos-registry, over the referrers endpoint
  # that was already there. No new endpoint was added for this milestone.
  Scenario: A signed skill verifies through epos-registry
    Given the author signs it
    When the consumer verifies it through epos-registry
    Then verification succeeds
    And the verification reports the signature it checked

  Scenario: Verification reports the attestations it checked
    Given the author signs it
    And the author attests it with predicate type "https://slsa.dev/provenance/v1"
    When the consumer verifies it through epos-registry
    Then verification succeeds
    And the verification reports the attestation "https://slsa.dev/provenance/v1"

  # The A5 gate. The attacker has write access to the registry: they rewrite
  # the skill, re-point the tag at it, and move the author's signature — which
  # they cannot forge, and do not need to — onto the new manifest. Everything
  # about that signature is genuine except what it is attached to, so the only
  # thing that catches it is the digest inside the signed payload.
  Scenario: A tampered artifact fails verification
    Given the author signs it
    When an attacker rewrites the skill and moves the signature onto it
    And the consumer verifies it through epos-registry
    Then verification fails
    And the error says the signature covers the untampered artifact

  # The same attack, one step further: the attacker rewrites the signed payload
  # so it names the tampered artifact, and pushes it as a new blob so the layer
  # descriptor still matches its contents. The digest check now passes and the
  # cryptography is what refuses.
  Scenario: A tampered artifact with a rewritten payload fails verification
    Given the author signs it
    When an attacker rewrites the skill and rewrites the signed payload
    And the consumer verifies it through epos-registry
    Then verification fails
    And the error says the signature does not verify against the public key

  # Unsigned and tampered are different answers, and a verifier that cannot
  # tell them apart passes the tampered one off as merely unsigned.
  Scenario: An unsigned skill is reported as unsigned
    When the consumer verifies it through epos-registry
    Then verification fails
    And the error says no cosign signature is attached

  # SPEC 5.2 and 4.4 -- the documented inflation, as a fact about the running
  # system: the signature blob shares the skill's repository, so it is counted,
  # and the epos CLI does not send Epos-Download for it, so it is counted
  # unverified.
  Scenario: Verifying counts the signature blob as an unverified download
    Given the author signs it
    When the consumer verifies it through epos-registry
    Then verification succeeds
    And the signature blob fetch is counted as an unverified download of the skill
