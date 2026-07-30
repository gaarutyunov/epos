//go:build integration

package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cucumber/godog"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
	"oras.land/oras-go/v2"
	"oras.land/oras-go/v2/content"
	"oras.land/oras-go/v2/content/memory"
	"oras.land/oras-go/v2/registry/remote"
	"oras.land/oras-go/v2/registry/remote/auth"
)

// eposHomeEnv is spelled out rather than taken from internal/store: these
// suites drive the CLI as a user does, and the variable's name is part of what
// the user is promised.
const eposHomeEnv = "EPOS_HOME"

// The credential the authenticated-registry scenario logs in with, and the
// bcrypt htpasswd line zot checks it against.
//
// The hash is committed rather than generated so the suite needs neither a
// bcrypt dependency nor the `htpasswd` binary; it protects a password that
// exists only inside a container this suite starts and terminates.
const (
	registryUser     = "epos"
	registryPassword = "epos-secret"
	registryHtpasswd = "epos:$2a$10$r58SHvt6cY1ZtUwSXyfQkO6wXHXTKr6Os2xF0Y/7iZBiG/GCBjqf.\n"
)

// authenticatedZotConfig turns zot's htpasswd authentication on. Without an
// accessControl block zot answers every anonymous request 401, which is exactly
// the state `epos push` has to explain to the user.
const authenticatedZotConfig = `{
  "distSpecVersion": "1.1.1",
  "storage": { "rootDirectory": "/tmp/zot-data" },
  "http": {
    "address": "0.0.0.0",
    "port": "5000",
    "auth": { "htpasswd": { "path": "/etc/zot/htpasswd" } }
  },
  "log": { "level": "warn" }
}`

// namespace is the destination `epos push` is given. The skill's own name is
// appended by push, so the published repository is
// demo/agent-skills/<name> — SPEC.md 2.1's convention, and the exact inverse of
// what `epos pull` reads back out of the last path segment.
const namespace = "demo/agent-skills"

// author drives the epos CLI as a user would: a real binary, a real store
// directory, a real registry. Nothing here reaches into the packages under
// test.
type author struct {
	registryURL string
	// eposHome is EPOS_HOME, so the CLI's store lands in the sandbox. Nothing
	// here moves HOME: EPOS_HOME redirects epos and only epos, whereas HOME is
	// read by everything else in the process too — and on Windows is not even
	// the variable os.UserHomeDir consults.
	eposHome       string
	secondEposHome string // a "different machine" with an empty store
	// registryConfig is where credentials land. Every epos invocation that
	// touches a registry is pointed at it, so nothing this suite does can reach
	// the developer's own ~/.docker/config.json.
	registryConfig string

	root       string // the scenario's sandbox
	dir        string // the skill directory
	altDir     string // an identical directory, written differently
	contextDir string // a Skillfile build context
	name       string
	version    string
	builtTag   string // the tag `epos build` wrote, when a scenario built one

	digests    []string // one per pack
	packDigest string   // what the first pack printed
	packErr    error
	orphan     string // a planted unreachable blob
	pulledTo   string

	push  eposRun // the last `epos push`
	login eposRun // the last `epos registry login`
}

// eposRun is one invocation of the CLI, kept whole so a scenario can assert on
// the argument vector as well as on the output — "no secret in argv" is a
// requirement, so argv is evidence.
type eposRun struct {
	args   []string
	stdout string
	stderr string
	err    error
}

func (r eposRun) output() string { return strings.TrimSpace(r.stdout + "\n" + r.stderr) }

func (a *author) reset(t *testing.T) {
	a.root = t.TempDir()
	a.eposHome = filepath.Join(a.root, "epos")
	a.secondEposHome = filepath.Join(a.root, "epos2")
	a.registryConfig = filepath.Join(a.root, "registry-config", "config.json")
	for _, d := range []string{a.eposHome, a.secondEposHome, filepath.Dir(a.registryConfig)} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	a.dir, a.altDir, a.contextDir, a.orphan, a.pulledTo = "", "", "", "", ""
	a.builtTag = ""
	a.digests = nil
	a.packDigest = ""
	a.packErr = nil
	a.push, a.login = eposRun{}, eposRun{}
}

// run invokes the CLI with stdout and stderr kept apart, because a scenario
// asserting on the one-line `<ref> <digest>` must not have a warning folded
// into it.
func (a *author) run(eposHome, stdin string, args ...string) eposRun {
	cmd := exec.Command(eposBin, args...)
	cmd.Env = append(os.Environ(), eposHomeEnv+"="+eposHome)
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	err := cmd.Run()
	return eposRun{
		args:   args,
		stdout: strings.TrimSpace(out.String()),
		stderr: strings.TrimSpace(errOut.String()),
		err:    err,
	}
}

func (a *author) epos(eposHome string, args ...string) (string, error) {
	r := a.run(eposHome, "", args...)
	return r.output(), r.err
}

// storeDir is where an EPOS_HOME puts the OCI layout, which is the one thing
// these suites need to know about the root's internals.
func storeDir(eposHome string) string {
	return filepath.Join(eposHome, "store")
}

// --- Given ------------------------------------------------------------------

func (a *author) aRegistry(ctx context.Context) error {
	return a.startZot(ctx, nil)
}

// aRegistryRequiringAuthentication starts the same zot with htpasswd on. SPEC
// 13.2 chooses zot partly for this: authentication is tested against a registry
// that really performs the handshake, never against a mock.
func (a *author) aRegistryRequiringAuthentication(ctx context.Context) error {
	dir := filepath.Join(a.root, "zot")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	config := filepath.Join(dir, "config.json")
	if err := os.WriteFile(config, []byte(authenticatedZotConfig), 0o644); err != nil {
		return err
	}
	htpasswd := filepath.Join(dir, "htpasswd")
	if err := os.WriteFile(htpasswd, []byte(registryHtpasswd), 0o644); err != nil {
		return err
	}
	return a.startZot(ctx, []testcontainers.ContainerFile{
		{HostFilePath: config, ContainerFilePath: "/etc/zot/config.json", FileMode: 0o644},
		{HostFilePath: htpasswd, ContainerFilePath: "/etc/zot/htpasswd", FileMode: 0o644},
	})
}

func (a *author) startZot(ctx context.Context, files []testcontainers.ContainerFile) error {
	req := testcontainers.ContainerRequest{
		Image:        zotImage,
		ExposedPorts: []string{"5000/tcp"},
		Files:        files,
		// 401 counts as ready: a registry with authentication on answers
		// GET /v2/ with a challenge, and waiting for 200 would wait forever.
		WaitingFor: wait.ForHTTP("/v2/").WithPort("5000/tcp").
			WithStatusCodeMatcher(func(status int) bool {
				return status == 200 || status == 401
			}).
			WithStartupTimeout(2 * time.Minute),
	}
	if len(files) > 0 {
		req.Cmd = []string{"serve", "/etc/zot/config.json"}
	}

	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		return fmt.Errorf("start zot: %w", err)
	}
	authorContainers = append(authorContainers, c)

	endpoint, err := c.PortEndpoint(ctx, "5000/tcp", "")
	if err != nil {
		return err
	}
	a.registryURL = endpoint
	return nil
}

func (a *author) aSkillDirectory(name, version string) error {
	a.name, a.version = name, version
	dir, err := writeSkill(name, version)
	if err != nil {
		return err
	}
	a.dir = dir
	return nil
}

// anIdenticalDirectory writes the same content again. The files land in a
// different creation order and with different permissions, neither of which
// 2.4 permits to reach the digest.
func (a *author) anIdenticalDirectory() error {
	dir, err := writeSkillReversed(a.name, a.version)
	if err != nil {
		return err
	}
	a.altDir = dir
	return nil
}

func (a *author) theDirectoryContainsASymlink() error {
	return os.Symlink("/etc/passwd", filepath.Join(a.dir, "leak.md"))
}

func (a *author) anUnreferencedBlobIsInTheStore() error {
	blobs := filepath.Join(storeDir(a.eposHome), "blobs", "sha256")
	a.orphan = filepath.Join(blobs, "orphaned0000000000000000000000000000000000000000000000000000000")
	return os.WriteFile(a.orphan, []byte("unreachable"), 0o644)
}

// aSkillfileDeriving writes a build context whose base is the packed skill's
// own directory. A local base needs no git server, which keeps this scenario
// about what push does with provenance rather than about where a base comes
// from.
func (a *author) aSkillfileDeriving(name, version string) error {
	a.contextDir = filepath.Join(a.root, "context")
	base := filepath.Join(a.contextDir, "base")
	for path, body := range skillFiles(a.name, a.version) {
		full := filepath.Join(base, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(full, []byte(body), 0o600); err != nil {
			return err
		}
	}
	a.builtTag = name + ":" + version
	skillfile := fmt.Sprintf("FROM ./base\nSET name %s\nSET version %s\n", name, version)
	return os.WriteFile(filepath.Join(a.contextDir, "Skillfile"), []byte(skillfile), 0o600)
}

func (a *author) builds() error {
	out, err := a.epos(a.eposHome, "build", a.contextDir)
	if err != nil {
		return fmt.Errorf("build: %v: %s", err, out)
	}
	return nil
}

// --- When -------------------------------------------------------------------

func (a *author) packs() error {
	out, err := a.epos(a.eposHome, "pack", a.dir)
	if err != nil {
		a.packErr = fmt.Errorf("%v: %s", err, out)
		return nil // scenarios that expect failure assert on packErr
	}
	digest := lastField(out)
	a.digests = append(a.digests, digest)
	if a.packDigest == "" {
		a.packDigest = digest
	}
	return nil
}

func (a *author) packsAgain() error { return a.packs() }

func (a *author) packsBoth() error {
	if err := a.packs(); err != nil {
		return err
	}
	out, err := a.epos(a.eposHome, "pack", a.altDir)
	if err != nil {
		return fmt.Errorf("pack the second directory: %v: %s", err, out)
	}
	a.digests = append(a.digests, lastField(out))
	return nil
}

// pushes publishes with `epos push` and nothing else — the whole point of the
// command. The destination names a namespace; push appends the skill's name.
func (a *author) pushes(tag string) error {
	a.push = a.run(a.eposHome, "", "push", tag,
		"oci://"+a.registryURL+"/"+namespace,
		"--plain-http", "--registry-config", a.registryConfig)
	if a.push.err != nil {
		return fmt.Errorf("push %s: %v: %s", tag, a.push.err, a.push.output())
	}
	a.digests = append(a.digests, lastField(a.push.stdout))
	return nil
}

func (a *author) pushesIt() error { return a.pushes(a.storeTag()) }

func (a *author) pushesTag(tag string) error { return a.pushes(tag) }

// pushesWhileLoggedOut expects to fail, so it records the run rather than
// returning the error.
func (a *author) pushesWhileLoggedOut() error {
	a.push = a.run(a.eposHome, "", "push", a.storeTag(),
		"oci://"+a.registryURL+"/"+namespace,
		"--plain-http", "--registry-config", a.registryConfig)
	return nil
}

// logsIn supplies the secret on standard input. There is no --password flag to
// use instead, which is the point of the assertion that follows.
func (a *author) logsIn() error {
	a.login = a.run(a.eposHome, registryPassword, "registry", "login", a.registryURL,
		"-u", registryUser, "--password-stdin",
		"--plain-http", "--registry-config", a.registryConfig)
	if a.login.err != nil {
		return fmt.Errorf("registry login: %v: %s", a.login.err, a.login.output())
	}
	return nil
}

func (a *author) aSecondMachinePulls() error {
	ref := fmt.Sprintf("%s/%s/%s:%s", a.registryURL, namespace, a.name, a.version)
	out, err := a.epos(a.secondEposHome, "pull", ref, "--plain-http")
	if err != nil {
		return fmt.Errorf("pull: %v: %s", err, out)
	}
	a.pulledTo = a.secondEposHome
	return nil
}

func (a *author) prunes() error {
	out, err := a.epos(a.eposHome, "store", "prune")
	if err != nil {
		return fmt.Errorf("prune: %v: %s", err, out)
	}
	return nil
}

// plainOrasPulls fetches with oras-go configured no differently from a client
// that has never heard of Epos (2.1).
func (a *author) plainOrasPulls() error {
	repo, err := a.remoteRepository(a.name)
	if err != nil {
		return err
	}

	desc, err := oras.Copy(context.Background(), repo, a.version, memory.New(), a.version,
		oras.DefaultCopyOptions)
	if err != nil {
		return fmt.Errorf("plain oras pull: %w", err)
	}
	a.digests = append(a.digests, desc.Digest.String())
	return nil
}

// --- Then -------------------------------------------------------------------

func (a *author) storeTag() string { return a.name + ":" + a.version }

func (a *author) remoteRepository(skill string) (*remote.Repository, error) {
	repo, err := remote.NewRepository(
		fmt.Sprintf("%s/%s/%s", a.registryURL, namespace, skill))
	if err != nil {
		return nil, err
	}
	repo.PlainHTTP = true
	return repo, nil
}

// manifestByTag reads one tagged manifest out of a store's OCI layout. By tag
// rather than by index position, because a scenario that builds as well as
// packs leaves two skills in the same flat layout.
func (a *author) manifestByTag(eposHome, tag string) (ocispec.Manifest, error) {
	root := storeDir(eposHome)
	idxBody, err := os.ReadFile(filepath.Join(root, "index.json"))
	if err != nil {
		return ocispec.Manifest{}, err
	}
	var idx ocispec.Index
	if err := json.Unmarshal(idxBody, &idx); err != nil {
		return ocispec.Manifest{}, err
	}

	for _, desc := range idx.Manifests {
		if tag != "" && desc.Annotations[ocispec.AnnotationRefName] != tag {
			continue
		}
		body, err := os.ReadFile(filepath.Join(root, "blobs", "sha256", desc.Digest.Encoded()))
		if err != nil {
			return ocispec.Manifest{}, err
		}
		var m ocispec.Manifest
		return m, json.Unmarshal(body, &m)
	}
	return ocispec.Manifest{}, fmt.Errorf("the store holds no manifest tagged %q", tag)
}

func (a *author) manifestFromStore(eposHome string) (ocispec.Manifest, error) {
	return a.manifestByTag(eposHome, a.storeTag())
}

func (a *author) exactlyOneContentLayer() error {
	m, err := a.manifestFromStore(a.eposHome)
	if err != nil {
		return err
	}
	if len(m.Layers) != 1 {
		return fmt.Errorf("manifest has %d layers, want exactly 1", len(m.Layers))
	}
	if m.Layers[0].MediaType != "application/vnd.agentskills.skill.content.v1.tar+gzip" {
		return fmt.Errorf("layer media type = %q", m.Layers[0].MediaType)
	}
	return nil
}

func (a *author) carriesTheArtifactType() error {
	m, err := a.manifestFromStore(a.eposHome)
	if err != nil {
		return err
	}
	if m.ArtifactType != "application/vnd.agentskills.skill.v1" {
		return fmt.Errorf("artifactType = %q", m.ArtifactType)
	}
	return nil
}

func (a *author) configIsInlined() error {
	m, err := a.manifestFromStore(a.eposHome)
	if err != nil {
		return err
	}
	if len(m.Config.Data) == 0 {
		return fmt.Errorf("config blob is not inlined; a pull would need a second fetch")
	}
	var cfg map[string]any
	if err := json.Unmarshal(m.Config.Data, &cfg); err != nil {
		return fmt.Errorf("inlined config is not JSON: %w", err)
	}
	if cfg["name"] != a.name {
		return fmt.Errorf("inlined config name = %v, want %s", cfg["name"], a.name)
	}
	return nil
}

func (a *author) bothPacksAgree() error {
	if len(a.digests) < 2 {
		return fmt.Errorf("expected 2 digests, got %d", len(a.digests))
	}
	if a.digests[0] != a.digests[1] {
		return fmt.Errorf("digests differ: %s vs %s; 2.4 requires them identical",
			a.digests[0], a.digests[1])
	}
	return nil
}

// everyDigestIsThePackDigest is the gate the design names: pack → push → pull
// all land on one digest, because push moves bytes and derives nothing.
func (a *author) everyDigestIsThePackDigest() error {
	if a.packDigest == "" {
		return fmt.Errorf("nothing was packed")
	}
	for i, d := range a.digests {
		if d != a.packDigest {
			return fmt.Errorf("digest %d is %s, but pack printed %s", i+1, d, a.packDigest)
		}
	}
	return nil
}

func (a *author) registryHoldsTagged(repository, tag string) error {
	repo, err := remote.NewRepository(a.registryURL + "/" + repository)
	if err != nil {
		return err
	}
	repo.PlainHTTP = true
	repo.Client = a.registryClient()

	if _, err := repo.Resolve(context.Background(), tag); err != nil {
		return fmt.Errorf("the registry does not hold %s:%s: %w", repository, tag, err)
	}
	return nil
}

// tagsAreExactly is 2.1's other half: the remote tag is the version alone. A
// stray "reviewer:1.0.0" tag in the registry would mean push had copied the
// store's tag across instead of mapping it.
func (a *author) tagsAreExactly(repository, want string) error {
	repo, err := remote.NewRepository(a.registryURL + "/" + repository)
	if err != nil {
		return err
	}
	repo.PlainHTTP = true
	repo.Client = a.registryClient()

	var tags []string
	if err := repo.Tags(context.Background(), "", func(page []string) error {
		tags = append(tags, page...)
		return nil
	}); err != nil {
		return fmt.Errorf("list the tags of %s: %w", repository, err)
	}
	if len(tags) != 1 || tags[0] != want {
		return fmt.Errorf("tags of %s are %v, want exactly [%s]", repository, tags, want)
	}
	return nil
}

// registryClient authenticates as the htpasswd user, so an assertion works
// against both the anonymous and the authenticated registry.
func (a *author) registryClient() remote.Client {
	return &auth.Client{
		Credential: auth.StaticCredential(a.registryURL, auth.Credential{
			Username: registryUser,
			Password: registryPassword,
		}),
	}
}

// pushReportsReferenceAndDigest is D6's output contract: one line, two
// whitespace-separated fields, the same shape `pack` and `pull` print — and the
// reference is the fully resolved one, so a mistyped destination is visible on
// the first run rather than on the first failed pull.
func (a *author) pushReportsReferenceAndDigest() error {
	lines := strings.Split(a.push.stdout, "\n")
	if len(lines) != 1 {
		return fmt.Errorf("push printed %d lines, want 1: %q", len(lines), a.push.stdout)
	}
	fields := strings.Fields(lines[0])
	if len(fields) != 2 {
		return fmt.Errorf("push printed %d fields, want 2: %q", len(fields), lines[0])
	}

	want := fmt.Sprintf("%s/%s/%s:%s", a.registryURL, namespace, a.name, a.version)
	if fields[0] != want {
		return fmt.Errorf("push reported %q, want the resolved reference %q", fields[0], want)
	}
	if fields[1] != a.packDigest {
		return fmt.Errorf("push reported digest %s, but pack printed %s", fields[1], a.packDigest)
	}
	return nil
}

// pushRefusedNaming checks the 401 message. It must name the registry and the
// command that fixes it, and it must carry no credential.
func (a *author) pushRefusedNaming(command string) error {
	if a.push.err == nil {
		return fmt.Errorf("the push succeeded against a registry that requires authentication")
	}
	out := a.push.output()
	if !strings.Contains(out, a.registryURL) {
		return fmt.Errorf("the failure does not name the registry %s: %s", a.registryURL, out)
	}
	if !strings.Contains(out, command) {
		return fmt.Errorf("the failure does not name %q: %s", command, out)
	}
	if strings.Contains(out, registryPassword) {
		return fmt.Errorf("the failure leaked the credential: %s", out)
	}
	return nil
}

// noSecretInArgv is the requirement D8 refuses helm parity for: a password
// passed as a flag value is world-readable through /proc/<pid>/cmdline and
// lands in shell history.
func (a *author) noSecretInArgv() error {
	if a.login.args == nil {
		return fmt.Errorf("no login was performed")
	}
	for _, run := range []eposRun{a.login, a.push} {
		for _, arg := range run.args {
			if strings.Contains(arg, registryPassword) {
				return fmt.Errorf("the secret reached the argument vector: %v", run.args)
			}
		}
	}
	if strings.Contains(a.login.output(), registryPassword) {
		return fmt.Errorf("login echoed the secret: %s", a.login.output())
	}
	return nil
}

func (a *author) storeHolds(tag string) error {
	out, err := a.epos(a.pulledTo, "store", "ls")
	if err != nil {
		return fmt.Errorf("store ls: %v: %s", err, out)
	}
	for _, line := range strings.Split(out, "\n") {
		if strings.TrimSpace(line) == tag {
			return nil
		}
	}
	return fmt.Errorf("store holds %q, want %s", out, tag)
}

func (a *author) authorStoreHolds(tag string) error {
	out, err := a.epos(a.eposHome, "store", "ls")
	if err != nil {
		return fmt.Errorf("store ls: %v: %s", err, out)
	}
	if !strings.Contains(out, tag) {
		return fmt.Errorf("store holds %q, want %s", out, tag)
	}
	return nil
}

func (a *author) pulledMatchesPushed() error {
	pulled, err := a.manifestFromStore(a.secondEposHome)
	if err != nil {
		return err
	}
	pushed, err := a.manifestFromStore(a.eposHome)
	if err != nil {
		return err
	}
	if pulled.Layers[0].Digest != pushed.Layers[0].Digest {
		return fmt.Errorf("pulled layer %s, pushed %s",
			pulled.Layers[0].Digest, pushed.Layers[0].Digest)
	}
	return nil
}

func (a *author) pulledArtifactMatches() error {
	if len(a.digests) < 2 {
		return fmt.Errorf("expected a pushed and a pulled digest, got %d", len(a.digests))
	}
	first, last := a.digests[len(a.digests)-2], a.digests[len(a.digests)-1]
	if first != last {
		return fmt.Errorf("plain oras pulled %s, epos pushed %s", last, first)
	}
	return nil
}

// publishedManifest fetches a manifest back out of the registry, verified
// against the digest the registry advertised for it.
func (a *author) publishedManifest(tag string) ([]byte, error) {
	name, version, _ := strings.Cut(tag, ":")
	repo, err := a.remoteRepository(name)
	if err != nil {
		return nil, err
	}
	desc, body, err := repo.FetchReference(context.Background(), version)
	if err != nil {
		return nil, fmt.Errorf("fetch the published manifest: %w", err)
	}
	defer func() { _ = body.Close() }()
	return content.ReadAll(body, desc)
}

// localManifest reads the same manifest out of the local OCI layout, as bytes.
func (a *author) localManifest(eposHome, tag string) ([]byte, error) {
	root := storeDir(eposHome)
	idxBody, err := os.ReadFile(filepath.Join(root, "index.json"))
	if err != nil {
		return nil, err
	}
	var idx ocispec.Index
	if err := json.Unmarshal(idxBody, &idx); err != nil {
		return nil, err
	}
	for _, desc := range idx.Manifests {
		if desc.Annotations[ocispec.AnnotationRefName] != tag {
			continue
		}
		return os.ReadFile(filepath.Join(root, "blobs", "sha256", desc.Digest.Encoded()))
	}
	return nil, fmt.Errorf("the store holds no manifest tagged %q", tag)
}

// publishedManifestIsIdentical compares the published manifest with the one the
// store holds, byte for byte. Push moves bytes: nothing is repacked, re-tagged,
// annotated or stamped, so equal bytes is the whole assertion.
func (a *author) publishedManifestIsIdentical() error {
	local, err := a.localManifest(a.eposHome, a.builtTag)
	if err != nil {
		return err
	}
	published, err := a.publishedManifest(a.builtTag)
	if err != nil {
		return err
	}
	if !bytes.Equal(local, published) {
		return fmt.Errorf("the published manifest differs from the local one:\nlocal:     %s\npublished: %s",
			local, published)
	}
	return nil
}

func (a *author) publishedManifestCarriesProvenance() error {
	raw, err := a.publishedManifest(a.builtTag)
	if err != nil {
		return err
	}
	var published ocispec.Manifest
	if err := json.Unmarshal(raw, &published); err != nil {
		return err
	}
	for _, key := range []string{
		"dev.epos.skillfile.digest",
		ocispec.AnnotationBaseImageName,
	} {
		if published.Annotations[key] == "" {
			return fmt.Errorf("the published manifest lost the %s annotation; it carries %v",
				key, published.Annotations)
		}
	}
	return nil
}

func (a *author) orphanIsGone() error {
	if _, err := os.Stat(a.orphan); !os.IsNotExist(err) {
		return fmt.Errorf("the unreferenced blob survived prune")
	}
	return nil
}

func (a *author) packingFails() error {
	if a.packErr == nil {
		return fmt.Errorf("pack succeeded; 2.5 rejects this at pack")
	}
	return nil
}

// --- helpers ----------------------------------------------------------------

func lastField(out string) string {
	fields := strings.Fields(out)
	if len(fields) == 0 {
		return ""
	}
	return fields[len(fields)-1]
}

func skillFiles(name, version string) map[string]string {
	return map[string]string{
		"SKILL.md": fmt.Sprintf("---\nname: %s\nversion: %s\ndescription: reviews code\n---\n\n# %s\n",
			name, version, name),
		"sections/checklist.md": "- table-driven tests\n",
		"reference/notes.md":    "notes\n",
	}
}

func writeSkill(name, version string) (string, error) {
	dir, err := os.MkdirTemp("", "epos-skill-*")
	if err != nil {
		return "", err
	}
	for path, body := range skillFiles(name, version) {
		full := filepath.Join(dir, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			return "", err
		}
		if err := os.WriteFile(full, []byte(body), 0o600); err != nil {
			return "", err
		}
	}
	return dir, nil
}

// writeSkillReversed writes the same files in the opposite order and with
// different permissions — neither may reach the digest (2.4).
func writeSkillReversed(name, version string) (string, error) {
	dir, err := os.MkdirTemp("", "epos-skill-alt-*")
	if err != nil {
		return "", err
	}
	files := skillFiles(name, version)
	paths := make([]string, 0, len(files))
	for p := range files {
		paths = append(paths, p)
	}
	for i := len(paths) - 1; i >= 0; i-- {
		full := filepath.Join(dir, filepath.FromSlash(paths[i]))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			return "", err
		}
		if err := os.WriteFile(full, []byte(files[paths[i]]), 0o755); err != nil {
			return "", err
		}
	}
	return dir, nil
}

// --- suite ------------------------------------------------------------------

var (
	eposBin          string
	authorContainers []testcontainers.Container
)

func TestAuthorAndPublish(t *testing.T) {
	godogT = t
	eposBin = buildBinary(t, "epos", "../../cmd/epos")

	a := &author{}
	t.Cleanup(func() {
		for _, c := range authorContainers {
			_ = c.Terminate(context.Background())
		}
		authorContainers = nil
	})

	suite := godog.TestSuite{
		ScenarioInitializer: func(sc *godog.ScenarioContext) {
			sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
				a.reset(t)
				return ctx, nil
			})
			// Each scenario's registry is torn down with it, so a long feature
			// does not hold one container per scenario open at once.
			sc.After(func(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
				for _, c := range authorContainers {
					_ = c.Terminate(ctx)
				}
				authorContainers = nil
				return ctx, nil
			})

			sc.Given(`^a registry$`, func(ctx context.Context) error { return a.aRegistry(ctx) })
			sc.Given(`^a registry that requires authentication$`,
				func(ctx context.Context) error { return a.aRegistryRequiringAuthentication(ctx) })
			sc.Given(`^a skill directory "([^"]+)" version "([^"]+)"$`, a.aSkillDirectory)
			sc.Given(`^an identical skill directory written in a different order$`, a.anIdenticalDirectory)
			sc.Given(`^the directory contains a symlink$`, a.theDirectoryContainsASymlink)
			sc.Given(`^an unreferenced blob is in the store$`, a.anUnreferencedBlobIsInTheStore)
			sc.Given(`^the author packs it$`, a.packs)
			sc.Given(`^a Skillfile deriving "([^"]+)" version "([^"]+)" from that directory$`,
				a.aSkillfileDeriving)
			sc.Given(`^the author builds it$`, a.builds)
			sc.Given(`^the author pushes it to the registry$`, a.pushesIt)

			sc.When(`^the author packs it$`, a.packs)
			sc.When(`^the author packs it again$`, a.packsAgain)
			sc.When(`^the author packs both$`, a.packsBoth)
			sc.When(`^the author pushes it to the registry$`, a.pushesIt)
			sc.When(`^the author pushes "([^"]+)" to the registry$`, a.pushesTag)
			sc.When(`^the author pushes it while logged out$`, a.pushesWhileLoggedOut)
			sc.When(`^the author logs in to the registry$`, a.logsIn)
			sc.When(`^a second machine pulls it$`, a.aSecondMachinePulls)
			sc.When(`^plain oras pulls it$`, a.plainOrasPulls)
			sc.When(`^the author prunes the store$`, a.prunes)

			sc.Then(`^the artifact has exactly one content layer$`, a.exactlyOneContentLayer)
			sc.Then(`^the artifact carries the agent-skills artifact type$`, a.carriesTheArtifactType)
			sc.Then(`^the config blob is inlined in the manifest$`, a.configIsInlined)
			sc.Then(`^both packs report the same digest$`, a.bothPacksAgree)
			sc.Then(`^every digest reported is the digest pack printed$`, a.everyDigestIsThePackDigest)
			sc.Then(`^the registry holds "([^"]+)" tagged "([^"]+)"$`, a.registryHoldsTagged)
			sc.Then(`^the tags of "([^"]+)" are exactly "([^"]+)"$`, a.tagsAreExactly)
			sc.Then(`^the push reports the resolved reference and the digest$`, a.pushReportsReferenceAndDigest)
			sc.Then(`^the push is refused, naming the registry and "([^"]+)"$`, a.pushRefusedNaming)
			sc.Then(`^no secret was passed in an argument$`, a.noSecretInArgv)
			sc.Then(`^that store holds "([^"]+)"$`, a.storeHolds)
			sc.Then(`^the store still holds "([^"]+)"$`, a.authorStoreHolds)
			sc.Then(`^the pulled digest matches the pushed digest$`, a.pulledMatchesPushed)
			sc.Then(`^the pulled artifact matches the one pushed$`, a.pulledArtifactMatches)
			sc.Then(`^the published manifest is identical to the one in the local store$`,
				a.publishedManifestIsIdentical)
			sc.Then(`^the published manifest carries the build's provenance annotations$`,
				a.publishedManifestCarriesProvenance)
			sc.Then(`^the unreferenced blob is gone$`, a.orphanIsGone)
			sc.Then(`^packing fails$`, a.packingFails)
		},
		Options: &godog.Options{
			Format:   "pretty,junit:junit-author.xml",
			Paths:    []string{"../../features/author-and-publish.feature"},
			Tags:     "~@wip",
			TestingT: t,
		},
	}

	if suite.Run() != 0 {
		t.Fatal("author and publish suite failed")
	}
}

// buildBinary compiles one of the repo's commands for the suite.
func buildBinary(t *testing.T, name, pkg string) string {
	t.Helper()

	bin := filepath.Join(t.TempDir(), name)
	cmd := exec.Command("go", "build", "-o", bin, pkg)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("build %s: %v", name, err)
	}
	return bin
}
