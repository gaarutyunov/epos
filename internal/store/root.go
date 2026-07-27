package store

import (
	"fmt"
	"os"
	"path/filepath"
)

// RootEnv names the environment variable that moves the epos root, for a user
// who keeps epos state somewhere other than their home directory.
const RootEnv = "EPOS_HOME"

// rootDirName is the epos root inside the user's home directory.
const rootDirName = ".epos"

// storeDirName is the OCI layout inside the epos root (SPEC.md 9.1).
const storeDirName = "store"

// Root resolves the epos root directory: the one directory every piece of epos
// state lives under, of which the store is the first.
//
// Precedence, highest first:
//
//  1. explicit — a directory the caller names, so a test can root epos at a
//     temp directory it owns without touching the environment at all;
//  2. $EPOS_HOME;
//  3. <user home>/.epos, the default.
//
// This is the only place epos consults the user's home directory. Redirecting
// the store used to mean moving HOME, which changes what every other part of
// the process reads, and on Windows does not even work: os.UserHomeDir reads
// USERPROFILE there.
//
// The root must be on a local filesystem — advisory locks are unreliable over
// NFS and SMB (9.4).
func Root(explicit string) (string, error) {
	if explicit != "" {
		return filepath.Clean(explicit), nil
	}
	if fromEnv := os.Getenv(RootEnv); fromEnv != "" {
		return filepath.Clean(fromEnv), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("locate home directory: %w", err)
	}
	return filepath.Join(home, rootDirName), nil
}

// Under returns the store inside an already-resolved epos root.
//
// Nothing is read from the environment and no home directory is looked up, so
// a test can hand it t.TempDir() and get a store no other process — and no
// other test — can reach.
func Under(root string) *Store {
	return &Store{root: filepath.Join(root, storeDirName)}
}

// Default is the store under the epos root Root resolves (SPEC.md 9.1).
func Default() (*Store, error) {
	root, err := Root("")
	if err != nil {
		return nil, err
	}
	return Under(root), nil
}
