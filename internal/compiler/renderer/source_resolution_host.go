// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//go:build !wasip1

package renderer

import (
	"fmt"
	"path/filepath"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/parse"
	"github.com/aggrega-ai/aggrega-cli/internal/sourceref"
	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/recipepin"
	"github.com/aggrega-ai/aggrega-cli/pkg/sourcecache"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// resolveSourceForNode populates nodeCtx.Source and
// nodeCtx.OriginalSource per the build-time source-artifact contract.
//
// Behaviour by case:
//
//   - Sourceless Recipe (no Recipe, or one carrying no source):
//     both Source and OriginalSource stay nil. Engines see today's
//     sourceless contract. Whether a Recipe carries a source is asked
//     of recipeSourceDeclared, which is the one home for that question,
//     so this arm and every other reader of it answer alike.
//   - Local source (./..., /..., file://...), authored as a scalar reference or
//     as a structured block: Source is set to the reference the source names, and
//     OriginalSource to the value as authored. Local references are never
//     lockfile-pinned; where the lock beside the entity records pins for the
//     source's files, the files are compared with them first.
//   - Remote source + lockfile/workspace/cacheDir all set on input:
//     evaluates source via closure.EvaluateSourceSpec, looks up the
//     lockfile entry by its location terms plus what the author wrote to
//     select a version — the declared ref where there is one, the declared
//     version otherwise — verifies the cached
//     archive's full digest, regenerates the extracted/ directory,
//     and populates Source with the extracted path + OriginalSource
//     with the verbatim spec.
//   - Remote source + any of lockfile/workspace/cacheDir unset:
//     falls back to verbatim forward — Source is the recipe's
//     spec.source value as authored. This keeps callers that do not
//     configure the optional source-resolution inputs on the verbatim
//     path.
//
// The source template evaluates against the node's own per-component,
// cascade-resolved infra — the same per-component bag the lock-time
// descent resolved against — so the build-time source identity matches
// the identity the lockfile pinned for that component.
//
// Returns wrapped oci.ErrFetchIntegrity when a remote source has no
// matching lockfile entry (build-time drift signal). Returns the
// wrapped error from sourcecache.LookupAndExtract on digest mismatch,
// missing cache slot, or extract failure.
// documentPath is the file this node's Recipe was declared in, and it is the key
// the recorded-pin comparison below reads its pins under. It is supplied by the
// caller rather than looked up here because the lookup that answers it is the
// caller's own, and a Recipe resolved from a registry passes the empty string,
// which records nothing and leaves that comparison with nothing to conclude.
//
// origin is the source as the Recipe's author wrote it and the set its templates
// were evaluated against, so a refusal naming a coordinate a template computed from
// a declared value reports without it.
func resolveSourceForNode(input RendererInput, recipe *types.Recipe, documentPath string, nodeCtx *NodeRenderContext, origin sourceOrigin) error {
	if !recipeSourceDeclared(recipe) {
		return nil
	}
	nodeCtx.OriginalSource = recipe.Spec.Source
	uri := recipe.Metadata.Name
	if recipe.Metadata.Version != "" {
		uri = uri + ":" + recipe.Metadata.Version
	}
	var values, infra map[string]any
	if input.Graph != nil {
		values = parse.NodePayloadMap(input.Graph.Spec.ValuesMapping, nodeCtx.NodeID)
		infra = parse.NodePayloadMap(input.Graph.Spec.InfraMapping, nodeCtx.NodeID)
	}
	resolved, err := resolveEntitySource(
		input, recipe.Spec.Source, recipe.Spec.Locals, uri, values, infra, documentPath, origin,
	)
	if err != nil {
		return err
	}
	nodeCtx.Source = resolved
	nodeCtx.SourceIgnore = localSourceIgnore(input, recipe.Spec.Source, documentPath)
	return nil
}

// localSourceIgnore returns what a render reads the local source declared in the
// document at documentPath through: the ignore rules the lock beside it records,
// read from the render input's record of what the load settled for that
// document, and the directory holding the document, which the rules are spelled
// against. A remote source, and an entity declared in no document on disk, read
// through no rule — a fetched source's identity is the archive it was fetched as,
// and a published entity's files are already the ones its publisher's rules kept.
//
// It is the one place a render turns the recorded rules into the reading a
// provider is handed, so what a render reads and what a publication of the same
// entity carries come from one record.
func localSourceIgnore(input RendererInput, source any, documentPath string) SourceIgnore {
	if source == nil || documentPath == "" || !sourceref.IsLocal(source) {
		return SourceIgnore{}
	}
	dir, err := filepath.Abs(filepath.Dir(documentPath))
	if err != nil {
		return SourceIgnore{}
	}
	return SourceIgnore{
		EntityDirectory: dir,
		Rules:           recipepin.IgnoreRulesOf(input.RecordedPins.For(documentPath).Ignore),
	}
}

// resolveEntitySource resolves one entity's source — a component Recipe's or a
// FeatureRecipe's — to the value an engine consumes, through ONE body both call
// sites share (the component node loop via resolveSourceForNode, the FeatureRecipe
// render sites directly). A local source forwards the reference it names, read out
// of whichever shape the source was authored in; a remote source
// with lockfile + workspaceRoot + cacheDir all set is evaluated to its authored
// coordinates and resolved through the lockfile-pin cache extract;
// with any of those inputs unset it forwards verbatim (preserving the pre-lockfile
// path). infra is the resolving instance's per-instance infra; uri is the entity URI
// for the source-spec evaluation. Returns the resolved source (extracted cache path,
// or the verbatim source) and nil when the source evaluates to nil.
//
// documentPath is the file the entity was declared in, and it is the only thing
// this body needs in order to read the source pins recorded for that entity: the
// pins travel on the render input keyed by document, so every caller supplies the
// key rather than the pins and there is one place that turns one into the other.
//
// Wrapping fetch errors into the FETCH_ERROR category is the caller's concern via
// wrapSourceResolutionError, so both call sites surface fetch failures identically.
//
// origin is the source as its author wrote it and the set its templates were
// evaluated against. A refusal raised on the remote path names the coordinates the
// evaluation produced, so where a template produced one by reading a value an author
// declared sensitive the refusal is reported without it — see
// sourceOrigin.withholdRefusal, which every such refusal below passes through. A
// refusal naming no such coordinate is returned exactly as it was raised.
func resolveEntitySource(input RendererInput, source any, locals types.OrderedMap[string], uri string, values, infra map[string]any, documentPath string, origin sourceOrigin) (any, error) {
	if source == nil {
		return nil, nil
	}
	if sourceref.IsLocal(source) {
		// The files a provider is about to read are compared with the pins the lock
		// beside the entity records for them first, so a source edited after it was
		// pinned halts here rather than rendering unverified.
		if err := checkLocalSourcePins(input, documentPath); err != nil {
			return nil, err
		}
		// What an engine consumes is the path, so what is forwarded is the
		// reference rather than the value that carries it. The two coincide for a
		// scalar source and do not for a structured one, where the value is a block
		// of authored fields and only its url names a place on disk — forwarding the
		// block reached the dispatch as a shape it refuses, naming a Go type at a
		// user who wrote a directory. The reference is non-empty here by
		// construction: the locality reading answers over the same projection, and
		// an empty reference is not local.
		ref, _ := sourceref.Reference(source)
		return ref, nil
	}
	if input.Lockfile == nil || input.WorkspaceRoot == "" || input.CacheDir == "" {
		return source, nil
	}
	spec, err := closure.EvaluateSourceSpecFields(values, infra, source, locals, uri)
	if err != nil {
		// Raised before any coordinate exists, over whichever part of the source it
		// could not read — so every identity part is one its text can quote.
		return nil, origin.withholdRefusal(err, "", "", "", sourceIdentityParts...)
	}
	if spec == nil {
		return nil, nil
	}
	// Name the source by whichever coordinate its author actually wrote. A source
	// declared only at a `ref:` has no version to print, and "@" with nothing after
	// it reads as a missing value rather than as a source identified a different way.
	at, atPart := spec.Version, "version"
	if at == "" {
		at, atPart = spec.Ref, "ref"
	}
	// Build-verify: where the source pin recorded for this entity and the lockfile's
	// source-artifact record both describe the identity this node just resolved,
	// they must agree. An entity frozen alongside the lockfile records both from one
	// resolution, so a disagreement between them is an integrity fault; the
	// cache-extract below independently verifies the cached bytes against the
	// lockfile digest, so agreement here completes the chain (recorded pin ==
	// lockfile record == cached bytes). The comparison and its diagnostic are
	// recipepin.CheckSourcePin's, the one home a verify of the same lock calls too.
	//
	// The pin is read from the render input's record of what the load settled for
	// this entity's document, never from the entity value in hand. The two would be
	// one fact with two answers, and the entity's own surface is the authored one:
	// what a build compares against is what the resolution measured, which is
	// recorded beside the document rather than inside it.
	//
	// 🔴 The comparison is scoped to what both records cover, and the scope is
	// narrower than "every build". A pull records a pin beside an entity only for a
	// source whose identity its author wrote out in full, so an entity whose source
	// identity is filled in from a template holds none. An entity never frozen at
	// all holds none either, and so does one whose lock came from some other
	// resolution than this workspace's, for any identity that resolution did not
	// reach. An entity declared in no document on disk lands there too: it has no
	// directory a lock could sit beside, so nothing was recorded under it. Such a
	// build proceeds on the lockfile, which is the record the fetch itself reads.
	// The lockfile side needs no guard of its own: the cache lookup below asks the
	// same question of the same record, and a build whose identity the lockfile
	// does not pin halts there rather than here.
	//
	// The authored reference is passed here as well as to the fetch below, and the
	// two must stay identical: the lookup answers which row describes this
	// artifact, so asking it two different questions would decide whether the
	// comparison runs rather than what it concludes.
	srcPins := input.RecordedPins.For(documentPath).SourceDigests
	if err := recipepin.CheckSourcePin(srcPins, input.Lockfile, recipepin.LockPath(documentPath),
		spec.Kind, spec.URL, spec.Chart, spec.ModuleSubdir, spec.Version, spec.Ref); err != nil {
		// The disagreement names the source by its url and the coordinate it was
		// asked at; the digests it states beside them are the records' own.
		return nil, origin.withholdRefusal(err, spec.URL, at, atPart, "url", atPart)
	}
	extracted, err := sourcecache.LookupAndExtract(
		input.Lockfile,
		spec.Kind, spec.URL, spec.Chart, spec.ModuleSubdir, spec.Version, spec.Ref,
		input.WorkspaceRoot, input.CacheDir,
	)
	if err != nil {
		// The lookup's own text names every coordinate it looked up, and a cache
		// path is built from them too, so any identity part is one it can quote.
		return nil, origin.withholdRefusal(fmt.Errorf("source-artifact %s@%s: %w", spec.URL, at, err),
			spec.URL, at, atPart, sourceIdentityParts...)
	}
	return extracted, nil
}

// checkLocalSourcePins compares the files of the local source declared in the
// document at documentPath with the pins the lock beside it records for them, and
// returns the halt a disagreement raises.
//
// Both halves are read rather than computed here. The recorded pins are the
// render input's record of what the load settled for this document, like the
// remote source's pins above. The current ones are the catalog's answer to which
// files the source contributes — the answer a publication packs and the pin step
// pins — so a build cannot come to compare a different set of files from the one
// that was pinned. The comparison and its diagnostic are
// recipepin.CheckLocalSourcePins's, the one home a verify of the same lock calls
// too.
//
// A lock recording no local source pin leaves nothing to compare, which is the
// ordinary answer for an entity nobody pinned and for one declared in no document
// on disk — a registry-resolved entity, whose publication is what covers its
// files. A lock that records some while the render has no way to read the source
// as it stands is refused rather than admitted: passing it would read exactly
// like a source that had not moved.
func checkLocalSourcePins(input RendererInput, documentPath string) error {
	recorded := input.RecordedPins.For(documentPath).LocalSourceDigests
	if len(recorded) == 0 {
		return nil
	}
	if input.CatalogLookup.LocalSourcePins == nil {
		return cerrors.Unidentified(cerrors.CatalogError, cerrors.SourceLocation{File: documentPath},
			"the local source could not be checked against the pins recorded for it beside the entity — "+
				"internal error: the render was given no way to read the source's files. This is a fault in the "+
				"compiler rather than in the entity, so there is nothing to correct in the catalog", nil, nil)
	}
	current, err := input.CatalogLookup.LocalSourcePins(documentPath)
	if err != nil {
		return err
	}
	return recipepin.CheckLocalSourcePins(documentPath, recorded, current)
}
