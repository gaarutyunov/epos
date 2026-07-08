// Code scaffolded by sysgo; edit freely (not regenerated).

package usecase

import (
	"github.com/gaarutyunov/epos/internal/install/domain"
	pkgdomain "github.com/gaarutyunov/epos/internal/packaging/domain"
)

// versionOf extracts the skill version from an unpacked bundle's Epos.yaml.
func versionOf(files map[string][]byte) string {
	data, ok := files["Epos.yaml"]
	if !ok {
		return ""
	}
	m, err := pkgdomain.ParseManifest(data)
	if err != nil {
		return ""
	}
	return m.Version
}

// resultOf builds an InstallResult for a release revision.
func resultOf(release string, revision int) domain.InstallResult {
	return domain.InstallResult{ReleaseName: release, Revision: int64(revision), Ok: true}
}
