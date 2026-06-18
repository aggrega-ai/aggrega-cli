// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package validator

import (
	"fmt"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
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
// This is the slot-membership and delivery-mechanism half of the workload
// secret check. The companion ValidateWorkloadSecretReferenceability enforces
// that the referenced slot's fill is a referenceable secret (its provides
// expose name/key) — a workload `containers.secrets` entry references a Secret
// resource by name, so a non-referenceable (inject-delivered) credential cannot
// be referenced there; it is delivered through the slot's inject pipe instead.
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

// ValidateWorkloadSecretReferenceability enforces that every Workload secrets
// entry references a slot whose resolved provides expose the keys a workload
// reference reads — `name` (the generated secret resource's locator) and `key`
// (the data field within it). A slot whose fill is not a referenceable secret
// (its provides lacks those keys) cannot be referenced by name from a Workload;
// the build halts with ErrSecretSlotNotReferenceable.
//
// Structural key-presence only — the check never inspects the provides values.
// Provider-agnostic: the same guarantee protects every provider's synthesizer
// (the helm secretKeyRef / SecretVolumeSource reads, and any future provider).
//
// secretProvides maps slot name → the slot's resolved provides bag, resolved by
// the orchestrator before Phase B. A slot absent from the map (no provides) or
// present but missing a key fails the same way. recipePath / recipeName /
// recipeVersion thread into the error for operator-facing context.
func ValidateWorkloadSecretReferenceability(
	w *types.Workload,
	secretProvides map[string]types.OrderedMap[string],
	recipePath, recipeName, recipeVersion string,
) error {
	if w == nil {
		return nil
	}

	var findings []WorkloadValidationFinding

	for name, c := range w.Containers.All() {
		findings = append(findings,
			checkContainerReferenceability(c, secretProvides, fmt.Sprintf(".containers.%s", name))...,
		)
	}
	for i, c := range w.InitContainers {
		findings = append(findings,
			checkContainerReferenceability(c, secretProvides, fmt.Sprintf(".initContainers[%d]", i))...,
		)
	}

	if len(findings) == 0 {
		return nil
	}
	return &WorkloadValidationError{
		Path:          recipePath,
		RecipeName:    recipeName,
		RecipeVersion: recipeVersion,
		Stage:         "secretReferenceability",
		Findings:      findings,
		Err:           ErrSecretSlotNotReferenceable,
	}
}

// checkContainerReferenceability walks a ContainerSpec's secrets entries and
// produces a finding per entry whose slot's resolved provides is missing a key
// a workload reference reads. The contract keys are fixed (name, key) — a
// referenceable-secret Feature declares both, so a fill lacking either is not
// referenceable regardless of the entry's delivery mechanism.
func checkContainerReferenceability(
	c types.ContainerSpec,
	secretProvides map[string]types.OrderedMap[string],
	containerPath string,
) []WorkloadValidationFinding {
	var findings []WorkloadValidationFinding

	for key, ref := range c.Secrets.All() {
		fieldPath := fmt.Sprintf("%s.secrets.%s", containerPath, key)
		provides := secretProvides[ref.From]
		for _, want := range []string{"name", "key"} {
			if _, ok := provides.Get(want); !ok {
				findings = append(findings, WorkloadValidationFinding{
					FieldPath:  fieldPath + ".from",
					Constraint: fmt.Sprintf("slot %q is not a referenceable secret: its resolved provides does not expose %q (a workload secret reference reads the provides keys name and key)", ref.From, want),
					Suggestion: "reference a slot filled by a referenceable-secret feature (one whose schema.provides declares name and key), or deliver this secret through the slot's inject pipe rather than a workload reference",
				})
			}
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
