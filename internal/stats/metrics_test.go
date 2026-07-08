package stats

import (
	"strings"
	"testing"
)

func TestWriteProm(t *testing.T) {
	c := New()
	c.CountManifestGet("reg1", "team/pdf-tools", false)
	c.CountManifestGet("reg1", "team/pdf-tools", false)
	c.CountManifestGet("reg1", "index/multi", true) // index: not counted
	c.CountError()

	var aggregate strings.Builder
	c.WriteProm(&aggregate, false)
	out := aggregate.String()
	if !strings.Contains(out, "epos_pulls_total 2") {
		t.Errorf("total wrong:\n%s", out)
	}
	if !strings.Contains(out, `epos_pulls_by_registry_total{registry="reg1"} 2`) {
		t.Errorf("per-registry wrong:\n%s", out)
	}
	if !strings.Contains(out, "epos_pull_errors_total 1") {
		t.Errorf("errors wrong:\n%s", out)
	}
	if strings.Contains(out, "epos_pulls_by_skill_total") {
		t.Errorf("per-skill series must be suppressed when perSkill=false:\n%s", out)
	}

	var withSkill strings.Builder
	c.WriteProm(&withSkill, true)
	if !strings.Contains(withSkill.String(), `epos_pulls_by_skill_total{skill="pdf-tools"} 2`) {
		t.Errorf("per-skill series missing:\n%s", withSkill.String())
	}
}
