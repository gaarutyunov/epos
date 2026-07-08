// Code scaffolded by sysgo; edit freely (not regenerated).

package gateway

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"encoding/json"

	"github.com/gaarutyunov/epos/internal/infrastructure/kube"
	"github.com/gaarutyunov/epos/internal/infrastructure/oci"
	"github.com/gaarutyunov/epos/internal/install/app/port/out"
	"github.com/gaarutyunov/epos/internal/install/configmap"
	"github.com/gaarutyunov/epos/internal/install/domain"
	"github.com/gaarutyunov/epos/internal/install/materialize"
	pkgdomain "github.com/gaarutyunov/epos/internal/packaging/domain"
	"github.com/gaarutyunov/epos/internal/render"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// MaterializePortImpl is the driven adapter implementing the MaterializePort:
// it writes a resolved skill to local files or mountable ConfigMap(s) (SPEC §14).
type MaterializePortImpl struct {
	workDir string
	oci     *oci.Client
	kube    *kube.Client
	last    map[string][]byte
	lastDig string
	lastVal map[string]any
}

var _ out.MaterializePort = (*MaterializePortImpl)(nil)

// NewMaterializePortImpl wraps the output dir and the OCI/Kube clients.
func NewMaterializePortImpl(workDir string, o *oci.Client, k *kube.Client) *MaterializePortImpl {
	return &MaterializePortImpl{workDir: workDir, oci: o, kube: k}
}

// Materialize fetches request.SkillID (a full OCI ref) and writes it to the
// requested target. The fetched files are retained for the revision snapshot.
func (m *MaterializePortImpl) Materialize(request domain.InstallRequest) (domain.InstallResult, error) {
	man, err := m.oci.Pull(context.Background(), request.SkillID)
	if err != nil {
		return domain.InstallResult{}, err
	}
	var files map[string][]byte
	for _, l := range man.Layers {
		if l.MediaType == pkgdomain.MediaTypeSkillContent {
			files, err = pkgdomain.UnpackTarGz(l.Data)
			if err != nil {
				return domain.InstallResult{}, err
			}
		}
	}

	// Render SKILL.md with the package values.yaml merged with the request's
	// -f/--set overrides, so the materialized/projected skill is the *rendered*
	// skill (SPEC §3, §14.1), and capture the effective values for the revision
	// snapshot (§5.3).
	overrides := map[string]any{}
	if request.Values != "" {
		_ = json.Unmarshal([]byte(request.Values), &overrides)
	}
	rendered, effective, err := render.Bundle(files, overrides)
	if err != nil {
		return domain.InstallResult{}, err
	}
	files = rendered
	m.last = files
	m.lastDig = man.Digest
	m.lastVal = effective

	if err := m.Write(request.ReleaseName, request.Target.Value, request.Namespace, files); err != nil {
		return domain.InstallResult{}, err
	}
	return domain.InstallResult{ReleaseName: request.ReleaseName, Ok: true}, nil
}

// Write materializes a file set to the target (used by install and rollback).
func (m *MaterializePortImpl) Write(release, target, namespace string, files map[string][]byte) error {
	if target == "configmap" {
		if m.kube == nil {
			return fmt.Errorf("configmap target requires a cluster client")
		}
		rendered, err := configmap.Render(release, namespace, "", files)
		if err != nil {
			return err
		}
		for i := range rendered.ConfigMaps {
			cm := rendered.ConfigMaps[i]
			obj := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{Name: cm.Metadata.Name, Namespace: namespace, Labels: map[string]string{"epos.dev/release": release}},
				Data:       cm.Data, BinaryData: cm.BinaryData,
			}
			if err := m.kube.ApplyConfigMap(context.Background(), namespace, obj); err != nil {
				return err
			}
		}
		return nil
	}
	return materialize.WriteTree(filepath.Join(m.workDir, release), files)
}

// LastFiles returns the file set from the most recent Materialize call.
func (m *MaterializePortImpl) LastFiles() map[string][]byte { return m.last }

// LastDigest returns the manifest digest from the most recent Materialize call.
func (m *MaterializePortImpl) LastDigest() string { return m.lastDig }

// LastValues returns the effective merged values from the most recent
// Materialize call (SPEC §5.3).
func (m *MaterializePortImpl) LastValues() map[string]any { return m.lastVal }

// Remove deletes a release's materialized files (uninstall, files target).
func (m *MaterializePortImpl) Remove(release, target, namespace string) error {
	if target == "configmap" {
		return nil // cluster ConfigMaps are removed via the revision store
	}
	return removeDir(filepath.Join(m.workDir, release))
}

func removeDir(dir string) error {
	if err := os.RemoveAll(dir); err != nil {
		return err
	}
	return nil
}
