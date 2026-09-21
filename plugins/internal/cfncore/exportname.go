// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package cfncore

import (
	"github.com/aggrega-ai/aggrega-cli/pkg/namesegment"
)

// exportNamePrefix is the fixed leading segment of every export and stack name
// this core assembles, so those names share one recognisable shape.
//
// It reserves nothing. The segment is a fixed literal, so an authored Outputs
// entry may carry an export name spelling it too, and a name carrying it is not
// on that account one this core assembled. It is also constant across every name
// assembled here, so it is no part of what tells two of those names apart — that
// is the Release identity segment following it. What an assembled name is
// unique against, and where that stops, is set out on [ExportName].
const exportNamePrefix = "aggrega"

// exportNameEncoding is the segment encoding the export and stack names this
// core assembles are built with. Its admitted alphabet is the ASCII letters and
// digits, which together with the hyphen the encoding reserves for its escapes
// and its join is the whole of what those names carry.
//
// The alphabet is stated here rather than taken from the encoding, because it is
// a property of what a CloudFormation export name and stack name accept and not
// of the encoding: a name derived for some other target admits its own set.
var exportNameEncoding = namesegment.NewEncoding('-', isExportNameRune)

// isExportNameRune reports whether a character is carried through the encoding as
// itself: an ASCII letter or digit.
func isExportNameRune(r rune) bool {
	return r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9'
}

// ExportName builds the deterministic CloudFormation export name for one
// cross-stack value, as the fixed prefix and four caller-supplied segments
// assembled by [namesegment.Encoding.Name]. The four segments carry, in order,
// the Release identity, the boundary path (a boundary is one
// stack), the component identity, and the provides key whose value the export
// publishes.
//
// The derivation is injective: two calls produce one name only where all four
// segments match. Each segment is encoded rather than filtered, so segments
// differing only in punctuation stay distinct, and the two-hyphen join is a
// sequence no encoded segment contains, so a name cannot be re-divided into a
// different set of segments. The name is byte-stable across runs of identical
// input.
//
// The name is a deterministic pointer string: it is assembled ahead of time from
// the build's own identity and never resolved against any AWS state.
//
// # What the name is unique against, and where that stops
//
// Because the derivation is injective, one export name within a build belongs to
// exactly one Release identity, boundary, component, and provides key. That
// is the whole of what a build can establish, and it is established here rather
// than assumed.
//
// An export name also has to be unique against every export already present in
// the account and region the stack is deployed into. That is a property of the
// deployment, not of the build, and it is beyond any derivation's reach rather
// than beyond this one's: the compiler does not authenticate, does not query,
// and cannot read the output of another build, so nothing it computes can
// observe what is already deployed. What carries the name across that boundary
// is the Release identity segment leading it — two Releases sharing an
// account and region keep their exports apart by being named apart.
func ExportName(release, boundary, component, providesKey string) string {
	return exportNameEncoding.Name(exportNamePrefix, release, boundary, component, providesKey)
}

// StackName builds the deterministic CloudFormation stack name for one boundary,
// as the fixed prefix and two caller-supplied segments assembled by
// [namesegment.Encoding.Name]. A boundary assembles into one stack, so the
// boundary path scopes the name; the Release identity leads it.
//
// It is built the same way an export name is, and is injective for the same
// reason: two calls produce one stack name only where both segments match. The
// name is byte-stable across runs of identical input.
//
// The name is a deterministic pointer string: it is assembled ahead of time from
// the build's own identity and never resolved against any AWS state — a
// cross-stack read names the producer stack by this pointer, and the runtime
// environment resolves it at deploy time. As with an export name, uniqueness
// against stacks already deployed into the account and region belongs to the
// deployment rather than to the build.
func StackName(release, boundary string) string {
	return exportNameEncoding.Name(exportNamePrefix, release, boundary)
}
