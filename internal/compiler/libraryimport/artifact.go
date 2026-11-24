// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package libraryimport

import (
	"fmt"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// ArtifactLookup is how far one artifacts entry's library import
// reference got against the ArtifactLibrary set the catalog resolved: the names
// it gave, and which step of the resolution each of them answered.
//
// It is the cascade rather than a verdict, because two readers ask the same
// question of one reference and neither wants the same answer. The artifacts walk
// asks in order to expand the bundle and stops at the first step that fails; the
// facts a rule over an authored artifacts entry reads ask in order to report
// WHICH step failed, one fact per step. A single status value would make each of
// them test for a value the other's question is hidden behind, and two readings
// of the reference would be free to disagree about a library nothing compares.
//
// The steps are separate fields for that reason and not because a caller may want
// to skip one. Each is filled only as far as the cascade got: a reference that
// does not split names no library, and one naming a library the set does not hold
// carries no bundle.
//
// It reaches for nothing, which is what makes a second reader safe here. The set
// is the one the load already resolved and a bundle's files are content that set
// carries, so asking twice over identical input answers identically. That is not
// true of an entry supplied by a file of the workspace, where the read happens
// once inside the walk and is retained rather than repeated.
type ArtifactLookup struct {
	// Library is the ArtifactLibrary the reference names, with any `:version`
	// floor stripped. It is empty where the reference does not split.
	Library string

	// BundleName is the bundle the reference names inside that library, empty
	// where the reference does not split.
	BundleName string

	// WellFormed reports whether the reference splits into the bare-name
	// `<Library>#/artifacts/<bundle>` shape at all.
	WellFormed bool

	// StaleVersionSeparator reports whether the library half carries the retired
	// `@` separator. It is answered only for a reference that splits, because the
	// separator is a property of the library half and one that does not split has
	// no library half to read it out of.
	StaleVersionSeparator bool

	// LibraryFound reports whether the named library is in the resolved set.
	LibraryFound bool

	// BundleFound reports whether that library declares the named bundle.
	BundleFound bool

	// Bundle is the bundle itself — the ordered filename-to-body mapping the
	// consumption site expands into one output per file. It is the zero value
	// wherever BundleFound is false.
	Bundle types.ArtifactLibraryEntry
}

// LookUpArtifactBundle resolves a bare-name ArtifactLibrary import
// reference against the resolved library set, reporting how far it got.
//
// It is the single reading of that resolution. Every reader of it — the walk that
// expands the bundle, and each fact a rule over an authored artifacts entry reads
// — comes through here, so a refusal and the facts describing it cannot come
// apart the way two derivations of one answer are free to.
//
// libs is the resolved ArtifactLibrary set: the local catalog plus any
// lockfile-pinned remote libraries bridged in at load. The version is supplied by
// that set, keyed by bare name, so a `:version` floor in the reference is a
// lock-time selection assertion and is stripped here.
func LookUpArtifactBundle(
	ref string, libs map[string]types.ArtifactLibrary,
) ArtifactLookup {
	library, entry, ok := SplitRef(ref, "/artifacts/")
	if !ok {
		return ArtifactLookup{}
	}
	out := ArtifactLookup{Library: library, BundleName: entry, WellFormed: true}
	if strings.IndexByte(library, '@') >= 0 {
		out.StaleVersionSeparator = true
		return out
	}
	al, found := libs[library]
	if !found {
		return out
	}
	out.LibraryFound = true
	bundle, found := al.Spec.Artifacts.Get(entry)
	if !found {
		return out
	}
	out.BundleFound = true
	out.Bundle = bundle
	return out
}

// Artifact resolves a bare-name ArtifactLibrary import
// reference `<Library>#/artifacts/<Entry>` to the entry's bundle — the ordered
// filename→body map the consumption site expands into one output per file. It
// mirrors the workload and mapping resolvers keyed on
// `/artifacts/`, with one deliberate divergence forced by the bundle entry shape:
// an ArtifactLibrary entry is a set of files, not a single body, so resolution
// returns the whole bundle rather than a scalar. A bundle file body is TERMINAL
// template content — a field-root `$import:` as a whole file body is not supported
// (a single file slot cannot expand into another bundle) and halts with
// ErrArtifactImportShape; cross-library composition happens at the consumption
// site, not nested inside a bundle file.
//
// libs is the resolved ArtifactLibrary set — the local catalog plus any
// lockfile-pinned remote libraries bridged in at load. The version is supplied by
// that set (keyed by bare name); a `:version` floor in the reference is a
// lock-time selection assertion and is stripped by the lookup this reads.
//
// Error model: a host-bearing reference is rejected (ErrHostInLibraryImport); a
// non-`#/artifacts/` shape is ErrArtifactImportShape; an unknown library or bundle
// surfaces ErrArtifactLibraryNotFound / ErrArtifactLibraryEntryNotFound.
//
// # Every arm below is retained although a declared rule now stands in front of
// some of them
//
// The rules over a node's own authored artifacts entries are declared over the
// stage that holds the bound Recipe, and they halt the build before any render
// reaches this resolver — so for a node's own entries these arms are unreachable
// by construction rather than by chance. They stay for two reasons, and neither
// is convenience. The artifacts block a FeatureRecipe carries flows through the
// same walk under a synthesized Recipe, and the declared rules read the node's
// OWN Recipe, so no rule covers that FeatureRecipe and these arms are the whole
// of its refusal. And a caller assembling a render's input directly runs no
// authored-stage rule at all, which is a documented shape of this renderer's entry
// rather than a defect to close here. Removing an arm would leave both silently
// admitting a reference the compiler refuses everywhere else; a resolver must
// also return a value, so an arm cannot simply be dropped the way a guard can.
func Artifact(ref string, libs map[string]types.ArtifactLibrary) (types.ArtifactLibraryEntry, error) {
	found := LookUpArtifactBundle(ref, libs)
	if !found.WellFormed {
		// No production path reaches this arm, and it is retained deliberately.
		// The one caller gates HasOCIPrefix ahead of its library-import branch, and
		// unlike the mapping and workload resolvers this one does not recurse — a
		// nested field-root $import inside a bundle file is refused rather than
		// re-entered — so nothing feeds it a host-bearing reference. It stays
		// because the three resolvers are deliberate mirrors of one another, and
		// because without it the same authoring mistake would surface here as a
		// shape error rather than a host error, which is the wrong steer. Its
		// conformance is proved against its specification by a direct test rather
		// than by a caller.
		if strings.HasPrefix(ref, "oci://") {
			return types.ArtifactLibraryEntry{}, fmt.Errorf("%w: %q", ErrHostInLibraryImport(), ref)
		}
		return types.ArtifactLibraryEntry{}, fmt.Errorf("%w: %q", ErrArtifactImportShape(), ref)
	}
	// A stale `@` version separator is rejected with a steer to `:` rather than
	// falling through to an opaque library-not-found.
	if found.StaleVersionSeparator {
		return types.ArtifactLibraryEntry{}, fmt.Errorf("%w: version separator is ':', not '@' — use %q", ErrArtifactImportShape(), strings.Replace(ref, "@", ":", 1))
	}
	if !found.LibraryFound {
		return types.ArtifactLibraryEntry{}, fmt.Errorf("%w: ArtifactLibrary %q (bundle %q)", ErrArtifactLibraryNotFound(), found.Library, found.BundleName)
	}
	if !found.BundleFound {
		return types.ArtifactLibraryEntry{}, fmt.Errorf("%w: bundle %q in ArtifactLibrary %q", ErrArtifactLibraryEntryNotFound(), found.BundleName, found.Library)
	}
	// A bundle file body is terminal content: a field-root `$import:` as a whole
	// file body cannot expand into another bundle (one file slot, many files),
	// so it is rejected rather than mis-resolved. The first offending file is
	// named, which is what this arm reported before the reading was shared: a
	// resolver returns one error, and the rule declared over the same fact is the
	// reading that reports every one of them.
	if nested := BundleNestedImportFiles(found.Bundle); len(nested) > 0 {
		return types.ArtifactLibraryEntry{}, fmt.Errorf("%w: bundle %q file %q body is a field-root $import: (nested library import inside a bundle file is not supported; compose at the consumption site)",
			ErrArtifactImportShape(), found.BundleName, nested[0])
	}
	return found.Bundle, nil
}

// BundleNestedImportFiles returns the names of a bundle's files whose own body is
// a field-root `$import:`, in the order the bundle declares its files.
//
// It is the single reading of what makes a bundle file body non-terminal, shared
// by the resolver above and by the fact a rule over an authored artifacts entry
// reads. Two readings would agree the day they were written and would be free to
// differ the next time the field-root vocabulary moves, with nothing comparing
// them — and the disagreement would surface as a build that refuses a bundle the
// rule admitted, or admits one the rule refused.
func BundleNestedImportFiles(bundle types.ArtifactLibraryEntry) []string {
	var out []string
	for _, fname := range bundle.Files.Keys() {
		body, _ := bundle.Files.Get(fname)
		if _, nested := FieldRootTarget(body); nested {
			out = append(out, fname)
		}
	}
	return out
}
