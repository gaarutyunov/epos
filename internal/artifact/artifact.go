// Package artifact turns a skill directory into the OCI artifact of SPEC.md 2:
// a config blob derived from SKILL.md frontmatter, and exactly one content
// layer that is a deterministic tar+gzip.
//
// Nothing here is Epos-specific on the wire. An artifact this package produces
// conforms to the Agent Skills OCI Artifacts spec (2.1) and is pullable by any
// conforming client — oras, skills-oci, Arconia CLI — with no knowledge of
// Epos.
package artifact

import (
	"encoding/json"
	"fmt"

	"github.com/goccy/go-yaml"
)

// Media types of the Agent Skills OCI Artifacts spec (SPEC.md 2.1).
const (
	ArtifactType     = "application/vnd.agentskills.skill.v1"
	ConfigMediaType  = "application/vnd.agentskills.skill.config.v1+json"
	ContentMediaType = "application/vnd.agentskills.skill.content.v1.tar+gzip"
	CollectionType   = "application/vnd.agentskills.collection.v1"
)

// SkillFile is the file whose frontmatter becomes the config blob.
const SkillFile = "SKILL.md"

// DownloadHeader marks a download verified (SPEC.md 5.2). The epos CLI sends
// it; stock oras does not, which is what lets epos-registry tell a verified
// download from an inflated one.
const DownloadHeader = "Epos-Download"

// Config is the config blob: SKILL.md's frontmatter, as JSON.
//
// 2.1 says the blob "mirrors SKILL.md frontmatter", so the frontmatter's own
// keys are carried through rather than projected onto a fixed struct — a skill
// may declare fields Epos has never heard of, and dropping them would make the
// artifact a lossy copy of the directory it came from.
type Config map[string]any

// Name is the skill's name, which the repository also encodes (2.1).
func (c Config) Name() string { return c.string("name") }

// Description is what a discovery client shows for the skill.
func (c Config) Description() string { return c.string("description") }

func (c Config) string(key string) string {
	s, _ := c[key].(string)
	return s
}

// JSON renders the config blob.
//
// encoding/json sorts map keys, so the same frontmatter always produces the
// same bytes and therefore the same digest — 2.4's determinism applies to the
// config blob as much as to the layer.
func (c Config) JSON() ([]byte, error) {
	b, err := json.Marshal(map[string]any(c))
	if err != nil {
		return nil, fmt.Errorf("encode config blob: %w", err)
	}
	return b, nil
}

// ParseFrontmatter reads the YAML frontmatter block from SKILL.md's contents.
//
// The block is the document's leading "---" fence. A file without one has no
// frontmatter to mirror, which is an error rather than an empty config: the
// name and description live there, and 6.1 has pack derive the config from
// them.
func ParseFrontmatter(src []byte) (Config, error) {
	block, err := frontmatterBlock(src)
	if err != nil {
		return nil, err
	}

	var cfg Config
	if err := yaml.Unmarshal(block, &cfg); err != nil {
		return nil, fmt.Errorf("parse %s frontmatter: %w", SkillFile, err)
	}
	if cfg == nil {
		cfg = Config{}
	}
	if cfg.Name() == "" {
		return nil, fmt.Errorf("%s frontmatter has no name", SkillFile)
	}
	return cfg, nil
}

// frontmatterBlock returns the bytes between the opening and closing fences.
func frontmatterBlock(src []byte) ([]byte, error) {
	lines, nl := splitLines(src)
	if len(lines) == 0 || !isFence(lines[0]) {
		return nil, fmt.Errorf("%s does not open with a --- frontmatter fence", SkillFile)
	}
	for i := 1; i < len(lines); i++ {
		if isFence(lines[i]) {
			return []byte(joinLines(lines[1:i], nl)), nil
		}
	}
	return nil, fmt.Errorf("%s frontmatter fence is never closed", SkillFile)
}
