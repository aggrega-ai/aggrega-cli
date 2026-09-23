// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package wasm

import "fmt"

// EntityCuratedSurface is the authored, curated surface a Plugin entity declares
// for the under-declaration advisory: the export names, the plugin-level
// capabilities, and whether the entity authors a render-behavior contract. It is
// the inverse view of the load-gate cross-check — the cross-check verifies the
// authored surface is a subset of the binary's self-report (author ⊆ binary),
// and this surface feeds the advisory that reports where the binary offers more
// than the entity curates (binary ⊇ entity).
type EntityCuratedSurface struct {
	// ExportNames is the export names the entity declares, in author order.
	ExportNames []string
	// Capabilities is the plugin-level capabilities the entity declares, in
	// author order.
	Capabilities []string
	// HasContract reports whether the entity authors a render-behavior contract.
	HasContract bool
}

// UnderDeclaredNotices reports, as advisory notice strings, each export,
// capability, or contract the plugin's describe() self-report offers that the
// entity's curated surface omits. It is the inverse of the load-gate cross-check:
// where the cross-check halts when the authored surface claims more than the
// binary provides (author ⊄ binary), this reports where the binary provides more
// than the entity curates (binary ⊋ entity).
//
// Under-declaration is legitimate: an author may curate a partial export or
// capability surface deliberately. So this NEVER signals failure — it returns
// notices for a caller to print, changes no exit status, and returns an empty
// slice when the entity's surface covers everything the binary reports. It is the
// single home of the under-declaration derivation; both the publish path and the
// lint path call it rather than each re-deriving the set difference.
func UnderDeclaredNotices(surface EntityCuratedSurface, desc PluginDescriptor) []string {
	var notices []string

	declaredExports := make(map[string]bool, len(surface.ExportNames))
	for _, n := range surface.ExportNames {
		declaredExports[n] = true
	}
	for _, e := range desc.Exports {
		if !declaredExports[e.Name] {
			notices = append(notices, fmt.Sprintf(
				"plugin describe() offers export %q the entity does not declare", e.Name))
		}
	}

	declaredCaps := make(map[string]bool, len(surface.Capabilities))
	for _, c := range surface.Capabilities {
		declaredCaps[c] = true
	}
	for _, c := range desc.Capabilities {
		if !declaredCaps[c] {
			notices = append(notices, fmt.Sprintf(
				"plugin describe() reports capability %q the entity does not declare", c))
		}
	}

	if !surface.HasContract && desc.Contract != nil && !desc.Contract.isZero() {
		notices = append(notices,
			"plugin describe() reports a render-behavior contract the entity does not declare")
	}

	return notices
}

// isZero reports whether a ProviderContract carries no render-behavior claim —
// the all-zero value describe() returns for a plugin that declares no contract.
func (c ProviderContract) isZero() bool {
	return !c.InputKinds.Sourced &&
		!c.InputKinds.Synthesized &&
		!c.InputKinds.ArtifactsStandalone &&
		c.BoundaryAssembly == "" &&
		c.ArtifactDelimiters == nil &&
		c.AssemblyNameAnnotation == nil &&
		!c.ReusableInstance &&
		!c.CrossBoundaryLinking
}
