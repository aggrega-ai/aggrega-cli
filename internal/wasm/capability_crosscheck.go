// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package wasm

import (
	"fmt"
	"sort"
)

// EntityContractClaim is the authored contract declaration carried by a
// catalog Plugin entity, in the neutral shape this package compares against
// a plugin's self-reported [PluginDescriptor]. The caller maps the entity's
// exports into this shape so this package does not depend on pkg/types; the claim
// is the authored promise of what each export does, checked at load against the
// plugin's own describe() report.
type EntityContractClaim struct {
	// Exports is the authored per-export capability claim, in author order.
	Exports []EntityExportClaim

	// Contract is the authored provider render-behavior contract, in the same
	// neutral shape the plugin self-reports in describe(). Nil when the entity
	// authors no render-behavior contract, in which case the cross-check compares
	// only the per-export claims.
	Contract *ProviderContract

	// Detect is the importer recognition signatures the entity records, in the
	// same neutral shape the plugin self-reports in describe(). Nil when the
	// entity records none — an entity generated before the record existed among
	// them — in which case nothing about the signatures is compared; a non-nil
	// list, even an empty one, is compared with describe() entry for entry.
	Detect *[]DetectSignature
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

// CrossCheckContract verifies that an entity's authored capability claim
// agrees with the plugin's self-reported describe() descriptor, returning a
// Loud Guardrail error wrapping [ErrWasmContractMismatch] on the first
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
// When the entity authors a provider render-behavior contract, it is compared
// field-by-field against the descriptor's self-reported contract: the input
// kinds, the boundary-assembly mode, the artifact-template delimiter pair, the
// assembly-name annotation, the reusable-instance claim, the cross-boundary-linking
// claim, the omit-entries claim, the shared-files claim, and the nest segment must
// each agree, and so must the source ignore rules where the entity records any. A
// nil contract skips the provider-level comparison — an entity is not required to
// author a render-behavior contract.
//
// When the entity records importer recognition signatures, they are compared with
// the ones describe() reports under importer.detect, entry for entry and in
// order, the way the source ignore rules are: the record is what an import reads
// to recognise a source, so a record that differs from the plugin would recognise
// sources by signatures the plugin never declared. An entity recording none
// makes no claim and keeps loading.
//
// entityIdentity names the entity in the rejection message so the author sees
// which entity's claim disagreed with which plugin.
func CrossCheckContract(entityIdentity string, claim EntityContractClaim, desc PluginDescriptor) error {
	for _, want := range claim.Exports {
		got, ok := findDescriptorExport(desc, want.Name)
		if !ok {
			return fmt.Errorf(
				"entity %q claims export %q, which the plugin's describe() does not declare: %w",
				entityIdentity, want.Name, ErrWasmContractMismatch())
		}
		if want.Operates != got.Operates {
			return fmt.Errorf(
				"entity %q export %q declares operates %q but the plugin's describe() reports %q: %w",
				entityIdentity, want.Name, want.Operates, got.Operates, ErrWasmContractMismatch())
		}
		if want.Scope != got.Scope {
			return fmt.Errorf(
				"entity %q export %q declares scope %q but the plugin's describe() reports %q: %w",
				entityIdentity, want.Name, want.Scope, got.Scope, ErrWasmContractMismatch())
		}
	}
	if claim.Contract != nil {
		var got ProviderContract
		if desc.Contract != nil {
			got = *desc.Contract
		}
		if err := crossCheckProviderContract(entityIdentity, *claim.Contract, got); err != nil {
			return err
		}
	}
	return crossCheckDetect(entityIdentity, claim.Detect, desc.Importer)
}

// crossCheckDeclaredCapabilities verifies that every capability an entity's list
// names is one the plugin's describe() reports, returning a Loud Guardrail error
// wrapping [ErrWasmContractMismatch] that names each capability the plugin does not
// report, and nil when the list names none.
//
// The direction is list ⊆ describe(), the same as the per-export claim's: a
// consumer resolves a plugin by the capabilities its published entity lists, so a
// listed capability the binary does not provide is a promise made where it cannot
// be kept. A list leaving reported capabilities out is a deliberate narrowing and
// passes here; [UnderDeclaredNotices] reports it, and refuses nothing.
//
// It is called by the publish-side validation door, [ValidatePlugin], which
// publish and the standalone validate given the plugin's binary both reach; the
// load gate compares the per-export and contract claims of [CrossCheckContract]
// alone.
func crossCheckDeclaredCapabilities(entityIdentity string, declared []string, desc PluginDescriptor) error {
	reported := make(map[string]bool, len(desc.Capabilities))
	for _, c := range desc.Capabilities {
		reported[c] = true
	}
	var unreported []string
	for _, c := range declared {
		if !reported[c] {
			unreported = append(unreported, c)
		}
	}
	if len(unreported) == 0 {
		return nil
	}
	return fmt.Errorf(
		"entity %q lists capabilities %q, which the plugin's describe() does not report (it reports %q); "+
			"remove them from the list, which may name only capabilities the plugin reports: %w",
		entityIdentity, unreported, desc.Capabilities, ErrWasmContractMismatch())
}

// crossCheckDetect compares the recognition signatures an entity records with
// the ones describe() reports, where the entity records any. The comparison is
// one-sided for the reason crossCheckSourceIgnore's is: an absent record makes no
// claim. A recorded list must equal the reported one entry for entry and in
// order — order is part of the declaration — and the first entry to differ is
// named by its position and field.
func crossCheckDetect(entityIdentity string, want *[]DetectSignature, got *ImporterContract) error {
	if want == nil {
		return nil
	}
	var reported []DetectSignature
	if got != nil {
		reported = got.Detect
	}
	recorded := *want
	if len(recorded) != len(reported) {
		return fmt.Errorf(
			"entity %q records %d importer.detect signature(s) but the plugin's describe() reports %d: %w",
			entityIdentity, len(recorded), len(reported), ErrWasmContractMismatch())
	}
	for i := range recorded {
		if err := crossCheckDetectSignature(entityIdentity, i, recorded[i], reported[i]); err != nil {
			return err
		}
	}
	return nil
}

// crossCheckDetectSignature compares one recorded signature with the reported
// signature at the same position, naming the first field to differ.
func crossCheckDetectSignature(entityIdentity string, i int, want, got DetectSignature) error {
	if want.Syntax != got.Syntax {
		return fmt.Errorf(
			"entity %q records importer.detect[%d].syntax %q but the plugin's describe() reports %q: %w",
			entityIdentity, i, want.Syntax, got.Syntax, ErrWasmContractMismatch())
	}
	if !sameStrings(want.Files, got.Files) {
		return fmt.Errorf(
			"entity %q records importer.detect[%d].files %q but the plugin's describe() reports %q: %w",
			entityIdentity, i, want.Files, got.Files, ErrWasmContractMismatch())
	}
	if len(want.Match) != len(got.Match) {
		return fmt.Errorf(
			"entity %q records %d importer.detect[%d].match predicate(s) but the plugin's describe() reports %d: %w",
			entityIdentity, len(want.Match), i, len(got.Match), ErrWasmContractMismatch())
	}
	for j := range want.Match {
		if want.Match[j] != got.Match[j] {
			return fmt.Errorf(
				"entity %q records importer.detect[%d].match[%d] as %s but the plugin's describe() reports %s: %w",
				entityIdentity, i, j, pathMatchText(want.Match[j]), pathMatchText(got.Match[j]), ErrWasmContractMismatch())
		}
	}
	return nil
}

// pathMatchText renders one predicate in the words its fields are written in —
// path, is, and the comparison where it declares one — so a refusal naming two
// predicates reads as the record and the report an author compares.
func pathMatchText(m PathMatch) string {
	text := fmt.Sprintf("path %q is %q", m.Path, m.Is)
	if m.Equals != "" {
		text += fmt.Sprintf(" equals %q", m.Equals)
	}
	if m.Prefix != "" {
		text += fmt.Sprintf(" prefix %q", m.Prefix)
	}
	return text
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
				pluginIdentity, name, ErrWasmManifestRejected())
		}
	}
	for _, name := range imported {
		if !declaredSet[name] {
			return fmt.Errorf(
				"plugin %q imports host function %q that its describe() does not declare: %w",
				pluginIdentity, name, ErrWasmManifestRejected())
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

// crossCheckProviderContract compares an entity's authored provider
// render-behavior contract against the descriptor's self-report field-by-field,
// returning a Loud Guardrail error wrapping [ErrWasmContractMismatch] that
// names the first field to disagree, or nil when every field agrees.
func crossCheckProviderContract(entityIdentity string, want, got ProviderContract) error {
	if want.InputKinds.Sourced != got.InputKinds.Sourced {
		return fmt.Errorf(
			"entity %q declares input_kinds.sourced %t but the plugin's describe() reports %t: %w",
			entityIdentity, want.InputKinds.Sourced, got.InputKinds.Sourced, ErrWasmContractMismatch())
	}
	if want.InputKinds.Synthesized != got.InputKinds.Synthesized {
		return fmt.Errorf(
			"entity %q declares input_kinds.synthesized %t but the plugin's describe() reports %t: %w",
			entityIdentity, want.InputKinds.Synthesized, got.InputKinds.Synthesized, ErrWasmContractMismatch())
	}
	if want.InputKinds.ArtifactsStandalone != got.InputKinds.ArtifactsStandalone {
		return fmt.Errorf(
			"entity %q declares input_kinds.artifacts_standalone %t but the plugin's describe() reports %t: %w",
			entityIdentity, want.InputKinds.ArtifactsStandalone, got.InputKinds.ArtifactsStandalone, ErrWasmContractMismatch())
	}
	if want.BoundaryAssembly != got.BoundaryAssembly {
		return fmt.Errorf(
			"entity %q declares boundary_assembly %q but the plugin's describe() reports %q: %w",
			entityIdentity, want.BoundaryAssembly, got.BoundaryAssembly, ErrWasmContractMismatch())
	}
	if err := crossCheckDelimiters(entityIdentity, want.ArtifactDelimiters, got.ArtifactDelimiters); err != nil {
		return err
	}
	if err := crossCheckAssemblyNameAnnotation(entityIdentity, want.AssemblyNameAnnotation, got.AssemblyNameAnnotation); err != nil {
		return err
	}
	if want.ReusableInstance != got.ReusableInstance {
		return fmt.Errorf(
			"entity %q declares reusable_instance %t but the plugin's describe() reports %t: %w",
			entityIdentity, want.ReusableInstance, got.ReusableInstance, ErrWasmContractMismatch())
	}
	if want.CrossBoundaryLinking != got.CrossBoundaryLinking {
		return fmt.Errorf(
			"entity %q declares cross_boundary_linking %t but the plugin's describe() reports %t: %w",
			entityIdentity, want.CrossBoundaryLinking, got.CrossBoundaryLinking, ErrWasmContractMismatch())
	}
	if want.Omits != got.Omits {
		return fmt.Errorf(
			"entity %q declares omits %t but the plugin's describe() reports %t: %w",
			entityIdentity, want.Omits, got.Omits, ErrWasmContractMismatch())
	}
	if want.BoundaryArtifacts != got.BoundaryArtifacts {
		return fmt.Errorf(
			"entity %q declares boundary_artifacts %t but the plugin's describe() reports %t: %w",
			entityIdentity, want.BoundaryArtifacts, got.BoundaryArtifacts, ErrWasmContractMismatch())
	}
	if want.NestSegment != got.NestSegment {
		return fmt.Errorf(
			"entity %q declares nest_segment %q but the plugin's describe() reports %q: %w",
			entityIdentity, want.NestSegment, got.NestSegment, ErrWasmContractMismatch())
	}
	return crossCheckSourceIgnore(entityIdentity, want.SourceIgnore, got.SourceIgnore)
}

// crossCheckSourceIgnore compares an entity's recorded ignore rules with the ones
// describe() reports, where the entity records any. The comparison is one-sided
// on purpose: the rules are what the pin step reads off the entity, so a record
// that differs from the plugin would pin a source under rules the plugin never
// declared and is refused, while an entity recording none makes no claim — it
// keeps loading against a plugin that declares some, and the pin step then
// records no plugin rules from it. Order is part of the rules, because a
// later rule overrides an earlier one, so the lists are compared in order.
func crossCheckSourceIgnore(entityIdentity string, want, got *SourceIgnore) error {
	if want == nil {
		return nil
	}
	var reported SourceIgnore
	if got != nil {
		reported = *got
	}
	if !sameStrings(want.Patterns, reported.Patterns) {
		return fmt.Errorf(
			"entity %q declares source_ignore patterns %q but the plugin's describe() reports %q: %w",
			entityIdentity, want.Patterns, reported.Patterns, ErrWasmContractMismatch())
	}
	if !sameStrings(want.Files, reported.Files) {
		return fmt.Errorf(
			"entity %q declares source_ignore files %q but the plugin's describe() reports %q: %w",
			entityIdentity, want.Files, reported.Files, ErrWasmContractMismatch())
	}
	return nil
}

// sameStrings reports whether two lists hold the same strings in the same order,
// an absent list and an empty one alike.
func sameStrings(a, b []string) bool {
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

// crossCheckDelimiters compares the authored and self-reported artifact-template
// delimiter pairs. An entity that declares the pair while the plugin omits it
// (or the reverse) disagrees, as does a pair whose left or right differs.
func crossCheckDelimiters(entityIdentity string, want, got *ArtifactDelimiters) error {
	if (want == nil) != (got == nil) {
		return fmt.Errorf(
			"entity %q and the plugin's describe() disagree on whether artifact_delimiters is declared (entity declares=%t, plugin declares=%t): %w",
			entityIdentity, want != nil, got != nil, ErrWasmContractMismatch())
	}
	if want != nil && *want != *got {
		return fmt.Errorf(
			"entity %q declares artifact_delimiters %q/%q but the plugin's describe() reports %q/%q: %w",
			entityIdentity, want.Left, want.Right, got.Left, got.Right, ErrWasmContractMismatch())
	}
	return nil
}

// crossCheckAssemblyNameAnnotation compares the authored and self-reported
// assembly-name annotation. An entity that declares it while the plugin omits it
// (or the reverse) disagrees, as does a pair whose annotation key or
// require-unique flag differs.
func crossCheckAssemblyNameAnnotation(entityIdentity string, want, got *AssemblyNameAnnotation) error {
	if (want == nil) != (got == nil) {
		return fmt.Errorf(
			"entity %q and the plugin's describe() disagree on whether assembly_name_annotation is declared (entity declares=%t, plugin declares=%t): %w",
			entityIdentity, want != nil, got != nil, ErrWasmContractMismatch())
	}
	if want != nil && *want != *got {
		return fmt.Errorf(
			"entity %q declares assembly_name_annotation %q (require_unique=%t) but the plugin's describe() reports %q (require_unique=%t): %w",
			entityIdentity, want.Annotation, want.RequireUnique, got.Annotation, got.RequireUnique, ErrWasmContractMismatch())
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
