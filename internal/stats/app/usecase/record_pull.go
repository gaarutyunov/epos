// Code scaffolded by sysgo; edit freely (not regenerated).

package usecase

import (
	"github.com/gaarutyunov/epos/internal/stats/app/port/in"
	"github.com/gaarutyunov/epos/internal/stats/app/port/out"
)

// RecordPullInteractor implements the RecordPull use case: it records a counted
// pull event through the StatSink driven port (SPEC §10).
type RecordPullInteractor struct {
	sink out.StatSink
}

var _ in.RecordPullUseCase = (*RecordPullInteractor)(nil)

// NewRecordPullInteractor injects the StatSink driven port.
func NewRecordPullInteractor(sink out.StatSink) *RecordPullInteractor {
	return &RecordPullInteractor{sink: sink}
}

func (r *RecordPullInteractor) RecordPull(input in.RecordPullInput) (in.RecordPullOutput, error) {
	snap, err := r.sink.StatSink(input.Request)
	return in.RecordPullOutput{Snapshot: snap}, err
}
