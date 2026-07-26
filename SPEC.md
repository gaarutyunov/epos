# EPOS — OCI-Native Packaging and Composition for Agent Skills

> **Epos** is named after the [library ship](https://en.wikipedia.org/wiki/Epos_\(library_ship\)) that ferried books between coastal towns.

**Spec version:** v2.0
**Status:** Proposed — no open questions
**Supersedes:** Epos v1.x (overlay-artifact model, upload server, bounded-context modularity)

-----

## 1. Purpose and Scope

Epos distributes and composes AI-agent **Skills** — `SKILL.md` directories as defined by the Agent Skills specification. The Agent Skills spec defines the on-disk format and stops there; it says nothing about distribution, versioning, integrity, or consumption behind a firewall. Epos fills that gap using OCI registries as the transport and storage substrate.

Epos consists of two independent tracks:

- **Track A — registry.** `epos-registry`, which fronts an upstream OCI registry and records usage, plus a CLI that packs, pulls, and installs conformant skill artifacts. Publishing is not Epos’s job (§4.5).
- **Track B — build.** `Skillfile`, a Dockerfile-shaped build language for deriving skills from other skills. Track B requires no registry at all; it joins Track A only when `FROM` names an OCI reference.

### 1.1 Out of scope (v2.0)

- Identity provider integration; Epos does not issue credentials.
- Private-registry credential brokering and authenticated upstreams.
- Caching or blob-streaming modes.
- `RUN`-style arbitrary command execution in builds.
- A write server that packs, validates, or holds credentials. `epos-registry` relays writes (§4.5) but transforms nothing; the CLI packs and holds the user’s own credentials.
- **Kubernetes and cluster install.** Deferred to a separate spec (`EPOS-K8S.md`).

### 1.2 Hard constraints

- Pure Go. **No cgo.** Cross-compilation is non-negotiable.
- No daemon. Every binary is a single static executable.
- Linux, macOS, and Windows.
- Versioned markdown spec files are the primary deliverable; this document is normative.

-----

## 2. Artifact Format

### 2.1 Conformance

Epos skill artifacts **conform to** the Agent Skills OCI Artifacts specification (draft v0.1.0, Apache-2.0). An artifact produced by Epos is pullable and usable by any conforming client — `oras`, `skills-oci`, Arconia CLI — with no Epos-specific knowledge.

|Role         |Media type                                             |
|-------------|-------------------------------------------------------|
|Artifact type|`application/vnd.agentskills.skill.v1`                 |
|Config blob  |`application/vnd.agentskills.skill.config.v1+json`     |
|Content layer|`application/vnd.agentskills.skill.content.v1.tar+gzip`|
|Collection   |`application/vnd.agentskills.collection.v1`            |

Invariants Epos must preserve:

- **Exactly one content layer.** No multi-layer skill artifacts.
- The layer is a tar+gzip rooted at `<skill-name>/`, and its extracted form is **indistinguishable from a hand-authored skill directory**.
- The config blob mirrors `SKILL.md` frontmatter. Epos inlines it via the descriptor `data` field, so a skill artifact has exactly one separately-fetchable blob.
- Repository convention is one skill per repository: `<registry>/<namespace>/agent-skills/<skill-name>`. The repository name therefore identifies the skill without any manifest lookup.

### 2.2 Epos extensions

The `vnd.epos.*` namespace is reserved for Epos-native concepts, which must never alter the skill artifact. **v2.0 defines no such types.** Overlays became `Skillfile` (§8), which produces ordinary conformant artifacts, and discovery is served by the registry’s own `_catalog` (§7) rather than by an Epos-specific representation.

### 2.3 Provenance annotations

A skill built from a base (§8) records provenance using standard OCI annotations on the manifest:

|Annotation                            |Value                                                 |
|--------------------------------------|------------------------------------------------------|
|`org.opencontainers.image.base.name`  |Base reference as written in `FROM`                   |
|`org.opencontainers.image.base.digest`|Resolved base digest (OCI) or commit+tree SHA (git)   |
|`dev.epos.skillfile.digest`           |SHA-256 of the `Skillfile` that produced this artifact|

Provenance is **descriptive, not traversable**: the registry cannot be queried for “what derives from this skill.” This matches Docker, where the recipe lives in git and the registry stores only results.

### 2.4 Determinism

Packing is a pure function of its inputs. The tar stream fixes entry order (lexicographic), mtimes (zero), uid/gid (0), and permissions (0644 files, 0755 directories) so that identical inputs yield identical digests on every platform.

### 2.5 Path handling

Paths in `Skillfile` and in tar entry names use **forward slashes exclusively**, on every platform. `pack` applies `filepath.ToSlash` when writing entries and `install` applies `filepath.FromSlash` when extracting — the same mechanism `moby/go-archive` uses.

**Rejected**, at pack and at install, as a security measure:

- `..` components, absolute paths, and any entry escaping the `<skill-name>/` root
- symlinks

**Not validated**, and therefore accepted:

- Windows reserved device names (`CON`, `PRN`, `AUX`, `NUL`, `COM1`–`COM9`, `LPT1`–`LPT9`)
- the characters `< > : " \ | ? *`
- trailing dots and spaces
- names colliding only under case-insensitive comparison

These are legal on Linux. Extraction fails on platforms that cannot represent them, at install time.

**Rationale.** This matches what the ecosystem actually does. Docker normalises separators and validates nothing further — image layers are never extracted onto a host filesystem, so the problem does not arise for it. Git does not validate either: with `core.protectNTFS=true`, `aux.md`, `con`, `a:b.md` and `trailing.` all check out without complaint, because `protectNTFS` guards `.git` name aliasing rather than portability. Strict validation at pack would reject skills every other tool accepts, including third-party bases pulled via `FROM` that the consumer cannot fix.

**Accepted consequence:** a skill authored on Linux may be un-installable on Windows, and this is discovered at install.

Because paths never contain backslashes, backslash remains unambiguous as the Skillfile escape character and BuildKit’s `# escape=` directive is unnecessary.

-----

## 3. Components

Components are **separate binaries** built from one repository and one Go module (§13.4). There is no `-target` flag and no module registry — which binary you run determines what runs.

|Binary         |Role                                                                         |Track|
|---------------|-----------------------------------------------------------------------------|-----|
|`epos`         |CLI: pack, pull, build, install, store management                            |A + B|
|`epos-registry`|Registry fronting an upstream OCI registry — one host for both read and write|A    |

Each has its own build, release, and container image; deployment treats each as an independent workload.

`epos-registry` implements the Distribution API surface but delegates all storage to upstream: it holds no blobs and no durable state (§4.4). It is a registry by protocol, not by storage. Capabilities that require owning an index — chiefly discovery (§7.4) — are added to it over time, making it progressively less of a pass-through.

-----

## 4. `epos-registry`

### 4.1 Protocol

`epos-registry` speaks the **OCI Distribution API** and nothing else. Any OCI client works against it unchanged; pointing `oras` at `epos-registry` instead of the upstream registry requires no client changes.

Required endpoints (Pull + Content Discovery conformance categories):

```
GET  /v2/
HEAD /v2/<name>/manifests/<reference>
GET  /v2/<name>/manifests/<reference>
GET  /v2/<name>/blobs/<digest>
GET  /v2/<name>/tags/list
GET  /v2/<name>/referrers/<digest>
GET  /v2/_catalog                      # proxied only if upstream supports it
```

Content Management (`DELETE`) is not implemented, and neither is the write path: blob uploads and manifest `PUT` are not served (§4.5).

`GET /v2/_catalog` is proxied **when the upstream registry supports it**, and is the basis for discovery (§7). It is outside the Content Discovery conformance category and is disabled on several hosted registries; where upstream does not support it, `epos-registry` relays upstream’s response unchanged and `epos search` reports the capability as unavailable.

### 4.2 Blob transfer posture

`epos-registry` **passes redirects through**. Blob bytes never cross it.

- Upstream returns 307 → the 307 is relayed to the client.
- Upstream returns 200 → the response is streamed (degraded case; some registries do not redirect).

`epos-registry` **must not** forward the client’s `Authorization` header to a redirect target. Object stores such as S3 accept exactly one authentication mechanism and reject requests carrying both a presigned URL and an `Authorization` header.

Consequence, stated plainly: clients need network egress to the upstream’s CDN. Epos is not an egress boundary in v2.0.

### 4.3 Discoverability

`epos-registry` sets `Epos-Version: <semver>` on all responses so a client can distinguish it from a plain registry without probing.

### 4.4 Statelessness

`epos-registry` holds no durable state. No manifest cache, no digest→role lookup table, no shared store between replicas. Scaling is N replicas behind a load balancer; any request may land on any replica.

### 4.5 Write path — withdrawn

**`epos-registry` does not serve writes.** Skills are published to the upstream registry directly, with whatever OCI client already holds the user’s credentials. `epos` has no `push` command.

This reverses the earlier design, which had `epos-registry` serve both directions so users configured one registry reference rather than two. Upload sessions were to receive a **307** so the client re-issued against upstream and got upstream’s `Location` natively — sidestepping `Location` normalisation, session mapping and chunked-resume accounting, and keeping the §4.2 promise that blob bytes never cross `epos-registry` in either direction.

**Why it was withdrawn.** `oras-go` refuses a blob upload `Location` on a different host than the registry it was pointed at:

```
blob upload Location "upstream:5000" is on a different host
than the registry "epos-registry:8080"
```

That check is deliberate. It is the fix for [GHSA-jxpm-75mh-9fp7](https://github.com/oras-project/oras-go/security/advisories/GHSA-jxpm-75mh-9fp7) — *“to prevent credentials from being forwarded to an attacker-controlled endpoint”* — and it compares the `Location` against the **originally targeted** registry, so following the 307 transparently does not satisfy it. Every `oras-go` client is affected, which includes `oras` itself and included Epos’s own `epos push`. The redirect design was therefore unimplementable, not merely inconvenient.

**Why not the alternatives.** Each contradicts something this specification asserts elsewhere:

|Alternative                                                        |Contradicts                                                                                                |
|-------------------------------------------------------------------|-----------------------------------------------------------------------------------------------------------|
|Relay the upload session instead of redirecting it                 |§4.2’s “blob bytes never cross `epos-registry`” — every published byte would                                  |
|Rewrite `Location` to point back at `epos-registry` and map the session|the hazard this section was written to avoid: normalisation, session mapping, chunked-resume accounting  |
|Keep the redirect and accept that publishing is broken             |nothing, but ships a path no client can use                                                                |

Withdrawing the write path costs the least. §4.2’s transfer posture is the load-bearing promise — it is why `epos-registry` is cheap to run and can be N replicas behind a load balancer (§4.4) — and relaying uploads would give that up on every publish.

**Consequence.** Users configure two references, not one: `epos-registry` for reading, the upstream registry for publishing. Reading is where the value is, because that is where §5.1’s counting happens; publishing is a plain OCI push that any existing client already does well.

**If this is revisited**, the question to answer first is whether `oras-go` will ever accept a cross-host upload `Location` under some opt-in. If not, the only route to one configured host is relaying uploads, and §4.2 has to be amended to say so.

-----

## 5. Usage Metrics

### 5.1 What counts as a download

A **download** is a `GET /v2/<name>/blobs/<digest>` that `epos-registry` answers (307 or 200). Manifest `GET` and `HEAD` are **resolves and are never counted** — the lock-file update-check workflow performs a digest resolve and comparison with no content fetch, and would otherwise dominate the numbers.

The repository name identifies the skill. No manifest parsing is required.

### 5.2 Verified and unverified counts

|Condition                                         |`verified`|
|--------------------------------------------------|----------|
|Request carries `Epos-Download: <skill>@<version>`|`true`    |
|Otherwise                                         |`false`   |

The `epos` CLI sends `Epos-Download`. Stock `oras` does not: verified against `oras-go` v2.6.2, `blobStore.Fetch` (`registry/remote/repository.go:750`) issues blob requests with **no `Accept` header at all** — every `Accept`-setting site in that file is on the manifest path. Media-type negotiation is therefore not a usable discriminator for blob fetches.

Documentation must state that accurate counts require a conforming client. Unverified counts are inflated by signature and attestation blob fetches in the same repository, which cannot be distinguished without state.

### 5.3 Emission

One instrumentation path: the OpenTelemetry Go SDK. The exporter is chosen by configuration.

|Exporter    |Use                          |
|------------|-----------------------------|
|`stdout`    |godog runs, local development|
|`prometheus`|Production scrape            |
|`otlp`      |Production push              |

Instrument: `epos.downloads`, a monotonic counter.

Attributes: `repository`, `verified`, `client` (from `User-Agent`; `oras-go` sets `User-Agent: oras-go` on its auth `DefaultClient`).

**Cardinality control.** The attribute set is configurable. Version-valued attributes accumulate without bound under a Prometheus exporter and are off by default.

### 5.4 Publishes — withdrawn

There is no publish counter. `epos.publishes` was to be recorded from the relayed manifest `PUT`, and §4.5 withdraws that path: a publish goes straight to upstream, where `epos-registry` never sees it.

Publishes per repository would have answered “which skill changes most often” directly. Nothing replaces it, because the only place to observe a publish is now the upstream registry’s own logs. Blob uploads would not have been counted in any case — content addressing means a new version reusing an existing layer uploads nothing, so upload volume does not track publishing.


-----

## 6. `epos` CLI — Registry Operations

### 6.1 Commands

```
epos pack <dir> [-t <name>:<version>]     # directory → artifact in local store
epos pull <ref>                           # registry → local store
```

`pack` derives the config blob from `SKILL.md` frontmatter, builds the deterministic content layer (§2.4), and writes the artifact into the local store (§9).

There is no `epos push`, and no Epos write server. A skill is published to the registry with whatever OCI client already holds the user’s credentials — `oras`, `docker`, a CI job. §4.5 records why the write path was withdrawn.

### 6.2 Non-goals

The CLI does not validate skills server-side, mediate credentials, or transform artifacts in transit. A malformed skill that reaches a registry will fail at install time, not when it was published.

-----

## 7. Catalog and Discovery

### 7.1 Scope

Discovery is supported **only against registries that implement `GET /v2/_catalog`**. There is no other way to enumerate a registry: `_catalog` is outside the Content Discovery conformance category and several hosted registries disable it, and OCI offers no substitute.

Epos does not compensate for that gap in v2.0. It does not maintain a hand-authored catalog file, does not scan configured repository prefixes, and does not publish a collection index. Against a registry without `_catalog`, `epos search` and `epos list` report the capability as unavailable and exit non-zero. Direct references continue to work — `epos pull ghcr.io/acme/agent-skills/pdf:1.0.0` needs no catalog.

zot implements `_catalog`, so the A3 gate is exercisable against the real registry used throughout §13.

### 7.2 Mechanism

Discovery is entirely client-side. No new server endpoint, no content negotiation, no Epos-specific media type.

1. `GET /v2/_catalog` through `epos-registry` → repository names.
1. Filter to repositories under the configured skill namespace.
1. `GET /v2/<name>/tags/list` per repository → versions.
1. Resolve manifests and read frontmatter-derived annotations for name and description.

Steps 3 and 4 are lazy: `epos list` stops at step 2 unless `--versions` is given.

### 7.3 Commands

```
epos search <query>
epos list [--versions]
```

Search matches against repository name, skill name and description. It is a client-side filter over the enumeration above, not a server-side query.

### 7.4 Planned: native discovery

Universal discovery requires an index that Epos owns. `epos-registry` already terminates the Distribution API, so serving `_catalog` from its own index — rather than relaying upstream’s, or reporting the capability unavailable — is an added capability, not a new component.

That index also opens skill-aware search and the `vnd.agentskills.collection.v1` representation, both of which need something upstream cannot provide.

Out of scope for v2.0. Until then, discovery is a function of which upstream registry you chose.

-----

## 8. Skillfile — Build Language (Track B)

### 8.1 Model

Skillfile follows Docker’s model exactly: **the registry stores results; the recipe lives in git.** A build is a pure function from (bases, Skillfile, context) to one conformant single-layer artifact. Nothing about the derivation is traversable from the registry afterwards (§2.3).

There is no `RUN`. There is no `ENTRYPOINT` or `CMD` — nothing executes. Removing execution removes the sandbox problem entirely and keeps the build pure Go, cgo-free, daemon-free, and cross-platform.

### 8.2 Instructions

```
ARG     <name>[=<default>]
FROM    <ref> [AS <stage>]
COPY    [--from=<stage>] <src>... <dest>
RM      <path>...
APPEND  <path> (<<EOF … EOF | <file>)
REPLACE <path> <pattern> <replacement>
PATCH   <path> <diff-file>
AWK     <path> (<<EOF … EOF | <script-file>)
SET     [--file <path>] <key> <value>
UNSET   [--file <path>] <key>
```

These map one-to-one onto the operation set already settled in v1.x §9.2, which was deliberately **byte/line oriented and format-agnostic** — it does not parse YAML or Markdown structure, giving one uniform mechanism across `SKILL.md`, `values.yaml`, references, and assets.

|v1.x operation   |Skillfile instruction                       |
|-----------------|--------------------------------------------|
|`add-file`       |`COPY`                                      |
|`delete-file`    |`RM`                                        |
|`append-to-file` |`APPEND`                                    |
|`replace-in-file`|`REPLACE` (regex)                           |
|`patch-file`     |`PATCH` (unified diff with context)         |
|— (new)          |`AWK` (sandboxed line-oriented program)     |
|— (new)          |`SET` / `UNSET` (structure-aware YAML edits)|

`REPLACE` is fuzzy and surgical; `PATCH` is precise and fails loudly on drift. Both exist because the choice is per-edit.

**Ordering:** instructions apply in file order. When two instructions affect the same bytes, the later wins.

**Payloads:** inline via heredoc, or by path to a file in the build context. This is the `inline`/`path:` duality of v1.x, expressed in Dockerfile syntax.

### 8.2.1 `PATCH` semantics

`PATCH` is applied with **`github.com/bluekeyes/go-gitdiff`** (MIT, zero dependencies) as a direct dependency, in its default strict mode.

- The hunk is applied at the line recorded in its header. `apply_text.go` computes `fragStart := f.OldPosition - 1` and applies there; there is **no offset search and no fuzz factor**.
- Any mismatch returns a `*gitdiff.Conflict` and **fails the build**. This includes a pure line-number shift caused by an unrelated upstream insertion, even when every context line still matches — behaviour stricter than `git apply`.
- Failures are fatal. No `.rej` files, no warn-and-continue: the artifact is content-addressed, so partial application would silently produce a different digest from the same inputs.
- Payloads are authored with `git diff`. The library accepts `git diff`, `git show`, `format-patch`, GNU unified diffs, and Git binary patches.

`REPLACE` is the instruction for edits that must survive line drift.

### 8.2.2 `REPLACE` semantics

- The engine is the **Go standard library `regexp` package**, which is RE2. No external regex dependency; no backreferences, lookahead or lookbehind; linear-time guarantee, so no ReDoS is possible from an author-supplied pattern.
- The replacement uses Go’s `$1` / `${name}` expansion.
- **All** occurrences are replaced by default. `REPLACE --count=N` limits application to the first `N` matches. Because RE2 has no lookaround, `--count` is the only means of targeting one occurrence among several; note that it is positional, so an upstream insertion of an earlier match silently retargets the edit.
- **Zero matches is not an error.** A warning is emitted, the file is left unchanged, and the build continues. This makes idempotent and defensive edits expressible — rebuilding after upstream has adopted the same change does not break.
- No-op `REPLACE` instructions are counted and reported in the build summary, so a build containing them is visibly distinguishable from one that applied cleanly.

### 8.2.3 `AWK` semantics

`REPLACE` handles single-line substitution. `AWK` handles everything structural that a regex cannot express — multi-line edits, conditional edits, section-scoped edits — without reintroducing arbitrary command execution.

Applied with **`github.com/benhoyt/goawk`** (MIT, zero dependencies), embedded via `interp.New` / `Execute`. The file’s current content is the program’s stdin; its stdout replaces the file.

**Sandbox (mandatory, not configurable):**

```go
interp.Config{
    NoExec:       true,  // no system(), no pipe operator
    NoFileWrites: true,  // no '>' or '>>'
    NoFileReads:  true,  // no getline, no '<'
    Environ:      []string{},
}
```

With these set the program is a pure stdin→stdout function. It cannot spawn a process, touch the filesystem, or read the environment. This is what makes `AWK` compatible with §8.1’s no-`RUN` rule.

**Termination.** AWK is Turing-complete, so execution is bound to a `context` deadline. Default 10s per instruction, configurable via `--timeout`. Exceeding it fails the build.

**Determinism.** `systime()`, `srand()` and `rand()` are rejected by a post-parse AST check. They would make the output digest vary across builds with identical inputs, breaking §2.4.

**Failure.** Parse errors, runtime errors, and timeouts are fatal. There is no partial application.

### 8.2.4 `SET` / `UNSET` semantics

The byte-oriented instructions are unreliable on YAML — quoting styles, block scalars and list indentation all defeat regex surgery — and `SKILL.md` frontmatter holds `name`, `description` and `allowed-tools`, the fields that determine whether an agent loads the skill at all. `SET` and `UNSET` are structure-aware and cannot produce invalid YAML.

```
SET   description "Extracts tables from PDFs"
SET   metadata.author acme
UNSET allowed-tools
SET   --file values.yaml model opus
```

- Default target is the YAML frontmatter block of `SKILL.md`. `--file` targets any YAML file in the build context.
- Keys use dotted paths for nested mappings.
- Values are parsed as YAML scalars, so `SET version 1.2` yields a float. Quote to force a string.
- `SET` on an absent key adds it; `UNSET` on an absent key emits a warning and continues, consistent with §8.2.2.

**Library:** **`github.com/goccy/go-yaml`** (MIT, zero dependencies), via `parser.ParseBytes(src, parser.ParseComments)`, AST mutation, and `File.String()`. The same dependency serves as the frontmatter reader for the config blob (§2.1) and the `values.yaml` reader at install (§10.3).

**Measured fidelity.** Editing one key in a representative frontmatter block containing a head comment, inline comments, a folded block scalar, single- and double-quoted scalars and a list: goccy’s AST path preserved comments, key order, quoting styles, the block scalar’s line breaks and 2-space indentation, changing **2 lines** beyond the intended edit. `go.yaml.in/yaml/v3`’s Node API changed **6** — reindenting to 4 spaces and folding the block scalar onto one line.

**Known deviation.** Inline comment whitespace is normalised (`- Read      # note` becomes `- Read # note`) across the whole edited block. Files not targeted by any instruction are never re-serialised and remain byte-identical.

### 8.3 `FROM` sources

|Scheme|Example                                            |Pin                             |
|------|---------------------------------------------------|--------------------------------|
|Local |`FROM ./skills/base`                               |none (content hash at build)    |
|Git   |`FROM git+https://github.com/o/r#v1.2.0:skills/pdf`|commit SHA + tree SHA of subpath|
|OCI   |`FROM ghcr.io/o/agent-skills/pdf:1.2.0`            |manifest digest                 |

Only the OCI scheme touches a registry. A Skillfile using local or git bases is a complete, standalone workflow.

### 8.4 Multi-stage composition

Multi-stage follows Docker semantics. Multiple `FROM … AS <stage>` declarations, composition by explicit `COPY --from=<stage>`.

```dockerfile
FROM ghcr.io/acme/agent-skills/pdf:1.2.0 AS base
FROM git+https://github.com/acme/refs#main:shared AS shared

FROM base
COPY --from=shared reference.md references/shared.md
REPLACE SKILL.md /model: sonnet/ "model: opus"
APPEND SKILL.md <<EOF
See references/shared.md for the house style.
EOF
```

Stage names are also the **values-scope keys** at install time (§10.3).

Composition is explicit enumeration, not merge-by-default: what you take, you name.

### 8.5 Parsing

Skillfile is parsed with `github.com/moby/buildkit/frontend/dockerfile/parser` — pure Go, tiny dependency tree, no daemon, no cgo. This yields line continuations, heredocs, `--flag=value` parsing, comment attachment, and `# syntax=`-style directives for free.

**Implementation note (source-verified against BuildKit `main`):** `newNodeFromLine` falls back to `parseIgnore` for instructions absent from its dispatch table, and `parseIgnore` returns an **empty Node** — the instruction name, flags, raw line, and comments survive, but **arguments are dropped from the AST**. Epos must either re-tokenise arguments from `node.Original`, or vendor the small dispatch map with Epos instruction names bound to the existing argument parsers.

BuildKit’s `instructions` package is **not** used; it rejects unknown commands and encodes Docker semantics Epos does not want.

### 8.6 Template preservation

A `SKILL.md` containing `{{ .Values.model }}` MUST pass through the build **untouched** and render only at install (§10). Consequently:

- Build-time substitution uses `ARG`/`$VAR` syntax, never `{{ }}`.
- Any `APPEND` or `COPY` payload containing `{{` is preserved verbatim.

### 8.7 Command

```
epos build [-f Skillfile] [--build-arg k=v] [-t <name>:<version>] <context>
```

Output is written to the local store (§9).

-----

## 9. Local Store

### 9.1 Layout

`~/.epos/store` is an **OCI Image Layout** — `oci-layout`, `index.json`, `blobs/sha256/` — created and read via `oras.land/oras-go/v2/content/oci`. Tags (`<name>:<version>`) resolve to digests, exactly as `docker build -t` does.

This makes the multi-version workflow work: build once, keep many versions resident, install any of them into any number of worktrees, reinstall without rebuilding, and compare versions across worktrees in parallel.

### 9.2 Concurrency

The OCI Image Layout specification is **silent on concurrency, locking, and mutation of `index.json`**. Implementers must supply it.

`oras-go` v2 `content/oci.Store` is **single-process only** (verified against v2.6.2): it holds `sync.RWMutex`/`sync.Mutex` only, reads `index.json` once at construction and never re-reads it, and `SaveIndex` calls `os.WriteFile` in place. Two concurrent processes silently lose each other’s tags, and a crash mid-write can truncate the index.

Epos therefore supplies both missing pieces, following the Go toolchain’s approach:

- **Advisory file locking** via `github.com/rogpeppe/go-internal/lockedfile` — a maintained exported copy of Go’s own `cmd/go/internal/lockedfile`. Build-tagged `flock`/`fcntl` on Unix and `LockFileEx` on Windows via `golang.org/x/sys`. Pure Go, no cgo, all three platforms.
- **Lock discipline:** shared lock for fetch, resolve, and install-into-worktree, so parallel worktree installs do not serialise. Exclusive lock for push, tag, untag, and prune.
- **Atomic index writes:** `AutoSaveIndex` is set false; Epos writes `index.json` itself via temp file → `fsync` → `os.Rename`, which is atomic within a directory on all three platforms.
- The store must be opened **inside** the lock so the on-disk index is read fresh.

### 9.3 Garbage collection

**Manual only**, matching the Go module cache, pnpm, Cargo, and Bazel.

```
epos store prune    # mark-and-sweep from tagged manifests
epos store path
epos store ls
```

There is no automatic collection, no reference counting, no GC roots, no leases, and no worktree liveness tracking. Those mechanisms exist to make *automatic* collection safe; with explicit cleanup there is nothing to make safe.

**Caveat:** if store blobs are written read-only for integrity, `prune` and uninstall MUST `chmod` writable before removal — the same defect that makes `rm -rf` on `GOMODCACHE` fail.

### 9.4 Filesystem requirements

`~/.epos/store` must be on a local filesystem. Advisory locks are unreliable over NFS and SMB. If blobs are ever hard-linked into worktrees, cross-filesystem installs MUST fall back to copying.

-----

## 10. Install — Local

### 10.1 Model

Helm’s model. The artifact carries templates verbatim; rendering happens at install with the values the user supplies. The registry never renders anything.

### 10.2 Manifests

Per the Agent Skills OCI Artifacts spec:

- `skills.json` — human-authored declaration of desired skills.
- `skills.lock.json` — digest-pinned resolution.
- `additionalBasePaths` — multi-vendor install targets.

Default install path is `.claude/skills`; `additionalBasePaths` covers other agent vendors.

`skills.lock.json` also serves as the **per-worktree version pin**, in the manner of `rust-toolchain.toml`: the store is a cache, the lock is the truth. Different worktrees pin different digests from shared storage. This is a pin *file*, not a symlink — no Windows symlink fragility.

### 10.3 Values and rendering

Values are supplied by `values.yaml` and `--set`. Rendering uses Go `text/template` with **no custom functions**.

Scoping follows Helm: values nest under the Skillfile **stage name**, with a shared `global` block visible to all stages. Two stages may both use `.Values.title` without collision; `global` is the deliberate cross-stage channel.

### 10.4 Commands

```
epos install <ref|name:version> [-f values.yaml] [--set k=v]
epos uninstall <name>
epos ls
```

-----

## 11. Signing and Verification

Cosign signatures and attestations are stored as **referrers** with `subject` pointing at the skill manifest. `epos-registry` already exposes `GET /v2/<name>/referrers/<digest>` (§4.1).

```
epos verify <ref>
```

Signature blob fetches occur in the same repository as the skill and are therefore counted as unverified downloads (§5.2). This is a known and documented inflation.

-----

## 12. Milestones

Every milestone delivers a usable solution with full integration coverage against a **real OCI registry in testcontainers** and godog Gherkin features. No milestone is phased or partial. The complete CI stack (§13.6) lands with A1, not incrementally.

### Track A — registry

|ID    |Deliverable                                                                                                                                    |Gate                                                                                                                                                                                                                                   |
|------|-----------------------------------------------------------------------------------------------------------------------------------------------|---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
|**A1**|`epos-registry`: `/v2/` Pull + Content Discovery, 307 pass-through, stateless counting, OTel stdout exporter. **Full CI stack (§13.6)**        |OCI Pull conformance suite green, godog scenarios driving real `oras` against real zot through `epos-registry`, and every CI workflow green — lint, vet, format, vulncheck, three-platform unit tests, integration suite, release build|
|**A2**|`epos pack` / `pull`; deterministic packing; local store with locking; verified counting lights up. The write path was attempted and withdrawn (§4.5)|A published artifact pulled back by plain `oras`; identical inputs produce identical digests across platforms                                                                     |
|**A3**|Discovery: `_catalog` passthrough, `epos search` / `list`                                                                                      |Skills enumerated and searched against real zot; a registry without `_catalog` reports the capability as unavailable rather than failing obscurely                                                                                     |
|**A4**|Install local: `values.yaml`, `text/template` rendering, `skills.json` + `skills.lock.json`, `additionalBasePaths`                             |Parameterised skill installs into `.claude/skills`; two worktrees pin different digests from one store simultaneously                                                                                                                  |
|**A5**|Sign and verify: cosign referrers, `epos verify`                                                                                               |Tampered artifact fails verification against a real registry                                                                                                                                                                           |

### Track B — build

|ID    |Deliverable                                                                                                                |Gate                                                                                      |
|------|---------------------------------------------------------------------------------------------------------------------------|------------------------------------------------------------------------------------------|
|**B1**|Skillfile: `FROM` local and git, multi-stage, `COPY`/`RM`/`APPEND`/`REPLACE`/`PATCH`, build to local store, install locally|A skill derived from a git base builds and installs with **no registry involved anywhere**|
|**B2**|`FROM` OCI; the built result is published with an ordinary OCI client (§4.5)                                                |Derived skill published and pulled back by plain `oras`; provenance annotations present   |

**Sequencing rationale.** Install precedes Skillfile because packing and installing are basic functionality — a user can clone a repo, pack it and install it without ever writing a Skillfile. Skillfile is for advanced users and is a parallel track, not a prerequisite.

-----

## 13. Testing

### 13.1 Discipline

BDD throughout. Minimal buildable shim first, tests red, implement scenario by scenario, **each scenario a separate commit**. No phased development.

### 13.2 Stack

|Concern       |Tool                                                                                    |
|--------------|----------------------------------------------------------------------------------------|
|Gherkin runner|`godog` (go test-integrated, JUnit output)                                              |
|Containers    |`testcontainers-go`                                                                     |
|Registry      |**zot** — OCI-conformance-validated, supports referrers, `htpasswd` auth                |
|Git server    |Gitea (Go-native, real HTTP transport, exercises commit and tree SHA pinning)           |
|Conformance   |OCI distribution-spec conformance suite (`OCI_TEST_PULL=1`), JUnit output, GitHub Action|

Registries are **real**. No fakes, no in-memory registry substitutes, no mocked HTTP.

### 13.3 Feature files

`features/*.feature` are **hand-authored, canonical, and single-source**. godog consumes them directly; they are never duplicated, paraphrased, or re-transcribed into Go test tables or documentation.

|File                          |Milestone|
|------------------------------|---------|
|`registry-read-path.feature`  |A1       |
|`author-and-publish.feature`  |A2       |
|`discover-and-search.feature` |A3       |
|`install-locally.feature`     |A4       |
|`sign-and-verify.feature`     |A5       |
|`build-with-skillfile.feature`|B1       |
|`build-from-registry.feature` |B2       |

### 13.4 Project structure

Plain Go. **No code generation, no model, no hexagonal layering, no DDD.** One repository, one module, one `go.mod`, with a `main` package per binary under `cmd/` — the layout Kubernetes uses and the common Go convention.

```
epos/
  cmd/
    epos/                # CLI entrypoint
    epos-registry/       # registry entrypoint
  internal/
    artifact/            # frontmatter → config blob, deterministic tar
    store/               # local OCI layout, locking, prune
    skillfile/           # parse and apply
    install/             # values, rendering, skills.lock.json
    upstream/            # OCI client, redirect and relay handling
    metrics/             # OTel instruments and exporters
  tests/
    integration/         # godog runners, //go:build integration (§13.5)
  features/              # godog .feature files, canonical (§13.3)
  .golangci.yml
  .goreleaser.yaml
  go.mod
```

Both binaries share one dependency set, but the linker includes only what each `main` transitively imports — `epos-registry` does not carry goawk, go-gitdiff or goccy.

Compile-time separation still holds where it matters: anything private to one binary lives under `cmd/<binary>/internal/`, which the Go toolchain makes unimportable from the other. Shared code goes in the top-level `internal/` deliberately, not by default.

Interfaces are introduced only where a test or a second implementation actually requires one — the upstream registry and the `FROM` resolvers are the likely cases. They are not introduced pre-emptively.

-----

### 13.5 Test layout and build tags

Unit tests live beside the code they test, untagged. **Integration tests live under `tests/integration` and every file in it begins with a build tag:**

```go
//go:build integration
```

|Command                                            |Runs                                |Requires                           |
|---------------------------------------------------|------------------------------------|-----------------------------------|
|`go test ./...`                                    |Unit tests only                     |Nothing — no containers, no network|
|`go test -tags=integration ./tests/integration/...`|godog suites against real containers|Docker                             |

The separation is not cosmetic. Unit tests must stay runnable on a laptop with no daemon and in a sandbox with no egress; container-backed suites take minutes and cannot be a precondition for `go test`.

The godog runners live in `tests/integration` and read `features/` at the repository root. Feature files are never copied into the test tree — they remain canonical and single-source (§13.3).

### 13.6 Continuous integration

**Full CI exists before A1 ships**, not after. A1’s gate is not met until every workflow below is green, including the integration suite against real zot.

All actions are pinned by commit SHA with the version in a trailing comment. Tag pins are mutable; SHA pins are not, and a spec that ships cosign verification (§11) should not undermine it in its own supply chain. Go toolchain: **1.26.5**.

|Purpose        |Action                         |Version                       |
|---------------|-------------------------------|------------------------------|
|Checkout       |`actions/checkout`             |v7.0.1                        |
|Go toolchain   |`actions/setup-go`             |v7.0.0                        |
|Lint           |`golangci/golangci-lint-action`|v9.3.0 (golangci-lint v2.12.2)|
|Vulnerabilities|`golang/govulncheck-action`    |v1.1.0                        |
|Release        |`goreleaser/goreleaser-action` |v7.2.3                        |
|Docs publish   |`peaceiris/actions-gh-pages`   |v4.1.0                        |
|PR preview     |`rossjrw/pr-preview-action`    |v1.8.1                        |

#### `.github/workflows/ci.yml`

```yaml
name: ci
on:
  push:
    branches: [main]
  pull_request:
env:
  GO_VERSION: "1.26.5"
jobs:
  check:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1 # v7.0.1
      - uses: actions/setup-go@b7ad1dad31e06c5925ef5d2fc7ad053ef454303e # v7.0.0
        with:
          go-version: ${{ env.GO_VERSION }}
      - name: format
        run: |
          unformatted=$(gofmt -l .)
          if [ -n "$unformatted" ]; then echo "$unformatted"; exit 1; fi
      - name: vet
        run: go vet ./...
      - uses: golangci/golangci-lint-action@d583c34f0599d37dbac4a198b9c83201be380893 # v9.3.0
        with:
          version: v2.12.2

  unit:
    runs-on: ubuntu-latest
    strategy:
      matrix:
        os: [ubuntu-latest, macos-latest, windows-latest]
    steps:
      - uses: actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1 # v7.0.1
      - uses: actions/setup-go@b7ad1dad31e06c5925ef5d2fc7ad053ef454303e # v7.0.0
        with:
          go-version: ${{ env.GO_VERSION }}
      - run: go test -race ./...

  integration:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1 # v7.0.1
      - uses: actions/setup-go@b7ad1dad31e06c5925ef5d2fc7ad053ef454303e # v7.0.0
        with:
          go-version: ${{ env.GO_VERSION }}
      - name: godog integration suite
        run: go test -tags=integration -timeout=30m ./tests/integration/...
      - if: always()
        uses: actions/upload-artifact@v4
        with:
          name: junit
          path: "**/junit.xml"

  vulncheck:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1 # v7.0.1
      - uses: golang/govulncheck-action@032d45514ae346b1db93c04b0c90b841c370344f # v1.1.0
        with:
          go-version-input: ${{ env.GO_VERSION }}
```

The unit job runs on all three platforms because §2.4 requires identical digests everywhere and §2.5 defines path behaviour that only differs on Windows. The integration job is Linux-only — testcontainers needs a Docker daemon.

#### `.github/workflows/release.yml`

```yaml
name: release
on:
  push:
    tags: ["v*"]
permissions:
  contents: write
jobs:
  goreleaser:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1 # v7.0.1
        with:
          fetch-depth: 0
      - uses: actions/setup-go@b7ad1dad31e06c5925ef5d2fc7ad053ef454303e # v7.0.0
        with:
          go-version: ${{ env.GO_VERSION }}
      - uses: goreleaser/goreleaser-action@f06c13b6b1a9625abc9e6e439d9c05a8f2190e94 # v7.2.3
        with:
          args: release --clean
        env:
          GITHUB_TOKEN: ${{ secrets.GITHUB_TOKEN }}
```

`.goreleaser.yaml` declares two builds, `epos` and `epos-registry`, each from its `cmd/` directory, with `CGO_ENABLED=0` and the linux/darwin/windows × amd64/arm64 matrix the no-cgo constraint (§1.2) makes available.

-----

## 14. Documentation and Deployment

### 14.1 Site

Astro, using **`gaarutyunov/ui-kit`** for all components.

**This specification is not published to the site.** It is a repository document for design and task decomposition, not user-facing material. The site is written for someone who has never heard of Epos.

|Surface                |Content                                                                                                                                                                                            |
|-----------------------|---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
|**Landing**            |What Epos is in one sentence, the problem it solves, install command, and a single copy-pasteable example that produces a visible result                                                           |
|**Quick start**        |End-to-end tutorial: publish a skill, pull it back, install it locally with values. Runnable start to finish against a public registry, no prior OCI knowledge assumed                             |
|**CLI reference**      |Every command and flag — `pack`, `pull`, `build`, `install`, `uninstall`, `ls`, `search`, `list`, `verify`, `store`                                                                        |
|**Skillfile reference**|Every instruction with syntax and worked examples — `FROM`, `COPY`, `RM`, `APPEND`, `REPLACE`, `PATCH`, `AWK`, `SET`, `UNSET`, `ARG` — plus multi-stage composition and the values/templating model|

Reference pages are generated from the same source as the CLI’s own help output and the Skillfile instruction table, so they cannot drift from the implementation.

### 14.2 GitHub Pages — branch mode

Publishing uses **branch mode** against the `gh-pages` branch via `peaceiris/actions-gh-pages`.

```yaml
name: docs
on:
  push:
    branches: [main]
permissions:
  contents: write
jobs:
  build-deploy:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1 # v7.0.1
      - uses: actions/setup-node@v4
        with:
          node-version: 22
      - run: npm ci && npm run build
        working-directory: docs
      - uses: peaceiris/actions-gh-pages@1ef5a1b1df4c63fe21a2242edbee6cac921ece01 # v4.1.0
        with:
          github_token: ${{ secrets.GITHUB_TOKEN }}
          publish_dir: docs/dist
          publish_branch: gh-pages
```

### 14.3 PR previews

Every pull request gets a preview deployment via **`rossjrw/pr-preview-action`** with `action: auto`, so previews are created, updated, and removed automatically with the PR lifecycle.

```yaml
name: pr-preview
on:
  pull_request:
    types: [opened, reopened, synchronize, closed]
concurrency: preview-${{ github.ref }}
permissions:
  contents: write
  pull-requests: write
jobs:
  preview:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1 # v7.0.1
      - uses: actions/setup-node@v4
        with:
          node-version: 22
      - if: github.event.action != 'closed'
        run: npm ci && npm run build
        working-directory: docs
      - uses: rossjrw/pr-preview-action@ffa7509e91a3ec8dfc2e5536c4d5c1acdf7a6de9 # v1.8.1
        with:
          source-dir: docs/dist
          preview-branch: gh-pages
          umbrella-dir: pr-preview
          action: auto
```

-----

## 15. Decision Ledger

|# |Decision             |Resolution                                                                                                              |
|--|---------------------|------------------------------------------------------------------------------------------------------------------------|
|1 |Wire format          |Conform to `vnd.agentskills.skill.v1`; extend with `vnd.epos.*` for Epos-native concepts                                |
|2 |Registry protocol    |`/v2/` only; no second API surface. Epos semantics would ride on `Accept` negotiation if ever needed — none are, in v2.0|
|3 |Rendering location   |Helm model — templates rendered at install, never by the registry or a server                                           |
|4 |Write path           |No write server, and no `epos push`. Skills are published with an ordinary OCI client (§4.5)                            |
|5 |Blob transfer        |Redirect pass-through. Blobs never cross `epos-registry`                                                                |
|6 |Download counting    |Stateless. Content blob GET counts; `Epos-Download` header marks verified; manifests never count                        |
|7 |Metrics sink         |OpenTelemetry SDK; exporter configurable (stdout / Prometheus / OTLP)                                                   |
|8 |Process model        |Separate binaries per component, one repo and one module; no `-target`, no module registry                              |
|9 |A1 scope             |`epos-registry` alone; `oras` is the client                                                                             |
|10|Verified marker      |Explicit `Epos-Download` header (`Accept` is unusable — `oras-go` sends none on blob GETs)                              |
|11|Derivation model     |Docker’s: recipe in git, registry stores results, provenance in annotations                                             |
|12|`RUN`                |None. Declarative instructions only                                                                                     |
|13|Composition syntax   |Multi-stage `FROM … AS` with `COPY --from`, Docker semantics                                                            |
|14|Sequencing           |Install before Skillfile; Skillfile is a parallel track                                                                 |
|15|Build output         |OCI layout in the local store — digest identity, tags, many versions resident                                           |
|16|Store location       |Global `~/.epos/store`, Go-native locking, manual cleanup                                                               |
|17|Spec organisation    |One file                                                                                                                |
|18|`PATCH` matching     |`bluekeyes/go-gitdiff`, strict — exact line, no offset, no fuzz; failure is fatal                                       |
|19|`REPLACE` no-match   |Warning, build continues; all occurrences by default, `--count=N` to limit                                              |
|20|Regex engine         |Go stdlib `regexp` (RE2); no external regex dependency                                                                  |
|21|Structural text edits|`AWK` via `benhoyt/goawk`, sandboxed with `NoExec`/`NoFileWrites`/`NoFileReads` and a timeout                           |
|22|Frontmatter edits    |`SET` / `UNSET` via `goccy/go-yaml` AST; measured 2-line drift versus 6 for `yaml/v3`                                   |
|23|Discovery            |Only where upstream implements `_catalog`; native discovery deferred to a later `epos-registry` capability              |
|24|Write path routing   |**Withdrawn.** `epos-registry` was to serve writes for one configured host; `oras-go` rejects the cross-host upload `Location` the 307 produces (GHSA-jxpm-75mh-9fp7), so no client could publish through it (§4.5) |

### Removed from scope

|Was                                         |Reason                                                                                                                                                    |
|--------------------------------------------|----------------------------------------------------------------------------------------------------------------------------------------------------------|
|Upload/write **server**                     |Helm has none; the CLI packs and holds the user’s credentials. `epos-registry` relays writes (§4.5) but transforms nothing — one host, not a write service|
|Overlay as an OCI artifact                  |Superseded by Skillfile; recipe lives in git                                                                                                              |
|Private registries and credential resolution|Over-complication for v2.0                                                                                                                                |
|Caching / streaming mode                    |Over-complication for v2.0                                                                                                                                |
|`-target` distributed mode                  |No second resource profile exists to separate                                                                                                             |
|Kubernetes / cluster install                |Separate spec (`EPOS-K8S.md`); runtime support for mounting non-image OCI artifacts is unsettled                                                          |
|Hand-authored or scanned catalogs           |Drift and cost without solving the general case; superseded by planned `epos-registry`                                                                    |
|`vnd.epos.*` media types                    |No Epos-native wire concept survived the design                                                                                                           |

-----

## 16. Deferred Work

This specification has no unresolved questions. Two areas are deliberately excluded and will be specified separately:

|Spec               |Scope                                                                             |Blocking issue                                                                                                                                                                                                                                                                             |
|-------------------|----------------------------------------------------------------------------------|-------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
|`EPOS-K8S.md`      |Cluster install — how a skill reaches a pod                                       |Runtime support for mounting non-image OCI artifacts. OCI VolumeSource reached GA in Kubernetes v1.36, but containerd mounts runnable images only, not the custom-`artifactType` shape a conformant skill artifact uses (containerd #11381, open). CRI-O supports it; k3s ships containerd.|
|`EPOS-DISCOVERY.md`|Native `_catalog` and skill-aware search served from an index `epos-registry` owns|Nothing blocking; deferred as scope. Would make discovery independent of upstream `_catalog` support (§7.4).                                                                                                                                                                               |