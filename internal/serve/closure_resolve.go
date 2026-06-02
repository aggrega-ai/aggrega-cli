// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// resolvedRelease bundles the entities the daemon's pipeline + drift
// flows need together. Returned by resolveReleaseClosure to keep
// that function's signature ergonomic (six discrete return values
// would crowd call sites).
type resolvedRelease struct {
	Release   *types.Release
	Manifest  *types.Manifest
	Aggregate *types.Aggregate
	AggBytes  []byte
	Closure   closure.Closure
}

// resolveReleaseClosure loads the Manifest + Aggregate siblings to
// the active Release and computes the catalog closure. Used by
// handleIrGraph (existing pipeline path) AND by the at-startup +
// per-irGraph drift checks.
//
// The closure compute uses a hermetic lookup (nil OCIPuller),
// matching the CLI's verifyLockfileAgainstClosure. Tier-3 (OCI
// pull) never fires during verification; missing cache entries
// surface as closure-walk failures rather than out-of-band network
// fetches.
//
// On error returns a populated *jsonRPCError envelope (1004
// errEntityNotResolvable) so handleIrGraph can forward it via
// writeErrorEnvelope. The drift-check caller treats a non-nil
// envelope as a stderr-logged warning and skips drift verification;
// the next handleIrGraph retries.
//
// The four error sites mirror the original handleIrGraph layout:
// Release/Manifest/Aggregate load failures populate Data.File with
// the path being loaded; the recipe-catalog resolution failure
// stays Data-less because the catalog spans multiple source dirs.
func (s *Server) resolveReleaseClosure(releasePath string) (*resolvedRelease, *jsonRPCError) {
	rel, err := resolver.LoadRelease(releasePath)
	if err != nil {
		return nil, &jsonRPCError{
			Code:    errEntityNotResolvable,
			Message: fmt.Sprintf("load release %s: %v", releasePath, err),
			Data:    newErrorData(releasePath, 0, 0),
		}
	}
	releaseDir := filepath.Dir(releasePath)
	manifestPath := filepath.Join(releaseDir, rel.Spec.Manifest+".yaml")
	manifest, err := resolver.LoadManifest(manifestPath)
	if err != nil {
		return nil, &jsonRPCError{
			Code:    errEntityNotResolvable,
			Message: fmt.Sprintf("load manifest %s: %v", manifestPath, err),
			Data:    newErrorData(manifestPath, 0, 0),
		}
	}
	aggPath := filepath.Join(releaseDir, rel.Spec.Aggregate+".yaml")
	aggBytes, err := os.ReadFile(aggPath)
	if err != nil {
		return nil, &jsonRPCError{
			Code:    errEntityNotResolvable,
			Message: fmt.Sprintf("read aggregate %s: %v", aggPath, err),
			Data:    newErrorData(aggPath, 0, 0),
		}
	}
	aggregate, err := resolver.LoadAggregate(aggPath)
	if err != nil {
		return nil, &jsonRPCError{
			Code:    errEntityNotResolvable,
			Message: fmt.Sprintf("load aggregate %s: %v", aggPath, err),
			Data:    newErrorData(aggPath, 0, 0),
		}
	}
	recipeCat, err := s.resolveRecipeCatalog()
	if err != nil {
		// Catalog-load failure aggregates multiple source dirs; no
		// single file is the source of the error. Data left nil so
		// the client falls back to workspace-level positioning.
		return nil, &jsonRPCError{
			Code:    errEntityNotResolvable,
			Message: err.Error(),
		}
	}

	externals := lockfile.LoadExternalEntities(rel, aggPath, manifestPath)
	cacheDir := s.wsConfig.Workspace.ResolvedCacheDir()
	lookup := recipeCat.ClosureLookupWithExternals(nil, s.workspaceRoot, cacheDir, externals)
	cl, err := closure.ComputeClosure(rel, manifest, aggregate, s.wsConfig, lookup)
	if err != nil {
		return nil, &jsonRPCError{
			Code:    errEntityNotResolvable,
			Message: fmt.Sprintf("closure compute: %v", err),
		}
	}
	return &resolvedRelease{
		Release:   rel,
		Manifest:  manifest,
		Aggregate: aggregate,
		AggBytes:  aggBytes,
		Closure:   cl,
	}, nil
}
