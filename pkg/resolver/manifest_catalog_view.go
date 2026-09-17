// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver

import (
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// manifestCatalogView presents one authored Manifest together with the catalog
// its annotation bindings resolve against, as the named facts complete at the
// catalog stage.
//
// It satisfies [constraint.CatalogView], and the compiler holds it to that
// contract at the bottom of this file rather than a comment claiming it.
//
// # What it holds, and what it deliberately does not
//
// One authored Manifest, and the catalog. Nothing normalized, no Aggregate, no
// Release, and no second document of any kind. A rule over an annotation
// binding's target needs exactly that pair: the binding's own declarations,
// which the document carries as it is handed over, and the contract of the
// Feature it routes to, which is only readable once the catalog can be reached.
// Neither half needs anything composed, so a rule reading them is invoked here
// rather than over a compilation it has no question about.
//
// # The document facts are embedded, not projected a second time
//
// Every fact about the Manifest itself is answered by the document presentation
// this value embeds, which is also what refuses the facts a Manifest does not
// carry. One fact has one producer, and two producers of a fact are free to
// disagree about it with nothing comparing them.
//
// # The bound Features' contracts resolve here, once
//
// The contract of each Feature a binding targets is resolved when this value is
// built, once per Feature, and a resolution that failed is kept with the failure
// that produced it. That is the same discipline the resolve applies to component
// contracts and it exists for the same reason: a rule reading a bare absence
// cannot tell a Feature declaring no contract from one whose contract could not
// be resolved, so the failure is reported where the resolving happened rather
// than inferred by every reader.
type manifestCatalogView struct {
	// manifestDocumentView answers every fact of the document stage: the
	// Manifest's own, and — through the base it carries — a refusal for every
	// fact another authored kind carries.
	manifestDocumentView

	// catalog is how a Feature a binding targets is reached.
	catalog CatalogLookup

	// targetContracts is the resolved contract of each Feature a binding targets,
	// produced once when this value was built.
	targetContracts map[string]resolvedContract

	// ancestries is the extends chain of each component type a rule has asked
	// about, kept with whatever the walk produced.
	//
	// It is filled on the ask rather than up front, unlike the contracts above,
	// and the difference is a property a mapping reading depends on: a rule asks
	// for an ancestry only after a mapping has already failed the direct
	// comparison, so a Manifest whose entries all match resolves no chain at all.
	// Resolving every mapped type when this value was built would make that walk
	// unconditional and the saving unavailable.
	ancestries map[string]resolvedAncestry

	// ancestriesAsked is the component types in ancestries, in the order they
	// were first asked about.
	//
	// The order is carried rather than recovered by sorting, because the ask
	// order is the order the mapping table declares its entries and that is the
	// order a reader meets the faults in. A map has no order of its own, so
	// leaving it to be sorted later would report the same faults in a different
	// sequence from the findings beside them.
	ancestriesAsked []string
}

// resolvedAncestry is one component type's extends chain as the catalog answered
// it: the bases, and whatever the walk failed with.
//
// The failure is kept rather than collapsed into the absence of bases, because
// the two are different answers. A type the catalog holds no entry for has no
// bases and no fault, and a chain that could not be walked has no bases either —
// a reader handed only the bases could not tell them apart, and one of them is a
// defect to report while the other is not.
type resolvedAncestry struct {
	bases []string
	err   error
}

// componentAncestryWalkFailure is one component type whose extends chain could
// not be walked, and why.
//
// It is the value a fault is composed from, not the fault itself. Keeping the two
// apart is what lets one resolution serve a diagnostic without the resolution
// deciding the verdict.
type componentAncestryWalkFailure struct {
	// ComponentType is the type whose chain did not walk.
	ComponentType string

	// Err is the walk's own fault, which is what a caller matching the chain's
	// condition keeps matching against.
	Err error
}

// newManifestCatalogView presents the Manifest a caller is holding beside the
// catalog it resolves against, resolving the contract of every Feature the
// Manifest's bindings target.
//
// The resolution of the bound Features' contracts is what this value produces,
// and it is the point of the type: a view otherwise projects facts already
// produced, and there is no earlier point at which this one exists.
func newManifestCatalogView(
	path string,
	manifest *types.Manifest,
	catalog CatalogLookup,
) *manifestCatalogView {
	v := &manifestCatalogView{
		manifestDocumentView: newManifestDocumentView(path, manifest),
		catalog:              catalog,
		targetContracts:      map[string]resolvedContract{},
		ancestries:           map[string]resolvedAncestry{},
	}
	for _, key := range v.ManifestAnnotationBindingKeys(path) {
		featureType, declared := v.ManifestAnnotationBindingFeature(path, key)
		if !declared {
			continue
		}
		if _, seen := v.targetContracts[featureType]; seen {
			continue
		}
		schema, hasContract, err := compileFeatureSchema(catalog, featureType)
		v.targetContracts[featureType] = resolvedContract{
			schema: schema, declared: hasContract, err: err,
		}
	}
	return v
}

// CatalogFeaturePresent reports whether the catalog this build resolves against
// holds a Feature entry for a type.
//
// It reaches the catalog through the one nil-tolerant Feature lookup this
// package declares, which is what the presentation of a compilation reads for
// the same fact — so the two answer from one lookup rather than from two that
// agree today.
func (v *manifestCatalogView) CatalogFeaturePresent(featureType string) bool {
	return lookupFeature(v.catalog, featureType) != nil
}

// CompiledFeatureSchema returns the contract of a Feature a binding targets,
// resolved, and whether there was a contract that resolved at all.
//
// It answers from the resolution this value performed when it was built, so every
// rule invoked over this view reads one result. A Feature no binding targets is
// not in that set and answers as absent — which is correct here: the facts this
// view states are about the bindings the Manifest declares, and a Feature outside
// that set is not one of its subjects.
func (v *manifestCatalogView) CompiledFeatureSchema(featureType string) (map[string]any, bool) {
	return v.targetContracts[featureType].resolved()
}

// MappedRecipeImplements returns what the Recipe or FeatureRecipe a mapping
// reference names declares it implements, and whether that reference resolves in
// the catalog at all.
//
// It reaches the catalog through the one nil-tolerant resolution this package
// declares for the question, which is what the presentation of a compilation
// reads for the same fact — so the two answer from one lookup rather than from
// two that agree today.
func (v *manifestCatalogView) MappedRecipeImplements(tier string, reference string) (string, bool) {
	return mappedRecipeImplements(v.catalog, tier, reference)
}

// MappedTypeExtendsAncestry returns the mapping keys of every base a component
// type descends from, nearest parent first, and whether the chain could be walked
// at all.
//
// It resolves on the first ask and answers from that resolution afterwards, so a
// type asked about twice is walked once and cannot be answered two different ways
// within one reading. A chain that did not walk is kept with its fault, which is
// what [manifestCatalogView.unwalkableAncestries] reports and what makes the
// second result meaningful: a reader handed only the bases could not tell a type
// with no ancestry from one whose chain is broken.
func (v *manifestCatalogView) MappedTypeExtendsAncestry(componentType string) ([]string, bool) {
	resolved, asked := v.ancestries[componentType]
	if !asked {
		bases, err := componentAncestryOf(v.catalog, componentType)
		resolved = resolvedAncestry{bases: bases, err: err}
		v.ancestries[componentType] = resolved
		v.ancestriesAsked = append(v.ancestriesAsked, componentType)
	}
	if resolved.err != nil {
		return nil, false
	}
	return resolved.bases, true
}

// unwalkableAncestries names each component type a rule asked about whose extends
// chain could not be walked, paired with the walk's own fault, in the order the
// types were asked about.
//
// It is how the failing outcome leaves this value, and it is a method rather than
// a declared fact for the reason its counterpart beside it is one: a rule reads
// the outcome and never produces it, so the fault is reported by the caller that
// holds the resolution, once, rather than raised again by every rule that met the
// absence.
func (v *manifestCatalogView) unwalkableAncestries() []componentAncestryWalkFailure {
	var out []componentAncestryWalkFailure
	for _, componentType := range v.ancestriesAsked {
		if err := v.ancestries[componentType].err; err != nil {
			out = append(out, componentAncestryWalkFailure{ComponentType: componentType, Err: err})
		}
	}
	return out
}

// unresolvableTargets names each Feature a binding targets whose declared
// contract could not be resolved, paired with the failure, in Feature-type order.
//
// It is how the third resolution outcome leaves this value, and it is a method
// rather than a declared fact for the reason its counterpart on the resolve's own
// presentation is one: a rule reads the outcome and never produces it, so the
// failure is reported by the caller that holds the resolution, once, rather than
// raised again by every rule that met the absence.
func (v *manifestCatalogView) unresolvableTargets() []FeatureContractResolutionFailure {
	var out []FeatureContractResolutionFailure
	for _, featureType := range sortedKeysOf(v.targetContracts) {
		c := v.targetContracts[featureType]
		if c.declared && c.err != nil {
			out = append(out, FeatureContractResolutionFailure{FeatureType: featureType, Err: c.err})
		}
	}
	return out
}

// FeatureContractResolutionFailure is one Feature contract that could not be
// resolved: the Feature type declaring it, and why it did not resolve.
//
// It is the value a finding is composed from, not the finding itself. Keeping the
// two apart is what lets one classification serve a diagnostic without the
// resolution deciding the verdict.
type FeatureContractResolutionFailure struct {
	// FeatureType is the Feature whose contract did not resolve.
	FeatureType string

	// Err is why the contract did not resolve.
	Err error
}

// The compiler holds this type to the contract rather than a comment claiming it:
// a signature drifting from the declared facts stops compiling here.
//
// The second assertion is the half embedding cannot supply. Embedding the
// document presentation makes every fact the chain declares before this stage
// compile, so the view assertion alone would accept a value answering none of
// this stage's own; naming the stage's obligation is what makes a fact added at
// this stage fail here. The Manifest's own obligation is asserted where that
// document presentation is declared, so it is not repeated.
var (
	_ constraint.CatalogView       = (*manifestCatalogView)(nil)
	_ constraint.CatalogStageFacts = (*manifestCatalogView)(nil)
)
