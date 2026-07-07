//go:build integration

package epos_test

import (
	"context"
	"fmt"
	"io"
	"strings"
	"sync"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/k3s"
	"github.com/testcontainers/testcontainers-go/wait"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/gaarutyunov/epos/internal/infrastructure/kube"
)

// This backend runs the journeys against REAL dependencies via testcontainers-go
// (SPEC §15.3): zot for the OCI registry (native /v2/_catalog, cosign/referrers,
// auth) and k3s for the cluster. Git uses real local repositories (the git
// binary), shared with the in-process backend. Build with `-tags=integration`.

// startRegistry starts a fresh zot container per scenario (isolation) and
// returns its host:port. zot is OCI-native and implements _catalog and the
// referrers API required by the discovery and signing journeys.
func startRegistry() (host string, cleanup func()) {
	ctx := context.Background()
	req := testcontainers.ContainerRequest{
		Image:        "ghcr.io/project-zot/zot-linux-amd64:latest",
		ExposedPorts: []string{"5000/tcp"},
		Files: []testcontainers.ContainerFile{{
			Reader:            zotConfig(),
			ContainerFilePath: "/etc/zot/config.json",
			FileMode:          0o644,
		}},
		Cmd:        []string{"serve", "/etc/zot/config.json"},
		WaitingFor: wait.ForHTTP("/v2/").WithPort("5000/tcp").WithStatusCodeMatcher(func(s int) bool { return s == 200 || s == 401 }),
	}
	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{ContainerRequest: req, Started: true})
	if err != nil {
		panic(fmt.Sprintf("start zot: %v", err))
	}
	h, _ := c.Host(ctx)
	p, _ := c.MappedPort(ctx, "5000")
	return fmt.Sprintf("%s:%s", h, p.Port()), func() { _ = c.Terminate(ctx) }
}

var (
	k3sOnce   sync.Once
	k3sClient kubernetes.Interface
	k3sErr    error
)

// newKubeClient returns a client for a shared k3s cluster (started once), with a
// fresh set of scenario namespaces ensured to exist.
func newKubeClient(_ *world) *kube.Client {
	k3sOnce.Do(startK3s)
	if k3sErr != nil {
		panic(k3sErr)
	}
	ensureNamespaces(k3sClient, "skills")
	return kube.NewFromInterface(k3sClient)
}

func startK3s() {
	ctx := context.Background()
	container, err := k3s.Run(ctx, "rancher/k3s:v1.31.2-k3s1")
	if err != nil {
		k3sErr = fmt.Errorf("start k3s: %w", err)
		return
	}
	kubeconfig, err := container.GetKubeConfig(ctx)
	if err != nil {
		k3sErr = fmt.Errorf("k3s kubeconfig: %w", err)
		return
	}
	cfg, err := clientcmd.RESTConfigFromKubeConfig(kubeconfig)
	if err != nil {
		k3sErr = err
		return
	}
	k3sClient, k3sErr = kubernetes.NewForConfig(cfg)
}

func ensureNamespaces(cs kubernetes.Interface, names ...string) {
	ctx := context.Background()
	for _, n := range names {
		_, _ = cs.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: n}}, metav1.CreateOptions{})
	}
}

// zotConfig returns a minimal zot configuration: filesystem storage, plain HTTP,
// no auth. zot natively serves /v2/_catalog and the OCI referrers API.
func zotConfig() io.Reader {
	return strings.NewReader(`{
  "storage": { "rootDirectory": "/var/lib/registry" },
  "http": { "address": "0.0.0.0", "port": "5000" },
  "log": { "level": "error" }
}`)
}
