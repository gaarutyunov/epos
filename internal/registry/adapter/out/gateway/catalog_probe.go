// Code scaffolded by sysgo; edit freely (not regenerated).

package gateway

import (
	"context"
	"io"

	"github.com/gaarutyunov/epos/internal/config"
	"github.com/gaarutyunov/epos/internal/infrastructure/oci"
	"github.com/gaarutyunov/epos/internal/registry/app/port/out"
	"github.com/gaarutyunov/epos/internal/registry/discovery"
	"github.com/gaarutyunov/epos/internal/registry/domain"
)

// CatalogProbeImpl is the driven adapter implementing the CatalogProbe port:
// it auto-detects a registry's discovery mode and enumerates its skills
// (SPEC §8.1). It uses the shared OCI client.
type CatalogProbeImpl struct {
	client *oci.Client
	// Warn receives capability warnings (namespaces ignored, §8.3.2). Nil discards.
	Warn io.Writer
}

var _ out.CatalogProbe = (*CatalogProbeImpl)(nil)

// NewCatalogProbeImpl wraps an OCI listing client.
func NewCatalogProbeImpl(client *oci.Client) *CatalogProbeImpl {
	if client == nil {
		client = &oci.Client{}
	}
	return &CatalogProbeImpl{client: client}
}

// CatalogProbe probes and enumerates a registry, returning the detected mode.
func (c *CatalogProbeImpl) CatalogProbe(entry domain.RegistryEntry) (domain.CatalogResult, error) {
	reg := config.Registry{
		Name:         entry.Name,
		URL:          entry.URL,
		Discovery:    entry.Discovery.Value,
		Repositories: entry.Repositories,
		Namespaces:   entry.Namespaces,
	}
	d := &discovery.Discoverer{Client: c.client, Warn: c.Warn}
	res, err := d.Discover(context.Background(), reg)
	if err != nil {
		return domain.CatalogResult{}, err
	}
	return domain.CatalogResult{Mode: domain.DiscoveryMode{Value: res.Mode}, Repos: res.Repos}, nil
}
