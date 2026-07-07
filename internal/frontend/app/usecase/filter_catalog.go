// Code scaffolded by sysgo; edit freely (not regenerated).

package usecase

import (
	"errors"
	"github.com/gaarutyunov/epos/internal/frontend/app/port/in"
)

// FilterCatalogInteractor implements the FilterCatalog use case. This scaffold is
// written once; add orchestration logic here. sysgo will not overwrite it.
type FilterCatalogInteractor struct{}

var _ in.FilterCatalogUseCase = (*FilterCatalogInteractor)(nil)

// NewFilterCatalogInteractor constructs the interactor. Inject driven ports here.
func NewFilterCatalogInteractor() *FilterCatalogInteractor {
	return &FilterCatalogInteractor{}
}

func (f *FilterCatalogInteractor) FilterCatalog(input in.FilterCatalogInput) (in.FilterCatalogOutput, error) {
	return in.FilterCatalogOutput{}, errors.New("not implemented")
}
