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
//
// # Every arm below is retained although a declared rule now stands in front of
// some of them
//
// The rules over the workload import a node's own Recipe writes are declared over
// the stage that holds the bound Recipe, and they halt the build before any render
// reaches this resolver. The arms stay, for three reasons of which any one would be
// enough.
//
// The workload a FeatureRecipe carries reaches this same resolver through a
// synthesized Recipe on the node's own consumption site, and the declared rules read
// the node's OWN Recipe, so no rule covers that FeatureRecipe and these arms are the
// whole of its refusal.
//
// This resolver RECURSES: an entry whose body is itself a field-root `$import:`
// re-enters it with a target lifted out of the library's own document. That target
// is a reference the consuming Recipe never wrote, so no rule over authored text can
// see it at all — which makes every arm below reachable from the recursion however
// completely the authored reference is covered.
//
// And a caller assembling a render's input directly runs no authored-stage rule at
// all, which is a documented shape of this package's entry rather than a defect to
// close here. A resolver must also return a value, so an arm cannot simply be
// dropped the way a guard can.
func resolveWorkloadLibraryImport(ref string, libs map[string]types.WorkloadLibrary, stack []string) (string, error) {
	found := lookUpWorkloadLibraryEntry(ref, libs)
	if !found.WellFormed {
		if strings.HasPrefix(ref, "oci://") {
			return "", fmt.Errorf("%w: %q", ErrHostInLibraryImport(), ref)
		}
		return "", fmt.Errorf("%w: %q", ErrWorkloadImportShape(), ref)
	}
	// A stale `@` version separator is rejected with a steer to `:` rather than
	// falling through to an opaque library-not-found.
	if found.StaleVersionSeparator {
		return "", fmt.Errorf("%w: version separator is ':', not '@' — use %q", ErrWorkloadImportShape(), strings.Replace(ref, "@", ":", 1))
	}
	key := found.Library + "#/workloads/" + found.EntryName
	if chain, cyclic := refcycle.Check(stack, key); cyclic {
		return "", fmt.Errorf("%w: %s", ErrCircularImport(), strings.Join(chain, " -> "))
	}
	if !found.LibraryFound {
		return "", fmt.Errorf("%w: WorkloadLibrary %q (entry %q)", ErrWorkloadLibraryNotFound(), found.Library, found.EntryName)
	}
	if !found.EntryFound {
		return "", fmt.Errorf("%w: entry %q in WorkloadLibrary %q", ErrWorkloadEntryNotFound(), found.EntryName, found.Library)
	}
	if target, ok := fieldRootImportTarget(found.Body); ok {
		return resolveWorkloadLibraryImport(target, libs, refcycle.Push(stack, key))
	}
	return found.Body, nil
}

// workloadLibraryLookup is how far one workload import reference got against the
// WorkloadLibrary set the catalog resolved: the names it gave, and which step of the
// resolution each of them answered.
//
// It is the cascade rather than a verdict, because two readers ask the same question
// of one reference and neither wants the same answer. The resolver asks in order to
// reach the entry's body and stops at the first step that fails; the facts a rule
// over an authored import field reads ask in order to report WHICH step failed, one
// fact per step. A single status value would make each of them test for a value the
// other's question is hidden behind, and two readings of the reference would be free
// to disagree about a library nothing compares.
//
// It reaches for nothing, which is what makes a second reader safe here. The set is
// the one the load already resolved and an entry's body is content that set carries,
// so asking twice over identical input answers identically.
type workloadLibraryLookup struct {
	// Library is the WorkloadLibrary the reference names, with any `:version`
	// floor stripped. It is empty where the reference does not split.
	Library string

	// EntryName is the entry the reference names inside that library, empty where
	// the reference does not split.
	EntryName string

	// WellFormed reports whether the reference splits into the bare-name
	// `<Library>#/workloads/<entry>` shape at all.
	WellFormed bool

	// StaleVersionSeparator reports whether the library half carries the retired
	// `@` separator. It is answered only for a reference that splits, because the
	// separator is a property of the library half and one that does not split has
	// no library half to read it out of.
	StaleVersionSeparator bool

	// LibraryFound reports whether the named library is in the resolved set.
	LibraryFound bool

	// EntryFound reports whether that library declares the named entry.
	EntryFound bool

	// Body is the entry's own Go-template fragment. It is empty wherever
	// EntryFound is false, and it is the text before any recursion collapses a
	// field-root `$import:` in it — which is what the resolver reads to decide
	// whether to recurse at all.
	Body string
}

// lookUpWorkloadLibraryEntry resolves a bare-name WorkloadLibrary import reference
// against the resolved library set, reporting how far it got.
//
// It is the single reading of that resolution. Every reader of it — the resolver
// above, and each fact a rule over an authored import field reads — comes through
// here, so a refusal and the facts describing it cannot come apart the way two
// derivations of one answer are free to.
//
// libs is the resolved WorkloadLibrary set: the local catalog. The version is
// supplied by that set, keyed by bare name, so a `:version` floor in the reference
// is a lock-time selection assertion and is stripped here.
func lookUpWorkloadLibraryEntry(
	ref string, libs map[string]types.WorkloadLibrary,
) workloadLibraryLookup {
	library, entry, ok := splitLibraryImportRef(ref, "/workloads/")
	if !ok {
		return workloadLibraryLookup{}
	}
	out := workloadLibraryLookup{Library: library, EntryName: entry, WellFormed: true}
	if strings.IndexByte(library, '@') >= 0 {
		out.StaleVersionSeparator = true
		return out
	}
	wl, found := libs[library]
	if !found {
		return out
	}
	out.LibraryFound = true
	we, found := wl.Spec.Workloads.Get(entry)
	if !found {
		return out
	}
	out.EntryFound = true
	out.Body = we.Body
	return out
}
