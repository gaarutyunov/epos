// Package install turns an artifact the local store holds into a skill
// directory in a worktree (SPEC.md 10).
//
// Helm's model (10.1): the artifact carries its templates verbatim, and
// rendering happens here, with the values the user supplies. Nothing upstream
// renders anything — a `{{ .Values.model }}` that survived the build (8.6) is
// substituted at this point and at no earlier one.
//
// Two files record the result, both in the worktree and both written by this
// package:
//
//   - skills.json, the human-authored declaration of what the worktree wants,
//     including the additionalBasePaths of any agent vendor beyond the default
//     .claude/skills;
//   - skills.lock.json, the digest-pinned resolution, which doubles as the
//     per-worktree version pin in the manner of rust-toolchain.toml. The store
//     is a cache; the lock is the truth. It is a file and never a symlink, so
//     nothing here depends on a Windows symlink working.
//
// Installing takes the store's *shared* lock (9.2). Two worktrees pinning two
// different digests out of one store must be able to do so at the same time,
// which an exclusive lock would prevent.
package install
