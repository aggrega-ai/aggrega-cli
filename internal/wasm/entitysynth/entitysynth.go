// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package entitysynth assembles a Plugin catalog entity from a Wasm guest's
// describe() self-report. The routing-relevant contract fields — pluginType,
// abiVersion, capabilities, exports, and the provider render-behavior contract —
// are always machine-derived from the descriptor, so a synthesized entity can
// never promise a capability the binary does not provide.
//
// MergeForPublish assembles a publish-ready entity by merging the descriptor
// with an authored overlay (identity, runtime limits, provider block), the
// guest's describe() self-report version, the machine-derived host-function set,
// and the guest's plugin-level description. The descriptor-to-routing-field
// assembly it shares
// with the descriptor helpers lives in exactly one place (applyDescriptorContract),
// so a synthesized entity's routing fields can never drift from the descriptor.
package entitysynth

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// MergeForPublish assembles a publish-ready Plugin entity from the describe()
// contract fields (machine-derived — the compiler-checked truth of what the
// guest does), the host-function set derived from the guest's own .wasm import
// section (also machine-derived), the command mirror read from the guest's
// describe() self-report (machine-derived — the top-level command verbs a CLI
// guest contributes, in describe()-declared order), the authored overlay
// (identity, runtime limits, provider block, and any authored metadata), and the
// guest's describe() self-report version. The plugin-level description, when
// present, becomes metadata.description. The contract fields, the host-function
// set, and the command mirror always come from the binary, overriding any the
// overlay carries, so the binary is the single source for the guest's declared
// behavior; the overlay supplies only what the binary does not report.
//
// A versioned entity requires a version, so an unstamped guest — one whose
// describe() reports no build-provenance version — halts rather than generating
// an entity with no version. identity names the guest in that rejection message
// and in the descriptor-validation rejection message.
func MergeForPublish(desc wasm.PluginDescriptor, description string, hostFunctions []string, overlay types.Plugin, identity string) (types.Plugin, error) {
	if err := desc.Validate(identity); err != nil {
		return types.Plugin{}, err
	}
	if strings.TrimSpace(overlay.Metadata.Name) == "" {
		return types.Plugin{}, fmt.Errorf("overlay for %q has no metadata.name identity", identity)
	}
	if desc.Metadata == nil || strings.TrimSpace(desc.Metadata.Version) == "" {
		return types.Plugin{}, fmt.Errorf("guest %q reports no describe() version; a stamped build-provenance version is required to generate a versioned entity", identity)
	}

	entity := types.Plugin{
		APIVersion: "aggrega.ai/v1alpha1",
		Kind:       "Plugin",
		Metadata:   overlay.Metadata,
		Spec:       types.PluginSpec{},
	}
	// Version is the guest's describe() self-report; description is the guest's
	// plugin-level self-report. Everything else in metadata is authored.
	entity.Metadata.Version = desc.Metadata.Version
	entity.Metadata.Description = description

	// Identity is metadata.name (copied from the overlay above); runtime limits
	// and the provider block are authored. The machine-owned host-function set is
	// derived from the guest's .wasm import section and stamped into the runtime
	// block here, so the generated entity is publish-ready without a separate
	// recording step. The signature pointer is not the generator's to write —
	// signing attaches the signature at push.
	entity.Spec.Runtime = stampRuntimeHostFunctions(overlay.Spec.Runtime, hostFunctions)
	entity.Spec.Provider = overlay.Spec.Provider

	applyDescriptorContract(&entity, desc)
	// The command mirror is a CLI-authoring surface, not a routing field, so it
	// is stamped only on the publish path.
	entity.Spec.Commands = DescriptorCommands(desc.Commands)

	return entity, nil
}

// applyDescriptorContract stamps the machine-derived routing contract fields
// from a descriptor onto entity: the pluginType identity, abiVersion,
// capabilities, exports, and — when the guest reports one — the provider
// render-behavior contract. It is the single place a descriptor maps onto an
// entity's routing fields, so those fields are always the compiler-checked truth
// of what the guest does.
//
// The capability list is the one field whose destination depends on the plugin
// type, and that dependence is deliberate rather than incidental. A descriptor
// reports one flat list; an entity keeps that list inside the per-type block
// that fixes what its entries mean — a render engine a provider serves, a
// provider a pipeline step is compatible with, a source format a parser or an
// importer handles. So the descriptor's own plugin type selects the block, and
// nothing else can: the contract-presence test below cannot stand in for it,
// because a parser reports capabilities and no contract at all. The block
// selection itself is not spelled out here — it lives once on the entity type,
// so this write and every read of a plugin's capabilities resolve the same
// block by the same rule.
func applyDescriptorContract(entity *types.Plugin, desc wasm.PluginDescriptor) {
	entity.Metadata.PluginType = desc.PluginType
	entity.Spec.AbiVersion = desc.AbiVersion
	entity.Spec.SetCapabilitiesFor(desc.PluginType, desc.Capabilities)
	entity.Spec.Exports = descriptorExports(desc.Exports)
	// The entity's provider block is held by value, so there is nothing to
	// allocate before writing into it: a plugin that is not a provider leaves the
	// block zero and its omitzero tag keeps it out of the serialized entity. The
	// guard stays on the guest's side of the mapping — a guest reporting no
	// contract writes none, rather than stamping a zero contract onto the entity.
	if contract := descriptorContract(desc.Contract); contract != nil {
		entity.Spec.Provider.Contract = *contract
	}
}

// stampRuntimeHostFunctions returns the entity's runtime block carrying the
// machine-owned host-function set derived from the guest's .wasm. The set is
// canonical (sorted, de-duplicated), so re-generating over unchanged bytes
// yields byte-identical output.
//
// When the overlay authors a runtime block, its authored limits are preserved
// and the derived set replaces whatever host functions it carried. When the
// overlay authors no runtime block but the guest imports host functions, a
// runtime block is created to hold the record — a validated machine-owned value
// is never silently dropped. When the guest imports nothing and the overlay
// authors no runtime block, the block stays absent; the field is omitempty and
// the publish check treats an absent record as an empty set, so this is
// publish-safe.
func stampRuntimeHostFunctions(rt *types.PluginRuntime, hostFunctions []string) *types.PluginRuntime {
	if rt == nil {
		if len(hostFunctions) == 0 {
			return nil
		}
		return &types.PluginRuntime{HostFunctions: hostFunctions}
	}
	out := *rt
	out.HostFunctions = hostFunctions
	return &out
}

// descriptorExports maps the describe() export declarations onto the entity's
// authored-export shape, preserving declared order and carrying the four fields
// the entity records (name, operates, scope, selector).
func descriptorExports(exports []wasm.PluginExport) []types.PluginExport {
	if len(exports) == 0 {
		return nil
	}
	out := make([]types.PluginExport, 0, len(exports))
	for _, e := range exports {
		out = append(out, types.PluginExport{
			Name:     e.Name,
			Operates: e.Operates,
			Scope:    e.Scope,
			Selector: e.Selector,
		})
	}
	return out
}

// DescriptorCommands maps the describe() command declarations onto the entity's
// machine-owned command mirror, preserving describe()-declared order and carrying
// the command verb and its short-help text. It is the exact reverse of the
// mirror projection the publish verify re-derives from the entity, so a generated
// entity's recorded set matches what the binary describes without a separate
// recording step. Empty input yields nil, so a non-CLI guest that describes no
// commands leaves the omitempty field absent.
func DescriptorCommands(commands []wasm.PluginCLICommand) []types.PluginCommand {
	if len(commands) == 0 {
		return nil
	}
	out := make([]types.PluginCommand, 0, len(commands))
	for _, c := range commands {
		out = append(out, types.PluginCommand{
			Name:        c.Name,
			Description: c.Description,
			Flags:       descriptorCommandFlags(c.Flags),
		})
	}
	return out
}

// descriptorCommandFlags maps a describe() command's flag declarations onto the
// entity's machine-owned flag mirror, preserving describe()-declared order and
// carrying every flag field. Empty input yields nil, so a command that declares
// no flags leaves the omitempty field absent.
func descriptorCommandFlags(flags []wasm.PluginCommandFlag) []types.PluginCommandFlag {
	if len(flags) == 0 {
		return nil
	}
	out := make([]types.PluginCommandFlag, 0, len(flags))
	for _, f := range flags {
		out = append(out, types.PluginCommandFlag{
			Name:        f.Name,
			Shorthand:   f.Shorthand,
			Description: f.Description,
			Type:        f.Type,
			Default:     f.Default,
		})
	}
	return out
}

// IsZeroContract reports whether a describe() render-behavior contract carries
// no claim — every field at its zero value — so the generator emits no contract
// block rather than an all-empty one. The cross-cutting sockets (linter, mutator,
// policy) report no render-behavior contract.
func IsZeroContract(c wasm.ProviderContract) bool {
	return c.InputKinds == (wasm.InputKinds{}) &&
		c.BoundaryAssembly == "" &&
		c.ArtifactDelimiters == nil &&
		len(c.SourceFilter) == 0 &&
		c.AssemblyNameAnnotation == nil &&
		!c.ReusableInstance &&
		!c.CrossBoundaryLinking &&
		c.NestSegment == ""
}

// descriptorContract maps the describe() provider render-behavior self-report
// onto the entity's authored-contract shape, or returns nil when the guest
// reports none.
//
// The input-kind copy below is one of the few that SURVIVE consolidation, and it
// survives for a structural reason rather than by oversight: it crosses from the
// GENERATED describe-socket codec into the entity's hand-written shape, and those
// two cannot be one Go type — the guest-side half of the socket lives in a module
// that must not import the host. The claim's one host-side home is
// [types.PluginInputKinds]; every consumer that can share it does, by alias.
//
// So adding an input kind means editing this copy too. It is guarded from the far
// side rather than from here: the field-set guard in internal/wasm fails when the
// generated shape and the home type stop agreeing, which is what makes a forgotten
// line here loud instead of silent. Do not answer a new kind by declaring another
// three-boolean struct.
func descriptorContract(c *wasm.ProviderContract) *types.ProviderContract {
	if c == nil || IsZeroContract(*c) {
		return nil
	}
	out := &types.ProviderContract{
		InputKinds: types.PluginInputKinds{
			Sourced:             c.InputKinds.Sourced,
			Synthesized:         c.InputKinds.Synthesized,
			ArtifactsStandalone: c.InputKinds.ArtifactsStandalone,
		},
		BoundaryAssembly:     c.BoundaryAssembly,
		SourceFilter:         c.SourceFilter,
		ReusableInstance:     c.ReusableInstance,
		CrossBoundaryLinking: c.CrossBoundaryLinking,
		NestSegment:          c.NestSegment,
	}
	if c.ArtifactDelimiters != nil {
		out.ArtifactDelimiters = &types.PluginArtifactDelimiters{
			Left:  c.ArtifactDelimiters.Left,
			Right: c.ArtifactDelimiters.Right,
		}
	}
	if c.AssemblyNameAnnotation != nil {
		out.AssemblyNameAnnotation = &types.PluginAssemblyNameAnnotation{
			Annotation:    c.AssemblyNameAnnotation.Annotation,
			RequireUnique: c.AssemblyNameAnnotation.RequireUnique,
		}
	}
	return out
}

// ParseDescriptorJSON unmarshals a describe() descriptor JSON payload into the
// validated descriptor shape and, separately, reads the optional plugin-level
// description field. The description is read through a forward-compatible view so
// a caller picks it up as soon as guests emit it, without this surface declaring
// an ABI field of its own.
func ParseDescriptorJSON(raw []byte) (wasm.PluginDescriptor, string, error) {
	var desc wasm.PluginDescriptor
	if err := json.Unmarshal(raw, &desc); err != nil {
		return wasm.PluginDescriptor{}, "", err
	}
	var extra struct {
		Description string `json:"description"`
	}
	if err := json.Unmarshal(raw, &extra); err != nil {
		return wasm.PluginDescriptor{}, "", err
	}
	return desc, extra.Description, nil
}
