// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package wasm

import (
	"fmt"
	"sort"
)

// EntityCapabilityClaim is the authored capability declaration carried by a
// catalog Plugin entity, in the neutral shape this package compares against
// a plugin's self-reported [PluginDescriptor]. The caller maps the entity's
// exports into this shape so this package stays ignorant of pkg/types; the claim
// is the authored promise of what each export does, checked at load against the
// plugin's own describe() report.
type EntityCapabilityClaim struct {
	// Exports is the authored per-export capability claim, in author order.
	Exports []EntityExportClaim

	// Capabilities is the authored plugin-level capability claim, in the same
	// neutral shape the plugin self-reports in describe(). Nil when the entity
	// authors no plugin-level claim, in which case the cross-check compares only
	// the per-export claims.
	Capabilities *Capabilities
}

// EntityExportClaim is one export's authored capability: the export name the
// claim is keyed by, and the operates/scope fields the cross-check compares
// against the plugin's self-report. The selector is intentionally absent — it is
// free-form human-readable documentation, not a structurally cross-checkable
// claim.
type EntityExportClaim struct {
	// Name is the export's ABI name; the cross-check keys the comparison by it.
	Name string
	// Operates is the authored pipeline operation ("patch", "emit", or empty).
	Operates string
	// Scope is the authored document root ("own", "host", or empty).
	Scope string
}

// CrossCheckCapabilities verifies that an entity's authored capability claim
// agrees with the plugin's self-reported describe() descriptor, returning a
// Loud Guardrail error wrapping [ErrWasmCapabilityMismatch] on the first
// disagreement and nil when the claim is consistent.
//
// This is the single contract check comparing an authored claim against
// describe(); the load gate calls it after descriptor validation, and the
// publish path reuses the same function so the two surfaces share one home for
// the comparison rather than each re-spelling the match semantics.
//
// The comparison is per-export, keyed by export name. For each export the entity
// claims, the descriptor must declare an export of the same name; a claimed
// export the descriptor omits halts (the entity promises a capability the plugin
// does not advertise). The operates and scope fields must agree by exact string;
// the selector is not compared (it is free-form documentation). The
// directionality is entity-claim ⊆ describe(): the descriptor may declare
// exports the entity omits — an entity that authors a partial export surface is
// not required to enumerate every export the plugin offers — but every export
// the entity does claim must match the plugin's self-report exactly.
//
// When the entity authors a plugin-level capabilities claim, it is compared
// field-by-field against the descriptor's self-reported capabilities: the input
// kinds, the boundary-assembly mode, the artifact-template delimiter pair, and
// the source filter must each agree. A nil claim skips the plugin-level
// comparison — an entity is not required to author a plugin-level claim.
//
// entityIdentity names the entity in the rejection message so the author sees
// which entity's claim disagreed with which plugin.
func CrossCheckCapabilities(entityIdentity string, claim EntityCapabilityClaim, desc PluginDescriptor) error {
	for _, want := range claim.Exports {
		got, ok := findDescriptorExport(desc, want.Name)
		if !ok {
			return fmt.Errorf(
				"entity %q claims export %q, which the plugin's describe() does not declare: %w",
				entityIdentity, want.Name, ErrWasmCapabilityMismatch)
		}
		if want.Operates != got.Operates {
			return fmt.Errorf(
				"entity %q export %q declares operates %q but the plugin's describe() reports %q: %w",
				entityIdentity, want.Name, want.Operates, got.Operates, ErrWasmCapabilityMismatch)
		}
		if want.Scope != got.Scope {
			return fmt.Errorf(
				"entity %q export %q declares scope %q but the plugin's describe() reports %q: %w",
				entityIdentity, want.Name, want.Scope, got.Scope, ErrWasmCapabilityMismatch)
		}
	}
	if claim.Capabilities != nil {
		if err := crossCheckPluginCapabilities(entityIdentity, *claim.Capabilities, desc.Capabilities); err != nil {
			return err
		}
	}
	return nil
}

// crossCheckHostFunctions verifies that the host functions a plugin declares
// in describe() agree exactly with the host functions it actually imports from
// its .wasm. The .wasm import section is ground truth (imported is derived from
// it at load); the descriptor's declared host_functions — the union across all
// exports — is a claim that must match it. Both sets are within the allowlist
// (the loader admits only allowlisted imports; the descriptor validator rejects
// a declared name outside the allowlist), so the comparison is exact-set
// equality over allowlisted names.
//
// A name declared in describe() that the .wasm does not import, or a name the
// .wasm imports that describe() does not declare, is a Loud Guardrail halt
// wrapping [ErrWasmManifestRejected] naming the plugin and the offending
// function — describe() cannot advertise a host surface the binary lacks, nor
// hide one the binary uses. The comparison is order-independent; the reported
// name is deterministic (the first offender in sorted order) so the same
// mismatch yields the same message across runs.
//
// pluginIdentity names the plugin in the rejection message. imported is the
// derived import set ([Plugin.HostFunctions]); the caller passes it so this
// pure function needs no live plugin.
func crossCheckHostFunctions(pluginIdentity string, desc PluginDescriptor, imported []string) error {
	declared := descriptorHostFunctionUnion(desc)
	importedSet := make(map[string]bool, len(imported))
	for _, name := range imported {
		importedSet[name] = true
	}
	declaredSet := make(map[string]bool, len(declared))
	for _, name := range declared {
		declaredSet[name] = true
	}
	for _, name := range declared {
		if !importedSet[name] {
			return fmt.Errorf(
				"plugin %q declares host function %q in describe() that its wasm does not import: %w",
				pluginIdentity, name, ErrWasmManifestRejected)
		}
	}
	for _, name := range imported {
		if !declaredSet[name] {
			return fmt.Errorf(
				"plugin %q imports host function %q that its describe() does not declare: %w",
				pluginIdentity, name, ErrWasmManifestRejected)
		}
	}
	return nil
}

// descriptorHostFunctionUnion returns the sorted, de-duplicated union of the
// host functions declared across every export in the descriptor. Descriptor
// host_functions are per-export while .wasm imports are module-level, so the
// union is the module-level projection of the descriptor's claim — the set the
// binary-derived import set is compared against.
func descriptorHostFunctionUnion(desc PluginDescriptor) []string {
	seen := make(map[string]bool)
	var union []string
	for _, export := range desc.Exports {
		for _, name := range export.HostFunctions {
			if seen[name] {
				continue
			}
			seen[name] = true
			union = append(union, name)
		}
	}
	sort.Strings(union)
	return union
}

// crossCheckPluginCapabilities compares an entity's authored plugin-level
// capability claim against the descriptor's self-report field-by-field,
// returning a Loud Guardrail error wrapping [ErrWasmCapabilityMismatch] that
// names the first field to disagree, or nil when every field agrees.
func crossCheckPluginCapabilities(entityIdentity string, want, got Capabilities) error {
	if want.InputKinds.Sourced != got.InputKinds.Sourced {
		return fmt.Errorf(
			"entity %q declares input_kinds.sourced %t but the plugin's describe() reports %t: %w",
			entityIdentity, want.InputKinds.Sourced, got.InputKinds.Sourced, ErrWasmCapabilityMismatch)
	}
	if want.InputKinds.Synthesized != got.InputKinds.Synthesized {
		return fmt.Errorf(
			"entity %q declares input_kinds.synthesized %t but the plugin's describe() reports %t: %w",
			entityIdentity, want.InputKinds.Synthesized, got.InputKinds.Synthesized, ErrWasmCapabilityMismatch)
	}
	if want.InputKinds.ArtifactsStandalone != got.InputKinds.ArtifactsStandalone {
		return fmt.Errorf(
			"entity %q declares input_kinds.artifacts_standalone %t but the plugin's describe() reports %t: %w",
			entityIdentity, want.InputKinds.ArtifactsStandalone, got.InputKinds.ArtifactsStandalone, ErrWasmCapabilityMismatch)
	}
	if want.BoundaryAssembly != got.BoundaryAssembly {
		return fmt.Errorf(
			"entity %q declares boundary_assembly %q but the plugin's describe() reports %q: %w",
			entityIdentity, want.BoundaryAssembly, got.BoundaryAssembly, ErrWasmCapabilityMismatch)
	}
	if err := crossCheckDelimiters(entityIdentity, want.ArtifactDelimiters, got.ArtifactDelimiters); err != nil {
		return err
	}
	if !equalStringSlices(want.SourceFilter, got.SourceFilter) {
		return fmt.Errorf(
			"entity %q declares source_filter %v but the plugin's describe() reports %v: %w",
			entityIdentity, want.SourceFilter, got.SourceFilter, ErrWasmCapabilityMismatch)
	}
	return nil
}

// crossCheckDelimiters compares the authored and self-reported artifact-template
// delimiter pairs. An entity that declares the pair while the plugin omits it
// (or the reverse) disagrees, as does a pair whose left or right differs.
func crossCheckDelimiters(entityIdentity string, want, got *ArtifactDelimiters) error {
	if (want == nil) != (got == nil) {
		return fmt.Errorf(
			"entity %q and the plugin's describe() disagree on whether artifact_delimiters is declared (entity declares=%t, plugin declares=%t): %w",
			entityIdentity, want != nil, got != nil, ErrWasmCapabilityMismatch)
	}
	if want != nil && *want != *got {
		return fmt.Errorf(
			"entity %q declares artifact_delimiters %q/%q but the plugin's describe() reports %q/%q: %w",
			entityIdentity, want.Left, want.Right, got.Left, got.Right, ErrWasmCapabilityMismatch)
	}
	return nil
}

// equalStringSlices reports whether two string slices carry the same elements
// in the same order. A nil slice and an empty slice compare equal.
func equalStringSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// findDescriptorExport returns the descriptor's export of the given name and
// true, or the zero export and false when the descriptor declares no such
// export.
func findDescriptorExport(desc PluginDescriptor, name string) (PluginExport, bool) {
	for _, e := range desc.Exports {
		if e.Name == name {
			return e, true
		}
	}
	return PluginExport{}, false
}
