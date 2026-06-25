// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package wasm

import "fmt"

// PluginDescriptor is the output a Wasm plugin returns from its
// describe() export — the plugin's self-declaration of the ABI version
// it binds against, its plugin type, the provider engines it is
// compatible with, the exports it offers (each with its capability
// declaration), and optional authorship metadata. The host reads
// describe() once when it loads a plugin, validates the declaration (see
// [PluginDescriptor.Validate]), and uses the per-export declarations to
// bind the least-privilege host surface each export needs before
// invoking it.
//
// describe() is cross-plugin-type: one shape covers provider, linter,
// and feature-recipe-pipeline plugins. It is consumed by the host
// loader for dispatch decisions, not emitted into rendered output, so
// its slices carry author/declared order without an OrderedMap; a
// future consumer that marshals a descriptor into a deterministic
// on-disk artifact relies on encoding/json's sorted object keys plus
// these declared-order slices.
//
// **Status: contract definition only — the live caller is not yet
// wired.** This file ships the describe() shape and a pure validator
// that the host applies to a parsed descriptor; both are exercised
// now. The loader step that invokes a guest's describe() export over
// the runtime wire, reads the returned bytes, and unmarshals them into
// this shape lands with the runtime that dispatches guest exports. The
// shape and the validator are stable surface so that work binds
// against them now.
//
// This is the cross-boundary wire analog of the in-tree
// renderer.ProviderCapabilities self-report: a provider engine reports
// its supported input kinds to the central gate through
// ProviderCapabilities, exactly as a provider Wasm guest reports its
// exports and ABI version through describe(). The two are independent
// surfaces over the same idea (a renderer self-declaring what it can
// do); describe() is the wire shape, ProviderCapabilities the in-tree
// Go shape, and neither is derived from the other.
type PluginDescriptor struct {
	// ABIVersion is the plugin ABI the guest binds against. The host
	// accepts exactly one version (ABIVersionV1Alpha1); any other value
	// fails the load. This is the ABI-compatibility handshake: a guest
	// built against an ABI the host does not speak is refused loudly at
	// load rather than mis-invoked at dispatch.
	ABIVersion string `json:"abi_version"`

	// PluginType is the plugin's kind — provider, linter, or
	// feature-recipe-pipeline step. It selects the minimum-export rule
	// the host validates and, with the export name, the default
	// input pattern.
	PluginType PluginType `json:"plugin_type"`

	// CompatibleProviders is the enumerated list of provider engines this
	// plugin is tested-compatible with (provider names such as "helm" or
	// "compose"), in declared order. A plugin is generic across artifacts
	// that share a provider's output shape, not across all providers; the
	// list is an explicit, tested-compatibility claim rather than an
	// implicit "any similar provider". The runtime enforces it — for each
	// scope a pipeline step touches, the provider rendering that scope's
	// documents must appear here — but this load-time validator does not:
	// entries are unchecked strings here, since no provider registry exists
	// at the load boundary to check them against. An empty or absent list
	// declares no compatibility claim, so it matches no provider and the
	// runtime halts any pipeline step that uses the plugin.
	CompatibleProviders []string `json:"compatible_providers,omitempty"`

	// Exports lists the plugin's offered exports in declared order. The
	// host validates that the per-type minimum export is present and
	// binds the per-export host surface before invoking each.
	Exports []PluginExport `json:"exports"`

	// Metadata is optional authorship information. It carries no
	// behavioral contract — the host neither validates nor acts on it.
	Metadata PluginDescriptorMeta `json:"metadata,omitempty,omitzero"`
}

// PluginExport is one export a plugin offers, as declared in describe().
type PluginExport struct {
	// Name is the export's ABI name — for example "render_node",
	// "assemble_boundary", "lint_naming", "step". The host dispatches
	// to the export by this name.
	Name string `json:"name"`

	// InputPattern selects how the host delivers the export's input:
	// the full payload inline (InputPatternFull) or a small payload the
	// guest enriches via host functions (InputPatternMinimal). An export
	// that omits this field takes its plugin-type/name default (see
	// [defaultInputPattern]).
	InputPattern InputPattern `json:"input_pattern,omitempty"`

	// HostFunctions lists the host function names this export imports.
	// Every name MUST be in the host-function allowlist (see
	// hostfuncs.go); the host binds exactly these functions for this
	// export and no others, giving each export the least-privilege host
	// surface it declares. In v1alpha1 the bound surface is the two
	// baseline diagnostic functions; data-fetch host functions are
	// contract-reserved but not yet bindable.
	HostFunctions []string `json:"host_functions,omitempty"`

	// Description is optional human-readable text describing the export.
	Description string `json:"description,omitempty"`

	// Stability is the export's stability level — stable, experimental,
	// or deprecated. An omitted value carries no stability claim.
	Stability Stability `json:"stability,omitempty"`

	// Operates declares the pipeline operation this export performs:
	// "patch" (modify an existing rendered document) or "emit" (add a new
	// document). The value is shape-validated here — it must be patch,
	// emit, or empty — but the host does not act on it at load; the
	// runtime reads it when dispatching the step.
	Operates string `json:"operates,omitempty"`

	// Scope declares the document root this export operates on: "own" (the
	// node's own rendered documents) or "host" (the documents of the
	// component a component-scope FeatureRecipe is attached to). The value
	// is shape-validated here — it must be own, host, or empty — but the
	// host does not act on it at load; the runtime reads it when
	// dispatching the step and resolves the scope's documents.
	Scope string `json:"scope,omitempty"`

	// Selector is a free-form, human-readable description of what the
	// export acts on (for example "workloads carrying a pod template").
	// It is documentation for catalog readers; the host neither validates
	// its content nor acts on it.
	Selector string `json:"selector,omitempty"`
}

// PluginDescriptorMeta is optional authorship metadata a plugin
// declares in describe(). It is informational; the host does not
// validate or act on it.
type PluginDescriptorMeta struct {
	Author     string `json:"author,omitempty"`
	Repository string `json:"repository,omitempty"`
	License    string `json:"license,omitempty"`
}

// PluginType is the kind of a Wasm plugin. It selects the
// minimum-export rule the host enforces and the default input pattern
// for an export.
type PluginType string

const (
	// PluginTypeProvider is a provider plugin: it renders IR nodes into
	// provider-native artifacts. Its minimum export is render_node.
	PluginTypeProvider PluginType = "provider"

	// PluginTypeLinter is a linter plugin: it inspects authored content
	// and raises diagnostics. Its minimum surface is at least one
	// lint_* export.
	PluginTypeLinter PluginType = "linter"

	// PluginTypeFRPStep is a feature-recipe-pipeline step plugin: it
	// transforms the IR within a feature recipe's pipeline. Its minimum
	// export is step.
	PluginTypeFRPStep PluginType = "frp_step"
)

// InputPattern selects how the host delivers an export's input payload.
type InputPattern string

const (
	// InputPatternFull delivers the export its full input payload
	// inline, so the guest renders from the payload alone.
	InputPatternFull InputPattern = "b"

	// InputPatternMinimal delivers a small input the guest enriches via
	// host functions. In v1alpha1 the bound host-function surface is the
	// two baseline diagnostic functions, so a minimal-input export has
	// no data-fetch host functions to call; the pattern is
	// contract-reserved ABI-evolution headroom rather than an active
	// v1alpha1 data path.
	InputPatternMinimal InputPattern = "a"
)

// Stability is an export's declared stability level.
type Stability string

const (
	StabilityStable       Stability = "stable"
	StabilityExperimental Stability = "experimental"
	StabilityDeprecated   Stability = "deprecated"
)

// ABIVersionV1Alpha1 is the single plugin ABI version the host speaks.
// A descriptor declaring any other abi_version fails the
// ABI-compatibility handshake in [PluginDescriptor.Validate].
const ABIVersionV1Alpha1 = "v1alpha1"

// lintExportPrefix is the prefix a linter plugin's rule exports carry;
// a linter satisfies its minimum-export rule with at least one export
// whose name begins with this prefix.
const lintExportPrefix = "lint_"

// frpStepExportName is the required export name for a
// feature-recipe-pipeline step plugin.
const frpStepExportName = "step"

// providerRenderExportName is the required export name for a provider
// plugin.
const providerRenderExportName = "render_node"

// operatesPatch and operatesEmit are the two pipeline operations an
// export may declare via [PluginExport.Operates]. The shape validator
// accepts these two values or the empty string; it does not require the
// field or act on its value — the runtime reads it when dispatching.
const (
	operatesPatch = "patch"
	operatesEmit  = "emit"
)

// ScopeOwn and ScopeHost are the two document roots an export may declare
// via [PluginExport.Scope]. The shape validator accepts these two values
// or the empty string; it does not require the field or act on its value
// — the runtime resolves the scope's documents when dispatching, and the
// renderer's pre-walk provider-compatibility check reads ScopeHost to decide
// which provider an export's scope is checked against. Exported so the single
// scope identity is shared between this load-time shape gate and the runtime
// enforcement rather than each side re-spelling the literal.
const (
	ScopeOwn  = "own"
	ScopeHost = "host"
)

// Validate checks a parsed descriptor against the host's contract and
// returns nil if the plugin is admissible, or an error wrapping
// [ErrWasmManifestRejected] naming pluginIdentity and the specific
// cause if not. It is a pure function over the parsed struct — the host
// applies it after unmarshaling a guest's describe() output, and the
// test suite exercises it directly. The checks, in order:
//
//   - ABI-compatibility handshake: abi_version is exactly
//     ABIVersionV1Alpha1. A guest built against any other ABI is
//     refused at load rather than mis-invoked at dispatch.
//   - plugin_type is a recognized [PluginType].
//   - the per-type minimum export is present: a provider declares
//     render_node; a linter declares at least one lint_* export; a
//     feature-recipe-pipeline step declares step.
//   - every export's declared host functions are a subset of the
//     host-function allowlist. This routes through the same
//     validateDeclaredHostFunctions gate the loader applies to a
//     PluginSpec, so describe()-declared host functions are checked
//     against the single allowlist — no second list, no second gate.
//   - every export's capability fields are well-shaped: operates is
//     patch, emit, or empty; scope is own, host, or empty. This is shape
//     validation only — the host does not check provider compatibility,
//     require a capability per plugin type, or act on the values. The
//     runtime enforces compatibleProviders and acts on operates/scope
//     when it dispatches a step; here the values are checked for shape
//     and otherwise piped through.
//
// pluginIdentity names the plugin in the rejection message. The caller
// passes whatever stable identity it holds; the field is plumbed so the
// message contract is met without this surface inventing an identity
// scheme of its own.
func (d PluginDescriptor) Validate(pluginIdentity string) error {
	if d.ABIVersion != ABIVersionV1Alpha1 {
		return fmt.Errorf("plugin %q declares abi_version %q; host speaks %q: %w",
			pluginIdentity, d.ABIVersion, ABIVersionV1Alpha1, ErrWasmManifestRejected)
	}

	switch d.PluginType {
	case PluginTypeProvider, PluginTypeLinter, PluginTypeFRPStep:
		// recognized
	default:
		return fmt.Errorf("plugin %q declares unknown plugin_type %q: %w",
			pluginIdentity, d.PluginType, ErrWasmManifestRejected)
	}

	if err := d.validateMinimumExports(pluginIdentity); err != nil {
		return err
	}

	if err := d.validateExportHostFunctions(pluginIdentity); err != nil {
		return err
	}

	return d.validateExportCapabilities(pluginIdentity)
}

// validateExportCapabilities shape-checks the per-export capability
// fields: operates must be patch, emit, or empty; scope must be own,
// host, or empty. selector is free-form and unchecked. This is shape
// validation only — it does not require a capability per plugin type,
// does not cross-check operates/scope against the plugin type or its
// compatibleProviders, and does not consult a provider registry. The
// runtime enforces compatibility and acts on these values when it
// dispatches a step; this load-time gate only rejects a malformed value.
func (d PluginDescriptor) validateExportCapabilities(pluginIdentity string) error {
	for _, export := range d.Exports {
		switch export.Operates {
		case "", operatesPatch, operatesEmit:
			// well-shaped
		default:
			return fmt.Errorf("plugin %q export %q declares unknown operates %q (want %q, %q, or empty): %w",
				pluginIdentity, export.Name, export.Operates, operatesPatch, operatesEmit, ErrWasmManifestRejected)
		}
		switch export.Scope {
		case "", ScopeOwn, ScopeHost:
			// well-shaped
		default:
			return fmt.Errorf("plugin %q export %q declares unknown scope %q (want %q, %q, or empty): %w",
				pluginIdentity, export.Name, export.Scope, ScopeOwn, ScopeHost, ErrWasmManifestRejected)
		}
	}
	return nil
}

// validateMinimumExports enforces the per-plugin-type minimum-export
// rule, returning an ErrWasmManifestRejected-wrapped error naming the
// missing export when the rule is unmet.
func (d PluginDescriptor) validateMinimumExports(pluginIdentity string) error {
	switch d.PluginType {
	case PluginTypeProvider:
		if !d.hasExport(providerRenderExportName) {
			return fmt.Errorf("provider plugin %q declares no %q export: %w",
				pluginIdentity, providerRenderExportName, ErrWasmManifestRejected)
		}
	case PluginTypeLinter:
		if !d.hasExportWithPrefix(lintExportPrefix) {
			return fmt.Errorf("linter plugin %q declares no %s* export: %w",
				pluginIdentity, lintExportPrefix, ErrWasmManifestRejected)
		}
	case PluginTypeFRPStep:
		if !d.hasExport(frpStepExportName) {
			return fmt.Errorf("feature-recipe-pipeline plugin %q declares no %q export: %w",
				pluginIdentity, frpStepExportName, ErrWasmManifestRejected)
		}
	}
	return nil
}

// validateExportHostFunctions checks that every export's declared host
// functions are a subset of the host-function allowlist, reusing the
// loader's validateDeclaredHostFunctions gate so the describe()-declared
// host functions are checked against the single allowlist the loader
// already enforces. The check runs per export so the rejection message
// names the offending export.
func (d PluginDescriptor) validateExportHostFunctions(pluginIdentity string) error {
	for _, export := range d.Exports {
		if len(export.HostFunctions) == 0 {
			continue
		}
		spec := PluginSpec{
			Name:                  fmt.Sprintf("%s/%s", pluginIdentity, export.Name),
			DeclaredHostFunctions: export.HostFunctions,
		}
		if err := validateDeclaredHostFunctions(spec); err != nil {
			return err
		}
	}
	return nil
}

// hasExport reports whether the descriptor declares an export with the
// given name.
func (d PluginDescriptor) hasExport(name string) bool {
	for _, export := range d.Exports {
		if export.Name == name {
			return true
		}
	}
	return false
}

// hasExportWithPrefix reports whether the descriptor declares any export
// whose name begins with the given prefix.
func (d PluginDescriptor) hasExportWithPrefix(prefix string) bool {
	for _, export := range d.Exports {
		if len(export.Name) >= len(prefix) && export.Name[:len(prefix)] == prefix {
			return true
		}
	}
	return false
}

// defaultInputPattern returns the input pattern the host applies to an
// export that omits an explicit input_pattern. The defaults: provider
// render_node and assemble_boundary take the full inline payload;
// linter lint_* exports take the full inline payload; a
// feature-recipe-pipeline step takes the minimal payload. Any
// export-declared input_pattern overrides this default — the caller
// reads the declared value first and falls back here only when it is
// empty.
func defaultInputPattern(pluginType PluginType, exportName string) InputPattern {
	if pluginType == PluginTypeFRPStep && exportName == frpStepExportName {
		return InputPatternMinimal
	}
	return InputPatternFull
}
