package domain

import (
	"fmt"

	"github.com/Masterminds/semver/v3"
	"sigs.k8s.io/yaml"
)

// Maintainer is one Epos.yaml maintainer entry.
type Maintainer struct {
	Name  string `json:"name,omitempty"`
	Email string `json:"email,omitempty"`
}

// Dependency is a unified, source-typed pulled layer declared in Epos.yaml
// (SPEC §9.6). Each entry names an OCI or git source; composition semantics are
// identical regardless of source, only pin capture differs (SPEC §9.7).
type Dependency struct {
	Name    string `json:"name"`
	Kind    string `json:"kind,omitempty"` // "" (skill) | overlay
	OCI     string `json:"oci,omitempty"`
	Git     string `json:"git,omitempty"`
	Version string `json:"version,omitempty"` // OCI tag
	Ref     string `json:"ref,omitempty"`     // git ref
	Subpath string `json:"subpath,omitempty"` // git subpath
}

// Manifest is the parsed Epos.yaml metadata (SPEC §2.2), the Chart.yaml analog.
type Manifest struct {
	APIVersion   string            `json:"apiVersion"`
	Name         string            `json:"name"`
	Version      string            `json:"version"`
	Description  string            `json:"description"`
	Keywords     []string          `json:"keywords,omitempty"`
	Maintainers  []Maintainer      `json:"maintainers,omitempty"`
	Home         string            `json:"home,omitempty"`
	Sources      []string          `json:"sources,omitempty"`
	Annotations  map[string]string `json:"annotations,omitempty"`
	Dependencies []Dependency      `json:"dependencies,omitempty"`
}

// ParseManifest decodes Epos.yaml bytes.
func ParseManifest(data []byte) (*Manifest, error) {
	var m Manifest
	if err := yaml.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parse Epos.yaml: %w", err)
	}
	return &m, nil
}

// Marshal serializes the manifest back to canonical JSON (used for the config
// blob so a registry/frontend can read metadata without pulling the package).
func (m *Manifest) Marshal() ([]byte, error) { return yaml.Marshal(m) }

// SemVer parses the manifest version into the SemVer value object.
func (m *Manifest) SemVer() (SemVer, error) { return ParseSemVer(m.Version) }

// ParseSemVer parses a strict SemVer 2.0.0 string into the SemVer value object.
func ParseSemVer(raw string) (SemVer, error) {
	v, err := semver.StrictNewVersion(raw)
	if err != nil {
		return SemVer{}, fmt.Errorf("invalid SemVer %q: %w", raw, err)
	}
	return SemVer{
		Raw:   raw,
		Major: int64(v.Major()),
		Minor: int64(v.Minor()),
		Patch: int64(v.Patch()),
		Build: v.Metadata(),
	}, nil
}

// OCITag returns the OCI-safe tag for a version: OCI tags cannot contain '+',
// so SemVer build metadata (1.4.2+build.5) is rewritten '+'->'_' on push and
// reversed on pull, matching Helm's behavior (SPEC §2.2 caveat).
func OCITag(version string) string {
	out := make([]rune, 0, len(version))
	for _, r := range version {
		if r == '+' {
			out = append(out, '_')
			continue
		}
		out = append(out, r)
	}
	return string(out)
}

// VersionFromOCITag reverses OCITag on pull.
func VersionFromOCITag(tag string) string {
	out := make([]rune, 0, len(tag))
	for _, r := range tag {
		if r == '_' {
			out = append(out, '+')
			continue
		}
		out = append(out, r)
	}
	return string(out)
}
