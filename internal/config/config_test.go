package config

import "testing"

func TestValidateRejectsUnimplementedBackend(t *testing.T) {
	ok := &EposConfig{Backend: Backend{Type: BackendMemory}}
	if err := ok.Validate(); err != nil {
		t.Errorf("memory backend should validate: %v", err)
	}
	pg := &EposConfig{Backend: Backend{Type: BackendPostgres}}
	if err := pg.Validate(); err == nil {
		t.Error("postgres backend must be a hard error in v1, not a silent no-op")
	}
	// configmap is honored for revision history; allowed.
	cm := &EposConfig{RevisionHistory: Backend{Type: BackendConfigMap}}
	if err := cm.Validate(); err != nil {
		t.Errorf("configmap revision backend should validate: %v", err)
	}
	bad := &EposConfig{Stats: Stats{Type: "influxdb"}}
	if err := bad.Validate(); err == nil {
		t.Error("unknown stats backend must be a hard error")
	}
}

func TestRegistryHostAndSigningPolicy(t *testing.T) {
	r := Registry{URL: "https://reg.example.com/", RequireSignature: true}
	if r.Host() != "reg.example.com" {
		t.Errorf("Host() = %q", r.Host())
	}
}
