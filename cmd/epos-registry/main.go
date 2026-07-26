// Command epos-registry fronts an upstream OCI registry.
//
// It serves the read surface of SPEC.md 4.1, relays blobs with the 4.2 transfer
// posture, sets Epos-Version on every response (4.3), and counts downloads
// (5.1). The write path (4.5) arrives in a later milestone.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gaarutyunov/epos/internal/metrics"
	"github.com/gaarutyunov/epos/internal/upstream"
)

// Version is the semver reported in the Epos-Version header (SPEC.md 4.3).
// Overridden at release time via -ldflags.
var Version = "0.0.0-dev"

func main() {
	addr := flag.String("addr", ":8080", "address to listen on")
	upstreamURL := flag.String("upstream", "", "upstream registry base URL (required)")
	exporter := flag.String("metrics-exporter", metrics.ExporterStdout,
		"metrics exporter: stdout or none")
	interval := flag.Duration("metrics-interval", 0,
		"how often the metrics exporter emits (0 uses the SDK default)")
	versionAttribute := flag.Bool("metrics-version-attribute", false,
		"record the skill version on each download; off by default because "+
			"version-valued attributes are unbounded in cardinality")
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

	downloads, shutdownMetrics, err := metrics.New(context.Background(), metrics.Config{
		Exporter:         *exporter,
		Interval:         *interval,
		VersionAttribute: *versionAttribute,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "epos-registry:", err)
		os.Exit(2)
	}
	srv := &http.Server{
		Addr:              *addr,
		Handler:           newHandler(Version, up, downloads),
		ReadHeaderTimeout: 10 * time.Second,
	}

	// A signalled shutdown drains in-flight requests and then flushes the
	// counter, so the last interval's downloads are not lost on every deploy.
	// This has to be wired explicitly: ListenAndServe blocks until the server
	// stops, and os.Exit runs no deferred function.
	signalled, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-signalled.Done()
		ctx, cancel := context.WithTimeout(context.Background(), shutdownGrace)
		defer cancel()
		if err := srv.Shutdown(ctx); err != nil {
			fmt.Fprintln(os.Stderr, "epos-registry: shutdown:", err)
		}
	}()

	log.Printf("epos-registry %s listening on %s, fronting %s", Version, *addr, *upstreamURL)
	serveErr := srv.ListenAndServe()

	ctx, cancel := context.WithTimeout(context.Background(), shutdownGrace)
	defer cancel()
	if err := shutdownMetrics(ctx); err != nil {
		fmt.Fprintln(os.Stderr, "epos-registry: flush metrics:", err)
	}

	if serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
		fmt.Fprintln(os.Stderr, "epos-registry:", serveErr)
		os.Exit(1)
	}
}

// shutdownGrace bounds both the request drain and the final metric flush.
const shutdownGrace = 5 * time.Second
