// Code scaffolded by sysgo; edit freely (not regenerated).

package usecase

import (
	"errors"
	"github.com/gaarutyunov/epos/internal/stats/app/port/in"
)

// ReadStatisticsInteractor implements the ReadStatistics use case. This scaffold is
// written once; add orchestration logic here. sysgo will not overwrite it.
type ReadStatisticsInteractor struct{}

var _ in.ReadStatisticsUseCase = (*ReadStatisticsInteractor)(nil)

// NewReadStatisticsInteractor constructs the interactor. Inject driven ports here.
func NewReadStatisticsInteractor() *ReadStatisticsInteractor {
	return &ReadStatisticsInteractor{}
}

func (r *ReadStatisticsInteractor) ReadStatistics(input in.ReadStatisticsInput) (in.ReadStatisticsOutput, error) {
	return in.ReadStatisticsOutput{}, errors.New("not implemented")
}
