// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"errors"
	"fmt"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/sourcecache"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

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
	resolved, err := resolveEntitySource(
		input, recipe.Spec.Source, recipe.Spec.Locals, uri,
		nodeBoundaryInfra(input, nodeCtx.NodeID),
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
func resolveEntitySource(input RendererInput, source any, locals types.OrderedMap[string], uri string, infra map[string]any) (any, error) {
	if source == nil {
		return nil, nil
	}
	if isLocalSource(source) {
		return source, nil
	}
	if input.Lockfile == nil || input.WorkspaceRoot == "" || input.CacheDir == "" {
		return source, nil
	}
	spec, err := closure.EvaluateSourceSpecFields(infra, source, locals, uri)
	if err != nil {
		return nil, err
	}
	if spec == nil {
		return nil, nil
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

// frSourceURI is the FeatureRecipe analogue of the recipe URI used for source-spec
// evaluation: metadata.name, optionally suffixed with :version.
func frSourceURI(fr *types.FeatureRecipe) string {
	uri := fr.Metadata.Name
	if fr.Metadata.Version != "" {
		uri = uri + ":" + fr.Metadata.Version
	}
	return uri
}

// wrapSourceResolutionError categorises a source-resolution error: fetch sentinels
// route to FETCH_ERROR with an appended remediation pointer (unless the chain already
// carries one); everything else routes to TRANSLATION_ERROR. Shared by the component
// node loop and the FeatureRecipe render sites so both surface source-fetch failures
// identically. label is the leading "Step 5 node %q ... source resolution" message.
func wrapSourceResolutionError(err error, nodeID, label string) error {
	category := cerrors.TranslationError
	message := label
	if sentinel, ok := matchFetchSentinel(err); ok {
		category = cerrors.FetchError
		if !strings.Contains(err.Error(), "remediation:") {
			if rem := oci.RemediationFor(sentinel, hostFromSourceErr(err)); rem != "" {
				message = message + "; " + rem
			}
		}
	}
	return cerrors.Wrap(err, category, cerrors.SourceLocation{File: nodeID}, message)
}

// isLocalSource returns true when source is a scalar URI naming a
// Tier-1 (filesystem) or Tier-2 (workspace-relative) reference, or
// when the structured form's url field is local-shaped. Mirrors the
// classifier in closure.recipe_source_descent so lock-time and
// build-time treat the same paths as local.
func isLocalSource(source any) bool {
	switch s := source.(type) {
	case string:
		return isLocalURL(s)
	case map[string]any:
		urlVal, _ := s["url"].(string)
		return isLocalURL(urlVal)
	default:
		return false
	}
}

func isLocalURL(s string) bool {
	if s == "" {
		return false
	}
	switch {
	case strings.HasPrefix(s, "oci://"),
		strings.HasPrefix(s, "https://"),
		strings.HasPrefix(s, "http://"),
		strings.HasPrefix(s, "git+ssh://"),
		strings.HasPrefix(s, "git://"),
		strings.HasPrefix(s, "ssh://"),
		strings.HasSuffix(s, ".git"):
		return false
	default:
		return true
	}
}

// nodeBoundaryInfra returns the per-component, cascade-resolved infra
// for the node identified by nodeID, projected to a plain map for
// source-template evaluation. The resolver wires this per-component bag
// (keyed by component instance name) during Step 1; the source template
// resolves against the same infra the lock-time descent used for that
// component.
//
// Returns an empty map when the resolve result or the node's entry is
// absent — a source template that references an unresolved .infra key
// then halts with the missingkey=error template setting, which is the
// build-time-drift signal (regenerate the lockfile when the component's
// infra changes).
func nodeBoundaryInfra(input RendererInput, nodeID string) map[string]any {
	if input.ResolveResult == nil {
		return map[string]any{}
	}
	bi, ok := input.ResolveResult.BoundaryInfra[nodeID]
	if !ok || bi == nil {
		return map[string]any{}
	}
	return bi.Infra.TemplateAlphabeticalMapDeep()
}

// matchFetchSentinel walks the error chain via errors.Is against each
// of the four fetch sentinels declared at pkg/oci/errors.go. Returns
// the matched sentinel + true on first hit; returns (nil, false)
// when the chain carries no fetch sentinel.
//
// The orchestrator uses the matched sentinel to (1) route the
// CompilerError through cerrors.FetchError instead of
// cerrors.TranslationError and (2) call oci.RemediationFor to append
// the operator-actionable remediation when the underlying chain
// doesn't already carry one (e.g., build-time errors from
// pkg/sourcecache that didn't pass through sourceresolver.Wrap).
func matchFetchSentinel(err error) (error, bool) {
	switch {
	case errors.Is(err, oci.ErrFetchNotFound):
		return oci.ErrFetchNotFound, true
	case errors.Is(err, oci.ErrFetchUnauthorized):
		return oci.ErrFetchUnauthorized, true
	case errors.Is(err, oci.ErrFetchNetworkUnreachable):
		return oci.ErrFetchNetworkUnreachable, true
	case errors.Is(err, oci.ErrFetchIntegrity):
		return oci.ErrFetchIntegrity, true
	default:
		return nil, false
	}
}

// hostFromSourceErr is a best-effort extractor for the registry
// host substring inside a source-resolution error chain. The
// orchestrator passes it to oci.RemediationFor for build-time
// errors that didn't pass through sourceresolver.Wrap (which would
// otherwise have done the extraction at the lock-time side).
//
// The current implementation scans the error message for a
// "url=<URL>" segment (the format sourceresolver.Wrap emits) and
// returns the host portion of the matched URL. When no URL segment
// is found — typical of pure sourcecache errors — returns "" so
// RemediationFor uses the placeholder.
func hostFromSourceErr(err error) string {
	if err == nil {
		return ""
	}
	msg := err.Error()
	idx := strings.Index(msg, "url=")
	if idx < 0 {
		return ""
	}
	rest := msg[idx+len("url="):]
	end := strings.IndexAny(rest, " \t")
	if end > 0 {
		rest = rest[:end]
	}
	for _, scheme := range []string{"oci://", "https://", "http://", "git+ssh://", "git://", "ssh://"} {
		if strings.HasPrefix(rest, scheme) {
			body := strings.TrimPrefix(rest, scheme)
			if slash := strings.Index(body, "/"); slash > 0 {
				return body[:slash]
			}
			return body
		}
	}
	return ""
}
