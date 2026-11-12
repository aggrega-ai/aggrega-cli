// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package catalog

import (
	"slices"
	"sort"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
)

// ComponentNameMappingNamesAComponent reports each key of the Manifest's
// mappings.components.name tier that names no component instance in the build.
//
// The name tier is matched by exact string against the instance a lookup asks
// about, and a component instance is looked up by the identity the composition
// gave it: its own name for a component the Aggregate declares, the qualified
// name for one a sub-aggregate contributes. A key naming anything else is
// consulted by no lookup, so every instance it was meant for resolves through the
// type tier, or halts unmapped, and the build would otherwise say nothing about
// the entry it ignored.
//
// # What it names, and why it reads the finalised graph
//
// The finding names the instances the key could have named: those of the type
// the mapped Recipe implements, or every instance where the Recipe is not in the
// catalog or no instance is of its type. And it offers the one the author
// probably meant — the component carrying an attachment the key names, which is
// the slip of writing an attachment's name in the wrong section, or else an
// instance one edit away. The instance set is complete once the composition is
// flattened, and the attachments a component carries once the attachment
// cascade has folded, so the graph's presentation is the earliest that holds
// every fact this reads.
//
// # Why it refuses only a run that emits
//
// It declares the severity the rules over a node's Recipe resolution declare, for
// their reason: an entry that binds nothing changes which Recipe renders the
// instance it was meant for, so a run emitting artifacts would emit them through
// a Recipe the author did not choose, while a run that only describes the
// composition is exactly where an author looks to find the name they meant.
var ComponentNameMappingNamesAComponent = constraint.Check[
	constraint.GraphView,
	constraint.DocumentMappingEntrySubject,
]{
	Sentinel: ErrNameMappingMatchedNothing(),
	Severity: constraint.SeverityEmissionFatal,
	Run: func(v constraint.GraphView, s constraint.DocumentMappingEntrySubject) []constraint.Objection {
		if s.MappingTier() != constraint.MappingTierComponents || s.MappingAxis() != constraint.MappingAxisName {
			return nil
		}
		key := s.MappingKey()
		if _, named := v.FlattenedNodeComponentType(key); named {
			return nil
		}
		reference, _ := v.ManifestRecipeMappingReference(s.SourceFile(), s.MappingTier(), s.MappingAxis(), key)
		componentType := mappedType(v.MappedRecipeImplements, s.MappingTier(), reference)

		nodes := v.FlattenedNodes()
		var typed []string
		for _, node := range nodes {
			if t, _ := v.FlattenedNodeComponentType(node); componentType != "" && stripVersion(t) == componentType {
				typed = append(typed, node)
			}
		}
		details := []constraint.DetailValue{constraint.Uncaptioned(key)}
		details = append(details, candidateDetails(
			"mapped to a recipe implementing", componentType,
			"component instances of that type in this build", typed,
			"component instances in this build", nodes,
		)...)

		var hosts []string
		for _, node := range nodes {
			if slices.Contains(v.MergedAttachmentNames(node), key) {
				hosts = append(hosts, node)
			}
		}
		if len(hosts) > 0 {
			return []constraint.Objection{s.RaiseWithAdvice(
				ErrNameMappingMatchedNothing(),
				"the key is the name of an attachment, which a features.name key names, while a components.name key names a component instance exactly as the Aggregate writes it; name the component carrying it here, or move the entry under mappings.features.name",
				append(details, constraint.Captioned("did you mean", joinedNames(hosts)))...,
			)}
		}
		const advice = "a components.name key names a component instance exactly as the Aggregate writes it; correct the key, or remove the entry"
		if near := resolver.NearMissCandidates(key, nodes); len(near) > 0 {
			details = append(details, constraint.Captioned("did you mean", joinedNames(near)))
		}
		return []constraint.Objection{s.RaiseWithAdvice(ErrNameMappingMatchedNothing(), advice, details...)}
	},
}

// FeatureNameMappingNamesAnAttachment reports each key of the Manifest's
// mappings.features.name tier that names no attachment in the build.
//
// An attachment is looked up by its own name — the key it is written under in a
// component's features, `<alias>.<slot>` for one written on a connection, or the
// key a boundary carries it under — and never by the name of the component it is
// attached to. A key naming anything else is consulted by no lookup, so every
// attachment it was meant for resolves through the type tier, or halts unmapped.
// The commonest slip is the host component's name written where the
// attachment's belongs.
//
// The finding names the attachments the key could have named: those of the
// Feature type the mapped FeatureRecipe implements, or every attachment where the
// FeatureRecipe is not in the catalog or no attachment is of its type. And it
// offers the one the author probably meant: where the key is a component's name,
// the attachments of that type the component carries, and otherwise an attachment
// one edit away. Both attachment axes are read — the ones each node carries once
// the cascade has folded, and the ones each boundary carries at its own scope —
// so it reads the finalised graph's presentation, for the reason the component
// rule beside it does, and refuses at the same severity for the same reason.
var FeatureNameMappingNamesAnAttachment = constraint.Check[
	constraint.GraphView,
	constraint.DocumentMappingEntrySubject,
]{
	Sentinel: ErrNameMappingMatchedNothing(),
	Severity: constraint.SeverityEmissionFatal,
	Run: func(v constraint.GraphView, s constraint.DocumentMappingEntrySubject) []constraint.Objection {
		if s.MappingTier() != constraint.MappingTierFeatures || s.MappingAxis() != constraint.MappingAxisName {
			return nil
		}
		key := s.MappingKey()
		attachments := buildAttachments(v.FlattenedNodes(), v.MergedAttachmentNames, v.AttachmentResolvedType, v.BoundaryAttachmentTypes)
		if slices.ContainsFunc(attachments, func(a buildAttachment) bool { return a.name == key }) {
			return nil
		}
		reference, _ := v.ManifestRecipeMappingReference(s.SourceFile(), s.MappingTier(), s.MappingAxis(), key)
		featureType := mappedType(v.MappedRecipeImplements, s.MappingTier(), reference)
		ofType := func(a buildAttachment) bool {
			return featureType != "" && stripVersion(a.featureType) == featureType
		}

		var all, typed []string
		for _, a := range attachments {
			all = append(all, a.name)
			if ofType(a) {
				typed = append(typed, a.name)
			}
		}
		details := []constraint.DetailValue{constraint.Uncaptioned(key)}
		details = append(details, candidateDetails(
			"mapped to a feature recipe implementing", featureType,
			"attachments of that type in this build", typed,
			"attachments in this build", all,
		)...)

		if _, isComponent := v.FlattenedNodeComponentType(key); isComponent {
			var carried, carriedOfType []string
			for _, a := range attachments {
				if a.host != key {
					continue
				}
				carried = append(carried, a.name)
				if ofType(a) {
					carriedOfType = append(carriedOfType, a.name)
				}
			}
			if len(carriedOfType) > 0 {
				carried = carriedOfType
			}
			if len(carried) > 0 {
				return []constraint.Objection{s.RaiseWithAdvice(
					ErrNameMappingMatchedNothing(),
					"the key is a component, and a features.name key names an attachment by its own name — the key it is written under in a component's features, or <alias>.<slot> for one written on a connection — never the component it is attached to; name the attachment here",
					append(details, constraint.Captioned("did you mean", joinedNames(carried)))...,
				)}
			}
		}
		const advice = "a features.name key names an attachment by its own name — the key it is written under in a component's features, or <alias>.<slot> for one written on a connection — never the component it is attached to; correct the key, or remove the entry"
		if near := resolver.NearMissCandidates(key, uniqueSorted(all)); len(near) > 0 {
			details = append(details, constraint.Captioned("did you mean", joinedNames(near)))
		}
		return []constraint.Objection{s.RaiseWithAdvice(ErrNameMappingMatchedNothing(), advice, details...)}
	},
}

// buildAttachment is one attachment the build holds, as a name-tier key is
// matched against it: its own name, the Feature type it attaches, and the
// component it sits on — empty for one a boundary carries at its own scope, which
// sits on no component.
type buildAttachment struct {
	name, featureType, host string
}

// buildAttachments gathers every attachment the build holds on both axes: the
// ones each node carries once the cascade has folded, node by node, then the ones
// each boundary carries at its own scope.
//
// It takes the facts it reads as the view's own accessors rather than as the
// view, so each check calling it still names every fact it reads on its view.
func buildAttachments(
	nodes []string,
	mergedNames func(node string) []string,
	resolvedType func(node, attachment string) string,
	boundaryTypes func() []constraint.BoundaryAttachmentType,
) []buildAttachment {
	var out []buildAttachment
	for _, node := range nodes {
		for _, name := range mergedNames(node) {
			out = append(out, buildAttachment{name: name, featureType: resolvedType(node, name), host: node})
		}
	}
	for _, b := range boundaryTypes() {
		out = append(out, buildAttachment{name: b.Attachment, featureType: b.FeatureType})
	}
	return out
}

// mappedType is the type the Recipe or FeatureRecipe a mapping reference names
// declares it implements, without its version, or empty where the reference
// resolves to nothing in the catalog.
func mappedType(
	mappedImplements func(tier, reference string) (string, bool),
	tier, reference string,
) string {
	implements, resolved := mappedImplements(tier, reference)
	if !resolved {
		return ""
	}
	return stripVersion(implements)
}

// candidateDetails is what a finding lists as the names a key could have named:
// the instances of the type the mapped realization implements where any is of
// that type, and otherwise every instance of the section's kind. An empty list
// contributes nothing rather than a quoted empty value.
//
// The names travel as one value rather than one detail each, because they are a
// single answer to one question — what the key could have named — and a reader
// needs them together to choose.
func candidateDetails(
	typeCaption, mappedType, typedCaption string, typed []string,
	allCaption string, all []string,
) []constraint.DetailValue {
	if len(typed) > 0 {
		return []constraint.DetailValue{
			constraint.Captioned(typeCaption, mappedType),
			constraint.Captioned(typedCaption, joinedNames(typed)),
		}
	}
	if len(all) == 0 {
		return nil
	}
	return []constraint.DetailValue{constraint.Captioned(allCaption, joinedNames(all))}
}

// joinedNames renders names de-duplicated, sorted and comma-separated, so a
// finding listing them reads the same on every run.
func joinedNames(names []string) string {
	return strings.Join(uniqueSorted(names), ", ")
}

// uniqueSorted returns names sorted with duplicates removed; two components may
// each carry an attachment of one name.
func uniqueSorted(names []string) []string {
	out := slices.Clone(names)
	sort.Strings(out)
	return slices.Compact(out)
}
