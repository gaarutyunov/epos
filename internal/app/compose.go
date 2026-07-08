package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	compgw "github.com/gaarutyunov/epos/internal/composition/adapter/out/gateway"
	compin "github.com/gaarutyunov/epos/internal/composition/app/port/in"
	compusecase "github.com/gaarutyunov/epos/internal/composition/app/usecase"
	cdomain "github.com/gaarutyunov/epos/internal/composition/domain"
	clock "github.com/gaarutyunov/epos/internal/composition/lock"
	"github.com/gaarutyunov/epos/internal/infrastructure/git"
	"github.com/gaarutyunov/epos/internal/infrastructure/oci"
	"github.com/gaarutyunov/epos/internal/packaging/domain"
)

// ComposeResult is the outcome of composing a skill: the merged file set, the
// per-file provenance, and the captured pins for pulled layers.
type ComposeResult struct {
	Merged *cdomain.Merged
	// LayerPins are the pulled-layer pins in Epos.lock form (with kind), for
	// persistence and verification (SPEC §9.7).
	LayerPins []clock.LayerPin
}

// pinRecord builds an Epos.lock pin record from a resolved layer + its pin.
func pinRecord(name, kind string, pin *cdomain.Pin) clock.LayerPin {
	return clock.LayerPin{
		Name: name, Kind: kind, SourceType: pin.SourceType.Value, Source: pin.Source,
		Version: pin.Version, Digest: pin.Digest, Commit: pin.Commit, TreeSha: pin.TreeSha, Subpath: pin.Subpath,
	}
}

// Compose builds the layer stack for a skill directory (origin/deps at the
// bottom, local overlays above, the consumer's own files on top) and resolves it
// into one merged skill (SPEC §9). strict promotes soft failures to hard errors.
func (a *App) Compose(ctx context.Context, skillDir string, strict bool) (*ComposeResult, error) {
	stack, pins, err := a.BuildStack(ctx, skillDir)
	if err != nil {
		return nil, err
	}
	// Drive the ComposeStack use case through the CompositionPort.
	port := compgw.NewCompositionPortImpl(stack, strict)
	interactor := compusecase.NewComposeStackInteractor(port)
	if _, err := interactor.ComposeStack(compin.ComposeStackInput{Request: cdomain.ComposeRequest{StackID: skillDir}}); err != nil {
		return nil, err
	}
	return &ComposeResult{Merged: port.Merged(), LayerPins: pins}, nil
}

// ResolvePins resolves a skill's pulled-layer pins without writing anything
// (used by `dependency list`).
func (a *App) ResolvePins(ctx context.Context, skillDir string) ([]clock.LayerPin, error) {
	_, pins, err := a.BuildStack(ctx, skillDir)
	return pins, err
}

// Lock resolves a skill's pulled-layer pins and writes Epos.lock without
// materializing (SPEC §4.3, §9.7). It returns the written pin records.
func (a *App) Lock(ctx context.Context, skillDir string) ([]clock.LayerPin, error) {
	pins, err := a.ResolvePins(ctx, skillDir)
	if err != nil {
		return nil, err
	}
	if err := clock.New(pins).Save(skillDir); err != nil {
		return nil, err
	}
	return pins, nil
}

// VerifyLock re-resolves a skill's pulled-layer pins and verifies them against a
// committed Epos.lock; any mismatch is a hard error (SPEC §9.7, --frozen/CI). It
// is a no-op when no Epos.lock is present.
func (a *App) VerifyLock(ctx context.Context, skillDir string) error {
	lf, err := clock.Load(skillDir)
	if err != nil || lf == nil {
		return err
	}
	_, pins, err := a.BuildStack(ctx, skillDir)
	if err != nil {
		return err
	}
	return lf.Verify(pins)
}

// BuildStack resolves the ordered layer stack for a skill directory (SPEC §9.1,
// §9.6): declared pulled dependencies (bottom→up in declaration order), then
// local overlays under overlays/, then the consumer skill's own files (top).
func (a *App) BuildStack(ctx context.Context, skillDir string) ([]cdomain.StackLayer, []clock.LayerPin, error) {
	var stack []cdomain.StackLayer
	var pins []clock.LayerPin

	manData, err := os.ReadFile(filepath.Join(skillDir, "Epos.yaml"))
	if err != nil {
		return nil, nil, err
	}
	man, err := domain.ParseManifest(manData)
	if err != nil {
		return nil, nil, err
	}

	for _, dep := range man.Dependencies {
		layer, pin, err := a.resolveDependency(ctx, dep)
		if err != nil {
			return nil, nil, err
		}
		stack = append(stack, *layer)
		if pin != nil {
			pins = append(pins, pinRecord(layer.Name, layer.Kind, pin))
		}
	}

	// Local overlays under overlays/<name>/Overlay.yaml (above pulled layers).
	locals, err := loadLocalOverlays(skillDir)
	if err != nil {
		return nil, nil, err
	}
	stack = append(stack, locals...)

	// Consumer's own files on top (excluding Epos.yaml and overlays/).
	own, err := readSkillOwnFiles(skillDir)
	if err != nil {
		return nil, nil, err
	}
	if len(own) > 0 {
		stack = append(stack, cdomain.StackLayer{Name: man.Name, Kind: cdomain.KindSkill, Source: cdomain.SourceLocal, Files: own})
	}
	return stack, pins, nil
}

// resolveDependency fetches a pulled layer (OCI or git) and captures its pin.
func (a *App) resolveDependency(ctx context.Context, dep domain.Dependency) (*cdomain.StackLayer, *cdomain.Pin, error) {
	kind := cdomain.KindSkill
	if dep.Kind == cdomain.KindOverlay {
		kind = cdomain.KindOverlay
	}

	switch {
	case dep.OCI != "":
		full := dep.OCI
		if dep.Version != "" {
			full += ":" + domain.OCITag(dep.Version)
		}
		man, err := a.OCI.Pull(ctx, full)
		if err != nil {
			return nil, nil, fmt.Errorf("resolve OCI dependency %q: %w", dep.Name, err)
		}
		pin := &cdomain.Pin{SourceType: cdomain.SourceKind{Value: cdomain.SourceOCI}, Source: dep.OCI, Version: dep.Version, Digest: man.Digest}
		layer, err := layerFromBlobs(dep.Name, kind, cdomain.SourceOCI, man, pin)
		return layer, pin, err

	case dep.Git != "":
		gc := &git.Client{Username: a.Opts.Username, Password: a.Opts.Password}
		res, err := gc.Resolve(dep.Git, dep.Ref, dep.Subpath)
		if err != nil {
			return nil, nil, fmt.Errorf("resolve git dependency %q: %w", dep.Name, err)
		}
		pin := &cdomain.Pin{SourceType: cdomain.SourceKind{Value: cdomain.SourceGit}, Source: dep.Git, Version: dep.Ref, Commit: res.Commit, TreeSha: res.TreeSha, Subpath: dep.Subpath}
		layer := &cdomain.StackLayer{Name: dep.Name, Kind: kind, Source: cdomain.SourceGit, Pin: pin}
		if kind == cdomain.KindOverlay {
			if err := fillOverlayFromFiles(layer, res.Files); err != nil {
				return nil, nil, err
			}
		} else {
			layer.Files = res.Files
		}
		return layer, pin, nil

	default:
		return nil, nil, fmt.Errorf("dependency %q declares neither oci: nor git:", dep.Name)
	}
}

// layerFromBlobs builds a layer from a pulled OCI artifact (skill or overlay).
func layerFromBlobs(name, kind, source string, man *oci.Manifest, pin *cdomain.Pin) (*cdomain.StackLayer, error) {
	layer := &cdomain.StackLayer{Name: name, Kind: kind, Source: source, Pin: pin}
	files, err := contentFiles(man)
	if err != nil {
		return nil, err
	}
	if kind == cdomain.KindOverlay {
		if err := fillOverlayFromArtifact(layer, man, files); err != nil {
			return nil, err
		}
		return layer, nil
	}
	layer.Files = files
	return layer, nil
}

// fillOverlayFromArtifact parses an overlay OCI artifact: Overlay.yaml from the
// config blob, payload files from the content layer.
func fillOverlayFromArtifact(layer *cdomain.StackLayer, man *oci.Manifest, files map[string][]byte) error {
	ov, err := cdomain.ParseOverlay(man.Config.Data)
	if err != nil {
		return err
	}
	if msgs := ov.Validate(); len(msgs) > 0 {
		return fmt.Errorf("overlay %q is invalid: %s", layer.Name, strings.Join(msgs, "; "))
	}
	layer.Operations = ov.Operations
	layer.PayloadFiles = files
	return nil
}

// fillOverlayFromFiles parses an overlay laid out as files (git subtree / local):
// Overlay.yaml at the root plus files/ payloads.
func fillOverlayFromFiles(layer *cdomain.StackLayer, files map[string][]byte) error {
	data, ok := files["Overlay.yaml"]
	if !ok {
		return fmt.Errorf("overlay %q: Overlay.yaml not found", layer.Name)
	}
	ov, err := cdomain.ParseOverlay(data)
	if err != nil {
		return err
	}
	if msgs := ov.Validate(); len(msgs) > 0 {
		return fmt.Errorf("overlay %q is invalid: %s", layer.Name, strings.Join(msgs, "; "))
	}
	layer.Operations = ov.Operations
	layer.PayloadFiles = files
	return nil
}

// loadLocalOverlays reads overlays/<name>/Overlay.yaml (+ files/) into overlay
// layers, ordered by directory name.
func loadLocalOverlays(skillDir string) ([]cdomain.StackLayer, error) {
	overlaysDir := filepath.Join(skillDir, "overlays")
	entries, err := os.ReadDir(overlaysDir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var layers []cdomain.StackLayer
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dir := filepath.Join(overlaysDir, e.Name())
		files, err := readDirFiles(dir)
		if err != nil {
			return nil, err
		}
		layer := cdomain.StackLayer{Name: e.Name(), Kind: cdomain.KindOverlay, Source: cdomain.SourceLocal}
		if err := fillOverlayFromFiles(&layer, files); err != nil {
			return nil, err
		}
		layers = append(layers, layer)
	}
	return layers, nil
}

// readSkillOwnFiles reads a skill dir's own files, excluding Epos.yaml and the
// overlays/ directory (which are stack inputs, not content).
func readSkillOwnFiles(skillDir string) (map[string][]byte, error) {
	all, err := readDirFiles(skillDir)
	if err != nil {
		return nil, err
	}
	out := map[string][]byte{}
	for rel, data := range all {
		if rel == "Epos.yaml" || strings.HasPrefix(rel, "overlays/") {
			continue
		}
		out[rel] = data
	}
	return out, nil
}

func readDirFiles(dir string) (map[string][]byte, error) {
	out := map[string][]byte{}
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		out[filepath.ToSlash(rel)] = data
		return nil
	})
	return out, err
}
