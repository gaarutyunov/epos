# Epos — Specification

> **Epos** (named after the [library ship](https://en.wikipedia.org/wiki/Epos_(library_ship)) that ferried books between coastal towns) is a Go-based packaging, distribution, and registry-proxy system for AI-agent **Skills**. It reimagines a Skill — today a bare `SKILL.md` directory — as a Helm-chart-like, versioned, templatable package distributed as an OCI artifact, with a Kustomize-style declarative overlay system, a credential-passthrough registry proxy, download statistics, and a federated, Kubernetes-deployable web frontend.

This document is the single authoritative specification. It is deliberately comprehensive; each concern has its own top-level section.

---

## 1. Overview and Goals

### 1.1 What Epos is

Epos gives Skills the packaging maturity that Helm gave Kubernetes manifests, **without** Helm's cluster-deployment machinery. A Skill becomes a package with metadata, versioning, values-driven templating, dependency-free distribution over any OCI registry, declarative overlays for composition, and reproducible, lockfile-pinned installation into a project.

Epos is, precisely stated, **a package manager that shares Helm's authoring-and-distribution surface** — not a release-lifecycle/deployment tool. It borrows Helm's *revision-record* model for history and rollback, but not Helm's cluster-reconciliation (three-way-merge) machinery, because a materialized Skill is static files with no running state to converge.

### 1.2 Design principles

- **OCI-native.** Skills are OCI artifacts with Epos's own media types. Any compliant OCI registry (GitLab Container Registry, Harbor, Nexus, `registry:2`, ECR, GHCR, ACR, …) can store them. Epos never pretends to be Helm on the wire.
- **Helm-familiar.** The CLI mirrors Helm's verbs 1:1 so Helm users are immediately productive; lifecycle verbs are reinterpreted for Skills and documented explicitly (§4).
- **Reproducible and tamper-evident.** Everything that can be pinned is pinned by digest. Tags are informational; digests are authoritative. A tag/digest mismatch is a hard error.
- **Stateless by default; pluggable when state is required.** The proxy stores no secrets. Where durable state is genuinely needed (registration index, revision history in-cluster), it lives behind a pluggable interface with a zero-dependency default.
- **Declarative composition.** Overlays modify a base Skill through inspectable, version-controllable patch operations rather than scripts.

### 1.3 Non-goals

- Epos does **not** deploy anything to a running Kubernetes cluster as a workload the way Helm installs releases. "Install" means *materialize files into a project*.
- Epos does **not** store or broker registry credentials of its own (§6).
- Epos does **not** define a release-reconciliation loop; `upgrade`/`rollback` re-materialize snapshots (§4, §5).

---

## 2. Package Format

### 2.1 On-disk layout of a Skill package

A Skill package is a directory:

```
my-skill/
  Epos.yaml            # package metadata (Chart.yaml analogue)
  values.yaml          # default values for templating
  SKILL.md             # the skill body — a Go text/template
  references/          # optional supporting reference files
  scripts/             # optional scripts
  assets/              # optional assets
  templates/           # optional: named-template helpers (_helpers.tpl etc.)
```

- **`Epos.yaml`** is the metadata manifest (see §2.2).
- **`SKILL.md`** is templated with the Helm templating model (§3). After rendering it must be valid Skill Markdown with YAML frontmatter (`name`, `description` required, per the Agent Skills standard).
- **`references/`, `scripts/`, `assets/`** hold supporting files the rendered `SKILL.md` may point at.
- **`templates/`** holds named-template helpers; files beginning with `_` (e.g. `_helpers.tpl`) never render to output, exactly as in Helm.

### 2.2 `Epos.yaml` metadata schema

Mirrors `Chart.yaml` (apiVersion v2 semantics) with Skill-appropriate fields:

```yaml
apiVersion: epos/v1
name: my-skill                 # must match directory name; skill-name rules apply
version: 1.4.2                 # SemVer 2.0.0 — the package version
description: One-line summary of what the skill does
keywords: [pdf, extraction]
maintainers:
  - name: Jane Doe
    email: jane@example.com
home: https://example.com/my-skill
sources: [https://github.com/example/my-skill]
annotations: {}                # free-form; used for frontend metadata & discovery hints
dependencies: []               # reserved for future sub-skill composition; see §2.5
```

**Validation constraints (strict Agent-Skills alignment).** Enforced at `epos create`, `epos lint`, and `epos package` time so that a package passing Epos validation always renders to a standard-valid Skill and always produces an OCI-safe repository path:

- **`name`** — lowercase letters, numbers, and hyphens only (`^[a-z0-9]+(-[a-z0-9]+)*$`); **max 64 characters**; **must equal the package directory name**; reserved words **`anthropic`** and **`claude`** are disallowed (case-insensitive, as substrings). This is also the OCI repository-path component, so the constraint doubles as OCI path safety.
- **`version`** — **required**; strict **SemVer 2.0.0**. (See §2.2 tag-rewrite note for the `+build` → `_` handling on push.)
- **`description`** — **required**, non-empty, **max 1024 characters**, must **not** contain XML/angle-bracket tags.

Validation failures are hard errors (non-zero exit) at `create`/`lint`/`package`. There is no relaxed mode in v1 — the strict floor is always enforced.

> **SemVer/OCI-tag caveat.** OCI tags cannot contain `+`. When a version carries SemVer build metadata (`1.4.2+build.5`), Epos rewrites `+`→`_` on push and reverses it on pull, matching Helm's behavior. Signing tools (cosign) are unaware of this rewrite; Epos documents the canonical tag it produced.

### 2.3 OCI artifact structure (on the wire)

A published Skill is an OCI artifact:

- **Config blob** — media type `application/vnd.epos.skill.config.v1+json`. Contains the parsed `Epos.yaml` metadata (so a registry/frontend can read metadata without pulling the whole package).
- **Content layer** — media type `application/vnd.epos.skill.content.v1.tar+gzip`. A **single tar+gzip** of the entire Skill directory (Helm-style single-layer packaging).
- **Optional provenance/signature** — cosign signatures are attached via the OCI 1.1 `subject`/referrers mechanism (§7), not as an inline layer.

Rationale for single-layer: simplicity of push/pull/digest/sign, and one digest to record in the lockfile. Selective inclusion/exclusion of references happens at **compile/render time on the unpacked directory** (§3.4), not by dropping OCI layers.

Push/pull is performed with [ORAS](https://oras.land) (`oras.land/oras-go/v2`). Media types are Epos-specific, so Helm and other OCI tooling correctly ignore Skill artifacts as an unknown type.

### 2.4 Media type registry (summary)

| Purpose | Media type |
|---|---|
| Skill config | `application/vnd.epos.skill.config.v1+json` |
| Skill content | `application/vnd.epos.skill.content.v1.tar+gzip` |
| Overlay config | `application/vnd.epos.overlay.config.v1+json` |
| Overlay content | `application/vnd.epos.overlay.content.v1.tar+gzip` |

### 2.5 Dependencies

`dependencies` in `Epos.yaml` declares other skills this skill composes with — a base skill plus modifications (typically your own overlays), merged into **one composite skill** at install/render time. It is a **unified, source-typed** mechanism: each entry names an **OCI** ref or a **git** source; composition semantics are identical regardless of source, and only pin-capture differs (OCI manifest digest vs. git commit + git tree SHA). Full detail — declaration schema, pin capture, the layer-stack composition model, precedence, granularity, and values scoping — is in **§9**. Pins are recorded in the lockfile/`Epos.lock` and travel with the skill.

---

## 3. Templating and References

### 3.1 Engine

`SKILL.md` (and any file under `templates/`) is rendered with **Go `text/template`** plus the **Sprig** function library, matching Helm's engine. As in Helm, the `env`/`expandenv` Sprig functions are omitted for safety, and Epos adds the Helm-style helpers `include` (returns a string, pipeline-friendly) and `required`.

### 3.2 Named-template helpers

Named templates (`define`/`template`/`include`) live by convention in `templates/_helpers.tpl`. Files beginning with `_` never render to output. **Template names are global** within a package; authors should prefix helper names with the skill name to avoid collisions (same hazard and mitigation as Helm).

### 3.3 Values and precedence

Values merge with Helm's precedence (lowest → highest):

1. Package `values.yaml`
2. Each `-f/--values` file, in the order given
3. `--set` / `--set-string` / `--set-file`

Maps deep-merge; **lists replace wholesale**; setting a key to `null` deletes it. (Behavior chosen to match Helm; the Helm 3.13 subchart-precedence change is noted for future dependency work only.)

### 3.4 References and the `includeReference` helper

References are handled with **plain Helm-style templating** — there is no separate declarative reference manifest. Authors gate reference mentions inline:

```markdown
{{- if .Values.features.advanced }}
See also: {{ includeReference "references/advanced.md" }}
{{- end }}
```

- `includeReference "path"` emits the correctly formatted link/mention text for a supporting file at `path` within the package.
- Which reference files end up "used" is a **render-time** outcome of the values supplied. Omitting a reference is simply not emitting its mention (and optionally not materializing the file — see below).

**Materialization.** On `install`/`template`, Epos materializes `SKILL.md` plus the supporting files the rendered output actually references. Files never referenced by the rendered `SKILL.md` may be omitted from the materialized output (this is the compile-time "ignore some references" behavior). The full set always remains in the published artifact.

### 3.5 Dangling-reference lint

`epos lint` performs **dangling-reference validation**: every path that the template can emit via `includeReference` (and every static reference link in the body) must resolve to a file that exists in the package. A reference to a non-existent file fails the lint. (Because references can sit inside template control flow, the lint evaluates reachable `includeReference` targets; unreachable-but-present files are allowed, unreferenced-and-absent targets fail.)

---

## 4. Command-Line Interface

Epos mirrors **Helm's verbs 1:1**. Authoring/distribution verbs map directly; lifecycle verbs are **reinterpreted for Skills** and documented below so their divergence from Helm's cluster semantics is explicit, not surprising.

### 4.1 Authoring & distribution (direct Helm parity)

| Command | Behavior |
|---|---|
| `epos create NAME` | Scaffold a new Skill package directory. |
| `epos package PATH` | Build the OCI artifact tarball + config from a package directory. |
| `epos push REF` | Push the artifact to an OCI registry (via ORAS), through the Epos proxy if configured (§6). |
| `epos pull REF` | Pull an artifact by tag or digest. |
| `epos template PATH` | Render `SKILL.md` + select referenced files with supplied values; write to stdout or a directory. |
| `epos lint PATH` | Validate metadata, template, and dangling references (§3.5). |
| `epos show REF` | Show metadata/values/rendered SKILL.md for a package. |
| `epos search TERM` | Search discoverable Skills across configured registries (§8). |
| `epos registry login/logout HOST` | Authenticate the **client** to a registry for push/pull; reuses the Docker credential config (like Helm/ORAS). Distinct from Epos's read-only listing secret, which is env-referenced (§6.2). |
| `epos dependency ...` | Resolve, capture, and compose skill dependencies (OCI + git) into one merged skill; writes pins to the lock. See §9. |

### 4.2 Lifecycle verbs (reinterpreted for Skills)

A Skill is not a running system, so these operate on the **project directory + digest-pinned lockfile bundle history** (§5) by default, not a cluster. Each is a snapshot re-materialization, not a reconciliation. Install/rollback also support a **cluster ConfigMap target** (`--target=configmap`); see §4.4 and §14.

| Command | Skill semantics |
|---|---|
| `epos install [NAME] REF` | Resolve REF to a digest and materialize the Skill bundle. **`--target=files`** (default): materialize into the project directory and record a new revision (version + digest + resolved values + pinned overlays) in the lockfile. **`--target=configmap`**: render and install as mountable ConfigMap(s) into a cluster namespace under the Helm-style install handle `NAME`, recording an in-cluster revision (§14). |
| `epos upgrade REF` | Fetch a newer version, re-materialize, and append a new revision. **No** three-way merge against live state (there is none). Honors `--target`. |
| `epos rollback [NAME] [REV]` | Restore a **previous bundle in full** — exact version+digest, the values used at that install, and the overlays applied. `--target=files`: from lockfile revision history. `--target=configmap NAME`: from the **in-cluster** revision records for handle `NAME` (§14). Recorded as a new revision (Helm-style). |
| `epos uninstall NAME` | `--target=files`: remove the Skill's materialized files and its lockfile entry (unless `--keep-history`). `--target=configmap`: delete the ConfigMap(s) and in-cluster revision records for handle `NAME`. |
| `epos status NAME` | Report the currently installed version/digest and applied overlays — from the lockfile (files) or the in-cluster records (configmap). |
| `epos history NAME` | List retained revisions — from the lockfile (§5.3) or the in-cluster records (§14). |

> **Explicit divergence note (shipped in `--help` text).** With `--target=files`, `upgrade`/`rollback`/`status`/`history`/`uninstall` concern *materialized files and lockfile revisions*, not Kubernetes releases — no live-object reconciliation, no cluster health. With `--target=configmap`, Epos writes real Kubernetes objects and self-contained in-cluster revision records (§14), Helm-release-style; this is the one path where Epos holds cluster credentials/RBAC.

### 4.3 Epos-native additions (no Helm analogue)

| Command | Behavior |
|---|---|
| `epos overlay ...` | Create, apply, package, and push declarative overlays (§9). |
| `epos proxy` / `epos serve` | Run the pass-through registry proxy and/or the federated frontend (§6, §10). |
| `epos lock` | Resolve and write the lockfile without materializing (parity with the "ci"/"frozen" install idiom). |
| `epos install --frozen` | Install strictly from the lockfile; error if lockfile and requested set disagree. |

### 4.4 Install targets (`--target`)

Materialization has two targets, separated Helm-style by verb and flag (full detail in §14):

- **`epos template --target=configmap`** — **emits** ConfigMap YAML + a ready-to-use `volumes`/`volumeMounts` snippet to stdout/file. No cluster access, no credentials — GitOps-friendly, the analogue of `helm template`.
- **`epos install --target=files`** (default) — local file materialization into the project; lockfile-recorded (§5). Unchanged default behavior.
- **`epos install --target=configmap`** — writes mountable ConfigMap(s) to a cluster namespace via the Kubernetes API under a Helm-style install handle, with self-contained in-cluster revision records (§14). The analogue of `helm install`; the one path requiring cluster RBAC.

---

## 5. Lockfile (`skills-lock.json`)

### 5.1 Purpose

`skills-lock.json` makes installs reproducible and gives `rollback`/`history` their data. It is the source of truth for "what is installed, at what exact bytes, with what values and overlays" — the role Helm delegates to in-cluster release records.

### 5.2 Pinning model

Each installed Skill is pinned by **both** version tag and manifest **digest**, with the **digest authoritative**:

- Resolution uses the digest.
- The tag is informational/human-readable.
- A tag/digest mismatch on reinstall/resolve is a **hard error** (never a silent swap).

### 5.3 Bundle revision history (embedded, bounded)

The lockfile embeds a **bounded revision history** of full **bundles**, so `rollback` restores the entire previously installed state — not just an older version. Behavior is **identical** locally and in-cluster; only the physical storage location differs (§5.4).

Each revision records the complete bundle:

- resolved skill `name`, `version` (tag), and `digest`;
- the **exact values** applied at that install (snapshotted in full — duplication across revisions is intentional, since self-contained rollback is the goal);
- the **overlays applied**, each **pinned by digest** (§9.7);
- source registry reference and a timestamp.

Retention is the last **N** revisions per Skill (configurable; default defined in config). Rollback to a retained revision re-materializes that bundle and appends it as a new revision (Helm-style new-revision-on-rollback).

### 5.4 Physical storage of the lockfile / revision records

The *format and semantics* above are constant; *where the bytes live* is pluggable and environment-driven:

- **Local (git):** `skills-lock.json` is a committed file in the project. Git versions it like any other file. Rollback/history read the embedded revisions directly from the file — **no dependency on git history** for correctness (the revisions are in the file itself); git simply tracks changes to it.
- **Kubernetes:** revision records persist in-cluster via the pluggable storage backend (§11) — a `ConfigMap` (or `Secret` when values/overlay data warrant it), mirroring how Helm stores releases as Kubernetes objects, or **PostgreSQL** at scale. Same embedded-bundle semantics.

Both are selected by config, consistent with the registration-index backend (§8, §11).

### 5.5 Example (illustrative)

```json
{
  "lockfileVersion": 1,
  "skills": {
    "my-skill": {
      "current": 3,
      "revisions": [
        {
          "revision": 3,
          "version": "1.4.2",
          "digest": "sha256:abc…",
          "registry": "registry.example.com/skills/my-skill",
          "values": { "features": { "advanced": true } },
          "overlays": [
            { "name": "team-refs", "digest": "sha256:def…" }
          ],
          "installedAt": "2026-07-07T12:00:00Z"
        }
      ]
    }
  }
}
```

---

## 6. Registry Proxy

### 6.1 Model: transparent credential pass-through

Epos can run as a registry **proxy** in front of one or more upstream OCI registries. It is a **transparent pass-through**:

- Epos **stores no secrets** and holds **no credentials of its own**.
- It relays the client's own credentials to the upstream. Under the OCI token-auth flow, Epos forwards the `Authorization` header and the upstream's `WWW-Authenticate` challenge unchanged; the client obtains a Bearer token from the upstream's token service and Epos forwards the authenticated requests. **The upstream enforces all authorization.**
- Both **pull-through and push-through** are supported: users push *through* Epos to the underlying registry, and pull through it. Chunked-upload sequences are relayed faithfully (with awareness that some registries have upload quirks; see §6.5).

### 6.2 The only stored credential: read-only listing secrets

The **sole** credential Epos may hold is a **read-only** secret used to **list** Skills available in a registry for discovery/frontend purposes (§8). These are scoped to `pull`/catalog-read only. They exist so the frontend can enumerate a registry the browsing user has not personally authenticated to. They are never used to push and never broker write access.

**Reference mechanism.** Read-only listing secrets are referenced **only by environment variable** from `registries.yaml` (see §8.3) — e.g. `usernameEnv:` / `tokenEnv:`. Epos reads them from the process environment at runtime; no secret value is ever written to a config file or persisted by Epos. This maps directly to a Kubernetes `Secret` projected into the pod as env vars.

**Boundary — this rule governs only Epos's own listing credential.** Push/pull in the transparent pass-through path use the **client's own** credentials, which are relayed, not stored. `epos push`/`epos pull`/`epos registry login` therefore continue to use the client's Docker credential store exactly as Helm/ORAS do. The env-var-only rule is specifically about the listing secret Epos holds, not a restriction on how end-user clients authenticate their own pushes.

### 6.3 OCI endpoints proxied

Standard `/v2/` distribution endpoints: manifest GET/HEAD/PUT, blob GET/HEAD/POST/PATCH/PUT, `tags/list`, and `_catalog` where the upstream supports it. The proxy honors the spec's proxy semantics (e.g., Host header is the proxy's; optional `ns` query parameter to disambiguate upstream host when federating).

### 6.4 Download statistics interception

The proxy is the natural interception point for download stats (§10). It counts **manifest GETs** as the countable "pull" event, following Docker's convention:

- Count `GET /v2/<name>/manifests/<ref>`.
- **Exclude** HEAD requests (freshness checks) and blob GETs (many per artifact).
- Handle multi-arch/index manifests carefully to avoid double counting (distinguish index vs. image manifest media types).

### 6.5 Known upstream quirks (documented constraints)

- Some managed registries **disable `_catalog`** (e.g., AWS ECR does not implement it) → discovery falls back to explicit registration (§8).
- On GitLab, `_catalog` exists but effectively requires an **admin-scoped** `registry:catalog:*` token; an ordinary read-only project token cannot enumerate instance-wide → explicit registration is the reliable path there.
- Registry catalog contents are advisory per the spec (a repo's presence/absence in `_catalog` is not a guarantee).
- Chunked upload conformance varies across registries; push-through relays sequences as-is and surfaces upstream errors rather than masking them.

---

## 7. Signing and Verification

### 7.1 Model: cosign / Sigstore, OCI-native

Signing uses **cosign/Sigstore**, with signatures attached via the **OCI 1.1 `subject`/referrers** mechanism (the same mechanism GitLab already uses to display cosign signatures next to artifacts). No PGP `.prov` layer; no Epos-specific signature format.

### 7.2 Policy: verify-when-present (opt-in enforcement)

- Epos **always verifies** a cosign signature **if one exists**, and **fails on a bad signature**.
- Unsigned Skills are **permitted** by default (low adoption friction for a young ecosystem).
- Enforcement is opt-in: a **`--require-signature`** flag and an equivalent **per-registry policy** setting promote "unsigned" to a hard failure where an operator wants mandatory signing.

### 7.3 Interaction with digest pinning

Signature verification is orthogonal to and complementary with the lockfile's digest pinning: digests give reproducibility/tamper-evidence over a fixed reference; cosign gives authorship/identity assurance. Both may be required together under `--require-signature`.

---

## 8. Discovery and Search

### 8.1 Hybrid discovery

Epos discovers Skills across a **list of registries** using a **hybrid, auto-detecting** strategy:

1. **Catalog-based discovery where supported.** Epos calls `/v2/_catalog` + `/v2/<name>/tags/list` (with the read-only listing credentials, §6.2) and **filters to Skills by inspecting manifest media types** (the Epos config media type in §2.4 is the discriminator). Works on GitLab (admin-scoped token), Harbor, Nexus, `registry:2`.
2. **Explicit registration (universal).** A Skill, repository, or namespace can be **registered directly** with Epos. This path never depends on `_catalog`, so it is the reliable/universal mechanism — required for ECR and for GitLab when the listing token is not admin-scoped.

**Registration is always sufficient on its own.** Catalog scanning is an *accelerator* layered on top where the registry and token privileges allow it. In both paths, media-type inspection is the skill/non-skill discriminator.

#### 8.1.1 Auto-detection with declared fallback

Per-registry mode is **auto-detected** unless forced. At startup (and on config reload, plus an optional refresh interval), Epos probes each registry once:

- **Probe:** `GET /v2/_catalog?n=1` using that registry's read-only listing credential.
- **Catalog mode** is selected when the probe returns **2xx with a parseable catalog body**.
- **Registered fallback** is selected when the probe returns **401 / 403 / 404 / 501**, any error, or an unparseable body. In this mode Epos enumerates only the entry's declared `repositories:` / `namespaces:` (§8.3).
- The probe result is **privilege-dependent** and this is expected, not an error: e.g. a non-admin GitLab token yields registered fallback because instance-wide `_catalog` requires an admin-scoped `registry:catalog:*` token. Epos logs the selected mode per registry.

**Forced mode.** An entry may set `discovery: catalog` or `discovery: registered` to **force** the mode and **skip the probe** entirely. A forced value is authoritative. This keeps deployments deterministic where the operator already knows a registry's capability.

**Re-probing.** The probe re-runs on process start, on `registries.yaml` reload, and on an optional configured interval; a registry that gains/loses catalog capability (or whose token scope changes) is re-classified on the next probe.

### 8.2 Registration index

The set of explicit registrations is the **registration index**. Its storage is pluggable (§11): in-memory by default; a durable backend (Kubernetes `ConfigMap`/`Secret` or PostgreSQL) is needed **only** when registrations are added at **runtime** (via API/UI) and must survive a restart. If registrations come solely from a **config file** (`registries.yaml`, §8.3), the in-memory index rebuilds from config on startup and no durable store is required.

### 8.3 Configuration files

Epos uses a **split configuration**: slow-changing server/backend/stats settings live in **`epos.yaml`**; the frequently-edited registry list lives in a separate **`registries.yaml`**. `registries.yaml` is the **config-sourced form of the registration index** (§8.2) — the same document that seeds the in-memory index. When registrations come only from this file, no durable backend is required.

**Precedence.** Where both files could express the same thing, `epos.yaml` governs server/backend/stats and `registries.yaml` governs the registry list; they are non-overlapping by design. Runtime registrations (API/UI), when enabled, are layered **on top of** the `registries.yaml`-seeded set and are the only registrations that require a durable backend to survive restart.

#### 8.3.1 `epos.yaml` (server / backends / stats)

```yaml
apiVersion: epos/v1
kind: Config

server:
  listen: ":8080"
  metricsListen: ":9090"        # Prometheus /metrics endpoint

# Pluggable durable-state backend (§11).
# Top-level `backend` sets the default for BOTH concerns; either may override.
backend:
  type: memory                  # memory | configmap | secret | postgres
  # configmap/secret:
  # namespace: epos
  # name: epos-state
  # postgres:
  # dsnEnv: EPOS_PG_DSN         # DSN referenced by env var, never inlined

registrationIndex:              # optional per-concern override of `backend`
  # type: configmap
  # namespace: epos
  # name: epos-registrations

revisionHistory:                # optional per-concern override of `backend`
  # type: postgres
  # dsnEnv: EPOS_PG_DSN
  retention: 10                 # N revisions retained per skill (§5.3)

# Stats is ALWAYS its own block (append-only events ≠ current-state store, §10–§11).
stats:
  type: prometheus              # prometheus | clickhouse
  prometheus:
    perSkill: false             # enable per-skill series only for small catalogs
  # clickhouse:
  #   dsnEnv: EPOS_CLICKHOUSE_DSN

signing:
  requireSignature: false       # global enforcement; per-registry may tighten (§7.2)
```

**Backend precedence (explicit):** a per-concern block (`registrationIndex:` / `revisionHistory:`) **overrides** the top-level `backend:` default for that concern; if absent, the concern inherits `backend:`. `stats:` never inherits `backend:`.

#### 8.3.2 `registries.yaml` (registry list = registration index seed)

```yaml
apiVersion: epos/v1
kind: Registries

# Optional refresh interval for re-probing discovery capability (§8.1.1).
discoveryRefreshInterval: "15m"

registries:
  - name: gitlab
    url: https://registry.gitlab.example.com
    # Read-only listing credential — ENV-VAR REFERENCES ONLY (§6.2).
    usernameEnv: GITLAB_LISTING_USER
    tokenEnv: GITLAB_LISTING_TOKEN
    # discovery omitted → auto-detect via probe (§8.1.1).
    # Registered fallback sources (used if probe → registered, or always merged in):
    repositories:                # explicit repos: guaranteed-working floor
      - skills/pdf-tools
      - skills/web-scrape
    namespaces:                  # enumerated where the registry supports listing
      - skills                   # ignored WITH A WARNING where not enumerable

  - name: ecr
    url: https://1234.dkr.ecr.us-east-1.amazonaws.com
    tokenEnv: ECR_LISTING_TOKEN
    discovery: registered        # forced: ECR has no _catalog; skip the probe
    repositories:
      - team-a/skill-alpha
      - team-a/skill-beta
    # namespaces here would be ignored-with-warning (ECR not enumerable)

  - name: harbor
    url: https://harbor.example.com
    usernameEnv: HARBOR_LISTING_USER
    tokenEnv: HARBOR_LISTING_TOKEN
    discovery: catalog           # forced catalog; skip the probe
```

**Registry entry schema.**

| Field | Meaning |
|---|---|
| `name` | Local identifier for the registry entry. |
| `url` | Registry base URL. |
| `usernameEnv` / `tokenEnv` | **Env-var names** for the read-only listing credential (§6.2). Never inline values. |
| `discovery` | Optional. `catalog` or `registered` to **force** mode and skip the probe; omit for auto-detect (§8.1.1). |
| `repositories` | Explicit repo paths — the **guaranteed-working** registered-fallback floor (required for non-enumerable registries like ECR). |
| `namespaces` | Namespace/prefix roots — **enumerated where the registry supports catalog-style listing**, and **ignored with a warning** where it does not. An entry may use `repositories`, `namespaces`, or both. |

**Capability-dependent behavior (documented):** a bare `namespaces:` entry on a non-enumerable registry contributes nothing until explicit `repositories:` are added (Epos emits a warning). This is the honest consequence of registries that disable `_catalog`.

---

## 9. Composition: Layers, Overlays, and Dependencies

Epos composes a skill from a **stack of layers** into **one merged skill**. This single model unifies what were previously two ideas — "overlays" (modifications) and "dependencies" (other skills you build on). A **layer is a layer**; the only differences are what *kind* of thing it is (a skill or an overlay) and where it *comes from* (local in your repo, or pulled from a registry/git). The dominant use case is a **base skill plus your own overlays**: pull the base as a lower layer, put your overlay on top, get one coherent skill out.

### 9.1 The layer stack

A composition is an ordered stack, lowest → highest precedence:

1. **Origin** (bottom) — an original skill's own files.
2. **Intermediate layers** (middle) — dependency skills and/or overlays that sit on the origin (e.g. an OCI-published skill that already modified some files, or a published overlay applied directly over the origin).
3. **Consumer / your repo** (top) — your local overlay(s) and this skill's own files; highest precedence.

The stack resolves into **one merged skill** using a single **later-overrides-earlier** rule (§9.5). A per-file **provenance report** (which layer each file came from) is available.

*Worked example.* Reference `A` replaced in your repo (top), `B` replaced in an OCI skill (middle), `C` unmodified from origin (bottom) → the merged skill contains **A from your repo, B from the OCI skill, C from origin** — each file resolved to the highest layer that provided/replaced it.

### 9.2 Two kinds of layer

- **Skill layer** — a full skill (origin or a dependency skill), contributing its `SKILL.md` + files.
- **Overlay layer** — a set of **operations** (§9.4) that modify whatever is **below it** in the stack, rather than a full skill. An overlay does not stand alone; it patches the layers beneath it.

### 9.3 Where a layer comes from: local vs. published

Independently of kind, a layer's **source** is either:

- **Local** — a directory in your repo. The common case for overlays: your own modifications, Kustomize-style, applied at install/render time. Local overlays are **not** registry artifacts; you publish the *resulting composed skill*, not the overlay.
- **Published / pulled** — an OCI artifact or a git source pulled into the stack. This is how you **share a reusable overlay** (e.g. "overlay the origin" — a distributable patch others can apply against the same upstream), and how **dependency skills** enter the stack.

Source affects only *how the layer is fetched and pinned* (§9.7), not how it composes. A published overlay is applied to **whatever layer sits below it in the stack** (targeting the right base is the composer's responsibility via stack order; the §9.5 `required: true` semantics are the safety mechanism for operations that must apply).

### 9.4 Operations (the modification vocabulary)

An overlay layer lists operations applied **in listed order** to the layers below. The format is **byte/line oriented and format-agnostic** (it does not parse YAML or Markdown structure), giving one uniform mechanism across `SKILL.md`, `values.yaml`, references, and assets:

| Operation | Meaning |
|---|---|
| `add-file` | Add a new file (reference, asset, script) not present below. |
| `delete-file` | Remove a file. |
| `append-to-file` | Append content to the end of an existing file (e.g., add a reference line to `SKILL.md`). |
| `replace-in-file` | Regex find-and-replace within a file (fuzzy, surgical text edits). |
| `patch-file` | Apply a line-offset unified-diff hunk (precise multi-line edits with context). |

#### 9.4.1 On-disk overlay format (`Overlay.yaml`)

The overlay-authoring format follows the shape Kustomize converged on after deprecating its split `patchesStrategicMerge`/`patchesJson6902` fields: a **single manifest with one ordered `operations:` list**, where **each operation supplies its payload either inline or by referencing a sibling file via `path:`** — author's choice per operation. Operations apply **in listed order**; when two operations affect the same bytes, the **later one wins**.

```
my-overlay/
  Overlay.yaml
  files/                 # payloads referenced by path: (add-file content, diffs, regex)
    advanced.md
    fix-typo.diff
```

```yaml
apiVersion: epos/v1
kind: Overlay
name: team-refs
version: 0.2.0

operations:
  - op: add-file
    target: references/advanced.md
    path: files/advanced.md

  - op: append-to-file
    target: SKILL.md
    content: |
      See also: [Advanced usage](references/advanced.md)

  - op: replace-in-file
    target: SKILL.md
    pattern: "PDF Tools"
    replacement: "PDF Tools (Team Edition)"
    required: false        # soft (default); true → hard error if no match (§9.5)

  - op: patch-file
    target: values.yaml
    path: files/fix-typo.diff
    required: true         # must apply cleanly or fail

  - op: delete-file
    target: assets/unused.png
```

**Per-operation fields.**

| Field | Applies to | Meaning |
|---|---|---|
| `op` | all | One of `add-file`, `delete-file`, `append-to-file`, `replace-in-file`, `patch-file`. |
| `target` | all | Path (in the layers below) the operation acts on. |
| `path` | `add-file`, `append-to-file`, `patch-file`, `replace-in-file` | Sibling file supplying the payload (alternative to inline). |
| `content` | `append-to-file`, `add-file` | Inline payload (alternative to `path:`). |
| `pattern` / `replacement` | `replace-in-file` | Inline regex and replacement (alternative to `path:` supplying both). |
| `required` | `replace-in-file`, `patch-file` | Per-operation strictness (§9.5). |

Exactly one payload source (`path:` or the inline field) is provided per operation that needs one; supplying both is a validation error.

> **Note — no mandatory base pin on overlays.** Because an overlay is a layer applied to whatever is beneath it, an overlay does **not** carry a mandatory digest-pinned `base:` fixing it to one specific origin. Correct targeting is by **stack order**; `required: true` (§9.5) is the safety mechanism for must-apply operations. (Reproducibility of *pulled* layers is handled by pin capture, §9.7.)

### 9.5 Override precedence, granularity, and failure semantics

**Precedence.** For every file in the merged result, the **highest layer that provides or replaces it wins**; unmodified files fall through from lower layers. The consumer (top) always has final say.

**Granularity.**
- **References / scripts / assets** → **whole-file**: the highest layer that supplies a given path owns that entire file; lower layers fall through (the §9.1 A/B/C example).
- **`SKILL.md`** → **operation-merge**: because every layer contributes to the single `SKILL.md`, higher layers apply their overlay operations (§9.4) onto the composed-so-far body — so a layer can append a reference line or patch a section of the base body **without restating the whole file**. Intra-`SKILL.md` conflicts across layers resolve by **layer order** (higher wins) plus the failure semantics below.

**Failure semantics — soft by default, strict opt-in.**
- By default, an operation that **finds no match** (`replace-in-file`) or **fails to apply cleanly** (`patch-file`) **warns and is skipped** — tolerant of benign drift in lower layers.
- A global **`--strict`** flag and a per-operation **`required: true`** promote non-matching/failing operations to **hard errors**.
- `add-file`/`delete-file`/`append-to-file` always apply deterministically; obvious conflicts (e.g. `add-file` onto an existing path) are reported.

### 9.6 Dependencies: declaring pulled layers (`Epos.yaml`)

Pulled layers (dependency skills and published overlays) are declared skill-level in `Epos.yaml` under `dependencies`, a **unified, source-typed** list. Each entry names an **OCI** or **git** source; composition semantics are identical regardless of source, and only pin-capture differs (§9.7). Declaration order places the layer in the stack; local overlays sit above the declared pulled layers, with the consumer skill on top.

```yaml
# Epos.yaml
dependencies:
  - name: base-pdf            # layer name; also the values-nesting key (§9.8)
    oci: registry.example.com/skills/pdf-tools
    version: 1.4.2            # tag; digest pinned in the lock (§9.7)
  - name: origin-patch        # a published overlay applied over what's below
    kind: overlay
    oci: registry.example.com/overlays/origin-patch
    version: 0.2.0
  - name: shared-refs
    git: https://git.example.com/org/skills.git
    ref: v2.1.0
    subpath: skills/shared-refs
```

Pins travel with the skill (recorded in the lock, §9.7). Declaring or composing dependencies **never republishes anything**; a one-time fetch may occur to compose.

### 9.7 Pin capture for pulled layers

Each pulled layer is pinned reproducibly at resolve/lock time; a one-time fetch may occur to compose, but nothing is repackaged or pushed:

- **OCI source** → pin the **manifest digest** (tag informational; digest authoritative), per §5.2.
- **Git source** → resolve the declared `ref` to a full **commit SHA** and record the **git tree object SHA** of `subpath` at that commit (`git rev-parse <commit>:<subpath>`) as the content hash. Pin record: `{ source, ref, commit, treeSha, subpath }`. Reuses git's native tree addressing (no custom canonicalization); caveat: this is git's default **SHA-1** object hash (SHA-256 object mode exists but is rare), so tamper-evidence rests on git tree addressing rather than an independent SHA-256 — accepted for zero new hashing scheme.

Verification re-resolves and compares digest/tree SHA; any mismatch is a **hard error**. Pins are recorded in the lock (`Epos.lock`; reflected in the install bundle revision, §5.3) as `{ name, kind: skill|overlay, sourceType: oci|git, source, version|ref, digest|commit+treeSha, subpath? }`. `--frozen`/CI installs verify pins and fail on mismatch.

### 9.8 Values scoping (Helm-style, nested + `global`)

Each layer's values are addressed **nested under the layer's `name`** (e.g. `base-pdf.someValue`), with a shared **`global`** block visible to all layers. Each skill layer renders its own templated `SKILL.md`/references with its **own scoped values before the merge**; composition then resolves the stack (§9.5). This preserves collision-isolation (two layers may both use `.Values.title` for different purposes without clashing), with `global` as the deliberate cross-layer channel. Consistent with the base values model (§3.3).

### 9.9 Packaging and distributing an overlay

A **local** overlay is never a registry artifact — you publish the resulting composed skill. A **published** overlay (for sharing, e.g. to "overlay the origin") is an OCI artifact (media types in §2.4): a **config blob** (`Overlay.yaml` metadata + the ordered `operations:` list) and a **single tar+gzip content layer** bundling the overlay's `files/` payloads. `epos overlay package` builds it; `epos overlay push` publishes it via ORAS. Published overlays are pulled/discovered like skills, may be signed (§7), and are pinned by digest when declared as a pulled layer (§9.7).

---

## 10. Download Statistics

### 10.1 Where counts come from

The proxy counts manifest GETs (§6.4). Stats storage is a **hybrid**, chosen to avoid Prometheus high-cardinality abuse while scaling to large catalogs:

- **Default — Prometheus, aggregate only.** Low-cardinality counters (total pulls, pulls per registry, error rates). No per-skill labels. Safe at any catalog size. Exposed at `/metrics`; scraped/stored by an external Prometheus; visualized in Grafana. Epos itself persists nothing in this mode.
- **Optional — Prometheus, per-skill export.** Per-skill series can be **enabled** for **small catalogs** (on the order of a couple hundred Skills) where the cardinality is manageable. The operator opts in knowing their catalog size.
- **Optional — ClickHouse.** For **large catalogs** (thousands of Skills), each countable manifest GET is written as an **event row** (skill name, version/digest, registry, timestamp). ClickHouse gives exact per-skill lifetime totals, top-N, and time-series without polluting Prometheus. This is the path the frontend's granular per-skill stats require.

### 10.2 Counting rules (summary)

Count manifest GETs; exclude HEAD and blob GETs; distinguish index vs. image manifests to avoid multi-arch double counting. These rules match Docker's documented pull-counting convention so numbers are comparable to other tooling.

---

## 11. Storage Backends (Pluggable)

### 11.1 One interface, multiple implementations

Two kinds of durable state — the **registration index** (§8.2) and the **in-cluster revision history** (§5.4) — sit behind a **single pluggable storage interface**, mirroring Helm's own pluggable storage philosophy (Helm: `Secret` default, `ConfigMap` or SQL optional). The operator selects a backend by config.

Implementations:

- **In-memory** — default; ephemeral; rebuilt from config on startup. Sufficient when there is no runtime registration and history lives in the local git-committed lockfile.
- **Kubernetes-native `ConfigMap`/`Secret`** — no external dependency; leverages the cluster; survives restarts via etcd; GitOps-friendly. Uses a `Secret` when entries include read-only listing credentials or sensitive values. Load-and-filter in memory; suitable for hundreds to low-thousands of entries; subject to per-object size limits (~1 MiB) and optimistic-concurrency (resourceVersion) on writes.
- **PostgreSQL** — durable, queryable, horizontally scalable across replicas; the choice for large catalogs or heavy runtime-registration use. (This is the Artifact-Hub-proven pattern.)

A **CRD-based backend** (one custom resource per registration, `kubectl get`-able and RBAC/GitOps-managed) is a reserved future implementation slot behind the same interface — not required for v1.

### 11.2 Scope boundary

**Download statistics are explicitly out of scope for this interface.** Stats always use the Prometheus/ClickHouse path (§10). The pluggable interface covers only the registration index and revision history.

---

## 12. Web Frontend

### 12.1 Purpose and behavior

A **Kubernetes-deployable** web frontend that connects to a **list of registries**, **federates** across them, and **filters to Skill packages only** (via the media-type discriminator, §8.1). It shows Skill metadata (from the OCI config blob / `Epos.yaml`), versions, and — when a per-skill stats backend is enabled (§10) — download statistics.

### 12.2 Data sources

- **Skill list:** an **in-memory index by default** (seeded from the registration list, enriched by periodic catalog scans where allowed, refreshed on an interval), so page loads are fast and decoupled from upstream latency/rate limits. An **optional durable PostgreSQL index** is used only when runtime (non-config) registrations must survive restarts (§8.2, §11). Discovery/classification state (the current-state catalog) is kept conceptually **separate** from the append-only stats event store.
- **Stats:** from Prometheus (aggregate) or ClickHouse (per-skill), per §10.

### 12.3 Architecture

Go backend (shares the proxy/discovery core) + a single-page frontend. Deployed as container images with `Deployment` + `Service` + `Ingress`. External/managed PostgreSQL and ClickHouse (when enabled) are recommended over in-cluster stateful sets. This mirrors Artifact Hub's proven "index metadata, don't proxy content" model — except Epos, sitting in the request path as a proxy, can also count real pulls.

---

## 13. Deployment

Epos ships two deployables: the **Go service** (proxy + discovery + frontend backend) as a container image for Kubernetes, and the **static frontend/docs site**, which is published via **GitHub Pages in branch mode** with **PR-preview** deployments.

### 13.1 Kubernetes deployment of the service

Standard pattern: container image, `Deployment` + `Service` + `Ingress`. Backends (registration index / revision history) selected via config per §11; stats via Prometheus and optional ClickHouse per §10. No secret storage beyond optional read-only listing secrets (§6.2), supplied as Kubernetes `Secret`s.

### 13.2 GitHub Pages — branch mode

The static site (project/docs frontend) deploys to GitHub Pages using **branch mode**: a workflow builds the site and publishes the built output to the **`gh-pages` branch**, which Pages serves. Build tooling sets a relative base path so assets resolve correctly when served from a project subpath.

Example production-deploy workflow (`.github/workflows/pages.yml`):

```yaml
name: Deploy site to GitHub Pages
on:
  push:
    branches: [main]
permissions:
  contents: write
jobs:
  build-and-deploy:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-node@v4
        with:
          node-version: 20
      - run: npm ci
      - run: npm run build            # emits ./dist with a relative base path
      - name: Publish to gh-pages
        uses: peaceiris/actions-gh-pages@v4
        with:
          github_token: ${{ secrets.GITHUB_TOKEN }}
          publish_dir: ./dist
          publish_branch: gh-pages
```

### 13.3 GitHub Pages — PR preview workflow

Every pull request gets an ephemeral **preview deployment** published to a subpath of the same `gh-pages` branch and torn down when the PR closes, using `rossjrw/pr-preview-action`.

Example PR-preview workflow (`.github/workflows/pr-preview.yml`):

```yaml
name: PR Preview
on:
  pull_request:
    types: [opened, synchronize, reopened, closed]
permissions:
  contents: write
  pull-requests: write
concurrency: preview-${{ github.ref }}
jobs:
  preview:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-node@v4
        with:
          node-version: 20
      - if: github.event.action != 'closed'
        run: |
          npm ci
          npm run build            # relative base path (e.g. ./) so subpath previews work
      - name: Deploy preview
        uses: rossjrw/pr-preview-action@v1
        with:
          source-dir: ./dist
          preview-branch: gh-pages
          umbrella-dir: pr-preview
```

> **Base-path note.** Because PR previews are served from a subpath (`/pr-preview/pr-N/`), the site build must use a **relative base path** so CSS/JS/asset URLs resolve under the subpath. Configure the build tool's base to `./` (or the project path) accordingly.

---

## 14. Cluster Install as Mountable ConfigMap(s)

Beyond local file materialization, a Skill can be **rendered and installed into Kubernetes as mountable ConfigMap(s)**, so an agent runtime can consume `SKILL.md` + references from a volume-mounted directory. This is a distinct **install target**, separated from local file installs Helm-style by verb and flag (§4.4).

### 14.1 Projection model: files-as-keys (mountable), not an opaque blob

The ConfigMap is the **rendered Skill projected as files** — one key per rendered file, human-readable and inspectable, so a `configMap` volume mounts them back as real files. This is deliberately **not** Helm's opaque gzip+base64 state blob (that encoding is reserved for the in-cluster *revision records*, §14.6). Text files go in `data`; non-UTF-8 files go in `binaryData` (base64), matching `kubectl create configmap --from-file` behavior.

### 14.2 Directory-tree strategy: single ConfigMap + `items[].path`, auto-split past 1 MiB

ConfigMap keys are flat and constrained to `[A-Za-z0-9-_.]` (no `/`). To reconstruct the Skill's nested tree:

- **Default (single ConfigMap).** Each file is stored under a sanitized flat key, and Epos emits the matching `configMap` volume **`items:` list** mapping each key to its true relative path (which *may* contain `/`). A single **full-volume mount** projects the whole tree, and — because it is not a `subPath` mount — it **auto-updates** when the ConfigMap changes.
- **Fallback (auto-split).** When the rendered content (including `binaryData` base64 inflation, ~33%) would exceed the **1 MiB** ConfigMap ceiling, Epos **auto-splits into one ConfigMap per subtree**, each full-volume-mounted at its directory. Split ConfigMaps are named by suffixing the install handle with the subtree (e.g. `<name>`, `<name>-references`, `<name>-scripts`).

**`subPath` is deliberately avoided** for the tree: a ConfigMap consumed via `subPath` does **not** receive updates when the ConfigMap changes, and it requires one `volumeMounts` entry per file. Full-volume mounts with `items[].path` avoid both problems.

Epos emits a ready-to-use `volumes` + `volumeMounts` snippet for the produced ConfigMap(s) in both the single and split cases.

### 14.3 Binary assets

Non-UTF-8 files (images, other `assets/`) are placed in `binaryData` (base64) and project as files on mount. Base64 inflation counts against the 1 MiB budget; the **per-subtree split (§14.2) is the size safety valve** — there is no separate binary-size rejection or threshold knob.

### 14.4 Verb/flag separation (recap of §4.4)

- **`epos template --target=configmap`** → emits ConfigMap YAML + mount snippet; **no cluster access / no credentials**; GitOps-friendly (commit or `kubectl apply`). Analogue of `helm template`.
- **`epos install --target=configmap`** → writes the ConfigMap(s) to the cluster via the Kubernetes API. Analogue of `helm install`; **requires cluster RBAC** (create/update ConfigMaps in the target namespace). This is the sole Epos path that holds cluster credentials.
- **`epos install --target=files`** (default) → unchanged local file materialization (§4.2, §5).

### 14.5 Install handle and naming (Helm-release-style)

Cluster installs use a **Helm-release-style handle**:

```
epos install <name> <skill-ref> --target=configmap -n <namespace>
```

- `<name>` is the stable **install handle** — it names the ConfigMap(s), keys the in-cluster revision records (§14.6), and is the argument to `rollback`/`uninstall`/`status`/`history` for that install.
- **Namespace** comes from `-n`/`--namespace` or the current kubeconfig context.
- **Split naming** suffixes the handle by subtree (§14.2).
- The **same Skill may be installed multiple times under different handles** without collision (independent ConfigMaps and revision records), exactly like Helm releases.

### 14.6 History and rollback: in-cluster revision records (cluster-authoritative)

For the ConfigMap target, revision history is **self-contained in-cluster** (Helm-release-style), via the pluggable §11 backend — **not** the local lockfile:

- Each `install`/`upgrade --target=configmap` writes a self-contained revision record (rendered bundle: version + digest + resolved values + pinned overlays), keyed by install handle, using the Helm-style opaque encoding (JSON → gzip → base64) in a `ConfigMap`/`Secret` (or PostgreSQL at scale).
- `epos rollback <name> --target=configmap` reads the **prior in-cluster revision** for that handle and re-applies it, recording a new revision (Helm-style new-revision-on-rollback).
- **Cluster installs are authoritative in-cluster** and work **without the local project/lockfile present** — e.g. from a CI runner or another machine.

**Provenance split (deliberate exception to §5.4).** Local **file** installs record bundle history in the git-committed lockfile; cluster **ConfigMap** installs record it **in-cluster**. The two targets have distinct history provenance by design. The §5.4 "lockfile embeds history everywhere" statement applies to the file target; the ConfigMap target is the stated exception.

### 14.7 Constraints and cautions (documented)

- **1 MiB ceiling** per ConfigMap (etcd) governs the split; very large or binary-heavy Skills produce more/larger split objects.
- Keys must match `[A-Za-z0-9-_.]`; the sanitized-key + `items[].path` mapping is how real paths (with `/`) are reconstructed.
- Out-of-band edits to a live ConfigMap are overwritten on the next `install`/`upgrade`/`rollback` for that handle (Epos owns the object under the handle).
- `install --target=configmap` requires cluster RBAC; `template --target=configmap` and `--target=files` do not.

---

## 15. Model-Driven Structure, BDD & Testing

Epos does not hand-write a project-structure section: **the structure is generated from a SysML v2 model** by [sysgo](https://github.com/gaarutyunov/sysgo), and behavior is specified as **Gherkin journeys** exercised by real integration tests. This section explains the model, the features, and the test/CI pipeline. The model and features live as source artifacts in the repo (not inline here): the model at `model/epos.sysml`, the features under `features/`.

### 15.1 The model is the structure (sysgo)

`model/epos.sysml` is a single consolidated SysML v2 model containing one `package` per bounded context. sysgo converts SysML v2 API JSON into a DDD/hexagonal Go scaffold; the textual `.sysml` is converted to that JSON by `scripts/sysml2json.sh` (OMG Pilot Implementation serializer). A single file is used deliberately — it matches sysgo's single-model-example shape and single-JSON-array input, avoiding any dependence on multi-file merge behavior.

sysgo's mapping (its §7) applied throughout the model: `package` → bounded context (top-level Go tree); `part def` with an identity attribute → aggregate root/entity, without identity → value object; `attribute` → field; `part x : T[*]` → composition; `port def` → Go interface (driven port → `app/port/out`, `in item` → param, `out item` → return); `action def` → use case; `item def` → DTO; `requirement def` → doc + test stub. Driven ports stay in the application region (`ports.repository-in-domain: false`, sysgo default).

**Bounded contexts** (one `package` each): `Packaging`, `Composition`, `Registry`, `Signing`, `Stats`, `Install`, `Frontend`, plus a shared `Infrastructure` context. The shared context holds **only** generic, domain-free low-level clients reused across contexts — `OciClient`, `GitClient`, `KubeClient`. Every domain-shaped driven port (e.g. `Install.RevisionStore`, `Registry.RegistrationStore`, `Stats.StatSink`, `Composition.LayerSource`) is owned by its context; its adapter may use a shared client. This is the hybrid rule: **domain ports are context-owned; only generic plumbing clients are shared.**

The pluggable backends decided elsewhere in this spec are exactly these ports: the storage backend (§11) is realized behind `RegistrationStore`/`RevisionStore` adapters (in-memory, ConfigMap/Secret, or Postgres); the stats backend (§10) behind `StatSink` (Prometheus/ClickHouse). sysgo regenerates idempotently (`// Code generated by sysgo; DO NOT EDIT.`), so field-level model changes are cheap; adapter/impl/main files are scaffolded once and then hand-owned.

### 15.2 Behavior as Gherkin journeys

Behavior is specified as **journey-style** feature files (one file per user-facing workflow, scenarios crossing contexts), under `features/`:

- `author-and-publish.feature` — package, validate/lint, push
- `install-locally.feature` — files target, lockfile, upgrade, rollback
- `install-to-cluster.feature` — ConfigMap target, template, auto-split, cluster rollback
- `compose-with-overlays.feature` — layer-stack precedence, OCI+git deps, pin capture, overlays
- `discover-and-search.feature` — catalog vs registered discovery, pass-through, filtering
- `sign-and-verify.feature` — verify-when-present, `--require-signature`, tamper detection

Journeys (not per-context features) are used so each scenario is a **vertical slice** that exercises real cross-context integration — which is what the container-backed tests target. The tradeoff (features don't map 1:1 to model packages) is accepted; model↔behavior traceability is via this list and the `.sysml` package names.

**The `features/*.feature` files are the canonical, executable test source and MUST be used directly by the test suite.** godog loads these files as-is (via `Options.Paths: ["features"]`); they are not illustrative examples, and their Gherkin MUST NOT be duplicated, paraphrased, or re-transcribed elsewhere (including inline in this spec). This spec references the files rather than embedding their content, so there is exactly one source of truth for behavior. Adding or changing a scenario means editing the `.feature` file directly; the step definitions and implementation follow from it (§15.4). Any Gherkin listing in documentation is a pointer to these files, never a copy.

### 15.3 Integration tests with testcontainers

The runner is **godog** (Cucumber for Go), integrated with `go test` and emitting JUnit for CI. Step definitions drive the CLI/service against **real dependencies** started via **testcontainers-go**:

- **OCI registry:** **zot** — OCI-native, supports `/v2/_catalog` (discovery scenarios), native cosign/referrers (signing scenarios), and htpasswd/bearer auth (pass-through scenarios). Driven via the generic container API (no dedicated module), with a config file and a readiness wait strategy.
- **Git server:** **Gitea** (itself written in Go) — real HTTP git transport for git-dependency resolution, ref→commit and tree-SHA capture, and private-repo auth.
- **Cluster:** **k3s** via the testcontainers k3s module (`k3s.Run`, `GetKubeConfig`, `LoadImages`, `WithManifest`) — a real cluster for the ConfigMap install/rollback journeys.

No mocks stand in for these systems; the journeys run end-to-end against them.

### 15.4 TDD workflow (vertical slices, one commit per scenario)

Implementation follows strict TDD with no phased development — the system is always buildable and each increment is a working vertical slice:

1. Generate the scaffold from `model/epos.sysml` (sysgo); add a **minimal buildable shim** so the module compiles and the CLI runs (no behavior yet).
2. Write the Gherkin for a scenario (already authored in `features/`), wire its step definitions, and **run it red**.
3. Implement just enough across the touched contexts to make that **one scenario green**.
4. **Commit — one scenario per commit.** Repeat for the next scenario.

No "phase" defers a whole subsystem to the end; every commit keeps all prior scenarios green. Each journey is completed scenario-by-scenario so a working slice exists at every step.

### 15.5 Decomposition summary

| Context (`package`) | Aggregates / key types | Owned driven ports | Journey coverage |
|---|---|---|---|
| Packaging | `SkillArtifact`, `SkillMetadata` | `PackagingPort`, `ValidationPort` | author-and-publish |
| Composition | `LayerStack`, `Layer`, `Pin`, `Operation` | `LayerSource`, `CompositionPort` | compose-with-overlays |
| Registry | `RegistrationIndex`, `RegistryEntry` | `RegistrationStore`, `CatalogProbe`, `ProxyPort` | discover-and-search |
| Signing | `VerificationSubject`, `Signature` | `SignaturePort` | sign-and-verify |
| Stats | `DownloadCounter` | `StatSink` | discover-and-search (pulls) |
| Install | `Release`, `Revision`, `Lockfile` | `RevisionStore`, `MaterializePort` | install-locally, install-to-cluster |
| Frontend | `Catalog`, `SkillCard` | `FrontendPort`, `CatalogFeed` | discover-and-search |
| Infrastructure (shared) | — | `OciClient`, `GitClient`, `KubeClient` | (used by all adapters) |

### 15.6 CI: everything runs on the merge request

A single GitHub Actions workflow runs on every merge request:

```yaml
name: CI
on:
  pull_request:
    branches: [main]
jobs:
  test:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version: '1.24'
      - name: Unit + integration (godog + testcontainers)
        run: go test ./... -tags=integration
```

The runner provides a Docker daemon so testcontainers can start zot, Gitea, and k3s. sysgo generation and `scripts/sysml2json.sh` are **not** run in CI — the generated Go scaffold is committed (matching the artifacts-committed posture); CI only builds and tests. A separate CI check asserts the committed scaffold is in sync with `model/epos.sysml` (regenerate-and-diff), failing the MR on drift.

Deployment is unchanged from §13: the static frontend/docs site publishes via **GitHub Pages in branch mode** (build → `gh-pages`), with **PR-preview** deployments on pull requests (§13.2–13.3).

---


## 16. Decision Ledger (traceability)

The following records the settled design decisions this spec encodes.

- **Language / CLI surface:** Go; Helm verbs mirrored **1:1** with lifecycle verbs reinterpreted for Skills; plus `overlay` and `proxy`/`serve`.
- **Packaging:** OCI artifacts with **Epos's own media types** via ORAS; **single tar+gzip content layer** + config blob.
- **References:** Helm-style templated `SKILL.md`; `includeReference` helper; **dangling-reference lint**.
- **Lockfile:** `skills-lock.json`; pin by **digest + tag, digest authoritative**; mismatch = **hard error**; embeds **bounded N-revision self-contained bundle history** (version + values + digest-pinned overlays).
- **Proxy:** transparent **credential pass-through**; **no secret storage**; **read-only listing secrets only**; **push-through** supported.
- **Discovery:** **hybrid** — `_catalog` where supported (GitLab admin-scoped; ECR unsupported) + **explicit registration** (universal, always sufficient).
- **Signing:** **cosign/Sigstore** via OCI 1.1 subject/referrers; **verify-when-present**; **`--require-signature`** to enforce.
- **Stats:** **Prometheus aggregate** default → optional **Prometheus per-skill** (small catalogs) → optional **ClickHouse** (large catalogs); count **manifest GETs** only.
- **Registration index & revision-history storage:** **single pluggable interface** — **in-memory** default, **ConfigMap/Secret**, or **PostgreSQL** (CRD reserved); **local git-committed lockfile** vs **in-cluster backend** for revision history; **identical bundle semantics** everywhere; **stats out of scope** for this interface.
- **Rollback:** restores the **whole previously installed bundle** (version + values + overlays), Helm-revision-style (new revision on rollback).
- **Overlays:** **declarative**; ops = `add-file`, `delete-file`, `append-to-file`, `replace-in-file` (regex), `patch-file` (line-offset diff); **soft-fail default** + `--strict` / `required: true`; base **pinned by digest**.
- **Frontend:** federated across registries; **filters to Skills**; in-memory index default (optional durable PostgreSQL for runtime registrations); Kubernetes-deployable.
- **Spec artifact:** single comprehensive `SPEC.md`.
- **Deployment:** **GitHub Pages branch mode** + **PR-preview workflow** (plus Kubernetes deployment of the Go service).

**Detail-level decisions (implementation config & formats):**

- **Config layout:** **split** — `epos.yaml` (server/backends/stats) + `registries.yaml` (registry list = registration-index seed). (§8.3)
- **Listing-secret references:** **environment-variable only** (`usernameEnv`/`tokenEnv`); never inlined; distinct from client push/pull credentials. (§6.2)
- **`Epos.yaml` validation:** **strict Agent-Skills alignment** — name (lowercase/digits/hyphens, ≤64, == dir name, no `anthropic`/`claude`), version (SemVer 2.0.0, required), description (required, ≤1024, no XML), enforced at `create`/`lint`/`package`. (§2.2)
- **Discovery mode:** **auto-detect with declared fallback** — probe `_catalog` once per registry; catalog on 2xx-parseable, registered fallback otherwise; `discovery:` forces mode and skips probe; re-probe on start/reload/interval. (§8.1.1)
- **Registry entry schema:** **both `repositories:` and `namespaces:`** — explicit repos are the guaranteed floor; namespaces enumerated where supported, else ignored-with-warning. (§8.3.2)
- **Backend selection:** **top-level `backend:` default + optional per-concern override** (`registrationIndex:` / `revisionHistory:`); `stats:` is always its own block. (§8.3.1, §11)
- **Overlay format:** **single `Overlay.yaml`, one ordered `operations:` list, inline-or-`path:` per operation** (C-Kustomize shape); later-overrides-earlier; `files/` holds referenced payloads. (§9.4.1)

**Cluster ConfigMap install decisions:**

- **Install target model:** files-as-keys **mountable** ConfigMap projection (not an opaque blob); text→`data`, binary→`binaryData`. (§14.1)
- **Tree strategy:** **single ConfigMap + emitted `items[].path`** reconstruction with full-volume mount (auto-updating); **auto-split one ConfigMap per subtree** past the 1 MiB ceiling; `subPath` deliberately avoided. (§14.2)
- **Binary assets:** `binaryData` + base64; **splitting is the size safety valve**, no separate threshold. (§14.3)
- **Verb/flag separation:** **`template --target=configmap`** emits YAML (no credentials); **`install --target=configmap`** writes via K8s API (needs RBAC); **`install --target=files`** default local. (§4.4, §14.4)
- **Cluster install naming:** **Helm-release-style handle** — `epos install <name> <skill> --target=configmap -n <ns>`; handle names ConfigMap(s), keys revision records, and drives rollback/uninstall/status/history; split names suffix the handle. (§14.5)
- **Cluster history/rollback:** **in-cluster, self-contained revision records** (Helm-style, via §11 backend), **cluster-authoritative** for cluster installs and usable without the local lockfile; deliberate provenance split vs. the git-lockfile file target. (§14.6)

**Skill dependency & composition decisions:**

- **Mechanism:** **unified, source-typed `dependencies`** (one list; each entry OCI or git); identical composition semantics regardless of source; only pin-capture differs. (§9.6)
- **Declaration scope:** **skill-level** in `Epos.yaml` (travels with the skill); realizes the formerly-reserved field. (§2.5, §9.6)
- **Composition result:** **one merged composite skill** (Helm-subchart-like pull-and-use-together, but fused, not side-by-side). (§9.1)
- **Layer stack:** origin (bottom) → intermediate dependency skills → consumer/your repo (top); **highest layer that provides/replaces a file wins**; unmodified files fall through. (§9.1, §9.5)
- **Granularity:** **whole-file** for references/scripts/assets; **operation-merge** for `SKILL.md` via overlay ops (§9.4), conflicts by layer order + §9.5 soft/strict. (§9.5)
- **Git pin capture:** **commit SHA + git tree object SHA** (`{source, ref, commit, treeSha, subpath}`); reuse git-native tree addressing (SHA-1 caveat noted); one-time fetch to compose, nothing republished; mismatch = hard error. (§9.7)
- **OCI pin capture:** **manifest digest** (digest authoritative), per §5.2. (§9.7)
- **Values scoping:** **Helm-style nested under dependency name + shared `global`**; each layer renders with its own scoped values before merge. (§9.8)
- **Unification:** dependency-composition and overlays are **one pipeline** (same later-overrides-earlier precedence, same operation set). (§9.1, §9.5)

**Model-driven structure, BDD & testing decisions:**

- **Structure via model:** project structure is **generated by sysgo** from a SysML v2 model, not hand-written; the model *is* the structure. (§15.1)
- **Single model file:** one consolidated `model/epos.sysml` with one `package` per context; matches sysgo's single-JSON-array input, avoiding multi-file-merge risk. (§15.1)
- **Bounded contexts:** fine-grained — `Packaging`, `Composition`, `Registry`, `Signing`, `Stats`, `Install`, `Frontend` + shared `Infrastructure`. (§15.1)
- **Port placement:** domain ports context-owned in `app/port/out` (sysgo default, `repository-in-domain:false`); only generic `OciClient`/`GitClient`/`KubeClient` shared. (§15.1)
- **Model depth:** full — every entity, value object, typed field, port method, use case, DTO. (§15.1)
- **BDD organization:** journey-style feature files (one per workflow, cross-context vertical slices), authored as real `features/*.feature`. (§15.2)
- **Gherkin in spec:** the spec references the `.feature` files and enumerates the journeys; the Gherkin lives in the repo where godog runs it. (§15.2)
- **Features used directly:** the `features/*.feature` files are the canonical executable test source, loaded as-is by godog (`Paths: ["features"]`); MUST NOT be duplicated/paraphrased anywhere — single source of truth for behavior. (§15.2)
- **Integration stack:** godog + testcontainers with **zot** (has `_catalog`, cosign/referrers, auth), **Gitea** (real git HTTP), **k3s** (real cluster). (§15.3)
- **TDD:** minimal buildable shim first; write test → red → implement → green; **one scenario per commit**; no phased development. (§15.4)
- **CI:** everything runs **on the merge request**; sysgo generation not in CI (scaffold committed) + a regenerate-and-diff drift check. (§15.6)
- **Deployment (unchanged):** GitHub Pages **branch mode** (`gh-pages`) + **PR-preview** workflow. (§13.2–13.3, §15.6)
