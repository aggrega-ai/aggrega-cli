// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package helmchartcore

import "github.com/aggrega-ai/aggrega-cli/pkg/namesegment"

const (
	helmChartAPIVersion = "v2"
	defaultChartVersion = "0.1.0"
	// ChartMetadataFileName is the file a chart declares its own name and
	// version in, at the chart root. It is named once because several paths reach
	// it — validating an acquired listing, rewriting the placed manifest's name,
	// reading the dependency's version, and picking the placed manifest out of a
	// contributor's rendered file set — and a literal repeated across them is a
	// literal that can come to disagree.
	//
	// It is exported because the last of those paths is in the provider guest,
	// which lives in a separate package: a guest spelling the name for itself
	// would hold a second copy of the one word that decides which rendered file
	// is the manifest.
	ChartMetadataFileName = "Chart.yaml"
)

// chartMetadataFileName is this package's own spelling of
// [ChartMetadataFileName], kept so the several internal readers are not each
// rewritten to the exported name for no gain.
const chartMetadataFileName = ChartMetadataFileName

// helmNameEncoding is the segment encoding the umbrella chart name and the
// per-sub-chart dependency alias are built with.
//
// Both are derived from an identity an author wrote — an aggregate name, a
// dispatch path, a component instance id — and both have to arrive in an
// alphabet poorer than the one that identity was written in. Encoding the
// characters the alphabet does not admit, rather than substituting for them, is
// what keeps the derivation injective: two identities differing only in
// punctuation reach two names. That matters most for the alias, because an alias
// is the key a sub-chart's values are scoped under, so two instances sharing one
// alias would leave one instance's values reaching the other.
//
// The alphabet is stated here rather than taken from the encoding, because it is
// a property of what Helm accepts in these positions and not of the encoding.
// The alias is the stricter of the two positions, so one alphabet keeps a
// derived name valid in either.
var helmNameEncoding = namesegment.NewEncoding('-', isHelmNameRune)

// isHelmNameRune reports whether a character is carried through the encoding as
// itself: an ASCII letter, a digit, or the underscore. Those, together with the
// hyphen the encoding reserves for its escapes and its join, are the whole of
// what a Helm dependency alias admits.
func isHelmNameRune(r rune) bool {
	return r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_'
}
