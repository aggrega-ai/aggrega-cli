// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package digest is the single source of the content-address spelling every
// push, pull, lockfile, and source-cache integrity check compares against.
// Keeping the one computation here — a stdlib-only leaf with no dependencies
// beyond crypto/sha256 and encoding/hex — lets every consumer share the exact
// format without re-deriving it and without importing a heavier package's
// dependency tree.
package digest

import (
	"crypto/sha256"
	"encoding/hex"
)

// ContentDigest returns the canonical content-address of b: the lowercase
// hex-encoded SHA-256 of the bytes, prefixed "sha256:". The "sha256:" prefix
// and the lowercase hex encoding are load-bearing — the returned string is
// compared verbatim across the content-addressing boundary (lockfile entries,
// pinned-digest checks, source-cache integrity verification), so any change to
// the spelling breaks that equality.
func ContentDigest(b []byte) string {
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:])
}
