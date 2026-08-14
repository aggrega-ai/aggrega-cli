// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package validator

import (
	"fmt"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// ValidateWorkloadAttachments enforces the cross-entity featuresSchema check: every
// `containers.<n>.attachments.<KEY>` and `initContainers[<i>].attachments.<KEY>`
// entry on a Workload must reference a slot declared in the owning
// Component's featuresSchema, with a delivery mechanism (`as:`) permitted by
// that slot's `inject:` allowlist. Blackbox slots (whose `inject:` is empty)
// are reserved for Recipe-internal plumbing and cannot be referenced from a
// Workload at all.
//
// Returns nil when every reference passes; otherwise returns a
// *WorkloadValidationError with one finding per offending entry.
//
// This is the slot-membership and delivery-mechanism half of the workload
// attachment check. The companion ValidateWorkloadResourceReferences enforces
// that the referenced slot's fill is referenceable (its provides expose
// name/key) — a workload `containers.attachments` entry references an emitted
// resource by name, so a fill that publishes no locator cannot be referenced
// there; it is delivered through the slot's inject pipe instead.
// The two checks are distinct rules over the same input and must stay separate:
// this one reads the slot declarations, its companion reads the slots' resolved
// provides. Neither subsumes the other.
//
// recipePath / recipeName / recipeVersion are threaded into the error for
// operator-facing message context; pass "" when not available.
func ValidateWorkloadAttachments(
	w *types.Workload,
	slots types.OrderedMap[types.AttachmentSlot],
	recipePath, recipeName, recipeVersion string,
) error {
	if w == nil {
		return nil
	}

	var findings []WorkloadValidationFinding

	// Containers (named, in OrderedMap author order).
	for name, c := range w.Containers.All() {
		findings = append(findings,
			checkContainerAttachments(c, slots, fmt.Sprintf(".containers.%s", name))...,
		)
	}

	// InitContainers (positional list).
	for i, c := range w.InitContainers {
		findings = append(findings,
			checkContainerAttachments(c, slots, fmt.Sprintf(".initContainers[%d]", i))...,
		)
	}

	if len(findings) == 0 {
		return nil
	}
	return &WorkloadValidationError{
		Path:          recipePath,
		RecipeName:    recipeName,
		RecipeVersion: recipeVersion,
		Stage:         "featuresSchema",
		Findings:      findings,
		Err:           ErrWorkloadFeaturesSchema(),
	}
}

// checkContainerAttachments walks a single ContainerSpec's attachments entries and
// produces a finding per offence. Container path is the dotted-prefix the
// caller wants prepended to each finding's FieldPath.
func checkContainerAttachments(
	c types.ContainerSpec,
	slots types.OrderedMap[types.AttachmentSlot],
	containerPath string,
) []WorkloadValidationFinding {
	var findings []WorkloadValidationFinding

	for key, ref := range c.Attachments.All() {
		fieldPath := fmt.Sprintf("%s.attachments.%s", containerPath, key)

		slot, slotOK := slots.Get(ref.From)
		if !slotOK {
			findings = append(findings, WorkloadValidationFinding{
				FieldPath:  fieldPath + ".from",
				Constraint: fmt.Sprintf("references slot %q which is not declared in the Component's featuresSchema", ref.From),
				Suggestion: "either declare the slot in the Component (or Feature) featuresSchema, or rename the reference to an existing slot",
			})
			continue
		}

		// Blackbox slot: inject is empty. Reserved for Recipe-internal use
		// (e.g., Helm existingSecret); must not appear in Workload attachments.
		if len(slot.Inject) == 0 {
			findings = append(findings, WorkloadValidationFinding{
				FieldPath:  fieldPath + ".from",
				Constraint: fmt.Sprintf("slot %q is blackbox (no `inject:` allowlist) and cannot be referenced from a Workload attachments entry", ref.From),
				Suggestion: "blackbox slots are delivered by Recipe-internal plumbing; remove the Workload-side reference, or widen the slot's inject allowlist if Workload-side delivery is intended",
			})
			continue
		}

		// `as:` must be in the slot's inject allowlist.
		if !contains(slot.Inject, ref.As) {
			findings = append(findings, WorkloadValidationFinding{
				FieldPath:  fieldPath + ".as",
				Constraint: fmt.Sprintf("delivery mechanism %q is not permitted by slot %q (inject allowlist: %v)", ref.As, ref.From, slot.Inject),
				Suggestion: fmt.Sprintf("use one of the allowlisted delivery mechanisms (%v), or widen the slot's inject allowlist in the Component/Feature featuresSchema", slot.Inject),
			})
		}
	}

	return findings
}

// ValidateWorkloadResourceReferences enforces that every Workload slot
// reference names a slot whose resolved provides expose the keys the reference
// reads — `name` (the referenced resource's locator) and `key` (the data field
// within it). A slot whose fill does not publish those keys cannot be referenced
// by name from a Workload; the build halts with ErrSlotNotReferenceable.
//
// Structural key-presence only — the check never inspects the provides values.
// Provider-agnostic: the same guarantee protects every provider's synthesizer
// (the helm secretKeyRef / SecretVolumeSource reads, and any future provider).
//
// It is the referenceability half of the workload slot check; its companion
// ValidateWorkloadAttachments enforces slot membership and the
// delivery-mechanism allowlist. The two are distinct rules over the same input.
//
// attachmentProvides maps slot name → the slot's resolved provides bag, resolved by
// the orchestrator before base rendering. A slot absent from the map (no provides) or
// present but missing a key fails the same way. recipePath / recipeName /
// recipeVersion thread into the error for operator-facing context.
func ValidateWorkloadResourceReferences(
	w *types.Workload,
	attachmentProvides map[string]types.OrderedMap[string],
	recipePath, recipeName, recipeVersion string,
) error {
	if w == nil {
		return nil
	}

	var findings []WorkloadValidationFinding

	for name, c := range w.Containers.All() {
		findings = append(findings,
			checkContainerReferenceability(c, attachmentProvides, fmt.Sprintf(".containers.%s", name))...,
		)
	}
	for i, c := range w.InitContainers {
		findings = append(findings,
			checkContainerReferenceability(c, attachmentProvides, fmt.Sprintf(".initContainers[%d]", i))...,
		)
	}

	if len(findings) == 0 {
		return nil
	}
	return &WorkloadValidationError{
		Path:          recipePath,
		RecipeName:    recipeName,
		RecipeVersion: recipeVersion,
		Stage:         "slotReferenceability",
		Findings:      findings,
		Err:           ErrSlotNotReferenceable(),
	}
}

// checkContainerReferenceability walks a ContainerSpec's attachments entries and
// produces a finding per entry whose slot's resolved provides is missing a key
// a workload reference reads. Which provides keys are required depends on the
// entry:
//
//   - When the entry names an explicit data key (its `key` field is set), only
//     the slot's resource locator (provides `name`) must be published — the
//     entry supplies the data key itself, so it may reference a multi-key
//     resource whose provides publishes only the locator.
//   - When the entry names no explicit key, it reads the slot's single
//     published data key, so both provides `name` and `key` must be present.
//
// An attachment's Feature declares the keys its consumers read; a fill lacking
// a required key is not referenceable regardless of the entry's delivery
// mechanism.
func checkContainerReferenceability(
	c types.ContainerSpec,
	attachmentProvides map[string]types.OrderedMap[string],
	containerPath string,
) []WorkloadValidationFinding {
	var findings []WorkloadValidationFinding

	for key, ref := range c.Attachments.All() {
		fieldPath := fmt.Sprintf("%s.attachments.%s", containerPath, key)
		provides := attachmentProvides[ref.From]

		want := []string{"name", "key"}
		reads := "name and key"
		if ref.Key != "" {
			want = []string{"name"}
			reads = "name"
		}

		for _, need := range want {
			if _, ok := provides.Get(need); !ok {
				findings = append(findings, WorkloadValidationFinding{
					FieldPath:  fieldPath + ".from",
					Constraint: fmt.Sprintf("slot %q is not referenceable: its resolved provides does not expose %q (a workload attachment reference reads the provides keys %s)", ref.From, need, reads),
					Suggestion: "reference a slot filled by an attachment whose schema.provides declares the keys this reference reads, or deliver it through the slot's inject pipe rather than a workload reference",
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
