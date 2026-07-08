package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"

	"github.com/gaarutyunov/epos/internal/infrastructure/oci"
	gw "github.com/gaarutyunov/epos/internal/install/adapter/out/gateway"
	iin "github.com/gaarutyunov/epos/internal/install/app/port/in"
	iout "github.com/gaarutyunov/epos/internal/install/app/port/out"
	"github.com/gaarutyunov/epos/internal/install/app/usecase"
	"github.com/gaarutyunov/epos/internal/install/configmap"
	idomain "github.com/gaarutyunov/epos/internal/install/domain"
	"github.com/gaarutyunov/epos/internal/install/lock"
	"github.com/gaarutyunov/epos/internal/packaging/domain"
	"github.com/gaarutyunov/epos/internal/render"
	signrepo "github.com/gaarutyunov/epos/internal/signing/adapter/out/repository"
	signin "github.com/gaarutyunov/epos/internal/signing/app/port/in"
	signusecase "github.com/gaarutyunov/epos/internal/signing/app/usecase"
	signdomain "github.com/gaarutyunov/epos/internal/signing/domain"
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
	// Values are the merged -f/--set override values (Helm precedence already
	// applied by the CLI, SPEC §3.3). They are layered over the package
	// values.yaml at render time and snapshotted into the revision (§5.3).
	Values map[string]any
}

// valuesJSON marshals the override values to the JSON carried on InstallRequest.
func (o InstallOpts) valuesJSON() string {
	if len(o.Values) == 0 {
		return ""
	}
	b, err := json.Marshal(o.Values)
	if err != nil {
		return ""
	}
	return string(b)
}

func (a *App) lockfilePath() string { return filepath.Join(a.Opts.WorkDir, lock.LockfileName) }

// installPorts constructs the Install context's driven-port adapters (the
// pluggable §11 seams) for the current work directory and cluster client.
func (a *App) installPorts() (*gw.MaterializePortImpl, *gw.RevisionStoreImpl) {
	mat := gw.NewMaterializePortImpl(a.Opts.WorkDir, a.OCI, a.Kube)
	store := gw.NewRevisionStoreImpl(a.Opts.WorkDir, a.Kube)
	return mat, store
}

// resolveSubject splits a full ref into (registry/repo, manifest descriptor) for
// referrers/signature lookup.
func (a *App) resolveSubject(ctx context.Context, full string) (string, ocispec.Descriptor, error) {
	desc, err := a.OCI.Resolve(ctx, full)
	if err != nil {
		return "", ocispec.Descriptor{}, err
	}
	return stripTag(full), desc, nil
}

// verifySignature drives the VerifySignature use case (verify-when-present)
// through the SignaturePort, recording the result (SPEC §7.2).
func (a *App) verifySignature(ctx context.Context, full string, requireSignature bool) error {
	repoRef, subject, err := a.resolveSubject(ctx, full)
	if err != nil {
		return err
	}
	interactor := signusecase.NewVerifySignatureInteractor(signrepo.NewSignaturePortImpl(a.OCI, repoRef))
	out, verr := interactor.VerifySignature(signin.VerifySignatureInput{Request: signdomain.VerifyRequest{
		SubjectDigest: subject.Digest.String(),
		Policy:        signdomain.VerifyPolicy{RequireSignature: requireSignature},
	}})
	a.LastVerify = &verify.Result{Verified: out.Result.Verified, Present: out.Result.Present, Messages: out.Result.Messages}
	if verr != nil {
		return fmt.Errorf("signature verification: %w", verr)
	}
	return nil
}

// Install resolves a skill, verifies signatures, and drives the InstallSkill use
// case (materialize + record a revision) through the Install context's ports
// (SPEC §4.2, §5). It honors --target and --frozen.
func (a *App) Install(ctx context.Context, release, ref string, opts InstallOpts) (int, error) {
	if opts.Target == "" {
		opts.Target = TargetFiles
	}
	if opts.Frozen {
		if err := a.verifyFrozen(ctx, release, ref, opts); err != nil {
			return 0, err
		}
	}
	full, err := a.ResolveRef(ctx, ref, opts.Version)
	if err != nil {
		return 0, err
	}
	if err := a.verifySignature(ctx, full, opts.RequireSignature); err != nil {
		return 0, err
	}

	mat, store := a.installPorts()
	interactor := usecase.NewInstallSkillInteractor(mat, store)
	out, err := interactor.InstallSkill(iin.InstallSkillInput{Request: idomain.InstallRequest{
		ReleaseName: release, SkillID: full, Target: idomain.Target{Value: opts.Target}, Namespace: opts.Namespace, Values: opts.valuesJSON(),
	}})
	if err != nil {
		return 0, err
	}
	fmt.Fprintf(a.Opts.Out, "Installed %s (release %q) revision %d → %s\n", full, release, out.Result.Revision, mat.LastDigest())
	return int(out.Result.Revision), nil
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

// Upgrade drives the UpgradeSkill use case (fetch newer version, re-materialize,
// append a revision) through the ports (SPEC §4.2). No three-way merge.
func (a *App) Upgrade(ctx context.Context, release, ref string, opts InstallOpts) (int, error) {
	if opts.Target == "" {
		opts.Target = TargetFiles
	}
	full, err := a.ResolveRef(ctx, ref, opts.Version)
	if err != nil {
		return 0, err
	}
	if err := a.verifySignature(ctx, full, opts.RequireSignature); err != nil {
		return 0, err
	}
	mat, store := a.installPorts()
	interactor := usecase.NewUpgradeSkillInteractor(mat, store)
	out, err := interactor.UpgradeSkill(iin.UpgradeSkillInput{Request: idomain.InstallRequest{
		ReleaseName: release, SkillID: full, Target: idomain.Target{Value: opts.Target}, Namespace: opts.Namespace, Values: opts.valuesJSON(),
	}})
	if err != nil {
		return 0, err
	}
	fmt.Fprintf(a.Opts.Out, "Upgraded release %q to revision %d → %s\n", release, out.Result.Revision, mat.LastDigest())
	return int(out.Result.Revision), nil
}

// Rollback drives the RollbackSkill use case (restore a whole prior bundle,
// record a new revision) through the ports (SPEC §4.2, §5.3).
func (a *App) Rollback(ctx context.Context, release string, toRevision int, opts InstallOpts) (int, error) {
	if opts.Target == "" {
		opts.Target = TargetFiles
	}
	mat, store := a.installPorts()
	interactor := usecase.NewRollbackSkillInteractor(mat, store, opts.Target, opts.Namespace)
	out, err := interactor.RollbackSkill(iin.RollbackSkillInput{Request: idomain.RollbackRequest{
		ReleaseName: release, ToRevision: int64(toRevision),
	}})
	if err != nil {
		return 0, err
	}
	fmt.Fprintf(a.Opts.Out, "Rolled back release %q to revision %d, recorded as revision %d\n", release, toRevision, out.Result.Revision)
	return int(out.Result.Revision), nil
}

// Uninstall drives the UninstallSkill use case (SPEC §4.2).
func (a *App) Uninstall(ctx context.Context, release string, keepHistory bool, opts InstallOpts) error {
	if opts.Target == "" {
		opts.Target = TargetFiles
	}
	mat, store := a.installPorts()
	interactor := usecase.NewUninstallSkillInteractor(mat, store, opts.Target, opts.Namespace)
	// Remove materialized files first.
	if err := mat.Remove(release, opts.Target, opts.Namespace); err != nil {
		return err
	}
	if keepHistory {
		return nil
	}
	_, err := interactor.UninstallSkill(iin.UninstallSkillInput{ReleaseName: release})
	return err
}

// Status reports the current revision of a release, read through the
// RevisionStore port (SPEC §4.2).
func (a *App) Status(ctx context.Context, release string, opts InstallOpts) (*iout.RevisionInfo, error) {
	if opts.Target == "" {
		opts.Target = TargetFiles
	}
	_, store := a.installPorts()
	revs, err := store.History(release, opts.Target, opts.Namespace)
	if err != nil {
		return nil, err
	}
	if len(revs) == 0 {
		return nil, fmt.Errorf("release %q not found", release)
	}
	last := revs[len(revs)-1]
	return &last, nil
}

// History lists retained revisions, read through the RevisionStore port.
func (a *App) History(ctx context.Context, release string, opts InstallOpts) ([]iout.RevisionInfo, error) {
	if opts.Target == "" {
		opts.Target = TargetFiles
	}
	_, store := a.installPorts()
	return store.History(release, opts.Target, opts.Namespace)
}

// Template renders a resolved skill and returns its rendered files without
// touching a cluster (SPEC §4.4). For --target=configmap it emits ConfigMap YAML.
func (a *App) Template(ctx context.Context, release, ref string, opts InstallOpts) (string, error) {
	full, err := a.ResolveRef(ctx, ref, opts.Version)
	if err != nil {
		return "", err
	}
	man, err := a.OCI.Pull(ctx, full)
	if err != nil {
		return "", err
	}
	files, err := extractSkillFiles(man)
	if err != nil {
		return "", err
	}
	// Render SKILL.md through the templating engine with the package values
	// merged with -f/--set overrides (Go text/template + Sprig + includeReference,
	// SPEC §3, §3.3).
	files, _, err = render.Bundle(files, opts.Values)
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
	for _, p := range sortedKeys(files) {
		fmt.Fprintf(&buf, "# %s\n%s\n", p, string(files[p]))
	}
	return buf.String(), nil
}

// ---- helpers ----

func extractSkillFiles(man *oci.Manifest) (map[string][]byte, error) {
	for _, l := range man.Layers {
		if l.MediaType == domain.MediaTypeSkillContent {
			return domain.UnpackTarGz(l.Data)
		}
	}
	return nil, fmt.Errorf("skill artifact has no content layer")
}

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

func sortedKeys(files map[string][]byte) []string {
	out := make([]string, 0, len(files))
	for k := range files {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
