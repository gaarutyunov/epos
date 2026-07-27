package install

import (
	"bytes"
	"fmt"
	"text/template"
)

// actionStart is the delimiter that makes a file a template. A file without
// one is copied through byte for byte, which is also what keeps a binary asset
// out of the parser.
var actionStart = []byte("{{")

// noValue is what text/template prints for a map key that is not there.
var noValue = []byte("<no value>")

// data is what a template is executed against: `.Values` and nothing else.
//
// 10.3 names no other binding, and adding one would be a promise about a
// namespace the spec has not defined.
type data struct {
	Values map[string]any
}

// Render substitutes values into one file (SPEC.md 10.3).
//
// Go text/template with **no custom functions**. The absence is deliberate:
// sprig and its relatives are a second language inside the skill, and a skill
// that renders with one client and not another stops being the plain directory
// 2.1 requires it to extract to.
//
// A missing value is an error rather than the `<no value>` Go prints by
// default. text/template's missingkey=error would say it more directly, but it
// also makes `{{ if .Values.optional }}` an error on the absent key it exists
// to test, so optionality would become unexpressible without the helper
// functions 10.3 rules out. Checking the output instead keeps `if` working and
// still refuses to ship a skill with a hole in it.
func Render(name string, src []byte, values map[string]any) ([]byte, error) {
	if !bytes.Contains(src, actionStart) {
		return src, nil
	}

	tmpl, err := template.New(name).Parse(string(src))
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", name, err)
	}

	var out bytes.Buffer
	if err := tmpl.Execute(&out, data{Values: values}); err != nil {
		return nil, fmt.Errorf("render %s: %w", name, err)
	}

	// Counted rather than merely looked for, so a skill that documents the
	// string `<no value>` in its own prose is not accused of having a hole.
	if bytes.Count(out.Bytes(), noValue) > bytes.Count(src, noValue) {
		return nil, fmt.Errorf("render %s: it uses a value that was not supplied", name)
	}
	return out.Bytes(), nil
}
