// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package wasm

import "strings"

// uncommittedChangesMarker is the suffix a build-provenance version carries when
// the working tree the build was made from held uncommitted changes.
//
// It is not a choice made here. The version is stamped by the shared recipe
// (scripts/lib/build-version.sh), which asks `git describe --dirty` for it, and
// this is the mark git appends by default. So this is the one statement in Go of
// a form the recipe produces, and a test drives that recipe over a scratch
// repository in every state it distinguishes and holds this predicate to what it
// stamps. A change to the recipe that moved the mark therefore fails that test
// rather than leaving this statement quietly describing a form nothing produces.
const uncommittedChangesMarker = "-dirty"

// BuiltFromUncommittedChanges reports whether a build-provenance version names a
// build made from a working tree with uncommitted changes — whether it ends with
// the mark the version recipe appends in that case.
//
// Such a version is a valid semantic version: the mark reads as part of a
// prerelease. What it says is different from every other form the recipe
// stamps. A clean development version (1.0.0-3-g1a2b3c4) and a release version
// (1.0.0) each name a commit that holds the bytes that were built, so building
// that commit reproduces them. A version carrying the mark names a commit that
// does not hold them, so nothing reproduces what was built from the version it
// carries.
func BuiltFromUncommittedChanges(version string) bool {
	return strings.HasSuffix(version, uncommittedChangesMarker)
}
