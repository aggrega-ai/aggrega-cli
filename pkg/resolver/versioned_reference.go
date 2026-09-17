// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver

import "strings"

// SplitVersionedURI splits a URI like "core/api-server:1.0.0" into its base
// (before the last colon) and version (after the last colon) segments. The
// last colon is the separator because version segments do not contain colons
// while base segments may carry registry-style separators ("hub.aggrega.ai/component/core/api-server:1.0.0").
//
// It is the single home for this split, so every caller applies the same
// separator rule. The composing counterpart — the one that builds a bare
// "name:version" value back up — sits beside the Manifest mapping form it
// serves, in the package that holds the catalog.
//
// It is exported because the rules that enforce this split live here while the
// resolution ladder that consumes it lives in the package that holds the
// catalog, which depends on this one: the grammar travels with the rule that
// enforces it, and every reader takes it from here rather than carrying a
// second separator rule that agrees today.
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
// Returns the bare URI as base, "" as version, and false as hasVersion when
// the URI carries no colon. hasVersion is what separates that case from a
// trailing colon with an empty version: "core/x" yields ("core/x", "", false)
// while "core/x:" yields ("core/x", "", true). The two agree on base and on
// version, so only hasVersion tells them apart.
func SplitVersionedURI(uri string) (base, version string, hasVersion bool) {
	idx := strings.LastIndex(uri, ":")
	if idx < 0 {
		return uri, "", false
	}
	return uri[:idx], uri[idx+1:], true
}
