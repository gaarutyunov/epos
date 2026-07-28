package main

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/gaarutyunov/epos/internal/skillfile"
)

// TestEveryInstructionHasASection is the surface SPEC.md 14.1 asks for: every
// instruction, with its syntax and a worked example, on the page.
func TestEveryInstructionHasASection(t *testing.T) {
	out := string(renderSkillfile())

	for _, doc := range skillfile.NewReference().Instructions {
		assert.Contains(t, out, `<section id="`+strings.ToLower(doc.Op)+`">`,
			"%s has no section", doc.Op)
		assert.Contains(t, out, escape(doc.Syntax), "%s has no syntax line", doc.Op)
		assert.Contains(t, out, escape(strings.TrimRight(doc.Example.Skillfile, "\n")),
			"%s has no worked example", doc.Op)
	}
}

// TestMultiStageAndValuesAreCovered pins the two composed subjects, which are
// checklist items in their own right and are not any one instruction.
func TestMultiStageAndValuesAreCovered(t *testing.T) {
	out := string(renderSkillfile())

	assert.Contains(t, out, `<section id="multi-stage">`)
	assert.Contains(t, out, escape("COPY --from=shared reference.md references/shared.md"))
	assert.Contains(t, out, escape("FROM ./shared AS shared"))
	assert.Contains(t, out, `<section id="values-and-templating">`)
	assert.Contains(t, out, escape("model: '{{ .Values.model }}'"))
}
