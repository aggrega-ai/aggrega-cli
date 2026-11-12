// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package resolver

import (
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// The reading over what a Release's boundaries place, expressed as a
// constraint declaration. See checks.go for what a declaration here is.

// componentIsPlacedInOneBoundary reports a placement that puts a component in a
// boundary another placement already put it in.
//
// The shape has no single meaning, which is why it halts rather than being
// ranked. A component's boundary context — its output directory, sub-directory,
// sync wave, annotations and boundary infrastructure — and its per-edge
// dependency surface are read by two separate walks of the same tree, and a
// component placed twice resolves to a different declaration in each: a node
// would take its output location from one entry and its per-edge configuration
// from another. Halting is what makes that unreachable; picking a winner would
// produce a build nobody asked for.
//
// A boundary places a component two ways and both count. It names the component,
// under the id it carries once the composition is flattened, and it routes a
// whole sub-module in, which places every component that module contains. So this
// also covers the same sub-module routed into two boundaries, a module routed
// into one boundary with a module nested inside it routed into another, and a
// component named at one boundary while the module containing it is routed into a
// different one.
//
// Two placements at ONE boundary are not a conflict unless both name a component:
// routing a module and naming one of its components under the same boundary
// agree about where that component belongs, and nesting a routing entry inside
// another under one boundary is pure addressing. One id named twice under one
// boundary is still a fault, because two entries then carry payloads for one node
// and nothing decides between them.
//
// It reports against the LATER placement and names the earlier one, because the
// earlier is the one already in effect and the later is what an author edits to
// resolve the ambiguity. Where several earlier placements conflict, the first is
// named: it is the one the rest collided with.
//
// It compares against earlier placements only, and it finds them by reading the
// declared set back off the view and stopping at its own coordinates rather than
// by holding state — a rule invoked once per placement holds none between
// invocations, and what it may look back at is the document, which the view
// presents whole. The stop is exact because a placement's coordinates name one
// authored entry: within one boundary the component block's keys are unique, a
// routed component's id is its module prefix and its local name and the document
// envelope admits a separator in neither, and a routed module's prefix is unique
// the same way — so the only way two entries share all four coordinates is a
// change to those key patterns, which would have to move this rule with it.
//
// It reports EVERY offending placement rather than the first, which is the
// framework's accumulation rather than a property of how this is written: the
// rule is invoked once per placement and what each invocation raised is
// collected, so an author fixing a Release that places three components twice
// sees all three in one build.
//
// Its detail leads with what the offending entry addresses, which is what the
// registered wording is about — except on the routed-module axis, where the value
// is a sub-module rather than a component and is captioned so it cannot be read
// as one. The boundary each placement sits in, the block each was written in and
// the document they were both written in follow, each captioned, because the
// registered wording names none of them. The remedy is the registered wording's
// own and is not repeated here.
var componentIsPlacedInOneBoundary = constraint.Check[
	constraint.DocumentView,
	constraint.BoundaryPlacementSubject,
]{
	Sentinel: ErrDuplicateBoundaryComponent(),
	Severity: constraint.SeverityFatal,
	Run: func(
		v constraint.DocumentView,
		s constraint.BoundaryPlacementSubject,
	) []constraint.Objection {
		own := constraint.BoundaryPlacement{
			Document:     s.AuthoringDocumentKind(),
			BoundaryPath: s.BoundaryPath(),
			Reference:    s.PlacementReference(),
			Axis:         s.PlacementAxis(),
		}
		for _, prior := range v.ReleaseBoundaryPlacements() {
			if prior == own {
				break
			}
			if !placementsConflict(prior, own) {
				continue
			}
			return []constraint.Objection{s.RaiseWith(
				ErrDuplicateBoundaryComponent(),
				placedValue(own),
				constraint.Captioned(placedInCaption(own.Axis), own.BoundaryPath),
				constraint.Captioned(alreadyPlacedCaption(prior.Axis), prior.BoundaryPath),
				constraint.Captioned("that earlier entry naming", prior.Reference),
				constraint.Captioned("both declared in", s.SourceFile()),
			)}
		}
		return nil
	},
}

// placementsConflict reports whether two placements put one component in two
// boundaries.
//
// Two placements at the same boundary never conflict unless both name a
// component: whichever axes they came from they agree on where the component
// belongs, and one id named twice under one boundary is the exception because two
// entries then carry payloads for one node.
//
// Across boundaries a routed module is compared by containment rather than by
// equality, because routing a module places every component inside it. Equality
// is what fires in practice: reaching a nested module requires an entry for the
// module it nests under, which routes that outer module too, so two boundaries
// meeting at a nested module meet at its ancestor first. The containment test
// stands behind that, holding if the routed-module reading ever narrows to the
// modules an entry names rather than every one it implies.
func placementsConflict(a, b constraint.BoundaryPlacement) bool {
	aModule := a.Axis == constraint.PlacementRoutedModule
	bModule := b.Axis == constraint.PlacementRoutedModule
	if a.BoundaryPath == b.BoundaryPath {
		return !aModule && !bModule && a.Reference == b.Reference
	}
	switch {
	case aModule && bModule:
		return types.IsUnderModulePrefix(a.Reference, b.Reference) ||
			types.IsUnderModulePrefix(b.Reference, a.Reference)
	case aModule:
		return types.IsUnderModulePrefix(b.Reference, a.Reference)
	case bModule:
		return types.IsUnderModulePrefix(a.Reference, b.Reference)
	default:
		return a.Reference == b.Reference
	}
}

// placedValue is the offending placement's own reference, left bare where the
// registered wording already names what it is and captioned where it does not.
//
// The registered wording is about a component, and on two of the three axes the
// reference is one. On the routed-module axis it is a sub-module, so leaving it
// bare would present a module prefix as the component the wording names.
func placedValue(placement constraint.BoundaryPlacement) constraint.DetailValue {
	if placement.Axis == constraint.PlacementRoutedModule {
		return constraint.Captioned("sub-module", placement.Reference)
	}
	return constraint.Uncaptioned(placement.Reference)
}

// placedInCaption is the prose ahead of the offending placement's boundary,
// saying which block the entry was written in.
//
// The axis reaches the reader as words rather than as a value, because it is what
// the sentence is about rather than a thing the finding names: an author reads
// "named in the components block of boundary X", not a label they would then have
// to interpret.
func placedInCaption(axis string) string {
	switch axis {
	case constraint.PlacementRoutedComponent:
		return "named inside a routing entry in boundary"
	case constraint.PlacementRoutedModule:
		return "routed as a sub-module into boundary"
	default:
		return "named in the components block of boundary"
	}
}

// alreadyPlacedCaption is the prose ahead of the earlier placement's boundary,
// saying which block that entry was written in. It is the counterpart of
// [placedInCaption] and is worded as the continuation of the same sentence.
func alreadyPlacedCaption(axis string) string {
	switch axis {
	case constraint.PlacementRoutedComponent:
		return "while it is already named inside a routing entry in boundary"
	case constraint.PlacementRoutedModule:
		return "while it is already placed by a sub-module routed into boundary"
	default:
		return "while it is already named in the components block of boundary"
	}
}
