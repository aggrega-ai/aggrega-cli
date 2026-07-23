// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"

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
// annotation-driven expansion already has (Invariant 4). An unconfigured socket
// is a no-op (returns nil before any resolution), which keeps a build with no
// annotation-transform attachment byte-identical to a build without the socket. A
// resolution, load, output-unmarshal, or confinement failure halts the build
// loudly.
func runAnnotationSocket(ctx context.Context, inputs Inputs, catalogLookup resolver.CatalogLookup, rr *resolver.ResolveResult) error {
	refs := inputs.WorkspaceConfig.Plugins.GovernanceRefsForRole(plugintype.RoleAnnotationTransform)
	if len(refs) == 0 {
		return nil
	}
	if rr == nil || rr.Release == nil {
		return nil
	}
	lookup := resolveSocketLookup(inputs, catalogLookup, workspaceconfig.SocketTierMutating)
	bindingKeys := resolver.BuildAnnotationRegistry(rr.Manifest).Keys()

	for _, ref := range refs {
		export := ref.Function
		if export == "" {
			export = wasm.TransformAnnotationsExport
		}
		resolved, err := lookup(ref.URI)
		if err != nil {
			return fmt.Errorf("Step 1 (annotation transform): resolve plugin %q: %w", ref.URI, err)
		}
		targets, targetSet := annotationTargets(rr.Release)
		in := AnnotationTransformInput{
			Targets:     targets,
			BindingKeys: bindingKeys,
			Sources:     annotationSources(ref),
			Settings:    annotationSettings(ref),
		}
		payload, err := json.Marshal(in)
		if err != nil {
			return fmt.Errorf("Step 1 (annotation transform): marshal input for plugin %q: %w", ref.URI, err)
		}
		spec := resolver.PluginSpec(pluginRefName(ref.URI), resolved)
		spec.PluginType = wasm.PluginTypeGovernance
		out, err := invokeSocketPlugin(ctx, spec, export, payload)
		if err != nil {
			return fmt.Errorf("Step 1 (annotation transform): plugin %q: %w", ref.URI, err)
		}
		var ao AnnotationTransformOutput
		if err := json.Unmarshal(out, &ao); err != nil {
			return fmt.Errorf("Step 1 (annotation transform): plugin %q output: %w", ref.URI, err)
		}
		if err := applyAnnotationInjections(rr, ao, targetSet, ref.URI); err != nil {
			return fmt.Errorf("Step 1 (annotation transform): plugin %q: %w", ref.URI, err)
		}
	}
	return nil
}

// annotationSettings projects the attachment's free-form settings into the
// verbatim JSON the socket hands the plugin, or nil when the attachment declares
// none. The host does not parse or act on the content.
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
// check reads. Each target carries the component's boundary path, name, and
// current scalar annotation set (structured annotations are routing instructions
// for expansion, not template substrate, so only scalars surface — the same
// projection the render context uses). Targets emit in author order via the
// boundary and component OrderedMaps, so the wire is deterministic.
func annotationTargets(release *types.Release) ([]AnnotationTarget, map[string]bool) {
	var targets []AnnotationTarget
	set := map[string]bool{}
	var walk func(path string, b types.Boundary)
	walk = func(path string, b types.Boundary) {
		for compName, comp := range b.Components.All() {
			targets = append(targets, AnnotationTarget{
				BoundaryPath:  path,
				ComponentName: compName,
				Annotations:   types.ScalarOnly(comp.Annotations),
			})
			set[annotationTargetKey(path, compName)] = true
		}
		for childName, child := range b.Boundaries.All() {
			childPath := childName
			if path != "" {
				childPath = path + "/" + childName
			}
			walk(childPath, child)
		}
	}
	for name, boundary := range release.Spec.Boundaries.All() {
		walk(name, boundary)
	}
	return targets, set
}

// applyAnnotationInjections applies an annotation-transform plugin's injections
// to the resolved Release, in the order the plugin emitted them. It is the
// annotation role's structural facet guarantee (LDD-3 analogue of the label
// role's confinedLabels): the host adds only string-valued annotations onto
// component coordinates it supplied as targets, and rejects — with
// [ErrAnnotationInjectionRejected] — any injection naming a coordinate outside
// the supplied set. The wire shape admits no other field, so a non-annotation
// mutation or a non-string value is structurally impossible; the coordinate
// check is the one remaining confinement the host enforces after apply.
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
	if rr.AnnotationProvenance == nil {
		rr.AnnotationProvenance = map[string]resolver.AnnotationProvenance{}
	}
	for _, inj := range out.Injections {
		if !targetSet[annotationTargetKey(inj.BoundaryPath, inj.ComponentName)] {
			return fmt.Errorf("%w: injection targets boundary %q component %q (annotation %q)",
				ErrAnnotationInjectionRejected, inj.BoundaryPath, inj.ComponentName, inj.AnnotationKey)
		}
		// Mutate the authoring tree so expansion (which reads fresh from the
		// Release) sees the injected annotation, and update the renderer-facing
		// per-component annotation projection so a downstream render-context read
		// sees it too — an injected annotation is thereby indistinguishable from an
		// authored one everywhere it flows.
		setReleaseComponentAnnotation(rr.Release, inj.BoundaryPath, inj.ComponentName, inj.AnnotationKey, inj.AnnotationValue)
		setReleaseComponentAnnotation(rr.Release, inj.BoundaryPath, inj.ComponentName, injectedByAnnotationKey, pluginURI)
		setBoundaryInfraAnnotation(rr, inj.ComponentName, inj.AnnotationKey, inj.AnnotationValue)
		setBoundaryInfraAnnotation(rr, inj.ComponentName, injectedByAnnotationKey, pluginURI)

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

// setBoundaryInfraAnnotation adds one scalar annotation to a component's
// renderer-facing annotation projection (resolver BoundaryInfra), so an injected
// annotation reaches the render context the same way an authored one does. A
// component with no BoundaryInfra entry is a no-op — the injection still lands on
// the authoring tree, which is what expansion reads.
func setBoundaryInfraAnnotation(rr *resolver.ResolveResult, componentName, key, value string) {
	if rr.BoundaryInfra == nil {
		return
	}
	bi, ok := rr.BoundaryInfra[componentName]
	if !ok || bi == nil {
		return
	}
	bi.Annotations.Set(key, value)
}

// setReleaseComponentAnnotation sets one scalar annotation on a Release boundary
// component identified by its slash-separated boundary path and component name,
// writing the mutated boundary chain back to the Release root. OrderedMap returns
// values by copy, so the walk holds a local copy of each boundary and component;
// the helper mutates the leaf component's annotation map and writes the chain
// back up so the mutation persists. A path or component the walk cannot resolve
// is a no-op — the caller's confinement check has already rejected an unknown
// coordinate before this runs.
func setReleaseComponentAnnotation(release *types.Release, boundaryPath, compName, key, value string) {
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
	comp, ok := leaf.Components.Get(compName)
	if !ok {
		return
	}
	v := value
	comp.Annotations.Set(key, &types.AnnotationValue{Scalar: &v})
	leaf.Components.Set(compName, comp)
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
