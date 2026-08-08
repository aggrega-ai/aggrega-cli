// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"fmt"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/pkg/refcycle"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// resolveMappingLibraryImport resolves a bare-name MappingLibrary import
// reference `<Library>#/mappings/<Entry>` to the entry's Go-template fragment.
// It is recursive: an entry whose whole body is itself a field-root `$import:`
// of another library entry resolves to that target's fragment, so a chain of
// library indirections collapses to the terminal template before the Go-template
// pass. Recursion is guarded by the shared refcycle primitive keyed on the
// canonical `<Library>#/mappings/<Entry>` identity, so a library↔library import
// cycle halts with ErrCircularImport instead of recursing without bound.
//
// libs is the resolved MappingLibrary set — the local catalog plus any
// lockfile-pinned remote libraries bridged in at load. The version is supplied
// by that set (keyed by bare name); a `:version` floor in the reference is a
// lock-time selection assertion and is stripped here.
//
// Error model: a host-bearing reference is rejected (ErrHostInLibraryImport); a
// non-`#/mappings/` shape is ErrMappingImportShape; an unknown library or entry
// surfaces ErrMappingLibraryNotFound / ErrMappingEntryNotFound.
func resolveMappingLibraryImport(ref string, libs map[string]types.MappingLibrary, stack []string) (string, error) {
	library, entry, ok := splitLibraryImportRef(ref, "/mappings/")
	if !ok {
		if strings.HasPrefix(ref, "oci://") {
			return "", fmt.Errorf("%w: %q", ErrHostInLibraryImport(), ref)
		}
		return "", fmt.Errorf("%w: %q", ErrMappingImportShape(), ref)
	}
	// A stale `@` version separator is rejected with a steer to `:` rather than
	// falling through to an opaque library-not-found.
	if strings.IndexByte(library, '@') >= 0 {
		return "", fmt.Errorf("%w: version separator is ':', not '@' — use %q", ErrMappingImportShape(), strings.Replace(ref, "@", ":", 1))
	}
	key := library + "#/mappings/" + entry
	if chain, cyclic := refcycle.Check(stack, key); cyclic {
		return "", fmt.Errorf("%w: %s", ErrCircularImport(), strings.Join(chain, " -> "))
	}
	ml, found := libs[library]
	if !found {
		return "", fmt.Errorf("%w: MappingLibrary %q (entry %q)", ErrMappingLibraryNotFound(), library, entry)
	}
	me, found := ml.Spec.Mappings.Get(entry)
	if !found {
		return "", fmt.Errorf("%w: entry %q in MappingLibrary %q", ErrMappingEntryNotFound(), entry, library)
	}
	if target, ok := fieldRootImportTarget(me.Body); ok {
		return resolveMappingLibraryImport(target, libs, refcycle.Push(stack, key))
	}
	return me.Body, nil
}

// splitLibraryImportRef parses a host-less `<Library>[:<version>]#<fragmentPrefix><Entry>`
// into its library name and entry, stripping any `:version` floor. A host-bearing
// reference (`oci://…`), a fragment shape other than the requested one, or an empty
// entry or library returns ok=false.
//
// The host-less identity grammar itself is shared from [types.IdentityRefParts]; the
// `:version` strip and the non-empty-entry requirement are layered here. Every
// import axis consumes the same grammar and differs only in the fragment prefix it
// admits, so the split is written once and the axis supplies the prefix.
//
// The prefixes are stated at the three call sites rather than read from the
// closure package's axis table: this is the render-time consumption site, and it
// stays decoupled from the catalog/closure layer on purpose. A reference reaches
// here already resolved, so nothing here needs to know how it was pinned.
func splitLibraryImportRef(ref, fragmentPrefix string) (library, entry string, ok bool) {
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

// fieldRootImportTarget reports whether body's first non-whitespace token is a
// field-root `$import: <ref>` directive (whole-content replacement) and returns
// <ref>. A plain template body (no field-root `$import:`) returns ok=false. The
// field-root-only rule matches the `$import:` authoring vocabulary: an `$import:`
// embedded deeper in a template body is not a whole-content replacement.
func fieldRootImportTarget(body string) (ref string, ok bool) {
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
