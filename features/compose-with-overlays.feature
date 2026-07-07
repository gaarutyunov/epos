Feature: Compose a skill with dependencies and overlays
  As a skill author
  I want to build on a base skill and modify it with overlays
  So that I get one merged skill with the right file from each layer

  Background:
    Given a running OCI registry
    And a running git server
    And a published origin skill "pdf-tools" containing "references/c.md"

  Scenario: Local overlay replaces a file over a pulled base
    Given my repo depends on "pdf-tools" as an OCI layer
    And my repo has a local overlay replacing "references/a.md"
    When I compose the skill
    Then the merged skill contains "references/a.md" from my repo
    And the merged skill contains "references/c.md" from the origin

  Scenario: Three-layer precedence resolves per file
    Given an intermediate OCI skill that replaces "references/b.md"
    And my repo replaces "references/a.md"
    When I compose the skill
    Then "references/a.md" comes from my repo
    And "references/b.md" comes from the intermediate skill
    And "references/c.md" comes from the origin

  Scenario: SKILL.md composes via operation-merge across layers
    Given the origin SKILL.md has a "Usage" section
    And a lower layer appends a reference line to SKILL.md
    And my repo patches the "Usage" section of SKILL.md
    When I compose the skill
    Then the merged SKILL.md contains both the appended line and my patched Usage section

  Scenario: A git dependency is pinned by commit and tree SHA
    Given my repo depends on a skill in the git server at ref "v2.1.0" subpath "skills/shared"
    When I compose the skill
    Then the lock records the resolved commit SHA
    And the lock records the git tree object SHA of the subpath

  Scenario: A required overlay operation that does not match fails
    Given my repo has an overlay with a replace-in-file marked required:true
    And the pattern does not match the base content
    When I compose the skill
    Then composition fails with a required-operation error

  Scenario: Publish an overlay to overlay the origin
    Given a local overlay against "pdf-tools"
    When I run "epos overlay push registry/overlays/team-refs:0.2.0"
    Then the overlay is stored as an OCI artifact
    And it can be declared as a pulled overlay layer by digest
