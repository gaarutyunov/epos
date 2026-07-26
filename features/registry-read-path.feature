Feature: epos-registry read path
  epos-registry speaks the OCI Distribution API and nothing else, so any OCI
  client works against it unchanged. It fronts an upstream registry, holds no
  state, passes blob redirects through, and counts content downloads.

  Background:
    Given an upstream registry
    And epos-registry is fronting it

  # SPEC 4.1 -- the version check every OCI client issues first.
  Scenario: The API version check succeeds
    When a client requests "GET /v2/"
    Then the response status is 200

  # SPEC 4.3 -- so a client can tell epos-registry from a plain registry
  # without probing. "All responses" includes errors.
  Scenario: Every response carries Epos-Version
    When a client requests "GET /v2/"
    Then the response has an "Epos-Version" header

  Scenario: Error responses carry Epos-Version too
    When a client requests "GET /v2/nonexistent/repo/tags/list"
    Then the response has an "Epos-Version" header

  Scenario: A manifest is resolved by tag
    Given the skill "demo/hello" version "1.0.0" is present upstream
    When a client resolves the manifest "demo/hello:1.0.0"
    Then the response status is 200
    And the returned digest matches the digest upstream reports

  Scenario: A manifest is resolved by HEAD without fetching content
    Given the skill "demo/hello" version "1.0.0" is present upstream
    When a client issues HEAD for the manifest "demo/hello:1.0.0"
    Then the response status is 200
    And no response body is returned

  Scenario: Tags are listed for a repository
    Given the skill "demo/hello" version "1.0.0" is present upstream
    And the skill "demo/hello" version "1.1.0" is present upstream
    When a client lists the tags of "demo/hello"
    Then the tag list contains "1.0.0" and "1.1.0"

  Scenario: Referrers are listed for a digest
    Given the skill "demo/hello" version "1.0.0" is present upstream
    When a client lists the referrers of the "demo/hello:1.0.0" manifest digest
    Then the response status is 200

  # SPEC 4.2 -- blob bytes never cross epos-registry when upstream redirects.
  @wip
  Scenario: A blob fetch is relayed as a redirect
    Given the skill "demo/hello" version "1.0.0" is present upstream
    And the upstream redirects blob requests
    When a client fetches a content blob of "demo/hello:1.0.0"
    Then the response status is 307
    And no blob bytes passed through epos-registry

  # SPEC 4.2 -- the degraded case; some registries do not redirect.
  @wip
  Scenario: A blob fetch is streamed when the upstream does not redirect
    Given the skill "demo/hello" version "1.0.0" is present upstream
    And the upstream serves blobs directly
    When a client fetches a content blob of "demo/hello:1.0.0"
    Then the response status is 200
    And the blob content is returned unchanged

  # SPEC 4.2 -- object stores accept exactly one authentication mechanism and
  # reject a request carrying both a presigned URL and an Authorization header.
  @wip
  Scenario: The client Authorization header is not forwarded to a redirect target
    Given the skill "demo/hello" version "1.0.0" is present upstream
    And the upstream redirects blob requests
    When a client fetches a content blob of "demo/hello:1.0.0" with an Authorization header
    Then the redirect target receives no "Authorization" header

  # SPEC 5.1 -- a content blob GET is a download.
  @wip
  Scenario: Fetching a content blob counts as a download
    Given the skill "demo/hello" version "1.0.0" is present upstream
    When a client fetches a content blob of "demo/hello:1.0.0"
    Then the download count for "demo/hello" increases by 1

  # SPEC 5.1 -- manifest requests are resolves. The lock-file update check does
  # a digest resolve with no content fetch and would otherwise dominate counts.
  @wip
  Scenario: Resolving a manifest does not count as a download
    Given the skill "demo/hello" version "1.0.0" is present upstream
    When a client resolves the manifest "demo/hello:1.0.0"
    And a client issues HEAD for the manifest "demo/hello:1.0.0"
    Then the download count for "demo/hello" is unchanged

  # SPEC 5.2 -- the epos CLI sends Epos-Download; stock oras does not.
  @wip
  Scenario: An Epos-Download header marks the download verified
    Given the skill "demo/hello" version "1.0.0" is present upstream
    When a client fetches a content blob of "demo/hello:1.0.0" sending "Epos-Download: demo/hello@1.0.0"
    Then the recorded download is verified

  @wip
  Scenario: A download without the header is recorded unverified
    Given the skill "demo/hello" version "1.0.0" is present upstream
    When a client fetches a content blob of "demo/hello:1.0.0"
    Then the recorded download is unverified

  # SPEC 4.1 -- pointing oras at epos-registry requires no client changes.
  @wip
  Scenario: Plain oras pulls through epos-registry unchanged
    Given the skill "demo/hello" version "1.0.0" is present upstream
    When oras pulls "demo/hello:1.0.0" through epos-registry
    Then the pulled artifact matches the one pushed upstream

  # SPEC 4.4 -- no manifest cache, no digest-to-role table, no shared store.
  # Any request may land on any replica.
  @wip
  Scenario: Requests may land on any replica
    Given the skill "demo/hello" version "1.0.0" is present upstream
    And a second epos-registry replica is fronting the same upstream
    When a client resolves the manifest "demo/hello:1.0.0" against the first replica
    And a client fetches a content blob of "demo/hello:1.0.0" against the second replica
    Then both requests succeed
