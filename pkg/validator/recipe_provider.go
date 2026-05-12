// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package validator

import (
	"fmt"

	"github.com/cognitio/aggrega-cli/pkg/types"
)

// ValidateRecipeProviderBinding enforces rule 4 from chapter 16: a Recipe
// declaring `workload:` or `workloadFrom:` requires its bound Provider to
// declare `workloadSchema`.
//
// This rule cannot be enforced at Recipe load time because Provider binding
// is not yet known then — the Manifest selects the Provider per Recipe
// during Step 1's resolution sub-phase. Call this function once per
// resolved (Recipe, Provider) pair after the Manifest's two-tier lookup
// completes, before the Recipe is dispatched to its rendering pipeline.
//
// Returns nil when the rule does not apply (the Recipe declares neither
// workload nor workloadFrom) or when the Provider satisfies the requirement.
func ValidateRecipeProviderBinding(r *types.Recipe, p *types.Provider, sourcePath string) error {
	if r == nil {
		return nil
	}
	if r.Spec.Workload.IsEmpty() {
		return nil
	}
	if p != nil && providerHasWorkloadSchema(p) {
		return nil
	}

	providerName := "<unbound>"
	if p != nil {
		providerName = p.Metadata.Name
	}
	return &RecipeValidationError{
		Path:        sourcePath,
		Name:        r.Metadata.Name,
		Version:     r.Metadata.Version,
		Rule:        "4",
		Description: "workload/workloadFrom requires the bound Provider to declare workloadSchema",
		Detail:      fmt.Sprintf("Provider %q has no workloadSchema; Workload synthesis is not supported on this provider", providerName),
		Err:         ErrRecipeRule4,
	}
}

// providerHasWorkloadSchema reports whether a Provider declares a non-empty
// workloadSchema. The Provider type is consulted by Recipe rule 4 to permit
// or reject Workload-bearing Recipes for the bound provider.
func providerHasWorkloadSchema(p *types.Provider) bool {
	if p == nil {
		return false
	}
	return p.Spec.WorkloadSchema != ""
}
