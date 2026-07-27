Feature: Build a skill with a Skillfile
  A Skillfile derives a skill from a base and writes one conformant artifact
  into the local store. The recipe lives in git and the store keeps the result,
  exactly as Docker does it.

  Nothing in this feature touches a registry. The base is fetched from a git
  server, the build is evaluated in memory, and the artifact stays in
  ~/.epos/store — so a Skillfile with local and git bases is a complete,
  standalone workflow.

  Background:
    Given a git server holding a base skill

  # SPEC 8.3, 8.7 -- the git scheme, and the result lands in the local store.
  Scenario: A skill derived from a git base builds into the local store
    Given a Skillfile deriving "reviewer" from that git base
    When the author builds it
    Then the build succeeds
    And the store holds "reviewer:2.0.0"
    And the artifact has exactly one content layer
    And the layer holds "reviewer/SKILL.md" containing "name: reviewer"
    And the layer holds "reviewer/references/notes.md" containing "in-house notes"
    And the layer does not hold "reviewer/extra.md"

  # SPEC 8.3 -- a branch is mutable, so the commit and tree SHAs are the only
  # record of what the build actually descended from.
  Scenario: The build reports and records the pin of the git base it used
    Given a Skillfile deriving "reviewer" from that git base
    When the author builds it
    Then the build reports the commit and tree of the git base
    And the artifact records the git base in its provenance annotations

  # SPEC 2.4 -- same bases, same Skillfile, same context, same digest, on a
  # machine that has never seen the skill before.
  Scenario: Building the same Skillfile twice produces the same digest
    Given a Skillfile deriving "reviewer" from that git base
    When the author builds it
    And a second machine builds it
    Then both builds report the same digest

  # SPEC 8.7 -- --build-arg beats the ARG default.
  Scenario: A build argument overrides the Skillfile's default
    Given a Skillfile deriving "reviewer" from that git base
    When the author builds it with the build argument "language=Rust"
    Then the build succeeds
    And the layer holds "reviewer/SKILL.md" containing "language: Rust"

  # SPEC 8.2.2 and 8.2.4 -- warnings, not errors, because idempotent edits must
  # stay expressible; but a Skillfile that has silently stopped editing its base
  # is what those clauses trade an error for, so the author is told.
  Scenario: A no-op REPLACE and an absent UNSET key are warned about
    Given a Skillfile whose REPLACE matches nothing and whose UNSET key is absent
    When the author builds it
    Then the build succeeds
    And the build warns that the REPLACE matched nothing
    And the build warns that the UNSET key was already absent

  # SPEC 8.4 -- composition is explicit enumeration, not merge-by-default.
  Scenario: A second stage contributes only what COPY --from names
    Given a Skillfile composing the git base with a local base
    When the author builds it
    Then the build succeeds
    And the layer holds "reviewer/references/style.md" containing "House style"
    And the layer does not hold "reviewer/extra.md"

  # SPEC 8.4 -- the worked example writes `FROM base` after `FROM ... AS base`,
  # so a bare name an earlier FROM bound is that stage and not a directory in
  # the context.
  Scenario: A later FROM starts from a stage declared earlier
    Given a Skillfile whose last stage starts from a stage declared earlier
    When the author builds it
    Then the build succeeds
    And the layer holds "reviewer/references/style.md" containing "House style"
    And the layer holds "reviewer/references/house.md" containing "house rules"
    And the layer does not hold "reviewer/extra.md"

  # SPEC 8.2.4 -- SET edits the document, it does not rewrite it: the comments,
  # the key order and the quoting are the skill author's, and an edit that
  # normalised them would be rewriting a file it was asked to amend.
  Scenario: SET leaves the frontmatter's comments, order and quoting alone
    Given a Skillfile setting one key of a hand-written frontmatter block
    When the author builds it
    Then the build succeeds
    And the layer's "reviewer/SKILL.md" is exactly:
      """
      ---
      # the fields an agent reads before loading anything
      name: reviewer
      version: "2.0.0" # pinned by hand, and a string on purpose
      description: reviews code
      model: opus # the cheap one
      language: 'Python'
      ---

      # Reviewer
      """

  # SPEC 2.1 -- the layer extracts to something indistinguishable from a
  # hand-authored skill directory, which is what makes the built artifact
  # usable in a worktree.
  Scenario: The built artifact extracts as an ordinary skill directory
    Given a Skillfile deriving "reviewer" from that git base
    When the author builds it
    And the artifact is extracted into a worktree
    Then the worktree holds the skill directory the Skillfile built
