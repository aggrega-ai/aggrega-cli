// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package types

import "strings"

// SplitVersionedURI splits an authored reference such as
// "core/api-server:1.0.0" into its base (before the last colon) and version
// (after the last colon) segments. The last colon is the separator because a
// version segment does not contain colons while a base segment may carry
// registry-style separators ("hub.aggrega.ai/component/core/api-server:1.0.0").
//
// It is the single home for this split, so every reader of a versioned
// reference applies the same separator rule. The readers sit in packages that
// cannot import one another — the catalog's resolution ladder, the renderer's
// provider dispatch, and the load-time shape rules — so the rule lives here,
// in the package all three already depend on, rather than being restated
// beside each of them. A second copy would agree on the day it was written and
// would disagree silently on the first reference neither author had in mind.
//
// The separator rule is deliberately not the one an OCI reference follows. An
// OCI tag is the last colon after the last slash, which is what stops a
// registry host's port being read as a tag; this split takes the last colon
// anywhere, because its base may carry the registry-style separators above. On
// "localhost:5000/core/api-server" this rule yields base "localhost" and
// version "5000/core/api-server", while the OCI rule confines its search past
// the last slash and reports no version. Both are right for their own input
// shapes and neither is right for the other's. The two take the same argument
// and return the same shape, so substituting one for the other still compiles
// and changes the answer on exactly the registry-qualified references a real
// workspace is written in. They stay separate rules for separate inputs.
//
// Returns the bare reference as base, "" as version, and false as hasVersion
// when the reference carries no colon. hasVersion is what separates that case
// from a trailing colon with an empty version: "core/x" yields ("core/x", "",
// false) while "core/x:" yields ("core/x", "", true). The two agree on base and
// on version, so only hasVersion tells them apart.
func SplitVersionedURI(uri string) (base, version string, hasVersion bool) {
	idx := strings.LastIndex(uri, ":")
	if idx < 0 {
		return uri, "", false
	}
	return uri[:idx], uri[idx+1:], true
}

// BareCapability returns the capability a `capability:` binding names, with any
// version pin removed.
//
// A binding may be written unversioned ("helm"), with an exact pin
// ("helm:1.0.0"), or with a major-compatible range ("helm:^1.0.0"). The bare
// name selects the provider kind; the pin selects which version of the plugin
// serving that capability renders the document, and it is not part of the
// capability's name. So every reading that asks *which capability is this* —
// the render dispatch, the output grouping, and the load-time rules that carve
// one capability out of a shape requirement — asks it of this projection rather
// than of the authored string, and a pinned binding answers exactly as the
// unpinned one does.
//
// A longer capability whose name merely begins with a shorter one is a
// different capability and is returned unchanged: the projection removes a pin
// and never a prefix.
func BareCapability(binding string) string {
	base, _, _ := SplitVersionedURI(binding)
	return base
}
