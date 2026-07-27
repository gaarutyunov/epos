//go:build integration

package integration

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/cucumber/godog"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/rogpeppe/go-internal/lockedfile"
	"oras.land/oras-go/v2/content"
	"oras.land/oras-go/v2/content/oci"
)

// installerHarness drives `epos install` as a user would: a real binary, a real
// store, real worktrees. Nothing here reaches into the packages under test, and
// — the point of A4 — nothing here starts a registry or a container.
type installerHarness struct {
	bin string

	// eposHome is EPOS_HOME, so the CLI's store lands in the sandbox. No HOME
	// is moved: HOME is read by everything else in the process too, and on
	// Windows is not even the variable os.UserHomeDir consults.
	eposHome string
	// worktrees are two directories with one store between them, which is what
	// 10.2's per-worktree pin is for.
	worktrees []string
	context   string // a build context, for the multi-stage fixture

	// digests are the manifest digests each pack and build reported, by tag.
	digests map[string]string

	runs    []run
	locks   []string // skills.lock.json after each install
	listing string

	held *lockedfile.File
}

// run is one CLI invocation.
type run struct {
	stdout string
	stderr string
	err    error
}

func (r run) failed() bool { return r.err != nil }

func (h *installerHarness) reset(t *testing.T) {
	h.releaseLock()

	root := t.TempDir()
	h.eposHome = filepath.Join(root, "epos")
	h.worktrees = []string{filepath.Join(root, "worktree-one"), filepath.Join(root, "worktree-two")}
	h.context = filepath.Join(root, "context")
	for _, dir := range append([]string{h.eposHome, h.context}, h.worktrees...) {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	h.digests = map[string]string{}
	h.runs = nil
	h.locks = nil
	h.listing = ""
}

// releaseLock lets go of the store lock a scenario asked to be held.
func (h *installerHarness) releaseLock() {
	if h.held != nil {
		_ = h.held.Close()
		h.held = nil
	}
}

// epos runs the CLI in a worktree, with stdout and stderr kept apart: stdout
// carries the one machine-readable line and stderr the paths and warnings.
func (h *installerHarness) epos(dir string, args ...string) run {
	cmd := exec.Command(h.bin, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), eposHomeEnv+"="+h.eposHome)

	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	err := cmd.Run()
	return run{stdout: out.String(), stderr: errOut.String(), err: err}
}

func (h *installerHarness) last() run {
	if len(h.runs) == 0 {
		return run{err: fmt.Errorf("no command has run")}
	}
	return h.runs[len(h.runs)-1]
}

// skillDir writes the parameterised skill 10.3 renders.
//
// The templated frontmatter value is quoted. It has to be: the config blob is
// derived from the frontmatter at pack time (2.1), and an unquoted `{{` opens
// a YAML flow mapping, so the skill would not pack at all.
func (h *installerHarness) skillDir(dir, version string) error {
	files := map[string]string{
		"SKILL.md": "---\nname: reviewer\nversion: " + version +
			"\ndescription: reviews code\nmodel: '{{ .Values.model }}'\n---\n\n" +
			"# {{ .Values.title }}\n\nReviewed for {{ .Values.global.org }}.\n",
		"references/notes.md": "Notes for {{ .Values.title }}.\n",
	}
	for name, body := range files {
		full := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(full, []byte(body), 0o600); err != nil {
			return err
		}
	}
	return nil
}

// --- Given ------------------------------------------------------------------

func (h *installerHarness) aParameterisedSkill(first, second string) error {
	for _, version := range []string{first, second} {
		dir := filepath.Join(h.context, "skill-"+version)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
		if err := h.skillDir(dir, version); err != nil {
			return err
		}
		packed := h.epos(h.eposHome, "pack", dir)
		if packed.failed() {
			return fmt.Errorf("pack %s: %v\n%s%s", version, packed.err, packed.stdout, packed.stderr)
		}
		h.digests["reviewer:"+version] = lastField(packed.stdout)
	}
	return nil
}

// aTwoStageSkill is 8.4's composition, with the same value key used in both
// stages: the collision the scoping of 10.3 exists to prevent.
func (h *installerHarness) aTwoStageSkill(key string) error {
	base := filepath.Join(h.context, "base")
	shared := filepath.Join(h.context, "shared")
	for _, dir := range []string{base, shared} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	if err := h.skillDir(base, "3.0.0"); err != nil {
		return err
	}
	// Deliberately the same key the final stage uses.
	body := "# {{ " + key + " }}\n\nShared at {{ .Values.global.org }}.\n"
	if err := os.WriteFile(filepath.Join(shared, "reference.md"), []byte(body), 0o600); err != nil {
		return err
	}

	skillfile := "FROM ./shared AS shared\nFROM ./base\n" +
		"COPY --from=shared reference.md references/shared.md\n"
	if err := os.WriteFile(filepath.Join(h.context, "Skillfile"),
		[]byte(skillfile), 0o600); err != nil {
		return err
	}

	built := h.epos(h.eposHome, "build", h.context)
	if built.failed() {
		return fmt.Errorf("build: %v\n%s%s", built.err, built.stdout, built.stderr)
	}
	h.digests["reviewer:3.0.0"] = lastField(built.stdout)
	return nil
}

// aValuesFile writes values.yaml into every worktree, so `-f values.yaml`
// resolves the same way wherever the install runs.
func (h *installerHarness) aValuesFile(body *godog.DocString) error {
	for _, dir := range h.worktrees {
		if err := os.WriteFile(filepath.Join(dir, "values.yaml"),
			[]byte(body.Content), 0o600); err != nil {
			return err
		}
	}
	return nil
}

func (h *installerHarness) anAdditionalBasePath(path string) error {
	body := fmt.Sprintf("{\n  \"skills\": [],\n  \"additionalBasePaths\": [%q]\n}\n", path)
	return os.WriteFile(filepath.Join(h.worktrees[0], "skills.json"), []byte(body), 0o600)
}

// theSharedLockIsHeld takes the store's lock for reading and keeps it.
//
// Taken through the same lockedfile the store itself uses, on the same file and
// on a handle of its own — which is what the operating system arbitrates on,
// whether the second holder is another process or another descriptor here. An
// install that took the lock exclusively would queue behind this and never
// finish; 9.2 says shared precisely so it does not.
func (h *installerHarness) theSharedLockIsHeld() error {
	held, err := lockedfile.Open(filepath.Join(h.eposHome, "store.lock"))
	if err != nil {
		return fmt.Errorf("hold the store lock: %w", err)
	}
	h.held = held
	return nil
}

// --- When -------------------------------------------------------------------

func (h *installerHarness) installs(ref string) error {
	return h.installsWith(ref, "")
}

func (h *installerHarness) installsWith(ref, extra string) error {
	args := []string{"install", ref, "-f", "values.yaml"}
	if extra != "" {
		// One flag and its value, so a value with spaces stays one argument.
		flag, value, _ := strings.Cut(extra, " ")
		args = append(args, flag, value)
	}

	result := h.epos(h.worktrees[0], args...)
	h.runs = append(h.runs, result)
	if body, err := os.ReadFile(filepath.Join(h.worktrees[0], "skills.lock.json")); err == nil {
		h.locks = append(h.locks, string(body))
	}
	return nil // scenarios assert on the result
}

// twoWorktreesInstallAtOnce is 10.2's claim, run rather than asserted: one
// store, two worktrees, two versions, both installs in flight together.
func (h *installerHarness) twoWorktreesInstallAtOnce(first, second string) error {
	refs := []string{first, second}
	results := make([]run, len(refs))

	// Released together, so the two processes are inside the store's lock at
	// the same time rather than one after the other.
	var start, done sync.WaitGroup
	start.Add(1)
	for i := range refs {
		done.Add(1)
		go func() {
			defer done.Done()
			start.Wait()
			results[i] = h.epos(h.worktrees[i], "install", refs[i], "-f", "values.yaml")
		}()
	}
	start.Done()
	done.Wait()

	h.runs = append(h.runs, results...)
	return nil
}

func (h *installerHarness) uninstalls(name string) error {
	h.runs = append(h.runs, h.epos(h.worktrees[0], "uninstall", name))
	return nil
}

func (h *installerHarness) lists() error {
	result := h.epos(h.worktrees[0], "ls")
	h.runs = append(h.runs, result)
	h.listing = result.stdout
	return nil
}

// --- Then -------------------------------------------------------------------

func (h *installerHarness) installSucceeds() error {
	if r := h.last(); r.failed() {
		return fmt.Errorf("the command failed: %v\nstdout: %s\nstderr: %s",
			r.err, r.stdout, r.stderr)
	}
	return nil
}

func (h *installerHarness) installFails() error {
	if !h.last().failed() {
		return fmt.Errorf("the command succeeded:\n%s", h.last().stdout)
	}
	return nil
}

func (h *installerHarness) errorNamesTheFile() error {
	if !strings.Contains(h.last().stderr, "SKILL.md") {
		return fmt.Errorf("the error does not name the file:\n%s", h.last().stderr)
	}
	return nil
}

func (h *installerHarness) bothInstallsSucceed() error {
	if len(h.runs) < 2 {
		return fmt.Errorf("expected 2 installs, got %d", len(h.runs))
	}
	for i, r := range h.runs[len(h.runs)-2:] {
		if r.failed() {
			return fmt.Errorf("install %d failed: %v\n%s%s", i+1, r.err, r.stdout, r.stderr)
		}
	}
	return nil
}

func (h *installerHarness) fileContains(path, want string) error {
	return h.fileInContains(h.worktrees[0], path, want)
}

func (h *installerHarness) firstWorktreeContains(path, want string) error {
	return h.fileInContains(h.worktrees[0], path, want)
}

func (h *installerHarness) secondWorktreeContains(path, want string) error {
	return h.fileInContains(h.worktrees[1], path, want)
}

func (h *installerHarness) fileInContains(dir, path, want string) error {
	body, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(path)))
	if err != nil {
		return err
	}
	if !strings.Contains(string(body), want) {
		return fmt.Errorf("%s does not contain %q:\n%s", path, want, body)
	}
	return nil
}

func (h *installerHarness) fileDoesNotContain(path, unwanted string) error {
	body, err := os.ReadFile(filepath.Join(h.worktrees[0], filepath.FromSlash(path)))
	if err != nil {
		return err
	}
	if strings.Contains(string(body), unwanted) {
		return fmt.Errorf("%s still contains %q:\n%s", path, unwanted, body)
	}
	return nil
}

func (h *installerHarness) pathDoesNotExist(path string) error {
	full := filepath.Join(h.worktrees[0], filepath.FromSlash(path))
	if _, err := os.Lstat(full); !os.IsNotExist(err) {
		return fmt.Errorf("%s exists", path)
	}
	return nil
}

// aPlainFile is 10.2's "a pin file, not a symlink": the claim is about what is
// on disk, so Lstat and not Stat.
func (h *installerHarness) aPlainFile(path string) error {
	info, err := os.Lstat(filepath.Join(h.worktrees[0], filepath.FromSlash(path)))
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%s is %s, not a regular file", path, info.Mode())
	}
	return nil
}

// lock is skills.lock.json, decoded far enough to assert on.
type lockFile struct {
	LockfileVersion int `json:"lockfileVersion"`
	Skills          []struct {
		Name      string   `json:"name"`
		Version   string   `json:"version"`
		Digest    string   `json:"digest"`
		BasePaths []string `json:"basePaths"`
	} `json:"skills"`
}

func (h *installerHarness) readLock(dir string) (lockFile, error) {
	var parsed lockFile
	body, err := os.ReadFile(filepath.Join(dir, "skills.lock.json"))
	if err != nil {
		return parsed, err
	}
	return parsed, json.Unmarshal(body, &parsed)
}

func (h *installerHarness) lockPinsDigest(name, tag string) error {
	parsed, err := h.readLock(h.worktrees[0])
	if err != nil {
		return err
	}
	want, ok := h.digests[tag]
	if !ok {
		return fmt.Errorf("no digest recorded for %s", tag)
	}
	for _, entry := range parsed.Skills {
		if entry.Name != name {
			continue
		}
		if entry.Digest != want {
			return fmt.Errorf("the lock pins %s at %s, want %s", name, entry.Digest, want)
		}
		return nil
	}
	return fmt.Errorf("the lock does not pin %s", name)
}

func (h *installerHarness) lockRecordsBothBasePaths() error {
	parsed, err := h.readLock(h.worktrees[0])
	if err != nil {
		return err
	}
	if len(parsed.Skills) != 1 {
		return fmt.Errorf("the lock pins %d skills, want 1", len(parsed.Skills))
	}
	want := []string{".claude/skills", ".cursor/skills"}
	if fmt.Sprint(parsed.Skills[0].BasePaths) != fmt.Sprint(want) {
		return fmt.Errorf("the lock records %v, want %v", parsed.Skills[0].BasePaths, want)
	}
	return nil
}

func (h *installerHarness) bothInstallsWroteTheSameLock() error {
	if len(h.locks) < 2 {
		return fmt.Errorf("expected 2 locks, got %d", len(h.locks))
	}
	if h.locks[0] != h.locks[1] {
		return fmt.Errorf("the second install rewrote the lock:\n%s\nwant\n%s",
			h.locks[1], h.locks[0])
	}
	return nil
}

func (h *installerHarness) worktreesPinnedDifferentDigests() error {
	first, err := h.readLock(h.worktrees[0])
	if err != nil {
		return err
	}
	second, err := h.readLock(h.worktrees[1])
	if err != nil {
		return err
	}
	if len(first.Skills) != 1 || len(second.Skills) != 1 {
		return fmt.Errorf("each worktree should pin one skill, got %d and %d",
			len(first.Skills), len(second.Skills))
	}
	if first.Skills[0].Digest == second.Skills[0].Digest {
		return fmt.Errorf("both worktrees pinned %s; 10.2 has them pin different digests",
			first.Skills[0].Digest)
	}
	return nil
}

// declares reads skills.json, which is the declaration rather than the pin.
func (h *installerHarness) declares(file, name string, want bool) error {
	body, err := os.ReadFile(filepath.Join(h.worktrees[0], file))
	if err != nil {
		return err
	}
	var manifest struct {
		Skills []struct {
			Name string `json:"name"`
		} `json:"skills"`
	}
	if err := json.Unmarshal(body, &manifest); err != nil {
		return err
	}
	found := false
	for _, entry := range manifest.Skills {
		if entry.Name == name {
			found = true
		}
	}
	if found != want {
		return fmt.Errorf("%s declares %s = %v, want %v:\n%s", file, name, found, want, body)
	}
	return nil
}

func (h *installerHarness) storeHoldsBothVersions() error {
	listed := h.epos(h.eposHome, "store", "ls")
	if listed.failed() {
		return fmt.Errorf("store ls: %v\n%s", listed.err, listed.stderr)
	}
	for _, tag := range []string{"reviewer:1.0.0", "reviewer:2.0.0"} {
		if !strings.Contains(listed.stdout, tag) {
			return fmt.Errorf("the store lost %s:\n%s", tag, listed.stdout)
		}
	}
	return nil
}

// storedArtifactHolds reads the content layer back out of the store, which is
// what proves 8.6: the template is still in the artifact after the install
// rendered a copy of it.
func (h *installerHarness) storedArtifactHolds(want string) error {
	files, err := storedLayer(h.eposHome, "reviewer:2.0.0")
	if err != nil {
		return err
	}
	body, ok := files["reviewer/SKILL.md"]
	if !ok {
		return fmt.Errorf("the layer holds no reviewer/SKILL.md")
	}
	if !strings.Contains(body, want) {
		return fmt.Errorf("the stored SKILL.md no longer holds %q:\n%s", want, body)
	}
	return nil
}

func (h *installerHarness) listingContains(want string) error {
	if !strings.Contains(h.listing, want) {
		return fmt.Errorf("the listing does not contain %q:\n%s", want, h.listing)
	}
	return nil
}

func (h *installerHarness) listingIsEmpty() error {
	if strings.TrimSpace(h.listing) != "" {
		return fmt.Errorf("the listing is not empty:\n%s", h.listing)
	}
	return nil
}

// storedLayer reads a tagged artifact's content layer as path → contents.
func storedLayer(eposHome, tag string) (map[string]string, error) {
	st, err := oci.New(storeDir(eposHome))
	if err != nil {
		return nil, err
	}
	ctx := context.Background()
	desc, err := st.Resolve(ctx, tag)
	if err != nil {
		return nil, fmt.Errorf("resolve %s: %w", tag, err)
	}
	body, err := content.FetchAll(ctx, st, desc)
	if err != nil {
		return nil, err
	}
	var m ocispec.Manifest
	if err := json.Unmarshal(body, &m); err != nil {
		return nil, err
	}
	if len(m.Layers) != 1 {
		return nil, fmt.Errorf("manifest has %d layers, want exactly 1", len(m.Layers))
	}
	packed, err := content.FetchAll(ctx, st, m.Layers[0])
	if err != nil {
		return nil, err
	}

	gr, err := gzip.NewReader(bytes.NewReader(packed))
	if err != nil {
		return nil, err
	}
	defer func() { _ = gr.Close() }()

	out := map[string]string{}
	tr := tar.NewReader(gr)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return out, nil
		}
		if err != nil {
			return nil, err
		}
		if h.Typeflag == tar.TypeDir {
			continue
		}
		contents, err := io.ReadAll(tr)
		if err != nil {
			return nil, err
		}
		out[h.Name] = string(contents)
	}
}

// --- suite ------------------------------------------------------------------

// TestInstallLocally is A4's gate: a parameterised skill installs into
// .claude/skills, and two worktrees pin different digests from one store at
// the same time.
//
// No container is started and none is needed. The skills are packed and built
// from directories, the store is a temp directory, and install never reaches a
// registry — an install is a read of the local store and a write of a worktree.
func TestInstallLocally(t *testing.T) {
	godogT = t

	h := &installerHarness{bin: buildBinary(t, "epos", "../../cmd/epos")}

	suite := godog.TestSuite{
		ScenarioInitializer: func(sc *godog.ScenarioContext) {
			sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
				h.reset(t)
				return ctx, nil
			})
			sc.After(func(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
				h.releaseLock()
				return ctx, nil
			})

			sc.Given(`^a parameterised skill packed at "([^"]+)" and at "([^"]+)"$`,
				h.aParameterisedSkill)
			sc.Given(`^a values file:$`, h.aValuesFile)
			sc.Given(`^a skill built from two stages that both use "([^"]+)"$`, h.aTwoStageSkill)
			sc.Given(`^skills\.json naming "([^"]+)" as an additional base path$`,
				h.anAdditionalBasePath)
			sc.Given(`^the store's shared lock is held open$`, h.theSharedLockIsHeld)

			sc.When(`^the author installs "([^"]+)"$`, h.installs)
			sc.When(`^the author installs "([^"]+)" with "([^"]+)"$`, h.installsWith)
			sc.When(`^two worktrees install "([^"]+)" and "([^"]+)" at the same time$`,
				h.twoWorktreesInstallAtOnce)
			sc.When(`^the author uninstalls "([^"]+)"$`, h.uninstalls)
			sc.When(`^the author lists what the worktree pinned$`, h.lists)

			sc.Then(`^the install succeeds$`, h.installSucceeds)
			sc.Then(`^the install fails$`, h.installFails)
			sc.Then(`^both installs succeed$`, h.bothInstallsSucceed)
			sc.Then(`^the error names the file it could not render$`, h.errorNamesTheFile)
			sc.Then(`^"([^"]+)" contains "([^"]+)"$`, h.fileContains)
			sc.Then(`^"([^"]+)" does not contain "([^"]+)"$`, h.fileDoesNotContain)
			sc.Then(`^"([^"]+)" does not exist$`, h.pathDoesNotExist)
			sc.Then(`^"([^"]+)" is a regular file and not a symlink$`, h.aPlainFile)
			sc.Then(`^the first worktree's "([^"]+)" contains "([^"]+)"$`, h.firstWorktreeContains)
			sc.Then(`^the second worktree's "([^"]+)" contains "([^"]+)"$`, h.secondWorktreeContains)
			sc.Then(`^the lock pins "([^"]+)" at the digest the store holds for "([^"]+)"$`,
				h.lockPinsDigest)
			sc.Then(`^the lock records both base paths$`, h.lockRecordsBothBasePaths)
			sc.Then(`^both installs wrote the same lock$`, h.bothInstallsWroteTheSameLock)
			sc.Then(`^the two worktrees pinned different digests$`, h.worktreesPinnedDifferentDigests)
			sc.Then(`^"([^"]+)" declares "([^"]+)"$`, func(file, name string) error {
				return h.declares(file, name, true)
			})
			sc.Then(`^"([^"]+)" does not declare "([^"]+)"$`, func(file, name string) error {
				return h.declares(file, name, false)
			})
			sc.Then(`^the store still holds both versions$`, h.storeHoldsBothVersions)
			sc.Then(`^the stored artifact still holds "([^"]+)"$`, h.storedArtifactHolds)
			sc.Then(`^the listing contains "([^"]+)"$`, h.listingContains)
			sc.Then(`^the listing is empty$`, h.listingIsEmpty)
		},
		Options: &godog.Options{
			Format:   "pretty,junit:junit-install.xml",
			Paths:    []string{"../../features/install-locally.feature"},
			Tags:     "~@wip",
			TestingT: t,
		},
	}

	if suite.Run() != 0 {
		t.Fatal("install locally suite failed")
	}
}
