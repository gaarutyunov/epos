// Package stats counts skill pulls. The proxy is the interception point; only
// manifest GETs are counted (HEAD and blob GETs excluded), following Docker's
// convention, with index-vs-image discrimination to avoid multi-arch double
// counting (SPEC §6.4, §10.2).
package stats

import (
	"strings"
	"sync"
)

// Counter is the default in-memory aggregate + per-skill counter. It backs the
// Prometheus aggregate export; a ClickHouse sink is the large-catalog option
// (SPEC §10.1).
type Counter struct {
	mu          sync.Mutex
	total       int
	perRegistry map[string]int
	perSkill    map[string]int
	errors      int
}

// New returns an empty counter.
func New() *Counter {
	return &Counter{perRegistry: map[string]int{}, perSkill: map[string]int{}}
}

// CountManifestGet records a countable pull event for repo (registry/name).
// isIndex distinguishes an index manifest (do not count as a pull) from an image
// manifest, avoiding multi-arch double counting (SPEC §10.2).
func (c *Counter) CountManifestGet(registry, repo string, isIndex bool) {
	if isIndex {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.total++
	if registry != "" {
		c.perRegistry[registry]++
	}
	c.perSkill[skillName(repo)]++
}

// CountError records a proxied error (aggregate error rate).
func (c *Counter) CountError() {
	c.mu.Lock()
	c.errors++
	c.mu.Unlock()
}

// Total returns the aggregate pull count.
func (c *Counter) Total() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.total
}

// Skill returns the pull count for a skill (per-skill export, SPEC §10.1).
func (c *Counter) Skill(name string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.perSkill[name]
}

// Registry returns the pull count for a registry.
func (c *Counter) Registry(name string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.perRegistry[name]
}

// skillName reduces a repo path to its skill name (last path segment).
func skillName(repo string) string {
	if i := strings.LastIndex(repo, "/"); i >= 0 {
		return repo[i+1:]
	}
	return repo
}
