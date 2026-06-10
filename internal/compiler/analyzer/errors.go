// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package analyzer

import "errors"

var (
	// ErrSelectorViolation is the ABI-stable sentinel for the
	// usesSchema-selector and Feature-appliesTo cross-node type-safety
	// checks Step 4 performs. Each violation is recorded as an
	// [AnalysisError] entry on [AnalysisResult].Errors with this sentinel
	// in its Sentinel field; [AnalysisError.ToCompilerError] wraps it as
	// the CompilerError's inner error, so the joined build error is
	// errors.Is-recoverable at the pipeline boundary. Both the selector
	// and the appliesTo checks carry this one sentinel.
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
