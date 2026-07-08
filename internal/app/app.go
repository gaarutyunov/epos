// Package app is Epos's application service: it orchestrates the bounded-context
// logic (packaging, composition, install, registry, signing) behind the CLI
// verbs. It is the composition seam the cmd/epos entrypoint drives.
package app

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Masterminds/semver/v3"

	"github.com/gaarutyunov/epos/internal/infrastructure/kube"
	"github.com/gaarutyunov/epos/internal/infrastructure/oci"
	pkggw "github.com/gaarutyunov/epos/internal/packaging/adapter/out/gateway"
	pkgin "github.com/gaarutyunov/epos/internal/packaging/app/port/in"
	pkgusecase "github.com/gaarutyunov/epos/internal/packaging/app/usecase"
	"github.com/gaarutyunov/epos/internal/packaging/domain"
	"github.com/gaarutyunov/epos/internal/signing/verify"
)

// Options configures the application service.
type Options struct {
	// DefaultRegistry resolves bare skill names (e.g. "pdf-tools") to a full
	// registry reference. Set from --registry or EPOS_DEFAULT_REGISTRY.
	DefaultRegistry string
	// PlainHTTP forces http:// (local/test registries).
	PlainHTTP bool
	// Username/Password are the client's own credentials, relayed to registries
	// for push/pull (never stored by Epos).
	Username string
	Password string
	// WorkDir is the project directory for file materialization and the lockfile.
	WorkDir string
	// Kubeconfig is the kubeconfig path for the ConfigMap target.
	Kubeconfig string
	// Out/Err are the CLI's output streams.
	Out io.Writer
	Err io.Writer
}

// App is the application service.
type App struct {
	Opts Options
	OCI  *oci.Client
	// Kube is the cluster client for the ConfigMap target (lazily built from
	// Kubeconfig). Only the install --target=configmap path holds cluster creds.
	Kube       *kube.Client
	Kubeconfig string

	// LastVerify holds the most recent signature verification result (for
	// status reporting and tests).
	LastVerify *verify.Result
}

// New constructs an App with an OCI client bound to the options' credentials.
func New(opts Options) *App {
	if opts.Out == nil {
		opts.Out = os.Stdout
	}
	if opts.Err == nil {
		opts.Err = os.Stderr
	}
	if opts.WorkDir == "" {
		opts.WorkDir, _ = os.Getwd()
	}
	c := &oci.Client{PlainHTTP: opts.PlainHTTP}
	if opts.Username != "" || opts.Password != "" {
		c.Auth = &oci.Auth{Username: opts.Username, Password: opts.Password}
	}
	return &App{Opts: opts, OCI: c, Kubeconfig: opts.Kubeconfig}
}

// Create scaffolds a new skill package directory (SPEC §4.1).
func (a *App) Create(name string) error {
	if msgs := domain.ValidateManifest(&domain.Manifest{Name: name, Version: "0.1.0", Description: "A new Epos skill."}, name); len(msgs) > 0 {
		return fmt.Errorf("invalid skill name: %s", strings.Join(msgs, "; "))
	}
	dir := filepath.Join(a.Opts.WorkDir, name)
	if _, err := os.Stat(dir); err == nil {
		return fmt.Errorf("directory %q already exists", dir)
	}
	files := map[string]string{
		"Epos.yaml":              fmt.Sprintf("apiVersion: epos/v1\nname: %s\nversion: 0.1.0\ndescription: A new Epos skill.\nkeywords: []\n", name),
		"values.yaml":            "features:\n  advanced: false\n",
		"SKILL.md":               fmt.Sprintf("---\nname: %s\ndescription: A new Epos skill.\n---\n\n# %s\n\nDescribe your skill here.\n{{- if .Values.features.advanced }}\nSee also: {{ includeReference \"references/advanced.md\" }}\n{{- end }}\n", name, name),
		"references/advanced.md": "# Advanced usage\n",
	}
	for rel, content := range files {
		full := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			return err
		}
	}
	fmt.Fprintf(a.Opts.Out, "Created skill package %q\n", dir)
	return nil
}

// Package validates and builds the OCI artifact for a package directory by
// driving the PackageSkill use case through the PackagingPort (SPEC §4.1, §2.3),
// returning the OCI-layout dir and manifest digest.
func (a *App) Package(ctx context.Context, path string) (layoutDir, digest string, err error) {
	if msgs, lerr := domain.LintDir(path); lerr != nil {
		return "", "", lerr
	} else if len(msgs) > 0 {
		return "", "", fmt.Errorf("validation failed:\n  - %s", strings.Join(msgs, "\n  - "))
	}
	interactor := pkgusecase.NewPackageSkillInteractor(pkggw.NewPackagingPortImpl(a.Opts.WorkDir))
	out, err := interactor.PackageSkill(pkgin.PackageSkillInput{Request: domain.PackageRequest{SourceDir: path}})
	if err != nil {
		return "", "", err
	}
	art := out.Artifact
	layoutDir = filepath.Join(a.Opts.WorkDir, fmt.Sprintf("%s-%s.epos", art.Ref.Repo, art.Ref.Tag))
	digest = art.Digest.Algo + ":" + art.Digest.Value
	fmt.Fprintf(a.Opts.Out, "Packaged %s:%s → %s (%s)\n", art.Ref.Repo, art.Ref.Tag, layoutDir, digest)
	return layoutDir, digest, nil
}

// Lint validates metadata, template, and dangling references (SPEC §3.5). It
// returns the messages and whether the package is valid.
func (a *App) Lint(path string) (ok bool, msgs []string, err error) {
	msgs, err = domain.LintDir(path)
	if err != nil {
		return false, nil, err
	}
	return len(msgs) == 0, msgs, nil
}

// Push builds the artifact from a package directory and pushes it to ref via
// ORAS, returning the manifest digest (SPEC §4.1). The pushed tag resolves to
// this digest.
func (a *App) Push(ctx context.Context, path, ref string) (string, error) {
	art, err := domain.BuildArtifact(path)
	if err != nil {
		return "", err
	}
	full := a.qualify(ref)
	desc, err := a.OCI.Push(ctx, full, domain.MediaTypeSkillConfig, art.Config.Data,
		[]oci.Blob{{MediaType: art.Content.MediaType, Data: art.Content.Data}},
		domain.MediaTypeSkillConfig,
		map[string]string{"org.opencontainers.image.title": art.Manifest.Name, "org.opencontainers.image.version": art.Manifest.Version})
	if err != nil {
		return "", err
	}
	fmt.Fprintf(a.Opts.Out, "Pushed %s → %s\n", full, desc.Digest)
	return desc.Digest.String(), nil
}

// Pull fetches a skill artifact and unpacks its content into destDir (SPEC §4.1).
func (a *App) Pull(ctx context.Context, ref, destDir string) (*oci.Manifest, error) {
	full, err := a.ResolveRef(ctx, ref, "")
	if err != nil {
		return nil, err
	}
	man, err := a.OCI.Pull(ctx, full)
	if err != nil {
		return nil, err
	}
	if destDir != "" {
		if err := os.MkdirAll(destDir, 0o755); err != nil {
			return nil, err
		}
		for _, l := range man.Layers {
			if l.MediaType == domain.MediaTypeSkillContent {
				if err := domain.UnpackContent(l.Data, destDir); err != nil {
					return nil, err
				}
			}
		}
	}
	return man, nil
}

// Show returns the metadata (config blob) of a skill reference (SPEC §4.1).
func (a *App) Show(ctx context.Context, ref string) (*domain.Manifest, error) {
	full, err := a.ResolveRef(ctx, ref, "")
	if err != nil {
		return nil, err
	}
	man, err := a.OCI.Pull(ctx, full)
	if err != nil {
		return nil, err
	}
	return domain.ParseManifest(man.Config.Data)
}

// qualify turns a bare name into DefaultRegistry/name, leaving full refs intact.
func (a *App) qualify(ref string) string {
	if strings.Contains(ref, "/") || a.Opts.DefaultRegistry == "" {
		return ref
	}
	return strings.TrimRight(a.Opts.DefaultRegistry, "/") + "/" + ref
}

// ResolveRef resolves a bare name or partial ref to a full registry/repo:tag,
// applying the default registry, an explicit version, or the highest published
// tag. Digest-pinned refs (…@sha256:…) pass through.
func (a *App) ResolveRef(ctx context.Context, ref, version string) (string, error) {
	full := a.qualify(ref)
	if strings.Contains(full, "@") {
		return full, nil
	}
	if hasTag(full) {
		return full, nil
	}
	if version != "" {
		return full + ":" + domain.OCITag(version), nil
	}
	tag, err := a.highestTag(ctx, full)
	if err != nil {
		return "", fmt.Errorf("resolve %q: %w", ref, err)
	}
	return full + ":" + tag, nil
}

// highestTag lists a repository's tags and returns the highest SemVer tag.
func (a *App) highestTag(ctx context.Context, repoRef string) (string, error) {
	tags, err := a.OCI.Tags(ctx, repoRef)
	if err != nil {
		return "", err
	}
	var versions []*semver.Version
	byVer := map[*semver.Version]string{}
	for _, t := range tags {
		v, err := semver.NewVersion(domain.VersionFromOCITag(t))
		if err != nil {
			continue
		}
		versions = append(versions, v)
		byVer[v] = t
	}
	if len(versions) == 0 {
		if len(tags) > 0 {
			sort.Strings(tags)
			return tags[len(tags)-1], nil
		}
		return "", fmt.Errorf("no tags found")
	}
	sort.Slice(versions, func(i, j int) bool { return versions[i].LessThan(versions[j]) })
	return byVer[versions[len(versions)-1]], nil
}

// hasTag reports whether a registry/repo reference already carries a :tag.
func hasTag(ref string) bool {
	slash := strings.LastIndex(ref, "/")
	lastSeg := ref
	if slash >= 0 {
		lastSeg = ref[slash+1:]
	}
	return strings.Contains(lastSeg, ":")
}
