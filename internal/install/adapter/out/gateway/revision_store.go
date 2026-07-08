// Code scaffolded by sysgo; edit freely (not regenerated).

package gateway

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strconv"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/gaarutyunov/epos/internal/infrastructure/kube"
	"github.com/gaarutyunov/epos/internal/install/app/port/out"
	"github.com/gaarutyunov/epos/internal/install/domain"
	"github.com/gaarutyunov/epos/internal/install/lock"
)

// RevisionStoreImpl is the driven adapter implementing the RevisionStore /
// RevisionRepository ports over the pluggable §11 backends: the local
// git-committed lockfile for the files target, and self-contained in-cluster
// ConfigMap records for the configmap target (SPEC §5.4, §14.6).
type RevisionStoreImpl struct {
	workDir   string
	kube      *kube.Client
	retention int
}

var _ out.RevisionRepository = (*RevisionStoreImpl)(nil)

// NewRevisionStoreImpl binds the store to the project directory and (optionally)
// a cluster client for the configmap target.
func NewRevisionStoreImpl(workDir string, k *kube.Client) *RevisionStoreImpl {
	return &RevisionStoreImpl{workDir: workDir, kube: k}
}

// SetRetention configures the retained-revision count from config (SPEC §5.3);
// 0 leaves the lockfile default in place.
func (r *RevisionStoreImpl) SetRetention(n int) { r.retention = n }

func (r *RevisionStoreImpl) lockfilePath() string {
	return filepath.Join(r.workDir, lock.LockfileName)
}

// RevisionStore persists a release's pending revisions (SPEC §5.3).
func (r *RevisionStoreImpl) RevisionStore(release domain.Release) (bool, error) {
	for _, rev := range release.Revisions {
		b, err := decodeBundle(rev.Blob)
		if err != nil {
			return false, err
		}
		if _, err := r.Append(release.Name, release.Target.Value, release.Namespace, out.RevisionSpec{
			Version: b.Version, Digest: b.Digest, Registry: b.Registry, Values: b.Values, Files: b.Files,
		}); err != nil {
			return false, err
		}
	}
	return true, nil
}

// Append records one revision bundle and returns its assigned number.
func (r *RevisionStoreImpl) Append(release, target, namespace string, spec out.RevisionSpec) (int, error) {
	if target == "configmap" {
		return r.appendCluster(release, namespace, spec)
	}
	lf, err := lock.Load(r.lockfilePath())
	if err != nil {
		return 0, err
	}
	if r.retention > 0 {
		lf.SetRetention(r.retention)
	}
	lr := lock.Revision{Version: spec.Version, Digest: spec.Digest, Registry: spec.Registry, Values: spec.Values, Overlays: toLockPins(spec.Overlays)}
	lr.SetFiles(spec.Files)
	n := lf.AddRevision(release, lr)
	if err := lf.Save(); err != nil {
		return 0, err
	}
	return n, nil
}

// toLockPins converts port overlay pins to lockfile overlay pins.
func toLockPins(pins []out.OverlayPin) []lock.OverlayPin {
	if len(pins) == 0 {
		return nil
	}
	out := make([]lock.OverlayPin, len(pins))
	for i, p := range pins {
		out[i] = lock.OverlayPin{Name: p.Name, Digest: p.Digest}
	}
	return out
}

// History returns the retained revisions of a release (oldest first).
func (r *RevisionStoreImpl) History(release, target, namespace string) ([]out.RevisionInfo, error) {
	if target == "configmap" {
		return r.historyCluster(release, namespace)
	}
	lf, err := lock.Load(r.lockfilePath())
	if err != nil {
		return nil, err
	}
	var infos []out.RevisionInfo
	for _, rev := range lf.History(release) {
		files, _ := rev.FileBytes()
		infos = append(infos, out.RevisionInfo{Number: rev.Revision, Version: rev.Version, Digest: rev.Digest, Registry: rev.Registry, Values: rev.Values, Overlays: fromLockPins(rev.Overlays), Files: files})
	}
	return infos, nil
}

// fromLockPins converts lockfile overlay pins to port overlay pins.
func fromLockPins(pins []lock.OverlayPin) []out.OverlayPin {
	if len(pins) == 0 {
		return nil
	}
	o := make([]out.OverlayPin, len(pins))
	for i, p := range pins {
		o[i] = out.OverlayPin{Name: p.Name, Digest: p.Digest}
	}
	return o
}

// Get returns a specific retained revision.
func (r *RevisionStoreImpl) Get(release, target, namespace string, number int) (out.RevisionInfo, error) {
	if target == "configmap" {
		return r.getCluster(release, namespace, number)
	}
	lf, err := lock.Load(r.lockfilePath())
	if err != nil {
		return out.RevisionInfo{}, err
	}
	rev, err := lf.Get(release, number)
	if err != nil {
		return out.RevisionInfo{}, err
	}
	files, _ := rev.FileBytes()
	return out.RevisionInfo{Number: rev.Revision, Version: rev.Version, Digest: rev.Digest, Registry: rev.Registry, Values: rev.Values, Overlays: fromLockPins(rev.Overlays), Files: files}, nil
}

// Delete removes a release's revision history.
func (r *RevisionStoreImpl) Delete(release, target, namespace string) error {
	if target == "configmap" {
		cms, err := r.kube.ListConfigMaps(context.Background(), namespace, "epos.dev/revision-of="+release)
		if err != nil {
			return err
		}
		for i := range cms {
			if err := r.kube.DeleteConfigMap(context.Background(), namespace, cms[i].Name); err != nil {
				return err
			}
		}
		return nil
	}
	lf, err := lock.Load(r.lockfilePath())
	if err != nil {
		return err
	}
	lf.Remove(release)
	return lf.Save()
}

// ---- in-cluster revision records (configmap target, SPEC §14.6) ----

func (r *RevisionStoreImpl) appendCluster(release, namespace string, spec out.RevisionSpec) (int, error) {
	if r.kube == nil {
		return 0, fmt.Errorf("configmap revision history requires a cluster client")
	}
	nums, err := r.clusterNums(release, namespace)
	if err != nil {
		return 0, err
	}
	next := 1
	if len(nums) > 0 {
		next = nums[len(nums)-1] + 1
	}
	blob, err := encodeBundle(bundle{
		Version: spec.Version, Digest: spec.Digest, Registry: spec.Registry,
		Values: spec.Values, Overlays: toBundlePins(spec.Overlays), Files: spec.Files,
	})
	if err != nil {
		return 0, err
	}
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("epos.%s.v%d", release, next),
			Namespace: namespace,
			Labels:    map[string]string{"epos.dev/revision-of": release, "epos.dev/revision": strconv.Itoa(next)},
		},
		Data: map[string]string{"release": blob},
	}
	if err := r.kube.ApplyConfigMap(context.Background(), namespace, cm); err != nil {
		return 0, err
	}
	return next, nil
}

func (r *RevisionStoreImpl) clusterNums(release, namespace string) ([]int, error) {
	cms, err := r.kube.ListConfigMaps(context.Background(), namespace, "epos.dev/revision-of="+release)
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

func (r *RevisionStoreImpl) historyCluster(release, namespace string) ([]out.RevisionInfo, error) {
	nums, err := r.clusterNums(release, namespace)
	if err != nil {
		return nil, err
	}
	var infos []out.RevisionInfo
	for _, n := range nums {
		info, err := r.getCluster(release, namespace, n)
		if err != nil {
			return nil, err
		}
		infos = append(infos, info)
	}
	return infos, nil
}

func (r *RevisionStoreImpl) getCluster(release, namespace string, number int) (out.RevisionInfo, error) {
	cm, err := r.kube.GetConfigMap(context.Background(), namespace, fmt.Sprintf("epos.%s.v%d", release, number))
	if err != nil {
		return out.RevisionInfo{}, err
	}
	b, err := decodeBundle(cm.Data["release"])
	if err != nil {
		return out.RevisionInfo{}, err
	}
	return out.RevisionInfo{Number: number, Version: b.Version, Digest: b.Digest, Registry: b.Registry, Values: b.Values, Overlays: fromBundlePins(b.Overlays), Files: b.Files}, nil
}
