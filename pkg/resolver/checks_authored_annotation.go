// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver

import (
	"fmt"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// The rules over what an author wrote in an annotation, expressed as constraint
// declarations: each is a value naming the condition it enforces, what a
// violation does to the build, the facts it reads, and what it is invoked once
// per.
//
// Each is invoked once per annotation entry the author WROTE rather than once
// per component the entry takes effect on, and that is the load-bearing part of
// the shape. An annotation written on a scope reaches every component that scope
// holds, so keyed by component a rule would be asked once per component about one
// authored string, not at all for a scope holding no components, and not at all
// for every entry but one wherever two scopes write against a single component
// name — because the composition keys components by the id they carry once
// flattened and keeps the last. Every one of those is silence a caller cannot
// tell from approval.
//
// They decide whether, and where, an entry routes through one declared fact
// rather than each walking the binding's route list — a rule reading the list
// reads it only to name the routes in its finding, or to hand a bare value's
// routes to the one reading expansion places such a value by — so the rule that a
// bare value routes through the binding's single declared path, and the tie-break
// for a path declared under both pillars, have one home and cannot come to be
// answered two ways.

// annotationSubkeyConformsToTheTargetFeature reports each authored annotation
// value the target Feature's contract refuses at the pillar and path its binding
// routes it through — a path the contract declares no property for, or a value
// the sub-contract located there does not admit.
//
// It ends where the routing ends. An annotation key the Manifest declares no
// binding for routes nowhere and contributes nothing to a build, so there is no
// contract to measure it against, and reporting one here would refuse an
// annotation the compiler simply passes over. A subkey the binding's own map does
// not name is not this rule's finding either: it is refused, by
// [annotationSubkeyIsRoutedByItsBinding], under its own condition.
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

// annotationSubkeyIsRoutedByItsBinding reports each subkey written inside a
// structured annotation value that the annotation's binding does not route: the
// presented Manifest declares a binding for the key, and the subkey names none of
// the paths that binding lists.
//
// It reads the routing through the same lookup its siblings do, so a subkey this
// rule passes is one the others find a target for. A value written bare carries no
// subkey and is passed over: whether it routes is a question of the binding's
// single declared path, which [annotationBareValueRoutesThroughOnePath] answers. A
// key no binding claims is passed over too, for the reason the conformance rule
// above states.
//
// It reads no contract and no catalog, so it is declared against the narrower view
// and placed with the side rule, at the earliest point its predicate allows.
//
// The finding leads with the subkey, which is what the registered wording is
// about, and names the key and the paths the binding does route, in the order the
// binding declares them, so the author can see the spelling they meant.
var annotationSubkeyIsRoutedByItsBinding = constraint.Check[
	constraint.DocumentView,
	constraint.AuthoredAnnotationSubject,
]{
	Sentinel: ErrAnnotationSubkeyNotBound(),
	Severity: constraint.SeverityFatal,
	Run: func(
		v constraint.DocumentView,
		s constraint.AuthoredAnnotationSubject,
	) []constraint.Objection {
		if s.AnnotationSubkey() == "" {
			return nil
		}
		if _, routed := v.AnnotationBindingSubkeyTarget(s.AnnotationKey(), s.AnnotationSubkey()); routed {
			return nil
		}
		routes, bound := v.AnnotationBindingDeclaredRoutes(s.AnnotationKey())
		if !bound {
			return nil
		}
		declared := constraint.Captioned("whose binding declares", "no path")
		if len(routes) > 0 {
			paths := make([]string, 0, len(routes))
			for _, route := range routes {
				paths = append(paths, route.Path)
			}
			declared = constraint.Captioned("whose binding routes only", strings.Join(paths, ", "))
		}
		return []constraint.Objection{s.RaiseWith(
			ErrAnnotationSubkeyNotBound(),
			constraint.Uncaptioned(s.AnnotationSubkey()),
			constraint.Captioned("written under annotation", s.AnnotationKey()),
			declared,
		)}
	},
}

// annotationBareValueRoutesThroughOnePath reports each annotation value written
// bare — a string rather than a map of subkeys — that its binding does not route
// to exactly one path: the presented Manifest declares a binding for the key, and
// that binding declares several paths across its two pillar lists, or none.
//
// A bare value names no subkey, so the binding's paths are all it has to go by,
// and where they offer more than one place the value cannot be split between them
// and choosing one would deliver the author's string into a field they did not
// name. The decision is [bareAnnotationValueRoute]'s, the same reading expansion
// places a value by, so a value this rule passes is one expansion can place. That
// is why the rule reads the value as well as the routes: an empty value writes no
// leaf, and the reading admits it where one pillar list holds a single path.
//
// It is asked over the entry as written rather than once per component the entry
// reaches, so a value written on a scope is refused once, at the line the author
// typed, whether or not a component below takes it up. A key no binding claims is
// passed over, for the reason the conformance rule above states, and so is a value
// written as a map, whose subkeys the routing rule above answers for.
//
// The finding names the key, the value and the paths the binding declares, in the
// order the binding declares them, so the author can see which one they meant and
// write the value under it.
var annotationBareValueRoutesThroughOnePath = constraint.Check[
	constraint.NormalizedView,
	constraint.AuthoredAnnotationSubject,
]{
	Sentinel: ErrAnnotationScalarNotRoutable(),
	Severity: constraint.SeverityFatal,
	Run: func(
		v constraint.NormalizedView,
		s constraint.AuthoredAnnotationSubject,
	) []constraint.Objection {
		if s.AnnotationSubkey() != "" {
			return nil
		}
		routes, bound := v.AnnotationBindingDeclaredRoutes(s.AnnotationKey())
		if !bound {
			return nil
		}
		written, present := v.AuthoredAnnotationSubvalue(
			s.AuthoringDocumentKind(), s.AnnotationScopePath(), s.AnnotationComponentName(),
			s.AnnotationKey(), s.AnnotationSubkey(),
		)
		if !present {
			return nil
		}
		value, bare := written.(string)
		if !bare {
			return nil
		}
		var maps types.AnnotationBindingMaps
		paths := make([]string, 0, len(routes))
		for _, route := range routes {
			paths = append(paths, route.Path)
			switch route.Pillar {
			case bindingRoutePillarInfra:
				maps.Infra = append(maps.Infra, route.Path)
			case bindingRoutePillarValues:
				maps.Values = append(maps.Values, route.Path)
			}
		}
		if _, _, routed := bareAnnotationValueRoute(maps, value); routed {
			return nil
		}
		declared := constraint.Captioned("whose binding declares", "no path")
		if len(paths) > 0 {
			declared = constraint.Captioned("whose binding routes", strings.Join(paths, ", "))
		}
		return []constraint.Objection{s.RaiseWith(
			ErrAnnotationScalarNotRoutable(),
			constraint.Uncaptioned(s.AnnotationKey()),
			constraint.Captioned("carrying the bare value", value),
			declared,
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
