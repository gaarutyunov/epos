package app

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/gaarutyunov/epos/internal/infrastructure/kube"
	"github.com/gaarutyunov/epos/internal/install/configmap"
	"github.com/gaarutyunov/epos/internal/install/lock"
)

// KubeClient is the cluster client used for the ConfigMap target; nil unless the
// caller wired one (install --target=configmap requires cluster RBAC).
func (a *App) kube() (*kube.Client, error) {
	if a.Kube != nil {
		return a.Kube, nil
	}
	c, err := kube.NewFromKubeconfig(a.Kubeconfig)
	if err != nil {
		return nil, err
	}
	a.Kube = c
	return c, nil
}

// installConfigMap writes mountable ConfigMap(s) into the namespace and records
// a self-contained in-cluster revision (SPEC §14.6).
func (a *App) installConfigMap(ctx context.Context, release, full, digest, version string, files map[string][]byte, opts InstallOpts) (int, error) {
	kc, err := a.kube()
	if err != nil {
		return 0, err
	}
	rendered, err := configmap.Render(release, opts.Namespace, opts.MountPath, files)
	if err != nil {
		return 0, err
	}
	for i := range rendered.ConfigMaps {
		cm := toCoreConfigMap(release, &rendered.ConfigMaps[i])
		if err := kc.ApplyConfigMap(ctx, opts.Namespace, cm); err != nil {
			return 0, err
		}
	}
	n, err := a.writeClusterRevision(ctx, kc, release, opts.Namespace, lock.Revision{
		Version: version, Digest: digest, Registry: stripTag(full),
	}, files)
	if err != nil {
		return 0, err
	}
	fmt.Fprintf(a.Opts.Out, "Installed %s as %d ConfigMap(s) in namespace %q (release %q) revision %d\n",
		full, len(rendered.ConfigMaps), opts.Namespace, release, n)
	return n, nil
}

// rollbackConfigMap restores a prior in-cluster revision and re-applies it,
// working without any local lockfile (cluster-authoritative, SPEC §14.6).
func (a *App) rollbackConfigMap(ctx context.Context, release string, toRevision int, opts InstallOpts) (int, error) {
	kc, err := a.kube()
	if err != nil {
		return 0, err
	}
	prev, files, err := a.readClusterRevision(ctx, kc, release, opts.Namespace, toRevision)
	if err != nil {
		return 0, err
	}
	rendered, err := configmap.Render(release, opts.Namespace, opts.MountPath, files)
	if err != nil {
		return 0, err
	}
	for i := range rendered.ConfigMaps {
		cm := toCoreConfigMap(release, &rendered.ConfigMaps[i])
		if err := kc.ApplyConfigMap(ctx, opts.Namespace, cm); err != nil {
			return 0, err
		}
	}
	n, err := a.writeClusterRevision(ctx, kc, release, opts.Namespace, lock.Revision{
		Version: prev.Version, Digest: prev.Digest, Registry: prev.Registry,
	}, files)
	if err != nil {
		return 0, err
	}
	fmt.Fprintf(a.Opts.Out, "Rolled back release %q to revision %d in-cluster, recorded as revision %d\n", release, toRevision, n)
	return n, nil
}

func (a *App) uninstallConfigMap(ctx context.Context, release string, opts InstallOpts) error {
	kc, err := a.kube()
	if err != nil {
		return err
	}
	cms, err := kc.ListConfigMaps(ctx, opts.Namespace, "epos.dev/release="+release)
	if err != nil {
		return err
	}
	for i := range cms {
		if err := kc.DeleteConfigMap(ctx, opts.Namespace, cms[i].Name); err != nil {
			return err
		}
	}
	return nil
}

func (a *App) statusConfigMap(ctx context.Context, release string, opts InstallOpts) (*lock.Revision, error) {
	kc, err := a.kube()
	if err != nil {
		return nil, err
	}
	nums, err := a.clusterRevisionNumbers(ctx, kc, release, opts.Namespace)
	if err != nil || len(nums) == 0 {
		return nil, fmt.Errorf("release %q not found in-cluster", release)
	}
	rev, _, err := a.readClusterRevision(ctx, kc, release, opts.Namespace, nums[len(nums)-1])
	return rev, err
}

func (a *App) historyConfigMap(ctx context.Context, release string, opts InstallOpts) ([]lock.Revision, error) {
	kc, err := a.kube()
	if err != nil {
		return nil, err
	}
	nums, err := a.clusterRevisionNumbers(ctx, kc, release, opts.Namespace)
	if err != nil {
		return nil, err
	}
	var out []lock.Revision
	for _, n := range nums {
		rev, _, err := a.readClusterRevision(ctx, kc, release, opts.Namespace, n)
		if err != nil {
			return nil, err
		}
		out = append(out, *rev)
	}
	return out, nil
}

// ---- in-cluster revision records (Helm-style opaque encoding, SPEC §14.6) ----

const revisionLabelKey = "epos.dev/revision-of"

func revisionCMName(release string, n int) string {
	return fmt.Sprintf("epos.%s.v%d", release, n)
}

type clusterBundle struct {
	Revision lock.Revision     `json:"revision"`
	Files    map[string][]byte `json:"files"`
}

func (a *App) clusterRevisionNumbers(ctx context.Context, kc *kube.Client, release, namespace string) ([]int, error) {
	cms, err := kc.ListConfigMaps(ctx, namespace, revisionLabelKey+"="+release)
	if err != nil {
		return nil, err
	}
	var nums []int
	for i := range cms {
		if s, ok := cms[i].Labels["epos.dev/revision"]; ok {
			if n, err := strconv.Atoi(s); err == nil {
				nums = append(nums, n)
			}
		}
	}
	sort.Ints(nums)
	return nums, nil
}

func (a *App) writeClusterRevision(ctx context.Context, kc *kube.Client, release, namespace string, rev lock.Revision, files map[string][]byte) (int, error) {
	nums, err := a.clusterRevisionNumbers(ctx, kc, release, namespace)
	if err != nil {
		return 0, err
	}
	next := 1
	if len(nums) > 0 {
		next = nums[len(nums)-1] + 1
	}
	rev.Revision = next
	blob, err := encodeBundle(clusterBundle{Revision: rev, Files: files})
	if err != nil {
		return 0, err
	}
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      revisionCMName(release, next),
			Namespace: namespace,
			Labels: map[string]string{
				revisionLabelKey:    release,
				"epos.dev/revision": strconv.Itoa(next),
				"epos.dev/owner":    "epos",
			},
		},
		Data: map[string]string{"release": blob},
	}
	if err := kc.ApplyConfigMap(ctx, namespace, cm); err != nil {
		return 0, err
	}
	return next, nil
}

func (a *App) readClusterRevision(ctx context.Context, kc *kube.Client, release, namespace string, n int) (*lock.Revision, map[string][]byte, error) {
	cm, err := kc.GetConfigMap(ctx, namespace, revisionCMName(release, n))
	if err != nil {
		return nil, nil, err
	}
	bundle, err := decodeBundle(cm.Data["release"])
	if err != nil {
		return nil, nil, err
	}
	return &bundle.Revision, bundle.Files, nil
}

// encodeBundle serializes a bundle as JSON → gzip → base64 (Helm-style, §14.6).
func encodeBundle(b clusterBundle) (string, error) {
	raw, err := json.Marshal(b)
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	if _, err := gz.Write(raw); err != nil {
		return "", err
	}
	if err := gz.Close(); err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(buf.Bytes()), nil
}

func decodeBundle(s string) (*clusterBundle, error) {
	data, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return nil, err
	}
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	defer gz.Close()
	var buf bytes.Buffer
	if _, err := buf.ReadFrom(gz); err != nil {
		return nil, err
	}
	var b clusterBundle
	if err := json.Unmarshal(buf.Bytes(), &b); err != nil {
		return nil, err
	}
	return &b, nil
}

// toCoreConfigMap converts a rendered ConfigMap into a typed corev1.ConfigMap
// carrying Epos ownership labels.
func toCoreConfigMap(release string, cm *configmap.ConfigMap) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      cm.Metadata.Name,
			Namespace: cm.Metadata.Namespace,
			Labels: map[string]string{
				"epos.dev/release": release,
				"epos.dev/owner":   "epos",
			},
		},
		Data:       cm.Data,
		BinaryData: cm.BinaryData,
	}
}
