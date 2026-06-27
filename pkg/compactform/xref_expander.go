// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package compactform

import (
	"strings"

	"github.com/aggrega-ai/aggrega-cli/pkg/refcycle"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"go.yaml.in/yaml/v3"
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
// shape as a bare-name cross-document reference (`<Library>#/schemas/<Entry>`).
// URI-target validation (library exists, entry exists) happens at
// expandSchemaLibraryRef.
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
	// the bare-name cross-document reference shape.
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

// ociRefPrefix is the scheme marker for the host-bearing cross-document $ref
// form. A library reference addresses its target by identity, not by a
// registry host, so this prefix marks a reference that is rejected at compile.
const ociRefPrefix = "oci://"

// isOCIRef reports whether a $ref value carries a registry host (the
// `oci://<host>/…` form). Such a reference is rejected — references address
// libraries by identity (the bare-name `<Library>#/schemas/<Entry>` form),
// never by physical registry location.
func isOCIRef(ref string) bool {
	return strings.HasPrefix(ref, ociRefPrefix)
}

// parseOCIRefURI extracts the library and entry names from a host-bearing
// `oci://<host>/<path>/<LibraryName>[:<version>]#/schemas/<Entry>` reference so
// the rejection diagnostic can suggest the identity-form equivalent
// (`<LibraryName>#/schemas/<Entry>`). The host and any registry path precede
// the library name (the last path segment); an optional `:<version>` is
// stripped (the version is not part of an identity reference — the lockfile
// resolves it). Returns (parsed, true) when the library + entry are
// recoverable, (zero, false) otherwise — the rejection fires regardless, the
// parse only enriches the message.
func parseOCIRefURI(ref string) (xrefURI, bool) {
	if !strings.HasPrefix(ref, ociRefPrefix) {
		return xrefURI{}, false
	}
	body := ref[len(ociRefPrefix):]
	hash := strings.IndexByte(body, '#')
	if hash < 0 {
		return xrefURI{}, false
	}
	doc := body[:hash]
	frag := body[hash+1:]
	const fragPrefix = "/schemas/"
	if !strings.HasPrefix(frag, fragPrefix) {
		return xrefURI{}, false
	}
	entry := frag[len(fragPrefix):]
	if entry == "" || strings.ContainsAny(entry, "/#") {
		return xrefURI{}, false
	}
	// The library name is the last path segment, with an optional `:<version>`
	// floor stripped. Taking the segment BEFORE stripping the version keeps a
	// registry `host:port` (an earlier path segment) from being mistaken for the
	// version separator.
	library := doc
	if slash := strings.LastIndexByte(doc, '/'); slash >= 0 {
		library = doc[slash+1:]
	}
	// Strip a trailing version on either separator — the host-bearing form is
	// rejected regardless, and a steer to the bare-name identity should isolate
	// the name whether the author wrote the new ":" or a stale "@".
	if at := strings.IndexAny(library, ":@"); at >= 0 {
		library = library[:at]
	}
	if library == "" {
		return xrefURI{}, false
	}
	return xrefURI{library: library, entry: entry}, true
}

// canonicalRefKey is the cycle-guard identity for a cross-document reference:
// the version-stripped, host-less `<Library>#/schemas/<Entry>` form. The cycle
// guard keys on this canonical identity, never the raw reference string, so the
// same library entry reached via different spellings — a bare-name reference and
// a host-less `:version` reference of the same entry — shares one cycle key. A
// cycle that traverses such an entry therefore cannot evade detection by
// spelling its version inconsistently.
func canonicalRefKey(library, entry string) string {
	return library + "#/schemas/" + entry
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
//   - ErrHostInLibraryRef when refURI carries a registry host (`oci://…`).
//     A reference addresses a library by identity, never by physical location;
//     the message steers the author to the bare-name form, suggesting the exact
//     identity-form equivalent when the host-bearing URI is parseable.
//   - ErrReferenceUnresolvable when refURI is not a cross-document reference
//     shape (`<Library>#/schemas/<Entry>`). A host-less `:version` is accepted
//     (stripped here; consumed as a minimum-version floor at lock time), not
//     rejected.
//   - ErrUnresolvedSchemaLibraryRef when refURI's library portion is not
//     present in libraries, or the entry portion is absent from the library's
//     Spec.Schemas map. The error message names the unresolved URI and the
//     consumer file location.
//   - ErrCircularRef when refURI is already in resolutionStack. The error
//     message names the chain (oldest -> newest -> refURI).
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
	// A reference carrying a registry host addresses by location, not
	// identity — rejected. Suggest the identity-form equivalent when the
	// host-bearing URI parses into a library + entry.
	if isOCIRef(refURI) {
		msg := "cross-document $ref carries a registry host; reference the library by identity"
		if p, ok := parseOCIRefURI(refURI); ok {
			msg += " (use `" + p.library + "#/schemas/" + p.entry + "`)"
		}
		return nil, "", "", &CompileError{
			Path:    consumerFile,
			Keyword: "$ref",
			Message: msg + ": " + refURI,
			Err:     ErrHostInLibraryRef,
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

	// A host-less `:version` is accepted as an optional minimum-version-floor
	// assertion: the closure walk records it as a floor for minimum-version
	// selection at lock time, and the lockfile owns the selected version. The
	// reference resolves by name, never by version — so the expander strips the
	// asserted version and resolves the bare-name identity. The version separator
	// is `:` (matching Manifest mappings and the lockfile pin); a stale `@`
	// separator is rejected with a steer rather than failing as an opaque
	// library-not-found.
	if at := strings.IndexByte(parsed.library, ':'); at >= 0 {
		parsed.library = parsed.library[:at]
	}
	if strings.IndexByte(parsed.library, '@') >= 0 {
		return nil, "", "", &CompileError{
			Path:    consumerFile,
			Keyword: "$ref",
			Message: "cross-document $ref version separator is ':', not '@' — use `" + strings.Replace(refURI, "@", ":", 1) + "`: " + refURI,
			Err:     ErrReferenceUnresolvable,
		}
	}

	// Cycle check keyed on the canonical identity (version-stripped, host-less
	// `<Library>#/schemas/<Entry>`), never the raw reference string — so the
	// same entry reached via a bare-name reference and a host-less `:version`
	// reference shares one cycle key and a cycle cannot evade the guard by
	// inconsistent version spelling. The resolution stack holds canonical keys
	// (the caller pushes the canonical key, not the raw URI).
	key := canonicalRefKey(parsed.library, parsed.entry)
	if chain, cyclic := refcycle.Check(resolutionStack, key); cyclic {
		return nil, "", "", &CompileError{
			Path:    consumerFile,
			Keyword: "$ref",
			Message: "circular cross-document $ref expansion: " + strings.Join(chain, " -> "),
			Err:     ErrCircularRef,
		}
	}

	// Library lookup — the bare-name library portion maps directly to
	// metadata.name (the catalog's Catalog.SchemaLibraries map key, which is
	// version-blind; the version is supplied by the lockfile).
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
