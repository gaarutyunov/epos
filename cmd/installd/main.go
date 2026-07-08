// Code scaffolded by sysgo; edit freely (not regenerated).

package main

import (
	"log"

	"github.com/gaarutyunov/epos/internal/infrastructure/oci"
	gw "github.com/gaarutyunov/epos/internal/install/adapter/out/gateway"
	"github.com/gaarutyunov/epos/internal/install/app/usecase"
)

// main is the composition root for the Install bounded context: it wires the
// concrete driven adapters (MaterializePort over files/ConfigMaps, RevisionStore
// over the lockfile/in-cluster records) into the use-case interactors.
func main() {
	workDir := "."
	ociClient := &oci.Client{}

	materializer := gw.NewMaterializePortImpl(workDir, ociClient, nil)
	revisions := gw.NewRevisionStoreImpl(workDir, nil)

	install := usecase.NewInstallSkillInteractor(materializer, revisions)
	upgrade := usecase.NewUpgradeSkillInteractor(materializer, revisions)
	rollback := usecase.NewRollbackSkillInteractor(materializer, revisions, "files", "")
	uninstall := usecase.NewUninstallSkillInteractor(materializer, revisions, "files", "")
	history := usecase.NewReadHistoryInteractor(revisions, "files", "")

	_ = install
	_ = upgrade
	_ = rollback
	_ = uninstall
	_ = history
	log.Println("Install: composition root wired (materialize + revision-store → interactors)")
}
