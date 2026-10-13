// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package wasm

import (
	"fmt"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/pkg/plugintype"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// The descriptor wire types — PluginDescriptor and its nested PluginExport,
// PluginCLICommand (with its per-command PluginCommandFlag list), ProviderContract
// and that contract's nested InputKinds, ArtifactDelimiters, and
// AssemblyNameAnnotation, plus PluginDescriptorMeta — are generated with their
// streaming JSON codec from the descriptor schema into the companion generated
// file. The host decodes a guest's describe() output through that codec, so the
// boundary shape check is the typed decode itself. An optional nested object the
// guest omits decodes to a nil pointer, so an absent contract, delimiter pair,
// assembly-name annotation, or metadata block is distinguishable from a present
// empty one.
//
// This file holds the host contract over that generated shape: the Validate
// entry point and its per-check helpers, the settings-schema validation, and the
// typed enum vocabularies (plugin type, input pattern, stability) plus the export
// and boundary-assembly name constants the checks compare the decoded string
// fields against.

// PluginType is the kind of a Wasm plugin. It selects the
// minimum-export rule the host enforces and the default input pattern
// for an export. It is an alias for the canonical plugin-type vocabulary
// so the host and the low-level registry name one type, not two.
type PluginType = plugintype.Type

const (
	// PluginTypeProvider is a provider plugin: it renders IR nodes into
	// provider-native artifacts. Its minimum export is render_node.
	PluginTypeProvider = plugintype.Provider

	// PluginTypeLinter is a linter plugin: it inspects authored content
	// and raises diagnostics. Its minimum surface is at least one
	// lint_* export.
	PluginTypeLinter = plugintype.Linter

	// PluginTypePipelineStep is a feature-recipe-pipeline step plugin: it
	// transforms the IR within a feature recipe's pipeline. Its minimum
	// export is step.
	PluginTypePipelineStep = plugintype.PipelineStep

	// PluginTypeGovernance is a governance plugin: it serves the build-pipeline
	// transform and verdict roles. Attached at a transform role it auto-tags
	// topology-graph node labels (declaring mutate); attached at a verdict role it
	// renders an accept/reject verdict over the IR graph or the rendered artifacts
	// (declaring verdict_ir over the IR graph, verdict_render over the rendered
	// artifacts). The role it is attached at, not its type, fixes what it may emit.
	// The role also supplies the export a governance attachment dispatches when it
	// names no entry function of its own, so a guest declaring only one role's
	// export cannot serve another role by default -- but an attachment that does
	// name an entry function dispatches that one instead.
	PluginTypeGovernance = plugintype.Governance

	// PluginTypeCLI is a CLI plugin: it contributes a runtime aggrega <cmd>
	// subcommand, invoked by an operator at the command line rather than by
	// the build pipeline. Its minimum export is cli_run.
	PluginTypeCLI = plugintype.CLI

	// PluginTypeParser is a parser plugin: it turns a foreign source text into
	// a canonical structured tree, so any consumer can read the tree without
	// carrying its own language parser. It advertises the formats it handles as
	// parse:<format> capabilities. Its minimum export is parse.
	PluginTypeParser = plugintype.Parser

	// PluginTypeImporter is an importer plugin: it turns a foreign infrastructure
	// or agent-definition source into draft Aggrega authoring files. It advertises
	// the formats it handles as import:<format> capabilities. Its minimum export
	// is import.
	PluginTypeImporter = plugintype.Importer
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

// minExportName returns the minimum-export name or prefix a plugin type
// declares, read from the canonical plugin-type registry so the literal lives
// once. It panics if the type is absent from the registry — a static
// programming error, since the caller passes a known plugin-type constant.
func minExportName(t plugintype.Type) string {
	e, ok := plugintype.Lookup(t)
	if !ok {
		panic("wasm: no plugin-type registry entry for " + string(t))
	}
	return e.MinExport.Name
}

// lintExportPrefix is the prefix a linter plugin's rule exports carry;
// a linter satisfies its minimum-export rule with at least one export
// whose name begins with this prefix. It reads the prefix from the canonical
// plugin-type registry so the literal lives once.
var lintExportPrefix = minExportName(plugintype.Linter)

// The exports the host calls a guest with a payload — the governance roles'
// defaults (TransformAnnotationsExport, MutateExport, VerdictIRExport,
// VerdictRenderExport), the importer's ImportExport, the parser's ParseExport,
// the CLI runner's CLIRunExport, the pipeline step's StepExport, the linter's
// LintRulesExport and the provider's render family, the cross-boundary pair
// LinkPublishExport and LinkResolveExport among them — are declared in the
// generated host-export file beside this one, one [HostExport] per name the
// plugin-type registry's host-export lists hold. This file's checks read them
// from there, so the minimum-export rule, the link-pair coherence rule and the
// export the host dispatches to read one name.
//
// The link pair is a paired contract: link_publish produces the wiring a
// consumed endpoint's output needs and the published identity a consumer
// resolves against; link_resolve produces the wiring that injects the reference
// into the consuming endpoint's output. A provider that wires cross-boundary
// edges declares both, and the load gate cross-checks the pair against the
// plugin's cross-boundary-linking capability (see [PluginDescriptor.Validate]).
//
// The two verdict roles have one name each rather than one between them, because
// each carries its own payload shape and the export name is what fixes which
// shape a caller is holding.

// BoundaryAssemblyNone, BoundaryAssemblyMerge, and BoundaryAssemblyNest are
// the three boundary-assembly modes a plugin reports via
// [ProviderContract.BoundaryAssembly]: none writes each node's output directly at
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
//     feature-recipe-pipeline step declares step; a CLI plugin declares
//     cli_run; a parser declares parse; an importer declares import. A
//     governance plugin has no single minimum export — the required export is
//     the attachment role's (mutate for a transform role, verdict_ir or
//     verdict_render for a verdict role), enforced at dispatch — so the load gate
//     admits it on any role.
//   - every export's declared host functions are a subset of the
//     host-function allowlist. This routes through the same
//     validateHostFunctionsSubset gate the loader applies to the set
//     derived from a .wasm, so describe()-declared host functions are
//     checked against the single allowlist — no second list, no second gate.
//   - every export's capability fields are well-shaped: operates is
//     patch, emit, or empty; scope is own, host, or empty. This is shape
//     validation only — the host does not check provider compatibility,
//     require a capability per plugin type, or act on the values. The
//     runtime enforces compatible providers and acts on operates/scope
//     when it dispatches a step; here the values are checked for shape
//     and otherwise piped through.
//   - the provider render-behavior contract block is well-shaped: boundary_assembly
//     is none, merge, nest, or empty, and a declared assembly_name_annotation
//     names a non-empty annotation key. This is shape validation only — the
//     host does not require the block, cross-check it against the plugin
//     type, or act on its values here; the load gate cross-checks it against
//     the entity's authored claim, and other input fields are piped through.
//   - the cross-boundary-linking capability is coherent with the export pair:
//     the cross_boundary_linking flag is set if and only if the descriptor
//     declares both the link_publish and link_resolve exports. A flag without
//     both exports, or both exports without the flag, is refused at load.
//   - every declared CLI command names a command: each entry in commands
//     carries a non-empty name. This is shape validation only — the gate does
//     not require any command to be present per plugin type (a CLI plugin's
//     command presence is enforced at install), and does not act on the value.
//   - a declared settings_schema compiles as a JSON Schema. This is shape
//     validation only — the gate confirms the schema is well-formed; it does
//     not cross-check the schema against the plugin type or validate any
//     settings value here. Settings values are validated at dispatch against
//     the compiled schema (see [PluginDescriptor.ValidateSettings]).
//   - a provider's served capability names, and a pipeline step's compatible
//     provider capabilities, carry no colon. A Recipe's capability reference is
//     split at its last colon into the name and a version pin, so a provider
//     serving a colon-bearing name could never be selected, and a step listing
//     one could never match the provider rendering a scope. A parser's and an
//     importer's capabilities are not judged: `parse:<format>` and
//     `import:<format>` carry the colon as part of the name, and they are
//     resolved by format, never through a Recipe's reference.
//
// pluginIdentity names the plugin in the rejection message. The caller
// passes whatever stable identity it holds; the field is plumbed so the
// message contract is met without this surface inventing an identity
// scheme of its own.
func (d PluginDescriptor) Validate(pluginIdentity string) error {
	if d.AbiVersion != ABIVersionV1Alpha1 {
		return fmt.Errorf("plugin %q declares abi_version %q; host speaks %q: %w",
			pluginIdentity, d.AbiVersion, ABIVersionV1Alpha1, ErrWasmManifestRejected())
	}

	// Membership is asked of the canonical plugin-type registry rather than
	// re-listed here, so the gate and the registry cannot disagree about what the
	// type set is. A type the registry does not carry is one the published
	// plugin_type vocabulary omits and the host could never dispatch, so the
	// descriptor is refused.
	if _, ok := plugintype.Lookup(PluginType(d.PluginType)); !ok {
		return fmt.Errorf("plugin %q declares unknown plugin_type %q: %w",
			pluginIdentity, d.PluginType, ErrWasmManifestRejected())
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

	if err := d.validateContract(pluginIdentity); err != nil {
		return err
	}

	if err := d.validateSettingsSchema(pluginIdentity); err != nil {
		return err
	}

	if err := d.validateCommands(pluginIdentity); err != nil {
		return err
	}

	if err := d.validateCapabilityNames(pluginIdentity); err != nil {
		return err
	}

	return d.validateLinkCapability(pluginIdentity)
}

// validateCapabilityNames refuses a colon in a capability name a Recipe's
// capability reference has to be able to spell: the capabilities a provider
// serves, and the provider capabilities a pipeline step is compatible with. The
// reference is split at its last colon into the capability name and a version
// pin, so the name it carries never contains one — a provider serving
// `helm:1` could be asked for by no Recipe, and a step compatible with it
// matches no provider a scope renders through. A parser's `parse:<format>` and
// an importer's `import:<format>` are not read here: the colon is part of those
// names, and they are resolved by format rather than through a Recipe's
// reference. A returned error wraps [ErrWasmManifestRejected] and names
// pluginIdentity, the plugin type, and the offending name.
func (d PluginDescriptor) validateCapabilityNames(pluginIdentity string) error {
	var what string
	switch PluginType(d.PluginType) {
	case PluginTypeProvider:
		what = "serves the capability"
	case PluginTypePipelineStep:
		what = "declares compatibility with the provider capability"
	default:
		return nil
	}
	for _, capability := range d.Capabilities {
		if strings.Contains(capability, ":") {
			return fmt.Errorf("plugin %q (plugin_type %q) %s %q, which contains a colon; a Recipe's capability reference is read as the capability up to its last colon and a version pin after it, so no Recipe can name %q: report the capability name without a colon: %w",
				pluginIdentity, d.PluginType, what, capability, capability, ErrWasmManifestRejected())
		}
	}
	return nil
}

// validateCommands shape-checks the descriptor's declared CLI commands: every
// entry in commands must carry a non-empty name. This is shape validation only
// — it does not require any command to be present (a CLI plugin's command
// presence is enforced at install) and does not act on the values. A returned
// error wraps [ErrWasmManifestRejected] and names pluginIdentity and the
// offending command index.
func (d PluginDescriptor) validateCommands(pluginIdentity string) error {
	for i, command := range d.Commands {
		if command.Name == "" {
			return fmt.Errorf("plugin %q declares a command at index %d with an empty name: %w",
				pluginIdentity, i, ErrWasmManifestRejected())
		}
	}
	return nil
}

// validateLinkCapability enforces the cross-boundary-linking coherence rule:
// the [ProviderContract.CrossBoundaryLinking] flag is set if and only if the
// descriptor declares both the link_publish and link_resolve exports. The two
// exports are a paired contract, and the flag is the plugin-level claim that
// the pair is present; a declaration that sets the flag without both exports,
// or declares both exports without the flag, is incoherent and refused at load
// rather than mis-routed at dispatch. A returned error wraps
// [ErrWasmManifestRejected] and names pluginIdentity and the specific
// inconsistency.
func (d PluginDescriptor) validateLinkCapability(pluginIdentity string) error {
	bothExports := d.hasExport(LinkPublishExport.Name()) && d.hasExport(LinkResolveExport.Name())
	crossBoundaryLinking := d.Contract != nil && d.Contract.CrossBoundaryLinking
	switch {
	case crossBoundaryLinking && !bothExports:
		return fmt.Errorf("plugin %q declares the cross-boundary linking capability but does not export both %q and %q: %w",
			pluginIdentity, LinkPublishExport, LinkResolveExport, ErrWasmManifestRejected())
	case !crossBoundaryLinking && bothExports:
		return fmt.Errorf("plugin %q exports both %q and %q but does not declare the cross-boundary linking capability: %w",
			pluginIdentity, LinkPublishExport, LinkResolveExport, ErrWasmManifestRejected())
	}
	return nil
}

// validateContract shape-checks the provider render-behavior contract block:
// boundary_assembly must be none, merge, nest, or empty, and a declared
// assembly_name_annotation must name a non-empty annotation key. This is shape
// validation only — it does not require the block, cross-check it against the
// plugin type or its compatible providers, or act on its values. The load gate
// cross-checks the block against the entity's authored claim; the other input
// fields are piped through unchecked here.
func (d PluginDescriptor) validateContract(pluginIdentity string) error {
	if d.Contract == nil {
		return nil
	}
	switch d.Contract.BoundaryAssembly {
	case "", BoundaryAssemblyNone, BoundaryAssemblyMerge, BoundaryAssemblyNest:
		// well-shaped
	default:
		return fmt.Errorf("plugin %q declares unknown boundary_assembly %q (want %q, %q, %q, or empty): %w",
			pluginIdentity, d.Contract.BoundaryAssembly,
			BoundaryAssemblyNone, BoundaryAssemblyMerge, BoundaryAssemblyNest, ErrWasmManifestRejected())
	}
	if a := d.Contract.AssemblyNameAnnotation; a != nil && a.Annotation == "" {
		return fmt.Errorf("plugin %q declares an assembly_name_annotation with an empty annotation key: %w",
			pluginIdentity, ErrWasmManifestRejected())
	}
	return nil
}

// validateExportCapabilities shape-checks the per-export capability
// fields: operates must be patch, emit, or empty; scope must be own,
// host, or empty. selector is free-form and unchecked. This is shape
// validation only — it does not require a capability per plugin type,
// does not cross-check operates/scope against the plugin type or its
// compatible providers, and does not consult a provider registry. The
// runtime enforces compatibility and acts on these values when it
// dispatches a step; this load-time gate only rejects a malformed value.
func (d PluginDescriptor) validateExportCapabilities(pluginIdentity string) error {
	for _, export := range d.Exports {
		switch export.Operates {
		case "", operatesPatch, operatesEmit:
			// well-shaped
		default:
			return fmt.Errorf("plugin %q export %q declares unknown operates %q (want %q, %q, or empty): %w",
				pluginIdentity, export.Name, export.Operates, operatesPatch, operatesEmit, ErrWasmManifestRejected())
		}
		switch export.Scope {
		case "", ScopeOwn, ScopeHost:
			// well-shaped
		default:
			return fmt.Errorf("plugin %q export %q declares unknown scope %q (want %q, %q, or empty): %w",
				pluginIdentity, export.Name, export.Scope, ScopeOwn, ScopeHost, ErrWasmManifestRejected())
		}
	}
	return nil
}

// validateMinimumExports enforces the per-plugin-type minimum-export
// rule, returning an ErrWasmManifestRejected-wrapped error naming the
// missing export when the rule is unmet. It reads the rule — the required
// export name or name prefix, and the human-facing type label — from the
// canonical plugin-type registry rather than re-spelling one case per type,
// so the gate and the registry can never disagree on what each type requires.
// A type absent from the registry, or one whose registry entry declares no rule,
// imposes no minimum-export requirement here (an unrecognized plugin_type is
// rejected earlier in Validate).
func (d PluginDescriptor) validateMinimumExports(pluginIdentity string) error {
	entry, ok := plugintype.Lookup(PluginType(d.PluginType))
	if !ok || entry.MinExport.Name == "" {
		return nil
	}
	rule := entry.MinExport
	if rule.IsPrefix {
		if !d.hasExportWithPrefix(rule.Name) {
			return fmt.Errorf("%s plugin %q declares no %s* export: %w",
				entry.Label, pluginIdentity, rule.Name, ErrWasmManifestRejected())
		}
		return nil
	}
	if !d.hasExport(rule.Name) {
		return fmt.Errorf("%s plugin %q declares no %q export: %w",
			entry.Label, pluginIdentity, rule.Name, ErrWasmManifestRejected())
	}
	return nil
}

// validateExportHostFunctions checks that every export's declared host
// functions are a subset of the host-function allowlist for the plugin's
// profile, reusing the loader's subset gates so the describe()-declared host
// functions are checked against the same allowlist the loader enforces at load.
// The check runs per export so the rejection message names the offending export.
//
// The allowlist depends on the plugin type: a CLI plugin runs under the runtime
// terminal profile, so its exports may declare the streaming emitters and are
// checked against the CLI union gate; every other plugin type runs under the
// build profile and is checked against the shared gate, which admits neither
// streaming name. This mirrors the load-time split between Loader.LoadCLI and
// Loader.Load, so a describe()-declared host function cannot pass the descriptor
// gate and then fail the derived-import gate (or vice versa).
func (d PluginDescriptor) validateExportHostFunctions(pluginIdentity string) error {
	subset := validateHostFunctionsSubset
	if PluginType(d.PluginType) == PluginTypeCLI {
		subset = validateHostFunctionsSubsetCLI
	}
	for _, export := range d.Exports {
		if len(export.HostFunctions) == 0 {
			continue
		}
		if err := subset(fmt.Sprintf("%s/%s", pluginIdentity, export.Name), d.PluginType, export.HostFunctions); err != nil {
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
	if pluginType == PluginTypePipelineStep && exportName == StepExport.Name() {
		return InputPatternMinimal
	}
	return InputPatternFull
}
