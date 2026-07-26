Feature: Author and publish a skill
  A skill is a directory with a SKILL.md. Epos packs it into an OCI artifact,
  keeps it in a local store, and pushes it to whichever registry you already
  run. There is no Epos write server: push uses your own credentials, and
  nothing is validated server-side.

  Background:
    Given a registry

  # SPEC 2.1 -- exactly one content layer, rooted at the skill name, with the
  # config blob inlined so a pull fetches one blob.
  Scenario: A skill directory becomes a conformant artifact
    Given a skill directory "reviewer" version "1.0.0"
    When the author packs it
    Then the artifact has exactly one content layer
    And the artifact carries the agent-skills artifact type
    And the config blob is inlined in the manifest

  # SPEC 2.4 -- packing is a pure function of its inputs.
  Scenario: Packing the same directory twice produces the same digest
    Given a skill directory "reviewer" version "1.0.0"
    When the author packs it
    And the author packs it again
    Then both packs report the same digest

  # SPEC 2.4 -- and the digest must not depend on how the files got there.
  Scenario: Two identical directories produce the same digest
    Given a skill directory "reviewer" version "1.0.0"
    And an identical skill directory written in a different order
    When the author packs both
    Then both packs report the same digest

  # SPEC 6.1 -- push is a plain OCI push, no write server involved.
  Scenario: A packed skill is pushed to a registry
    Given a skill directory "reviewer" version "1.0.0"
    And the author packs it
    When the author pushes it to the registry
    Then the registry holds the skill

  # SPEC 9.1 -- pull brings it back, tagged, into a store that never had it.
  Scenario: A published skill is pulled into an empty store
    Given a skill directory "reviewer" version "1.0.0"
    And the author packs it
    And the author pushes it to the registry
    When a second machine pulls it
    Then that store holds "reviewer:1.0.0"
    And the pulled digest matches the pushed digest

  # SPEC 2.1 -- the artifact is conformant, so a client that has never heard
  # of Epos can consume it.
  Scenario: A published skill is pulled by plain oras
    Given a skill directory "reviewer" version "1.0.0"
    And the author packs it
    And the author pushes it to the registry
    When plain oras pulls it
    Then the pulled artifact matches the one pushed

  # SPEC 9.3 -- manual collection, mark and sweep from the tags.
  Scenario: Prune keeps what a tag reaches and sweeps the rest
    Given a skill directory "reviewer" version "1.0.0"
    And the author packs it
    And an unreferenced blob is in the store
    When the author prunes the store
    Then the unreferenced blob is gone
    And the store still holds "reviewer:1.0.0"

  # SPEC 2.5 -- rejected at pack, as a security measure.
  Scenario: A skill containing a symlink is refused
    Given a skill directory "reviewer" version "1.0.0"
    And the directory contains a symlink
    When the author packs it
    Then packing fails

  # SPEC 4.5 -- one configured host serves both directions, so users point at
  # one registry rather than two.
  #
  # @wip pending a decision on the issue: 4.5 redirects the upload session to
  # upstream, and oras-go refuses a cross-host upload Location as a fix for
  # GHSA-jxpm-75mh-9fp7. No oras-go client -- including epos push -- can
  # complete a push through epos-registry as 4.5 specifies it.
  @wip
  Scenario: A skill is published through epos-registry
    Given epos-registry is fronting the registry
    And a skill directory "reviewer" version "1.0.0"
    And the author packs it
    When the author pushes it through epos-registry
    Then the registry holds the skill
    And no upload bytes crossed epos-registry

  # SPEC 5.4 -- a publish is a manifest PUT upstream accepts with 201.
  # @wip for the same reason as the scenario above: the push cannot complete.
  @wip
  Scenario: Publishing through epos-registry counts a publish
    Given epos-registry is fronting the registry
    And a skill directory "reviewer" version "1.0.0"
    And the author packs it
    When the author pushes it through epos-registry
    Then the publish count for "demo/agent-skills/reviewer" is 1
