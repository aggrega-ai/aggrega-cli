// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package compactform

import (
	"strings"

	"github.com/cognitio/aggrega-cli/pkg/types"
	"gopkg.in/yaml.v3"
)

// xrefURI is the parsed form of a cross-document $ref URI. The compact-
// form authoring shape splits at `#` into a document portion (PascalCase
// SchemaLibrary metadata.name) and a fragment portion (JSON Pointer of
// the form `/schemas/<entry-name>`).
//
// Example: a $ref value of "BaseComponents#/schemas/BaseComponentParameter"
// parses to {library: "BaseComponents", entry: "BaseComponentParameter"}.
type xrefURI struct {
	library string
	entry   string
}

// parseCrossDocRefURI splits a cross-document $ref value into its library
// and entry components. Returns (parsed, true) on a syntactically valid
// cross-document URI, (zero, false) otherwise — callers route in-document
// references (`#/$defs/...`) and other shapes to their own emission paths.
//
// The function does not consult the catalog; it only validates the URI's
// shape against the canonical-$id convention documented at
// pkg/types/schemalibrary.go:51-55. URI-target validation (library exists,
// entry exists) happens at expandSchemaLibraryRef.
func parseCrossDocRefURI(ref string) (xrefURI, bool) {
	if ref == "" {
		return xrefURI{}, false
	}
	// In-document refs start with `#/` — no document portion. Not a
	// cross-document URI.
	if strings.HasPrefix(ref, "#") {
		return xrefURI{}, false
	}
	hash := strings.IndexByte(ref, '#')
	if hash < 0 {
		// No fragment — cannot identify a specific entry within the library.
		// The cross-document shape requires both library and entry.
		return xrefURI{}, false
	}
	doc := ref[:hash]
	frag := ref[hash+1:]
	// Fragment must be `/schemas/<entry-name>`. The leading slash matches
	// JSON Pointer / RFC 6901 fragment shape; the `schemas` segment matches
	// the canonical-$id derivation rule.
	const fragPrefix = "/schemas/"
	if !strings.HasPrefix(frag, fragPrefix) {
		return xrefURI{}, false
	}
	entry := frag[len(fragPrefix):]
	if doc == "" || entry == "" {
		return xrefURI{}, false
	}
	// Reject any further `/` in the entry portion — entries are scalar
	// PascalCase identifiers, not nested paths.
	if strings.ContainsAny(entry, "/#") {
		return xrefURI{}, false
	}
	return xrefURI{library: doc, entry: entry}, true
}

// isOCIRef reports whether a $ref value carries the `oci://` scheme.
// OCI URIs are recognised but currently rejected with
// ErrOCINotYetSupported; activation lands when the lockfile + OCI pull
// infrastructure ships.
func isOCIRef(ref string) bool {
	return strings.HasPrefix(ref, "oci://")
}

// expandSchemaLibraryRef resolves a cross-document $ref to its target
// SchemaLibrary entry's content. The caller (the cross-document branch
// of the $ref dispatch arm) is responsible for compiling the returned
// yaml.Node and merging the result into the consumer's schema; this
// function does not compile, merge, or annotate.
//
// resolutionStack carries in-flight $ref URIs encountered during the
// caller's recursive expansion. The expander prepends the current refURI
// to the stack before lookup so library-to-library cycle chains surface
// as ErrCircularRef rather than infinite recursion. The caller threads
// the extended stack through the recursive Compile call that produces
// the merged schema.
//
// Error model:
//   - ErrOCINotYetSupported when refURI has the `oci://` scheme.
//   - ErrUnresolvedSchemaLibraryRef when refURI's library portion is not
//     present in libraries, or the entry portion is absent from the
//     library's Spec.Schemas map. The error message names the unresolved
//     URI and the consumer file location.
//   - ErrCircularRef when refURI is already in resolutionStack. The error
//     message names the chain (oldest -> newest -> refURI).
//   - ErrReferenceUnresolvable when refURI is syntactically malformed
//     (not a recognised cross-document or OCI shape).
//
// Returns the entry's raw yaml.Node alongside the canonical library +
// entry name pair. The names are returned so the caller can use them for
// source-map annotation injection without re-parsing the URI.
func expandSchemaLibraryRef(
	refURI string,
	libraries map[string]types.SchemaLibrary,
	resolutionStack []string,
	consumerFile string,
) (entryContent *yaml.Node, libraryName, entryName string, err error) {
	// OCI scheme — recognised but deferred.
	if isOCIRef(refURI) {
		return nil, "", "", &CompileError{
			Path:    consumerFile,
			Keyword: "$ref",
			Message: "oci:// $ref scheme not yet supported: " + refURI,
			Err:     ErrOCINotYetSupported,
		}
	}

	parsed, ok := parseCrossDocRefURI(refURI)
	if !ok {
		return nil, "", "", &CompileError{
			Path:    consumerFile,
			Keyword: "$ref",
			Message: "cross-document $ref shape must be `<LibraryName>#/schemas/<EntryName>`: " + refURI,
			Err:     ErrReferenceUnresolvable,
		}
	}

	// Cycle check: refURI already on the in-flight resolution stack.
	for _, inFlight := range resolutionStack {
		if inFlight == refURI {
			chain := append(append([]string{}, resolutionStack...), refURI)
			return nil, "", "", &CompileError{
				Path:    consumerFile,
				Keyword: "$ref",
				Message: "circular cross-document $ref expansion: " + strings.Join(chain, " -> "),
				Err:     ErrCircularRef,
			}
		}
	}

	// Library lookup — document portion of the URI maps directly to
	// metadata.name (the catalog's Catalog.SchemaLibraries map key).
	lib, found := libraries[parsed.library]
	if !found {
		return nil, "", "", &CompileError{
			Path:    consumerFile,
			Keyword: "$ref",
			Message: "SchemaLibrary " + parsed.library + " not found in catalog (referenced from " + consumerFile + "): " + refURI,
			Err:     ErrUnresolvedSchemaLibraryRef,
		}
	}

	// Entry lookup — fragment portion navigates within Spec.Schemas.
	entry, entryFound := lib.Spec.Schemas.Get(parsed.entry)
	if !entryFound {
		return nil, "", "", &CompileError{
			Path:    consumerFile,
			Keyword: "$ref",
			Message: "entry " + parsed.entry + " not found in SchemaLibrary " + parsed.library + ": " + refURI,
			Err:     ErrUnresolvedSchemaLibraryRef,
		}
	}

	// Return the entry's raw yaml.Node alongside the canonical names so
	// the caller can recursively compile + annotate. SchemaEntry.Content
	// is captured at parse time by SchemaEntry.UnmarshalYAML; returning
	// a pointer to a fresh copy avoids the caller mutating the catalog's
	// stored copy.
	content := entry.Content
	return &content, parsed.library, parsed.entry, nil
}
