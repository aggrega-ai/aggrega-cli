// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver

import (
	"fmt"
	"strings"

	"github.com/Masterminds/semver/v3"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
	"github.com/aggrega-ai/aggrega-cli/pkg/entitykind"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// The satisfaction rule a Manifest mapping entry is judged by has ONE home: the
// matcher below, sitting beside the rule that enforces it and beside the
// version-splitting grammar it is built on. The implements SEARCH in the package
// that holds the catalog reads the same matcher from here rather than carrying a
// second one, because a second matcher agrees on the day it is written and is
// free to disagree the day either side moves — and the disagreement would be a
// mapping accepted by one surface and refused by the other, with nothing
// comparing them.

// ErrManifestImplementsMismatch fires when a Manifest mapping entry's resolved
// Recipe (or FeatureRecipe) carries a spec.implements that does not match the
// Manifest's component-type / feature-type key for that entry.
//
// The mismatch is a structural consistency invariant of the mapping authoring
// surface: the Manifest entry IS the Recipe URI, and the Recipe's spec.implements
// is a consistency anchor rather than a dispatch mechanism. Mismatches halt at
// workspace load.
func ErrManifestImplementsMismatch() error { return errManifestImplementsMismatch }

var errManifestImplementsMismatch = cerrors.Register("manifest-implements-mismatch", cerrors.Meta{
	ShortCode: "E0096",
	Message:   "manifest mapping entry's recipe spec.implements does not match the mapped component-type / feature-type key",
	RulePlace: cerrors.RuleOverAuthoredInput(),
})

// manifestCategory is the authoring domain a fault on a Manifest is reported
// under. It is read from the one home of the kind-to-domain correspondence
// rather than written out here: a category named at a raise site is a second
// copy of that correspondence, and a copy agrees on the day it is written and is
// free to disagree the day either side moves.
//
// It panics on an unmapped kind, for the reason the other resolutions of this
// question panic: the kind is a constant chosen in the compiler rather than read
// from an authored document, so an unmappable one means the correspondence
// stopped covering a kind that raises categorised faults. That is a mistake in
// the compiler, not in anything a user wrote, and it would otherwise surface as a
// fault quietly attributed to the wrong authoring domain.
func manifestCategory() cerrors.Category {
	category, ok := cerrors.ClassifyEntityKind(string(entitykind.Manifest))
	if !ok {
		panic("resolver: the Manifest kind belongs to no authoring domain, so a manifest-mapping " +
			"fault cannot be attributed; extend the entity-kind category mapping to cover it")
	}
	return category
}

// ImplementsSatisfies reports whether a recipe declaring spec.implements==impl
// satisfies a request for componentType, under the three-form matching rule
// (exact, caret-range, unversioned base).
//
// It is expressed per entry so the implements SEARCH can collect every matching
// recipe — the catalog holds more than one recipe per type — and it is the single
// caret-aware implements matcher: the mapping-consistency rule below consumes it
// too, so mapping validation honours caret ranges rather than comparing
// version-stripped bases.
//
// It is exported because that search lives in the package that holds the catalog,
// which depends on this one: the grammar travels with the rule that enforces it,
// and the search reads it from here rather than carrying a second matcher that
// agrees today.
func ImplementsSatisfies(impl, componentType string) bool {
	if impl == componentType {
		return true
	}
	cBase, cVer, cHasVer := SplitVersionedURI(componentType)
	if !cHasVer {
		return false
	}
	iBase, iVer, iHasVer := SplitVersionedURI(impl)
	if iHasVer && iBase == cBase && strings.HasPrefix(iVer, "^") {
		if cv, err := semver.NewVersion(cVer); err == nil {
			if con, cerr := semver.NewConstraint(iVer); cerr == nil && con.Check(cv) {
				return true
			}
		}
		return false
	}
	return impl == cBase
}

// manifestMappingConsistent reports whether a Manifest mapping key and the mapped
// recipe's spec.implements refer to the same type at compatible versions. It is
// bidirectional over the version dimension:
//
//   - A version-pinned key (exact or caret) is judged by ImplementsSatisfies,
//     which honours an exact version, a caret range, and an unversioned
//     implements (a recipe implementing any version of the base).
//   - A bare (versionless) key imposes no version constraint, so a base match
//     alone is consistent — including a bare key against a version-pinned
//     implements. ImplementsSatisfies rejects that direction because it treats a
//     versionless request as unmatchable, so the bare-key case is handled here
//     rather than by loosening ImplementsSatisfies (whose other caller passes a
//     concrete component version, where a bare request never arises).
func manifestMappingConsistent(impl, key string) bool {
	if ImplementsSatisfies(impl, key) {
		return true
	}
	if _, _, keyHasVer := SplitVersionedURI(key); !keyHasVer {
		base, _, _ := SplitVersionedURI(impl)
		return base == key
	}
	return false
}

// manifestMappingSatisfiedByAncestry reports whether the mapped recipe's
// spec.implements satisfies any base the mapped type extends. Ancestry enters as
// additional INPUTS to manifestMappingConsistent — each base is offered exactly
// as the mapped key itself is — never as a second matching rule, so one
// satisfaction rule keeps answering for every caller while a Recipe written
// against a base serves everything that extends it.
func manifestMappingSatisfiedByAncestry(impl string, ancestry []string) bool {
	for _, base := range ancestry {
		if manifestMappingConsistent(impl, base) {
			return true
		}
	}
	return false
}

// mappedRecipeImplements resolves a Manifest mapping value against the catalog
// and returns what the recipe it names declares it implements, plus whether the
// reference resolved at all.
//
// The tier selects the catalog kind: the components tier names Recipes and the
// features tier names FeatureRecipes, and the reference string alone does not say
// which. A tier the vocabulary does not declare resolves nothing rather than
// defaulting to either kind — the set is closed by the authoring surface, so an
// unrecognised value is a caller mistake and answering for one of the two kinds
// would hide it.
//
// The nil-tolerance is the point rather than a detail of it, as it is for the
// other catalog readings beside it: a build running without the lookup filled
// resolves nothing, and a reader reaching the lookup function directly there
// dereferences nothing and takes the process down.
func mappedRecipeImplements(catalog CatalogLookup, tier, reference string) (string, bool) {
	switch tier {
	case constraint.MappingTierComponents:
		if catalog.Recipe == nil {
			return "", false
		}
		r, ok := catalog.Recipe(reference)
		if !ok {
			return "", false
		}
		return r.Spec.Implements, true
	case constraint.MappingTierFeatures:
		if catalog.FeatureRecipe == nil {
			return "", false
		}
		fr, ok := catalog.FeatureRecipe(reference)
		if !ok {
			return "", false
		}
		return fr.Spec.Implements, true
	}
	return "", false
}

// componentAncestryOf walks a component type's extends chain through the catalog,
// answering no ancestry and no fault where the build resolves none.
//
// The nil-tolerance carries a meaning here rather than only guarding a
// dereference, and the lookup's own documentation states it: a caller whose
// catalog holds no Component entries supplies no resolver, which declares that it
// resolves no ancestry. A mapping that relies on one is then judged on the direct
// comparison alone, which is the honest answer for such a caller and the wrong one
// for a caller that has an index and did not pass it.
func componentAncestryOf(catalog CatalogLookup, componentType string) ([]string, error) {
	if catalog.ComponentAncestry == nil {
		return nil, nil
	}
	return catalog.ComponentAncestry(componentType)
}

// ManifestImplementsConsistencyFaults reports every mapping entry in a Manifest
// whose mapped recipe declares an implements the entry's own key does not match,
// one fault per entry, in the order the Manifest declares them.
//
// It presents the Manifest beside the catalog and names the rule rather than
// walking the mapping table itself: the rule is declared in this package as a
// constraint, so where it runs follows from the facts it reads, and the site each
// fault is positioned at is supplied by the framework rather than assembled here.
// The entries are read back off the presentation rather than off the Manifest
// beside it — they are one of the facts the presentation states, so reading them
// anywhere else would be answering from a second source that happens to agree.
//
// It takes the resolved Manifest path and hands it to the presentation, which is
// what makes every fault name the file the author has to open. A mapping entry is
// a coordinate inside a document rather than a document, so nothing in the entry's
// own coordinates says where it was written; the file travels from whatever
// resolved the Manifest, and every caller of this holds it.
//
// A chain that cannot be walked is reported AHEAD of the mismatches, once per
// offending type, naming the walk's own fault. The rule reads that outcome and
// never produces it — the presentation keeps the failure and this reports it —
// which is the same discipline the annotation-binding resolution applies, and it
// exists for the same reason: a rule reading a bare absence cannot tell a type
// with no ancestry from one whose chain did not walk, so the failure is reported
// where the resolving happened rather than inferred by every reader.
//
// It returns findings rather than a joined error because the verdict is a
// caller's to reach: a surface that produces a deployable artifact halts on a
// finding, while a surface that reports on a document enumerates it alongside its
// other findings.
//
// Returns nil for a nil Manifest and when every resolved mapping is consistent.
func ManifestImplementsConsistencyFaults(
	manifestPath string,
	manifest *types.Manifest,
	catalog CatalogLookup,
) []error {
	if manifest == nil {
		return nil
	}
	presented := newManifestCatalogView(manifestPath, manifest, catalog)
	objections := constraint.RunOnDocumentMappingEntries(
		manifestPath,
		presented.ManifestRecipeMappingEntries(manifestPath),
		constraint.CatalogView(presented),
		manifestMappingRecipeImplementsTheMappedType,
	)

	var faults []error
	for _, f := range presented.unwalkableAncestries() {
		faults = append(faults, ancestryWalkFaultFrom(presented, manifestPath, f))
	}
	for _, o := range objections {
		faults = append(faults, manifestImplementsFaultFrom(o))
	}
	return faults
}

// ancestryWalkFaultFrom renders one type whose extends chain did not walk into
// the failure shape this rule's callers already read.
//
// The message wraps the walk's OWN fault rather than this condition, so a caller
// matching the chain's sentinel keeps matching: the mapping may be perfectly well
// authored, and reporting a chain defect as a mapping mismatch sends the author to
// the wrong file.
//
// The reference is read back off the presentation rather than carried through the
// resolution, because it is a coordinate of the mapping entry and not of the type:
// the resolution is keyed by the type alone, and the entry that asked for it is
// the type-axis entry under the components tier, whose value the presentation
// already states. An entry the presentation does not declare contributes no
// reference, which leaves the sentence naming the type alone rather than naming a
// value nobody wrote.
func ancestryWalkFaultFrom(
	presented *manifestCatalogView,
	manifestPath string,
	failure componentAncestryWalkFailure,
) error {
	reference, _ := presented.ManifestRecipeMappingReference(
		manifestPath,
		constraint.MappingTierComponents,
		constraint.MappingAxisType,
		failure.ComponentType,
	)
	return cerrors.Wrap(failure.Err, manifestCategory(),
		cerrors.SourceLocation{File: manifestPath},
		fmt.Sprintf(
			"manifest mapping %q -> %q: the mapped component type's extends chain cannot be walked, "+
				"so the mapping cannot be judged", failure.ComponentType, reference))
}

// manifestImplementsFaultFrom renders one objection into the failure shape this
// rule's callers already read.
//
// The sentence is composed here rather than taken from the framework's own
// renderer, and the reason is that this rule's wording is published: the
// halting-conditions reference reproduces it verbatim, so the sentence is a
// contract with a reader rather than a presentation choice. The framework's
// renderer composes a different, general shape — a captioned value reads as its
// caption then the quoted value — which cannot produce this rule's leading
// element, two quoted values joined by an arrow, without smuggling a quoted value
// into a caption.
//
// What it does share with every other diagnostic naming a condition is the
// COMPOSITION: the registered sentence, then the occurrence's values in
// parentheses, assembled by the one function that spells that shape. Only the
// values are this rule's own.
//
// The file the fault names is read off the objection rather than passed in
// again, so the file the framework attributed the subject to and the file the
// diagnostic points at are one value.
//
// The rule's own values arrive in the order the rule supplies them, which is the
// contract between the two and is asserted rather than assumed: a rule and the
// conversion that renders it are two halves of one sentence, and a silent
// mismatch would produce a message with the values in the wrong slots.
func manifestImplementsFaultFrom(o constraint.Objection) error {
	detail := o.Detail()
	if len(detail) != manifestImplementsDetailCount {
		panic(fmt.Sprintf(
			"resolver: the mapping-consistency rule supplied %d value(s) and this conversion composes "+
				"its sentence from %d; the rule and its conversion are two halves of one sentence, so "+
				"change them together",
			len(detail), manifestImplementsDetailCount))
	}
	key := detail[manifestImplementsDetailKey]
	reference := detail[manifestImplementsDetailReference]
	implements := detail[manifestImplementsDetailImplements]

	values := []string{
		fmt.Sprintf("manifest mapping %q -> %q", key, reference),
		fmt.Sprintf("recipe spec.implements is %q", implements),
		fmt.Sprintf("expected %q, or a type %q extends", key, key),
	}
	if detail[manifestImplementsDetailTier] == constraint.MappingTierFeatures {
		// The feature arm names no extends alternative, because a Feature's
		// extends composes its pillars rather than inheriting them: whether a
		// FeatureRecipe written against a base serves an extending Feature is a
		// separate question this rule does not answer, so offering the base as a
		// remedy would suggest an edit that does not resolve the fault.
		values = []string{
			fmt.Sprintf("manifest feature mapping %q -> %q", key, reference),
			fmt.Sprintf("featurerecipe spec.implements is %q", implements),
			fmt.Sprintf("expected %q", key),
		}
	}
	return cerrors.Wrap(ErrManifestImplementsMismatch(), manifestCategory(),
		cerrors.SourceLocation{File: o.SourceFile()},
		cerrors.ComposeMessage(ErrManifestImplementsMismatch(), values, ""))
}

// The order the mapping-consistency rule supplies its values in, named so the
// conversion above reads them by name rather than by position at the call site.
//
// They are positions and not keys because that is what an objection's detail is:
// an ordered list of values whose meaning belongs to the rule that raised them.
// Naming the positions here is what keeps the two halves legible as one sentence
// — the rule raises in this order, the conversion reads in this order, and the
// count below is what makes a divergence loud.
const (
	manifestImplementsDetailKey = iota
	manifestImplementsDetailReference
	manifestImplementsDetailImplements
	manifestImplementsDetailTier
	manifestImplementsDetailCount
)
