// Code scaffolded by sysgo; edit freely (not regenerated).

package main

import (
	"log"

	gw "github.com/gaarutyunov/epos/internal/composition/adapter/out/gateway"
	"github.com/gaarutyunov/epos/internal/composition/app/usecase"
	"github.com/gaarutyunov/epos/internal/composition/domain"
	"github.com/gaarutyunov/epos/internal/infrastructure/git"
	"github.com/gaarutyunov/epos/internal/infrastructure/oci"
)

// main is the composition root for the Composition bounded context.
func main() {
	layerSource := gw.NewLayerSourceImpl(&oci.Client{}, &git.Client{})
	compose := gw.NewCompositionPortImpl([]domain.StackLayer{}, false)

	capturePin := usecase.NewCaptureDependencyPinInteractor(layerSource)
	composeStack := usecase.NewComposeStackInteractor(compose)
	verifyPin := usecase.NewVerifyPinInteractor(layerSource)

	_ = capturePin
	_ = composeStack
	_ = verifyPin
	log.Println("Composition: composition root wired (layer-source + composition ports → interactors)")
}
