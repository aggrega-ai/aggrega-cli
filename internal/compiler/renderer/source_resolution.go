// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"errors"
	"fmt"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
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
//     spec.source value as authored. Preserves pre-WI-06e behaviour
//     so existing tests that don't configure the optional fields
//     stay green.
//
// Returns wrapped oci.ErrFetchIntegrity when a remote source has no
// matching lockfile entry (build-time drift signal per amendment §3.7).
// Returns the wrapped error from sourcecache.LookupAndExtract on
// digest mismatch, missing cache slot, or extract failure.
func resolveSourceForNode(input RendererInput, recipe *types.Recipe, nodeCtx *NodeRenderContext) error {
	if recipe == nil || recipe.Spec.Source == nil {
		return nil
	}
	originalSource := recipe.Spec.Source
	nodeCtx.OriginalSource = originalSource

	if isLocalSource(originalSource) {
		nodeCtx.Source = originalSource
		return nil
	}

	if input.Lockfile == nil || input.WorkspaceRoot == "" || input.CacheDir == "" {
		nodeCtx.Source = originalSource
		return nil
	}

	spec, err := closure.EvaluateSourceSpec(buildReleaseForInfra(input), recipe)
	if err != nil {
		return err
	}
	if spec == nil {
		nodeCtx.Source = nil
		return nil
	}

	extracted, err := sourcecache.LookupAndExtract(
		input.Lockfile,
		spec.Kind, spec.URL, spec.Version,
		input.WorkspaceRoot, input.CacheDir,
	)
	if err != nil {
		return fmt.Errorf("source-artifact %s@%s: %w", spec.URL, spec.Version, err)
	}
	nodeCtx.Source = extracted
	return nil
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

// buildReleaseForInfra returns the Release the orchestrator's
// source-template evaluator needs. In the current code the orchestrator
// doesn't carry the Release directly; the resolver-result it has
// access to (input.ResolveResult) is the post-resolved view. The
// closure.EvaluateSourceSpec wrapper accepts a nil Release —
// assembleInfra returns an empty map, and source templates that
// don't reference .infra evaluate cleanly. Templates that reference
// .infra against a nil Release halt with the missingkey=error
// template setting — matching the build-time-drift behaviour
// (regenerate the lockfile when the Release's infra changes).
//
// A future implementation will thread the loaded Release into
// RendererInput so .infra-referencing templates evaluate against
// the same Release that produced the lockfile. The current shape
// supports the structured-source acceptance walk via fixture
// Releases that don't reference .infra; the existing tests use
// literal version pins that don't need infra resolution.
func buildReleaseForInfra(input RendererInput) *types.Release {
	return nil
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
