package catalog

// The tools page is a capability table, not a logo wall (D9).
//
// The issue asked for "logos of registries that support OCI like zot and
// gitlab". An unqualified logo says *epos works here*, and epos's discovery
// needs GET /v2/_catalog, which several major registries do not serve —
// internal/registry has ErrNoCatalog precisely because of it. So every row
// carries what it was verified to support and how it was measured, and a reader
// can tell "pull works, enumeration does not" from "everything works".
//
// The table is checked in rather than probed at render time. A page that made
// four outbound requests to draw itself would be slow, would be wrong whenever
// a network was, and would put the catalog on somebody else's availability.

// ToolRow is one registry and what it was verified to do.
type ToolRow struct {
	Name string
	// Host is the registry's API host, so a reader can repeat the measurement.
	Host string
	// Pull, Push and Catalog are short verdicts, not booleans: "yes",
	// "authenticated" and "no" are three different answers and a checkbox
	// column would flatten them into two.
	Pull    string
	Push    string
	Catalog string
	// Verified says how the row was established. A capability claim with no
	// method behind it is a logo with extra steps.
	Verified string
}

// AgentRow is one agent and where epos installs a skill for it.
type AgentRow struct {
	Name string
	// Directory is the skill directory the agent reads.
	Directory string
	// Default says whether epos installs there without being told to.
	Default string
}

// registryRows is the checked-in capability table.
//
// The _catalog column was measured on 2026-08-04 against each registry's live
// API. The measurements are recorded here rather than in a commit message
// because the page makes the claim.
func registryRows() []ToolRow {
	return []ToolRow{
		{
			Name: "zot", Host: "(self-hosted)",
			Pull: "yes", Push: "yes", Catalog: "yes",
			Verified: "epos's own conformance and integration tiers run against a pinned " +
				"zot image, including GET /v2/_catalog",
		},
		{
			Name: "GitHub Container Registry", Host: "ghcr.io",
			Pull: "yes", Push: "yes", Catalog: "no",
			Verified: "GET /v2/_catalog answers 401 anonymously and 403 DENIED with an " +
				"anonymously issued registry:catalog:* token — the endpoint is not " +
				"served, so a namespace sweep cannot work and a reference list is " +
				"the mode to use",
		},
		{
			Name: "GitLab Container Registry", Host: "registry.gitlab.com",
			Pull: "yes", Push: "yes", Catalog: "authenticated",
			Verified: "GET /v2/_catalog challenges with " +
				`Bearer realm="https://gitlab.com/jwt/auth", scope="registry:catalog:*"; ` +
				"enumeration depends on a credential that carries that scope",
		},
		{
			Name: "Docker Hub", Host: "registry-1.docker.io",
			Pull: "yes", Push: "yes", Catalog: "authenticated",
			Verified: "GET /v2/_catalog challenges for registry:catalog:* against " +
				"auth.docker.io; anonymous enumeration is refused",
		},
		{
			Name: "Quay", Host: "quay.io",
			Pull: "yes", Push: "yes", Catalog: "yes",
			Verified: `GET /v2/_catalog answers 200 anonymously with {"repositories":[]} — ` +
				"the endpoint is served, and an anonymous caller sees the public set",
		},
	}
}

// agentRows is where epos installs, written by hand against internal/install.
//
// It cannot be derived from the code, and saying so is the point: epos has one
// default base path, and every other target arrives through the worktree
// manifest's additional base paths. A table that implied epos ships a
// per-agent integration would be a positioning claim rather than a fact, so
// each row names the directory and whether epos writes there by default.
func agentRows() []AgentRow {
	return []AgentRow{
		{Name: "Claude Code", Directory: ".claude/skills/<name>/", Default: "yes"},
		{Name: "Any other agent", Directory: "a base path the manifest names",
			Default: "no — configure an additional base path"},
	}
}
