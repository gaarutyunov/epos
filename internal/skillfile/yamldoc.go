package skillfile

import (
	"fmt"
	"strings"

	"github.com/goccy/go-yaml"
)

// yamlDoc is a YAML document being edited in place.
//
// SKILL.md is not a YAML file — it is Markdown with a leading frontmatter
// fence — so the document tracks the prefix and suffix around the block and
// puts them back afterwards. A plain .yaml file is the degenerate case with
// both empty.
type yamlDoc struct {
	path   string
	before string // everything up to and including the opening fence
	yaml   string
	after  string // everything from the closing fence on
	values map[string]any
}

// openYAML reads a document, finding the frontmatter block if there is one.
func openYAML(p string, body []byte) (*yamlDoc, error) {
	doc := &yamlDoc{path: p}

	if strings.HasSuffix(p, ".md") {
		before, block, after, ok := splitFrontmatter(string(body))
		if !ok {
			return nil, fmt.Errorf("%s has no --- frontmatter block to edit", p)
		}
		doc.before, doc.yaml, doc.after = before, block, after
	} else {
		doc.yaml = string(body)
	}

	if err := yaml.Unmarshal([]byte(doc.yaml), &doc.values); err != nil {
		return nil, fmt.Errorf("%s: %w", p, err)
	}
	if doc.values == nil {
		doc.values = map[string]any{}
	}
	return doc, nil
}

// set writes a dotted key, creating intermediate mappings.
//
// The value is parsed as a YAML scalar, so `SET count 3` writes a number and
// `SET count "3"` writes a string — quote to force one.
func (d *yamlDoc) set(key, raw string) error {
	var value any
	if err := yaml.Unmarshal([]byte(raw), &value); err != nil {
		// Not valid YAML on its own — treat it as the string it looks like.
		value = raw
	}
	if raw == "" {
		value = ""
	}

	parts := strings.Split(key, ".")
	node := d.values
	for _, p := range parts[:len(parts)-1] {
		next, ok := node[p].(map[string]any)
		if !ok {
			next = map[string]any{}
			node[p] = next
		}
		node = next
	}
	node[parts[len(parts)-1]] = value
	return nil
}

// unset removes a dotted key, reporting whether it was there.
func (d *yamlDoc) unset(key string) bool {
	parts := strings.Split(key, ".")
	node := d.values
	for _, p := range parts[:len(parts)-1] {
		next, ok := node[p].(map[string]any)
		if !ok {
			return false
		}
		node = next
	}

	last := parts[len(parts)-1]
	if _, ok := node[last]; !ok {
		return false
	}
	delete(node, last)
	return true
}

// bytes renders the document, putting any surrounding Markdown back.
func (d *yamlDoc) bytes() ([]byte, error) {
	block, err := yaml.Marshal(d.values)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", d.path, err)
	}
	return []byte(d.before + string(block) + d.after), nil
}

// splitFrontmatter cuts a Markdown document into the text before the block,
// the block itself, and the text after it.
func splitFrontmatter(src string) (before, block, after string, ok bool) {
	lines := strings.Split(strings.ReplaceAll(src, "\r\n", "\n"), "\n")
	if len(lines) == 0 || strings.TrimRight(lines[0], " \t") != "---" {
		return "", "", "", false
	}
	for i := 1; i < len(lines); i++ {
		if strings.TrimRight(lines[i], " \t") == "---" {
			return "---\n",
				strings.Join(lines[1:i], "\n") + "\n",
				strings.Join(lines[i:], "\n"),
				true
		}
	}
	return "", "", "", false
}
