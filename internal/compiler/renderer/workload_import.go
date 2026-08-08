// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"fmt"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/pkg/refcycle"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// resolveWorkloadLibraryImport resolves a bare-name WorkloadLibrary import
// reference `<Library>#/workloads/<Entry>` to the entry's Go-template fragment.
// It mirrors resolveMappingLibraryImport keyed on `/workloads/`: recursive, so an
// entry whose whole body is itself a field-root `$import:` of another library
// entry resolves to that target's fragment; recursion is guarded by the shared
// refcycle primitive keyed on the canonical `<Library>#/workloads/<Entry>`
// identity, so a library↔library import cycle halts with ErrCircularImport.
//
// libs is the resolved WorkloadLibrary set — the local catalog. The version is
// supplied by that set (keyed by bare name); a `:version` floor in the reference
// is a lock-time selection assertion and is stripped here.
//
// Error model: a host-bearing reference is rejected (ErrHostInLibraryImport); a
// non-`#/workloads/` shape is ErrWorkloadImportShape; an unknown library or entry
// surfaces ErrWorkloadLibraryNotFound / ErrWorkloadEntryNotFound.
func resolveWorkloadLibraryImport(ref string, libs map[string]types.WorkloadLibrary, stack []string) (string, error) {
	library, entry, ok := splitWorkloadImportRef(ref)
	if !ok {
		if strings.HasPrefix(ref, "oci://") {
			return "", fmt.Errorf("%w: %q", ErrHostInLibraryImport(), ref)
		}
		return "", fmt.Errorf("%w: %q", ErrWorkloadImportShape(), ref)
	}
	// A stale `@` version separator is rejected with a steer to `:` rather than
	// falling through to an opaque library-not-found.
	if strings.IndexByte(library, '@') >= 0 {
		return "", fmt.Errorf("%w: version separator is ':', not '@' — use %q", ErrWorkloadImportShape(), strings.Replace(ref, "@", ":", 1))
	}
	key := library + "#/workloads/" + entry
	if chain, cyclic := refcycle.Check(stack, key); cyclic {
		return "", fmt.Errorf("%w: %s", ErrCircularImport(), strings.Join(chain, " -> "))
	}
	wl, found := libs[library]
	if !found {
		return "", fmt.Errorf("%w: WorkloadLibrary %q (entry %q)", ErrWorkloadLibraryNotFound(), library, entry)
	}
	we, found := wl.Spec.Workloads.Get(entry)
	if !found {
		return "", fmt.Errorf("%w: entry %q in WorkloadLibrary %q", ErrWorkloadEntryNotFound(), entry, library)
	}
	if target, ok := fieldRootImportTarget(we.Body); ok {
		return resolveWorkloadLibraryImport(target, libs, refcycle.Push(stack, key))
	}
	return we.Body, nil
}

// splitWorkloadImportRef parses a host-less `<Library>[:<version>]#/workloads/<Entry>`
// into its library name and entry, stripping any `:version` floor. A host-bearing
// reference (`oci://…`), a non-`#/workloads/` fragment shape, or an empty entry or
// library returns ok=false. The host-less identity grammar itself is shared from
// [types.IdentityRefParts]; the `:version` strip and the non-empty-entry
// requirement are layered here — the same split shape splitMappingImportRef uses.
func splitWorkloadImportRef(ref string) (library, entry string, ok bool) {
	doc, ent, matched := types.IdentityRefParts(ref, "/workloads/")
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
