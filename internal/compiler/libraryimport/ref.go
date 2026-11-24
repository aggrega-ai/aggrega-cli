// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package libraryimport

import (
	"strings"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// SplitRef parses a host-less `<Library>[:<version>]#<fragmentPrefix><Entry>`
// into its library name and entry, stripping any `:version` floor. A host-bearing
// reference (`oci://…`), a fragment shape other than the requested one, or an empty
// entry or library returns ok=false.
//
// The host-less identity grammar itself is shared from [types.IdentityRefParts]; the
// `:version` strip and the non-empty-entry requirement are layered here. Every
// import axis consumes the same grammar and differs only in the fragment prefix it
// admits, so the split is written once and the axis supplies the prefix.
//
// The fragment prefix (`/mappings/`, `/artifacts/` or `/workloads/`) is stated by
// each caller; nothing here knows how a reference was pinned.
func SplitRef(ref, fragmentPrefix string) (library, entry string, ok bool) {
	doc, ent, matched := types.IdentityRefParts(ref, fragmentPrefix)
	if !matched || ent == "" {
		return "", "", false
	}
	library = doc
	if at := strings.IndexByte(doc, ':'); at >= 0 {
		library = doc[:at]
	}
	if library == "" {
		return "", "", false
	}
	return library, ent, true
}

// FieldRootTarget reports whether body's first non-whitespace token is a
// field-root `$import: <ref>` directive (whole-content replacement) and returns
// <ref>. A plain template body (no field-root `$import:`) returns ok=false. The
// field-root-only rule matches the `$import:` authoring vocabulary: an `$import:`
// embedded deeper in a template body is not a whole-content replacement.
func FieldRootTarget(body string) (ref string, ok bool) {
	trimmed := strings.TrimSpace(body)
	if !strings.HasPrefix(trimmed, "$import:") {
		return "", false
	}
	rest := strings.TrimPrefix(trimmed, "$import:")
	if nl := strings.IndexByte(rest, '\n'); nl >= 0 {
		rest = rest[:nl]
	}
	rest = strings.TrimSpace(rest)
	if rest == "" {
		return "", false
	}
	return rest, true
}
