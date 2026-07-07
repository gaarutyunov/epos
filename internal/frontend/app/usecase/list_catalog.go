// Code scaffolded by sysgo; edit freely (not regenerated).

package usecase

import (
	"errors"
	"github.com/gaarutyunov/epos/internal/frontend/app/port/in"
)

// ListCatalogInteractor implements the ListCatalog use case. This scaffold is
// written once; add orchestration logic here. sysgo will not overwrite it.
type ListCatalogInteractor struct{}

var _ in.ListCatalogUseCase = (*ListCatalogInteractor)(nil)

// NewListCatalogInteractor constructs the interactor. Inject driven ports here.
func NewListCatalogInteractor() *ListCatalogInteractor {
	return &ListCatalogInteractor{}
}

func (l *ListCatalogInteractor) ListCatalog(input in.ListCatalogInput) (in.ListCatalogOutput, error) {
	return in.ListCatalogOutput{}, errors.New("not implemented")
}
