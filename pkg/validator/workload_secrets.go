// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package validator

import (
	"fmt"

	"github.com/cognitio/aggrega-cli/pkg/types"
)

// ValidateWorkloadSecrets enforces the cross-wave secretsSchema check: every
// `containers.<n>.secrets.<KEY>` and `initContainers[<i>].secrets.<KEY>`
// entry on a Workload must reference a slot declared in the owning
// Component's secretsSchema, with a delivery mechanism (`as:`) permitted by
// that slot's `inject:` allowlist. Blackbox slots (whose `inject:` is empty)
// are reserved for Recipe-internal plumbing and cannot be referenced from a
// Workload at all.
//
// Returns nil when every reference passes; otherwise returns a
// *WorkloadValidationError with one finding per offending entry.
//
// recipePath / recipeName / recipeVersion are threaded into the error for
// operator-facing message context; pass "" when not available.
func ValidateWorkloadSecrets(
	w *types.Workload,
	slots types.OrderedMap[types.SecretSlot],
	recipePath, recipeName, recipeVersion string,
) error {
	if w == nil {
		return nil
	}

	var findings []WorkloadValidationFinding

	// Containers (named, in OrderedMap author order).
	for name, c := range w.Containers.All() {
		findings = append(findings,
			checkContainerSecrets(c, slots, fmt.Sprintf(".containers.%s", name))...,
		)
	}

	// InitContainers (positional list).
	for i, c := range w.InitContainers {
		findings = append(findings,
			checkContainerSecrets(c, slots, fmt.Sprintf(".initContainers[%d]", i))...,
		)
	}

	if len(findings) == 0 {
		return nil
	}
	return &WorkloadValidationError{
		Path:          recipePath,
		RecipeName:    recipeName,
		RecipeVersion: recipeVersion,
		Stage:         "secretsSchema",
		Findings:      findings,
		Err:           ErrWorkloadSecretsSchema,
	}
}

// checkContainerSecrets walks a single ContainerSpec's secrets entries and
// produces a finding per offence. Container path is the dotted-prefix the
// caller wants prepended to each finding's FieldPath.
func checkContainerSecrets(
	c types.ContainerSpec,
	slots types.OrderedMap[types.SecretSlot],
	containerPath string,
) []WorkloadValidationFinding {
	var findings []WorkloadValidationFinding

	for key, ref := range c.Secrets.All() {
		fieldPath := fmt.Sprintf("%s.secrets.%s", containerPath, key)

		slot, slotOK := slots.Get(ref.From)
		if !slotOK {
			findings = append(findings, WorkloadValidationFinding{
				FieldPath:  fieldPath + ".from",
				Constraint: fmt.Sprintf("references slot %q which is not declared in the Component's secretsSchema", ref.From),
				Suggestion: "either declare the slot in the Component (or Feature) secretsSchema, or rename the reference to an existing slot",
			})
			continue
		}

		// Blackbox slot: inject is empty. Reserved for Recipe-internal use
		// (e.g., Helm existingSecret); must not appear in Workload secrets.
		if len(slot.Inject) == 0 {
			findings = append(findings, WorkloadValidationFinding{
				FieldPath:  fieldPath + ".from",
				Constraint: fmt.Sprintf("slot %q is blackbox (no `inject:` allowlist) and cannot be referenced from a Workload secrets entry", ref.From),
				Suggestion: "blackbox slots are delivered by Recipe-internal plumbing; remove the Workload-side reference, or widen the slot's inject allowlist if Workload-side delivery is intended",
			})
			continue
		}

		// `as:` must be in the slot's inject allowlist.
		if !contains(slot.Inject, ref.As) {
			findings = append(findings, WorkloadValidationFinding{
				FieldPath:  fieldPath + ".as",
				Constraint: fmt.Sprintf("delivery mechanism %q is not permitted by slot %q (inject allowlist: %v)", ref.As, ref.From, slot.Inject),
				Suggestion: fmt.Sprintf("use one of the allowlisted delivery mechanisms (%v), or widen the slot's inject allowlist in the Component/Feature secretsSchema", slot.Inject),
			})
		}
	}

	return findings
}

func contains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}
