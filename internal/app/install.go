package app

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"

	"github.com/gaarutyunov/epos/internal/infrastructure/oci"
	"github.com/gaarutyunov/epos/internal/install/configmap"
	"github.com/gaarutyunov/epos/internal/install/lock"
	"github.com/gaarutyunov/epos/internal/install/materialize"
	"github.com/gaarutyunov/epos/internal/packaging/domain"
	"github.com/gaarutyunov/epos/internal/signing/verify"
)

// Targets (SPEC §4.4).
const (
	TargetFiles     = "files"
	TargetConfigMap = "configmap"
)

// InstallOpts configures install/upgrade/template.
type InstallOpts struct {
	Target           string
	Namespace        string
	Frozen           bool
	RequireSignature bool
	Version          string
	MountPath        string
}

func (a *App) lockfilePath() string { return filepath.Join(a.Opts.WorkDir, lock.LockfileName) }

func (a *App) releaseDir(release string) string { return filepath.Join(a.Opts.WorkDir, release) }

// resolveSubject splits a full ref into (registry/repo, manifest descriptor) for
// referrers/signature lookup.
func (a *App) resolveSubject(ctx context.Context, full string) (string, ocispec.Descriptor, error) {
	desc, err := a.OCI.Resolve(ctx, full)
	if err != nil {
		return "", ocispec.Descriptor{}, err
	}
	return stripTag(full), desc, nil
}

// fetchBundle resolves a ref, verifies signatures (verify-when-present), pulls
// the artifact, and returns the resolved full ref, its digest, and the unpacked
// skill files.
func (a *App) fetchBundle(ctx context.Context, ref string, opts InstallOpts) (full, digest string, files map[string][]byte, err error) {
	full, err = a.ResolveRef(ctx, ref, opts.Version)
	if err != nil {
		return "", "", nil, err
	}
	repoRef, subject, err := a.resolveSubject(ctx, full)
	if err != nil {
		return "", "", nil, err
	}
	if _, verr := verify.Verify(ctx, a.OCI, repoRef, subject, opts.RequireSignature); verr != nil {
		return "", "", nil, fmt.Errorf("signature verification: %w", verr)
	}
	man, err := a.OCI.Pull(ctx, full)
	if err != nil {
		return "", "", nil, err
	}
	files, err = extractSkillFiles(man)
	if err != nil {
		return "", "", nil, err
	}
	return full, man.Digest, files, nil
}

// Install resolves a skill, verifies signatures, materializes the bundle, and
// records a new revision (SPEC §4.2). It honors --target and --frozen.
func (a *App) Install(ctx context.Context, release, ref string, opts InstallOpts) (int, error) {
	if opts.Target == "" {
		opts.Target = TargetFiles
	}

	if opts.Frozen {
		if err := a.verifyFrozen(ctx, release, ref, opts); err != nil {
			return 0, err
		}
	}

	full, digest, files, err := a.fetchBundle(ctx, ref, opts)
	if err != nil {
		return 0, err
	}
	meta, _ := manifestMeta(files)
	version := opts.Version
	if meta != nil && version == "" {
		version = meta.Version
	}

	switch opts.Target {
	case TargetConfigMap:
		return a.installConfigMap(ctx, release, full, digest, version, files, opts)
	default:
		return a.installFiles(release, full, digest, version, files)
	}
}

// installFiles materializes into <workdir>/<release> and records a lockfile
// revision pinned by digest (SPEC §5).
func (a *App) installFiles(release, full, digest, version string, files map[string][]byte) (int, error) {
	dir := a.releaseDir(release)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return 0, err
	}
	if err := materialize.WriteTree(dir, files); err != nil {
		return 0, err
	}
	lf, err := lock.Load(a.lockfilePath())
	if err != nil {
		return 0, err
	}
	rev := lock.Revision{Version: version, Digest: digest, Registry: stripTag(full)}
	rev.SetFiles(files)
	n := lf.AddRevision(release, rev)
	if err := lf.Save(); err != nil {
		return 0, err
	}
	fmt.Fprintf(a.Opts.Out, "Installed %s (release %q) revision %d → %s\n", full, release, n, digest)
	return n, nil
}

// verifyFrozen enforces the lockfile pin against the current tag resolution: a
// tag/digest mismatch is a hard error (SPEC §5.2), never a silent swap.
func (a *App) verifyFrozen(ctx context.Context, release, ref string, opts InstallOpts) error {
	lf, err := lock.Load(a.lockfilePath())
	if err != nil {
		return err
	}
	cur, err := lf.Current(release)
	if err != nil {
		return nil // nothing pinned yet
	}
	full, err := a.ResolveRef(ctx, ref, opts.Version)
	if err != nil {
		return err
	}
	desc, err := a.OCI.Resolve(ctx, full)
	if err != nil {
		return err
	}
	if cur.Digest != "" && cur.Digest != desc.Digest.String() {
		return fmt.Errorf("digest mismatch: lockfile pins %s but tag %s resolves to %s (refusing silent swap)", cur.Digest, full, desc.Digest)
	}
	return nil
}

// Upgrade fetches a newer version, re-materializes, and appends a new revision
// (SPEC §4.2). No three-way merge; honors --target.
func (a *App) Upgrade(ctx context.Context, release, ref string, opts InstallOpts) (int, error) {
	return a.Install(ctx, release, ref, opts)
}

// Rollback restores a previous bundle in full and records it as a new revision
// (SPEC §4.2, §5.3). --target=files reads the lockfile; --target=configmap reads
// the in-cluster revision records.
func (a *App) Rollback(ctx context.Context, release string, toRevision int, opts InstallOpts) (int, error) {
	if opts.Target == TargetConfigMap {
		return a.rollbackConfigMap(ctx, release, toRevision, opts)
	}
	lf, err := lock.Load(a.lockfilePath())
	if err != nil {
		return 0, err
	}
	prev, err := lf.Get(release, toRevision)
	if err != nil {
		return 0, err
	}
	files, err := prev.FileBytes()
	if err != nil {
		return 0, err
	}
	dir := a.releaseDir(release)
	if err := materialize.WriteTree(dir, files); err != nil {
		return 0, err
	}
	restore := lock.Revision{Version: prev.Version, Digest: prev.Digest, Registry: prev.Registry, Values: prev.Values, Overlays: prev.Overlays, Files: prev.Files}
	n := lf.AddRevision(release, restore)
	if err := lf.Save(); err != nil {
		return 0, err
	}
	fmt.Fprintf(a.Opts.Out, "Rolled back release %q to revision %d, recorded as revision %d\n", release, toRevision, n)
	return n, nil
}

// Uninstall removes a release's materialized files and lockfile entry (SPEC §4.2).
func (a *App) Uninstall(ctx context.Context, release string, keepHistory bool, opts InstallOpts) error {
	if opts.Target == TargetConfigMap {
		return a.uninstallConfigMap(ctx, release, opts)
	}
	lf, err := lock.Load(a.lockfilePath())
	if err != nil {
		return err
	}
	if cur, err := lf.Current(release); err == nil {
		if files, err := cur.FileBytes(); err == nil {
			_ = materialize.RemoveTree(a.releaseDir(release), files)
		}
	}
	_ = os.RemoveAll(a.releaseDir(release))
	if !keepHistory {
		lf.Remove(release)
	}
	return lf.Save()
}

// Status reports the current version/digest of a release (SPEC §4.2).
func (a *App) Status(ctx context.Context, release string, opts InstallOpts) (*lock.Revision, error) {
	if opts.Target == TargetConfigMap {
		return a.statusConfigMap(ctx, release, opts)
	}
	lf, err := lock.Load(a.lockfilePath())
	if err != nil {
		return nil, err
	}
	return lf.Current(release)
}

// History lists retained revisions of a release (SPEC §4.2).
func (a *App) History(ctx context.Context, release string, opts InstallOpts) ([]lock.Revision, error) {
	if opts.Target == TargetConfigMap {
		return a.historyConfigMap(ctx, release, opts)
	}
	lf, err := lock.Load(a.lockfilePath())
	if err != nil {
		return nil, err
	}
	return lf.History(release), nil
}

// TemplateFiles renders a resolved skill and returns its rendered files without
// touching a cluster (SPEC §4.4). For --target=configmap it emits ConfigMap YAML.
func (a *App) Template(ctx context.Context, release, ref string, opts InstallOpts) (string, error) {
	_, _, files, err := a.fetchBundle(ctx, ref, opts)
	if err != nil {
		return "", err
	}
	if opts.Target == TargetConfigMap {
		r, err := configmap.Render(release, opts.Namespace, opts.MountPath, files)
		if err != nil {
			return "", err
		}
		return r.YAML + "\n# --- volume/mount snippet ---\n" + commentBlock(r.MountSnippet), nil
	}
	var buf bytes.Buffer
	for _, p := range materialize.SortedPaths(files) {
		fmt.Fprintf(&buf, "# %s\n%s\n", p, string(files[p]))
	}
	return buf.String(), nil
}

// ---- helpers ----

// extractSkillFiles unpacks the content layer of a pulled skill into a file map.
func extractSkillFiles(man *oci.Manifest) (map[string][]byte, error) {
	for _, l := range man.Layers {
		if l.MediaType == domain.MediaTypeSkillContent {
			return domain.UnpackTarGz(l.Data)
		}
	}
	return nil, fmt.Errorf("skill artifact has no content layer")
}

// manifestMeta parses Epos.yaml from an unpacked skill file map.
func manifestMeta(files map[string][]byte) (*domain.Manifest, error) {
	data, ok := files["Epos.yaml"]
	if !ok {
		return nil, fmt.Errorf("Epos.yaml not found in bundle")
	}
	return domain.ParseManifest(data)
}

// stripTag reduces registry/repo:tag or …@digest to registry/repo.
func stripTag(ref string) string {
	if i := strings.LastIndex(ref, "@"); i >= 0 {
		ref = ref[:i]
	}
	slash := strings.LastIndex(ref, "/")
	if c := strings.LastIndex(ref, ":"); c > slash {
		return ref[:c]
	}
	return ref
}

func commentBlock(s string) string {
	var b strings.Builder
	for _, line := range strings.Split(strings.TrimRight(s, "\n"), "\n") {
		b.WriteString("# ")
		b.WriteString(line)
		b.WriteString("\n")
	}
	return b.String()
}
