// Package materialize writes a composed/rendered skill bundle to the local
// project directory (the --target=files materialization, SPEC §4.2, §5).
package materialize

import (
	"os"
	"path/filepath"
	"sort"
)

// WriteTree writes a path→bytes file set under dir, creating parent directories.
func WriteTree(dir string, files map[string][]byte) error {
	for rel, data := range files {
		full := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(full, data, 0o644); err != nil {
			return err
		}
	}
	return nil
}

// ReadTree reads every regular file under dir into a path→bytes map, skipping a
// given set of top-level names (e.g. the lockfile itself).
func ReadTree(dir string, skip map[string]bool) (map[string][]byte, error) {
	out := map[string][]byte{}
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if skip[rel] {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		out[rel] = data
		return nil
	})
	return out, err
}

// SortedPaths returns the sorted keys of a file set (deterministic iteration).
func SortedPaths(files map[string][]byte) []string {
	paths := make([]string, 0, len(files))
	for p := range files {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	return paths
}

// RemoveTree deletes the materialized files of a bundle from dir (uninstall).
func RemoveTree(dir string, files map[string][]byte) error {
	for rel := range files {
		full := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.Remove(full); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}
