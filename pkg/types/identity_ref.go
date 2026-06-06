// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package types

import "strings"

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
// the stale `@` separator, or require a non-empty entry — each consuming site
// layers those concerns on the returned parts.
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
