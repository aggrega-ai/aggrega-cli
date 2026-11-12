// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package resolver

import (
	"fmt"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// aggregateDocumentView presents one authored Aggregate to a check as named
// facts: the bytes the author wrote, and the parameter names the same document's
// contract declares.
//
// It answers about the one document it was built for, and that is the framework's
// contract rather than a simplification of it. A [constraint.DocumentSubject]
// stands for the one document being presented rather than a member of a document
// set, and the entries that invoke a check over a document exist because no point
// in the compilation holds a materialised set of documents. So the document
// argument each accessor takes names the document this value was built for, and
// asking about another document is a mistake rather than a lookup that misses —
// the accessor says so instead of answering.
//
// It satisfies [constraint.DocumentView], which declares facts an Aggregate
// cannot carry — an entity's inheritance chain, a Manifest's annotation
// bindings, a Recipe's overlay and source pointer. The embedded base supplies
// those and refuses each at the point a check reads it, naming the document kind
// that does carry it. What holds this type to the facts an Aggregate itself
// carries is the obligation asserted at the bottom of this file: an Aggregate
// fact added to the registry stops this type compiling, and a fact carried by
// any other kind does not reach it.
type aggregateDocumentView struct {
	// AggregateDocumentBase supplies the document-stage facts an Aggregate does
	// not carry, and declares nothing for the ones it does — so this type writes
	// bodies for exactly the Aggregate's own facts.
	constraint.AggregateDocumentBase

	// path is the workspace-relative file the document was read from. It is the
	// document coordinate itself, which is why an accessor's argument is checked
	// against it.
	path string

	// raw is the document's bytes as authored, before parameter interpolation.
	raw []byte

	// contract is the parameter contract loaded from those same bytes.
	contract types.OrderedMap[any]
}

// newAggregateDocumentView presents the Aggregate a resolve is holding: the file
// it was read from, its bytes as authored, and the parameter contract loaded from
// those bytes.
//
// The two halves are taken together rather than derived here, because a view
// projects facts already produced and never produces one of its own. A caller
// holding both is what makes the presentation possible at all.
func newAggregateDocumentView(
	path string,
	raw []byte,
	contract types.OrderedMap[any],
) aggregateDocumentView {
	return aggregateDocumentView{path: path, raw: raw, contract: contract}
}

// RawAggregateText returns the presented Aggregate's bytes as authored, before
// parameter interpolation.
func (v aggregateDocumentView) RawAggregateText(document string) []byte {
	v.refuseOtherDocument(document, "RawAggregateText")
	return v.raw
}

// DeclaredParameterKeys returns the parameter names the presented Aggregate's
// contract declares, in author order.
func (v aggregateDocumentView) DeclaredParameterKeys(document string) []string {
	v.refuseOtherDocument(document, "DeclaredParameterKeys")
	return v.contract.Keys()
}

// manifestDocumentView presents one authored Manifest to a check as named facts:
// the annotation binding keys its registry declares, the Feature each of those
// bindings routes to, and the entries of its recipe mapping table with the
// reference each of those entries names.
//
// It is the Manifest's counterpart to [aggregateDocumentView] and is deliberately
// the same shape rather than a shape of its own: both satisfy the one generated
// [constraint.DocumentView], both answer only about the document they were built
// for, and both take the facts their document cannot carry from the same base. A
// separate Manifest-only view interface written beside that one would give the
// document stage two contracts, and a check would then have to know which of them
// it was handed.
//
// What holds it to the facts a Manifest itself carries is the obligation asserted
// at the bottom of this file: a Manifest fact added to the registry stops this
// type compiling, and a fact carried by any other kind does not reach it.
//
// It answers about the one document it was built for, on the same terms
// [aggregateDocumentView] does: an accessor asked about another document refuses
// rather than answering, because answering would hand back this document's fact
// under another document's name.
//
// It presents the binding registry — the bindings written under
// mappings.features.annotations, which is the one site a Manifest declares an
// annotation binding at. That is also the surface annotation expansion consumes,
// so presenting it is presenting the whole of what the compiler acts on, and the
// fact's own declaration states the same site so a rule meets it there.
//
// It presents the mapping table on the same terms: the four axis maps written
// under mappings.components and mappings.features, which are the whole of what a
// Manifest says about which recipe renders what. A Manifest declaring no tier at
// all presents no entries, which is the same answer a declared-but-empty tier
// gives — the two are not distinguished here because nothing downstream asks the
// question, and a rule needing it would read the tier rather than the entry set.
type manifestDocumentView struct {
	// ManifestDocumentBase supplies the document-stage facts a Manifest does not
	// carry, and declares nothing for the ones it does — so this type writes
	// bodies for exactly the Manifest's own facts.
	constraint.ManifestDocumentBase

	// path is the workspace-relative file the Manifest was read from. It is the
	// document coordinate itself, which is why an accessor's argument is checked
	// against it.
	path string

	// bindings is the annotation binding registry built from that document.
	//
	// The registry is held rather than the Manifest, and that is the point
	// rather than a convenience: the registry is where that tier is already
	// read, so presenting from it keeps one reading of it instead of a second
	// one here that agrees with it today.
	bindings *AnnotationRegistry

	// mappings are the document's recipe mapping entries, in the order the
	// enumerating fact declares. It is the answer and not the table, on the
	// same terms the registry above is: a view presents facts already
	// produced, and holding the table would leave an accessor free to read a
	// field no fact declares.
	mappings []constraint.ManifestRecipeMapping

	// references is what each of those entries maps to, keyed by the entry's
	// own coordinates.
	//
	// It is built in the same walk that produces the entry list rather than in
	// a second one, which is what keeps the two answers about one table from
	// disagreeing: an entry present in the list and absent from this map would
	// make a rule invoked for it read as an entry nobody declared.
	references map[mappingEntryCoordinates]string
}

// mappingEntryCoordinates is one mapping entry's identity as the presentation
// keys it: the tier, the axis and the key together.
//
// It is a struct key rather than a joined string because the three are authored
// values and an author's key may contain whatever separator a join would pick —
// at which point two distinct entries would collide on one composed key, silently
// and in favour of whichever was written last.
type mappingEntryCoordinates struct {
	tier string
	axis string
	key  string
}

// newManifestDocumentView presents the Manifest a caller is holding: the file it
// was read from, and the annotation binding registry derived from it.
//
// The registry is derived here rather than taken from the caller because it has
// exactly one producer, and routing the presentation through that producer is
// what keeps the fact single-valued. A caller passing its own registry could pass
// one built from a different document.
// The mapping table is read here, in one walk producing both answers, for the
// reason the registry is derived rather than taken: a caller passing its own
// entry list could pass one built from a different document, and two walks over
// one table could disagree about which entries it holds.
func newManifestDocumentView(path string, manifest *types.Manifest) manifestDocumentView {
	v := manifestDocumentView{path: path, bindings: BuildAnnotationRegistry(manifest)}
	if manifest == nil {
		return v
	}
	v.references = make(map[mappingEntryCoordinates]string)
	read := func(tier, axis string, entries types.OrderedMap[string]) {
		for _, key := range entries.Keys() {
			reference, ok := entries.Get(key)
			if !ok {
				continue
			}
			v.mappings = append(v.mappings, constraint.ManifestRecipeMapping{
				Tier: tier, Axis: axis, Key: key,
			})
			v.references[mappingEntryCoordinates{tier: tier, axis: axis, key: key}] = reference
		}
	}
	readTier := func(tier string, entries *types.MappingTier) {
		if entries == nil {
			return
		}
		read(tier, constraint.MappingAxisType, entries.Type)
		read(tier, constraint.MappingAxisName, entries.Name)
	}
	readTier(constraint.MappingTierComponents, manifest.Spec.Mappings.Components)
	readTier(constraint.MappingTierFeatures, manifest.Spec.Mappings.Features)
	return v
}

// ManifestRecipeMappingEntries returns the entries of the presented Manifest's
// recipe mapping table, in the order the fact declares: the components tier
// before the features tier, and within each the type axis before the name axis,
// each axis in author order.
func (v manifestDocumentView) ManifestRecipeMappingEntries(
	document string,
) []constraint.ManifestRecipeMapping {
	v.refuseOtherDocument(document, "ManifestRecipeMappingEntries")
	return v.mappings
}

// ManifestRecipeMappingReference returns the Recipe or FeatureRecipe reference
// the entry at those coordinates names, and whether the presented Manifest
// declares an entry there at all.
func (v manifestDocumentView) ManifestRecipeMappingReference(
	document string,
	tier string,
	axis string,
	key string,
) (string, bool) {
	v.refuseOtherDocument(document, "ManifestRecipeMappingReference")
	reference, ok := v.references[mappingEntryCoordinates{tier: tier, axis: axis, key: key}]
	return reference, ok
}

// ManifestAnnotationBindingKeys returns the annotation keys the presented
// Manifest's registry declares, in author order.
func (v manifestDocumentView) ManifestAnnotationBindingKeys(document string) []string {
	v.refuseOtherDocument(document, "ManifestAnnotationBindingKeys")
	return v.bindings.Keys()
}

// ManifestAnnotationBindingFeature returns the Feature the named binding routes
// to, and whether the presented Manifest declares a binding of that key at all.
func (v manifestDocumentView) ManifestAnnotationBindingFeature(
	document string,
	binding string,
) (string, bool) {
	v.refuseOtherDocument(document, "ManifestAnnotationBindingFeature")
	declared, ok := v.bindings.Lookup(binding)
	if !ok {
		return "", false
	}
	return declared.Feature, true
}

// ManifestAnnotationBindingRoutes returns the routing slots the named binding
// claims on the Feature it targets, in author order with each pillar's own paths
// in the order the binding declares them.
//
// A key the registry does not answer for yields no routes, which is the same
// answer a binding routing nowhere gives. The two are not confused downstream
// because whether the presented Manifest declares the key at all is what
// ManifestAnnotationBindingFeature's second result says, so a rule needing to
// tell them apart asks that one.
//
// The pillars are visited in a fixed order rather than the order the author
// happened to write the two blocks in, because a Manifest's maps block is a
// mapping with two named keys rather than a sequence: there is no author order
// between them to carry, and a rule reporting in invocation order needs one that
// does not move.
func (v manifestDocumentView) ManifestAnnotationBindingRoutes(
	document string,
	binding string,
) []constraint.AnnotationBindingRoute {
	v.refuseOtherDocument(document, "ManifestAnnotationBindingRoutes")
	routes, _ := v.AnnotationBindingDeclaredRoutes(binding)
	return routes
}

// AnnotationBindingDeclaredRoutes returns the routing slots the binding of one
// annotation key claims, and whether the presented Manifest declares a binding of
// that key at all.
//
// It takes no document argument, for the reason AnnotationBindingSubkeyTarget
// takes none: it is asked by a rule over an annotation an author wrote, which holds
// the key and no Manifest coordinate. The document-keyed routes above are this
// answer asked about the presented document, so the table has one reading.
func (v manifestDocumentView) AnnotationBindingDeclaredRoutes(
	binding string,
) ([]constraint.AnnotationBindingRoute, bool) {
	declared, ok := v.bindings.Lookup(binding)
	if !ok {
		return nil, false
	}
	var routes []constraint.AnnotationBindingRoute
	for _, pillar := range []struct {
		name  string
		paths []string
	}{
		{name: bindingRoutePillarInfra, paths: declared.Maps.Infra},
		{name: bindingRoutePillarValues, paths: declared.Maps.Values},
	} {
		for _, path := range pillar.paths {
			routes = append(routes, constraint.AnnotationBindingRoute{
				Pillar: pillar.name,
				Path:   path,
			})
		}
	}
	return routes, true
}

// AnnotationBindingSubkeyTarget returns what one subkey written under an
// annotation key has to satisfy — the Feature, the pillar and the path its value
// is routed to — and whether the presented Manifest routes it anywhere at all.
//
// It takes no document argument, unlike the binding facts above, because it
// needs none: the accessor's key is the binding and the subkey, and the document
// is the presentation itself. A parameter here would be a value the caller has
// to have and the answer does not depend on.
//
// The empty subkey asks about an annotation written as a bare value. Such a
// value carries no subkey to route by, so it routes through the binding's single
// declared path — and only where exactly one is declared across both pillars. A
// binding declaring several is answered as routing nowhere rather than as
// routing through whichever came first: the value cannot be split between them,
// and picking one would deliver an author's single string into a field they did
// not name. The answer is [bindingSinglePath]'s, the reading expansion places a
// bare value carrying content by.
//
// A subkey the binding's own map does not name is answered the same way, which
// is one of the two things the second result separates. Whether the key names a
// binding at all is the other, and a rule needing to tell them apart asks
// ManifestAnnotationBindingFeature beside this.
//
// The pillar comes from the routing lookup rather than from a scan of the route
// list, so the tie-break between a path declared under both pillars is the one
// the compiler acts on rather than a second reading of it.
func (v manifestDocumentView) AnnotationBindingSubkeyTarget(
	binding string,
	subkey string,
) (constraint.AnnotationSubkeyTarget, bool) {
	declared, ok := v.bindings.Lookup(binding)
	if !ok {
		return constraint.AnnotationSubkeyTarget{}, false
	}
	if subkey == "" {
		path, pillar, single := bindingSinglePath(declared.Maps)
		if !single {
			return constraint.AnnotationSubkeyTarget{}, false
		}
		return constraint.AnnotationSubkeyTarget{
			FeatureType: declared.Feature,
			Pillar:      pillar,
			Path:        path,
		}, true
	}
	pillar, routed := declared.Maps.Lookup(subkey)
	if !routed {
		return constraint.AnnotationSubkeyTarget{}, false
	}
	return constraint.AnnotationSubkeyTarget{
		FeatureType: declared.Feature,
		Pillar:      pillar,
		Path:        subkey,
	}, true
}

// The two pillars an annotation binding routes into, named once here so the
// presentation and any rule reading a route agree on the spelling rather than
// each writing its own literal. They are the names the binding's author writes
// under maps, which is what makes them the right value to carry: a route is
// reported back to the author in the words the author used.
const (
	bindingRoutePillarInfra  = "infra"
	bindingRoutePillarValues = "values"
)

// ManifestExtendsChainCycleEdge refuses: this view presents one Manifest
// document, and the fact is read off a walk of the chain that document heads.
//
// Both chain facts refuse here rather than being answered from the document,
// and the ground is that the answer is not in the document. The reference a
// Manifest declares is one string; whether following it returns to a file
// already in the chain, and how many files the chain reaches, are properties of
// the walk that follows it — and the walk resolves each reference against the
// Release's directory, which this presentation neither holds nor may reach.
// Answering from the document would report an absence the chain does not have,
// and a rule cannot tell that from a Manifest that heads no chain at all.
//
// Which presentation does answer them is named rather than left to a reader: the
// walk builds its own, beside the rules it invokes.
func (v manifestDocumentView) ManifestExtendsChainCycleEdge(
	document string,
) (constraint.ExtendsChainCycleEdge, bool) {
	panic(v.refuseChainFact("ManifestExtendsChainCycleEdge"))
}

// ManifestExtendsChainLength refuses, for the reason
// [manifestDocumentView.ManifestExtendsChainCycleEdge] states.
func (v manifestDocumentView) ManifestExtendsChainLength(document string) (int, bool) {
	panic(v.refuseChainFact("ManifestExtendsChainLength"))
}

// refuseChainFact is what this view says about a chain fact, which it was not
// built to answer.
func (v manifestDocumentView) refuseChainFact(accessor string) string {
	return fmt.Sprintf(
		"manifestDocumentView.%s asked of a view built from one Manifest document: the fact is "+
			"read from a walk of the chain that document heads, so present that walk",
		accessor)
}

// refuseOtherDocument refuses an accessor call naming a document this value was
// not built for, for the reason [aggregateDocumentView.refuseOtherDocument]
// states.
func (v manifestDocumentView) refuseOtherDocument(document, accessor string) {
	if document == v.path {
		return
	}
	panic(fmt.Sprintf(
		"manifestDocumentView.%s asked about document %q while presenting %q: "+
			"a view presents one document, so present the document being asked about",
		accessor, document, v.path))
}

// refuseOtherDocument refuses an accessor call naming a document this value was
// not built for, because answering would hand back this document's fact under
// another document's name.
func (v aggregateDocumentView) refuseOtherDocument(document, accessor string) {
	if document == v.path {
		return
	}
	panic(fmt.Sprintf(
		"aggregateDocumentView.%s asked about document %q while presenting %q: "+
			"a view presents one document, so present the document being asked about",
		accessor, document, v.path))
}

// The compiler holds both types to the contract rather than a comment claiming
// it: a signature drifting from the declared facts stops compiling here.
//
// Both are asserted against the one generated interface, which is what makes the
// two presentations the same shape rather than two shapes that agree today.
//
// The per-kind assertions are the half the embedded base cannot supply.
// Embedding makes every document-stage fact compile, so the view assertion alone
// would accept a presentation answering none of them; naming each kind's own
// obligation is what makes a fact added to the registry fail on the
// presentations of that kind, and on no other.
var (
	_ constraint.DocumentView = aggregateDocumentView{}
	_ constraint.DocumentView = manifestDocumentView{}

	_ constraint.AggregateDocumentFacts = aggregateDocumentView{}
	_ constraint.ManifestDocumentFacts  = manifestDocumentView{}
)
