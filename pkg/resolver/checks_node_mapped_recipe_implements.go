// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver

import (
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
)

// NodeMappedRecipeImplementsTheNodeVersion reports each component instance that a
// Manifest mapping key whose version is not the instance's own sends to a Recipe
// whose spec.implements the instance's own type version does not satisfy.
//
// The type-axis lookup matches a key to an instance by the type's base when no
// key names the instance's type exactly, so a key written without a version
// reaches every version of its type, and a key naming one version reaches the
// instances of another. The rule over the mapping entry judges the Recipe against
// the key alone. Which version the Recipe has to serve is settled by the instance
// the key reaches, and an instance of one version mapped to a Recipe written for
// another would otherwise render through it with nothing said — its sizing read
// by no template, its contract served by a Recipe that never saw it. So the
// judgement is asked again here, once per instance, with the instance's own type
// at the version the build compiles it as standing where the key stood. It is
// the same judgement the rule over the entry asks, through
// [mappingRefusesRequest], so exact, caret and unversioned implements values are
// satisfied here exactly as they are there, and a Recipe written against a base
// the instance's type extends still serves it.
//
// # Why it is asked over the flattened composition
//
// It reads which key resolved an instance, which only the instance lookup knows,
// and the instance set is complete only once the composition is flattened: a
// component a sub-aggregate contributes is declared in that sub-aggregate's
// document and resolved under the identity the composition gave it. Asked any
// earlier, it would never be invoked for such a component, and a rule that is not
// invoked reports exactly what a rule that ran and approved reports.
//
// # What it passes over, and who reports it instead
//
// A key naming the instance's own version is the request the rule over the
// mapping entry already asked, so the second judgement repeats the first and
// cannot refuse what the first accepted; one mapping is never reported twice.
// Passed over too are an instance a name-axis entry reached, because
// a name keys an instance and says nothing about a type; an instance no mapping
// reached, or one whose lookup refused, which the rules over the instance's
// resolution report; and a reference the catalog does not hold, which the path
// that resolves references reports.
//
// An entry the rule over the mapping entry already refuses on its key alone is
// passed over too, so a Recipe implementing an unrelated type is reported once,
// over the entry, rather than once more for every instance the key reaches. A
// malformed version pin, on the instance's type or on the Recipe's
// spec.implements, is passed over as well: a pin naming no version cannot be
// compared with one, and the fault is its text rather than a mismatch.
//
// # Why it refuses every run
//
// It halts at the severity the rule over the mapping entry declares, and for the
// same reason: a Recipe rendering a contract it was not written for is the
// inconsistency that rule exists to stop, and the condition is one condition
// wherever it is found. A run that only describes the composition is refused as
// well, as the rule over the entry refuses it.
var NodeMappedRecipeImplementsTheNodeVersion = constraint.Check[
	constraint.TopologyView,
	constraint.NodeSubject,
]{
	Sentinel: ErrManifestImplementsMismatch(),
	Severity: constraint.SeverityFatal,
	Run: func(v constraint.TopologyView, s constraint.NodeSubject) []constraint.Objection {
		node := s.NodeID()
		refused, ok := nodeMappingRefused(node,
			v.NodeRecipeMappingTypeKey, v.NodeMappedRecipeReference, v.NodeResolvedComponentType,
			v.MappedRecipeImplements, v.MappedTypeExtendsAncestry)
		if !ok {
			return nil
		}
		componentType, _ := v.FlattenedNodeComponentType(node)
		// The instance leads, as every rule over an instance's resolution leads
		// with it. Its type follows as the author wrote it, then the version the
		// build compiled it as where the author named none, because that version
		// is what the Recipe was judged against and the authored type does not
		// show it. The mapping entry and what its Recipe declares come last, which
		// is where the remedy is written.
		detail := []constraint.DetailValue{
			constraint.Uncaptioned(node),
			constraint.Captioned("of type", componentType),
		}
		if refused.request != componentType {
			detail = append(detail, constraint.Captioned("compiled as", refused.request))
		}
		detail = append(detail,
			constraint.Captioned("reached through manifest mapping key", refused.key),
			constraint.Captioned("mapped to recipe", refused.reference),
			constraint.Captioned("whose spec.implements is", refused.implements),
		)
		return []constraint.Objection{s.RaiseWithAdvice(
			ErrManifestImplementsMismatch(),
			"map this type at this version to a recipe that implements it, with a mapping key "+
				"naming the version",
			detail...,
		)}
	},
}

// refusedNodeMapping is one component instance the rule over an instance's
// mapping refuses, as the values it judged it by: the type-axis key that
// reached it, the reference that key maps to, what that Recipe declares it
// implements, and the instance's type at the version the build compiled it as.
type refusedNodeMapping struct {
	key        string
	reference  string
	implements string
	request    string
}

// nodeMappingRefused reports the mapping a component instance was resolved
// through when the mapping-consistency judgement refuses the mapped Recipe for
// the instance's own type version, and false when the rule passes the instance
// over or finds the two consistent.
//
// It takes the facts it reads as the view's own accessors rather than as the
// view, for the reason manifestMappingRefused does: an accessor handed on is
// still named on the view at the call, so the check states exactly the facts it
// reads.
func nodeMappingRefused(
	node string,
	typeKey func(node string) (string, bool),
	mappedReference func(node string) (string, bool),
	resolvedType func(node string) string,
	mappedImplements func(tier, reference string) (string, bool),
	mappedAncestry func(componentType string) ([]string, bool),
) (refusedNodeMapping, bool) {
	key, byType := typeKey(node)
	if !byType {
		return refusedNodeMapping{}, false
	}
	reference, mapped := mappedReference(node)
	if !mapped {
		return refusedNodeMapping{}, false
	}
	implements, resolved := mappedImplements(constraint.MappingTierComponents, reference)
	if !resolved {
		return refusedNodeMapping{}, false
	}
	if mappingRefusesRequest(implements, key, constraint.MappingTierComponents, mappedAncestry) {
		return refusedNodeMapping{}, false
	}
	request := resolvedType(node)
	if _, _, malformed := mappingPinAtFault(request, implements); malformed {
		return refusedNodeMapping{}, false
	}
	if !mappingRefusesRequest(implements, request, constraint.MappingTierComponents, mappedAncestry) {
		return refusedNodeMapping{}, false
	}
	return refusedNodeMapping{
		key:        key,
		reference:  reference,
		implements: implements,
		request:    request,
	}, true
}
