// Code scaffolded by sysgo; edit freely (not regenerated).

package usecase

import (
	"errors"
	"github.com/gaarutyunov/epos/internal/install/app/port/in"
)

// ReadHistoryInteractor implements the ReadHistory use case. This scaffold is
// written once; add orchestration logic here. sysgo will not overwrite it.
type ReadHistoryInteractor struct{}

var _ in.ReadHistoryUseCase = (*ReadHistoryInteractor)(nil)

// NewReadHistoryInteractor constructs the interactor. Inject driven ports here.
func NewReadHistoryInteractor() *ReadHistoryInteractor {
	return &ReadHistoryInteractor{}
}

func (r *ReadHistoryInteractor) ReadHistory(input in.ReadHistoryInput) (in.ReadHistoryOutput, error) {
	return in.ReadHistoryOutput{}, errors.New("not implemented")
}
