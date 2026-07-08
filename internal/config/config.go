// Package config parses Epos's split configuration: epos.yaml (server/backends/
// stats) and registries.yaml (the registry list that seeds the registration
// index). Listing credentials are referenced by env var only (SPEC §6.2, §8.3).
package config

import (
	"fmt"
	"os"
	"strings"

	"sigs.k8s.io/yaml"
)

// Discovery modes (SPEC §8.1.1).
const (
	DiscoveryCatalog    = "catalog"
	DiscoveryRegistered = "registered"
)

// Registry is one entry in registries.yaml (SPEC §8.3.2).
type Registry struct {
	Name         string   `json:"name"`
	URL          string   `json:"url"`
	UsernameEnv  string   `json:"usernameEnv,omitempty"`
	TokenEnv     string   `json:"tokenEnv,omitempty"`
	Discovery    string   `json:"discovery,omitempty"` // "" (auto) | catalog | registered
	Repositories []string `json:"repositories,omitempty"`
	Namespaces   []string `json:"namespaces,omitempty"`
	// RequireSignature is the per-registry signing policy: when true, artifacts
	// from this registry must carry a valid signature, tightening the global
	// default (SPEC §7.2).
	RequireSignature bool `json:"requireSignature,omitempty"`
}

// Host returns the registry URL without its scheme, for matching against a
// registry/repo reference.
func (r *Registry) Host() string {
	h := r.URL
	for _, p := range []string{"https://", "http://"} {
		h = strings.TrimPrefix(h, p)
	}
	return strings.TrimSuffix(h, "/")
}

// Username resolves the read-only listing username from its env var (never
// stored inline, SPEC §6.2).
func (r *Registry) Username() string { return os.Getenv(r.UsernameEnv) }

// Token resolves the read-only listing token from its env var.
func (r *Registry) Token() string { return os.Getenv(r.TokenEnv) }

// Registries is the parsed registries.yaml (SPEC §8.3.2).
type Registries struct {
	APIVersion               string     `json:"apiVersion"`
	Kind                     string     `json:"kind"`
	DiscoveryRefreshInterval string     `json:"discoveryRefreshInterval,omitempty"`
	Registries               []Registry `json:"registries"`
}

// LoadRegistries reads and parses registries.yaml.
func LoadRegistries(path string) (*Registries, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var r Registries
	if err := yaml.Unmarshal(data, &r); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return &r, nil
}

// Find returns the registry entry with the given name.
func (r *Registries) Find(name string) *Registry {
	for i := range r.Registries {
		if r.Registries[i].Name == name {
			return &r.Registries[i]
		}
	}
	return nil
}

// Backend selects a durable-state backend (SPEC §8.3.1, §11).
type Backend struct {
	Type      string `json:"type,omitempty"` // memory | configmap | secret | postgres
	Namespace string `json:"namespace,omitempty"`
	Name      string `json:"name,omitempty"`
	DSNEnv    string `json:"dsnEnv,omitempty"`
	Retention int    `json:"retention,omitempty"`
}

// Server holds listen addresses (SPEC §8.3.1).
type Server struct {
	Listen        string `json:"listen,omitempty"`
	MetricsListen string `json:"metricsListen,omitempty"`
}

// Stats configures the download-statistics sink (SPEC §10, always its own block).
type Stats struct {
	Type       string `json:"type,omitempty"` // prometheus | clickhouse
	Prometheus struct {
		PerSkill bool `json:"perSkill,omitempty"`
	} `json:"prometheus,omitempty"`
	ClickHouse struct {
		DSNEnv string `json:"dsnEnv,omitempty"`
	} `json:"clickhouse,omitempty"`
}

// Signing configures signature enforcement (SPEC §7.2).
type Signing struct {
	RequireSignature bool `json:"requireSignature,omitempty"`
}

// EposConfig is the parsed epos.yaml (SPEC §8.3.1).
type EposConfig struct {
	APIVersion        string  `json:"apiVersion"`
	Kind              string  `json:"kind"`
	Server            Server  `json:"server,omitempty"`
	Backend           Backend `json:"backend,omitempty"`
	RegistrationIndex Backend `json:"registrationIndex,omitempty"`
	RevisionHistory   Backend `json:"revisionHistory,omitempty"`
	Stats             Stats   `json:"stats,omitempty"`
	Signing           Signing `json:"signing,omitempty"`
}

// LoadEposConfig reads and parses epos.yaml.
func LoadEposConfig(path string) (*EposConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var c EposConfig
	if err := yaml.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if err := c.Validate(); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &c, nil
}

// Backend type values (SPEC §8.3.1, §11).
const (
	BackendMemory    = "memory"
	BackendConfigMap = "configmap"
	BackendSecret    = "secret"
	BackendPostgres  = "postgres"
)

// Validate reports configuration that would be silently ignored, so an operator
// is not misled by a backend that is declared but not honored. Backends beyond
// those Epos implements are a hard error rather than a silent no-op (SPEC §11).
func (c *EposConfig) Validate() error {
	// The registration index implements memory today; the revision history
	// implements the local lockfile (files target) and in-cluster ConfigMap
	// (configmap target). Postgres/Secret/ClickHouse are reserved (SPEC §11).
	check := func(concern string, b Backend, allowed ...string) error {
		if b.Type == "" {
			return nil
		}
		for _, a := range allowed {
			if b.Type == a {
				return nil
			}
		}
		return fmt.Errorf("%s backend %q is not implemented in v1 (supported: %s)", concern, b.Type, strings.Join(allowed, ", "))
	}
	if err := check("registrationIndex", c.RegistrationBackend(), BackendMemory, BackendConfigMap); err != nil {
		return err
	}
	if err := check("revisionHistory", c.RevisionBackend(), BackendMemory, BackendConfigMap); err != nil {
		return err
	}
	if c.Stats.Type != "" && c.Stats.Type != "prometheus" && c.Stats.Type != "clickhouse" {
		return fmt.Errorf("stats backend %q is unknown (supported: prometheus, clickhouse)", c.Stats.Type)
	}
	return nil
}

// RegistrationBackend returns the effective registration-index backend: the
// per-concern override if set, else the top-level default (SPEC §8.3.1).
func (c *EposConfig) RegistrationBackend() Backend {
	if c.RegistrationIndex.Type != "" {
		return c.RegistrationIndex
	}
	return c.Backend
}

// RevisionBackend returns the effective revision-history backend (SPEC §8.3.1).
func (c *EposConfig) RevisionBackend() Backend {
	if c.RevisionHistory.Type != "" {
		return c.RevisionHistory
	}
	b := c.Backend
	if c.RevisionHistory.Retention != 0 {
		b.Retention = c.RevisionHistory.Retention
	}
	return b
}
