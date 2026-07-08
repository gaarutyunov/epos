// Code scaffolded by sysgo; edit freely (not regenerated).

package main

import (
	"log"

	"github.com/gaarutyunov/epos/internal/frontend"
	gw "github.com/gaarutyunov/epos/internal/frontend/adapter/out/gateway"
	"github.com/gaarutyunov/epos/internal/frontend/app/usecase"
	"github.com/gaarutyunov/epos/internal/infrastructure/oci"
	"github.com/gaarutyunov/epos/internal/stats"
)

// main is the composition root for the Frontend bounded context.
func main() {
	feed := gw.NewCatalogFeedImpl(&frontend.Feed{Client: &oci.Client{}, Stats: stats.New()})
	list := usecase.NewListCatalogInteractor(feed)
	filter := usecase.NewFilterCatalogInteractor(feed)
	_ = list
	_ = filter
	log.Println("Frontend: composition root wired (catalog-feed port → interactors)")
}
