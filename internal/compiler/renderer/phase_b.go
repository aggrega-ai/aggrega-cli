// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"github.com/cognitio/aggrega-cli/pkg/types"
)

// ExecutePhaseB is the legacy entry point for Phase B's valuesMapping
// evaluation. Production renders no longer call it — the orchestrator
// runs the sub-pass pipeline (phaseB0 → Phase A → phaseB1 → ...) directly
// per WI04.
//
// Retained for the dedicated phase B unit tests in phase_b_test.go and
// number_shape_test.go that exercise yaml-decode edge cases (block
// scalars, numeric-shape drift between yaml.v3 and encoding/json,
// JSON-shape parity). The body delegates to phaseB1 so behaviour and
// edge cases stay aligned with the production sub-pass.
func ExecutePhaseB(ctx *types.TemplateContext, recipe *types.Recipe) (map[string]interface{}, error) {
	state := &PhaseBState{Recipe: recipe}
	if err := phaseB1(ctx, state); err != nil {
		return nil, err
	}
	return state.ResolvedValues, nil
}
