// Code scaffolded by sysgo; edit freely (not regenerated).

package gateway

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/gaarutyunov/epos/internal/infrastructure/oci"
	"github.com/gaarutyunov/epos/internal/packaging/app/port/out"
	"github.com/gaarutyunov/epos/internal/packaging/domain"
)

// PackagingPortImpl is the driven adapter implementing the PackagingPort port:
// it builds the reproducible tar+gzip OCI artifact + config blob from a package
// directory and writes it as an OCI image layout (SPEC §2.3).
type PackagingPortImpl struct {
	workDir string
}

var _ out.PackagingPort = (*PackagingPortImpl)(nil)

// NewPackagingPortImpl binds the adapter to an output working directory.
func NewPackagingPortImpl(workDir string) *PackagingPortImpl {
	return &PackagingPortImpl{workDir: workDir}
}

// Packaging builds and writes the OCI artifact for request.SourceDir.
func (p *PackagingPortImpl) Packaging(request domain.PackageRequest) (domain.PackagedArtifact, error) {
	art, err := domain.BuildArtifact(request.SourceDir)
	if err != nil {
		return domain.PackagedArtifact{}, err
	}
	layoutDir := filepath.Join(p.workDir, fmt.Sprintf("%s-%s.epos", art.Manifest.Name, art.Manifest.Version))
	desc, err := oci.WriteLayout(context.Background(), layoutDir, domain.MediaTypeSkillConfig, art.Config.Data,
		[]oci.Blob{{MediaType: art.Content.MediaType, Data: art.Content.Data}},
		domain.MediaTypeSkillConfig, art.Tag, nil)
	if err != nil {
		return domain.PackagedArtifact{}, err
	}
	return domain.PackagedArtifact{
		Ref:    domain.OciRef{Repo: art.Manifest.Name, Tag: art.Tag},
		Digest: toDigest(desc.Digest.String()),
	}, nil
}

func toDigest(s string) domain.Digest {
	if i := strings.Index(s, ":"); i >= 0 {
		return domain.Digest{Algo: s[:i], Value: s[i+1:]}
	}
	return domain.Digest{Value: s}
}
