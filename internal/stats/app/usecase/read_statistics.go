// Code scaffolded by sysgo; edit freely (not regenerated).

package usecase

import (
	"github.com/gaarutyunov/epos/internal/stats/app/port/in"
	"github.com/gaarutyunov/epos/internal/stats/app/port/out"
	"github.com/gaarutyunov/epos/internal/stats/domain"
)

// ReadStatisticsInteractor implements the ReadStatistics use case: it reads a
// skill's aggregate download total through the StatSink driven port.
type ReadStatisticsInteractor struct {
	sink out.StatSink
}

var _ in.ReadStatisticsUseCase = (*ReadStatisticsInteractor)(nil)

// NewReadStatisticsInteractor injects the StatSink driven port.
func NewReadStatisticsInteractor(sink out.StatSink) *ReadStatisticsInteractor {
	return &ReadStatisticsInteractor{sink: sink}
}

func (r *ReadStatisticsInteractor) ReadStatistics(input in.ReadStatisticsInput) (in.ReadStatisticsOutput, error) {
	snap, err := r.sink.StatSink(domain.CountRequest{Event: domain.PullEvent{Repo: input.Skill}})
	return in.ReadStatisticsOutput{Snapshot: snap}, err
}
