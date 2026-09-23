// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//go:build !wasip1

package renderer

import (
	"fmt"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/parse"
	"github.com/aggrega-ai/aggrega-cli/internal/sourceref"
	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
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
//     lockfile-pinned.
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
func resolveSourceForNode(input RendererInput, recipe *types.Recipe, documentPath string, nodeCtx *NodeRenderContext) error {
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
		input, recipe.Spec.Source, recipe.Spec.Locals, uri, values, infra, documentPath,
	)
	if err != nil {
		return err
	}
	nodeCtx.Source = resolved
	return nil
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
func resolveEntitySource(input RendererInput, source any, locals types.OrderedMap[string], uri string, values, infra map[string]any, documentPath string) (any, error) {
	if source == nil {
		return nil, nil
	}
	if sourceref.IsLocal(source) {
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
		return nil, err
	}
	if spec == nil {
		return nil, nil
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
		return nil, err
	}
	extracted, err := sourcecache.LookupAndExtract(
		input.Lockfile,
		spec.Kind, spec.URL, spec.Chart, spec.ModuleSubdir, spec.Version, spec.Ref,
		input.WorkspaceRoot, input.CacheDir,
	)
	if err != nil {
		// Name the source by whichever coordinate its author actually wrote. A
		// source declared only at a `ref:` has no version to print, and "@" with
		// nothing after it reads as a missing value rather than as a source
		// identified a different way.
		at := spec.Version
		if at == "" {
			at = spec.Ref
		}
		return nil, fmt.Errorf("source-artifact %s@%s: %w", spec.URL, at, err)
	}
	return extracted, nil
}
