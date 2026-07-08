// Code scaffolded by sysgo; edit freely (not regenerated).

package gateway

import (
	"github.com/gaarutyunov/epos/internal/frontend/app/port/out"
	"github.com/gaarutyunov/epos/internal/frontend/domain"
)

// FrontendPortImpl implements the Frontend boundary port by delegating to the
// CatalogFeed (SPEC §12).
type FrontendPortImpl struct {
	feed out.CatalogFeed
}

var _ out.FrontendPort = (*FrontendPortImpl)(nil)

// NewFrontendPortImpl injects the CatalogFeed driven port.
func NewFrontendPortImpl(feed out.CatalogFeed) *FrontendPortImpl {
	return &FrontendPortImpl{feed: feed}
}

// Frontend returns the filtered federated listing for a request.
func (f *FrontendPortImpl) Frontend(request domain.ListingRequest) (domain.Listing, error) {
	return f.feed.CatalogFeed(request.Filter)
}
