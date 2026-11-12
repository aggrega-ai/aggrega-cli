// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package ir

import (
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
)

// ErrFeatureTypeMismatch is the fatal-halt sentinel for the
// same-instance-name / divergent-type check during Step 3 Feature
// normalization. It fires when the same Feature instance name is
// declared on multiple authoring surfaces (Aggregate component-level,
// Aggregate subsystem, Release boundary-wide, Release
// component-specific — the five cascade tiers) with conflicting
// catalog `type` URIs.
//
// The match rule is the precondition that makes the pillar-split
// merge coherent (values from the Aggregate, infra from the
// Release): same instance name across tiers must describe the same
// Feature.
//
// The check fires inside mergeFeature when an existing feature entry
// for instanceName carries a non-empty Type that disagrees with the
// incoming Type. Callers receive a wrapped error naming nodeID,
// instanceName, and both conflicting type URIs.
func ErrFeatureTypeMismatch() error { return errFeatureTypeMismatch }

var errFeatureTypeMismatch = cerrors.Register("feature-type-mismatch", cerrors.Meta{
	ShortCode: "E0013",
	Message:   "feature type mismatch between Aggregate and Release",
	RulePlace: cerrors.RuleOverAuthoredInput(),
})
