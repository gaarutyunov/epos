// Code scaffolded by sysgo; edit freely (not regenerated).

package usecase

import (
	"github.com/gaarutyunov/epos/internal/frontend/app/port/in"
	"github.com/gaarutyunov/epos/internal/frontend/app/port/out"
)

// FilterCatalogInteractor implements the FilterCatalog use case via the
// CatalogFeed driven port (SPEC §12.1).
type FilterCatalogInteractor struct {
	feed out.CatalogFeed
}

var _ in.FilterCatalogUseCase = (*FilterCatalogInteractor)(nil)

// NewFilterCatalogInteractor injects the CatalogFeed driven port.
func NewFilterCatalogInteractor(feed out.CatalogFeed) *FilterCatalogInteractor {
	return &FilterCatalogInteractor{feed: feed}
}

func (f *FilterCatalogInteractor) FilterCatalog(input in.FilterCatalogInput) (in.FilterCatalogOutput, error) {
	listing, err := f.feed.CatalogFeed(input.Request.Filter)
	return in.FilterCatalogOutput{Listing: listing}, err
}
