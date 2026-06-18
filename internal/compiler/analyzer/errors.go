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

	// ErrSecretSlotSelectorUnsatisfied is the ABI-stable sentinel for a
	// secretsSchema slot whose declared `selector` is not satisfied by the
	// labels of the credential Feature attached to fill it — the credential
	// analog of a usesSchema socket selector violation, evaluated against the
	// attached Feature's labels at Step 4.
	ErrSecretSlotSelectorUnsatisfied = errors.New("secret slot selector unsatisfied")

	// ErrSecretSlotSelectorNotSecretKind is the ABI-stable sentinel for a
	// secretsSchema slot whose declared `selector` does not constrain its fill
	// to secret-kind Features — its admitted set is not a subset of
	// {feature.aggrega.ai/kind=secret}, so it could broaden a slot back out of
	// the credential partition. A structural check on the parsed selector,
	// never a judgement of the secret's meaning.
	ErrSecretSlotSelectorNotSecretKind = errors.New("secret slot selector does not constrain to secret-kind Features")

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
