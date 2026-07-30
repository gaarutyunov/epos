Feature: Author and publish a skill
  A skill is a directory with a SKILL.md. Epos packs it into an OCI artifact,
  keeps it in a local store, publishes it to whichever registry you already run,
  and pulls published skills back out again.

  Publishing is `epos push`: a direct copy from the local store to the upstream
  registry, needing no second OCI client. The epos-registry *write path* stays
  withdrawn (SPEC 4.5) — it would have to redirect the upload session to
  upstream, and oras-go refuses a cross-host upload Location as the fix for
  GHSA-jxpm-75mh-9fp7. That check compares the Location against the registry the
  client was pointed at, so a client pointed straight at the upstream gets that
  upstream's own Location and is unaffected. Two references stay two references:
  epos-registry for reading, the upstream for publishing.

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

  # SPEC 9.1, 2.4 -- push moves bytes and derives nothing, so the digest pack
  # printed is the digest push reports is the digest a machine that has never
  # seen the skill pulls back.
  Scenario: A pushed skill is pulled back into a second store
    Given a skill directory "reviewer" version "1.0.0"
    And the author packs it
    When the author pushes it to the registry
    And a second machine pulls it
    Then that store holds "reviewer:1.0.0"
    And the pulled digest matches the pushed digest
    And every digest reported is the digest pack printed

  # SPEC 2.4 -- content addressing makes a repeat publish a no-op by
  # definition, so a second push must neither fail nor land on a new digest.
  Scenario: Pushing an unchanged skill twice changes nothing
    Given a skill directory "reviewer" version "1.0.0"
    And the author packs it
    And the author pushes it to the registry
    When the author pushes it to the registry
    Then every digest reported is the digest pack printed
    And the tags of "demo/agent-skills/reviewer" are exactly "1.0.0"

  # SPEC 2.1 -- what `epos push` publishes is conformant, so a client that has
  # never heard of Epos can consume it. This is the claim the new publishing
  # path must not quietly break.
  Scenario: A skill published by epos push is pulled by plain oras
    Given a skill directory "reviewer" version "1.0.0"
    And the author packs it
    And the author pushes it to the registry
    When plain oras pulls it
    Then the pulled artifact matches the one pushed

  # SPEC 2.1 -- the repository name identifies the skill without a manifest
  # lookup, so the destination names a namespace and the skill's name is
  # appended. The remote tag is the version alone; only the local store needs
  # <name>:<version>, because one flat layout holds many skills.
  Scenario: The published repository is the namespace plus the skill name
    Given a skill directory "reviewer" version "1.0.0"
    And the author packs it
    When the author pushes it to the registry
    Then the registry holds "demo/agent-skills/reviewer" tagged "1.0.0"
    And the tags of "demo/agent-skills/reviewer" are exactly "1.0.0"
    And the push reports the resolved reference and the digest

  # The issue's own test: a fresh machine publishes with epos and nothing else.
  # A 401 must say which command would fix it -- and 403 is deliberately not
  # folded in, because an unauthorised credential is a different problem.
  Scenario: Pushing to an authenticated registry needs a login first
    Given a registry that requires authentication
    And a skill directory "reviewer" version "1.0.0"
    And the author packs it
    When the author pushes it while logged out
    Then the push is refused, naming the registry and "epos registry login"
    When the author logs in to the registry
    And the author pushes it to the registry
    Then the registry holds "demo/agent-skills/reviewer" tagged "1.0.0"
    And no secret was passed in an argument

  # SPEC 2.3, 8 -- a derived skill's provenance rides on the manifest, and push
  # copies the manifest rather than re-deriving it, so it arrives byte for byte.
  Scenario: A skill built from a Skillfile publishes with its provenance intact
    Given a skill directory "reviewer" version "1.0.0"
    And the author packs it
    And a Skillfile deriving "reviewer-plus" version "2.0.0" from that directory
    And the author builds it
    When the author pushes "reviewer-plus:2.0.0" to the registry
    Then the published manifest is identical to the one in the local store
    And the published manifest carries the build's provenance annotations

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
