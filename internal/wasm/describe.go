// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package wasm

import (
	"fmt"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// PluginDescriptor is the output a Wasm plugin returns from its
// describe() export — the plugin's self-declaration of the ABI version
// it binds against, its plugin type, the provider engines it is
// compatible with, the exports it offers (each with its capability
// declaration), a plugin-level capability self-report, and optional
// authorship metadata. The host reads describe() once when it loads a
// plugin, validates the declaration (see [PluginDescriptor.Validate]),
// and uses the per-export declarations to bind the least-privilege host
// surface each export needs before invoking it.
//
// describe() is cross-plugin-type: one shape covers provider, linter,
// feature-recipe-pipeline, mutator, and policy-validator plugins. It is consumed by the host
// loader for dispatch decisions, not emitted into rendered output, so
// its slices carry author/declared order without an OrderedMap; a
// future consumer that marshals a descriptor into a deterministic
// on-disk artifact relies on encoding/json's sorted object keys plus
// these declared-order slices.
//
// describe() is on the live plugin-load path: for every guest it loads,
// the host calls the guest's describe() export, unmarshals the returned
// bytes into this shape, and validates them (see
// [PluginDescriptor.Validate]) before dispatching any other export. A
// descriptor the host refuses fails the load rather than being
// mis-invoked at dispatch. The same shape and validator run in the test
// suite over crafted bytes, so the parse-and-validate behaviour is
// exercised with and without a running guest.
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

	// PluginType is the plugin's kind — provider, linter,
	// feature-recipe-pipeline step, mutator, or policy-validator. It
	// selects the minimum-export rule the host validates and, with the
	// export name, the default input pattern.
	PluginType PluginType `json:"plugin_type"`

	// Targets is what the plugin applies to, interpreted per plugin type:
	// for a provider plugin, the render targets (provider engine names such
	// as "helm" or "compose") whose output shape it renders; for an frp_step
	// plugin, the provider scopes its step is compatible with; the
	// cross-cutting sockets leave it empty. Entries are in declared order.
	// The list is an explicit, tested-compatibility claim rather than an
	// implicit "any similar target". The runtime enforces it — for each
	// scope a pipeline step touches, the provider rendering that scope's
	// documents must appear here — but this load-time validator does not:
	// entries are unchecked strings here, since no provider registry exists
	// at the load boundary to check them against. An empty or absent list
	// declares no target claim, so it matches nothing and the runtime halts
	// any pipeline step that uses the plugin.
	//
	// The wire tag is the frozen ABI name the guest emits in describe(); the
	// Go field name follows the entity-side target vocabulary.
	Targets []string `json:"compatible_providers,omitempty"`

	// Exports lists the plugin's offered exports in declared order. The
	// host validates that the per-type minimum export is present and
	// binds the per-export host surface before invoking each.
	Exports []PluginExport `json:"exports"`

	// Capabilities is the plugin's plugin-level capability self-report —
	// the input kinds it renders, how it assembles a dispatch group's
	// per-node output, the artifact-template delimiter pair it applies,
	// and any source-file restriction. Unlike the per-export declarations,
	// it describes the plugin as a whole. The load gate cross-checks it
	// against the entity's authored claim; an all-zero value carries no
	// plugin-level capability claim.
	Capabilities Capabilities `json:"capabilities,omitempty,omitzero"`

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
	// Every name MUST be in the host-function allowlist (see hostfuncs.go).
	// The declaration is authoritative and checked against the binary: the
	// load gate cross-checks the union of host functions declared across the
	// plugin's exports against the set the guest actually imports from its
	// .wasm, halting on any name declared but not imported or imported but
	// not declared, so this list cannot drift from the functions the guest
	// really calls. The host binds each imported allowlisted function,
	// giving the guest the least-privilege host surface its imports declare.
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

// Capabilities is a plugin's self-declaration of the compile-time render
// behaviors it performs across the nodes it renders — the input kinds it
// accepts, whether and how it assembles a dispatch group's per-node output
// into a single artifact, the Go-template delimiter pair it applies to
// imported artifact files, and any restriction on which source files it
// consumes. It is a plugin-level self-report: it describes the plugin as a
// whole, not any one export. The host reads it from describe() and the load
// gate cross-checks it against the entity's authored claim.
type Capabilities struct {
	// InputKinds reports which of the three Recipe input kinds the plugin
	// renders: a sourced file set, a synthesized workload, and a standalone
	// artifacts-only node.
	InputKinds InputKinds `json:"input_kinds"`

	// BoundaryAssembly reports how the plugin combines a dispatch group's
	// per-node render output: "none" writes each node's output directly at the
	// dispatch path, "merge" folds the per-node fragments into one document,
	// and "nest" places each node's output under its own position in an
	// umbrella structure. An empty value carries no assembly claim.
	BoundaryAssembly string `json:"boundary_assembly"`

	// ArtifactDelimiters reports the Go-template delimiter pair the plugin
	// applies to imported artifact files. Absent means the default "{{"/"}}"
	// pair; a plugin whose target syntax collides with that pair reports an
	// alternate pair here.
	ArtifactDelimiters *ArtifactDelimiters `json:"artifact_delimiters,omitempty"`

	// SourceFilter reports the source-file paths the plugin consumes when a
	// node supplies a file set. Absent means the plugin consumes every file in
	// the set.
	SourceFilter []string `json:"source_filter,omitempty"`

	// AssemblyNameAnnotation reports a boundary annotation whose resolved value
	// names the artifact the plugin assembles from a dispatch group. A plugin
	// that folds a group's contributing boundaries into one named assembly
	// declares the annotation key it reads and whether every contributing
	// boundary must agree on that annotation's resolved value; absent means the
	// plugin names its assembly from the dispatch path alone. The host reads it
	// from describe() and the load gate cross-checks it against the entity's
	// authored claim.
	AssemblyNameAnnotation *AssemblyNameAnnotation `json:"assembly_name_annotation,omitempty"`

	// ReusableInstance reports whether one loaded plugin instance may serve
	// every render, assemble, and validate crossing of the build rather than a
	// fresh instance per crossing. A plugin declares it true only when it holds
	// no state across crossings — a pure function of its per-crossing input — so
	// a single instance produces byte-identical output whether reused or freshly
	// loaded. Absent (false) is the safe default: the host loads a fresh instance
	// per crossing, so a plugin that carries state is isolated by construction.
	ReusableInstance bool `json:"reusable_instance,omitempty"`

	// CrossBoundaryLinking reports whether the plugin wires cross-boundary uses
	// edges — edges whose consuming and consumed ends assemble into different
	// dispatch groups — by producing provider-native cross-reference wiring
	// through its link_publish and link_resolve exports. A plugin declares it
	// true only when it exports both; the load gate rejects a declaration that
	// does not match the export pair (see [PluginDescriptor.Validate]). Absent
	// (false) is the safe default: the plugin wires no cross-boundary edges, and
	// the host does not route link crossings to it.
	CrossBoundaryLinking bool `json:"cross_boundary_linking,omitempty"`
}

// InputKinds reports which Recipe input kinds a plugin renders. Each field is
// an independent claim; a plugin may report any combination.
type InputKinds struct {
	// Sourced reports that the plugin renders a sourced file set (a chart, a
	// module, a compose file).
	Sourced bool `json:"sourced"`
	// Synthesized reports that the plugin renders a synthesized workload.
	Synthesized bool `json:"synthesized"`
	// ArtifactsStandalone reports that the plugin renders a standalone
	// artifacts-only node — one carrying imported artifact documents and no
	// source or workload.
	ArtifactsStandalone bool `json:"artifacts_standalone"`
}

// ArtifactDelimiters is the Go-template delimiter pair a plugin applies to
// imported artifact files, reported when it differs from the default
// "{{"/"}}" pair.
type ArtifactDelimiters struct {
	// Left is the opening delimiter.
	Left string `json:"left"`
	// Right is the closing delimiter.
	Right string `json:"right"`
}

// AssemblyNameAnnotation is the boundary annotation whose resolved value names a
// plugin's assembled artifact, reported when the plugin folds a dispatch group's
// contributing boundaries into one named assembly.
type AssemblyNameAnnotation struct {
	// Annotation is the boundary annotation key the plugin reads the assembled
	// artifact's name from.
	Annotation string `json:"annotation"`
	// RequireUnique reports whether every boundary contributing to one dispatch
	// group must agree on the annotation's resolved value. When true, a group
	// whose contributing boundaries resolve the annotation to different values
	// halts the build; when false, the plugin accepts the first resolved value.
	RequireUnique bool `json:"require_unique,omitempty"`
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

	// PluginTypeMutator is a mutator plugin: it auto-tags topology-graph
	// node labels at Step 2, returning JSON Patch documents the host applies
	// to a labels-only projection of the graph. Its minimum export is mutate.
	PluginTypeMutator PluginType = "mutator"

	// PluginTypePolicyValidator is a policy-validator plugin: it evaluates the
	// full IR graph at Step 4 and returns one pass/fail verdict per rule; a
	// failed rule halts the build. Its minimum export is validate.
	PluginTypePolicyValidator PluginType = "policy-validator"
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

// MutateExport and ValidateExport are the default entry-function names for
// the mutator and policy-validator sockets. They are the single source for
// both the minimum-export rule this gate enforces and the default a socket
// applies when a plugin attachment leaves its entry function unset, so the
// two sides read one name rather than each re-spelling the literal.
const (
	MutateExport   = "mutate"
	ValidateExport = "validate"
)

// LinkPublishExport and LinkResolveExport are the two cross-boundary
// edge-linking exports a provider plugin declares as a pair. link_publish
// produces the wiring a consumed endpoint's output needs and the published
// identity a consumer resolves against; link_resolve produces the wiring that
// injects the reference into the consuming endpoint's output. They are a paired
// contract — a provider that wires cross-boundary edges declares both, and the
// load gate cross-checks the pair against the plugin's cross-boundary-linking
// capability (see [PluginDescriptor.Validate]). They are the single source for
// both the coherence rule this gate enforces and the export names the host
// dispatches to.
const (
	LinkPublishExport = "link_publish"
	LinkResolveExport = "link_resolve"
)

// providerRenderExportName is the required export name for a provider
// plugin.
const providerRenderExportName = "render_node"

// BoundaryAssemblyNone, BoundaryAssemblyMerge, and BoundaryAssemblyNest are
// the three boundary-assembly modes a plugin reports via
// [Capabilities.BoundaryAssembly]: none writes each node's output directly at
// the dispatch path, merge folds the per-node fragments into one document, and
// nest places each node's output under its own position in an umbrella
// structure. The shape validator accepts these three values or the empty
// string; it does not require the field or act on its value.
const (
	BoundaryAssemblyNone  = "none"
	BoundaryAssemblyMerge = "merge"
	BoundaryAssemblyNest  = "nest"
)

// operatesPatch and operatesEmit are the two pipeline operations an
// export may declare via [PluginExport.Operates]. The shape validator
// accepts these two values or the empty string; it does not require the
// field or act on its value — the runtime reads it when dispatching. The
// values are the schema-generated Plugin operates vocabulary, re-exported
// here so this load-time gate and the entity authoring surface read one set of
// values rather than each re-spelling the literals.
const (
	operatesPatch = types.OperatesPatch
	operatesEmit  = types.OperatesEmit
)

// ScopeOwn and ScopeHost are the two document roots an export may declare
// via [PluginExport.Scope]. The shape validator accepts these two values
// or the empty string; it does not require the field or act on its value
// — the runtime resolves the scope's documents when dispatching, and the
// renderer's pre-walk provider-compatibility check reads ScopeHost to decide
// which provider an export's scope is checked against. Exported so the single
// scope identity is shared between this load-time shape gate and the runtime
// enforcement rather than each side re-spelling the literal. The values are the
// schema-generated Plugin scope vocabulary, re-exported here so this gate,
// the renderer, and the entity authoring surface read one set of values.
const (
	ScopeOwn  = types.ScopeOwn
	ScopeHost = types.ScopeHost
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
//     feature-recipe-pipeline step declares step; a mutator declares
//     mutate; a policy-validator declares validate.
//   - every export's declared host functions are a subset of the
//     host-function allowlist. This routes through the same
//     validateHostFunctionsSubset gate the loader applies to the set
//     derived from a .wasm, so describe()-declared host functions are
//     checked against the single allowlist — no second list, no second gate.
//   - every export's capability fields are well-shaped: operates is
//     patch, emit, or empty; scope is own, host, or empty. This is shape
//     validation only — the host does not check provider compatibility,
//     require a capability per plugin type, or act on the values. The
//     runtime enforces targets and acts on operates/scope
//     when it dispatches a step; here the values are checked for shape
//     and otherwise piped through.
//   - the plugin-level capabilities block is well-shaped: boundary_assembly
//     is none, merge, nest, or empty, and a declared assembly_name_annotation
//     names a non-empty annotation key. This is shape validation only — the
//     host does not require the block, cross-check it against the plugin
//     type, or act on its values here; the load gate cross-checks it against
//     the entity's authored claim, and other input fields are piped through.
//   - the cross-boundary-linking capability is coherent with the export pair:
//     the cross_boundary_linking flag is set if and only if the descriptor
//     declares both the link_publish and link_resolve exports. A flag without
//     both exports, or both exports without the flag, is refused at load.
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
	case PluginTypeProvider, PluginTypeLinter, PluginTypeFRPStep, PluginTypeMutator, PluginTypePolicyValidator:
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

	if err := d.validateExportCapabilities(pluginIdentity); err != nil {
		return err
	}

	if err := d.validateCapabilities(pluginIdentity); err != nil {
		return err
	}

	return d.validateLinkCapability(pluginIdentity)
}

// validateLinkCapability enforces the cross-boundary-linking coherence rule:
// the [Capabilities.CrossBoundaryLinking] flag is set if and only if the
// descriptor declares both the link_publish and link_resolve exports. The two
// exports are a paired contract, and the flag is the plugin-level claim that
// the pair is present; a declaration that sets the flag without both exports,
// or declares both exports without the flag, is incoherent and refused at load
// rather than mis-routed at dispatch. A returned error wraps
// [ErrWasmManifestRejected] and names pluginIdentity and the specific
// inconsistency.
func (d PluginDescriptor) validateLinkCapability(pluginIdentity string) error {
	bothExports := d.hasExport(LinkPublishExport) && d.hasExport(LinkResolveExport)
	switch {
	case d.Capabilities.CrossBoundaryLinking && !bothExports:
		return fmt.Errorf("plugin %q declares the cross-boundary linking capability but does not export both %q and %q: %w",
			pluginIdentity, LinkPublishExport, LinkResolveExport, ErrWasmManifestRejected)
	case !d.Capabilities.CrossBoundaryLinking && bothExports:
		return fmt.Errorf("plugin %q exports both %q and %q but does not declare the cross-boundary linking capability: %w",
			pluginIdentity, LinkPublishExport, LinkResolveExport, ErrWasmManifestRejected)
	}
	return nil
}

// validateCapabilities shape-checks the plugin-level capabilities block:
// boundary_assembly must be none, merge, nest, or empty, and a declared
// assembly_name_annotation must name a non-empty annotation key. This is shape
// validation only — it does not require the block, cross-check it against the
// plugin type or its targets, or act on its values. The load gate
// cross-checks the block against the entity's authored claim; the other input
// fields are piped through unchecked here.
func (d PluginDescriptor) validateCapabilities(pluginIdentity string) error {
	switch d.Capabilities.BoundaryAssembly {
	case "", BoundaryAssemblyNone, BoundaryAssemblyMerge, BoundaryAssemblyNest:
		// well-shaped
	default:
		return fmt.Errorf("plugin %q declares unknown boundary_assembly %q (want %q, %q, %q, or empty): %w",
			pluginIdentity, d.Capabilities.BoundaryAssembly,
			BoundaryAssemblyNone, BoundaryAssemblyMerge, BoundaryAssemblyNest, ErrWasmManifestRejected)
	}
	if a := d.Capabilities.AssemblyNameAnnotation; a != nil && a.Annotation == "" {
		return fmt.Errorf("plugin %q declares an assembly_name_annotation with an empty annotation key: %w",
			pluginIdentity, ErrWasmManifestRejected)
	}
	return nil
}

// validateExportCapabilities shape-checks the per-export capability
// fields: operates must be patch, emit, or empty; scope must be own,
// host, or empty. selector is free-form and unchecked. This is shape
// validation only — it does not require a capability per plugin type,
// does not cross-check operates/scope against the plugin type or its
// targets, and does not consult a provider registry. The
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
	case PluginTypeMutator:
		if !d.hasExport(MutateExport) {
			return fmt.Errorf("mutator plugin %q declares no %q export: %w",
				pluginIdentity, MutateExport, ErrWasmManifestRejected)
		}
	case PluginTypePolicyValidator:
		if !d.hasExport(ValidateExport) {
			return fmt.Errorf("policy-validator plugin %q declares no %q export: %w",
				pluginIdentity, ValidateExport, ErrWasmManifestRejected)
		}
	}
	return nil
}

// validateExportHostFunctions checks that every export's declared host
// functions are a subset of the host-function allowlist, reusing the
// loader's validateHostFunctionsSubset gate so the describe()-declared
// host functions are checked against the single allowlist the loader
// already enforces. The check runs per export so the rejection message
// names the offending export.
func (d PluginDescriptor) validateExportHostFunctions(pluginIdentity string) error {
	for _, export := range d.Exports {
		if len(export.HostFunctions) == 0 {
			continue
		}
		if err := validateHostFunctionsSubset(fmt.Sprintf("%s/%s", pluginIdentity, export.Name), export.HostFunctions); err != nil {
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
