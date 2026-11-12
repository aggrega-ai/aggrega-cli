// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package resolver

import "github.com/aggrega-ai/aggrega-cli/pkg/types"

// SplitVersionedURI splits a URI like "core/api-server:1.0.0" into its base
// (before the last colon) and version (after the last colon) segments.
//
// It forwards to types.SplitVersionedURI, which holds the separator rule, the
// hasVersion semantics, and the reason this rule is deliberately not the one an
// OCI reference follows. The rule sits there because its readers sit in
// packages that cannot import one another, and this package is one of them.
//
// It stays exported here because the rules that enforce this split live here
// while the resolution ladder that consumes it lives in the package that holds
// the catalog, which depends on this one: a reader of those rules takes the
// grammar from the package holding them. The composing counterpart — the one
// that builds a bare "name:version" value back up — sits beside the Manifest
// mapping form it serves, in the package that holds the catalog.
func SplitVersionedURI(uri string) (base, version string, hasVersion bool) {
	return types.SplitVersionedURI(uri)
}
