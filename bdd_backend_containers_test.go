//go:build integration

package epos_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"

	"github.com/testcontainers/testcontainers-go"
	tcexec "github.com/testcontainers/testcontainers-go/exec"
	"github.com/testcontainers/testcontainers-go/modules/k3s"
	"github.com/testcontainers/testcontainers-go/wait"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/gaarutyunov/epos/internal/infrastructure/kube"
)

// This backend runs the journeys as end-to-end INTEGRATION tests against real
// dependencies started via testcontainers-go (SPEC §15.3), with no mocks:
//   - zot   — OCI registry (native /v2/_catalog, cosign/referrers, auth)
//   - Gitea — real HTTP git transport for git-dependency resolution
//   - k3s   — a real Kubernetes cluster for the ConfigMap install/rollback journeys
//
// Build with `-tags=integration` (the runner requires a Docker daemon, which the
// CI runner provides, SPEC §15.6).

// ---- OCI registry: zot (fresh per scenario for isolation) ----

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
		Cmd: []string{"serve", "/etc/zot/config.json"},
		WaitingFor: wait.ForHTTP("/v2/").WithPort("5000/tcp").
			WithStatusCodeMatcher(func(s int) bool { return s == 200 || s == 401 }),
	}
	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{ContainerRequest: req, Started: true})
	if err != nil {
		panic(fmt.Sprintf("start zot: %v", err))
	}
	h, _ := c.Host(ctx)
	p, _ := c.MappedPort(ctx, "5000")
	return fmt.Sprintf("%s:%s", h, p.Port()), func() { _ = c.Terminate(ctx) }
}

func zotConfig() io.Reader {
	return strings.NewReader(`{
  "storage": { "rootDirectory": "/var/lib/registry" },
  "http": { "address": "0.0.0.0", "port": "5000" },
  "log": { "level": "error" }
}`)
}

// ---- cluster: k3s (started once, reused across scenarios) ----

var (
	k3sOnce      sync.Once
	k3sClient    kubernetes.Interface
	k3sContainer *k3s.K3sContainer
	k3sErr       error
)

func newKubeClient(_ *world) *kube.Client {
	k3sOnce.Do(startK3s)
	if k3sErr != nil {
		panic(k3sErr)
	}
	ensureNamespaces(k3sClient, "skills")
	return kube.NewFromInterface(k3sClient)
}

// withExposedPorts is a testcontainers customizer that publishes additional
// container ports — used to surface the in-cluster NodePorts of the Postgres and
// ClickHouse services (SPEC §11, §16) to the host test process.
func withExposedPorts(ports ...string) testcontainers.CustomizeRequestOption {
	return func(req *testcontainers.GenericContainerRequest) error {
		req.ExposedPorts = append(req.ExposedPorts, ports...)
		return nil
	}
}

func startK3s() {
	ctx := context.Background()
	container, err := k3s.Run(ctx, "rancher/k3s:v1.31.2-k3s1",
		withExposedPorts(pgNodePortSpec, chNodePortSpec))
	if err != nil {
		k3sErr = fmt.Errorf("start k3s: %w", err)
		return
	}
	k3sContainer = container
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

// ---- git server: Gitea (started once, reused across scenarios) ----

const (
	giteaUser = "tester"
	giteaPass = "tester-password-123"
)

var (
	giteaOnce    sync.Once
	giteaBaseURL string // http://host:port
	giteaErr     error
	giteaMu      sync.Mutex
	giteaRepos   = map[string]bool{}
)

func startGitea() {
	ctx := context.Background()
	req := testcontainers.ContainerRequest{
		Image:        "gitea/gitea:1.22",
		ExposedPorts: []string{"3000/tcp"},
		Env: map[string]string{
			"GITEA__database__DB_TYPE":             "sqlite3",
			"GITEA__security__INSTALL_LOCK":        "true",
			"GITEA__server__DISABLE_SSH":           "true",
			"GITEA__service__DISABLE_REGISTRATION": "true",
			"GITEA__log__LEVEL":                    "error",
		},
		WaitingFor: wait.ForHTTP("/api/v1/version").WithPort("3000/tcp").
			WithStatusCodeMatcher(func(s int) bool { return s == 200 }),
	}
	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{ContainerRequest: req, Started: true})
	if err != nil {
		giteaErr = fmt.Errorf("start gitea: %w", err)
		return
	}
	// Create the admin user via the Gitea CLI (runs as the git user).
	_, _, err = c.Exec(ctx, []string{
		"gitea", "admin", "user", "create",
		"--username", giteaUser, "--password", giteaPass,
		"--email", "tester@example.com", "--admin", "--must-change-password=false",
	}, tcexec.WithUser("git"))
	if err != nil {
		giteaErr = fmt.Errorf("gitea create admin: %w", err)
		return
	}
	h, _ := c.Host(ctx)
	p, _ := c.MappedPort(ctx, "3000")
	giteaBaseURL = fmt.Sprintf("http://%s:%s", h, p.Port())
}

// gitCloneURL creates a public Gitea repo (once per name), pushes localRepoPath
// to it over HTTP, and returns the public clone URL. git-dependency resolution
// then clones it over real HTTP transport (SPEC §15.3).
func gitCloneURL(localRepoPath, name string) (string, error) {
	giteaOnce.Do(startGitea)
	if giteaErr != nil {
		return "", giteaErr
	}

	giteaMu.Lock()
	created := giteaRepos[name]
	giteaRepos[name] = true
	giteaMu.Unlock()

	if !created {
		if err := createGiteaRepo(name); err != nil {
			return "", err
		}
	}

	// Authenticated push URL; the returned clone URL is public (no creds).
	base := strings.TrimPrefix(giteaBaseURL, "http://")
	pushURL := fmt.Sprintf("http://%s:%s@%s/%s/%s.git", giteaUser, giteaPass, base, giteaUser, name)
	if out, err := gitRun(localRepoPath, "push", pushURL, "+refs/heads/*:refs/heads/*", "+refs/tags/*:refs/tags/*"); err != nil {
		return "", fmt.Errorf("git push to gitea: %w: %s", err, out)
	}
	return fmt.Sprintf("%s/%s/%s.git", giteaBaseURL, giteaUser, name), nil
}

func createGiteaRepo(name string) error {
	body, _ := json.Marshal(map[string]any{"name": name, "private": false, "auto_init": false})
	req, _ := http.NewRequest(http.MethodPost, giteaBaseURL+"/api/v1/user/repos", bytes.NewReader(body))
	req.SetBasicAuth(giteaUser, giteaPass)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 && resp.StatusCode != http.StatusConflict {
		msg, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("create gitea repo %q: status %d: %s", name, resp.StatusCode, msg)
	}
	return nil
}

func gitRun(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_TERMINAL_PROMPT=0")
	out, err := cmd.CombinedOutput()
	return string(out), err
}
