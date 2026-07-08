// Code scaffolded by sysgo; edit freely (not regenerated).

package gateway

import (
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"encoding/json"

	"github.com/gaarutyunov/epos/internal/install/app/port/out"
)

// bundlePin is an overlay pin recorded in an in-cluster revision (SPEC §9.7).
type bundlePin struct {
	Name   string `json:"name"`
	Digest string `json:"digest"`
}

// bundle is the self-contained revision payload encoded into domain.Revision.Blob
// (Helm-style JSON → gzip → base64, SPEC §14.6). It carries the rendered files,
// the resolved values, and the pinned overlays so rollback restores the whole
// bundle (§5.3, §14.6).
type bundle struct {
	Version  string            `json:"version"`
	Digest   string            `json:"digest"`
	Registry string            `json:"registry,omitempty"`
	Values   map[string]any    `json:"values,omitempty"`
	Overlays []bundlePin       `json:"overlays,omitempty"`
	Files    map[string][]byte `json:"files"`
}

// toBundlePins converts port overlay pins to bundle pins.
func toBundlePins(pins []out.OverlayPin) []bundlePin {
	if len(pins) == 0 {
		return nil
	}
	o := make([]bundlePin, len(pins))
	for i, p := range pins {
		o[i] = bundlePin{Name: p.Name, Digest: p.Digest}
	}
	return o
}

// fromBundlePins converts bundle pins to port overlay pins.
func fromBundlePins(pins []bundlePin) []out.OverlayPin {
	if len(pins) == 0 {
		return nil
	}
	o := make([]out.OverlayPin, len(pins))
	for i, p := range pins {
		o[i] = out.OverlayPin{Name: p.Name, Digest: p.Digest}
	}
	return o
}

func encodeBundle(b bundle) (string, error) {
	raw, err := json.Marshal(b)
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	if _, err := gz.Write(raw); err != nil {
		return "", err
	}
	if err := gz.Close(); err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(buf.Bytes()), nil
}

func decodeBundle(s string) (*bundle, error) {
	data, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return nil, err
	}
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	defer gz.Close()
	var buf bytes.Buffer
	if _, err := buf.ReadFrom(gz); err != nil {
		return nil, err
	}
	var b bundle
	if err := json.Unmarshal(buf.Bytes(), &b); err != nil {
		return nil, err
	}
	return &b, nil
}
