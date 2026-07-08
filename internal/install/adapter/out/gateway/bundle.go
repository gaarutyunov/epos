// Code scaffolded by sysgo; edit freely (not regenerated).

package gateway

import (
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"encoding/json"
)

// bundle is the self-contained revision payload encoded into domain.Revision.Blob
// (Helm-style JSON → gzip → base64, SPEC §14.6). It carries the rendered files so
// rollback restores the whole bundle.
type bundle struct {
	Version string            `json:"version"`
	Digest  string            `json:"digest"`
	Values  string            `json:"values"`
	Files   map[string][]byte `json:"files"`
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
