// Code scaffolded by sysgo; edit freely (not regenerated).

package usecase

import (
	"github.com/gaarutyunov/epos/internal/frontend/app/port/in"
	"github.com/gaarutyunov/epos/internal/frontend/app/port/out"
)

// ListCatalogInteractor implements the ListCatalog use case via the CatalogFeed
// driven port (SPEC §12).
type ListCatalogInteractor struct {
	feed out.CatalogFeed
}

var _ in.ListCatalogUseCase = (*ListCatalogInteractor)(nil)

// NewListCatalogInteractor injects the CatalogFeed driven port.
func NewListCatalogInteractor(feed out.CatalogFeed) *ListCatalogInteractor {
	return &ListCatalogInteractor{feed: feed}
}

func (l *ListCatalogInteractor) ListCatalog(input in.ListCatalogInput) (in.ListCatalogOutput, error) {
	listing, err := l.feed.CatalogFeed(input.Request.Filter)
	return in.ListCatalogOutput{Listing: listing}, err
}
