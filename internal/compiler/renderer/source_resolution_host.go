// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//go:build !wasip1

package renderer

import (
	"fmt"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/parse"
	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/recipepin"
	"github.com/aggrega-ai/aggrega-cli/pkg/sourcecache"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// lockfileSourceRecord returns the lockfile source-artifact record for the
// identity triple {kind, url, version}, and whether one is present.
func lockfileSourceRecord(lf *lockfile.Lockfile, kind, url, version string) (lockfile.SourceArtifact, bool) {
	if lf == nil {
		return lockfile.SourceArtifact{}, false
	}
	for _, sa := range lf.SourceArtifacts {
		if sa.Kind == kind && sa.URL == url && sa.PinnedVersion == version {
			return sa, true
		}
	}
	return lockfile.SourceArtifact{}, false
}

// resolveSourceForNode populates nodeCtx.Source and
// nodeCtx.OriginalSource per the build-time source-artifact contract.
//
// Behaviour by case:
//
//   - Sourceless Recipe (recipe == nil OR recipe.Spec.Source == nil):
//     both Source and OriginalSource stay nil. Engines see today's
//     sourceless contract.
//   - Local-string source (./..., /..., file://...): both Source and
//     OriginalSource set to the verbatim string. Tier-1 / Tier-2
//     paths are never lockfile-pinned.
//   - Remote source + lockfile/workspace/cacheDir all set on input:
//     evaluates source via closure.EvaluateSourceSpec, looks up the
//     lockfile entry by {kind, url, version}, verifies the cached
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
func resolveSourceForNode(input RendererInput, recipe *types.Recipe, nodeCtx *NodeRenderContext) error {
	if recipe == nil || recipe.Spec.Source == nil {
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
		input, recipe.Spec.Source, recipe.Spec.Locals, uri, values, infra, recipe.Spec.SourceDigests,
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
// render sites directly). A local/inline source forwards verbatim; a remote source
// with lockfile + workspaceRoot + cacheDir all set is evaluated to a
// {kind, url, version} identity and resolved through the lockfile-pin cache extract;
// with any of those inputs unset it forwards verbatim (preserving the pre-lockfile
// path). infra is the resolving instance's per-instance infra; uri is the entity URI
// for the source-spec evaluation. Returns the resolved source (extracted cache path,
// or the verbatim source) and nil when the source evaluates to nil.
//
// Wrapping fetch errors into the FETCH_ERROR category is the caller's concern via
// wrapSourceResolutionError, so both call sites surface fetch failures identically.
func resolveEntitySource(input RendererInput, source any, locals types.OrderedMap[string], uri string, values, infra map[string]any, srcPins []types.SourceDigest) (any, error) {
	if source == nil {
		return nil, nil
	}
	if isLocalSource(source) {
		return source, nil
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
	// Build-verify: the recipe's own recorded source pin must agree with the
	// lockfile's source-artifact record for the same {kind, url, version}. Both
	// derive from one resolution, so a disagreement is an integrity fault; the
	// cache-extract below independently verifies the cached bytes against the
	// lockfile digest, so this recipe-vs-lockfile check completes the chain
	// (recipe pin == lockfile record == cached bytes). A recipe with no recorded
	// pin (never frozen) has nothing to cross-check and proceeds on the lockfile.
	if pin, ok := recipepin.MatchSourcePin(srcPins, spec.Kind, spec.URL, spec.Version); ok {
		if lfrec, found := lockfileSourceRecord(input.Lockfile, spec.Kind, spec.URL, spec.Version); found {
			if pin.Digest != lfrec.Digest || pin.ManifestDigest != lfrec.ManifestDigest {
				return nil, fmt.Errorf("source-artifact %s@%s: recipe pin (digest %s) disagrees with lockfile record (digest %s): %w",
					spec.URL, spec.Version, pin.Digest, lfrec.Digest, oci.ErrFetchIntegrity)
			}
		}
	}
	extracted, err := sourcecache.LookupAndExtract(
		input.Lockfile,
		spec.Kind, spec.URL, spec.Version,
		input.WorkspaceRoot, input.CacheDir,
	)
	if err != nil {
		return nil, fmt.Errorf("source-artifact %s@%s: %w", spec.URL, spec.Version, err)
	}
	return extracted, nil
}
