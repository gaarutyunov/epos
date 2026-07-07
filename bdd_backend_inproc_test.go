//go:build !integration

package epos_test

import (
	"net/http/httptest"
	"net/url"

	"github.com/google/go-containerregistry/pkg/registry"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/gaarutyunov/epos/internal/infrastructure/kube"
)

// startRegistry starts a pure-Go in-process OCI registry (a genuine registry
// implementing /v2/, _catalog, tags, and referrers — no docker). CI swaps this
// for a real zot container via the `containers` build tag (SPEC §15.3).
func startRegistry() (host string, cleanup func()) {
	srv := httptest.NewServer(registry.New())
	u, _ := url.Parse(srv.URL)
	return u.Host, srv.Close
}

// newKubeClient wires a fake Kubernetes clientset so the ConfigMap install
// target runs without a cluster. CI uses a real k3s cluster (SPEC §15.3).
func newKubeClient(_ *world) *kube.Client {
	return kube.NewFromInterface(fake.NewSimpleClientset())
}
