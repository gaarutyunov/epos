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

	"github.com/gaarutyunov/epos/internal/upstream"
)

// Version is the semver reported in the Epos-Version header (SPEC.md 4.3).
// Overridden at release time via -ldflags.
var Version = "0.0.0-dev"

func main() {
	addr := flag.String("addr", ":8080", "address to listen on")
	upstreamURL := flag.String("upstream", "", "upstream registry base URL (required)")
	flag.Parse()

	if *upstreamURL == "" {
		fmt.Fprintln(os.Stderr, "epos-registry: -upstream is required")
		os.Exit(2)
	}

	up, err := upstream.New(*upstreamURL)
	if err != nil {
		fmt.Fprintln(os.Stderr, "epos-registry:", err)
		os.Exit(2)
	}

	srv := &http.Server{
		Addr:              *addr,
		Handler:           newHandler(Version, up),
		ReadHeaderTimeout: 10 * time.Second,
	}

	log.Printf("epos-registry %s listening on %s, fronting %s", Version, *addr, *upstreamURL)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		fmt.Fprintln(os.Stderr, "epos-registry:", err)
		os.Exit(1)
	}
}
