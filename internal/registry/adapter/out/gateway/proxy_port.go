// Code scaffolded by sysgo; edit freely (not regenerated).

package gateway

import (
	"context"
	"strings"

	"github.com/gaarutyunov/epos/internal/infrastructure/oci"
	"github.com/gaarutyunov/epos/internal/registry/app/port/out"
	"github.com/gaarutyunov/epos/internal/registry/domain"
	"github.com/gaarutyunov/epos/internal/stats"
)

// ProxyPortImpl is the driven adapter implementing the ProxyPort boundary as a
// transparent pass-through to an upstream registry (SPEC §6). It stores no
// secrets and counts manifest GETs (SPEC §6.4).
type ProxyPortImpl struct {
	upstream string
	client   *oci.Client
	counter  *stats.Counter
}

var _ out.ProxyPort = (*ProxyPortImpl)(nil)

// NewProxyPortImpl wraps an upstream URL, OCI client, and stats counter.
func NewProxyPortImpl(upstream string, client *oci.Client, counter *stats.Counter) *ProxyPortImpl {
	if client == nil {
		client = &oci.Client{}
	}
	return &ProxyPortImpl{upstream: upstream, client: client, counter: counter}
}

// Proxy relays a manifest request to the upstream, counting a manifest GET.
func (p *ProxyPortImpl) Proxy(request domain.ProxyRequest) (domain.ProxyResponse, error) {
	host := strings.TrimRight(strings.TrimPrefix(strings.TrimPrefix(p.upstream, "https://"), "http://"), "/")
	sep := ":"
	if strings.HasPrefix(request.Reference, "sha256:") {
		sep = "@"
	}
	man, err := p.client.Pull(context.Background(), host+"/"+request.Repo+sep+request.Reference)
	if err != nil {
		return domain.ProxyResponse{Status: 404, Body: err.Error()}, nil
	}
	if strings.EqualFold(request.Method, "GET") && p.counter != nil {
		p.counter.CountManifestGet(host, request.Repo, false)
	}
	return domain.ProxyResponse{Status: 200, Body: string(man.Raw)}, nil
}
