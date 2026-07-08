// Package discovery implements Epos's hybrid, auto-detecting skill discovery
// (SPEC §8.1): catalog-based where supported, explicit registration everywhere.
// Per-registry mode is auto-detected by probing /v2/_catalog unless forced.
package discovery

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/gaarutyunov/epos/internal/config"
	"github.com/gaarutyunov/epos/internal/infrastructure/oci"
	"github.com/gaarutyunov/epos/internal/packaging/domain"
)

// Result is the outcome of discovering a registry: the detected mode and the
// skill repositories found.
type Result struct {
	Mode  string
	Repos []string
}

// Discoverer probes and enumerates registries using a read-only listing client.
type Discoverer struct {
	Client *oci.Client
	// Warn receives capability warnings (e.g. namespaces ignored on a
	// non-enumerable registry, SPEC §8.3.2). Nil discards them.
	Warn io.Writer
}

func (d *Discoverer) warnf(format string, a ...any) {
	if d.Warn != nil {
		fmt.Fprintf(d.Warn, "warning: "+format+"\n", a...)
	}
}

// Probe auto-detects a registry's discovery mode (SPEC §8.1.1): catalog on a
// 2xx-parseable /v2/_catalog response, registered fallback otherwise. A forced
// discovery: value in the entry skips the probe and is authoritative.
func (d *Discoverer) Probe(ctx context.Context, entry config.Registry) string {
	if entry.Discovery == config.DiscoveryCatalog || entry.Discovery == config.DiscoveryRegistered {
		return entry.Discovery // forced mode, skip probe
	}
	if _, err := d.Client.Catalog(ctx, entry.URL); err != nil {
		return config.DiscoveryRegistered // 401/403/404/501/error ⇒ registered fallback
	}
	return config.DiscoveryCatalog
}

// Discover enumerates a registry's skills. In catalog mode it lists via
// /v2/_catalog and filters to skills by the config media-type discriminator; in
// registered mode it enumerates only the declared repositories (SPEC §8.1).
func (d *Discoverer) Discover(ctx context.Context, entry config.Registry) (*Result, error) {
	mode := d.Probe(ctx, entry)
	res := &Result{Mode: mode}

	switch mode {
	case config.DiscoveryCatalog:
		repos, err := d.Client.Catalog(ctx, entry.URL)
		if err != nil {
			// Lost catalog capability between probe and list: fall back.
			res.Mode = config.DiscoveryRegistered
			if len(entry.Namespaces) > 0 {
				d.warnf("registry %q: namespaces are ignored (registry is not enumerable); add explicit repositories", entry.Name)
			}
			res.Repos = dedup(entry.Repositories)
			return res, nil
		}
		for _, repo := range repos {
			// Namespaces, where the registry supports listing, act as prefix
			// filters (enumerated where supported, SPEC §8.3.2).
			if !underNamespaces(repo, entry.Namespaces) {
				continue
			}
			if d.isSkill(ctx, entry, repo) {
				res.Repos = append(res.Repos, repo)
			}
		}
		// Declared repositories are always merged in (guaranteed floor, §8.3.2).
		res.Repos = dedup(append(res.Repos, entry.Repositories...))
	default:
		// Registered: the declared repositories are the guaranteed-working floor;
		// namespaces cannot be enumerated on a non-catalog registry.
		if len(entry.Namespaces) > 0 {
			d.warnf("registry %q: namespaces are ignored (registry is not enumerable in registered mode); add explicit repositories", entry.Name)
		}
		res.Repos = dedup(entry.Repositories)
	}
	return res, nil
}

// underNamespaces reports whether repo falls under one of the namespace prefixes.
// An empty namespace list matches everything (no filtering).
func underNamespaces(repo string, namespaces []string) bool {
	if len(namespaces) == 0 {
		return true
	}
	for _, ns := range namespaces {
		if repo == ns || strings.HasPrefix(repo, strings.TrimSuffix(ns, "/")+"/") {
			return true
		}
	}
	return false
}

// dedup removes duplicate repo paths, preserving first-seen order.
func dedup(repos []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, r := range repos {
		if !seen[r] {
			seen[r] = true
			out = append(out, r)
		}
	}
	return out
}

// isSkill reports whether a repo's latest tag is an Epos skill, by inspecting
// the manifest's config media type (the skill/non-skill discriminator, §8.1).
func (d *Discoverer) isSkill(ctx context.Context, entry config.Registry, repo string) bool {
	ref := hostJoin(entry.URL, repo)
	tags, err := d.Client.Tags(ctx, ref)
	if err != nil || len(tags) == 0 {
		return false
	}
	man, err := d.Client.Pull(ctx, ref+":"+tags[len(tags)-1])
	if err != nil {
		return false
	}
	return domain.IsSkillConfigMediaType(man.Config.MediaType) ||
		man.ArtifactType == domain.MediaTypeSkillConfig
}

// hostJoin joins a registry URL and a repo path into a registry/repo reference.
func hostJoin(url, repo string) string {
	host := url
	for _, p := range []string{"https://", "http://"} {
		if len(host) > len(p) && host[:len(p)] == p {
			host = host[len(p):]
		}
	}
	return host + "/" + repo
}
