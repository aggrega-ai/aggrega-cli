// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package types

import (
	"strings"
	"unicode"

	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
)

// ErrLibraryReferenceWhitespace fires when a cross-document library reference
// carries whitespace — a space, a tab or a line break — anywhere in it. It
// covers every surface that addresses a library entry by identity: a
// `valuesMapping:`, `workload:` or `artifacts.<name>:` `$import:` target, and a
// schema `$ref:`.
//
// No part of the reference admits whitespace. The library name and the entry
// name are both constrained by the library schemas to identifier spellings, and
// the optional `:<version>` floor is a version string, so whitespace is never
// part of a reference anyone can resolve. What it does instead is make two
// readings of one reference disagree: the text before the whitespace is a
// resolvable reference on its own, so a reader that stops there resolves a
// different thing from a reader that does not.
func ErrLibraryReferenceWhitespace() error { return errLibraryReferenceWhitespace }

var errLibraryReferenceWhitespace = cerrors.Register("library-reference-whitespace", cerrors.Meta{
	ShortCode: "E0295",
	Message:   "library reference: a reference to a library entry carries no whitespace or line break",
	RulePlace: cerrors.RuleNoStageReaches("Two moments evaluate the rule. A catalog entity's read evaluates it as each authored " +
		"$import: target is decoded, where the line the author wrote is still known; and the closure walk " +
		"evaluates it over a $ref: or $import: value met while pinning what a Release reaches, which is " +
		"the arm that reaches a reference lifted out of a library entry's own body during transitive " +
		"descent. What decides it is whitespace anywhere in the reference text, which no part of a " +
		"reference admits. The read decodes a document before anything compiles it, and the walk reads a " +
		"reference to decide what to fetch rather than to compile it, so no declared stage of a " +
		"compilation holds the value either moment judges."),
})

// IdentityRefParts reports whether value is a host-less identity library
// reference on fragmentPrefix — the grammar
// `<Library>[:<version>]#<fragmentPrefix><entry>`: not empty, not a leading-`#`
// in-document anchor, not a host-bearing `oci://` reference, a `#` at index > 0,
// and a fragment beginning with fragmentPrefix. On a match it returns doc (the
// `<Library>[:<version>]` text before `#`) and entry (the fragment text after
// fragmentPrefix); ok is false otherwise.
//
// This is the single encoding of the identity-reference grammar shared by the
// SchemaLibrary `$ref` (`/schemas/`) and MappingLibrary `$import` (`/mappings/`)
// surfaces. It is pure grammar: it does NOT strip the `:version` floor, reject
// the stale `@` separator, require a non-empty entry, or reject whitespace —
// each consuming site layers those concerns on the returned parts.
func IdentityRefParts(value, fragmentPrefix string) (doc, entry string, ok bool) {
	if value == "" || value[0] == '#' || strings.HasPrefix(value, "oci://") {
		return "", "", false
	}
	hash := strings.IndexByte(value, '#')
	if hash <= 0 {
		return "", "", false
	}
	frag := value[hash+1:]
	if !strings.HasPrefix(frag, fragmentPrefix) {
		return "", "", false
	}
	return value[:hash], frag[len(fragmentPrefix):], true
}

// IdentityRefCarriesWhitespace reports whether value addresses a library entry
// and carries whitespace anywhere in it — the malformation
// [ErrLibraryReferenceWhitespace] names.
//
// It is the single implementation of that rule, and it is deliberately
// independent of the fragment surface: whitespace makes a reference unreadable
// on every axis alike, and a per-axis copy of the test would be four chances to
// disagree about one malformation.
//
// "Addresses a library entry" is read the way [IdentityRefParts] reads it, minus
// the fragment prefix: a non-empty, host-less value carrying a `#` past its
// first character. A local-file `$import:` target carries no `#` and is
// untouched, so a path with a space in it stays a path. A host-bearing
// `oci://…` reference is refused at its own consumption site, under the
// condition that names the host, and is left to it here.
func IdentityRefCarriesWhitespace(value string) bool {
	if value == "" || value[0] == '#' || strings.HasPrefix(value, "oci://") {
		return false
	}
	if strings.IndexByte(value, '#') <= 0 {
		return false
	}
	return strings.IndexFunc(value, unicode.IsSpace) >= 0
}
