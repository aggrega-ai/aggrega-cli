// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package closure

import (
	"fmt"
	"regexp"
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
