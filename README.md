# Epos — Helm for Agent Skills

**Epos** (named after the [library ship](https://en.wikipedia.org/wiki/Epos_(library_ship)))
is a Go-based packaging, distribution, and registry-proxy system for AI-agent
**Skills**. It reimagines a Skill — today a bare `SKILL.md` directory — as a
Helm-chart-like, versioned, templatable package distributed as an OCI artifact,
with a Kustomize-style declarative overlay system, a credential-passthrough
registry proxy, download statistics, and a federated, Kubernetes-deployable web
frontend.

The authoritative specification is [`SPEC.md`](./SPEC.md).

## What's here

The project structure is **generated from a SysML v2 model** by the real
[`sysgo`](https://github.com/gaarutyunov/sysgo) binary — the model *is* the
structure (SPEC §15.1). Behavior is specified as **Gherkin journeys** under
[`features/`](./features), executed as-is by godog against real dependencies.

| Path | What |
|---|---|
| `model/epos.sysml` | Consolidated SysML v2 domain model (one `package` per bounded context) |
| `model/model.json` | SysML v2 API JSON (the sysgo input) produced from the model |
| `sysgo.yaml` | sysgo generation config |
| `internal/<context>/` | Generated DDD/hexagonal scaffold + hand-owned logic per bounded context |
| `internal/app` | Application service orchestrating the contexts behind the CLI |
| `internal/infrastructure/{oci,git,kube}` | Shared generic clients (ORAS, git, client-go) |
| `cmd/epos`, `internal/cli` | The `epos` CLI (Helm verbs mirrored 1:1) |
| `features/*.feature` | Canonical, executable behavior journeys (godog) |
| `bdd_test.go` + `bdd_*_test.go` | godog runner and step definitions |

## Build & test

```bash
make build            # go build -o bin/epos ./cmd/epos
make test             # unit + BDD journeys (in-process OCI registry, real git, fake k8s)
make test-integration # the same journeys against zot + k3s via testcontainers
```

The default `make test` needs **no Docker**: it drives the journeys against a
genuine in-process OCI registry, real git repositories, and a fake Kubernetes
API. `-tags=integration` swaps in real containers (zot, Gitea, k3s) per SPEC
§15.3.

## Regenerating the scaffold

```bash
make generate         # model/epos.sysml → model.json → sysgo → Go scaffold
make check-generated  # assert the committed scaffold is in sync (CI drift check)
```

`make model` prefers the OMG SysML v2 Pilot serializer
(`scripts/sysml2json.sh`); where that distribution cannot be downloaded it falls
back to the offline converter (`scripts/sysml2json.py`), which emits the same
SysML v2 API JSON sysgo consumes.

## CLI at a glance

```bash
epos create my-skill                       # scaffold a package
epos lint my-skill                          # strict validation + dangling-reference lint
epos package my-skill                       # build the OCI artifact (tar+gzip + config blob)
epos push my-skill registry.example.com/skills/my-skill:1.0.0
epos install my-skill registry.example.com/skills/my-skill --target=files
epos template my-skill <ref> --target=configmap -n skills   # emit ConfigMap YAML + mount snippet
epos rollback my-skill 1                     # restore a whole previous bundle
epos compose my-skill                        # resolve deps + overlays into one merged skill
epos overlay push <dir> registry.example.com/overlays/team-refs:0.2.0
epos proxy --upstream https://registry.example.com          # pass-through proxy + stats
epos serve --registries registries.yaml                     # federated frontend
```
