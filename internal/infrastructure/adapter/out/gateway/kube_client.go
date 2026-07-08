// Code scaffolded by sysgo; edit freely (not regenerated).

package gateway

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/yaml"

	"github.com/gaarutyunov/epos/internal/infrastructure/app/port/out"
	"github.com/gaarutyunov/epos/internal/infrastructure/kube"
)

// KubeClientImpl is the shared, domain-free Kubernetes adapter (SPEC §15.1). It
// applies serialized ConfigMap/Secret objects — the primitive behind the
// ConfigMap install target (SPEC §14).
type KubeClientImpl struct {
	client *kube.Client
}

var _ out.KubeClient = (*KubeClientImpl)(nil)

// NewKubeClientImpl wraps a Kubernetes client.
func NewKubeClientImpl(client *kube.Client) *KubeClientImpl {
	return &KubeClientImpl{client: client}
}

// KubeClient applies a serialized object of the given kind into namespace.
func (k *KubeClientImpl) KubeClient(namespace string, kind string, name string, manifest string) (bool, error) {
	if k.client == nil {
		return false, fmt.Errorf("kube: no cluster client configured")
	}
	switch kind {
	case "ConfigMap":
		var cm corev1.ConfigMap
		if err := yaml.Unmarshal([]byte(manifest), &cm); err != nil {
			return false, err
		}
		if cm.Name == "" {
			cm.Name = name
		}
		if err := k.client.ApplyConfigMap(context.Background(), namespace, &cm); err != nil {
			return false, err
		}
		return true, nil
	default:
		return false, fmt.Errorf("kube: unsupported kind %q", kind)
	}
}
