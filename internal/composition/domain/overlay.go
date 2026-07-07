package domain

import (
	"fmt"

	"sigs.k8s.io/yaml"
)

// Overlay operation names (SPEC §9.4).
const (
	OpAddFile      = "add-file"
	OpDeleteFile   = "delete-file"
	OpAppendToFile = "append-to-file"
	OpReplaceIn    = "replace-in-file"
	OpPatchFile    = "patch-file"
)

// OverlayManifest is the parsed Overlay.yaml (SPEC §9.4.1): a single ordered
// operations list where each operation supplies its payload inline or via a
// sibling file referenced by path:.
type OverlayManifest struct {
	APIVersion string      `json:"apiVersion"`
	Kind       string      `json:"kind"`
	Name       string      `json:"name"`
	Version    string      `json:"version"`
	Operations []Operation `json:"operations"`
}

// ParseOverlay decodes Overlay.yaml bytes.
func ParseOverlay(data []byte) (*OverlayManifest, error) {
	var o OverlayManifest
	if err := yaml.Unmarshal(data, &o); err != nil {
		return nil, fmt.Errorf("parse Overlay.yaml: %w", err)
	}
	return &o, nil
}

// Marshal serializes the overlay manifest (used for the overlay config blob).
func (o *OverlayManifest) Marshal() ([]byte, error) { return yaml.Marshal(o) }

// Validate checks operation shapes: exactly one payload source where required.
func (o *OverlayManifest) Validate() []string {
	var msgs []string
	for i, op := range o.Operations {
		switch op.Op {
		case OpAddFile, OpDeleteFile, OpAppendToFile, OpReplaceIn, OpPatchFile:
		default:
			msgs = append(msgs, fmt.Sprintf("operation %d: unknown op %q", i, op.Op))
			continue
		}
		if op.Target == "" {
			msgs = append(msgs, fmt.Sprintf("operation %d (%s): target is required", i, op.Op))
		}
		// Exactly one payload source where the op needs one.
		hasInline := op.Content != "" || op.Pattern != ""
		if op.PayloadPath != "" && hasInline {
			msgs = append(msgs, fmt.Sprintf("operation %d (%s): supply either path: or an inline payload, not both", i, op.Op))
		}
	}
	return msgs
}
