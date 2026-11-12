// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package pipeline

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"sort"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
	"github.com/aggrega-ai/aggrega-cli/pkg/plugintype"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// injectedByAnnotationKey is the host-written companion annotation the
// annotation-transform role stamps alongside each injected annotation, carrying
// the injecting plugin's URI. It rides through expansion as an auditable marker
// of which plugin authored the injection; the host writes it (a guest cannot
// forge provenance), and it is not a binding key, so expansion skips it.
const injectedByAnnotationKey = "aggrega.ai/injected-by"

// runAnnotationSocket runs the pre-expansion annotation-transform socket: for
// each configured annotation-transform plugin, in order, it assembles the
// injection targets from the resolved Release, dispatches the plugin's
// transform_annotations export (or a per-attachment override), and applies the
// returned injections to the resolved Release before annotation expansion. It
// runs after resolve and before ValidateAnnotationBindingTargets +
// ExpandAnnotations, so an injected annotation bound in the Manifest is seen by
// binding validation and expansion with no re-run — a bound injection becomes a
// synthetic Feature attachment, the same explicit-declaration standing
// annotation-driven expansion already has (Invariant 4). The socket carries
// canonical CBOR in both directions: the input is encoded with the generated
// encoder, each target's annotations riding in the order the component holds
// them, and the output is read through the binary encoding's strict entry
// point. An unconfigured socket
// is a no-op (returns nil before any resolution), which keeps a build with no
// annotation-transform attachment byte-identical to a build without the socket;
// a run asked for the policy payloads still assembles the payload, to record it,
// and dispatches nothing. A
// resolution, load, settings, or confinement failure halts the build loudly, as does an
// output that is not this socket's shape — a dispatched export whose output
// carries a field the annotation-transform shape does not declare is refused
// rather than read as an empty injection list.
func runAnnotationSocket(ctx context.Context, inputs Inputs, catalogLookup resolver.CatalogLookup, rr *resolver.ResolveResult) error {
	refs := inputs.WorkspaceConfig.Plugins.GovernanceRefsForRole(plugintype.RoleAnnotationTransform)
	if len(refs) == 0 && !inputs.policyInputs.wanted() {
		return nil
	}
	if rr == nil || rr.Release == nil {
		return nil
	}
	bindingKeys := resolver.BuildAnnotationRegistry(rr.Manifest).Keys()
	inputs.policyInputs.reach(plugintype.RoleAnnotationTransform, len(refs))
	if len(refs) == 0 {
		// Nothing is attached, and the payloads were asked for: the payload an
		// attachment with no sources and no settings would be handed, from the same
		// assembly, dispatched to nobody.
		in, _ := annotationTransformInput(rr.Release, bindingKeys, workspaceconfig.PluginRef{})
		inputs.policyInputs.record(plugintype.RoleAnnotationTransform, -1, workspaceconfig.PluginRef{}, in)
		return nil
	}
	lookup := resolveSocketLookup(inputs, catalogLookup, workspaceconfig.SocketTierMutating)

	for i, ref := range refs {
		export := wasm.AuthoredExportName(wasm.TransformAnnotationsExport, ref.Function)
		resolved, err := lookup(ref.URI)
		if err != nil {
			return fmt.Errorf("annotation-transform: resolve plugin %q: %w", ref.URI, err)
		}
		in, targetSet := annotationTransformInput(rr.Release, bindingKeys, ref)
		inputs.policyInputs.record(plugintype.RoleAnnotationTransform, i, ref, in)
		payload, err := in.EncodeCBOR()
		if err != nil {
			return fmt.Errorf("annotation-transform: encode input for plugin %q: %w", ref.URI, err)
		}
		spec := resolver.PluginSpec(pluginRefName(ref.URI), resolved)
		spec.PluginType = wasm.PluginTypeGovernance
		out, err := invokeSocketPlugin(ctx, spec, wasm.TransformAnnotationsExport, ref.Function, payload, attachmentSettings(ref))
		if err != nil {
			return fmt.Errorf("annotation-transform: plugin %q: %w", ref.URI, err)
		}
		var ao AnnotationTransformOutput
		if err := wasm.DecodeGuestOutputCBOR(export, out, &ao); err != nil {
			return fmt.Errorf("annotation-transform: plugin %q: %w", ref.URI, err)
		}
		if err := wasm.GuestReturnedError(ref.URI, export, wasm.GuestFailure{Message: ao.Error}); err != nil {
			return fmt.Errorf("annotation-transform: plugin %q: %w", ref.URI, err)
		}
		if err := applyAnnotationInjections(rr, ao, targetSet, ref.URI); err != nil {
			return fmt.Errorf("annotation-transform: plugin %q: %w", ref.URI, err)
		}
	}
	return nil
}

// annotationTransformInput assembles the payload the annotation-transform role
// hands the attachment ref — the injection targets as the resolved Release holds
// them now, the binding keys, and the attachment's own sources and settings — and
// the coordinate set its confinement check reads. It is the one assembly of that
// payload: the dispatch encodes what it returns, and a run asked for the payloads
// records the same value.
func annotationTransformInput(release *types.Release, bindingKeys []string, ref workspaceconfig.PluginRef) (AnnotationTransformInput, map[string]bool) {
	targets, targetSet := annotationTargets(release)
	// The wire always carries both lists, empty where there is nothing to list,
	// and a guest hands its policy empty lists. Held as empty lists rather than as
	// none, the payload written out for an author reads the same way; the encoding
	// the guest receives is the same either way.
	if targets == nil {
		targets = []AnnotationTarget{}
	}
	if bindingKeys == nil {
		bindingKeys = []string{}
	}
	return AnnotationTransformInput{
		Targets:     targets,
		BindingKeys: bindingKeys,
		Sources:     annotationSources(ref),
		Settings:    annotationSettings(ref),
	}, targetSet
}

// annotationSettings projects the attachment's free-form settings into the
// verbatim JSON the socket hands the plugin, or nil when the attachment declares
// none. The host acts on none of its content; the dispatch checks the attachment's
// settings against the settings schema the plugin declares.
func annotationSettings(ref workspaceconfig.PluginRef) json.RawMessage {
	settings := attachmentSettings(ref)
	if len(settings) == 0 {
		return nil
	}
	raw, err := json.Marshal(settings)
	if err != nil {
		return nil
	}
	return raw
}

// annotationSources projects the attachment's pinned source files into the
// annotation socket's ordered source entries, sorted by base name. Like the
// label-transform socket (and unlike the verdict sockets), it reads CONTENT
// pinned in the lockfile and reconstructed onto the ref (PinnedSources) rather
// than live host files — so an annotation-transform build stays byte-identical
// after a post-pull change to the source file. Returns nil when the attachment
// pins no sources.
func annotationSources(ref workspaceconfig.PluginRef) []AnnotationSource {
	if ref.With == nil || len(ref.With.PinnedSources) == 0 {
		return nil
	}
	sources := make([]AnnotationSource, 0, len(ref.With.PinnedSources))
	for _, s := range ref.With.PinnedSources {
		sources = append(sources, AnnotationSource{Path: s.Path, Content: s.Content})
	}
	sort.Slice(sources, func(i, j int) bool { return sources[i].Path < sources[j].Path })
	return sources
}

// annotationTargetKey composes the confinement key for one injection target
// coordinate. It is the single home of the coordinate's string form, so the
// target set and the confinement check read one spelling.
func annotationTargetKey(boundaryPath, componentName string) string {
	return boundaryPath + "\x00" + componentName
}

// annotationTargets projects the resolved Release's boundary components into the
// injection targets the plugin sees, and the coordinate set the confinement
// check reads. Each target carries the component's boundary path, name, and the
// annotations written on the component in every shape an author writes them —
// see [annotationTargetValues] for why a structured or null value is carried
// rather than left out.
//
// The components are the ones every other walker of the boundary tree reads, on
// both of a boundary's axes: those it names directly, and those a routing entry
// names inside the sub-module it routes in, each under the id it carries in the
// flattened graph ("metrics.query"). That id is the coordinate an injection
// names, so a plugin addresses a routed component exactly as the graph and its
// diagnostics spell it. A component a routing entry places without naming it is
// listed too: a build names it under the routing entry, with an empty body, before
// this role runs.
//
// Targets emit in author order — per boundary, its own components, then the
// routed ones in routing order, then its child boundaries — so the wire is
// deterministic. The confinement set is read off the list once it is built
// rather than collected beside it, so a coordinate is accepted exactly when it
// was offered.
func annotationTargets(release *types.Release) ([]AnnotationTarget, map[string]bool) {
	var targets []AnnotationTarget
	_ = types.WalkInstanceTree(release.Spec.Boundaries, types.BoundaryTree(), "", struct{}{},
		types.InstanceTreeVisitor[types.Boundary, types.BoundaryComponent, struct{}]{
			Path: joinAnnotationBoundaryPath,
			Visit: func(comp types.BoundaryComponent, name, path string, _ bool, _ struct{}) error {
				targets = append(targets, AnnotationTarget{
					BoundaryPath:  path,
					ComponentName: name,
					Annotations:   annotationTargetValues(comp.Annotations),
				})
				return nil
			},
		})
	set := make(map[string]bool, len(targets))
	for _, target := range targets {
		set[annotationTargetKey(target.BoundaryPath, target.ComponentName)] = true
	}
	return targets, set
}

// joinAnnotationBoundaryPath composes a boundary's slash-separated path from its
// parent's path and its own name — the spelling a target's boundary path carries
// and the one [splitBoundaryPathSlash] takes apart. A synthesized boundary is
// named like any other.
func joinAnnotationBoundaryPath(parentPath, name string, _ bool) string {
	if parentPath == "" {
		return name
	}
	return parentPath + "/" + name
}

// annotationTargetValues projects a component's own annotations into the values
// a target carries: a string for a scalar, the author-ordered map of subkeys for
// a structured value, and nil — null on the wire — for an explicit opt-out.
//
// Every key is carried, whatever its shape, because the plugin reads the set to
// avoid clobbering an annotation the component already has, and the host applies
// an injection by setting its key. A projection carrying only the scalars would
// hide a structured value an author wrote, or a null an author wrote to opt out
// of an inherited annotation, and an injection under that key would then replace
// what the author wrote without the plugin having been able to see it.
func annotationTargetValues(in types.OrderedMap[*types.AnnotationValue]) types.OrderedMap[any] {
	out := types.OrderedMap[any]{}
	for key, value := range in.All() {
		switch {
		case value.IsScalar():
			out.Set(key, *value.Scalar)
		case value.IsStructured():
			out.Set(key, value.Structured)
		default:
			out.Set(key, nil)
		}
	}
	return out
}

// injectedAnnotationValue reads an injection's value into the shape an authored
// annotation value takes, refusing — naming the injection — a value no injection
// may carry.
//
// The value is read by the decoder that reads an annotation an author writes in a
// document, so an injection can carry exactly the shapes that decoder yields and
// expansion meets an injected value exactly as it meets the same value authored:
// a structured value keeps the order its subkeys were written in, at every depth.
// What this adds is the narrowing to what an injection may do, which is add an
// annotation. A string and an object of subkeys add one. Null, and an object with
// no subkeys, read as the empty value an author writes to opt a component out of
// an annotation it would inherit, which an injection may not do; and a list, a
// number or a boolean is not a value an annotation takes on the wire.
func injectedAnnotationValue(inj AnnotationInjection) (*types.AnnotationValue, error) {
	refuse := func(reason string) error {
		return fmt.Errorf("%w: injection for boundary %q component %q (annotation %q) %s; an injected annotation value is a string or an object of subkeys",
			ErrAnnotationInjectionRejected(), inj.BoundaryPath, inj.ComponentName, inj.AnnotationKey, reason)
	}
	raw := bytes.TrimSpace(inj.AnnotationValue)
	if len(raw) == 0 || !json.Valid(raw) {
		return nil, refuse("carries a value that is not a JSON document")
	}
	switch raw[0] {
	case '"', '{':
	case 'n':
		return nil, refuse("carries null")
	case '[':
		return nil, refuse("carries a list")
	case 't', 'f':
		return nil, refuse("carries a boolean")
	default:
		return nil, refuse("carries a number")
	}
	var value types.AnnotationValue
	if err := yaml.Unmarshal(raw, &value); err != nil {
		return nil, refuse(fmt.Sprintf("carries a value an annotation does not take: %v", err))
	}
	if value.IsEmpty() {
		if raw[0] == '{' {
			return nil, refuse("carries an object with no subkeys")
		}
		// The decoder reads a few strings — "null" and "~" — as the null an author
		// writes to opt out, wherever they are written, so an injected one reads
		// the same way and is refused for the reason null is.
		return nil, refuse("carries a string an annotation reads as null")
	}
	return &value, nil
}

// applyAnnotationInjections applies an annotation-transform plugin's injections
// to the resolved Release, in the order the plugin emitted them. It is the
// annotation role's structural confinement, the counterpart of the label role's
// patch confinement: the host adds only annotations, onto component coordinates
// it supplied as targets, each carrying a value an author could write on that
// annotation. It rejects — with [ErrAnnotationInjectionRejected] — any injection
// naming a coordinate outside the supplied set, and any injection whose value is
// not a string or an object of subkeys (see [injectedAnnotationValue]). The wire
// shape admits no other field, so a non-annotation mutation is structurally
// impossible; the coordinate and value checks are the confinement the host
// enforces at apply.
//
// Every injection is checked before any is applied, so a refused output leaves
// the Release exactly as the plugin found it.
//
// For each accepted injection the host also stamps the companion
// aggrega.ai/injected-by annotation (the plugin's URI) on the same component and
// pre-seeds the annotation-provenance side-table with the injecting plugin's URI,
// so the synthetic Feature attachment expansion later produces records which
// plugin authored the injection. Both are host-written — a guest cannot forge
// provenance.
func applyAnnotationInjections(rr *resolver.ResolveResult, out AnnotationTransformOutput, targetSet map[string]bool, pluginURI string) error {
	if len(out.Injections) == 0 {
		return nil
	}
	values := make([]*types.AnnotationValue, len(out.Injections))
	for i, inj := range out.Injections {
		if !targetSet[annotationTargetKey(inj.BoundaryPath, inj.ComponentName)] {
			return fmt.Errorf("%w: injection targets boundary %q component %q (annotation %q)",
				ErrAnnotationInjectionRejected(), inj.BoundaryPath, inj.ComponentName, inj.AnnotationKey)
		}
		value, err := injectedAnnotationValue(inj)
		if err != nil {
			return err
		}
		values[i] = value
	}
	if rr.AnnotationProvenance == nil {
		rr.AnnotationProvenance = map[string]resolver.AnnotationProvenance{}
	}
	for i, inj := range out.Injections {
		// Mutate the authoring tree so expansion (which reads fresh from the
		// Release) sees the injected annotation, and update the renderer-facing
		// per-component annotation projection so a downstream render-context read
		// sees it too — an injected annotation is thereby indistinguishable from an
		// authored one everywhere it flows.
		pluginValue := pluginURI
		setReleaseComponentAnnotation(rr.Release, inj.BoundaryPath, inj.ComponentName, inj.AnnotationKey, values[i])
		setReleaseComponentAnnotation(rr.Release, inj.BoundaryPath, inj.ComponentName, injectedByAnnotationKey, &types.AnnotationValue{Scalar: &pluginValue})
		setBoundaryInfraAnnotation(rr, inj.ComponentName, inj.AnnotationKey, values[i])
		setBoundaryInfraAnnotation(rr, inj.ComponentName, injectedByAnnotationKey, &types.AnnotationValue{Scalar: &pluginValue})

		// Pre-seed the provenance side-table with the injecting plugin's URI at the
		// injection coordinate; expansion preserves it when it records the
		// annotation key and source, so the synthetic Feature attachment's
		// provenance carries which plugin authored the injection.
		pk := resolver.AnnotationProvenanceKey(inj.BoundaryPath, inj.ComponentName, inj.AnnotationKey)
		prov := rr.AnnotationProvenance[pk]
		prov.InjectedByPlugin = pluginURI
		rr.AnnotationProvenance[pk] = prov
	}
	return nil
}

// setBoundaryInfraAnnotation reflects one injected annotation in a component's
// renderer-facing annotation projection (resolver BoundaryInfra), so an injected
// annotation reaches the render context the same way an authored one does. That
// projection carries scalar values only, so a scalar is set and a structured
// value removes the key — exactly as a structured value an author wrote on the
// component leaves no scalar there to read. A component with no BoundaryInfra
// entry is a no-op — the injection still lands on the authoring tree, which is
// what expansion reads.
func setBoundaryInfraAnnotation(rr *resolver.ResolveResult, componentName, key string, value *types.AnnotationValue) {
	if rr.BoundaryInfra == nil {
		return
	}
	bi, ok := rr.BoundaryInfra[componentName]
	if !ok || bi == nil {
		return
	}
	if value.IsScalar() {
		bi.Annotations.Set(key, *value.Scalar)
		return
	}
	bi.Annotations.Delete(key)
}

// setReleaseComponentAnnotation sets one annotation on a Release boundary
// component identified by its slash-separated boundary path and component name,
// writing the mutated boundary chain back to the Release root. OrderedMap returns
// values by copy, so the walk holds a local copy of each boundary and component;
// the helper mutates the leaf component's annotation map and writes the chain
// back up so the mutation persists. A path or component the walk cannot resolve
// is a no-op — the caller's confinement check has already rejected an unknown
// coordinate before this runs.
//
// The component is read and written on whichever axis of the boundary declares
// it, so an annotation injected onto a routed component lands on the routing
// entry that names it — the entry an author would write it on — rather than on a
// second entry under its qualified id, which would place one component twice.
func setReleaseComponentAnnotation(release *types.Release, boundaryPath, compName, key string, value *types.AnnotationValue) {
	segments := splitBoundaryPathSlash(boundaryPath)
	if len(segments) == 0 {
		return
	}
	chain := make([]types.Boundary, len(segments))
	top, ok := release.Spec.Boundaries.Get(segments[0])
	if !ok {
		return
	}
	chain[0] = top
	for i := 1; i < len(segments); i++ {
		child, ok := chain[i-1].Boundaries.Get(segments[i])
		if !ok {
			return
		}
		chain[i] = child
	}
	leaf := chain[len(segments)-1]
	held := types.BoundaryComponents(leaf)
	comp, ok := held.Get(compName)
	if !ok {
		return
	}
	comp.Annotations.Set(key, value)
	types.SetBoundaryComponent(&leaf, compName, comp)
	chain[len(segments)-1] = leaf
	for i := len(segments) - 1; i > 0; i-- {
		chain[i-1].Boundaries.Set(segments[i], chain[i])
	}
	release.Spec.Boundaries.Set(segments[0], chain[0])
}

// splitBoundaryPathSlash splits a slash-separated boundary path into its
// segments. Empty input returns nil.
func splitBoundaryPathSlash(path string) []string {
	if path == "" {
		return nil
	}
	var out []string
	start := 0
	for i := 0; i < len(path); i++ {
		if path[i] == '/' {
			out = append(out, path[start:i])
			start = i + 1
		}
	}
	return append(out, path[start:])
}
