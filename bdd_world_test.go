//go:build integration

package epos_test

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/cucumber/godog"

	"github.com/gaarutyunov/epos/internal/app"
	"github.com/gaarutyunov/epos/internal/infrastructure/oci"
)

// world is the per-scenario state driving the epos application service against
// real dependencies.
type world struct {
	workspace string
	registry  string // registry host:port
	plainHTTP bool
	client    *oci.Client
	app       *app.App
	out       bytes.Buffer
	lastErr   error

	// scenario state
	skillDir         string
	pushDigest       string
	discoverMode     string
	listing          []string
	proxyPersist     int
	cards            []string
	gitRemote        string
	consumerDir      string
	composeRes       *app.ComposeResult
	composeErr       error
	reg404           string
	pullStatus       int
	publishedVersion map[string]string

	teardown []func()
}

func (w *world) reset() error {
	for i := len(w.teardown) - 1; i >= 0; i-- {
		w.teardown[i]()
	}
	w.teardown = nil
	*w = world{}

	dir, err := os.MkdirTemp("", "epos-bdd-*")
	if err != nil {
		return err
	}
	w.workspace = dir
	w.teardown = append(w.teardown, func() { os.RemoveAll(dir) })

	host, cleanup := startRegistry()
	w.registry = host
	w.plainHTTP = true
	w.teardown = append(w.teardown, cleanup)

	w.client = &oci.Client{PlainHTTP: true}
	w.app = app.New(app.Options{
		DefaultRegistry: host,
		PlainHTTP:       true,
		WorkDir:         w.workspace,
		Out:             &w.out,
		Err:             &w.out,
	})
	w.app.Kube = newKubeClient(w)
	w.publishedVersion = map[string]string{}
	return nil
}

// publishSkillOnly publishes a skill with exactly the given files (plus a
// generated Epos.yaml and a Usage-bearing SKILL.md), returning its digest.
func (w *world) publishSkillOnly(name, version string, files map[string]string) (string, error) {
	dir := filepath.Join(w.workspace, "_pub", name)
	all := map[string]string{
		"Epos.yaml": fmt.Sprintf("apiVersion: epos/v1\nname: %s\nversion: %s\ndescription: The %s skill\n", name, version, name),
		"SKILL.md":  "---\nname: " + name + "\ndescription: the " + name + " skill\n---\n\n# " + name + "\n\n## Usage\nRun the tool.\n",
	}
	for k, v := range files {
		all[k] = v
	}
	for rel, content := range all {
		full := filepath.Join(dir, rel)
		_ = os.MkdirAll(filepath.Dir(full), 0o755)
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			return "", err
		}
	}
	ref := w.registry + "/" + name + ":" + version
	digest, err := w.app.Push(context.Background(), dir, ref)
	if err == nil {
		w.publishedVersion[name] = version
	}
	return digest, err
}

// ensureConsumer creates the consumer repo (Epos.yaml only, no own files) that
// depends on the origin skill, so overlays apply to the origin's SKILL.md.
func (w *world) ensureConsumer() string {
	if w.consumerDir != "" {
		return w.consumerDir
	}
	dir := filepath.Join(w.workspace, "myrepo")
	_ = os.MkdirAll(dir, 0o755)
	w.consumerDir = dir
	originVer := w.publishedVersion["pdf-tools"]
	if originVer == "" {
		originVer = "1.0.0"
	}
	eposYAML := fmt.Sprintf("apiVersion: epos/v1\nname: myrepo\nversion: 0.1.0\ndescription: consumer repo\ndependencies:\n  - name: pdf-tools\n    oci: %s/pdf-tools\n    version: %s\n", w.registry, originVer)
	_ = os.WriteFile(filepath.Join(dir, "Epos.yaml"), []byte(eposYAML), 0o644)
	return dir
}

// appendDep appends a dependency block to the consumer Epos.yaml.
func (w *world) appendDep(block string) {
	dir := w.ensureConsumer()
	path := filepath.Join(dir, "Epos.yaml")
	data, _ := os.ReadFile(path)
	_ = os.WriteFile(path, append(data, []byte(block)...), 0o644)
}

// addLocalOverlay writes overlays/<name>/Overlay.yaml (+ files) into the consumer.
func (w *world) addLocalOverlay(name, overlayYAML string, files map[string]string) {
	dir := filepath.Join(w.ensureConsumer(), "overlays", name)
	_ = os.MkdirAll(dir, 0o755)
	_ = os.WriteFile(filepath.Join(dir, "Overlay.yaml"), []byte(overlayYAML), 0o644)
	for rel, content := range files {
		full := filepath.Join(dir, rel)
		_ = os.MkdirAll(filepath.Dir(full), 0o755)
		_ = os.WriteFile(full, []byte(content), 0o644)
	}
}

// ---- skill/overlay fixtures ----

func (w *world) writeSkill(name, version, description string, extra map[string]string) string {
	dir := filepath.Join(w.workspace, name)
	files := map[string]string{
		"Epos.yaml":       fmt.Sprintf("apiVersion: epos/v1\nname: %s\nversion: %s\ndescription: %s\n", name, version, description),
		"values.yaml":     "features:\n  advanced: false\n",
		"SKILL.md":        "---\nname: " + name + "\ndescription: " + description + "\n---\n\n# " + name + "\n\n## Usage\nRun the tool.\n",
		"references/c.md": "reference c\n",
	}
	for k, v := range extra {
		files[k] = v
	}
	for rel, content := range files {
		full := filepath.Join(dir, rel)
		_ = os.MkdirAll(filepath.Dir(full), 0o755)
		_ = os.WriteFile(full, []byte(content), 0o644)
	}
	return dir
}

// publishSkill builds and pushes a skill to the registry, returning its digest.
func (w *world) publishSkill(name, version string, extra map[string]string) (string, error) {
	dir := w.writeSkill(name, version, "Extract and manipulate "+name, extra)
	ref := w.registry + "/" + name + ":" + version
	return w.app.Push(context.Background(), dir, ref)
}

// ---- epos command dispatch ----

// runEpos parses an `epos …` command line and dispatches to the application
// service (the CLI is a 1:1 thin wrapper over these calls). It rewrites the
// literal "registry/" host placeholder used in the features to the running
// registry, and captures output and error.
func (w *world) runEpos(line string) {
	w.out.Reset()
	w.lastErr = nil
	fields := strings.Fields(line)
	if len(fields) == 0 || fields[0] != "epos" {
		w.lastErr = fmt.Errorf("not an epos command: %q", line)
		return
	}
	args := fields[1:]
	for i, a := range args {
		if strings.HasPrefix(a, "registry/") {
			args[i] = w.registry + strings.TrimPrefix(a, "registry")
		}
	}
	pos, opts := parseArgs(args[1:])
	ctx := context.Background()
	verb := args[0]

	switch verb {
	case "package":
		_, _, w.lastErr = w.app.Package(ctx, w.abs(pos[0]))
	case "lint":
		ok, msgs, err := w.app.Lint(w.abs(pos[0]))
		for _, m := range msgs {
			fmt.Fprintln(&w.out, m)
		}
		if err != nil {
			w.lastErr = err
		} else if !ok {
			w.lastErr = fmt.Errorf("validation failed")
		}
	case "push":
		w.pushDigest, w.lastErr = w.app.Push(ctx, w.abs(pos[0]), pos[1])
	case "install":
		_, w.lastErr = w.app.Install(ctx, pos[0], pos[1], opts)
	case "upgrade":
		_, w.lastErr = w.app.Upgrade(ctx, pos[0], pos[1], opts)
	case "rollback":
		var rev int
		fmt.Sscanf(pos[1], "%d", &rev)
		_, w.lastErr = w.app.Rollback(ctx, pos[0], rev, opts)
	case "template":
		var s string
		s, w.lastErr = w.app.Template(ctx, pos[0], pos[1], opts)
		w.out.WriteString(s)
	case "overlay":
		if len(pos) >= 2 && pos[0] == "push" {
			w.pushDigest, w.lastErr = w.app.OverlayPush(ctx, w.workspace, pos[1])
		}
	default:
		w.lastErr = fmt.Errorf("unknown epos command %q", verb)
	}
}

// abs resolves a relative path argument against the scenario workspace.
func (w *world) abs(p string) string {
	if filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(w.workspace, p)
}

// parseArgs splits positionals from flags into an InstallOpts.
func parseArgs(args []string) ([]string, app.InstallOpts) {
	var pos []string
	opts := app.InstallOpts{Target: app.TargetFiles}
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--frozen":
			opts.Frozen = true
		case a == "--require-signature":
			opts.RequireSignature = true
		case strings.HasPrefix(a, "--target="):
			opts.Target = strings.TrimPrefix(a, "--target=")
		case a == "--target":
			i++
			opts.Target = args[i]
		case a == "-n" || a == "--namespace":
			i++
			opts.Namespace = args[i]
		case strings.HasPrefix(a, "--namespace="):
			opts.Namespace = strings.TrimPrefix(a, "--namespace=")
		case a == "--version":
			i++
			opts.Version = args[i]
		case strings.HasPrefix(a, "--version="):
			opts.Version = strings.TrimPrefix(a, "--version=")
		default:
			pos = append(pos, a)
		}
	}
	return pos, opts
}

// ---- local git helper (backend-agnostic; uses the git binary) ----

func (w *world) initGitSkill(subpath, ref string, files map[string]string) (string, error) {
	repo := filepath.Join(w.workspace, "gitrepo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		return "", err
	}
	run := func(args ...string) error {
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_TERMINAL_PROMPT=0")
		out, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("git %v: %v: %s", args, err, out)
		}
		return nil
	}
	if err := run("init", "-q"); err != nil {
		return "", err
	}
	_ = run("config", "user.email", "t@example.com")
	_ = run("config", "user.name", "t")
	for rel, content := range files {
		full := filepath.Join(repo, filepath.FromSlash(subpath), filepath.FromSlash(rel))
		_ = os.MkdirAll(filepath.Dir(full), 0o755)
		_ = os.WriteFile(full, []byte(content), 0o644)
	}
	if err := run("add", "-A"); err != nil {
		return "", err
	}
	if err := run("commit", "-q", "-m", "init"); err != nil {
		return "", err
	}
	if err := run("tag", ref); err != nil {
		return "", err
	}
	// Publish the repository to the real HTTP git server (Gitea) and use its
	// clone URL, so git-dependency resolution exercises real HTTP transport
	// (SPEC §15.3), not a local path.
	url, err := gitCloneURL(repo, "shared")
	if err != nil {
		return "", err
	}
	w.gitRemote = url
	return url, nil
}

// InitializeScenario registers the Before hook and all step definitions.
func InitializeScenario(sc *godog.ScenarioContext) {
	w := &world{}
	sc.Before(func(ctx context.Context, s *godog.Scenario) (context.Context, error) {
		return ctx, w.reset()
	})
	sc.After(func(ctx context.Context, s *godog.Scenario, err error) (context.Context, error) {
		for i := len(w.teardown) - 1; i >= 0; i-- {
			w.teardown[i]()
		}
		w.teardown = nil
		return ctx, nil
	})
	w.registerSteps(sc)
}
