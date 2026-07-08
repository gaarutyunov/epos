// Code scaffolded by sysgo; edit freely (not regenerated).

package gateway

import (
	"strings"

	"github.com/gaarutyunov/epos/internal/stats"
	"github.com/gaarutyunov/epos/internal/stats/app/port/out"
	"github.com/gaarutyunov/epos/internal/stats/domain"
)

// StatSinkImpl is the driven adapter implementing the StatSink port over the
// in-memory aggregate + per-skill counter (the Prometheus-aggregate default,
// SPEC §10.1). A ClickHouse-backed sink is the large-catalog alternative.
type StatSinkImpl struct {
	counter *stats.Counter
}

var _ out.StatSink = (*StatSinkImpl)(nil)

// NewStatSinkImpl constructs the sink over a shared counter.
func NewStatSinkImpl(counter *stats.Counter) *StatSinkImpl {
	if counter == nil {
		counter = stats.New()
	}
	return &StatSinkImpl{counter: counter}
}

// StatSink records a counted pull (only manifest GETs are counted, SPEC §6.4)
// and returns the skill's current total.
func (s *StatSinkImpl) StatSink(request domain.CountRequest) (domain.CountSnapshot, error) {
	ev := request.Event
	skill := lastSegment(ev.Repo)
	if ev.IsManifestGet {
		s.counter.CountManifestGet("", ev.Repo, false)
	}
	return domain.CountSnapshot{Skill: skill, Total: int64(s.counter.Skill(skill))}, nil
}

func lastSegment(repo string) string {
	if i := strings.LastIndex(repo, "/"); i >= 0 {
		return repo[i+1:]
	}
	return repo
}
