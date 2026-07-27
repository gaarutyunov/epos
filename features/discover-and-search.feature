Feature: Discover and search skills
  Discovery is entirely client-side. epos reads the registry's catalog through
  epos-registry, keeps the repositories under the configured skill namespace,
  and only then asks each repository for its versions and each manifest for its
  name and description. There is no discovery endpoint, no content negotiation
  and no Epos-specific media type.

  It works only against registries that implement GET /v2/_catalog. Epos does
  not compensate for a registry that does not: it reports the capability as
  unavailable and exits non-zero. Direct references need no catalog and keep
  working.

  Background:
    Given a registry holding published skills
    And epos-registry is fronting it

  # SPEC 7.2 -- steps 1 and 2: the catalog, filtered to the skill namespace.
  Scenario: Skills are listed from the registry catalog
    When the author lists the skills
    Then the listing contains "demo/agent-skills/pdf"
    And the listing contains "demo/agent-skills/reviewer"

  # SPEC 7.2 step 2 -- the registry also holds a repository that is not a skill.
  Scenario: Repositories outside the skill namespace are not listed
    When the author lists the skills
    Then the listing does not contain "other/toolbox"

  # SPEC 7.2 -- steps 3 and 4 are lazy. A stated requirement, not an
  # optimisation, so the proof is the requests the client actually made.
  Scenario: Listing without --versions asks no repository for anything
    When the author lists the skills
    Then the registry catalog was requested
    And no repository was asked for its tags
    And no manifest was resolved

  # SPEC 7.2 -- --versions opts into steps 3 and 4.
  Scenario: Listing with --versions reports every version
    When the author lists the skills with versions
    Then the listing contains "demo/agent-skills/pdf:1.0.0"
    And the listing contains "demo/agent-skills/pdf:1.1.0"
    And every repository was asked for its tags
    And the listing carries the skill name and description

  # A listing whose order depends on the registry, or on map iteration, is a
  # listing that differs between runs.
  Scenario: The listing has a defined order
    When the author lists the skills with versions
    Then the listing is ordered by repository and then version

  # SPEC 7.3 -- a client-side filter over the enumeration, not a server query.
  Scenario: Search matches the skill description
    When the author searches for "extracts text"
    Then the listing contains "demo/agent-skills/pdf:1.0.0"
    And the listing does not contain "demo/agent-skills/reviewer"

  Scenario: Search matches the repository and skill name
    When the author searches for "reviewer"
    Then the listing contains "demo/agent-skills/reviewer:1.0.0"
    And the listing does not contain "demo/agent-skills/pdf"

  Scenario: A search that matches nothing lists nothing
    When the author searches for "spreadsheet"
    Then the listing is empty
    And the command succeeded

  # SPEC 4.1 -- proxied when the upstream supports it.
  Scenario: epos-registry relays the upstream catalog
    When a client requests the catalog through epos-registry
    Then the response status is 200
    And the relayed catalog contains "demo/agent-skills/pdf"

  # SPEC 4.1 -- where the upstream does not support it, upstream's own response
  # is relayed unchanged rather than replaced by an invented catalog.
  Scenario: epos-registry relays an upstream that has no catalog unchanged
    Given the upstream does not implement _catalog
    When a client requests the catalog through epos-registry
    Then the response status is 404

  # SPEC 7.1 -- reported as a missing capability, not as a bare HTTP failure.
  Scenario: Listing a registry without _catalog reports the capability unavailable
    Given the upstream does not implement _catalog
    When the author lists the skills
    Then the command failed
    And the error says catalog enumeration is unsupported

  Scenario: Searching a registry without _catalog reports the capability unavailable
    Given the upstream does not implement _catalog
    When the author searches for "pdf"
    Then the command failed
    And the error says catalog enumeration is unsupported

  # SPEC 7.1 -- a direct reference needs no catalog, so losing discovery must
  # not cost anything else.
  Scenario: A direct pull still works without a catalog
    Given the upstream does not implement _catalog
    When a second machine pulls "demo/agent-skills/pdf:1.0.0" directly
    Then the pull succeeded
    And that machine's store holds "pdf:1.0.0"
