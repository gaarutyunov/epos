package domain

// Epos OCI media types (SPEC §2.3, §2.4). Media types are Epos-specific so Helm
// and other OCI tooling correctly ignore Skill artifacts as an unknown type.
const (
	// MediaTypeSkillConfig is the config-blob media type carrying parsed
	// Epos.yaml metadata so a registry/frontend can read metadata without
	// pulling the whole package.
	MediaTypeSkillConfig = "application/vnd.epos.skill.config.v1+json"
	// MediaTypeSkillContent is the single tar+gzip content-layer media type
	// bundling the entire skill directory (Helm-style single-layer packaging).
	MediaTypeSkillContent = "application/vnd.epos.skill.content.v1.tar+gzip"
	// MediaTypeOverlayConfig is the overlay config-blob media type.
	MediaTypeOverlayConfig = "application/vnd.epos.overlay.config.v1+json"
	// MediaTypeOverlayContent is the overlay content-layer media type.
	MediaTypeOverlayContent = "application/vnd.epos.overlay.content.v1.tar+gzip"
)

// IsSkillConfigMediaType reports whether mt is the Epos skill config media type.
// This is the discriminator used to filter Skills from non-Skills during
// catalog discovery (SPEC §8.1).
func IsSkillConfigMediaType(mt string) bool { return mt == MediaTypeSkillConfig }
