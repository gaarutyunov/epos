package stats

import (
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
)

// WriteProm writes the counter as Prometheus text-format exposition (SPEC §10.1).
// Aggregate series are always emitted (total, per-registry, errors); per-skill
// series are emitted only when perSkill is enabled, since they are high
// cardinality and safe only for small catalogs (SPEC §10.1).
func (c *Counter) WriteProm(w io.StringWriter, perSkill bool) {
	c.mu.Lock()
	total := c.total
	errs := c.errors
	perReg := make(map[string]int, len(c.perRegistry))
	for k, v := range c.perRegistry {
		perReg[k] = v
	}
	perSk := make(map[string]int, len(c.perSkill))
	for k, v := range c.perSkill {
		perSk[k] = v
	}
	c.mu.Unlock()

	writeCounterHeader(w, "epos_pulls_total", "Total countable skill manifest pulls.")
	_, _ = w.WriteString(fmt.Sprintf("epos_pulls_total %d\n", total))

	writeCounterHeader(w, "epos_pull_errors_total", "Total proxied errors.")
	_, _ = w.WriteString(fmt.Sprintf("epos_pull_errors_total %d\n", errs))

	writeCounterHeader(w, "epos_pulls_by_registry_total", "Countable pulls per registry.")
	for _, k := range sortedKeys(perReg) {
		_, _ = w.WriteString(fmt.Sprintf("epos_pulls_by_registry_total{registry=%q} %d\n", k, perReg[k]))
	}

	if perSkill {
		writeCounterHeader(w, "epos_pulls_by_skill_total", "Countable pulls per skill (small catalogs only).")
		for _, k := range sortedKeys(perSk) {
			_, _ = w.WriteString(fmt.Sprintf("epos_pulls_by_skill_total{skill=%q} %d\n", k, perSk[k]))
		}
	}
}

func writeCounterHeader(w io.StringWriter, name, help string) {
	_, _ = w.WriteString("# HELP " + name + " " + help + "\n")
	_, _ = w.WriteString("# TYPE " + name + " counter\n")
}

func sortedKeys(m map[string]int) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// MetricsHandler returns an http.Handler exposing the counter at /metrics in
// Prometheus text format (SPEC §10.1). perSkill enables per-skill series.
func MetricsHandler(c *Counter, perSkill bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		var b strings.Builder
		c.WriteProm(&b, perSkill)
		_, _ = w.Write([]byte(b.String()))
	})
}
