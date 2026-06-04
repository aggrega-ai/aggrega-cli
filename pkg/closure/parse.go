// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package closure

import (
	"fmt"
	"regexp"
	"strings"
)

// libraryRefPattern matches the canonical library-reference URI
// shape per the file-import vocabulary spec:
//
//	oci://<host>/<library>@<version>#/<entry-path>
//
// The @<version> segment uses `@` as the version separator (NOT
// `:` like catalog entities) so that JSON Pointer fragments
// (`#/schemas/PortSpec`) remain unambiguous. Both @version and
// #fragment are optional — a bare `oci://<host>/<library>` matches
// with empty version and fragment.
var libraryRefPattern = regexp.MustCompile(`^(oci://[^@#]+)(?:@([^#]+))?(?:#(.*))?$`)

// parseLibraryRef parses a library-reference URI into its three
// components: the library identifier (everything before @ or #),
// the version pin (between @ and #), and the JSON Pointer fragment
// (after #). The version pin and fragment are both optional.
//
// Example inputs:
//
//	"oci://hub.aggrega.ai/schema-library/core/base-types@1.0.0#/schemas/PortSpec"
//	  → libraryURI = "oci://hub.aggrega.ai/schema-library/core/base-types"
//	    version    = "1.0.0"
//	    fragment   = "/schemas/PortSpec"
//
//	"oci://hub.aggrega.ai/mapping-library/core/base-mappings@1.0.0"
//	  → libraryURI = "oci://hub.aggrega.ai/mapping-library/core/base-mappings"
//	    version    = "1.0.0"
//	    fragment   = ""
//
//	"oci://hub.aggrega.ai/schema-library/core/base-types#/schemas/PortSpec"
//	  → libraryURI = "oci://hub.aggrega.ai/schema-library/core/base-types"
//	    version    = ""  (unpinned; defers to minimum version selection at closure level)
//	    fragment   = "/schemas/PortSpec"
//
// Inputs that don't match the canonical shape return a wrapped
// ErrInvalidLibraryRef naming the offending input.
//
// The fragment return value does NOT include the leading `#`; the
// fragment "/schemas/PortSpec" is the JSON Pointer body. Callers
// reconstruct the wire-form fragment by prepending `#` when
// emitting to the lockfile (the lockfile's consumers list keeps
// the `#/...` wire form).
func parseLibraryRef(s string) (libraryURI, version, fragment string, err error) {
	match := libraryRefPattern.FindStringSubmatch(s)
	if match == nil {
		return "", "", "", fmt.Errorf("%w: %q", ErrInvalidLibraryRef, s)
	}
	return match[1], match[2], match[3], nil
}

// ociScheme marks the host-bearing reference form. A library is addressed by
// identity, never by registry host; a `$ref:` carrying this scheme
// is rejected at the cross-document expander and is not a library-closure
// reference here, so the discovery walker does not record it.
const ociScheme = "oci://"

// schemasFragmentPrefix is the JSON Pointer fragment body that marks a
// SchemaLibrary entry reference (`<Library>#/schemas/<Entry>`). The MappingLibrary
// `$import:` surface (`/mappings/`) is discovered through parseLibraryRef until
// its identity flip lands; parseIdentityRef is the SchemaLibrary `$ref:` parser.
const schemasFragmentPrefix = "/schemas/"

// isIdentityLibraryRef reports whether a `$ref:` value is a host-less
// cross-document SchemaLibrary reference in the identity form
// `<Library>[@<version>]#/schemas/<Entry>` (no registry host). In-document anchors
// (`#/$defs/...`), host-bearing `oci://…` references (rejected at the
// cross-document expander), and any other shape return false, so the discovery
// walker records only genuine cross-library references.
func isIdentityLibraryRef(value string) bool {
	if value == "" || value[0] == '#' || strings.HasPrefix(value, ociScheme) {
		return false
	}
	hash := strings.IndexByte(value, '#')
	if hash <= 0 {
		return false
	}
	return strings.HasPrefix(value[hash+1:], schemasFragmentPrefix)
}

// parseIdentityRef parses a host-less identity SchemaLibrary reference
// `<Library>[@<version>]#/schemas/<Entry>` into its library name, optional
// version assertion, and fragment body (the part after `#`, e.g.
// "/schemas/PortSpec"). The optional `@<version>` is a minimum-version floor for
// selection, never a pin (the lockfile owns the selected version); the
// library name carries no registry host. Inputs that are not the identity shape
// return a wrapped ErrInvalidLibraryRef naming the offending string.
func parseIdentityRef(s string) (libraryName, version, fragment string, err error) {
	if !isIdentityLibraryRef(s) {
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

// formatFragmentRef rebuilds the wire-form fragment ("#/schemas/X")
// from the bare body returned by parseLibraryRef. Empty input
// produces an empty output (a top-level library reference with no
// fragment).
func formatFragmentRef(fragment string) string {
	if fragment == "" {
		return ""
	}
	return "#" + fragment
}
