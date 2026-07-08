// Code scaffolded by sysgo; edit freely (not regenerated).

package gateway

import (
	"context"

	"github.com/gaarutyunov/epos/internal/frontend"
	"github.com/gaarutyunov/epos/internal/frontend/app/port/out"
	"github.com/gaarutyunov/epos/internal/frontend/domain"
)

// CatalogFeedImpl is the driven adapter implementing the CatalogFeed port: it
// gathers a federated skill listing across registries (discovery + stats) and
// filters it (SPEC §12.2).
type CatalogFeedImpl struct {
	feed *frontend.Feed
}

var _ out.CatalogFeed = (*CatalogFeedImpl)(nil)

// NewCatalogFeedImpl wraps a federated feed.
func NewCatalogFeedImpl(feed *frontend.Feed) *CatalogFeedImpl {
	return &CatalogFeedImpl{feed: feed}
}

// CatalogFeed gathers and filters the federated listing.
func (c *CatalogFeedImpl) CatalogFeed(filter domain.Filter) (domain.Listing, error) {
	cat, err := c.feed.Gather(context.Background())
	if err != nil {
		return domain.Listing{}, err
	}
	cards := cat.Filter(frontend.Filter{Keyword: filter.Keyword, Registry: filter.Registry})
	return toDomainListing(cards), nil
}

func toDomainListing(cards []frontend.SkillCard) domain.Listing {
	out := domain.Listing{}
	for _, c := range cards {
		out.Cards = append(out.Cards, domain.SkillCard{
			Name:        c.Name,
			Version:     c.Version,
			Description: c.Description,
			Registry:    c.Registry,
			Downloads:   int64(c.Downloads),
		})
	}
	return out
}
