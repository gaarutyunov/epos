Feature: Author and publish a skill
  As a skill author
  I want to package my skill directory as an OCI artifact and push it
  So that others can discover, pull, and install it

  Background:
    Given a running OCI registry
    And a skill directory "pdf-tools" with a valid Epos.yaml

  Scenario: Package a valid skill into an OCI artifact
    When I run "epos package pdf-tools"
    Then an OCI artifact is produced with a single tar+gzip content layer
    And the config blob records the Epos.yaml metadata
    And the artifact media types are the Epos skill types

  Scenario: Strict validation rejects an invalid name
    Given the Epos.yaml "name" is "Anthropic-PDF"
    When I run "epos lint pdf-tools"
    Then validation fails
    And the report mentions the name must be lowercase and must not contain "anthropic"

  Scenario: Dangling reference lint fails the package
    Given SKILL.md references "references/missing.md" which does not exist
    When I run "epos lint pdf-tools"
    Then validation fails
    And the report mentions the dangling reference "references/missing.md"

  Scenario: Push a packaged skill to the registry
    Given a packaged skill "pdf-tools" version "1.4.2"
    When I run "epos push pdf-tools registry/skills/pdf-tools:1.4.2"
    Then the manifest is stored in the registry
    And the pushed tag resolves to the artifact digest
