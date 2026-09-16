// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver

import (
	"fmt"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
)

// The rules over what an author wrote in an annotation, expressed as constraint
// declarations: each is a value naming the condition it enforces, what a
// violation does to the build, the facts it reads, and what it is invoked once
// per.
//
// Both are invoked once per annotation entry the author WROTE rather than once
// per component the entry takes effect on, and that is the load-bearing part of
// the shape. An annotation written on a scope reaches every component that scope
// holds, so keyed by component a rule would be asked once per component about one
// authored string, not at all for a scope holding no components, and not at all
// for every entry but one wherever two scopes write against a single component
// name — because the composition keys components by the id they carry once
// flattened and keeps the last. Every one of those is silence a caller cannot
// tell from approval.
//
// They read the routing through one declared fact rather than each walking the
// binding's route list, so the rule that a bare value routes through the
// binding's single declared path, and the tie-break for a path declared under
// both pillars, have one home and cannot come to be answered two ways.

// annotationSubkeyConformsToTheTargetFeature reports each authored annotation
// value the target Feature's contract refuses at the pillar and path its binding
// routes it through — a path the contract declares no property for, or a value
// the sub-contract located there does not admit.
//
// It ends where the routing ends. An annotation key the Manifest declares no
// binding for routes nowhere and contributes nothing to a build, so there is no
// contract to measure it against; the same holds for a subkey the binding's own
// map does not name. Neither is this rule's finding, and reporting one here would
// refuse an annotation the compiler simply passes over.
//
// It ends too where the target Feature's contract cannot be read. A Feature
// declaring no contract, a Feature type the catalog does not hold, and a contract
// that failed to resolve all arrive as the same negative, and all three are
// reported where the resolution happened rather than by this rule meeting the
// absence — the binding-target condition covers the second and the presentation
// holding the resolution covers the third.
//
// The finding names the value as well as the path, because the two failure shapes
// differ in which of them is at fault: a path the contract does not declare is a
// routing fault and the value is incidental, while a value the located
// sub-contract refuses is the other way round. Naming both leaves a reader able to
// tell which they are looking at without reading the condition's own sentence
// twice.
var annotationSubkeyConformsToTheTargetFeature = constraint.Check[
	constraint.NormalizedView,
	constraint.AuthoredAnnotationSubject,
]{
	Sentinel: ErrAnnotationSubkeyNotConforming(),
	Severity: constraint.SeverityFatal,
	Run: func(
		v constraint.NormalizedView,
		s constraint.AuthoredAnnotationSubject,
	) []constraint.Objection {
		target, routed := v.AnnotationBindingSubkeyTarget(s.AnnotationKey(), s.AnnotationSubkey())
		if !routed {
			return nil
		}
		schema, resolved := v.CompiledFeatureSchema(target.FeatureType)
		if !resolved {
			return nil
		}
		value, written := v.AuthoredAnnotationSubvalue(
			s.AuthoringDocumentKind(), s.AnnotationScopePath(), s.AnnotationComponentName(),
			s.AnnotationKey(), s.AnnotationSubkey(),
		)
		if !written {
			return nil
		}

		subSchema, found, skippable := schemaSubSchemaAtPath(schema, target.Pillar, target.Path)
		if skippable {
			return nil
		}
		if !found {
			return []constraint.Objection{s.RaiseWith(
				ErrAnnotationSubkeyNotConforming(),
				constraint.Uncaptioned(target.Path),
				constraint.Captioned("written under annotation", s.AnnotationKey()),
				constraint.Captioned("routed to Feature", target.FeatureType),
				constraint.Captioned("at pillar", target.Pillar),
				constraint.Captioned("which that Feature declares no property for under", "schema"),
			)}
		}
		terminal := target.Path
		if i := strings.LastIndex(terminal, "."); i >= 0 {
			terminal = terminal[i+1:]
		}
		msg, bad := subkeyConformanceFailure(schema, subSchema, terminal, value)
		if !bad {
			return nil
		}
		return []constraint.Objection{s.RaiseWith(
			ErrAnnotationSubkeyNotConforming(),
			constraint.Uncaptioned(target.Path),
			constraint.Captioned("written under annotation", s.AnnotationKey()),
			constraint.Captioned("carrying the value", fmt.Sprintf("%v", value)),
			constraint.Captioned("routed to Feature", target.FeatureType),
			constraint.Captioned("at pillar", target.Pillar),
			constraint.Captioned("which that Feature's schema refuses because", msg),
		)}
	},
}

// annotationRoutesThroughItsAuthoringSidesPillar reports each authored annotation
// whose binding routes it through a pillar the document it was written in may not
// use.
//
// The partition is the Aggregate-as-authoring-surface split expressed on the
// annotation shortcut: a developer's annotation contributes application values,
// an operator's contributes infrastructure sizing, and an annotation that crossed
// would let one surface author the other's pillar through a shortcut it could not
// write directly.
//
// The authoring side is the subject's own document coordinate rather than a fact,
// and that is what the kind being rooted at the authored site buys: the side an
// annotation was written on IS the document the entry sits in, so a rule invoked
// once per authored entry carries it and nothing has to be declared to supply it.
//
// A binding declaring paths under both pillars is not a violation. Each side
// fires the subset routed through its own pillar, which is the ordinary
// cross-side composition shape, so this rule reads the slot one entry routes
// through rather than the binding's whole route list.
//
// It reads no contract and no catalog, and its declared view says so: the pillar
// is settled by the binding the Manifest declares and the side by the document
// the entry sits in, both complete as soon as a document is presented. So the
// rule is placed at the earliest point its predicate allows rather than beside
// its sibling, and the finding is reachable on a build resolving against no
// catalog at all — which is the shape that would otherwise lose it.
var annotationRoutesThroughItsAuthoringSidesPillar = constraint.Check[
	constraint.DocumentView,
	constraint.AuthoredAnnotationSubject,
]{
	Sentinel: ErrAnnotationSidePillarMismatch(),
	Severity: constraint.SeverityFatal,
	Run: func(
		v constraint.DocumentView,
		s constraint.AuthoredAnnotationSubject,
	) []constraint.Objection {
		target, routed := v.AnnotationBindingSubkeyTarget(s.AnnotationKey(), s.AnnotationSubkey())
		if !routed {
			return nil
		}
		permitted, known := permittedAnnotationPillar(s.AuthoringDocumentKind())
		if !known || target.Pillar == permitted {
			return nil
		}
		return []constraint.Objection{s.RaiseWith(
			ErrAnnotationSidePillarMismatch(),
			constraint.Uncaptioned(s.AnnotationKey()),
			constraint.Captioned("which routes through pillar", target.Pillar),
			constraint.Captioned("at path", target.Path),
			constraint.Captioned("while the document it is written in may only route through",
				permitted),
		)}
	},
}

// permittedAnnotationPillar returns the pillar an annotation written in one
// authoring document may route through, and whether that document is one the
// partition covers.
//
// It is keyed on the document coordinate the framework carries rather than on a
// spelling of its own, so the value a rule compares against and the value a
// subject carries are the same closed set. A document outside that set is
// reported as uncovered rather than defaulted to either pillar: defaulting would
// silently admit one side's authoring on a surface nobody classified.
func permittedAnnotationPillar(document string) (string, bool) {
	switch document {
	case constraint.AuthoringDocumentAggregate:
		return bindingRoutePillarValues, true
	case constraint.AuthoringDocumentRelease:
		return bindingRoutePillarInfra, true
	default:
		return "", false
	}
}
