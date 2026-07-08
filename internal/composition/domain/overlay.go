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

// wireOverlay mirrors OverlayManifest but decodes each operation's payload
// reference from the on-disk key `path:` (SPEC §9.4.1). The generated Operation
// value object stores it as PayloadPath (json:"payloadPath"), so we decode into
// this DTO — which also accepts the generated key as an alias — and map across.
// This keeps the fix in hand-owned code and survives sysgo regeneration.
type wireOverlay struct {
	APIVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`
	Name       string `json:"name"`
	Version    string `json:"version"`
	Operations []struct {
		Op          string `json:"op"`
		Target      string `json:"target"`
		Path        string `json:"path"`
		PayloadPath string `json:"payloadPath"`
		Content     string `json:"content"`
		Pattern     string `json:"pattern"`
		Replacement string `json:"replacement"`
		Required    bool   `json:"required"`
	} `json:"operations"`
}

// ParseOverlay decodes Overlay.yaml bytes, mapping the spec's `path:` payload
// reference onto the Operation value object.
func ParseOverlay(data []byte) (*OverlayManifest, error) {
	var w wireOverlay
	if err := yaml.Unmarshal(data, &w); err != nil {
		return nil, fmt.Errorf("parse Overlay.yaml: %w", err)
	}
	o := &OverlayManifest{APIVersion: w.APIVersion, Kind: w.Kind, Name: w.Name, Version: w.Version}
	for _, wo := range w.Operations {
		payloadPath := wo.Path
		if payloadPath == "" {
			payloadPath = wo.PayloadPath
		}
		o.Operations = append(o.Operations, Operation{
			Op:          wo.Op,
			Target:      wo.Target,
			PayloadPath: payloadPath,
			Content:     wo.Content,
			Pattern:     wo.Pattern,
			Replacement: wo.Replacement,
			Required:    wo.Required,
		})
	}
	return o, nil
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
		hasInline := op.Content != "" || op.Pattern != "" || op.Replacement != ""
		if op.PayloadPath != "" && hasInline {
			msgs = append(msgs, fmt.Sprintf("operation %d (%s): supply either path: or an inline payload, not both", i, op.Op))
		}
	}
	return msgs
}
