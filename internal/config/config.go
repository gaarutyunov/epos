// Package config parses Epos's split configuration: epos.yaml (server/backends/
// stats) and registries.yaml (the registry list that seeds the registration
// index). Listing credentials are referenced by env var only (SPEC §6.2, §8.3).
package config

import (
	"fmt"
	"os"

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
	return &c, nil
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
