Feature: Build a skill from a registry base
  A Skillfile can name an OCI reference as its base. This is the point at
  which Track B joins Track A: the base comes out of a registry, and the
  result is an ordinary conformant artifact that any client can read.

  A tag is mutable, so naming one is not a pin. The build resolves the
  reference to a manifest digest and records that, because the digest is the
  only thing that names the bytes the artifact actually descended from.

  Nothing about the derivation is traversable afterwards. The registry stores
  results, the recipe lives in git, and the provenance annotations are a
  description a reader may consult -- not an index anyone can query.

  Background:
    Given a registry holding a base skill

  # SPEC 8.3, 8.7 -- the OCI scheme, and the result lands in the local store.
  Scenario: A skill derived from a registry base builds into the local store
    Given a Skillfile deriving "reviewer" from that OCI base
    When the author builds it
    Then the build succeeds
    And the store holds "reviewer:2.0.0"
    And the artifact has exactly one content layer
    And the layer holds "reviewer/SKILL.md" containing "name: reviewer"
    And the layer holds "reviewer/references/style.md" containing "House style"
    And the layer holds "reviewer/references/notes.md" containing "in-house notes"
    And the layer does not hold "reviewer/extra.md"

  # SPEC 8.3 and 2.3 -- the pin is the point: a mutable tag resolves to
  # immutable content, and the digest it resolved to is recorded.
  Scenario: The build reports and records the manifest digest of the base
    Given a Skillfile deriving "reviewer" from that OCI base
    When the author builds it
    Then the build reports the manifest digest of the OCI base
    And the artifact records the OCI base in its provenance annotations
    And the recorded base digest is the one the registry holds

  # SPEC 8.3 -- a tag can be re-pushed over entirely different content, which
  # is exactly why recording the tag would not be a pin.
  Scenario: Moving the base tag changes the digest the build records
    Given a Skillfile deriving "reviewer" from that OCI base
    When the author builds it
    And the base tag is moved to different content
    And the author builds it again
    Then the build succeeds
    And the two builds record different base digests

  # SPEC 8.3 -- the recorded digest is a reference in its own right, and
  # building from it is the only thing a pin is ultimately for.
  Scenario: The recorded digest names the same base the tag did
    Given a Skillfile deriving "reviewer" from that OCI base
    When the author builds it
    And the base tag is moved to different content
    And the author builds it from the digest the first build recorded
    Then the build succeeds
    And the second build records the same base digest as the first
    And both builds produce the same content layer

  # SPEC 2.2 and 8.1 -- v2.0 defines no vnd.epos.* media type, and a derived
  # artifact is an ordinary single-layer skill: a consumer cannot tell it was
  # derived except by reading the annotations.
  Scenario: The derived artifact is indistinguishable from a hand-packed one
    Given a Skillfile deriving "reviewer" from that OCI base
    When the author builds it
    Then the build succeeds
    And the artifact declares no Epos media type
    And the artifact carries the agent-skills media types and nothing else

  # SPEC 2.5 -- validation is deliberately permissive, and a third-party base
  # may carry names the consumer deriving from it cannot fix. Rejecting them
  # would refuse a base every other tool in the ecosystem accepts.
  Scenario: A base carrying paths the consumer cannot fix still builds
    Given a registry holding a base skill with awkward but legal paths
    And a Skillfile deriving "reviewer" from that OCI base
    When the author builds it
    Then the build succeeds
    And the layer holds every awkward path the base carried

  # SPEC 2.5 -- the other half: what escapes the skill root is still rejected,
  # because that is a security rule and not a portability question.
  Scenario: A base whose paths escape the skill root is rejected
    Given a registry holding a base skill whose layer escapes its root
    And a Skillfile deriving "reviewer" from that OCI base
    When the author builds it
    Then the build fails because the base escapes the skill root

  # B2's gate. The derived skill goes to the registry through a stock client
  # and comes back through one, with its provenance intact -- which is what
  # 2.1 promises and what 2.3 is for.
  Scenario: A derived skill is published and pulled back by plain oras
    Given a Skillfile deriving "reviewer" from that OCI base
    When the author builds it
    And the derived skill is published with plain oras
    And plain oras pulls it back
    Then the pulled digest matches the digest the build reported
    And the pulled manifest carries the provenance annotations
    And the pulled manifest names the base it was built from
