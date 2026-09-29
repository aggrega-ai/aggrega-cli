// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver

import (
	"fmt"

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
//
// Both references are split and their pins parsed by types.ParseVersionPin, the
// one home of the pin grammar, so a caret declaration is matched only against a
// requested version that is a strict semantic version. A malformed pin on either
// side satisfies nothing beyond an identical string, with the one exception the
// unversioned form carries by definition: an unversioned implements matches any
// request naming its base, whatever pin that request carries, a malformed one
// included. Which of the two is at fault is the reporting surface's to say, not
// this predicate's.
func ImplementsSatisfies(impl, componentType string) bool {
	if impl == componentType {
		return true
	}
	requested := types.ParseVersionPin(componentType)
	if _, pinned := requested.Pin(); !pinned {
		return false
	}
	declared := types.ParseVersionPin(impl)
	if declared.IsCaret() && declared.Base() == requested.Base() {
		version, rng := requested.Version(), declared.Range()
		return version != nil && rng != nil && rng.Check(version)
	}
	return impl == requested.Base()
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

// refusedMapping is one Manifest mapping entry the mapping-consistency rule
// refuses, as the values the rule judged it by: the key the entry is declared
// under, the reference it maps to, what that recipe declares it implements, and
// the tier it is written under.
type refusedMapping struct {
	key        string
	reference  string
	implements string
	tier       string
}

// manifestMappingRefused reports the mapping entry a subject names when the
// mapping-consistency rule refuses it, and false when the rule passes the entry
// over or finds it consistent.
//
// It is the rule, stated once, and two checks ask it. The one reporting a mismatch
// raises on a refusal whose pins are well formed, and the one reporting a
// malformed pin raises on a refusal whose key or implements carries one; each
// passes over exactly what the other raises on, so an entry is reported once and
// as the condition the author has to fix. A second statement of the rule for
// either check would agree on the day it was written and be free to drift the day
// either moved, and the drift would be an entry reported twice or not at all.
//
// It takes the three facts it reads as the view's own accessors rather than as the
// view. A check handing its view to a helper leaves the facts it reads outside its
// own body, where the reading that holds a check's declared view against the facts
// its predicate reads cannot follow them; an accessor handed on is still named on
// the view at the call, so each check calling this states exactly the facts it
// reads.
func manifestMappingRefused(
	s constraint.DocumentMappingEntrySubject,
	mappingReference func(document, tier, axis, key string) (string, bool),
	mappedImplements func(tier, reference string) (string, bool),
	mappedAncestry func(componentType string) ([]string, bool),
) (refusedMapping, bool) {
	// The name axis is passed over, and that bound is the rule's rather than an
	// omission. A name-axis entry keys an instance NAME, which the recipe's
	// implements says nothing about: a Recipe declares the type it implements and
	// never which instance may use it, so a comparison there would refuse every
	// instance whose name differs from a type — which is every one of them. The
	// type axis is where the two values are about the same thing.
	if s.MappingAxis() != constraint.MappingAxisType {
		return refusedMapping{}, false
	}
	reference, declared := mappingReference(
		s.SourceFile(), s.MappingTier(), s.MappingAxis(), s.MappingKey())
	if !declared {
		return refusedMapping{}, false
	}
	// An entry whose reference resolves to nothing is passed over rather than
	// reported. A missing or unresolvable recipe is a resolution concern owned by
	// the path that resolves references, which surfaces it with richer context —
	// including, for an entity skipped at a tolerant load, a replay of WHY it went
	// missing. Reporting "not found" here too would preempt that better diagnostic,
	// so this rule is the found-but-mismatched reading only.
	implements, resolved := mappedImplements(s.MappingTier(), reference)
	if !resolved {
		return refusedMapping{}, false
	}
	key := s.MappingKey()
	if manifestMappingConsistent(implements, key) {
		return refusedMapping{}, false
	}
	// Feature mappings are judged on the direct comparison alone. A Feature's
	// extends composes its pillars rather than inheriting them, so whether a
	// FeatureRecipe written against a base serves an extending Feature is a
	// separate question this rule does not answer — and resolving an ancestry in
	// order to accept a mapping on it would be answering it.
	if s.MappingTier() != constraint.MappingTierFeatures {
		// Ancestry is read only after the direct comparison has failed, so a
		// mapping that matches outright costs no catalog walk at all.
		bases, walkable := mappedAncestry(key)
		// A chain that did not walk leaves the mapping unjudged rather than
		// refused: the entry may be perfectly well authored, and reporting it as
		// the fault would send the author to the wrong file. The walk's own fault
		// is reported by whatever holds the resolution, once, so raising here as
		// well would report one defect twice.
		if !walkable {
			return refusedMapping{}, false
		}
		if manifestMappingSatisfiedByAncestry(implements, bases) {
			return refusedMapping{}, false
		}
	}
	return refusedMapping{
		key:        key,
		reference:  reference,
		implements: implements,
		tier:       s.MappingTier(),
	}, true
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
// one fault per entry, in the order the Manifest declares them. An entry whose key,
// or whose recipe's implements, carries a malformed version pin is reported as that
// malformation rather than as a mismatch.
//
// It presents the Manifest beside the catalog and names the rules rather than
// judging the mapping table itself: the mismatch and the malformed pin are two
// rules declared in this package as constraints, so where they run follows from
// the facts they read, and the site each fault is positioned at is supplied by the
// framework rather than assembled here. The entries are read back off the
// presentation rather than off the Manifest beside it — they are one of the facts
// the presentation states, so reading them anywhere else would be answering from a
// second source that happens to agree.
//
// Each entry is presented to both rules before the next one is, and that is what
// keeps the findings in the order the Manifest declares its entries. The two rules
// ask one predicate and part on its answer — each passes over exactly what the
// other raises on — so one entry yields at most one finding, and asking both about
// one entry at a time interleaves them by construction. Walking the whole table
// once per rule would report every mismatch ahead of every malformed pin.
//
// It takes the resolved Manifest path and hands it to the presentation, which is
// what makes every fault name the file the author has to open. A mapping entry is
// a coordinate inside a document rather than a document, so nothing in the entry's
// own coordinates says where it was written; the file travels from whatever
// resolved the Manifest, and every caller of this holds it.
//
// A chain that cannot be walked is reported AHEAD of the mismatches and the
// malformed pins, once per offending type, naming the walk's own fault. The rule
// reads that outcome and never produces it — the presentation keeps the failure
// and this reports it — which is the same discipline the annotation-binding
// resolution applies, and it exists for the same reason: a rule reading a bare
// absence cannot tell a type with no ancestry from one whose chain did not walk,
// so the failure is reported where the resolving happened rather than inferred by
// every reader.
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
	view := constraint.CatalogView(presented)
	entries := presented.ManifestRecipeMappingEntries(manifestPath)

	var findings []error
	for i := range entries {
		entry := entries[i : i+1]
		for _, o := range constraint.RunOnDocumentMappingEntries(
			manifestPath, entry, view, manifestMappingRecipeImplementsTheMappedType,
		) {
			findings = append(findings, manifestImplementsFaultFrom(o))
		}
		for _, o := range constraint.RunOnDocumentMappingEntries(
			manifestPath, entry, view, manifestMappingPinsAreWellFormed,
		) {
			findings = append(findings, mappingPinFaultFrom(o))
		}
	}

	// The chain faults are read only once every entry has been judged, because an
	// ancestry is resolved on the ask and the last entry may be the first to ask.
	var faults []error
	for _, f := range presented.unwalkableAncestries() {
		faults = append(faults, ancestryWalkFaultFrom(presented, manifestPath, f))
	}
	return append(faults, findings...)
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

// manifestImplementsFaultFrom renders one objection the mismatch rule raised into
// the failure shape this rule's callers already read.
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

// mappingPinAtFault reports which side of a refused mapping entry carries a
// malformed version pin — the reference carrying it, and the side it sits on — and
// true; false when neither pin is malformed. The key is judged first, because it is
// the value the Manifest being judged authored.
//
// It is the one judgement of whether a refusal is a malformed pin, and both
// readings of a refused entry ask it: the mismatch rule passes over what it
// reports, and the malformed-pin rule raises on it. It is asked only of an entry
// the mapping-consistency rule refuses, so it never turns a consistent mapping into
// a failure — an identical key and implements are consistent even when both carry
// the same malformed pin. What it changes is the explanation: a malformed pin names
// no version, so no key and no implements could be consistent with it, and "does
// not match" would send the author comparing two versions when the fault is one
// pin's text. The grammar is the one types.ParseVersionPin states, read here rather
// than restated.
func mappingPinAtFault(key, implements string) (pinned, side string, malformed bool) {
	if types.ParseVersionPin(key).Fault() != nil {
		return key, mappingPinSideKey, true
	}
	if types.ParseVersionPin(implements).Fault() != nil {
		return implements, mappingPinSideImplements, true
	}
	return "", "", false
}

// The two sides of a mapping entry a malformed pin can sit on, as the
// malformed-pin rule names them in its detail. They are the author's own words for
// the two values: the key written in the Manifest, and the field the mapped recipe
// declares.
const (
	mappingPinSideKey        = "mapping key"
	mappingPinSideImplements = "spec.implements"
)

// mappingPinFaultFrom renders one objection the malformed-pin rule raised into the
// failure shape the mapping-consistency rule's callers already read, at the file
// the framework attributed the entry to.
//
// The message is the one the halting-conditions reference describes for a mapping
// judged against its recipe's implements: the mapping entry and the side of it that
// carries the pin come ahead of the malformation's own values, and the three forms a
// pin takes are the advice. The side is named because the two sides are written in
// two documents — the key in the Manifest, the implements in the recipe it maps to.
//
// The malformation's own values are read from the one grammar rather than carried
// in the detail and reassembled here, so they have one spelling wherever the fault
// is reported. That the grammar agrees the pin is malformed is asserted rather than
// assumed, as the detail order is: the rule and this conversion are two halves of
// one sentence, and a silent disagreement would report a pin as malformed with no
// malformation to name.
func mappingPinFaultFrom(o constraint.Objection) error {
	detail := o.Detail()
	if len(detail) != mappingPinDetailCount {
		panic(fmt.Sprintf(
			"resolver: the malformed-pin rule supplied %d value(s) and this conversion composes its "+
				"sentence from %d; the rule and its conversion are two halves of one sentence, so "+
				"change them together",
			len(detail), mappingPinDetailCount))
	}
	pinned := detail[mappingPinDetailPinned]
	key := detail[mappingPinDetailKey]
	reference := detail[mappingPinDetailReference]

	pe, malformed := types.ParseVersionPin(pinned).Malformed()
	if !malformed {
		panic(fmt.Sprintf(
			"resolver: the malformed-pin rule raised over %q, whose pin the grammar accepts; the rule "+
				"and its conversion read one grammar, so they have come to disagree", pinned))
	}

	mapping := fmt.Sprintf("manifest mapping %q -> %q", key, reference)
	recipe := "recipe"
	if detail[mappingPinDetailTier] == constraint.MappingTierFeatures {
		mapping = fmt.Sprintf("manifest feature mapping %q -> %q", key, reference)
		recipe = "featurerecipe"
	}
	var where string
	switch side := detail[mappingPinDetailSide]; side {
	case mappingPinSideKey:
		where = "in the mapping key"
	case mappingPinSideImplements:
		where = "in the " + recipe + "'s spec.implements"
	default:
		panic(fmt.Sprintf(
			"resolver: the malformed-pin rule named the side %q, which this conversion does not "+
				"compose; the rule and its conversion are two halves of one sentence, so change them "+
				"together", side))
	}

	values := append([]string{mapping, where}, pe.Values()...)
	return cerrors.Wrap(types.ErrMalformedVersionPin(), manifestCategory(),
		cerrors.SourceLocation{File: o.SourceFile()},
		cerrors.ComposeMessage(types.ErrMalformedVersionPin(), values, types.VersionPinForms))
}

// The order the mismatch rule supplies its values in, named so the conversion
// above reads them by name rather than by position at the call site.
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

// The order the malformed-pin rule supplies its values in, named for the reason
// the mismatch rule's are: the rule raises in this order, the conversion reads in
// this order, and the count is what makes a divergence loud.
const (
	mappingPinDetailPinned = iota
	mappingPinDetailSide
	mappingPinDetailKey
	mappingPinDetailReference
	mappingPinDetailTier
	mappingPinDetailCount
)
