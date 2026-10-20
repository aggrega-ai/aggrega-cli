// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package recipepin owns a Recipe or FeatureRecipe's intrinsic-input content
// pins — one per resolved remote source, one per $import artifact file — from
// the moment the resolution computes them to the moment the build reads them
// back.
//
// The pins live in a lock file beside the entity, never inside it. An authored
// document carries exactly what its author typed, so the resolution has nothing
// to splice and no writable-file requirement on the entity itself; what it
// derives goes in the sibling the author commits and reads as a diff. [Lock],
// [ReadLock] and [WriteLock] are that file's one home.
//
// The package never fetches. The caller supplies the resolved source pins, and
// the $import file bytes are read from disk — so what is recorded is what the
// resolution acquired and what the build compares against is what is on disk
// now.
package recipepin

import (
	"fmt"
	"sort"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/pkg/digest"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// ArtifactPinsForRecipe computes the artifact content pins for a recipe's
// artifacts map — for a Recipe, the map [PinnedArtifactEntries] presents, which
// carries its boundary block's entries beside its own under their pin names: one
// pin per entry whose value is a LOCAL file import ($import),
// digesting the imported file's bytes. Inline (literal) entries are part of the
// entity and carry no pin. A library import (`<Library>#/artifacts/<entry>`)
// carries no file pin either — its content is lockfile-pinned by the per-Release
// library pin, not file-digest-pinned — and a host-bearing (`oci://…`) import is
// rejected at the consumption site; both are skipped here. Each remaining
// entry's Import is the resolved absolute $import path (the catalog rewrites
// relative $import paths to absolute at load). The result is sorted by name.
//
// entityKind is "Recipe" or "FeatureRecipe" and entityName is the entity's
// metadata.name. They name the document the artifacts map was read from, and they
// are parameters because this computation is shared by both kinds and the map
// alone says which entity declared it. A failure to read one of the imported
// files reports the entity, the artifacts key and the path together, which is
// what a reader at the pin step has to have in order to know which of a
// closure's recipes to open.
func ArtifactPinsForRecipe(artifacts types.OrderedMap[types.ArtifactEntry], entityKind, entityName string) ([]types.ArtifactDigest, error) {
	var pins []types.ArtifactDigest
	for _, name := range artifacts.Keys() {
		entry, _ := artifacts.Get(name)
		if entry.Import == "" || entry.IsLibraryImport() || entry.HasOCIPrefix() {
			continue
		}
		body, err := entry.Text()
		if err != nil {
			return nil, artifactImportLoadError(entry.Import, err, entityKind, entityName, name)
		}
		pins = append(pins, types.ArtifactDigest{Name: name, Digest: digest.ContentDigest([]byte(body))})
	}
	sortArtifactPins(pins)
	return pins, nil
}

// BoundaryArtifactPinPrefix opens the name the entity lock pins a file under when
// a Recipe imports it inside its boundary block, `spec.boundary.artifacts`. Such a
// file is pinned in the same artifactDigests list as the Recipe's own artifacts, so
// the two blocks share one pin computation and one comparison; the prefix is what
// keeps an entry of each block apart when both are written under one key. It cannot
// collide with an own-artifacts name: an artifacts key is a single filename, which
// carries no `/`.
const BoundaryArtifactPinPrefix = "boundary/"

// BoundaryArtifactPinName is the name the entity lock pins the file a boundary-block
// entry imports under.
func BoundaryArtifactPinName(key string) string { return BoundaryArtifactPinPrefix + key }

// BoundaryArtifactKeyOf reports the boundary-block key a pin name was composed
// from, and whether the name is one at all.
func BoundaryArtifactKeyOf(pinName string) (string, bool) {
	return strings.CutPrefix(pinName, BoundaryArtifactPinPrefix)
}

// PinnedArtifactEntries is a Recipe's file-importing entries as the entity lock
// pins them: its own artifacts under their keys, then its boundary block's under
// [BoundaryArtifactPinName], each block in author order. It is the one answer to
// which entries a Recipe's lock pins and under what names, read by the pin step
// and by the verifying pull alike, so the two cannot come to disagree about a
// boundary-block import; [ArtifactPinsForRecipe] computes the pins over it.
func PinnedArtifactEntries(r *types.Recipe) types.OrderedMap[types.ArtifactEntry] {
	if r == nil {
		return types.OrderedMap[types.ArtifactEntry]{}
	}
	if r.Spec.Boundary.Artifacts.Len() == 0 {
		return r.Spec.Artifacts
	}
	out := types.NewOrderedMap[types.ArtifactEntry]()
	for _, key := range r.Spec.Artifacts.Keys() {
		entry, _ := r.Spec.Artifacts.Get(key)
		out.Set(key, entry)
	}
	for _, key := range r.Spec.Boundary.Artifacts.Keys() {
		entry, _ := r.Spec.Boundary.Artifacts.Get(key)
		out.Set(BoundaryArtifactPinName(key), entry)
	}
	return *out
}

// artifactImportLoadError reports an artifacts entry whose $import: names a file
// the pin step could not acquire, as the load failure it is.
//
// It names a condition this package does not declare, because the failure is not
// specific to the pin step: the same file is read again when the entry is
// rendered, and the same authoring mistake on a valuesMapping or a workload
// import is the same failure with the same remedy. One identity covers them, and
// each raise path supplies the clause saying where it happened.
//
// The diagnostic it builds is the canonical envelope rather than a wrapped
// string, and that is what lets the failure reach the reader with a head at all:
// the boundary that renders a halt prints a bracketed head for an error that is
// or wraps the envelope and falls through to plain single-line text for one that
// is not. Composing through the condition's registered sentence keeps that
// boundary from printing the sentence a second time beneath the diagnostic.
//
// Its clause names what the author wrote — the entity, the artifacts key, the
// imported path — and nothing about the packages that read them, so a reader is
// shown the vocabulary of the file they would open to fix it.
//
// Its advice is [types.ImportFragmentAdvice], the same sentence the render's own
// raise path attaches, and it is carried here for the reason the pin step exists:
// editing an artifacts `$import:` invalidates the pin the entity's lock records,
// so the author who mistyped one reaches the pin step before the render and
// would otherwise be the one reader of the three who is told nothing. The
// sentence has one home rather than a copy here, so the two paths cannot come to
// say different things about one mistake.
func artifactImportLoadError(path string, cause error, entityKind, entityName, artifactName string) error {
	condition := resolver.ErrYAMLLoad()
	return cerrors.Wrap(
		condition,
		cerrors.CatalogError,
		cerrors.SourceLocation{File: path},
		cerrors.ComposeMessage(condition, []string{
			fmt.Sprintf("%s %q artifact %q file import", entityKind, entityName, artifactName),
			cause.Error(),
		}, types.ImportFragmentAdvice),
	)
}

// MatchSourcePin returns the recorded source pin for the identity
// {kind, url, chart, moduleSubdir, version}, and whether one is present.
func MatchSourcePin(pins []types.SourceDigest, kind, url, chart, moduleSubdir, version string) (types.SourceDigest, bool) {
	for _, p := range pins {
		if p.Kind == kind && p.URL == url && p.Chart == chart &&
			p.ModuleSubdir == moduleSubdir && p.Version == version {
			return p, true
		}
	}
	return types.SourceDigest{}, false
}

// MatchArtifactPin returns the recorded artifact pin for the artifact key name,
// and whether one is present.
func MatchArtifactPin(pins []types.ArtifactDigest, name string) (types.ArtifactDigest, bool) {
	for _, p := range pins {
		if p.Name == name {
			return p, true
		}
	}
	return types.ArtifactDigest{}, false
}

func sortSourcePins(p []types.SourceDigest) {
	sort.Slice(p, func(i, j int) bool {
		if p[i].Kind != p[j].Kind {
			return p[i].Kind < p[j].Kind
		}
		if p[i].URL != p[j].URL {
			return p[i].URL < p[j].URL
		}
		if p[i].Chart != p[j].Chart {
			return p[i].Chart < p[j].Chart
		}
		if p[i].ModuleSubdir != p[j].ModuleSubdir {
			return p[i].ModuleSubdir < p[j].ModuleSubdir
		}
		return p[i].Version < p[j].Version
	})
}

func sortArtifactPins(p []types.ArtifactDigest) {
	sort.Slice(p, func(i, j int) bool { return p[i].Name < p[j].Name })
}
