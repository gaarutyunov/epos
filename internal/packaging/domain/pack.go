package domain

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/opencontainers/image-spec/specs-go"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

// Blob is an OCI blob with its computed descriptor.
type Blob struct {
	MediaType string
	Data      []byte
	Digest    string // "sha256:..."
	Size      int64
}

func newBlob(mt string, data []byte) Blob {
	sum := sha256.Sum256(data)
	return Blob{
		MediaType: mt,
		Data:      data,
		Digest:    "sha256:" + fmt.Sprintf("%x", sum),
		Size:      int64(len(data)),
	}
}

// Descriptor renders the blob as an OCI descriptor.
func (b Blob) Descriptor() ocispec.Descriptor {
	return ocispec.Descriptor{
		MediaType: b.MediaType,
		Digest:    parseDigest(b.Digest),
		Size:      b.Size,
	}
}

// Artifact is a fully-built, in-memory Epos OCI artifact: a config blob, a
// single tar+gzip content layer, and an image manifest referencing both.
type Artifact struct {
	Manifest      *Manifest
	Config        Blob
	Content       Blob
	ImageManifest Blob   // the marshaled OCI image manifest
	Tag           string // OCI-safe tag
	Files         []SkillFile
}

// ManifestDigest returns the artifact's authoritative manifest digest.
func (a *Artifact) ManifestDigest() string { return a.ImageManifest.Digest }

// BuildArtifact reads a skill package directory and produces the in-memory OCI
// artifact: a single tar+gzip of the whole directory (SPEC §2.3) plus a config
// blob carrying the parsed Epos.yaml metadata, wrapped in an OCI image manifest
// tagged with the OCI-safe version.
func BuildArtifact(dir string) (*Artifact, error) {
	data, err := os.ReadFile(filepath.Join(dir, "Epos.yaml"))
	if err != nil {
		return nil, fmt.Errorf("read Epos.yaml: %w", err)
	}
	m, err := ParseManifest(data)
	if err != nil {
		return nil, err
	}
	if msgs := ValidateManifest(m, filepath.Base(dir)); len(msgs) > 0 {
		return nil, fmt.Errorf("validation failed: %s", strings.Join(msgs, "; "))
	}

	tgz, files, err := tarGzDir(dir)
	if err != nil {
		return nil, err
	}
	configJSON, err := json.Marshal(m)
	if err != nil {
		return nil, err
	}

	art := &Artifact{Manifest: m, Files: files, Tag: OCITag(m.Version)}
	art.Config = newBlob(MediaTypeSkillConfig, configJSON)
	art.Content = newBlob(MediaTypeSkillContent, tgz)

	im := ocispec.Manifest{
		Versioned:    specs.Versioned{SchemaVersion: 2},
		MediaType:    ocispec.MediaTypeImageManifest,
		ArtifactType: MediaTypeSkillConfig,
		Config:       art.Config.Descriptor(),
		Layers:       []ocispec.Descriptor{art.Content.Descriptor()},
		Annotations: map[string]string{
			"org.opencontainers.image.title":   m.Name,
			"org.opencontainers.image.version": m.Version,
		},
	}
	imBytes, err := json.Marshal(im)
	if err != nil {
		return nil, err
	}
	art.ImageManifest = newBlob(ocispec.MediaTypeImageManifest, imBytes)
	return art, nil
}

// tarGzDir builds a deterministic tar+gzip of every regular file under dir and
// returns the archive plus the SkillFile inventory (with binary detection).
func tarGzDir(dir string) ([]byte, []SkillFile, error) {
	var paths []string
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		paths = append(paths, path)
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	sort.Strings(paths) // deterministic layout

	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	var files []SkillFile
	for _, p := range paths {
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return nil, nil, err
		}
		rel = filepath.ToSlash(rel)
		content, err := os.ReadFile(p)
		if err != nil {
			return nil, nil, err
		}
		hdr := &tar.Header{
			Name:    rel,
			Mode:    0o644,
			Size:    int64(len(content)),
			ModTime: fixedModTime,
		}
		if err := tw.WriteHeader(hdr); err != nil {
			return nil, nil, err
		}
		if _, err := tw.Write(content); err != nil {
			return nil, nil, err
		}
		files = append(files, SkillFile{
			Path:      rel,
			IsBinary:  !utf8.Valid(content),
			SizeBytes: int64(len(content)),
		})
	}
	if err := tw.Close(); err != nil {
		return nil, nil, err
	}
	if err := gz.Close(); err != nil {
		return nil, nil, err
	}
	return buf.Bytes(), files, nil
}

// UnpackTarGz extracts a tar+gzip content layer into a path→bytes map.
func UnpackTarGz(tgz []byte) (map[string][]byte, error) {
	gz, err := gzip.NewReader(bytes.NewReader(tgz))
	if err != nil {
		return nil, err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	out := map[string][]byte{}
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		clean := filepath.ToSlash(filepath.Clean(hdr.Name))
		if strings.HasPrefix(clean, "..") || filepath.IsAbs(clean) {
			return nil, fmt.Errorf("unsafe path in archive: %q", hdr.Name)
		}
		data, err := io.ReadAll(tr) //nolint:gosec // bounded by registry blob size
		if err != nil {
			return nil, err
		}
		out[clean] = data
	}
	return out, nil
}

// UnpackContent extracts a tar+gzip content layer into dstDir.
func UnpackContent(tgz []byte, dstDir string) error {
	gz, err := gzip.NewReader(bytes.NewReader(tgz))
	if err != nil {
		return err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		clean := filepath.Clean(hdr.Name)
		if strings.HasPrefix(clean, "..") || filepath.IsAbs(clean) {
			return fmt.Errorf("unsafe path in archive: %q", hdr.Name)
		}
		dst := filepath.Join(dstDir, clean)
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		out, err := os.Create(dst)
		if err != nil {
			return err
		}
		if _, err := io.Copy(out, tr); err != nil { //nolint:gosec // bounded by registry blob size
			out.Close()
			return err
		}
		out.Close()
	}
	return nil
}
