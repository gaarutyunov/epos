package install

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2/content"
	"oras.land/oras-go/v2/content/oci"
	"oras.land/oras-go/v2/errdef"

	"github.com/gaarutyunov/epos/internal/artifact"
	"github.com/gaarutyunov/epos/internal/store"
)

// maxLayer caps how much a content layer may expand to. A skill is Markdown
// and YAML; anything past this is a decompression bomb rather than a skill,
// and the extraction runs before the user has seen a single file of it.
const maxLayer = 256 << 20

// Options is one `epos install` (SPEC.md 10.4).
type Options struct {
	// Dir is the worktree the skill installs into, and where skills.json and
	// skills.lock.json live.
	Dir string
	// Ref is what the user asked for: a store tag, or the registry reference
	// the skill was pulled from.
	Ref string
	// ValueFiles are -f, in order.
	ValueFiles []string
	// Sets are --set k=v, applied after every file.
	Sets []string
}

// Result is what an install did.
type Result struct {
	Name      string
	Version   string
	Digest    string
	BasePaths []string
}

// Install writes a skill from the local store into a worktree.
//
// The whole operation runs inside the store's *shared* lock (9.2). Shared is
// the point: two worktrees pinning two different digests out of one store is
// the workflow 10.2 exists for, and an exclusive lock would make them queue.
// The lock is held across the read and the extraction rather than only the
// read, because a prune running in between would be free to collect the layer
// this install is halfway through.
//
// The store is never written. An install resolves what is already there; a ref
// the store does not hold is an error naming what to run first, not a silent
// fetch — fetching takes the exclusive lock, and taking it here would give the
// serialisation back.
func Install(ctx context.Context, s *store.Store, opts Options) (Result, error) {
	tag, err := StoreTag(opts.Ref)
	if err != nil {
		return Result{}, err
	}
	version := tag[strings.LastIndex(tag, ":")+1:]

	values, err := LoadValues(opts.ValueFiles, opts.Sets)
	if err != nil {
		return Result{}, err
	}
	manifest, err := LoadManifest(opts.Dir)
	if err != nil {
		return Result{}, err
	}
	lock, err := LoadLock(opts.Dir)
	if err != nil {
		return Result{}, err
	}
	basePaths := manifest.BasePaths()

	var res Result
	err = s.Read(ctx, func(ctx context.Context, st *oci.Store) error {
		desc, err := st.Resolve(ctx, tag)
		if err != nil {
			if errors.Is(err, errdef.ErrNotFound) {
				return fmt.Errorf("the local store holds no %s; pull or build it first", tag)
			}
			return fmt.Errorf("resolve %s: %w", tag, err)
		}

		skill, err := read(ctx, st, desc)
		if err != nil {
			return err
		}

		rendered, err := render(skill, values)
		if err != nil {
			return err
		}
		for _, base := range basePaths {
			if err := write(opts.Dir, base, skill.name, rendered); err != nil {
				return err
			}
		}

		res = Result{
			Name:      skill.name,
			Version:   version,
			Digest:    desc.Digest.String(),
			BasePaths: basePaths,
		}
		return nil
	})
	if err != nil {
		return Result{}, err
	}

	manifest.Declare(Declared{Name: res.Name, Ref: opts.Ref})
	if err := manifest.Save(opts.Dir); err != nil {
		return Result{}, err
	}
	lock.Pin(Locked{
		Name:      res.Name,
		Version:   res.Version,
		Ref:       opts.Ref,
		Digest:    res.Digest,
		BasePaths: res.BasePaths,
	})
	if err := lock.Save(opts.Dir); err != nil {
		return Result{}, err
	}
	return res, nil
}

// Uninstall removes a skill from a worktree and from both manifests.
//
// The directories come from the lock rather than from skills.json, so a skill
// installed before additionalBasePaths changed is still removed from where it
// actually went.
func Uninstall(dir, name string) ([]string, error) {
	lock, err := LoadLock(dir)
	if err != nil {
		return nil, err
	}
	entry, ok := lock.Unpin(name)
	if !ok {
		return nil, fmt.Errorf("%s is not installed here", name)
	}

	removed := make([]string, 0, len(entry.BasePaths))
	for _, base := range entry.BasePaths {
		target := filepath.Join(dir, filepath.FromSlash(base), name)
		if err := os.RemoveAll(target); err != nil {
			return nil, fmt.Errorf("remove %s: %w", base+"/"+name, err)
		}
		removed = append(removed, base+"/"+name)
	}

	manifest, err := LoadManifest(dir)
	if err != nil {
		return nil, err
	}
	manifest.Undeclare(name)
	if err := manifest.Save(dir); err != nil {
		return nil, err
	}
	if err := lock.Save(dir); err != nil {
		return nil, err
	}
	return removed, nil
}

// List is what the worktree has pinned, which is what `epos ls` prints.
//
// Read out of the lock and not off the filesystem: the lock is the truth
// (10.2), and a directory somebody dropped into .claude/skills by hand is not
// something this worktree installed.
func List(dir string) ([]Locked, error) {
	lock, err := LoadLock(dir)
	if err != nil {
		return nil, err
	}
	return lock.Skills, nil
}

// StoreTag turns what the user typed into the tag the local store holds.
//
// `epos pull` tags a registry reference under the skill's own name (2.1: the
// repository identifies the skill), so `install ghcr.io/acme/agent-skills/pdf:1.0.0`
// and `install pdf:1.0.0` name the same artifact and must resolve alike.
//
// The version separator is the last colon *after* the last slash. A registry
// may carry a port, and cutting at the first colon splits
// "127.0.0.1:45100/demo/agent-skills/pdf:1.0.0" in the wrong place entirely.
func StoreTag(ref string) (string, error) {
	colon := strings.LastIndex(ref, ":")
	if colon < 0 || colon < strings.LastIndex(ref, "/") {
		return "", fmt.Errorf("reference %q has no version; write it as <name>:<version>", ref)
	}
	name, version := ref[:colon], ref[colon+1:]
	if slash := strings.LastIndex(name, "/"); slash >= 0 {
		name = name[slash+1:]
	}
	if name == "" || version == "" {
		return "", fmt.Errorf("reference %q is not <name>:<version>", ref)
	}
	return name + ":" + version, nil
}

// packed is an artifact read out of the store, ready to render.
type packed struct {
	name string
	// files are the skill's contents keyed by their path inside the skill,
	// slash-separated, with the "<name>/" layer root already stripped.
	files map[string][]byte
	// stages maps those same paths to the Skillfile stage that contributed
	// them (8.4), for the files a COPY --from named. Empty for a packed skill
	// and for a single-stage build.
	stages map[string]string
}

// read pulls the manifest, the config and the one content layer.
func read(ctx context.Context, st *oci.Store, desc ocispec.Descriptor) (packed, error) {
	body, err := content.FetchAll(ctx, st, desc)
	if err != nil {
		return packed{}, fmt.Errorf("read the manifest: %w", err)
	}
	var m ocispec.Manifest
	if err := json.Unmarshal(body, &m); err != nil {
		return packed{}, fmt.Errorf("parse the manifest: %w", err)
	}
	// 2.1: exactly one content layer. Anything else is not a skill artifact,
	// and guessing which layer was meant is how a client starts accepting
	// shapes the spec does not define.
	if len(m.Layers) != 1 {
		return packed{}, fmt.Errorf("the artifact has %d layers; a skill has exactly one",
			len(m.Layers))
	}

	// The config rides inline in its descriptor (2.1), so the usual case costs
	// no fetch; a manifest written by some other client may not inline it.
	configJSON := m.Config.Data
	if len(configJSON) == 0 {
		if configJSON, err = content.FetchAll(ctx, st, m.Config); err != nil {
			return packed{}, fmt.Errorf("read the config blob: %w", err)
		}
	}
	var cfg artifact.Config
	if err := json.Unmarshal(configJSON, &cfg); err != nil {
		return packed{}, fmt.Errorf("parse the config blob: %w", err)
	}
	if cfg.Name() == "" {
		return packed{}, fmt.Errorf("the artifact's config blob has no name")
	}

	layer, err := content.FetchAll(ctx, st, m.Layers[0])
	if err != nil {
		return packed{}, fmt.Errorf("read the content layer: %w", err)
	}
	files, err := extract(layer, cfg.Name())
	if err != nil {
		return packed{}, err
	}

	stages, err := readStages(m.Annotations)
	if err != nil {
		return packed{}, err
	}
	return packed{name: cfg.Name(), files: files, stages: stages}, nil
}

// readStages decodes the stage provenance the build recorded (8.4, 10.3).
//
// Absent for a packed skill, and for a build no COPY --from ever composed.
// Both render entirely in the top-level scope, which is the right answer: a
// skill with one stage has one scope.
func readStages(annotations map[string]string) (map[string]string, error) {
	raw, ok := annotations[artifact.StagesAnnotation]
	if !ok || raw == "" {
		return nil, nil
	}
	var stages map[string]string
	if err := json.Unmarshal([]byte(raw), &stages); err != nil {
		return nil, fmt.Errorf("parse the %s annotation: %w", artifact.StagesAnnotation, err)
	}
	return stages, nil
}

// extract unpacks the content layer into path → contents.
//
// The entry names inside an OCI layer are forward-slashed on every platform
// (2.5) and are kept that way here: they are the keys the stage provenance and
// the renderer use, and turning them into filesystem paths is write's job and
// happens once, at the end.
func extract(layer []byte, name string) (map[string][]byte, error) {
	gr, err := gzip.NewReader(bytes.NewReader(layer))
	if err != nil {
		return nil, fmt.Errorf("read the content layer: %w", err)
	}
	defer func() { _ = gr.Close() }()

	root := name + "/"
	files := map[string][]byte{}
	tr := tar.NewReader(io.LimitReader(gr, maxLayer))
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("read the content layer: %w", err)
		}

		// 2.5 rejects these at install as well as at pack: a layer is not
		// necessarily one this epos produced.
		switch h.Typeflag {
		case tar.TypeDir, tar.TypeReg:
		case tar.TypeSymlink, tar.TypeLink:
			return nil, fmt.Errorf("%s: the layer contains a link, which is not allowed", h.Name)
		default:
			return nil, fmt.Errorf("%s: the layer contains a %c entry, which is not a file",
				h.Name, h.Typeflag)
		}
		if strings.Contains(h.Name, `\`) {
			return nil, fmt.Errorf("%s: layer entry names are slash-separated", h.Name)
		}
		if h.Name == root || h.Name == name {
			continue
		}
		if !strings.HasPrefix(h.Name, root) {
			return nil, fmt.Errorf("%s: the layer is not rooted at %s", h.Name, root)
		}

		rel := strings.TrimPrefix(h.Name, root)
		rel = strings.TrimSuffix(rel, "/")
		if err := checkEntry(rel); err != nil {
			return nil, err
		}
		if h.Typeflag == tar.TypeDir {
			// Directories are implied by the files under them; an empty one
			// carries nothing a skill can use.
			continue
		}

		body, err := io.ReadAll(tr)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", h.Name, err)
		}
		files[rel] = body
	}

	if _, ok := files[artifact.SkillFile]; !ok {
		return nil, fmt.Errorf("the artifact holds no %s", artifact.SkillFile)
	}
	return files, nil
}

// checkEntry rejects a layer entry that would escape the skill root (2.5).
func checkEntry(rel string) error {
	switch {
	case rel == "":
		return fmt.Errorf("the layer holds an entry with an empty name")
	case path.IsAbs(rel):
		return fmt.Errorf("%s: absolute paths are not allowed", rel)
	case rel == ".." || strings.HasPrefix(rel, "../") || strings.Contains(rel, "/../"):
		return fmt.Errorf("%s: .. escapes the skill root", rel)
	}
	if cleaned := path.Clean(rel); cleaned != rel {
		return fmt.Errorf("%s: entry name is not in canonical form (%s)", rel, cleaned)
	}
	return nil
}

// render substitutes the values into every file, each in the scope of the
// stage that contributed it (10.3).
//
// Everything is rendered before anything is written, so a template that fails
// leaves the worktree exactly as it was rather than half-installed.
func render(skill packed, values Values) (map[string][]byte, error) {
	out := make(map[string][]byte, len(skill.files))
	// Sorted, so the file a broken template is reported against is the same
	// one on every run and on every platform.
	for _, rel := range sortedPaths(skill.files) {
		body, err := Render(rel, skill.files[rel], values.Scope(skill.stages[rel]))
		if err != nil {
			return nil, err
		}
		out[rel] = body
	}
	return out, nil
}

// write puts the rendered skill into one base path.
//
// The skill directory is replaced rather than merged into: a reinstall that
// left the previous version's files behind would produce a directory that is
// neither version, and the lock would name a digest the worktree does not hold.
//
// Modes are fixed at 0644 and 0755, the same normalisation packing applies
// (2.4). Carrying the tar's modes would mean an install differed by whichever
// umask packed it.
func write(dir, base, name string, files map[string][]byte) error {
	root := filepath.Join(dir, filepath.FromSlash(base), name)
	if err := os.RemoveAll(root); err != nil {
		return fmt.Errorf("replace %s: %w", base+"/"+name, err)
	}

	for _, rel := range sortedPaths(files) {
		target := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return fmt.Errorf("create %s: %w", filepath.Dir(rel), err)
		}
		if err := os.WriteFile(target, files[rel], 0o644); err != nil {
			return fmt.Errorf("write %s: %w", rel, err)
		}
	}
	return nil
}

func sortedPaths(files map[string][]byte) []string {
	out := make([]string, 0, len(files))
	for rel := range files {
		out = append(out, rel)
	}
	sort.Strings(out)
	return out
}
