package domain

import (
	"time"

	"github.com/opencontainers/go-digest"
)

// fixedModTime makes tarball builds reproducible: the same directory content
// always yields the same archive bytes and therefore the same content digest.
var fixedModTime = time.Unix(0, 0).UTC()

// parseDigest converts a "sha256:hex" string to an OCI digest.
func parseDigest(s string) digest.Digest { return digest.Digest(s) }
