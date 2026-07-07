// Code scaffolded by sysgo; edit freely (not regenerated).

package usecase

import (
	"errors"
	"github.com/gaarutyunov/epos/internal/stats/app/port/in"
)

// RecordPullInteractor implements the RecordPull use case. This scaffold is
// written once; add orchestration logic here. sysgo will not overwrite it.
type RecordPullInteractor struct{}

var _ in.RecordPullUseCase = (*RecordPullInteractor)(nil)

// NewRecordPullInteractor constructs the interactor. Inject driven ports here.
func NewRecordPullInteractor() *RecordPullInteractor {
	return &RecordPullInteractor{}
}

func (r *RecordPullInteractor) RecordPull(input in.RecordPullInput) (in.RecordPullOutput, error) {
	return in.RecordPullOutput{}, errors.New("not implemented")
}
