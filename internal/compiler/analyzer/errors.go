// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package analyzer

import "errors"

var (
	// ErrSelectorViolation is the ABI-stable sentinel reserved for the
	// usesSchema-selector and Feature-appliesTo cross-node type-safety
	// checks Step 4 performs. The current implementation surfaces every
	// individual violation as an [AnalysisError] entry on
	// [AnalysisResult].Errors rather than wrapping this sentinel; the
	// sentinel exists for callers that prefer errors.Is-based detection
	// of the violation class and for future emission sites that may
	// elevate selector violations from structured records to wrapped
	// errors.
	ErrSelectorViolation = errors.New("selector violation")

	// ErrCatalogLookup is the ABI-stable sentinel reserved for catalog
	// lookups that fail during analyzer-phase evaluation. Today catalog
	// misses inside validateSelectors and validateAppliesTo trigger a
	// silent skip rather than a wrapped halt: Step 1's resolver guards
	// already validate that every component and feature reference an
	// Aggregate or Release names, so an analyzer-phase miss reflects an
	// internal invariant violation rather than an authoring error. The
	// sentinel ships preserved for any future emission site that needs
	// to surface that invariant violation as a fatal halt.
	ErrCatalogLookup = errors.New("catalog lookup failed")
)
