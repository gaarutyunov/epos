// Code scaffolded by sysgo; edit freely (not regenerated).

package gateway

import (
	"context"
	"strings"

	"github.com/gaarutyunov/epos/internal/infrastructure/app/port/out"
	"github.com/gaarutyunov/epos/internal/infrastructure/domain"
	"github.com/gaarutyunov/epos/internal/infrastructure/oci"
)

// OciClientImpl is the shared, domain-free OCI distribution adapter (SPEC §15.1),
// wrapping ORAS. Reused by the Packaging, Registry, Composition, and Signing
// adapters.
type OciClientImpl struct {
	client *oci.Client
}

var _ out.OciClient = (*OciClientImpl)(nil)

// NewOciClientImpl wraps an OCI client (nil ⇒ default).
func NewOciClientImpl(client *oci.Client) *OciClientImpl {
	if client == nil {
		client = &oci.Client{}
	}
	return &OciClientImpl{client: client}
}

// OciClient fetches the manifest bytes for endpoint/repo:reference (or @digest).
func (o *OciClientImpl) OciClient(endpoint domain.HTTPEndpoint, repo string, reference string) (string, error) {
	host := strings.TrimPrefix(strings.TrimPrefix(endpoint.URL, "https://"), "http://")
	host = strings.TrimRight(host, "/")
	sep := ":"
	if strings.HasPrefix(reference, "sha256:") {
		sep = "@"
	}
	man, err := o.client.Pull(context.Background(), host+"/"+repo+sep+reference)
	if err != nil {
		return "", err
	}
	return string(man.Raw), nil
}
