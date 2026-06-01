// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package compactform

import (
	"strings"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
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

// ociRefPrefix is the scheme marker for OCI-form cross-document $ref URIs.
const ociRefPrefix = "oci://"

// isOCIRef reports whether a $ref value carries the `oci://` scheme. The
// OCI form resolves for workspace-local libraries (matched by name against
// the loaded catalog); only fetch-at-build of a remote, not-locally-present
// library is deferred.
func isOCIRef(ref string) bool {
	return strings.HasPrefix(ref, ociRefPrefix)
}

// parseOCIRefURI splits an OCI-form cross-document $ref value into its
// library, version, and entry components. The canonical OCI authoring shape
// (pkg/types/schemalibrary.go) is
// `oci://<host>/<path>/<LibraryName>@<version>#/schemas/<Entry>`: the host and
// any registry path precede the library name (the last path segment), `@`
// separates the pinned version, and the `#/schemas/<Entry>` fragment names the
// entry. Returns (parsed, version, true) on a syntactically valid OCI URI,
// (zero, "", false) when the shape is malformed (missing `@version` or a
// `/schemas/<Entry>` fragment) — callers route a malformed OCI URI to the
// shape error at expandSchemaLibraryRef.
//
// The library is matched by name against the loaded catalog at resolution
// time; <version> is a validated assertion against the resolved library, not a
// selector (the catalog's SchemaLibraries map is name-keyed, one version per
// name). The host portion is not consulted — workspace-local libraries carry
// the placeholder host `local.aggrega`, and resolution is by name regardless
// of host.
func parseOCIRefURI(ref string) (xrefURI, string, bool) {
	if !strings.HasPrefix(ref, ociRefPrefix) {
		return xrefURI{}, "", false
	}
	body := ref[len(ociRefPrefix):]
	hash := strings.IndexByte(body, '#')
	if hash < 0 {
		return xrefURI{}, "", false
	}
	doc := body[:hash]
	frag := body[hash+1:]
	const fragPrefix = "/schemas/"
	if !strings.HasPrefix(frag, fragPrefix) {
		return xrefURI{}, "", false
	}
	entry := frag[len(fragPrefix):]
	if entry == "" || strings.ContainsAny(entry, "/#") {
		return xrefURI{}, "", false
	}
	// `@` separates the pinned version from the host/path/library portion.
	at := strings.LastIndexByte(doc, '@')
	if at < 0 {
		return xrefURI{}, "", false
	}
	version := doc[at+1:]
	pathPortion := doc[:at]
	if version == "" || pathPortion == "" {
		return xrefURI{}, "", false
	}
	// The library name is the last path segment before `@`.
	library := pathPortion
	if slash := strings.LastIndexByte(pathPortion, '/'); slash >= 0 {
		library = pathPortion[slash+1:]
	}
	if library == "" {
		return xrefURI{}, "", false
	}
	return xrefURI{library: library, entry: entry}, version, true
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
//   - ErrOCINotYetSupported when refURI is an `oci://` form whose library is
//     NOT in the local catalog — a remote library deferred until the
//     remote-library fetch path ships (distinct from a plain not-found so a
//     reader can tell not-yet-fetchable from absent).
//   - ErrSchemaLibraryVersionMismatch when an `oci://` refURI resolves its
//     library by name but the library's metadata.version does not equal the
//     URI's `@version` assertion.
//   - ErrUnresolvedSchemaLibraryRef when refURI's library portion is not
//     present in libraries (bare-name form), or the entry portion is absent
//     from the library's Spec.Schemas map (either form). The error message
//     names the unresolved URI and the consumer file location.
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
	// Parse: both the OCI form and the bare-name form yield a {library,
	// entry} pair; the OCI form additionally pins a version asserted below.
	// The library-not-found error class differs by form (deferred-remote for
	// OCI, plain not-found for bare-name), so track which form parsed.
	var (
		parsed     xrefURI
		ociVersion string
		isOCI      bool
	)
	if isOCIRef(refURI) {
		p, version, ok := parseOCIRefURI(refURI)
		if !ok {
			return nil, "", "", &CompileError{
				Path:    consumerFile,
				Keyword: "$ref",
				Message: "OCI cross-document $ref shape must be `oci://<host>/<path>/<LibraryName>@<version>#/schemas/<EntryName>`: " + refURI,
				Err:     ErrReferenceUnresolvable,
			}
		}
		parsed, ociVersion, isOCI = p, version, true
	} else {
		p, ok := parseCrossDocRefURI(refURI)
		if !ok {
			return nil, "", "", &CompileError{
				Path:    consumerFile,
				Keyword: "$ref",
				Message: "cross-document $ref shape must be `<LibraryName>#/schemas/<EntryName>`: " + refURI,
				Err:     ErrReferenceUnresolvable,
			}
		}
		parsed = p
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

	// Library lookup — the library portion (bare name, or the name extracted
	// from the OCI URI) maps directly to metadata.name (the catalog's
	// Catalog.SchemaLibraries map key, which is version-blind).
	lib, found := libraries[parsed.library]
	if !found {
		if isOCI {
			// Not in the local catalog: a remote library that would have to
			// be fetched at build. Deferred until the remote-library fetch
			// path ships — a distinct signal from a plain not-found.
			return nil, "", "", &CompileError{
				Path:    consumerFile,
				Keyword: "$ref",
				Message: "remote OCI SchemaLibrary " + parsed.library + " is not present in the local catalog and remote fetch-at-build is not yet supported: " + refURI,
				Err:     ErrOCINotYetSupported,
			}
		}
		return nil, "", "", &CompileError{
			Path:    consumerFile,
			Keyword: "$ref",
			Message: "SchemaLibrary " + parsed.library + " not found in catalog (referenced from " + consumerFile + "): " + refURI,
			Err:     ErrUnresolvedSchemaLibraryRef,
		}
	}

	// Version assertion (OCI form only): the URI's `@version` must equal the
	// loaded library's metadata.version. The map is name-keyed, so `@version`
	// is a validated assertion against the single loaded version, not a
	// selector that picks among versions.
	if isOCI && lib.Metadata.Version != ociVersion {
		return nil, "", "", &CompileError{
			Path:    consumerFile,
			Keyword: "$ref",
			Message: "OCI $ref pins version " + ociVersion + " but SchemaLibrary " + parsed.library + " is loaded at version " + lib.Metadata.Version + ": " + refURI,
			Err:     ErrSchemaLibraryVersionMismatch,
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
