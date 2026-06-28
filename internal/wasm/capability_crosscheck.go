// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package wasm

import "fmt"

// EntityCapabilityClaim is the authored capability declaration carried by a
// catalog WasmPlugin entity, in the neutral shape this package compares against
// a plugin's self-reported [PluginDescriptor]. The caller maps the entity's
// exports into this shape so this package stays ignorant of pkg/types; the claim
// is the authored promise of what each export does, checked at load against the
// plugin's own describe() report.
type EntityCapabilityClaim struct {
	// Exports is the authored per-export capability claim, in author order.
	Exports []EntityExportClaim
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
	return nil
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
