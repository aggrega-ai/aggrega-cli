// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

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
	Release       *types.Release
	Manifest      *types.Manifest
	Aggregate     *types.Aggregate
	AggBytes      []byte
	AggregatePath string
	Closure       closure.Closure
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
	manRes, err := resolver.ResolveManifest(releaseDir, rel.Spec.Manifest)
	if err != nil {
		return nil, &jsonRPCError{
			Code:    errEntityNotResolvable,
			Message: fmt.Sprintf("load manifest %q: %v", rel.Spec.Manifest, err),
			Data:    newErrorData(releasePath, 0, 0),
		}
	}
	manifest := manRes.Manifest
	// The daemon resolves the top-level Aggregate by same-directory scan or
	// `./`/`../` file (nil catalog tier): an LSP workspace's Release sits beside
	// its Aggregate, and a catalog-resident top-level Aggregate is not part of
	// this co-located resolution.
	aggRes, err := resolver.ResolveAggregate(releaseDir, rel.Spec.Aggregate, nil)
	if err != nil {
		return nil, &jsonRPCError{
			Code:    errEntityNotResolvable,
			Message: fmt.Sprintf("load aggregate %q: %v", rel.Spec.Aggregate, err),
			Data:    newErrorData(releasePath, 0, 0),
		}
	}
	aggregate := aggRes.Aggregate
	aggBytes := aggRes.Bytes
	externals := lockfile.ExternalEntitiesFromBytes(rel, aggRes.Bytes, manRes.Bytes)
	cacheDir := s.wsConfig.Workspace.ResolvedCacheDir()
	// Lazy closure walk — no eager whole-catalog load (0090 point-7). The daemon
	// passes a nil puller (hermetic, Tier-3 disabled), matching the eager path it
	// replaces; a broken out-of-closure entity is never enumerated-fatal.
	lookup := s.lazyCatalog.ClosureLookup(nil, s.workspaceRoot, cacheDir, externals)
	cl, err := closure.ComputeClosure(rel, manifest, aggregate, s.wsConfig, lookup)
	if err != nil {
		// An in-closure entity that was skipped at the tolerant lazy enumeration
		// surfaces here as an unresolvable URI; skipContext replays WHY it went
		// missing so the diagnostic is never a bare "not found".
		return nil, &jsonRPCError{
			Code:    errEntityNotResolvable,
			Message: s.skipContext(fmt.Sprintf("closure compute: %v", err)),
		}
	}
	return &resolvedRelease{
		Release:       rel,
		Manifest:      manifest,
		Aggregate:     aggregate,
		AggBytes:      aggBytes,
		AggregatePath: aggRes.Path,
		Closure:       cl,
	}, nil
}

// skipContext annotates a closure-level message with the catalog files the lazy
// enumeration skipped (tolerantly) — so an in-closure entity that went missing
// because it was skipped is explained, not silently absent. Deterministic
// (sorted by path); no-op when nothing was skipped. Mirrors the CLI build's
// withSkipContext, sourced from the lazy backing's skip record.
func (s *Server) skipContext(msg string) string {
	skips := s.lazyCatalog.Skips()
	if len(skips) == 0 {
		return msg
	}
	paths := make([]string, 0, len(skips))
	for p := range skips {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	var b strings.Builder
	b.WriteString(msg)
	b.WriteString("; note: catalog files skipped at load (one may be the unresolved in-closure entity):")
	for _, p := range paths {
		fmt.Fprintf(&b, " %s: %v;", p, skips[p])
	}
	return b.String()
}
