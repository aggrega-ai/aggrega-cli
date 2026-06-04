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
// by that set (keyed by bare name); a `@version` floor in the reference is a
// lock-time selection assertion and is stripped here.
//
// Error model: a host-bearing reference is rejected (ErrHostInLibraryImport); a
// non-`#/mappings/` shape is ErrMappingImportShape; an unknown library or entry
// surfaces ErrMappingLibraryNotFound / ErrMappingEntryNotFound.
func resolveMappingLibraryImport(ref string, libs map[string]types.MappingLibrary, stack []string) (string, error) {
	library, entry, ok := splitMappingImportRef(ref)
	if !ok {
		if strings.HasPrefix(ref, "oci://") {
			return "", fmt.Errorf("%w: %q", ErrHostInLibraryImport, ref)
		}
		return "", fmt.Errorf("%w: %q", ErrMappingImportShape, ref)
	}
	key := library + "#/mappings/" + entry
	if chain, cyclic := refcycle.Check(stack, key); cyclic {
		return "", fmt.Errorf("%w: %s", ErrCircularImport, strings.Join(chain, " -> "))
	}
	ml, found := libs[library]
	if !found {
		return "", fmt.Errorf("%w: MappingLibrary %q (entry %q)", ErrMappingLibraryNotFound, library, entry)
	}
	me, found := ml.Spec.Mappings.Get(entry)
	if !found {
		return "", fmt.Errorf("%w: entry %q in MappingLibrary %q", ErrMappingEntryNotFound, entry, library)
	}
	if target, ok := fieldRootImportTarget(me.Body); ok {
		return resolveMappingLibraryImport(target, libs, refcycle.Push(stack, key))
	}
	return me.Body, nil
}

// splitMappingImportRef parses a host-less `<Library>[@<version>]#/mappings/<Entry>`
// into its library name and entry, stripping any `@version` floor. A host-bearing
// reference (`oci://…`) or a non-`#/mappings/` fragment shape returns ok=false.
// This is the consumption-side mirror of the closure walker's identity-import
// gate (kept here so the renderer stays decoupled from pkg/closure).
func splitMappingImportRef(ref string) (library, entry string, ok bool) {
	if ref == "" || strings.HasPrefix(ref, "oci://") {
		return "", "", false
	}
	hash := strings.IndexByte(ref, '#')
	if hash <= 0 {
		return "", "", false
	}
	doc := ref[:hash]
	frag := ref[hash+1:]
	const prefix = "/mappings/"
	if !strings.HasPrefix(frag, prefix) {
		return "", "", false
	}
	entry = frag[len(prefix):]
	if entry == "" {
		return "", "", false
	}
	if at := strings.IndexByte(doc, '@'); at >= 0 {
		doc = doc[:at]
	}
	if doc == "" {
		return "", "", false
	}
	return doc, entry, true
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
