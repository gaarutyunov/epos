Feature: Install a skill locally
  A skill artifact carries its templates verbatim. Installing is where they are
  rendered, with the values the user supplies, and nothing upstream of the
  install renders anything — the registry least of all.

  What a worktree ends up on is written down twice: skills.json says what was
  asked for, and skills.lock.json pins the digest that answered. The store is a
  cache and the lock is the truth, so two worktrees sit on two different
  versions of one skill out of one store, at the same time.

  Nothing here starts a container and nothing here reaches a registry. The
  skills are packed and built from directories on disk, and install resolves
  what the local store already holds.

  Background:
    Given a parameterised skill packed at "1.0.0" and at "2.0.0"
    And a values file:
      """
      title: The reviewer
      model: opus
      global:
        org: Acme
      """

  # SPEC 10.1, 10.2 and 10.4 -- the first half of the gate.
  Scenario: A parameterised skill installs into .claude/skills
    When the author installs "reviewer:2.0.0"
    Then the install succeeds
    And ".claude/skills/reviewer/SKILL.md" contains "# The reviewer"
    And ".claude/skills/reviewer/SKILL.md" contains "model: 'opus'"
    And ".claude/skills/reviewer/SKILL.md" contains "Reviewed for Acme"
    And ".claude/skills/reviewer/SKILL.md" does not contain "{{"
    And ".claude/skills/reviewer/references/notes.md" contains "The reviewer"

  # SPEC 8.6 and 10.1 -- the template reached the store untouched and is
  # rendered here and nowhere earlier.
  Scenario: The stored artifact keeps the template the install rendered
    When the author installs "reviewer:2.0.0"
    Then the install succeeds
    And the stored artifact still holds "{{ .Values.title }}"

  # SPEC 10.3 -- --set is the later word.
  Scenario: --set overrides the values file
    When the author installs "reviewer:2.0.0" with "--set title=From the flag"
    Then the install succeeds
    And ".claude/skills/reviewer/SKILL.md" contains "# From the flag"
    And ".claude/skills/reviewer/SKILL.md" contains "model: 'opus'"

  # SPEC 10.3 and 8.4 -- values nest under the Skillfile stage name, which is
  # what lets two stages both write .Values.title and mean two different
  # things. Without the scoping this scenario is the collision.
  Scenario: Two stages both using .Values.title do not collide
    Given a skill built from two stages that both use ".Values.title"
    And a values file:
      """
      global:
        org: Acme
      title: The final stage
      model: opus
      shared:
        title: The shared stage
      """
    When the author installs "reviewer:3.0.0"
    Then the install succeeds
    And ".claude/skills/reviewer/SKILL.md" contains "# The final stage"
    And ".claude/skills/reviewer/references/shared.md" contains "# The shared stage"
    And ".claude/skills/reviewer/references/shared.md" does not contain "The final stage"

  # SPEC 10.3 -- global is the deliberate cross-stage channel, so it is the one
  # thing both scopes see.
  Scenario: The global block reaches every stage
    Given a skill built from two stages that both use ".Values.title"
    And a values file:
      """
      global:
        org: Acme
      title: The final stage
      model: opus
      shared:
        title: The shared stage
      """
    When the author installs "reviewer:3.0.0"
    Then the install succeeds
    And ".claude/skills/reviewer/SKILL.md" contains "Reviewed for Acme"
    And ".claude/skills/reviewer/references/shared.md" contains "Shared at Acme"

  # A skill installed with a hole in it is worse than an install that stops.
  Scenario: A value nobody supplied stops the install
    Given a values file:
      """
      title: The reviewer
      """
    When the author installs "reviewer:2.0.0"
    Then the install fails
    And the error names the file it could not render
    And ".claude/skills/reviewer" does not exist
    And "skills.lock.json" does not exist

  # SPEC 10.2 -- additionalBasePaths covers the other agent vendors.
  Scenario: additionalBasePaths installs the skill for a second vendor
    Given skills.json naming ".cursor/skills" as an additional base path
    When the author installs "reviewer:2.0.0"
    Then the install succeeds
    And ".claude/skills/reviewer/SKILL.md" contains "# The reviewer"
    And ".cursor/skills/reviewer/SKILL.md" contains "# The reviewer"
    And the lock records both base paths

  # SPEC 10.2 -- a pin file, never a symlink, so nothing depends on a Windows
  # symlink working.
  Scenario: The lock pins the digest and is a plain file
    When the author installs "reviewer:2.0.0"
    Then the install succeeds
    And "skills.lock.json" is a regular file and not a symlink
    And the lock pins "reviewer" at the digest the store holds for "reviewer:2.0.0"
    And "skills.json" declares "reviewer"

  # A pin that differs between two runs that installed the same thing is not a
  # pin. Ranging a Go map to write it would be enough to break this.
  Scenario: Installing the same skill twice writes the same lock
    When the author installs "reviewer:2.0.0"
    And the author installs "reviewer:2.0.0"
    Then the install succeeds
    And both installs wrote the same lock

  # SPEC 10.2 and 9.2 -- the second half of the gate. The lock is per-worktree
  # and the store lock is shared, so one store answers two worktrees at once.
  Scenario: Two worktrees pin different digests from one store simultaneously
    When two worktrees install "reviewer:1.0.0" and "reviewer:2.0.0" at the same time
    Then both installs succeed
    And the two worktrees pinned different digests
    And the first worktree's ".claude/skills/reviewer/SKILL.md" contains "version: 1.0.0"
    And the second worktree's ".claude/skills/reviewer/SKILL.md" contains "version: 2.0.0"
    And the store still holds both versions

  # SPEC 9.2 -- shared, not exclusive. A lock already held for reading must not
  # make an install wait, or parallel worktree installs serialise after all.
  Scenario: An install proceeds while the store's shared lock is already held
    Given the store's shared lock is held open
    When the author installs "reviewer:2.0.0"
    Then the install succeeds
    And ".claude/skills/reviewer/SKILL.md" contains "# The reviewer"

  # SPEC 10.4 -- ls reads the lock, because the lock is what the worktree
  # installed; uninstall takes the skill out of the worktree and leaves the
  # store's copy alone, which is what makes the store a cache.
  Scenario: ls lists what the worktree pinned and uninstall takes it away
    When the author installs "reviewer:2.0.0"
    And the author lists what the worktree pinned
    Then the listing contains "reviewer:2.0.0"
    When the author uninstalls "reviewer"
    And the author lists what the worktree pinned
    Then the listing is empty
    And ".claude/skills/reviewer" does not exist
    And "skills.json" does not declare "reviewer"
    And the store still holds both versions
