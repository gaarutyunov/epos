Feature: Install a skill locally
  As a skill user
  I want to install a skill to local files with pinned dependencies
  So that my installation is reproducible and rollback-able

  Background:
    Given a running OCI registry
    And a published skill "pdf-tools" version "1.4.2"

  Scenario: Install materializes files and writes a lockfile
    When I run "epos install pdf pdf-tools --target=files"
    Then the skill files are written to the local target directory
    And a lockfile records the release "pdf" at revision 1
    And the lockfile pins the skill by digest

  Scenario: Reinstall with a digest mismatch is a hard error
    Given the lockfile pins "pdf-tools" to a digest that no longer matches the tag
    When I run "epos install pdf pdf-tools --target=files --frozen"
    Then the install fails with a digest mismatch error

  Scenario: Upgrade creates a new revision
    Given release "pdf" is installed at revision 1
    And a published skill "pdf-tools" version "1.5.0"
    When I run "epos upgrade pdf pdf-tools --version 1.5.0 --target=files"
    Then the lockfile records revision 2
    And the history shows both revisions

  Scenario: Rollback restores the whole bundle
    Given release "pdf" is installed at revision 2
    When I run "epos rollback pdf 1"
    Then the files match revision 1
    And a new revision 3 is recorded whose content equals revision 1
