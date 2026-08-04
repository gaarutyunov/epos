package catalog

import (
	"bytes"
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/gaarutyunov/epos/internal/registry"
)

// exportMarker is written into every export and is what tells a directory this
// export may prune from one a human named by mistake.
//
// `--out` prunes pages it did not write, and a prune inside somebody's home
// directory is not recoverable. So the rule is: create the directory if it is
// absent, accept one carrying this marker, and refuse anything else. Never
// recursively delete a directory a human named.
const exportMarker = ".epos-catalog-export"

// Export renders the whole site into a directory.
//
// The same renderer, the same route table and the same templates the served
// catalog uses — export walks the routes, serve serves them, and the two
// produce identical bytes for the same base path, model and counts. A page only
// one of them can produce is a bug.
//
// Counts are read once and baked in with their capture time. That is the
// difference the deployed site has to be honest about: on the served catalog a
// pull followed by a reload moves the number, and on the export it does not
// move until the next export. It is a tested property rather than a production
// surprise.
func Export(ctx context.Context, renderer *Renderer, client registry.Client,
	stats Stats, out string) error {
	if err := prepareOut(out); err != nil {
		return err
	}

	counts := StatsOrNil(ctx, stats, func(err error) {
		fmt.Fprintf(os.Stderr, "epos-registry: the export could not read its counts: %v\n", err)
	})

	written := map[string]bool{}
	catalog := renderer.Catalog()

	for _, route := range catalog.Routes() {
		var document *Skill
		if route.Kind == PageDetail {
			skill, ok := catalog.Lookup(route.Repository)
			if !ok {
				return fmt.Errorf("the route table names %s, which is not in the catalog",
					route.Repository)
			}
			loaded := LoadDocument(ctx, client, skill)
			document = &loaded
		}

		var page bytes.Buffer
		if err := renderer.Render(&page, route, counts, document); err != nil {
			return fmt.Errorf("render %s: %w", route.Path, err)
		}
		if err := writeUnder(out, route.File(), page.Bytes(), written); err != nil {
			return err
		}
	}

	assets, err := Assets()
	if err != nil {
		return err
	}
	names := make([]string, 0, len(assets))
	for name := range assets {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if err := writeUnder(out, name, assets[name], written); err != nil {
			return err
		}
	}

	return prune(out, written)
}

// prepareOut creates or accepts the output directory.
func prepareOut(out string) error {
	if out == "" {
		return fmt.Errorf("an output directory is required: pass --out")
	}

	info, err := os.Stat(out)
	switch {
	case os.IsNotExist(err):
		if err := os.MkdirAll(out, 0o755); err != nil {
			return fmt.Errorf("create %s: %w", out, err)
		}
	case err != nil:
		return fmt.Errorf("read %s: %w", out, err)
	case !info.IsDir():
		return fmt.Errorf("%s is not a directory", out)
	default:
		entries, err := os.ReadDir(out)
		if err != nil {
			return fmt.Errorf("read %s: %w", out, err)
		}
		if len(entries) > 0 {
			if _, err := os.Stat(filepath.Join(out, exportMarker)); err != nil {
				return fmt.Errorf("%s is not empty and is not a previous export "+
					"(no %s in it); pass an empty or new directory, because an export "+
					"prunes files it did not write", out, exportMarker)
			}
		}
	}

	return os.WriteFile(filepath.Join(out, exportMarker),
		[]byte("Written by `epos-registry catalog export`. Its presence is what lets a\n"+
			"later export prune this directory; removing it makes the next export refuse\n"+
			"to write here.\n"), 0o644)
}

// writeUnder writes one file, after proving its path resolves inside out.
//
// Route paths come from registry-supplied repository names, so this is a
// containment check and not a formality: a repository called `../../etc` would
// otherwise write outside the directory the operator named. Checked on the
// resolved path rather than on the name, because the name is exactly what an
// attacker controls.
func writeUnder(out, name string, body []byte, written map[string]bool) error {
	target := filepath.Join(out, filepath.FromSlash(name))

	root, err := filepath.Abs(out)
	if err != nil {
		return err
	}
	resolved, err := filepath.Abs(target)
	if err != nil {
		return err
	}
	if resolved != root && !strings.HasPrefix(resolved, root+string(filepath.Separator)) {
		return fmt.Errorf("%s resolves outside %s and was not written", name, out)
	}

	if err := os.MkdirAll(filepath.Dir(resolved), 0o755); err != nil {
		return fmt.Errorf("create %s: %w", filepath.Dir(resolved), err)
	}
	if err := os.WriteFile(resolved, body, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", name, err)
	}
	written[filepath.ToSlash(name)] = true
	return nil
}

// prune removes files this export did not write.
//
// A skill removed from the registry has to disappear from the site, and a
// publish that only ever adds accumulates pages nothing links to. The marker
// prepareOut wrote is kept.
func prune(out string, written map[string]bool) error {
	var stale []string
	err := filepath.WalkDir(out, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, err := filepath.Rel(out, p)
		if err != nil {
			return err
		}
		slash := filepath.ToSlash(rel)
		if slash == exportMarker || written[slash] {
			return nil
		}
		stale = append(stale, p)
		return nil
	})
	if err != nil {
		return fmt.Errorf("read %s: %w", out, err)
	}

	for _, p := range stale {
		if err := os.Remove(p); err != nil {
			return fmt.Errorf("remove the stale page %s: %w", p, err)
		}
	}
	return nil
}

// GuardInputs refuses an --out that would delete the export's own inputs.
//
// export prunes files it did not write. Pointed at the directory holding its
// reference list or its counts file, it deletes them — and the second run then
// fails for a reason that has nothing to do with the first.
func GuardInputs(out string, inputs ...string) error {
	root, err := filepath.Abs(out)
	if err != nil {
		return err
	}
	for _, input := range inputs {
		if input == "" {
			continue
		}
		resolved, err := filepath.Abs(input)
		if err != nil {
			continue
		}
		if resolved == root || strings.HasPrefix(resolved, root+string(filepath.Separator)) {
			return fmt.Errorf("%s is inside --out %s, and an export prunes what it did not "+
				"write: it would delete its own input", input, out)
		}
	}
	return nil
}
