// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package closure

import (
	"fmt"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// The JSON Pointer fragment body that marks an entry reference on each axis —
// `/schemas/`, `/mappings/`, `/workloads/`, `/artifacts/` — is declared once, on
// the axis itself, and reached through [LibraryAxis.IsRef] and
// [LibraryAxis.ParseRef]. The primitives below carry the grammar every axis
// shares; the axis supplies the one thing that differs.

// isIdentityRefWithFragment reports whether value is a host-less cross-document
// library reference in the identity form `<Library>[:<version>]#<fragmentPrefix>…`
// (no registry host). In-document anchors (`#/$defs/...`), host-bearing `oci://…`
// references (rejected at the consumption site), and any other shape return
// false, so a discovery walker records only genuine cross-library references on
// the requested fragment surface.
func isIdentityRefWithFragment(value, fragmentPrefix string) bool {
	_, _, ok := types.IdentityRefParts(value, fragmentPrefix)
	return ok
}

// parseIdentityRefWithFragment parses a host-less identity library reference
// `<Library>[:<version>]#<fragmentPrefix>…` into its library name, optional
// version assertion, and fragment body (the part after `#`, e.g.
// "/schemas/PortSpec" or "/mappings/SimpleValues"). The optional `:<version>` is
// a minimum-version floor for selection, never a pin (the lockfile owns the
// selected version); the library name carries no registry host. Inputs that are
// not the identity shape on fragmentPrefix return a wrapped ErrInvalidLibraryRef
// naming the offending string.
//
// A reference carrying whitespace is refused here under
// [types.ErrLibraryReferenceWhitespace] rather than being trimmed into something
// resolvable. The document read refuses an AUTHORED reference under the same
// condition, so this arm is what covers a reference this package lifts out of a
// library entry's own body during transitive descent — text no document read
// ever saw. Refusing rather than trimming is what keeps the version floor honest:
// a trailing space inside the floor is carried into the lockfile as part of the
// selected version, naming a version no artifact publishes.
func parseIdentityRefWithFragment(s, fragmentPrefix string) (libraryName, version, fragment string, err error) {
	if !isIdentityRefWithFragment(s, fragmentPrefix) {
		return "", "", "", fmt.Errorf("%w: %q", ErrInvalidLibraryRef(), s)
	}
	if types.IdentityRefCarriesWhitespace(s) {
		return "", "", "", fmt.Errorf("%w: %q", types.ErrLibraryReferenceWhitespace(), s)
	}
	hash := strings.IndexByte(s, '#')
	doc := s[:hash]
	fragment = s[hash+1:]
	libraryName = doc
	if at := strings.IndexByte(doc, ':'); at >= 0 {
		libraryName = doc[:at]
		version = doc[at+1:]
	}
	if libraryName == "" {
		return "", "", "", fmt.Errorf("%w: %q", ErrInvalidLibraryRef(), s)
	}
	if strings.IndexByte(libraryName, '@') >= 0 {
		return "", "", "", fmt.Errorf("%w: version separator is ':', not '@' — use %q", ErrInvalidLibraryRef(), strings.Replace(s, "@", ":", 1))
	}
	return libraryName, version, fragment, nil
}

// formatFragmentRef rebuilds the wire-form fragment ("#/schemas/X",
// "#/mappings/X", or "#/workloads/X") from the bare body returned by the identity-ref parsers.
// Empty input produces an empty output (a top-level library reference with
// no fragment).
func formatFragmentRef(fragment string) string {
	if fragment == "" {
		return ""
	}
	return "#" + fragment
}
