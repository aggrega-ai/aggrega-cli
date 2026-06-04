// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// ExecutePhaseB is the legacy entry point for Phase B's valuesMapping
// evaluation. Production renders no longer call it — the orchestrator
// runs the sub-pass pipeline (phaseB0 → Phase A → phaseB1 → ...) directly.
//
// Retained for the dedicated phase B unit tests in phase_b_test.go and
// number_shape_test.go that exercise yaml-decode edge cases (block
// scalars, numeric-shape drift between yaml.v3 and encoding/json,
// JSON-shape parity). The body delegates to phaseB1 so behaviour and
// edge cases stay aligned with the production sub-pass. mappingLibs is the
// resolved MappingLibrary set used to resolve a bare-name library $import; a
// nil map is fine for the inline / file-import cases the unit tests exercise.
func ExecutePhaseB(ctx *types.TemplateContext, recipe *types.Recipe, mappingLibs map[string]types.MappingLibrary) (map[string]any, error) {
	state := &PhaseBState{Recipe: recipe, MappingLibraries: mappingLibs}
	if err := phaseB1(ctx, state); err != nil {
		return nil, err
	}
	return state.ResolvedValues, nil
}
