// Code scaffolded by sysgo; edit freely (not regenerated).

package main

import (
	"log"

	"github.com/gaarutyunov/epos/internal/infrastructure/oci"
	gw "github.com/gaarutyunov/epos/internal/registry/adapter/out/gateway"
	"github.com/gaarutyunov/epos/internal/registry/app/usecase"
	"github.com/gaarutyunov/epos/internal/registry/domain"
	"github.com/gaarutyunov/epos/internal/stats"
)

// main is the composition root for the Registry bounded context.
func main() {
	client := &oci.Client{}
	probe := gw.NewCatalogProbeImpl(client)
	proxy := gw.NewProxyPortImpl("", client, stats.New())
	store := gw.NewRegistrationStoreImpl()

	detect := usecase.NewDetectDiscoveryInteractor(probe)
	proxyManifest := usecase.NewProxyManifestInteractor(proxy)
	register := usecase.NewRegisterRegistryInteractor(store)
	list := usecase.NewListSkillsInteractor(probe, []domain.RegistryEntry{})

	_ = detect
	_ = proxyManifest
	_ = register
	_ = list
	log.Println("Registry: composition root wired (catalog-probe + proxy + registration-store → interactors)")
}
