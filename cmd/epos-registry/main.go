// Command epos-registry fronts an upstream OCI registry.
//
// This is the minimal buildable shim (SPEC.md 13.1): it serves the /v2/
// handshake and sets Epos-Version on every response. The rest of the read
// surface, the blob redirect posture and download counting arrive in later
// milestones.
package main

import (
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"
)

// Version is the semver reported in the Epos-Version header (SPEC.md 4.3).
// Overridden at release time via -ldflags.
var Version = "0.0.0-dev"

func main() {
	addr := flag.String("addr", ":8080", "address to listen on")
	flag.Parse()

	srv := &http.Server{
		Addr:              *addr,
		Handler:           newHandler(Version),
		ReadHeaderTimeout: 10 * time.Second,
	}

	log.Printf("epos-registry %s listening on %s", Version, *addr)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		fmt.Fprintln(os.Stderr, "epos-registry:", err)
		os.Exit(1)
	}
}
