// Package kube is the shared, domain-free Kubernetes client (the model's
// Infrastructure.KubeClient, SPEC §15.1). It applies/reads/deletes ConfigMap and
// Secret objects — the primitives behind the ConfigMap install target (SPEC §14)
// and the in-cluster revision-history backend (SPEC §11).
package kube

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

// Client wraps a Kubernetes clientset.
type Client struct {
	cs kubernetes.Interface
}

// NewFromKubeconfig builds a client from a kubeconfig path (empty ⇒ default
// loading rules / in-cluster config).
func NewFromKubeconfig(path string) (*Client, error) {
	var cfg *rest.Config
	var err error
	if path != "" {
		cfg, err = clientcmd.BuildConfigFromFlags("", path)
	} else {
		rules := clientcmd.NewDefaultClientConfigLoadingRules()
		cfg, err = clientcmd.NewNonInteractiveDeferredLoadingClientConfig(rules, &clientcmd.ConfigOverrides{}).ClientConfig()
	}
	if err != nil {
		return nil, fmt.Errorf("kube: load config: %w", err)
	}
	cs, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, err
	}
	return &Client{cs: cs}, nil
}

// NewFromInterface wraps an existing clientset (used with a fake for tests).
func NewFromInterface(cs kubernetes.Interface) *Client { return &Client{cs: cs} }

// ApplyConfigMap creates or updates a ConfigMap (Epos owns the object under its
// handle, overwriting out-of-band edits, SPEC §14.7).
func (c *Client) ApplyConfigMap(ctx context.Context, namespace string, cm *corev1.ConfigMap) error {
	api := c.cs.CoreV1().ConfigMaps(namespace)
	existing, err := api.Get(ctx, cm.Name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		_, err = api.Create(ctx, cm, metav1.CreateOptions{})
		return err
	}
	if err != nil {
		return err
	}
	cm.ResourceVersion = existing.ResourceVersion
	_, err = api.Update(ctx, cm, metav1.UpdateOptions{})
	return err
}

// GetConfigMap fetches a ConfigMap.
func (c *Client) GetConfigMap(ctx context.Context, namespace, name string) (*corev1.ConfigMap, error) {
	return c.cs.CoreV1().ConfigMaps(namespace).Get(ctx, name, metav1.GetOptions{})
}

// ListConfigMaps lists ConfigMaps matching a label selector.
func (c *Client) ListConfigMaps(ctx context.Context, namespace, selector string) ([]corev1.ConfigMap, error) {
	list, err := c.cs.CoreV1().ConfigMaps(namespace).List(ctx, metav1.ListOptions{LabelSelector: selector})
	if err != nil {
		return nil, err
	}
	return list.Items, nil
}

// DeleteConfigMap removes a ConfigMap (ignoring not-found).
func (c *Client) DeleteConfigMap(ctx context.Context, namespace, name string) error {
	err := c.cs.CoreV1().ConfigMaps(namespace).Delete(ctx, name, metav1.DeleteOptions{})
	if apierrors.IsNotFound(err) {
		return nil
	}
	return err
}
