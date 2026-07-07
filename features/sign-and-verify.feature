Feature: Sign and verify a skill
  As a security-conscious user
  I want signatures verified when present and enforceable on demand
  So that I can trust the provenance of a skill

  Background:
    Given a running OCI registry with referrers support
    And a published skill "pdf-tools" version "1.4.2"

  Scenario: Verification passes when a valid signature is present
    Given "pdf-tools" is signed with cosign
    When I run "epos install pdf pdf-tools"
    Then signature verification passes

  Scenario: Unsigned skill installs when signature is not required
    Given "pdf-tools" has no signature
    When I run "epos install pdf pdf-tools"
    Then the install succeeds
    And verification reports no signature present

  Scenario: Require-signature enforces presence
    Given "pdf-tools" has no signature
    When I run "epos install pdf pdf-tools --require-signature"
    Then the install fails because no signature is present

  Scenario: Tampered content fails verification
    Given "pdf-tools" is signed
    And the content digest no longer matches the signed subject
    When I run "epos install pdf pdf-tools --require-signature"
    Then signature verification fails
