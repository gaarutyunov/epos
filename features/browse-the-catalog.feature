Feature: Browse the catalog
  epos-registry can serve a read-only HTML catalog of the registry it fronts,
  on the listener that already answers /v2/, and export the same pages to a
  directory for a static host. The catalog is off by default.

  Background:
    Given a registry holding published skills
    And epos-registry is fronting it with the catalog enabled

  Scenario: The catalog lists the skills the registry holds
    When the catalog's home page is requested
    Then the page lists every published skill
    And every skill links to a page of its own

  Scenario: A skill's page renders its document
    When the page for "demo/agent-skills/pdf" is requested
    Then the page carries the skill's SKILL.md rendered as markup
    And the page does not carry the frontmatter

  Scenario: A hostile document renders nothing executable
    When the page for "demo/agent-skills/hostile" is requested
    Then the page carries no script tag and no event handler

  Scenario: Enabling the catalog does not change the distribution API
    When each distribution API endpoint is requested
    Then every one of them is answered by the relay
    And a pull through epos-registry still succeeds

  Scenario: The catalog answers only for the skills in its index
    When a page for a repository the catalog does not list is requested
    Then the catalog answers 404
    And no request for that repository reaches the registry

  Scenario: Counts are read for every request
    Given a statistics source holding 7 verified pulls of "demo/agent-skills/pdf"
    When the catalog's home page is requested
    Then the page shows 7 pulls for "demo/agent-skills/pdf"
    When the statistics source is changed to 8 verified pulls
    And the catalog's home page is requested again
    Then the page shows 8 pulls for "demo/agent-skills/pdf"

  Scenario: Without a statistics source the home page ranks nothing
    When the catalog's home page is requested
    Then the page carries no pull column

  Scenario: A skill with no recorded pulls is unknown rather than zero
    Given a statistics source holding 7 verified pulls of "demo/agent-skills/pdf"
    When the catalog's home page is requested
    Then the row for "demo/agent-skills/reviewer" shows an unknown count

  Scenario: The export produces the same site the server serves
    When the catalog is exported to a directory
    Then the exported home page is byte-identical to the served one
    And the exported directory carries a page for every skill
    And the exported directory carries the stylesheet and the script
