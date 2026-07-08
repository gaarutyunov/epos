//go:build integration

package epos_test

import (
	"context"
	"database/sql"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/docker/go-connections/nat"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/kubernetes"

	instgw "github.com/gaarutyunov/epos/internal/install/adapter/out/gateway"
	instout "github.com/gaarutyunov/epos/internal/install/app/port/out"
	reggw "github.com/gaarutyunov/epos/internal/registry/adapter/out/gateway"
	regdomain "github.com/gaarutyunov/epos/internal/registry/domain"
	statsgw "github.com/gaarutyunov/epos/internal/stats/adapter/out/gateway"
	statsdomain "github.com/gaarutyunov/epos/internal/stats/domain"
)

// This file runs the pluggable durable-state backends (SPEC §11) as end-to-end
// INTEGRATION tests against REAL database instances — with no mocks and no
// in-memory doubles:
//   - PostgreSQL — the registration-index and revision-history backends (§8.2,
//     §5.4, §11)
//   - ClickHouse — the large-catalog download-statistics sink (§10.1)
//
// Both databases run as REAL workloads INSIDE the k3s cluster that the container
// backend already starts via testcontainers-go (SPEC §15.3, §16: in-cluster or
// external managed Postgres/ClickHouse). Each is exposed to the host test
// process through a fixed NodePort published on the k3s container, so the
// adapters are exercised over a genuine network connection to a genuine server.
//
// Build with `-tags=integration` (the runner requires a Docker daemon).

// Fixed NodePorts (the 30000–32767 range) published on the k3s container.
const (
	pgNodePort     = 30432
	chNodePort     = 30900
	pgNodePortSpec = "30432/tcp"
	chNodePortSpec = "30900/tcp"
)

// ---- Postgres in k3s (deployed once, reused across the store tests) ----

var (
	pgOnce sync.Once
	pgDSN  string
	pgErr  error
)

func postgresDSN(t *testing.T) string {
	t.Helper()
	k3sOnce.Do(startK3s)
	if k3sErr != nil {
		t.Fatalf("start k3s: %v", k3sErr)
	}
	pgOnce.Do(func() { pgDSN, pgErr = deployPostgres() })
	if pgErr != nil {
		t.Fatalf("deploy postgres: %v", pgErr)
	}
	return pgDSN
}

func deployPostgres() (string, error) {
	ctx := context.Background()
	const (
		name = "epos-postgres"
		user = "epos"
		pass = "epos-password-123"
		db   = "epos"
	)
	deployment := dbDeployment(name, "postgres:16-alpine", 5432, []corev1.EnvVar{
		{Name: "POSTGRES_USER", Value: user},
		{Name: "POSTGRES_PASSWORD", Value: pass},
		{Name: "POSTGRES_DB", Value: db},
		// Keep PGDATA off the read-only image layers.
		{Name: "PGDATA", Value: "/var/lib/postgresql/data/pgdata"},
	})
	if err := applyDeploymentAndService(ctx, k3sClient, deployment, name, 5432, pgNodePort); err != nil {
		return "", err
	}
	host, port, err := nodePortEndpoint(ctx, pgNodePortSpec)
	if err != nil {
		return "", err
	}
	dsn := fmt.Sprintf("postgres://%s:%s@%s:%s/%s?sslmode=disable", user, pass, host, port, db)
	if err := waitForDB(ctx, "pgx", dsn, 4*time.Minute); err != nil {
		return "", fmt.Errorf("postgres not ready: %w", err)
	}
	return dsn, nil
}

// ---- ClickHouse in k3s (deployed once, reused across the store tests) ----

var (
	chOnce sync.Once
	chDSN  string
	chErr  error
)

func clickhouseDSN(t *testing.T) string {
	t.Helper()
	k3sOnce.Do(startK3s)
	if k3sErr != nil {
		t.Fatalf("start k3s: %v", k3sErr)
	}
	chOnce.Do(func() { chDSN, chErr = deployClickHouse() })
	if chErr != nil {
		t.Fatalf("deploy clickhouse: %v", chErr)
	}
	return chDSN
}

func deployClickHouse() (string, error) {
	ctx := context.Background()
	const name = "epos-clickhouse"
	deployment := dbDeployment(name, "clickhouse/clickhouse-server:24.8-alpine", 9000, []corev1.EnvVar{
		{Name: "CLICKHOUSE_DB", Value: "epos"},
		{Name: "CLICKHOUSE_USER", Value: "epos"},
		{Name: "CLICKHOUSE_PASSWORD", Value: "epos-password-123"},
		{Name: "CLICKHOUSE_DEFAULT_ACCESS_MANAGEMENT", Value: "1"},
	})
	if err := applyDeploymentAndService(ctx, k3sClient, deployment, name, 9000, chNodePort); err != nil {
		return "", err
	}
	host, port, err := nodePortEndpoint(ctx, chNodePortSpec)
	if err != nil {
		return "", err
	}
	dsn := fmt.Sprintf("clickhouse://epos:epos-password-123@%s:%s/epos", host, port)
	// Open through the same seam the adapter uses, so readiness is measured over
	// the ClickHouse native protocol rather than a raw TCP dial.
	if err := waitForClickHouse(ctx, dsn, 4*time.Minute); err != nil {
		return "", fmt.Errorf("clickhouse not ready: %w", err)
	}
	return dsn, nil
}

// ---- tests: PostgreSQL registration-index backend (SPEC §8.2, §11) ----

func TestPostgresRegistrationStore(t *testing.T) {
	dsn := postgresDSN(t)
	store, err := reggw.NewPostgresRegistrationStore(context.Background(), dsn)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	index := regdomain.RegistrationIndex{
		ID: "primary",
		Entries: []regdomain.RegistryEntry{
			{Name: "hub", URL: "https://registry.example/hub", Repositories: []string{"skills/pdf-tools"}},
			{Name: "internal", URL: "https://registry.example/internal", Namespaces: []string{"team-a"}},
		},
	}
	if ok, err := store.RegistrationStore(index); err != nil || !ok {
		t.Fatalf("store index: ok=%v err=%v", ok, err)
	}

	// Read back through a FRESH store (proves durability across connections, not
	// just an in-process cache).
	fresh, err := reggw.NewPostgresRegistrationStore(context.Background(), dsn)
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	t.Cleanup(func() { _ = fresh.Close() })

	got, err := fresh.Load()
	if err != nil {
		t.Fatalf("load index: %v", err)
	}
	if got.ID != index.ID || len(got.Entries) != len(index.Entries) {
		t.Fatalf("loaded index mismatch: got %+v want %+v", got, index)
	}
	if got.Entries[0].Name != "hub" || len(got.Entries[0].Repositories) != 1 {
		t.Fatalf("entry not round-tripped: %+v", got.Entries[0])
	}

	// A runtime re-registration overwrites the durable row (SPEC §8.2).
	index.Entries = append(index.Entries, regdomain.RegistryEntry{Name: "extra", URL: "https://registry.example/extra"})
	if _, err := store.RegistrationStore(index); err != nil {
		t.Fatalf("update index: %v", err)
	}
	got, err = fresh.Load()
	if err != nil {
		t.Fatalf("reload index: %v", err)
	}
	if len(got.Entries) != 3 {
		t.Fatalf("update not persisted: got %d entries", len(got.Entries))
	}
}

// ---- tests: PostgreSQL revision-history backend (SPEC §5.4, §11) ----

func TestPostgresRevisionStore(t *testing.T) {
	dsn := postgresDSN(t)
	store, err := instgw.NewPostgresRevisionStore(context.Background(), dsn, 0)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	const (
		release   = "pdf-tools"
		target    = "postgres"
		namespace = "skills"
	)
	// Distinct release namespace per run keeps re-runs independent.
	_ = store.Delete(release, target, namespace)

	files1 := map[string][]byte{"SKILL.md": []byte("# v1\n")}
	files2 := map[string][]byte{"SKILL.md": []byte("# v2\n"), "refs/a.md": []byte("A\n")}

	n1, err := store.Append(release, target, namespace, "1.0.0", "sha256:aaa", files1)
	if err != nil || n1 != 1 {
		t.Fatalf("append rev1: n=%d err=%v", n1, err)
	}
	n2, err := store.Append(release, target, namespace, "1.1.0", "sha256:bbb", files2)
	if err != nil || n2 != 2 {
		t.Fatalf("append rev2: n=%d err=%v", n2, err)
	}

	hist, err := store.History(release, target, namespace)
	if err != nil {
		t.Fatalf("history: %v", err)
	}
	if len(hist) != 2 {
		t.Fatalf("history length: got %d want 2", len(hist))
	}
	if hist[0].Number != 1 || hist[0].Version != "1.0.0" || hist[1].Number != 2 {
		t.Fatalf("history ordering wrong: %+v", hist)
	}

	got, err := store.Get(release, target, namespace, 2)
	if err != nil {
		t.Fatalf("get rev2: %v", err)
	}
	if got.Digest != "sha256:bbb" || string(got.Files["refs/a.md"]) != "A\n" {
		t.Fatalf("rev2 bundle not round-tripped: %+v", got)
	}

	// History is scoped per (release,target,namespace): a different namespace is
	// isolated and starts its own numbering.
	nOther, err := store.Append(release, target, "other", "2.0.0", "sha256:ccc", files1)
	if err != nil || nOther != 1 {
		t.Fatalf("append other-namespace: n=%d err=%v", nOther, err)
	}

	if err := store.Delete(release, target, namespace); err != nil {
		t.Fatalf("delete: %v", err)
	}
	hist, err = store.History(release, target, namespace)
	if err != nil {
		t.Fatalf("history after delete: %v", err)
	}
	if len(hist) != 0 {
		t.Fatalf("history not cleared: got %d", len(hist))
	}
	_ = store.Delete(release, target, "other")

	// The adapter satisfies the full RevisionRepository port.
	var _ instout.RevisionRepository = store
}

// TestPostgresRevisionRetention exercises the retention window: only the last N
// revisions of a release are retained (SPEC §5.4, §11).
func TestPostgresRevisionRetention(t *testing.T) {
	dsn := postgresDSN(t)
	store, err := instgw.NewPostgresRevisionStore(context.Background(), dsn, 3)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	const (
		release   = "retention-demo"
		target    = "postgres"
		namespace = "skills"
	)
	_ = store.Delete(release, target, namespace)
	t.Cleanup(func() { _ = store.Delete(release, target, namespace) })

	for i := 1; i <= 5; i++ {
		if _, err := store.Append(release, target, namespace, fmt.Sprintf("1.%d.0", i), "sha256:x", nil); err != nil {
			t.Fatalf("append %d: %v", i, err)
		}
	}
	hist, err := store.History(release, target, namespace)
	if err != nil {
		t.Fatalf("history: %v", err)
	}
	if len(hist) != 3 {
		t.Fatalf("retention window: got %d revisions want 3", len(hist))
	}
	if hist[0].Number != 3 || hist[2].Number != 5 {
		t.Fatalf("retention kept wrong window: %+v", hist)
	}
}

// ---- tests: ClickHouse download-statistics sink (SPEC §10.1) ----

func TestClickHouseStatSink(t *testing.T) {
	dsn := clickhouseDSN(t)
	sink, err := statsgw.NewClickHouseStatSink(context.Background(), dsn)
	if err != nil {
		t.Fatalf("open sink: %v", err)
	}
	t.Cleanup(func() { _ = sink.Close() })

	// A unique skill name keeps the per-skill total independent of prior runs.
	skill := fmt.Sprintf("pdf-tools-%d", time.Now().UnixNano())
	repo := "registry.example/skills/" + skill

	countEvent := func() statsdomain.CountSnapshot {
		snap, err := sink.StatSink(statsdomain.CountRequest{Event: statsdomain.PullEvent{
			Repo: repo, Reference: "sha256:deadbeef", IsManifestGet: true,
		}})
		if err != nil {
			t.Fatalf("record pull: %v", err)
		}
		return snap
	}

	// Only manifest GETs are counted (SPEC §6.4): a non-manifest request records
	// nothing and reports the current total.
	if snap, err := sink.StatSink(statsdomain.CountRequest{Event: statsdomain.PullEvent{Repo: repo}}); err != nil || snap.Total != 0 {
		t.Fatalf("non-manifest request should not count: snap=%+v err=%v", snap, err)
	}

	countEvent()
	countEvent()
	snap := countEvent()
	if snap.Skill != skill {
		t.Fatalf("snapshot skill: got %q want %q", snap.Skill, skill)
	}
	if snap.Total != 3 {
		t.Fatalf("per-skill total: got %d want 3", snap.Total)
	}

	// The read path returns the same lifetime total without recording an event.
	read, err := sink.StatSink(statsdomain.CountRequest{Event: statsdomain.PullEvent{Repo: skill}})
	if err != nil {
		t.Fatalf("read total: %v", err)
	}
	if read.Total != 3 {
		t.Fatalf("read-back total: got %d want 3", read.Total)
	}
}

// ---- k3s deployment helpers ----

func int32p(v int32) *int32 { return &v }

// dbDeployment builds a single-replica Deployment for a database image.
func dbDeployment(name, image string, port int32, env []corev1.EnvVar) *appsv1.Deployment {
	labels := map[string]string{"app": name}
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default", Labels: labels},
		Spec: appsv1.DeploymentSpec{
			Replicas: int32p(1),
			Selector: &metav1.LabelSelector{MatchLabels: labels},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labels},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{
						Name:  name,
						Image: image,
						Env:   env,
						Ports: []corev1.ContainerPort{{ContainerPort: port}},
					}},
				},
			},
		},
	}
}

// applyDeploymentAndService creates the Deployment plus a NodePort Service and
// waits for the Deployment to report a ready replica.
func applyDeploymentAndService(ctx context.Context, cs kubernetes.Interface, dep *appsv1.Deployment, name string, port, nodePort int32) error {
	if _, err := cs.AppsV1().Deployments("default").Create(ctx, dep, metav1.CreateOptions{}); err != nil && !apierrors.IsAlreadyExists(err) {
		return fmt.Errorf("create deployment: %w", err)
	}
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
		Spec: corev1.ServiceSpec{
			Type:     corev1.ServiceTypeNodePort,
			Selector: map[string]string{"app": name},
			Ports: []corev1.ServicePort{{
				Port:       port,
				TargetPort: intstr.FromInt32(port),
				NodePort:   nodePort,
			}},
		},
	}
	if _, err := cs.CoreV1().Services("default").Create(ctx, svc, metav1.CreateOptions{}); err != nil && !apierrors.IsAlreadyExists(err) {
		return fmt.Errorf("create service: %w", err)
	}
	return waitForDeploymentReady(ctx, cs, name, 4*time.Minute)
}

func waitForDeploymentReady(ctx context.Context, cs kubernetes.Interface, name string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		dep, err := cs.AppsV1().Deployments("default").Get(ctx, name, metav1.GetOptions{})
		if err == nil && dep.Status.ReadyReplicas >= 1 {
			return nil
		}
		if time.Now().After(deadline) {
			if err != nil {
				return fmt.Errorf("deployment %q not ready: %w", name, err)
			}
			return fmt.Errorf("deployment %q not ready within %s", name, timeout)
		}
		time.Sleep(2 * time.Second)
	}
}

// nodePortEndpoint returns the host:port on which a published NodePort is
// reachable from the test process.
func nodePortEndpoint(ctx context.Context, spec string) (string, string, error) {
	host, err := k3sContainer.Host(ctx)
	if err != nil {
		return "", "", err
	}
	mapped, err := k3sContainer.MappedPort(ctx, portFromSpec(spec))
	if err != nil {
		return "", "", err
	}
	return host, mapped.Port(), nil
}

func portFromSpec(spec string) nat.Port { return nat.Port(spec) }

// waitForDB opens a database/sql handle and retries Ping until the server
// accepts connections (the pod may be Ready before the server finishes its
// first-boot initialization).
func waitForDB(ctx context.Context, driver, dsn string, timeout time.Duration) error {
	db, err := sql.Open(driver, dsn)
	if err != nil {
		return err
	}
	defer db.Close()
	return pingUntil(ctx, db, timeout)
}

// waitForClickHouse probes readiness through the adapter's own open path so the
// native protocol handshake is what gets validated.
func waitForClickHouse(ctx context.Context, dsn string, timeout time.Duration) error {
	sink, err := statsgw.NewClickHouseStatSink(ctx, dsn)
	deadline := time.Now().Add(timeout)
	for err != nil {
		if time.Now().After(deadline) {
			return err
		}
		time.Sleep(3 * time.Second)
		sink, err = statsgw.NewClickHouseStatSink(ctx, dsn)
	}
	_ = sink.Close()
	return nil
}

func pingUntil(ctx context.Context, db *sql.DB, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		err := db.PingContext(pingCtx)
		cancel()
		if err == nil {
			return nil
		}
		if time.Now().After(deadline) {
			return err
		}
		time.Sleep(2 * time.Second)
	}
}
