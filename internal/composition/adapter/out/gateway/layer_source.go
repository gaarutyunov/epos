// Code scaffolded by sysgo; edit freely (not regenerated).

package gateway

import (
	"context"
	"strings"

	"github.com/gaarutyunov/epos/internal/composition/app/port/out"
	"github.com/gaarutyunov/epos/internal/composition/domain"
	"github.com/gaarutyunov/epos/internal/infrastructure/git"
	"github.com/gaarutyunov/epos/internal/infrastructure/oci"
)

// LayerSourceImpl is the driven adapter implementing the LayerSource port: it
// resolves a pulled layer and captures its reproducible pin — OCI manifest
// digest or git commit+tree SHA (SPEC §9.7). It uses the shared OCI/git clients.
type LayerSourceImpl struct {
	oci *oci.Client
	git *git.Client
}

var _ out.LayerSource = (*LayerSourceImpl)(nil)

// NewLayerSourceImpl wraps the shared OCI and git clients.
func NewLayerSourceImpl(o *oci.Client, g *git.Client) *LayerSourceImpl {
	if o == nil {
		o = &oci.Client{}
	}
	if g == nil {
		g = &git.Client{}
	}
	return &LayerSourceImpl{oci: o, git: g}
}

// LayerSource re-resolves a pulled layer's source and returns its captured pin.
func (l *LayerSourceImpl) LayerSource(layer domain.Layer) (domain.PinRecord, error) {
	pin := layer.Pin
	switch pin.SourceType.Value {
	case domain.SourceOCI:
		ref := pin.Source
		if pin.Version != "" {
			ref += ":" + strings.ReplaceAll(pin.Version, "+", "_")
		}
		man, err := l.oci.Pull(context.Background(), ref)
		if err != nil {
			return domain.PinRecord{}, err
		}
		pin.Digest = man.Digest
	case domain.SourceGit:
		res, err := l.git.Resolve(pin.Source, pin.Version, pin.Subpath)
		if err != nil {
			return domain.PinRecord{}, err
		}
		pin.Commit = res.Commit
		pin.TreeSha = res.TreeSha
	}
	return domain.PinRecord{Name: layer.Name, Pin: pin}, nil
}
