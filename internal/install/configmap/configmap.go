// Package configmap renders a composed skill as mountable Kubernetes
// ConfigMap(s) — the files-as-keys projection with items[].path tree
// reconstruction and 1 MiB auto-split (SPEC §14).
package configmap

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	"sigs.k8s.io/yaml"
)

// SizeCeiling is the etcd ConfigMap object ceiling that governs auto-split
// (SPEC §14.2). Rendered content (including binaryData base64 inflation) beyond
// this forces one ConfigMap per subtree.
const SizeCeiling = 1 << 20 // 1 MiB

// keySanitize maps a real relative path to a flat ConfigMap key. Keys are
// constrained to [A-Za-z0-9-_.]; '/' becomes '_' and the true path is preserved
// in items[].path for tree reconstruction (SPEC §14.2).
var invalidKeyChar = regexp.MustCompile(`[^A-Za-z0-9._-]`)

// ConfigMap is a minimal typed ConfigMap for YAML emission.
type ConfigMap struct {
	APIVersion string            `json:"apiVersion"`
	Kind       string            `json:"kind"`
	Metadata   Metadata          `json:"metadata"`
	Data       map[string]string `json:"data,omitempty"`
	BinaryData map[string][]byte `json:"binaryData,omitempty"`
}

// Metadata is the ConfigMap's object metadata.
type Metadata struct {
	Name      string `json:"name"`
	Namespace string `json:"namespace,omitempty"`
}

// Rendered is the result of rendering a skill to ConfigMap(s): the objects, the
// key→path item mappings per object, and a ready-to-use volume/mount snippet.
type Rendered struct {
	ConfigMaps   []ConfigMap
	Items        map[string][]Item // configmap name → items
	MountPath    string
	YAML         string
	MountSnippet string
}

// Item maps a flat ConfigMap key to its true relative path (items[].path).
type Item struct {
	Key  string `json:"key"`
	Path string `json:"path"`
}

// Render projects files into ConfigMap(s) named from the install handle, mounted
// at mountPath (default /skills/<name>). It auto-splits per subtree when the
// single-object size would exceed the 1 MiB ceiling (SPEC §14.2).
func Render(name, namespace, mountPath string, files map[string][]byte) (*Rendered, error) {
	if mountPath == "" {
		mountPath = "/skills/" + name
	}
	single := buildConfigMap(name, namespace, files)
	r := &Rendered{MountPath: mountPath, Items: map[string][]Item{}}

	if objectSize(single) <= SizeCeiling {
		items := itemsFor(files)
		r.ConfigMaps = []ConfigMap{single}
		r.Items[name] = items
	} else {
		// Auto-split: one ConfigMap per top-level subtree, name suffixed by subtree.
		for _, sub := range subtrees(files) {
			cmName := name
			if sub.name != "" {
				cmName = name + "-" + sanitizeSuffix(sub.name)
			}
			cm := buildConfigMap(cmName, namespace, sub.files)
			r.ConfigMaps = append(r.ConfigMaps, cm)
			r.Items[cmName] = itemsFor(sub.files)
		}
	}

	if err := r.encode(name); err != nil {
		return nil, err
	}
	return r, nil
}

// buildConfigMap places text files in data and non-UTF-8 files in binaryData
// (SPEC §14.1), keyed by sanitized flat keys.
func buildConfigMap(name, namespace string, files map[string][]byte) ConfigMap {
	cm := ConfigMap{
		APIVersion: "v1",
		Kind:       "ConfigMap",
		Metadata:   Metadata{Name: name, Namespace: namespace},
		Data:       map[string]string{},
		BinaryData: map[string][]byte{},
	}
	for _, rel := range sortedKeys(files) {
		key := sanitizeKey(rel)
		data := files[rel]
		if utf8.Valid(data) {
			cm.Data[key] = string(data)
		} else {
			cm.BinaryData[key] = data
		}
	}
	if len(cm.Data) == 0 {
		cm.Data = nil
	}
	if len(cm.BinaryData) == 0 {
		cm.BinaryData = nil
	}
	return cm
}

func itemsFor(files map[string][]byte) []Item {
	var items []Item
	for _, rel := range sortedKeys(files) {
		items = append(items, Item{Key: sanitizeKey(rel), Path: rel})
	}
	return items
}

type subtree struct {
	name  string
	files map[string][]byte
}

// subtrees groups files by top-level directory ("" for root-level files).
func subtrees(files map[string][]byte) []subtree {
	groups := map[string]map[string][]byte{}
	for rel, data := range files {
		top := ""
		if i := strings.Index(rel, "/"); i >= 0 {
			top = rel[:i]
		}
		if groups[top] == nil {
			groups[top] = map[string][]byte{}
		}
		groups[top][rel] = data
	}
	names := make([]string, 0, len(groups))
	for n := range groups {
		names = append(names, n)
	}
	sort.Strings(names)
	out := make([]subtree, 0, len(groups))
	for _, n := range names {
		out = append(out, subtree{name: n, files: groups[n]})
	}
	return out
}

func (r *Rendered) encode(baseName string) error {
	var docs []string
	for _, cm := range r.ConfigMaps {
		b, err := yaml.Marshal(cm)
		if err != nil {
			return err
		}
		docs = append(docs, strings.TrimRight(string(b), "\n"))
	}
	r.YAML = strings.Join(docs, "\n---\n") + "\n"
	r.MountSnippet = r.mountSnippet(baseName)
	return nil
}

// mountSnippet emits a ready-to-use volumes + volumeMounts snippet using
// full-volume mounts with items[].path (auto-updating, not subPath) (SPEC §14.2).
func (r *Rendered) mountSnippet(baseName string) string {
	var vols, mounts strings.Builder
	names := make([]string, 0, len(r.Items))
	for n := range r.Items {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, cmName := range names {
		volName := "skill-" + cmName
		mount := r.MountPath
		if cmName != baseName {
			// split subtree mounts at their directory
			suffix := strings.TrimPrefix(cmName, baseName+"-")
			mount = r.MountPath + "/" + suffix
		}
		fmt.Fprintf(&mounts, "  - name: %s\n    mountPath: %s\n", volName, mount)
		fmt.Fprintf(&vols, "  - name: %s\n    configMap:\n      name: %s\n      items:\n", volName, cmName)
		for _, it := range r.Items[cmName] {
			path := it.Path
			if cmName != baseName {
				suffix := strings.TrimPrefix(cmName, baseName+"-")
				path = strings.TrimPrefix(it.Path, suffix+"/")
			}
			fmt.Fprintf(&vols, "        - key: %s\n          path: %s\n", it.Key, path)
		}
	}
	return "volumeMounts:\n" + mounts.String() + "volumes:\n" + vols.String()
}

// objectSize estimates the serialized ConfigMap size for the split decision,
// counting base64 inflation of binaryData (~33%) (SPEC §14.2).
func objectSize(cm ConfigMap) int {
	total := len(cm.Metadata.Name) + len(cm.Metadata.Namespace) + 64
	for k, v := range cm.Data {
		total += len(k) + len(v)
	}
	for k, v := range cm.BinaryData {
		total += len(k) + (len(v)*4+2)/3
	}
	return total
}

func sanitizeKey(rel string) string {
	return invalidKeyChar.ReplaceAllString(rel, "_")
}

func sanitizeSuffix(name string) string {
	return invalidKeyChar.ReplaceAllString(name, "-")
}

func sortedKeys(files map[string][]byte) []string {
	out := make([]string, 0, len(files))
	for k := range files {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
