// Code scaffolded by sysgo; edit freely (not regenerated).

package main

import (
	"log"

	gw "github.com/gaarutyunov/epos/internal/infrastructure/adapter/out/gateway"
	"github.com/gaarutyunov/epos/internal/infrastructure/oci"
)

// main is the composition root for the shared Infrastructure context: the
// generic OCI/git/kube clients reused by every other context's adapters.
func main() {
	ociAdapter := gw.NewOciClientImpl(&oci.Client{})
	gitAdapter := gw.NewGitClientImpl(nil)
	_ = ociAdapter
	_ = gitAdapter
	log.Println("Infrastructure: composition root wired (oci + git clients)")
}
