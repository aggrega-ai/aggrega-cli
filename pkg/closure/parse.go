// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package closure

import (
	"fmt"
	"strings"
)

// ociScheme marks the host-bearing reference form. A library is addressed by
// identity, never by registry host; a `$ref:` or `$import:` carrying this scheme
// is rejected at its consumption site and is not a library-closure
// reference here, so the discovery walker does not record it.
const ociScheme = "oci://"

// schemasFragmentPrefix is the JSON Pointer fragment body that marks a
// SchemaLibrary entry reference (`<Library>#/schemas/<Entry>`), the surface
// parseIdentityRef parses. mappingsFragmentPrefix is the parallel marker for a
// MappingLibrary `$import:` entry reference (`<Library>#/mappings/<Entry>`),
// parsed by parseIdentityImportRef. Both share the host-less identity grammar
// and differ only in the fragment surface they admit.
const (
	schemasFragmentPrefix  = "/schemas/"
	mappingsFragmentPrefix = "/mappings/"
)

// isIdentityRefWithFragment reports whether value is a host-less cross-document
// library reference in the identity form `<Library>[@<version>]#<fragmentPrefix>…`
// (no registry host). In-document anchors (`#/$defs/...`), host-bearing `oci://…`
// references (rejected at the consumption site), and any other shape return
// false, so a discovery walker records only genuine cross-library references on
// the requested fragment surface.
func isIdentityRefWithFragment(value, fragmentPrefix string) bool {
	if value == "" || value[0] == '#' || strings.HasPrefix(value, ociScheme) {
		return false
	}
	hash := strings.IndexByte(value, '#')
	if hash <= 0 {
		return false
	}
	return strings.HasPrefix(value[hash+1:], fragmentPrefix)
}

// isIdentityLibraryRef reports whether a `$ref:` value is a host-less
// cross-document SchemaLibrary reference in the identity form
// `<Library>[@<version>]#/schemas/<Entry>`.
func isIdentityLibraryRef(value string) bool {
	return isIdentityRefWithFragment(value, schemasFragmentPrefix)
}

// isIdentityImportRef reports whether an `$import:` value is a host-less
// cross-document MappingLibrary reference in the identity form
// `<Library>[@<version>]#/mappings/<Entry>`. A local file path (mode 2, no
// fragment) and the host-bearing `oci://…` form both return false.
func isIdentityImportRef(value string) bool {
	return isIdentityRefWithFragment(value, mappingsFragmentPrefix)
}

// parseIdentityRefWithFragment parses a host-less identity library reference
// `<Library>[@<version>]#<fragmentPrefix>…` into its library name, optional
// version assertion, and fragment body (the part after `#`, e.g.
// "/schemas/PortSpec" or "/mappings/SimpleValues"). The optional `@<version>` is
// a minimum-version floor for selection, never a pin (the lockfile owns the
// selected version); the library name carries no registry host. Inputs that are
// not the identity shape on fragmentPrefix return a wrapped ErrInvalidLibraryRef
// naming the offending string.
func parseIdentityRefWithFragment(s, fragmentPrefix string) (libraryName, version, fragment string, err error) {
	if !isIdentityRefWithFragment(s, fragmentPrefix) {
		return "", "", "", fmt.Errorf("%w: %q", ErrInvalidLibraryRef, s)
	}
	hash := strings.IndexByte(s, '#')
	doc := s[:hash]
	fragment = s[hash+1:]
	libraryName = doc
	if at := strings.IndexByte(doc, '@'); at >= 0 {
		libraryName = doc[:at]
		version = doc[at+1:]
	}
	if libraryName == "" {
		return "", "", "", fmt.Errorf("%w: %q", ErrInvalidLibraryRef, s)
	}
	return libraryName, version, fragment, nil
}

// parseIdentityRef parses a host-less identity SchemaLibrary `$ref:` reference
// `<Library>[@<version>]#/schemas/<Entry>`. See parseIdentityRefWithFragment.
func parseIdentityRef(s string) (libraryName, version, fragment string, err error) {
	return parseIdentityRefWithFragment(s, schemasFragmentPrefix)
}

// parseIdentityImportRef parses a host-less identity MappingLibrary `$import:`
// reference `<Library>[@<version>]#/mappings/<Entry>`. See
// parseIdentityRefWithFragment.
func parseIdentityImportRef(s string) (libraryName, version, fragment string, err error) {
	return parseIdentityRefWithFragment(s, mappingsFragmentPrefix)
}

// formatFragmentRef rebuilds the wire-form fragment ("#/schemas/X" or
// "#/mappings/X") from the bare body returned by the identity-ref parsers.
// Empty input produces an empty output (a top-level library reference with
// no fragment).
func formatFragmentRef(fragment string) string {
	if fragment == "" {
		return ""
	}
	return "#" + fragment
}
