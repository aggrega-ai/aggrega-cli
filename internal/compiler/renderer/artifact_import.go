// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"fmt"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// resolveArtifactLibraryImport resolves a bare-name ArtifactsLibrary import
// reference `<Library>#/artifacts/<Entry>` to the entry's bundle — the ordered
// filename→body map the consumption site expands into one output per file. It
// mirrors resolveWorkloadLibraryImport / resolveMappingLibraryImport keyed on
// `/artifacts/`, with one deliberate divergence forced by the bundle entry shape:
// an ArtifactsLibrary entry is a set of files, not a single body, so resolution
// returns the whole bundle rather than a scalar. A bundle file body is TERMINAL
// template content — a field-root `$import:` as a whole file body is not supported
// (a single file slot cannot expand into another bundle) and halts with
// ErrArtifactImportShape; cross-library composition happens at the consumption
// site, not nested inside a bundle file.
//
// libs is the resolved ArtifactsLibrary set — the local catalog plus any
// lockfile-pinned remote libraries bridged in at load. The version is supplied by
// that set (keyed by bare name); a `:version` floor in the reference is a
// lock-time selection assertion and is stripped here.
//
// Error model: a host-bearing reference is rejected (ErrHostInLibraryImport); a
// non-`#/artifacts/` shape is ErrArtifactImportShape; an unknown library or bundle
// surfaces ErrArtifactsLibraryNotFound / ErrArtifactsLibraryEntryNotFound.
func resolveArtifactLibraryImport(ref string, libs map[string]types.ArtifactsLibrary) (types.ArtifactsLibraryEntry, error) {
	library, entry, ok := splitArtifactImportRef(ref)
	if !ok {
		if strings.HasPrefix(ref, "oci://") {
			return types.ArtifactsLibraryEntry{}, fmt.Errorf("%w: %q", ErrHostInLibraryImport(), ref)
		}
		return types.ArtifactsLibraryEntry{}, fmt.Errorf("%w: %q", ErrArtifactImportShape(), ref)
	}
	// A stale `@` version separator is rejected with a steer to `:` rather than
	// falling through to an opaque library-not-found.
	if strings.IndexByte(library, '@') >= 0 {
		return types.ArtifactsLibraryEntry{}, fmt.Errorf("%w: version separator is ':', not '@' — use %q", ErrArtifactImportShape(), strings.Replace(ref, "@", ":", 1))
	}
	al, found := libs[library]
	if !found {
		return types.ArtifactsLibraryEntry{}, fmt.Errorf("%w: ArtifactsLibrary %q (bundle %q)", ErrArtifactsLibraryNotFound(), library, entry)
	}
	bundle, found := al.Spec.Artifacts.Get(entry)
	if !found {
		return types.ArtifactsLibraryEntry{}, fmt.Errorf("%w: bundle %q in ArtifactsLibrary %q", ErrArtifactsLibraryEntryNotFound(), entry, library)
	}
	// A bundle file body is terminal content: a field-root `$import:` as a whole
	// file body cannot expand into another bundle (one file slot, many files),
	// so it is rejected rather than mis-resolved.
	for _, fname := range bundle.Files.Keys() {
		body, _ := bundle.Files.Get(fname)
		if _, nested := fieldRootImportTarget(body); nested {
			return types.ArtifactsLibraryEntry{}, fmt.Errorf("%w: bundle %q file %q body is a field-root $import: (nested library import inside a bundle file is not supported; compose at the consumption site)",
				ErrArtifactImportShape(), entry, fname)
		}
	}
	return bundle, nil
}

// splitArtifactImportRef parses a host-less `<Library>[:<version>]#/artifacts/<Entry>`
// into its library name and entry (bundle) name, stripping any `:version` floor. A
// host-bearing reference (`oci://…`), a non-`#/artifacts/` fragment shape, or an
// empty entry or library returns ok=false. The host-less identity grammar itself is
// shared from [types.IdentityRefParts]; the `:version` strip and the non-empty-entry
// requirement are layered here — the same split shape splitWorkloadImportRef uses.
func splitArtifactImportRef(ref string) (library, entry string, ok bool) {
	doc, ent, matched := types.IdentityRefParts(ref, "/artifacts/")
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
