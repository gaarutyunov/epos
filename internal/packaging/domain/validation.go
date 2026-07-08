package domain

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Skill-name rules (SPEC §2.2): lowercase letters, numbers, and hyphens only;
// max 64 chars; reserved words "anthropic"/"claude" disallowed (case-insensitive
// substrings). This is also the OCI repository-path component.
var (
	nameRe        = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)
	angleBracket  = regexp.MustCompile(`[<>]`)
	reservedWords = []string{"anthropic", "claude"}
	// includeReference "path" or includeReference `path` in the template body.
	includeRefRe = regexp.MustCompile("includeReference\\s+[\"`]([^\"`]+)[\"`]")
	// Markdown link/mention to a supporting file under references/scripts/assets.
	supportLinkRe = regexp.MustCompile(`(references|scripts|assets)/[A-Za-z0-9._/-]+`)
)

// ValidateManifest enforces the strict Agent-Skills alignment (SPEC §2.2). dirName
// is the package directory basename that name must equal. Returns the list of
// human-readable violation messages (empty ⇒ valid).
func ValidateManifest(m *Manifest, dirName string) []string {
	var msgs []string

	// name
	switch m.Name {
	case "":
		msgs = append(msgs, "name is required")
	default:
		lower := strings.ToLower(m.Name)
		if !nameRe.MatchString(m.Name) {
			msgs = append(msgs, fmt.Sprintf("name %q must be lowercase letters, numbers, and hyphens only (^[a-z0-9]+(-[a-z0-9]+)*$)", m.Name))
		}
		if len(m.Name) > 64 {
			msgs = append(msgs, fmt.Sprintf("name %q exceeds the 64-character maximum", m.Name))
		}
		if dirName != "" && m.Name != dirName {
			msgs = append(msgs, fmt.Sprintf("name %q must equal the package directory name %q", m.Name, dirName))
		}
		for _, w := range reservedWords {
			if strings.Contains(lower, w) {
				msgs = append(msgs, fmt.Sprintf("name %q must not contain the reserved word %q", m.Name, w))
			}
		}
	}

	// version
	if m.Version == "" {
		msgs = append(msgs, "version is required")
	} else if _, err := ParseSemVer(m.Version); err != nil {
		msgs = append(msgs, fmt.Sprintf("version %q is not strict SemVer 2.0.0", m.Version))
	}

	// description
	switch {
	case strings.TrimSpace(m.Description) == "":
		msgs = append(msgs, "description is required and must be non-empty")
	default:
		if len(m.Description) > 1024 {
			msgs = append(msgs, "description exceeds the 1024-character maximum")
		}
		if angleBracket.MatchString(m.Description) {
			msgs = append(msgs, "description must not contain XML/angle-bracket tags")
		}
	}

	return msgs
}

// LintDir validates the manifest and performs dangling-reference validation
// (SPEC §3.5): every path a template can emit via includeReference and every
// static reference link in the body must resolve to a file in the package.
func LintDir(dir string) ([]string, error) {
	dirName := filepath.Base(dir)

	data, err := os.ReadFile(filepath.Join(dir, "Epos.yaml"))
	if err != nil {
		return nil, fmt.Errorf("read Epos.yaml: %w", err)
	}
	m, err := ParseManifest(data)
	if err != nil {
		return []string{err.Error()}, nil
	}
	msgs := ValidateManifest(m, dirName)

	// Dangling references: scan SKILL.md (template source) for reference targets.
	body, err := os.ReadFile(filepath.Join(dir, "SKILL.md"))
	if err != nil {
		return append(msgs, "SKILL.md is required"), nil
	}
	for _, target := range referencedPaths(string(body)) {
		if _, err := os.Stat(filepath.Join(dir, target)); err != nil {
			msgs = append(msgs, fmt.Sprintf("dangling reference %q: file does not exist in the package", target))
		}
	}
	return msgs, nil
}

// referencedPaths returns the deduplicated set of supporting-file paths the
// SKILL.md body reaches, via includeReference calls and static markdown links.
func referencedPaths(body string) []string {
	seen := map[string]bool{}
	var out []string
	add := func(p string) {
		p = strings.TrimSpace(p)
		if p == "" || seen[p] {
			return
		}
		seen[p] = true
		out = append(out, p)
	}
	for _, mm := range includeRefRe.FindAllStringSubmatch(body, -1) {
		add(mm[1])
	}
	for _, p := range supportLinkRe.FindAllString(body, -1) {
		add(p)
	}
	return out
}
