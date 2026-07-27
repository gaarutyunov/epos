package skillfile

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/goccy/go-yaml/ast"
	"github.com/goccy/go-yaml/parser"
)

// yamlDoc is a YAML document being edited in place.
//
// SKILL.md is not a YAML file — it is Markdown with a leading frontmatter
// fence — so the document tracks the prefix and suffix around the block and
// puts them back afterwards. A plain .yaml file is the degenerate case with
// both empty.
//
// The block is held as goccy's AST, parsed with parser.ParseComments and
// rendered back with File.String(), which is the mechanism 8.2.4 prescribes.
// The mechanism is the point: unmarshalling into a map and re-marshalling
// would round-trip the *data* and throw the *document* away — comments gone,
// keys alphabetised, `"1.0.0"` silently reduced to 1.0.0 — and a skill
// author's frontmatter is theirs, not ours to normalise. Mutating the AST also
// keeps 2.4 out of danger by construction: nothing here ever ranges a Go map,
// so there is no iteration order to leak into the bytes and no digest to
// destabilise.
type yamlDoc struct {
	path   string
	before string // everything up to and including the opening fence
	after  string // everything from the closing fence on
	file   *ast.File
}

// openYAML reads a document, finding the frontmatter block if there is one.
func openYAML(p string, body []byte) (*yamlDoc, error) {
	doc := &yamlDoc{path: p}

	block := string(body)
	if strings.HasSuffix(p, ".md") {
		before, frontmatter, after, ok := splitFrontmatter(block)
		if !ok {
			return nil, fmt.Errorf("%s has no --- frontmatter block to edit", p)
		}
		doc.before, block, doc.after = before, frontmatter, after
	}

	file, err := parser.ParseBytes([]byte(block), parser.ParseComments)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", p, err)
	}
	doc.file = file
	return doc, nil
}

// set writes a dotted key, creating intermediate mappings.
//
// raw is the value as the Skillfile wrote it, with the tokenizer's quotes
// already off; forceString says whether those quotes were there. 8.2.4 parses
// an unquoted value as a YAML scalar — `SET count 3` is a number — and makes a
// quoted one a string, which is the only reason the tokenizer bothers to
// remember the quoting at all.
//
// An existing key keeps its place in the mapping and its trailing comment;
// only its value moves. A new key is appended, which leaves every key already
// in the document exactly where its author put it.
func (d *yamlDoc) set(key, raw string, forceString bool) error {
	parts := strings.Split(key, ".")

	mapping := d.rootMapping()
	if mapping == nil {
		// An empty document — or one holding nothing but comments — has no
		// mapping to mutate, so the first SET establishes one.
		file, err := parseNested(parts, raw, forceString)
		if err != nil {
			return fmt.Errorf("%s: %w", d.path, err)
		}
		d.file = file
		return nil
	}

	for i, part := range parts {
		existing := lookup(mapping, part)

		if i == len(parts)-1 {
			pair, err := parsePair(part, raw, forceString)
			if err != nil {
				return fmt.Errorf("%s: %w", d.path, err)
			}
			if existing == nil {
				appendPair(mapping, pair)
				return nil
			}
			return replaceValue(existing, pair.Value)
		}

		if next, ok := mappingOf(existing); ok {
			mapping = next
			continue
		}

		// An intermediate key that is absent, or that holds something a dotted
		// path cannot descend into, is written as the whole remaining path in
		// one nested block. Overwriting a scalar is what the map-based
		// implementation did, and `SET metadata.author acme` against
		// `metadata: none` has no other reading.
		pair, err := nestedPair(parts[i:], raw, forceString)
		if err != nil {
			return fmt.Errorf("%s: %w", d.path, err)
		}
		if existing == nil {
			appendPair(mapping, pair)
			return nil
		}
		return replacePair(mapping, existing, pair)
	}
	return nil
}

// unset removes a dotted key, reporting whether it was there.
//
// Only the key's own entry goes. A comment on a line of its own belongs to the
// key it sits above, so it leaves with that key and every other comment stays
// attached to the key it was written against.
func (d *yamlDoc) unset(key string) bool {
	parts := strings.Split(key, ".")

	mapping := d.rootMapping()
	if mapping == nil {
		return false
	}
	for _, part := range parts[:len(parts)-1] {
		next, ok := mappingOf(lookup(mapping, part))
		if !ok {
			return false
		}
		mapping = next
	}

	last := parts[len(parts)-1]
	for i, v := range mapping.Values {
		if keyName(v.Key) != last {
			continue
		}
		mapping.Values = append(mapping.Values[:i:i], mapping.Values[i+1:]...)
		return true
	}
	return false
}

// bytes renders the document, putting any surrounding Markdown back.
func (d *yamlDoc) bytes() ([]byte, error) {
	block := d.file.String()
	// One terminating newline, whatever the AST printer produced: the closing
	// fence has to start a line of its own, and a document that gained or lost
	// a trailing blank line on every edit would make SET non-idempotent.
	block = strings.TrimRight(block, "\n") + "\n"
	return []byte(d.before + block + d.after), nil
}

// rootMapping returns the document's top-level mapping, or nil if it has none.
func (d *yamlDoc) rootMapping() *ast.MappingNode {
	for _, doc := range d.file.Docs {
		if m, ok := doc.Body.(*ast.MappingNode); ok {
			return m
		}
	}
	return nil
}

// lookup finds a mapping's entry for a key, or nil.
func lookup(m *ast.MappingNode, key string) *ast.MappingValueNode {
	for _, v := range m.Values {
		if keyName(v.Key) == key {
			return v
		}
	}
	return nil
}

// keyName is a key node's text, without whatever comment is attached to it.
// Key.String() would carry the comment along and never match.
func keyName(k ast.MapKeyNode) string {
	if tk := k.GetToken(); tk != nil {
		return tk.Value
	}
	return ""
}

// mappingOf is the mapping an entry's value is, if it is one.
func mappingOf(v *ast.MappingValueNode) (*ast.MappingNode, bool) {
	if v == nil {
		return nil, false
	}
	m, ok := v.Value.(*ast.MappingNode)
	return m, ok
}

// appendPair adds an entry to the end of a mapping, indented to match it.
//
// Appending rather than inserting is what keeps 8.2.4's ordering promise: a
// SET that adds a key must not disturb the keys already there.
func appendPair(m *ast.MappingNode, pair *ast.MappingValueNode) {
	alignPair(pair, mappingColumn(m))
	m.Values = append(m.Values, pair)
}

// replaceValue swaps an entry's value, keeping the comment trailing the line.
//
// goccy hangs an inline comment off the *value* node, so replacing the value
// would take `version: 1.0.0 # pinned by hand` down to `version: 2.0.0` and
// quietly delete a line of the author's prose.
func replaceValue(entry *ast.MappingValueNode, value ast.Node) error {
	comment := entry.Value.GetComment()
	if err := entry.Replace(value); err != nil {
		return err
	}
	if comment != nil {
		return entry.Value.SetComment(comment)
	}
	return nil
}

// replacePair swaps a whole entry, keeping its place in the mapping and the
// comment written above it. Only the path that turns a scalar into a nested
// mapping needs it; every other edit replaces a value, not an entry.
func replacePair(m *ast.MappingNode, old, pair *ast.MappingValueNode) error {
	alignPair(pair, mappingColumn(m))
	if comment := old.GetComment(); comment != nil {
		if err := pair.SetComment(comment); err != nil {
			return err
		}
	}
	for i, v := range m.Values {
		if v == old {
			m.Values[i] = pair
			return nil
		}
	}
	return fmt.Errorf("%s is not in the mapping it was found in", keyName(old.Key))
}

// mappingColumn is the column a mapping's keys start at.
func mappingColumn(m *ast.MappingNode) int {
	if len(m.Values) > 0 {
		return m.Values[0].Key.GetToken().Position.Column
	}
	return m.Start.Position.Column
}

// alignPair shifts an entry, and everything under it, to start at col.
//
// A node parsed on its own starts at column 1; spliced into a nested mapping
// unshifted it would render at the wrong indentation and change the document's
// meaning.
func alignPair(pair *ast.MappingValueNode, col int) {
	pair.AddColumn(col - pair.Key.GetToken().Position.Column)
}

// parseNested renders a dotted path as a nested block and parses it, so
// `SET metadata.author acme` on a document without `metadata` writes the
// mapping and the key together.
func parseNested(parts []string, raw string, forceString bool) (*ast.File, error) {
	value, err := valueLiteral(raw, forceString)
	if err != nil {
		return nil, err
	}

	var b strings.Builder
	for i, part := range parts {
		b.WriteString(strings.Repeat("  ", i))
		b.WriteString(keyLiteral(part))
		b.WriteString(":")
		if i == len(parts)-1 {
			b.WriteString(" " + value)
		}
		b.WriteString("\n")
	}

	file, err := parser.ParseBytes([]byte(b.String()), parser.ParseComments)
	if err != nil {
		return nil, err
	}
	if _, ok := onePair(file); !ok {
		return nil, fmt.Errorf("cannot write %q as a YAML value", raw)
	}
	return file, nil
}

// nestedPair is parseNested's document as the single entry it holds.
func nestedPair(parts []string, raw string, forceString bool) (*ast.MappingValueNode, error) {
	file, err := parseNested(parts, raw, forceString)
	if err != nil {
		return nil, err
	}
	pair, _ := onePair(file)
	return pair, nil
}

// parsePair builds one `key: value` entry.
func parsePair(key, raw string, forceString bool) (*ast.MappingValueNode, error) {
	return nestedPair([]string{key}, raw, forceString)
}

// onePair returns a parsed document's single top-level entry.
func onePair(f *ast.File) (*ast.MappingValueNode, bool) {
	if len(f.Docs) != 1 {
		return nil, false
	}
	m, ok := f.Docs[0].Body.(*ast.MappingNode)
	if !ok || len(m.Values) != 1 {
		return nil, false
	}
	return m.Values[0], true
}

// plainKey matches a key that can be written without quoting.
var plainKey = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.\-/]*$`)

// keyLiteral renders a key, quoting it when it would not survive plain.
func keyLiteral(key string) string {
	if plainKey.MatchString(key) {
		return key
	}
	return strconv.Quote(key)
}

// valueLiteral renders a value as the YAML the author wrote.
//
// 8.2.4 parses values as YAML scalars, so an unquoted value goes in as written
// rather than through a Go value and back out — that round trip is what would
// lose `SET count 3` being a number.
//
// forceString is 8.2.4's other half: quote to force a string. The author's
// quotes are gone by the time the value gets here (the tokenizer resolves
// them), so the string is re-quoted for YAML, which is what keeps `1.2` from
// being read back as a float.
//
// Anything that is not a scalar the parser can put after a `key: ` becomes the
// string it looks like, which is what the map-based implementation did with a
// value that failed to unmarshal. An alias is refused the same way: `*x` would
// parse but name an anchor that is not in the document, so writing it through
// would produce YAML that no longer loads.
func valueLiteral(raw string, forceString bool) (string, error) {
	// An empty value is a string too, and distinct from a null: the
	// instruction gave a value, and it was empty.
	if !forceString && strings.TrimSpace(raw) != "" && usable(raw) {
		return raw, nil
	}
	quoted := strconv.Quote(raw)
	if !usable(quoted) {
		return "", fmt.Errorf("cannot write %q as a YAML value", raw)
	}
	return quoted, nil
}

// usable reports whether raw can stand as the value half of a `key: ` line.
func usable(raw string) bool {
	file, err := parser.ParseBytes([]byte("k: "+raw), parser.ParseComments)
	if err != nil {
		return false
	}
	pair, ok := onePair(file)
	if !ok {
		return false
	}
	switch pair.Value.(type) {
	case *ast.AliasNode, *ast.AnchorNode, *ast.TagNode:
		return false
	}
	return true
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
