package app

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	cdomain "github.com/gaarutyunov/epos/internal/composition/domain"
	"github.com/gaarutyunov/epos/internal/infrastructure/oci"
	"github.com/gaarutyunov/epos/internal/packaging/domain"
)

// contentFiles unpacks the (skill or overlay) content layer of a pulled artifact.
func contentFiles(man *oci.Manifest) (map[string][]byte, error) {
	for _, l := range man.Layers {
		if l.MediaType == domain.MediaTypeSkillContent || l.MediaType == domain.MediaTypeOverlayContent {
			return domain.UnpackTarGz(l.Data)
		}
	}
	// Fall back to the first layer if media type differs.
	if len(man.Layers) > 0 {
		return domain.UnpackTarGz(man.Layers[0].Data)
	}
	return map[string][]byte{}, nil
}

// OverlayPush builds a published overlay OCI artifact from a local overlay
// directory and pushes it via ORAS (SPEC §9.9): a config blob (Overlay.yaml +
// operations) and a single tar+gzip content layer bundling files/.
func (a *App) OverlayPush(ctx context.Context, overlayDir, ref string) (string, error) {
	ovData, err := os.ReadFile(filepath.Join(overlayDir, "Overlay.yaml"))
	if err != nil {
		return "", fmt.Errorf("read Overlay.yaml: %w", err)
	}
	ov, err := cdomain.ParseOverlay(ovData)
	if err != nil {
		return "", err
	}
	if msgs := ov.Validate(); len(msgs) > 0 {
		return "", fmt.Errorf("invalid overlay: %v", msgs)
	}
	tgz, err := tarDir(overlayDir)
	if err != nil {
		return "", err
	}
	full := a.qualify(ref)
	desc, err := a.OCI.Push(ctx, full, domain.MediaTypeOverlayConfig, ovData,
		[]oci.Blob{{MediaType: domain.MediaTypeOverlayContent, Data: tgz}},
		domain.MediaTypeOverlayConfig,
		map[string]string{"org.opencontainers.image.title": ov.Name, "org.opencontainers.image.version": ov.Version})
	if err != nil {
		return "", err
	}
	fmt.Fprintf(a.Opts.Out, "Pushed overlay %s → %s\n", full, desc.Digest)
	return desc.Digest.String(), nil
}

// tarDir builds a deterministic tar+gzip of every regular file under dir.
func tarDir(dir string) ([]byte, error) {
	var paths []string
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			paths = append(paths, path)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(paths)
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, p := range paths {
		rel, _ := filepath.Rel(dir, p)
		content, err := os.ReadFile(p)
		if err != nil {
			return nil, err
		}
		if err := tw.WriteHeader(&tar.Header{Name: filepath.ToSlash(rel), Mode: 0o644, Size: int64(len(content))}); err != nil {
			return nil, err
		}
		if _, err := tw.Write(content); err != nil {
			return nil, err
		}
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}
	if err := gz.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
