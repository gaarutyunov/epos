Feature: Discover and search skills across registries
  As a skill user
  I want the proxy to discover skills via catalog or registration
  So that I can browse and filter available skills

  Background:
    Given a running OCI registry with a "_catalog" endpoint
    And published skills "pdf-tools", "csv-tools", "img-tools"

  Scenario: Auto-detect catalog mode from a probe
    When the proxy probes the registry
    Then the discovery mode is "catalog"

  Scenario: Fall back to registered mode when catalog is unavailable
    Given a registry that returns 404 for "_catalog"
    When the proxy probes that registry
    Then the discovery mode is "registered"
    And only the declared repositories are listed

  Scenario: List the catalog through the proxy
    When I request the catalog listing through the proxy
    Then the listing includes "pdf-tools", "csv-tools", and "img-tools"

  Scenario: Credential pass-through stores no secrets
    Given the registry requires basic auth
    When I pull "pdf-tools" through the proxy with my credentials
    Then the pull succeeds
    And the proxy persists no credentials

  Scenario: Filter the federated frontend listing
    When I open the frontend and filter by keyword "csv"
    Then only "csv-tools" is shown
