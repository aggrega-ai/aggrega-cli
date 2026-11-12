// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package featurerecipe_pipeline

import (
	"fmt"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// ApplyPatches applies each returned patch to the unit it addresses by scope
// and key. RFC-6902 application runs through the stock [Applier] over the
// unit's tree-view; the applier is the single home of the ABI→engine
// operation mapping (a [JSONPatchOp] becomes a jsonpatch.Op there and only
// there), so this function carries no engine import beyond the seam.
//
// docs is mutated in place: a patched unit's Tree is replaced with the
// applied result. An absent (scope, key) target returns
// [ErrPipelineTargetAbsent]; a patch against an opaque unit (the codec gave
// it no tree-view) returns [ErrPipelineTargetOpaque]; any engine rejection
// propagates wrapped. On error docs may be partially mutated; the caller's
// snapshot restore makes rollback whole-step.
//
// It is the single home of the (scope, key)-addressed apply and its
// opaque/absent target checks, shared by the step walk and by any caller that
// applies a set of pre-resolved ops to an addressable document set.
func ApplyPatches(docs *DocumentSet, ops []JSONPatchOp) error {
	var applier StockApplier
	for _, op := range ops {
		root, err := scopeRoot(docs, op.Scope)
		if err != nil {
			return err
		}

		unit, ok := root.Get(op.Key)
		if !ok {
			return fmt.Errorf("%w: scope %q key %q", ErrPipelineTargetAbsent(), op.Scope, op.Key)
		}
		if !unit.Structured() {
			return fmt.Errorf("%w: scope %q key %q", ErrPipelineTargetOpaque(), op.Scope, op.Key)
		}

		newTree, err := applier.Apply(unit.Tree, []JSONPatchOp{op})
		if err != nil {
			return fmt.Errorf("%w: scope %q key %q: %w", ErrPipelineStepFailed(), op.Scope, op.Key, err)
		}
		unit.Tree = newTree
		root.Set(op.Key, unit)
	}
	return nil
}

// scopeRoot returns the unit root a patch scope addresses. An unknown scope
// is a step failure. Host-scope availability (a host root exists only for a
// component-scope FeatureRecipe attached to a host) is the caller's concern;
// here an addressed-but-empty host root simply yields an absent-target on
// Get.
func scopeRoot(docs *DocumentSet, scope string) (*types.OrderedMap[Unit], error) {
	switch scope {
	case DocumentScopeOwn:
		return &docs.Own, nil
	case DocumentScopeHost:
		return &docs.Host, nil
	default:
		return nil, fmt.Errorf("%w: unknown patch scope %q", ErrPipelineStepFailed(), scope)
	}
}
