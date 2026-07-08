// Code scaffolded by sysgo; edit freely (not regenerated).

package main

import (
	"log"

	gw "github.com/gaarutyunov/epos/internal/packaging/adapter/out/gateway"
	"github.com/gaarutyunov/epos/internal/packaging/app/usecase"
	"github.com/gaarutyunov/epos/internal/packaging/domain"
)

// main is the composition root for the Packaging bounded context.
func main() {
	packaging := gw.NewPackagingPortImpl(".")
	validation := gw.NewValidationPortImpl()

	pkg := usecase.NewPackageSkillInteractor(packaging)
	val := usecase.NewValidateSkillInteractor(validation)
	push := usecase.NewPushSkillInteractor(func(domain.OciRef, domain.SkillArtifact) (domain.PackagedArtifact, error) {
		return domain.PackagedArtifact{}, nil
	})

	_ = pkg
	_ = val
	_ = push
	log.Println("Packaging: composition root wired (packaging + validation ports → interactors)")
}
