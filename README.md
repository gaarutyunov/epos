# epos

Helm & Docker for Agent Skills

epos packages agent skills as OCI artifacts, publishes them to whichever
registry you already run, and installs them into a worktree with their
templates rendered and their digest pinned.

## Install

```sh
go install github.com/gaarutyunov/epos/cmd/epos@latest
```

Or build from a clone: `go build ./cmd/epos`. Go 1.26 or newer.

## Usage

A skill is a directory with a `SKILL.md`. Pack it into the local store:

```sh
epos pack ./reviewer
```

Publish it, then pull it back down somewhere else:

```sh
echo "$TOKEN" | epos registry login ghcr.io -u <user> --password-stdin
epos push reviewer:1.0.0 oci://ghcr.io/acme/agent-skills
epos pull ghcr.io/acme/agent-skills/reviewer:1.0.0
```

Install it into the current worktree. Templates are rendered here — nothing
upstream renders anything — and the resolved digest is pinned in
`skills.lock.json`:

```sh
epos install reviewer:1.0.0 --set title="The reviewer"
epos ls
```

`epos build` composes a skill from a `Skillfile`, `epos sign` and `epos verify`
handle cosign signatures, and `epos-registry` is a read-only relay that fronts
an upstream registry and counts downloads.

## Docs

Start with the [quick start](https://epos.garutyunov.com/quickstart/).
There is also a [CLI reference](https://epos.garutyunov.com/cli/) and the
[`Skillfile` reference](https://epos.garutyunov.com/skillfile/).
