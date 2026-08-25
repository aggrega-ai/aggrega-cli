// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package helmcore

import (
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/rendercore"
	"github.com/aggrega-ai/aggrega-cli/pkg/namesegment"
)

// releaseNameEncoding is the segment encoding a Helm release name is built with.
//
// A release name is an in-cluster identity. It reaches every template as
// .Release.Name and charts routinely name resources after it, so two component
// identities arriving at one release name give two components one set of
// resource names in the cluster. The derivation therefore escapes the
// characters the alphabet does not admit rather than substituting for them:
// distinct identities reach distinct names, and no substitution can make two of
// them meet.
//
// The alphabet is stated here rather than taken from the encoding, because it
// is a property of what Helm accepts in this position rather than of the
// encoding. Helm accepts a release name built from lowercase letters, digits,
// hyphens and dots that begins and ends with a lowercase letter or a digit, up
// to 53 characters. The hyphen is the encoding's own — it opens every escape
// and composes the join — so an alphabet may not admit it. The dot is left out
// even though Helm accepts one: Helm requires a lowercase letter or a digit on
// each side of a dot, so a dot carried through next to a hyphen the encoding
// emitted would leave the grammar the name has to satisfy.
//
// The two conditions on position and length are properties of the identity
// rather than of the encoding, and nothing here enforces either: an identity
// whose first character is escaped begins its name with the hyphen every escape
// opens with, and an identity carrying escaped characters encodes longer than
// it was authored.
var releaseNameEncoding = namesegment.NewEncoding(isReleaseNameRune)

// isReleaseNameRune reports whether a character is carried through the encoding
// as itself: a lowercase ASCII letter or a digit. Every other character,
// uppercase letters included, is escaped.
func isReleaseNameRune(r rune) bool {
	return r >= 'a' && r <= 'z' || r >= '0' && r <= '9'
}

// releaseName derives a node's Helm release name from the same component
// identity its rendered output files are prefixed with,
// [rendercore.ProvenanceComponentID].
//
// One source feeds both, so the two are the same identity written twice — once
// as authored in the file prefix, once through an injective encoding here — and
// two nodes reach one release name exactly when they reach one prefix. Deriving
// it from a second value would put a lossy name beside a lossless one: the
// lossless one would keep two nodes' files apart while the lossy one quietly
// gave both a single in-cluster identity, and nothing about the files would
// show it, because a release name lives inside the rendered bytes rather than
// in the name of the file carrying them.
func releaseName(nodeCtx rendercore.NodeRenderContext) string {
	return releaseNameEncoding.Segment(rendercore.ProvenanceComponentID(nodeCtx))
}
