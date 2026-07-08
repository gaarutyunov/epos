// Code scaffolded by sysgo; edit freely (not regenerated).

package main

import (
	"log"

	"github.com/gaarutyunov/epos/internal/infrastructure/oci"
	gw "github.com/gaarutyunov/epos/internal/signing/adapter/out/repository"
	"github.com/gaarutyunov/epos/internal/signing/app/usecase"
)

// main is the composition root for the Signing bounded context.
func main() {
	signature := gw.NewSignaturePortImpl(&oci.Client{}, "")
	verify := usecase.NewVerifySignatureInteractor(signature)
	_ = verify
	log.Println("Signing: composition root wired (signature port → verify interactor)")
}
