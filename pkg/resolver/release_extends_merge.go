// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver

import (
	"fmt"
	"path/filepath"
	"sort"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// ResolveReleaseExtends composes a Release's extends chain into a single
// effective Release.
//
// What a child inherits, it may replace key by key: its value wins all the way
// down until the first value that is one indivisible thing — a scalar, a list, a
// template — which it takes or replaces whole. A key it writes as null is
// removed from what it inherited. That is the same rule an extending Component,
// Feature and Manifest already follow, so an author who has met one has met all
// of them.
//
// Applied to a Release: boundaries compose per boundary name and a boundary per
// key, components per component name, infra deep, features per instance name and
// then per pillar, a connection per socket and alias with its attachments per
// slot by that same features rule; and directory, subDirectory, syncWave and the
// Aggregate and Manifest pointers are scalars that replace.
//
// Identity and provenance do not travel. Every metadata field is the child's
// own, which is what keeps a child from carrying its parent's environment into
// its provenance labels — an error nothing downstream could report, because a
// parent's environment is a perfectly well-formed value to find there.
//
// The extends reference itself is not carried onto the result. It is spent once
// the chain is folded, and leaving it would describe the composed Release as
// still extending something while also carrying the Aggregate pointer it
// inherited — a pair no author may write.
//
// A Release declaring no extends reference is returned unchanged. A reference
// that cannot be resolved, a chain that revisits a file already in the chain and
// a chain past the depth bound each halt with the failure the walk reports; the
// fold never sees a chain that is not one.
//
// It returns the composed Release, whatever the composition rules advised about,
// and the one failure that stopped it. The advisories ride back rather than
// being printed here for the reason the Manifest consistency findings do: the
// verdict differs by what the caller produces, and a caller emitting an artifact
// and a caller reporting on a document want the same reading rendered
// differently. Every caller owes a verdict on them.
func ResolveReleaseExtends(
	releaseDir string,
	child ResolvedRelease,
) (*types.Release, []error, error) {
	if child.Release == nil || child.Release.Spec.Extends == "" {
		return child.Release, nil, nil
	}

	chain, err := ReleaseExtendsChain(releaseDir, child)
	if err != nil {
		return nil, nil, err
	}

	// The composed-shape rule is asked before the fold runs, because the fold
	// would otherwise produce the document the rule exists to refuse and hand it
	// downstream while the refusal was still being assembled.
	shapeFacts := releaseChainCompositionFacts(chain, nil)
	var shapeView constraint.DocumentView = shapeFacts
	if refusals := FindingsFromObjections(
		constraint.RunOnDocument("", shapeView, releaseChainComposesOneRoutingShape),
		fmt.Sprintf("Release %s", child.Path),
	); len(refusals) > 0 {
		// The rule is invoked once and objects about the composition as a whole,
		// so the first refusal is the only one there can be.
		return nil, nil, refusals[0]
	}

	composed, cerr := composeReleaseChain(chain)
	if cerr != nil {
		return nil, nil, cerr
	}

	advisoryFacts := releaseChainCompositionFacts(chain, composed)
	var advisoryView constraint.DocumentView = advisoryFacts
	advisories := FindingsFromObjections(
		constraint.RunOnDocument("", advisoryView,
			inheritedBoundaryDirectoryResolvesWhereItsParentWrites),
		fmt.Sprintf("Release %s", child.Path),
	)
	out := make([]error, 0, len(advisories))
	for _, advisory := range advisories {
		out = append(out, advisory)
	}
	return composed, out, nil
}

// composeReleaseChain folds a walked chain (child first, root ancestor last)
// into one Release.
//
// It starts from the root ancestor and applies each descendant in turn, so the
// child — applied last — wins every collision. Identity comes from the child and
// only the spec is composed.
func composeReleaseChain(chain []ResolvedRelease) (*types.Release, error) {
	child := chain[0].Release
	out := &types.Release{
		APIVersion: child.APIVersion,
		Kind:       child.Kind,
		Metadata:   child.Metadata,
		Spec:       types.ReleaseSpec{},
	}

	// Parameters are accumulated as a plain map because that is the shape the
	// merge this delegates to works in; the result is materialized back onto the
	// spec once the whole chain has been applied.
	var params map[string]any

	for i := len(chain) - 1; i >= 0; i-- {
		link := chain[i]
		spec := link.Release.Spec

		if spec.Aggregate != "" {
			out.Spec.Aggregate = spec.Aggregate
		}
		if spec.Manifest != "" {
			out.Spec.Manifest = spec.Manifest
		}

		contributed, perr := releaseParameterContribution(link)
		if perr != nil {
			return nil, perr
		}
		params = deepMerge(params, contributed)

		out.Spec.Boundaries = mergeBoundaryMaps(out.Spec.Boundaries, spec.Boundaries)
		out.Spec.Components = mergeBoundaryComponentMaps(out.Spec.Components, spec.Components)
	}

	out.Spec.Parameters = types.ParametersBlock{Inline: plainToOrderedSorted(params)}
	return out, nil
}

// releaseParameterContribution is what one link along the chain contributes to
// the composed parameter set, in the shape the parameter merge works in.
//
// A link's import is resolved here, before the fold, and against the directory
// holding that link rather than the directory the resolution started from: a
// parent's import names a file beside the parent. Resolving it here is also what
// keeps "inline or imported, never both" a property of each authored document —
// the rule is about what one author wrote in one file, and a chain supplying one
// mode at one link and the other mode at another breaks no author's rule.
func releaseParameterContribution(link ResolvedRelease) (map[string]any, error) {
	block := link.Release.Spec.Parameters
	if block.Import == "" {
		// MergeParameters with no external side flattens the inline map, which is
		// the same flattening every other reader of a parameters block gets.
		return MergeParameters(block, nil), nil
	}
	return LoadParametersImport(filepath.Join(filepath.Dir(link.Path), block.Import))
}

// plainToOrderedSorted materializes a composed parameter map back onto the
// spec's ordered shape, in sorted key order.
//
// The order is sorted rather than authored because the merge this follows works
// in plain maps, which carry none — and sorted is the only order that is the
// same on every run, which is what the composed Release owes. Nothing observable
// rests on the choice: the one reader of a Release's parameters flattens them
// through TemplateAlphabeticalMapDeep before a template ever sees them, so the
// rendered bytes are the same whichever order this imposed.
func plainToOrderedSorted(m map[string]any) types.OrderedMap[any] {
	out := types.OrderedMap[any]{}
	if len(m) == 0 {
		return out
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		v := m[k]
		if nested, ok := v.(map[string]any); ok {
			sub := plainToOrderedSorted(nested)
			out.Set(k, &sub)
			continue
		}
		out.Set(k, v)
	}
	return out
}

// mergeBoundaryMaps composes two boundary maps per boundary name: a name only
// the base carries stands, a name only the overlay carries is introduced, and a
// name both carry is composed per key.
//
// Base author order is kept and overlay-only names are appended in theirs, which
// is the order the annotation cascade's own merge produces and the order a
// reader of the composed document expects: what the parent laid out, then what
// the child added.
func mergeBoundaryMaps(base, overlay types.OrderedMap[types.Boundary]) types.OrderedMap[types.Boundary] {
	out := types.OrderedMap[types.Boundary]{}
	for k, v := range base.All() {
		out.Set(k, v)
	}
	for k, ov := range overlay.All() {
		if inherited, had := out.Get(k); had {
			out.Set(k, mergeBoundary(inherited, ov))
			continue
		}
		out.Set(k, ov)
	}
	return out
}

// mergeBoundary composes one boundary the child redeclared onto the one it
// inherited, per key rather than wholesale.
//
// Per key rather than wholesale is what lets a child change a boundary's sizing
// without restating its whole component list — the duplication this feature
// exists to remove. The scalars replace when the child wrote one: a boundary's
// directory, its sub-directory and its sync wave are each one indivisible value.
//
// A sync wave of zero is read as unwritten. The field carries no way to tell an
// authored zero from an absent one — it is a plain int with omitempty — so a
// child that means to pull a boundary back to wave zero cannot say so today, and
// reading zero as authored would instead reset every boundary the child merely
// mentioned.
func mergeBoundary(base, overlay types.Boundary) types.Boundary {
	out := base
	if overlay.Description != "" {
		out.Description = overlay.Description
	}
	if overlay.Directory != "" {
		out.Directory = overlay.Directory
	}
	if overlay.SubDirectory != "" {
		out.SubDirectory = overlay.SubDirectory
	}
	if overlay.SyncWave != 0 {
		out.SyncWave = overlay.SyncWave
	}
	out.Annotations = mergeAnnotationValueMaps(base.Annotations, overlay.Annotations)
	out.Features = mergeBoundaryFeatureMaps(base.Features, overlay.Features)
	out.Boundaries = mergeBoundaryMaps(base.Boundaries, overlay.Boundaries)
	out.Components = mergeBoundaryComponentMaps(base.Components, overlay.Components)
	out.Aggregates = mergeAggregateRoutingMaps(base.Aggregates, overlay.Aggregates)
	return out
}

// mergeBoundaryComponentMaps composes two routed-component maps per component
// name, on the terms mergeBoundaryMaps composes boundaries.
func mergeBoundaryComponentMaps(
	base, overlay types.OrderedMap[types.BoundaryComponent],
) types.OrderedMap[types.BoundaryComponent] {
	out := types.OrderedMap[types.BoundaryComponent]{}
	for k, v := range base.All() {
		out.Set(k, v)
	}
	for k, ov := range overlay.All() {
		if inherited, had := out.Get(k); had {
			out.Set(k, mergeBoundaryComponent(inherited, ov))
			continue
		}
		out.Set(k, ov)
	}
	return out
}

// mergeBoundaryComponent composes one routed component the child redeclared onto
// the one it inherited, per key.
func mergeBoundaryComponent(base, overlay types.BoundaryComponent) types.BoundaryComponent {
	out := base
	if overlay.Description != "" {
		out.Description = overlay.Description
	}
	if overlay.SubDirectory != "" {
		out.SubDirectory = overlay.SubDirectory
	}
	out.Annotations = mergeAnnotationValueMaps(base.Annotations, overlay.Annotations)
	out.Infra = mergeInheritedAnyMaps(base.Infra, overlay.Infra)
	out.Features = mergeBoundaryFeatureMaps(base.Features, overlay.Features)
	out.Uses = mergeReleaseUsesBlocks(base.Uses, overlay.Uses)
	return out
}

// mergeBoundaryFeatureMaps composes two feature-attachment maps per instance
// name, and one attachment per pillar.
func mergeBoundaryFeatureMaps(
	base, overlay types.OrderedMap[types.BoundaryFeature],
) types.OrderedMap[types.BoundaryFeature] {
	return mergeAttachmentMaps(base, overlay, func(inherited, ov types.BoundaryFeature) types.BoundaryFeature {
		merged := inherited
		merged.Type, merged.Description, merged.Infra = composeAttachmentDeclaration(
			inherited.Type, inherited.Description, inherited.Infra, ov.Type, ov.Description, ov.Infra)
		merged.Uses = mergeUsesSocketValueMaps(inherited.Uses, ov.Uses)
		return merged
	})
}

// mergeConnectionAttachmentMaps composes the attachments a Release connection
// entry carries, per slot name and then per pillar, by the rule a component
// entry's own attachments compose under.
//
// It is that rule and not a parallel one. A connection attachment the Release
// writes is the same declaration as a component attachment it writes, less the
// wiring a connection attachment cannot carry, so a child Release sizing a
// credential on a connection and a child sizing one on the component meet one
// behaviour: the type and description replace when the child writes them, the
// sizing composes key by key, and an attachment only the parent wrote stands.
func mergeConnectionAttachmentMaps(
	base, overlay types.OrderedMap[types.ReleaseConnectionAttachment],
) types.OrderedMap[types.ReleaseConnectionAttachment] {
	return mergeAttachmentMaps(base, overlay, func(inherited, ov types.ReleaseConnectionAttachment) types.ReleaseConnectionAttachment {
		merged := inherited
		merged.Type, merged.Description, merged.Infra = composeAttachmentDeclaration(
			inherited.Type, inherited.Description, inherited.Infra, ov.Type, ov.Description, ov.Infra)
		return merged
	})
}

// mergeAttachmentMaps composes two attachment maps a Release writes per instance
// name: a name only the parent carries stands, a name only the child carries is
// introduced, and a name both carry is composed by mergeOne. The parent's author
// order is kept and the child's new names follow in theirs, the order every other
// map in this file composes in.
func mergeAttachmentMaps[A any](base, overlay types.OrderedMap[A], mergeOne func(inherited, ov A) A) types.OrderedMap[A] {
	out := types.OrderedMap[A]{}
	for k, v := range base.All() {
		out.Set(k, v)
	}
	for k, ov := range overlay.All() {
		inherited, had := out.Get(k)
		if !had {
			out.Set(k, ov)
			continue
		}
		out.Set(k, mergeOne(inherited, ov))
	}
	return out
}

// composeAttachmentDeclaration is the per-pillar rule for one attachment a
// Release writes and a child Release redeclares: the type and the description are
// each one indivisible value and replace when the child writes one, and the infra
// composes key by key. It is the one home of that rule, read by both attachment
// surfaces a Release has — a component entry's own and a connection entry's.
func composeAttachmentDeclaration(
	baseType, baseDescription string, baseInfra types.OrderedMap[any],
	overlayType, overlayDescription string, overlayInfra types.OrderedMap[any],
) (string, string, types.OrderedMap[any]) {
	typ, description := baseType, baseDescription
	if overlayType != "" {
		typ = overlayType
	}
	if overlayDescription != "" {
		description = overlayDescription
	}
	return typ, description, mergeInheritedAnyMaps(baseInfra, overlayInfra)
}

// mergeUsesSocketValueMaps composes an attachment's topology wiring per socket
// name. A socket value is one expression, so a socket the child wrote replaces
// the one it inherited whole rather than composing with it.
func mergeUsesSocketValueMaps(
	base, overlay types.OrderedMap[types.FeatureUsesSocketValue],
) types.OrderedMap[types.FeatureUsesSocketValue] {
	out := types.OrderedMap[types.FeatureUsesSocketValue]{}
	for k, v := range base.All() {
		out.Set(k, v)
	}
	for k, v := range overlay.All() {
		out.Set(k, v)
	}
	return out
}

// mergeReleaseUsesBlocks composes the operator edge surface, which is keyed by
// socket and then by alias, per key at both levels. Within one connection both
// documents wrote, the target replaces when the child names one, the per-edge
// infra composes key by key, and the attachments compose per slot by the rule a
// component entry's attachments compose under.
func mergeReleaseUsesBlocks(
	base, overlay types.OrderedMap[types.OrderedMap[types.ReleaseUsesEntry]],
) types.OrderedMap[types.OrderedMap[types.ReleaseUsesEntry]] {
	out := types.OrderedMap[types.OrderedMap[types.ReleaseUsesEntry]]{}
	for k, v := range base.All() {
		out.Set(k, v)
	}
	for socket, ov := range overlay.All() {
		inherited, had := out.Get(socket)
		if !had {
			out.Set(socket, ov)
			continue
		}
		merged := types.OrderedMap[types.ReleaseUsesEntry]{}
		for alias, entry := range inherited.All() {
			merged.Set(alias, entry)
		}
		for alias, entry := range ov.All() {
			if base, hadAlias := merged.Get(alias); hadAlias {
				composed := base
				if entry.Target != "" {
					composed.Target = entry.Target
				}
				composed.Infra = mergeInheritedAnyMaps(base.Infra, entry.Infra)
				composed.Features = mergeConnectionAttachmentMaps(base.Features, entry.Features)
				merged.Set(alias, composed)
				continue
			}
			merged.Set(alias, entry)
		}
		out.Set(socket, merged)
	}
	return out
}

// mergeAggregateRoutingMaps composes sub-module routing per routed name.
func mergeAggregateRoutingMaps(
	base, overlay types.OrderedMap[types.AggregateRouting],
) types.OrderedMap[types.AggregateRouting] {
	out := types.OrderedMap[types.AggregateRouting]{}
	for k, v := range base.All() {
		out.Set(k, v)
	}
	for k, ov := range overlay.All() {
		inherited, had := out.Get(k)
		if !had {
			out.Set(k, ov)
			continue
		}
		out.Set(k, types.AggregateRouting{
			Components: mergeBoundaryComponentMaps(inherited.Components, ov.Components),
			Aggregates: mergeAggregateRoutingMaps(inherited.Aggregates, ov.Aggregates),
		})
	}
	return out
}

// mergeInheritedAnyMaps composes the open-content maps a Release carries — a
// routed component's infra, an attachment's infra, a per-edge infra — per key,
// recursing while both sides are maps and replacing whole at the first value
// that is not. A key the overlay writes as null is removed from the result.
//
// It is order-preserving, and that is why it exists rather than delegating to
// the parameter merge this file also calls. The two do the same arithmetic and
// are deliberately not one function: a parameter map is composed at the template
// boundary, which discards key order on purpose because a template reads by key,
// while infra travels into the rendered artifact, where key order is part of the
// bytes a deterministic build owes. A single implementation would have to pick
// one of those, and either choice is wrong on the other side.
func mergeInheritedAnyMaps(base, overlay types.OrderedMap[any]) types.OrderedMap[any] {
	out := types.OrderedMap[any]{}
	for k, v := range base.All() {
		out.Set(k, v)
	}
	for k, ov := range overlay.All() {
		if ov == nil {
			out.Delete(k)
			continue
		}
		inherited, had := out.Get(k)
		if !had {
			out.Set(k, ov)
			continue
		}
		baseNested, baseIsMap := inheritedNestedMap(inherited)
		overlayNested, overlayIsMap := inheritedNestedMap(ov)
		if baseIsMap && overlayIsMap {
			merged := mergeInheritedAnyMaps(baseNested, overlayNested)
			out.Set(k, &merged)
			continue
		}
		out.Set(k, ov)
	}
	return out
}

// inheritedNestedMap reports whether a value inside an open-content map is
// itself a map, accepting both storage forms such a tree uses.
//
// The two forms are not interchangeable to a type assertion and both genuinely
// occur: decoding a nested mapping inside an any-typed value yields a pointer,
// while a value-typed nested map is what this file's own composition builds. A
// reader testing for one form alone would silently degrade a partially
// overridden group to wholesale replacement, dropping every sibling the child
// did not write.
//
// A nil pointer is reported as not-a-map. It carries no entries, so the caller's
// remaining branch stores it unchanged, which is the same outcome an empty map
// reaches and gets there without dereferencing.
func inheritedNestedMap(v any) (types.OrderedMap[any], bool) {
	switch n := v.(type) {
	case types.OrderedMap[any]:
		return n, true
	case *types.OrderedMap[any]:
		if n == nil {
			return types.OrderedMap[any]{}, false
		}
		return *n, true
	}
	return types.OrderedMap[any]{}, false
}
