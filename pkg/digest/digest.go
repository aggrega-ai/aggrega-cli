// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

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
	"strings"
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

// BareHex reduces a content digest to its prefix-free comparison key: the bare
// hex with any algorithm prefix stripped, lowercased. It accepts all three
// spellings a digest travels in — the colon form "sha256:<hex>" (the
// lockfile/equality spelling), the dash form "sha256-<hex>" (the spelling a
// registry tag carries, since a tag may not contain ":"), and bare "<hex>" (the
// membership-key spelling) — and returns the same key for all three. The forms
// are mutually unambiguous: bare hex contains none of the prefix characters, so
// it can begin with neither prefix.
//
// The return value is guaranteed lowercased bare hex. Callers may rely on this
// lowercasing as a stated contract: a digest keyed or compared through BareHex
// joins any other BareHex-keyed value regardless of the case or prefix spelling
// it arrived in.
func BareHex(d string) string {
	d = strings.TrimPrefix(d, "sha256:")
	d = strings.TrimPrefix(d, "sha256-")
	return strings.ToLower(d)
}
