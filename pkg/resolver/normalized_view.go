// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver

import (
	"fmt"
	"sort"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
	"github.com/aggrega-ai/aggrega-cli/pkg/compactform"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"go.yaml.in/yaml/v3"
)

// normalizedView presents the two authoring documents, normalized and
// parameter-interpolated, with the catalog reachable, to a check as named facts.
//
// It satisfies [constraint.NormalizedView], and the compiler holds it to that
// contract at the bottom of this file rather than a comment claiming it.
//
// # The derived contracts are resolved once, here
//
// A component type's declared contract and a dependency socket's per-edge
// contracts are resolved when this value is built, once per type and once per
// socket pillar, and every reader downstream reads that result. Resolving per
// reader would multiply the work by the number of readers, hand each reader
// responsibility for supplying the documents a reference points at, and turn one
// failure reported once into several each reader reports separately — or, worse,
// swallows. That is the whole reason a resolved contract is a named fact rather
// than something each reader computes.
//
// # A contract that fails to resolve is a third answer, never a bare absence
//
// Resolution has three outcomes and this value keeps all three apart: the type
// declares no contract, the contract resolved, or the contract was declared and
// could not be resolved. The accessor's two results carry the first two; the
// third is held with the failure that produced it, so the one site that resolves
// is also the one site that can name what went wrong. Merging the third into the
// first is exactly the shape that makes a diagnostic stop firing quietly — a
// reader handed a bare "no contract" for a contract that failed to compile
// validates nothing and reports nothing, and no build changes to say so.
//
// It does not halt on that third outcome. Whether such a contract is reachable
// from an authored document is not established here, and refusing a build on an
// unmeasured input class would refuse documents that build today. The outcome is
// therefore distinguishable and available, and what a caller does with it is the
// caller's.
//
// # What it cannot answer
//
// Every fact of one authored document is refused here rather than answered,
// because this presents a compilation and not one of the documents it was
// compiled from. An empty result would be the truthful answer for a document
// that declares none, so nothing downstream could tell that answer apart from a
// presentation with nothing to project: the value would be plausible, permanent
// and wrong. Those refusals come from the embedded base rather than being
// written here.
//
// The parameter names the Aggregate's contract declares are the one such fact
// this could answer and does not. They survive normalization, so the value is
// still reachable from here — but the presentation of the authored document
// produces that fact already, reading the bytes as authored where this would
// read the parsed and interpolated document, and two producers of one fact are
// free to disagree about it with nothing comparing them. Refusing keeps the fact
// with the single presentation holding the document it is a fact of.
//
// It asserts the normalized stage's own obligation and no document kind's: it
// presents a compilation rather than one authored document, so claiming a kind
// would state something about this presentation that is not true of it.
//
// # Values are handed over uncopied
//
// The contract every view shares makes a fact's value immutable to a check, and
// this value keeps that contract rather than inventing a second policy: what an
// accessor returns aliases what the compilation already holds.
type normalizedView struct {
	// NormalizedViewBase supplies the document-stage facts this presentation does
	// not carry, and declares nothing at the normalized stage — so this type
	// writes bodies for exactly the facts that stage declares.
	constraint.NormalizedViewBase

	// aggregate is the normalized Aggregate document.
	aggregate types.NormalizedAggregate

	// release is the Release document, nil when the caller holds none.
	release *types.Release

	// catalog is how a declared type is reached.
	catalog CatalogLookup

	// parameters is the resolved parameter context every document in this
	// presentation shares.
	parameters map[string]any

	// instances is every component instance the Aggregate declares, by the
	// identity every keyed fact takes.
	instances map[string]types.ComponentInstance

	// releaseComponents is every component the Release wrote against, by the
	// same identity.
	releaseComponents map[string]types.BoundaryComponent

	// subsystemFeatures and boundaryFeatures are the attachments each authoring
	// document declares at a scope, by that scope's own path.
	subsystemFeatures map[string]types.OrderedMap[types.FeatureInstance]
	boundaryFeatures  map[string]types.OrderedMap[types.BoundaryFeature]

	// subsystemPaths and boundaryPaths are the same scopes in the order their
	// documents declare them, outer scope before the scopes nested inside it.
	//
	// They are recorded by the walks that fill the two maps above rather than
	// derived from those maps afterwards, and the reason is that they cannot be
	// derived from them at all: a Go map holds no order, so a reader ranging one
	// gets a different sequence per run. The order is a property of the authored
	// document, so it is captured where the document is read.
	//
	// Composing a scope's path is the same one rule either way — a synthesized
	// scope contributes no level, a nested one joins under its axis's separator
	// — so recording the path alongside the block keyed by it is what keeps a
	// second expression of that rule from existing to disagree with this one.
	subsystemPaths []string
	boundaryPaths  []string

	// annotations is every annotation entry either authoring document declares,
	// in the order the enumerating axis returns them: the Release's scopes
	// before the Aggregate's, each document's scopes in the order it declares
	// them, and within a scope its own entries before its components'.
	//
	// It is recorded by the same walks that fill the maps above rather than
	// derived from them afterwards, for the reason the scope orders are: a Go map
	// holds no order, and the order is a property of the authored document.
	annotations []constraint.AuthoredAnnotationInstance

	// annotationValues is the value each of those entries carries, keyed by the
	// entry's own coordinates.
	//
	// It is filled in the same walk that appends the instance, which is what
	// keeps the two answers about one entry from disagreeing: an entry present in
	// the enumeration and absent here would make a rule invoked for it read as an
	// entry nobody wrote.
	annotationValues map[authoredAnnotationCoordinates]any

	// componentContracts is the resolved contract of each component type in
	// play, produced once when this value was built.
	componentContracts map[string]resolvedContract

	// socketContracts is the resolved per-edge contract of each socket pillar in
	// play, produced once when this value was built and keyed by the type, the
	// socket and the pillar together.
	socketContracts map[socketPillarKey]resolvedContract
}

// authoredAnnotationCoordinates identifies one authored annotation entry as this
// presentation keys it: the document it was written in, the scope inside that
// document, the component it was written on, the annotation key and the subkey
// within the value.
//
// It is a struct key rather than a joined string because every part of it is a
// value an author typed, and an author's key may carry whatever separator a join
// would pick — at which point two distinct entries would collide on one composed
// key, silently and in favour of whichever was recorded last. An annotation key
// conventionally carries a period, so that is not a remote case here.
type authoredAnnotationCoordinates struct {
	document  string
	scope     string
	component string
	key       string
	subkey    string
}

// socketPillarKey identifies one socket's contract over one of the two pillars a
// connection into it may carry.
type socketPillarKey struct {
	componentType string
	socket        string
	pillar        string
}

// resolvedContract is what resolving one declared contract produced: the
// resolved document, whether a contract was declared at all, and the failure
// when one was declared and did not resolve.
//
// The three outcomes are held apart rather than collapsed into a present-or-not
// answer, because two of them require opposite treatment and the third is the
// one nothing else can report. A type declaring no contract has nothing to
// measure against and that is correct; a contract that failed to resolve has
// something to measure against that nobody can read, and reporting it as the
// first would retire a diagnostic without saying so.
type resolvedContract struct {
	// schema is the resolved document, nil unless declared is true and err is
	// nil. A contract that resolves to nothing is a non-nil empty document, so
	// it is distinguishable from a resolution that did not happen.
	schema compactform.Schema

	// declared reports whether a contract was authored at all.
	declared bool

	// err is why a declared contract did not resolve, nil when it did.
	err error
}

// resolved reports whether there is a contract to read, and returns it. The pair
// is what every fact over a resolved contract answers with, and the failure case
// answers the same way the absent case does — the difference between them is
// held on this value, where the resolution happened, rather than handed to every
// reader as something to classify again.
func (c resolvedContract) resolved() (compactform.Schema, bool) {
	if !c.declared || c.err != nil {
		return nil, false
	}
	return c.schema, true
}

// newNormalizedView presents the documents a guarded resolve is holding, with
// every derived contract resolved once.
//
// The documents and the catalog are taken rather than reached for, because a
// view projects facts already produced and never produces one of its own. What
// it does produce is the resolution of the contracts those documents declare,
// and that production is the point of the type: it happens here, once, so that
// no reader downstream resolves anything.
func newNormalizedView(
	na types.NormalizedAggregate,
	release *types.Release,
	catalog CatalogLookup,
	parameters map[string]any,
) *normalizedView {
	v := &normalizedView{
		aggregate:          na,
		release:            release,
		catalog:            catalog,
		parameters:         parameters,
		instances:          map[string]types.ComponentInstance{},
		releaseComponents:  map[string]types.BoundaryComponent{},
		subsystemFeatures:  map[string]types.OrderedMap[types.FeatureInstance]{},
		boundaryFeatures:   map[string]types.OrderedMap[types.BoundaryFeature]{},
		annotationValues:   map[authoredAnnotationCoordinates]any{},
		componentContracts: map[string]resolvedContract{},
		socketContracts:    map[socketPillarKey]resolvedContract{},
	}

	for _, p := range collectComponentInstances(na) {
		v.instances[p.name] = p.inst
	}
	// The Release is read before the Aggregate so its entries lead the
	// annotation axis, which is the order a rule over that axis raises in.
	if release != nil {
		v.collectBoundaryContent(types.NormalizeRelease(release))
	}
	if agg := na.Aggregate(); agg != nil {
		v.collectSubsystemContent("", agg.Spec.Subsystems)
	}

	v.resolveContracts()
	return v
}

// NormalizedView presents the two authoring documents, normalized and
// parameter-interpolated with the catalog reachable, as the named facts complete
// at that point in the compilation.
//
// It is the exported way to obtain that presentation, and it exists because a
// later stage's presentation contains this one: the facts complete here stay
// complete, so the value holding a later stage's product satisfies the earlier
// contract by delegating to this rather than by re-projecting the same documents.
// Re-projecting would give one fact two producers, and two producers of one fact
// are free to disagree about it with nothing comparing them.
//
// The contracts the documents declare are resolved once, when this value is
// built, exactly as they are for the guarded resolve that produced the documents.
func NewNormalizedView(
	na types.NormalizedAggregate,
	release *types.Release,
	catalog CatalogLookup,
	parameters map[string]any,
) constraint.NormalizedView {
	return newNormalizedView(na, release, catalog, parameters)
}

// collectSubsystemFeatures records each declared scope's attachment block under
// the scope path the enumerator names it by, descending into nested scopes, and
// appends that path to order in the order the document declares it.
//
// A synthesized scope is descended into and contributes no path of its own: it
// is not a scope the author declared, so an attachment named under it would be
// reported at a key no file contains. It contributes no entry to order either,
// for the same reason and in the same step — a scope absent from the map and
// present in the order would be enumerated as a coordinate no fact answers for.
//
// The order is appended here rather than recovered from the map afterwards
// because the map holds none: the sequence is the author's and this walk is
// where the authored document is read.
// It also records every annotation entry the scope and its components declare,
// in the same walk. Recording them here is what keeps one reading of the document
// tree: a second walk would agree with this one the day it was written and be
// free to disagree afterwards about which scopes are synthesized, with nothing
// comparing the two.
func (v *normalizedView) collectSubsystemContent(
	path string,
	subsystems types.OrderedMap[types.Subsystem],
) {
	for name, sub := range subsystems.All() {
		child := name
		if sub.Synthesized {
			child = path
		} else if path != "" {
			child = path + "." + name
		}
		if !sub.Synthesized {
			v.subsystemFeatures[child] = sub.Features
			v.subsystemPaths = append(v.subsystemPaths, child)
		}
		// A synthesized scope declares no annotations of its own: it is a container
		// normalization generated to hold components an author wrote at the
		// document's top level, so recording its own block would name a key no file
		// spells. Its components are recorded, at the top-level shape the author did
		// write, which is what the empty scope path names.
		if !sub.Synthesized {
			v.recordAnnotations(
				constraint.AuthoringDocumentAggregate, "",
				constraint.AuthoredSite{ScopePath: child}, nil, sub.Annotations)
		}
		for component, inst := range sub.Components.All() {
			v.recordAnnotations(
				constraint.AuthoringDocumentAggregate, component,
				constraint.AuthoredSite{ScopePath: child, Component: component},
				nil, inst.Annotations)
		}
		v.collectSubsystemContent(child, sub.Subsystems)
	}
}

// collectBoundaryContent records every component the Release wrote against
// and every scope-wide attachment it declared, descending the boundary tree, and
// appends each declared scope's path to order in the order the document declares
// it.
//
// A synthesized scope contributes neither a block nor an entry in the order, on
// the same reasoning its counterpart on the other authoring document does: the
// operator declared no such scope, so naming one would report at a key the file
// does not contain.
// It also records every annotation entry the scope and its components declare,
// in the same walk and for the reason its counterpart on the other authoring
// document does.
//
// The component entries are read through the declaration axis rather than the
// folded one, because the two answer differently for a routed entry and the
// difference is exactly what a locator needs: the fold keys every component by
// the id it carries once the composition is flattened, while a routed entry is
// written under the LOCAL name it carries inside the module it came from, so a
// locator built from the id would spell a key the document does not contain. The
// declaration carries both — the id every keyed fact takes, and the authoring
// keys reaching it — so an entry is recorded under one and positioned by the
// other.
func (v *normalizedView) collectBoundaryContent(nr types.NormalizedRelease) {
	rel := nr.Release()
	if rel == nil {
		return
	}
	var walk func(path string, b types.Boundary)
	walk = func(path string, b types.Boundary) {
		if !b.Synthesized {
			v.boundaryFeatures[path] = b.Features
			v.boundaryPaths = append(v.boundaryPaths, path)
			// A synthesized scope declares no annotations of its own, on the reading
			// its counterpart on the other authoring document states: it is a
			// container the compiler generated, so its own block is a key no file
			// spells.
			v.recordAnnotations(
				constraint.AuthoringDocumentRelease, "",
				constraint.AuthoredSite{ScopePath: path}, nil, b.Annotations)
		}
		declared := types.BoundaryComponents(b)
		for name, bc := range declared.All() {
			v.releaseComponents[name] = bc
		}
		for _, decl := range types.BoundaryComponentDeclarations(b) {
			site := constraint.AuthoredSite{ScopePath: path}
			var routed []string
			// The scope's own component axis is what the boundary spells under
			// `components`, so membership there is the exact discriminator — not the
			// declaration's key path, which would be read for its shape. An id on
			// both axes is a document placing one node twice, which the Release
			// refuses when it loads, so it does not reach here.
			if _, direct := b.Components.Get(decl.ID); direct {
				site.Component = decl.ID
			} else {
				routed = decl.Locator
			}
			v.recordAnnotations(
				constraint.AuthoringDocumentRelease, decl.ID,
				site, routed, decl.Component.Annotations)
		}
		for name, child := range b.Boundaries.All() {
			next := name
			if path != "" {
				next = path + "/" + name
			}
			walk(next, child)
		}
	}
	for name, b := range rel.Spec.Boundaries.All() {
		start := name
		if b.Synthesized {
			start = ""
		}
		walk(start, b)
	}
}

// recordAnnotations appends one instance per routed value an annotation block
// declares, and records the value each instance carries.
//
// A structured value contributes one instance per subkey, because each subkey is
// a separate authored contribution routed to its own destination and judged on
// its own; a value written bare contributes one instance with no subkey. Both
// are recorded under the coordinates the enumerated instance carries, in one
// step, so the two answers about one entry cannot disagree.
//
// An entry carrying no value at all routes nothing and is deliberately not
// recorded. That is how an author removes an entry a scope above declared, so a
// rule invoked for one would be judging a removal.
//
// A subkey whose own value is absent is left out on the same reading: it is the
// per-subkey form of the same opt-out, and the compiler routes nothing for it.
func (v *normalizedView) recordAnnotations(
	document string,
	component string,
	site constraint.AuthoredSite,
	routedKeys []string,
	annotations types.OrderedMap[*types.AnnotationValue],
) {
	for key, value := range annotations.All() {
		switch {
		case value.IsStructured():
			for subkey, subvalue := range value.Structured.All() {
				if subvalue == nil {
					continue
				}
				v.recordAnnotation(document, component, site, routedKeys, key, subkey, subvalue)
			}
		case value.IsScalar():
			v.recordAnnotation(document, component, site, routedKeys, key, "", *value.Scalar)
		}
	}
}

// recordAnnotation appends one enumerated entry and the value it carries.
func (v *normalizedView) recordAnnotation(
	document string,
	component string,
	site constraint.AuthoredSite,
	routedKeys []string,
	key string,
	subkey string,
	value any,
) {
	v.annotations = append(v.annotations, constraint.AuthoredAnnotationInstance{
		Document:   document,
		Component:  component,
		Site:       site,
		RoutedKeys: routedKeys,
		Key:        key,
		Subkey:     subkey,
	})
	v.annotationValues[authoredAnnotationCoordinates{
		document:  document,
		scope:     site.ScopePath,
		component: component,
		key:       key,
		subkey:    subkey,
	}] = value
}

// AuthoredAnnotationSubvalue returns the value an author wrote at one annotation
// entry, and whether this presentation holds a routed value there at all.
func (v *normalizedView) AuthoredAnnotationSubvalue(
	document string,
	scope string,
	component string,
	key string,
	subkey string,
) (any, bool) {
	value, ok := v.annotationValues[authoredAnnotationCoordinates{
		document:  document,
		scope:     scope,
		component: component,
		key:       key,
		subkey:    subkey,
	}]
	return value, ok
}

// resolveContracts resolves every declared contract this presentation can be
// asked about, once each.
//
// It walks the component types the Aggregate names rather than the whole
// catalog: a type nothing declares has no fact keyed by it, and resolving it
// would pay for a contract nobody reads. Each socket the resolved type declares
// contributes its two per-edge contracts, so a reader asking about a socket that
// was never wired still gets the same answer as one asking about a wired socket.
func (v *normalizedView) resolveContracts() {
	for _, inst := range v.instances {
		v.resolveTypeContracts(inst.Type)
	}
}

// resolveTypeContracts resolves one component type's own contract and the
// per-edge contracts of every socket it declares, skipping a type already
// resolved.
func (v *normalizedView) resolveTypeContracts(componentType string) {
	if _, seen := v.componentContracts[componentType]; seen {
		return
	}
	schema, declared, err := compileComponentSchema(v.catalog, componentType)
	v.componentContracts[componentType] = resolvedContract{schema: schema, declared: declared, err: err}

	catComp, found := lookupComponent(v.catalog, componentType)
	if !found {
		return
	}
	for socket := range catComp.Spec.UsesSchema.All() {
		for _, pillar := range []string{"values", "infra"} {
			s, d, e := compileSocketPillarSchema(v.catalog, componentType, socket, pillar)
			v.socketContracts[socketPillarKey{componentType: componentType, socket: socket, pillar: pillar}] =
				resolvedContract{schema: s, declared: d, err: e}
		}
	}
}

// UnresolvableContracts names each component type whose declared contract could
// not be resolved, paired with the failure, in component-type order.
//
// It is how the third resolution outcome leaves this value. The obligation the
// resolved-contract fact places on whatever produces it is that a contract which
// fails to resolve is reported where the resolving happens, once — so the
// producer holds the failures rather than handing every reader a bare absence to
// misread, and a caller with somewhere to report them reads them from here.
//
// [RunGuards] is that caller: it reads this before asking any rule, and renders
// each entry as a halting finding. So this is the one reading of the
// classification, and a reader wanting the same distinction asks here rather than
// compiling a contract again to recover it.
func (v *normalizedView) UnresolvableContracts() []ContractResolutionFailure {
	var out []ContractResolutionFailure
	for _, componentType := range sortedKeysOf(v.componentContracts) {
		c := v.componentContracts[componentType]
		if c.declared && c.err != nil {
			out = append(out, ContractResolutionFailure{ComponentType: componentType, Err: c.err})
		}
	}
	for _, key := range sortedSocketPillarKeys(v.socketContracts) {
		c := v.socketContracts[key]
		if c.declared && c.err != nil {
			out = append(out, ContractResolutionFailure{
				ComponentType: key.componentType,
				Socket:        key.socket,
				Pillar:        key.pillar,
				Err:           c.err,
			})
		}
	}
	return out
}

// ContractResolutionFailure is one declared contract that could not be resolved:
// the component type declaring it, the socket and pillar when the contract is a
// socket's per-edge one, and why it did not resolve.
//
// It is the value a halt is composed from, not the halt itself, and keeping the
// two apart is what lets one classification serve a diagnostic without the
// resolution deciding the verdict. The resolution's job is to keep the failure
// distinguishable from an absent contract; deciding what a build does about it
// belongs to the surface that reports findings, which reads this.
//
// A declared contract that fails to compile leaves its subject with something to
// measure against that nobody can read, so a build carrying one halts. Passing it
// over would retire a check with nothing to show that it had gone, which is
// exactly how a diagnostic comes to stop firing in silence.
type ContractResolutionFailure struct {
	// ComponentType is the type whose contract did not resolve.
	ComponentType string

	// Socket is the dependency socket declaring the contract, empty when the
	// contract is the component type's own.
	Socket string

	// Pillar is the configuration pillar the socket's contract covers, empty
	// when the contract is the component type's own.
	Pillar string

	// Err is why the contract did not resolve.
	Err error
}

// ---------------------------------------------------------------- instance facts

// InstanceComponentType returns the component type a named instance declares,
// and whether an instance of that name exists at all.
func (v *normalizedView) InstanceComponentType(node string) (string, bool) {
	inst, ok := v.instances[node]
	if !ok {
		return "", false
	}
	return inst.Type, true
}

// AuthoredValuesPillar returns one configuration pillar as the author wrote it.
//
// A component instance authors exactly one pillar. The operator's sizing for the
// same component is written on the other document and is a separate fact, so
// asking this one for it is a mistake rather than a lookup that misses.
func (v *normalizedView) AuthoredValuesPillar(node string, pillar string) types.OrderedMap[any] {
	if pillar != authoredValuesPillarName {
		panic(fmt.Sprintf(
			"a component instance authors the %q pillar only; %q is authored on the Release document "+
				"and is a separate fact", authoredValuesPillarName, pillar))
	}
	return v.instances[node].Values
}

// authoredValuesPillarName is the one pillar a component instance authors. It is
// named once here so the accessor's refusal and its answer cannot disagree about
// which pillar that is.
const authoredValuesPillarName = "values"

// AuthoredAttachmentBlock returns the attachments an instance's own block
// declares, in author order.
func (v *normalizedView) AuthoredAttachmentBlock(node string) types.OrderedMap[types.FeatureInstance] {
	return v.instances[node].Features
}

// AuthoredAttachmentNames returns the names an instance's own attachment block
// declares, in author order.
func (v *normalizedView) AuthoredAttachmentNames(node string) []string {
	features := v.instances[node].Features
	return features.Keys()
}

// instanceUses returns one socket's authored value from an instance's own
// dependency block.
func (v *normalizedView) instanceUses(node, socket string) (types.UsesSocketValue, bool) {
	inst := v.instances[node]
	return inst.Uses.Get(socket)
}

// releaseUses returns one socket's authored entries from the Release's edge
// surface for a component.
func (v *normalizedView) releaseUses(node, socket string) (types.OrderedMap[types.ReleaseUsesEntry], bool) {
	bc := v.releaseComponents[node]
	return bc.Uses.Get(socket)
}

// AuthoredAttachmentType returns the Feature type one attachment of an
// instance's own block names, and whether an attachment of that name exists.
func (v *normalizedView) AuthoredAttachmentType(node string, attachment string) (string, bool) {
	features := v.instances[node].Features
	fi, ok := features.Get(attachment)
	if !ok {
		return "", false
	}
	return fi.Type, true
}

// AttachmentAuthoredUsesSocketNames returns the socket names one attachment's
// own dependency block wires, in author order.
func (v *normalizedView) AttachmentAuthoredUsesSocketNames(node string, attachment string) []string {
	features := v.instances[node].Features
	fi, ok := features.Get(attachment)
	if !ok {
		return nil
	}
	return fi.Uses.Keys()
}

// SubsystemAttachmentDeclaration returns what one scope-wide attachment of the
// Aggregate declares, and whether an attachment of that name exists there.
func (v *normalizedView) SubsystemAttachmentDeclaration(
	subsystem string, attachment string,
) (constraint.AttachmentDeclaration, bool) {
	features, ok := v.subsystemFeatures[subsystem]
	if !ok {
		return constraint.AttachmentDeclaration{}, false
	}
	fi, ok := features.Get(attachment)
	if !ok {
		return constraint.AttachmentDeclaration{}, false
	}
	return constraint.AttachmentDeclaration{
		FeatureType:         fi.Type,
		AuthoredSocketNames: fi.Uses.Keys(),
	}, true
}

// ---------------------------------------------------------------- injection facts

// AuthoredInjectBlockPresent reports whether an instance wrote an injection
// block at all, whatever the block carries.
func (v *normalizedView) AuthoredInjectBlockPresent(node string) bool {
	return v.instances[node].Inject != nil
}

// AuthoredInjectMechanismNames returns the delivery mechanisms an instance's
// injection block declares, in author order.
func (v *normalizedView) AuthoredInjectMechanismNames(node string) []string {
	return InjectMechanismNames(v.instances[node].Inject)
}

// AuthoredInjectDeliveryKeys returns the keys an instance delivers through one
// mechanism, in author order where the authored shape carries one and
// alphabetically where it does not.
func (v *normalizedView) AuthoredInjectDeliveryKeys(node string, mechanism string) []string {
	return injectDeliveryKeys(v.instances[node].Inject, mechanism)
}

// AuthoredInjectProvidesReference returns the published key one delivery entry
// of an instance's injection block reads from, and whether that entry reads a
// published key at all.
//
// It reads the entry through the same decoder a connection's delivery payload is
// read with, in the mode that treats a bare string value as a literal — which is
// what such a value is on this surface, where the same shape on a connection is a
// reference.
func (v *normalizedView) AuthoredInjectProvidesReference(
	node string, mechanism string, deliveryKey string,
) (string, bool) {
	inject := v.instances[node].Inject
	if inject == nil {
		return "", false
	}
	for _, entry := range decodeFromEntries(injectMechanismPayload(inject, mechanism), true) {
		if entry.deliveryKey == deliveryKey {
			return entry.providesKey, true
		}
	}
	return "", false
}

// ---------------------------------------------------------------- connection facts

// AuthoredUsesSocketConnections returns the connections an instance's own
// dependency block carries, one entry per socket the instance wrote, in author
// order, each holding that socket's connection count.
func (v *normalizedView) AuthoredUsesSocketConnections(node string) types.OrderedMap[int] {
	out := types.OrderedMap[int]{}
	inst := v.instances[node]
	for socket, sv := range inst.Uses.All() {
		out.Set(socket, len(EdgeValuesOf(sv)))
	}
	return out
}

// AuthoredUsesSocketEntryAliases returns the aliases one socket's own dependency
// block wrote an entry under, in author order.
func (v *normalizedView) AuthoredUsesSocketEntryAliases(node string, socket string) []string {
	sv, ok := v.instanceUses(node, socket)
	if !ok {
		return nil
	}
	connections := EdgeValuesOf(sv)
	out := make([]string, 0, len(connections))
	for _, ev := range connections {
		out = append(out, ev.Alias)
	}
	return out
}

// CompactSocketTargets returns the targets one socket's short authoring form
// lists, in author order.
func (v *normalizedView) CompactSocketTargets(node string, socket string) []string {
	sv, ok := v.instanceUses(node, socket)
	if !ok {
		return nil
	}
	return sv.Compact
}

// PerEdgeTarget returns the component one connection of an instance's own
// dependency block names, empty when the entry named none.
func (v *normalizedView) PerEdgeTarget(node string, socket string, alias string) string {
	entry, ok := v.verboseEntry(node, socket, alias)
	if !ok {
		return ""
	}
	return entry.Target
}

// PerEdgeValues returns the developer's configuration for one connection, as the
// author wrote it, empty when the author wrote none.
func (v *normalizedView) PerEdgeValues(node string, socket string, alias string) types.OrderedMap[any] {
	entry, ok := v.verboseEntry(node, socket, alias)
	if !ok {
		return types.OrderedMap[any]{}
	}
	return entry.Values
}

// PerEdgeAttachmentNames returns the names one connection of an instance's own
// dependency block carries an attachment under, in author order.
func (v *normalizedView) PerEdgeAttachmentNames(node string, socket string, alias string) []string {
	entry, ok := v.verboseEntry(node, socket, alias)
	if !ok {
		return nil
	}
	return entry.Features.Keys()
}

// AuthoredPerEdgeInject returns the delivery payload one connection of an
// instance's own dependency block dictates, by mechanism, in author order.
func (v *normalizedView) AuthoredPerEdgeInject(
	node string, socket string, alias string,
) types.OrderedMap[any] {
	entry, ok := v.verboseEntry(node, socket, alias)
	if !ok || entry.Inject == nil {
		return types.OrderedMap[any]{}
	}
	return InjectMechanismPayloads(entry.Inject)
}

// verboseEntry returns one connection's long-form entry. The short form carries
// no entry to return: it lists targets and gives each connection the target's
// own name as its alias, with nowhere to write a payload.
func (v *normalizedView) verboseEntry(node, socket, alias string) (types.VerboseUsesEntry, bool) {
	sv, ok := v.instanceUses(node, socket)
	if !ok {
		return types.VerboseUsesEntry{}, false
	}
	return sv.Verbose.Get(alias)
}

// ---------------------------------------------------------------- Release facts

// ReleaseComponentEntryPresent reports whether the Release wrote an entry
// against one component, on either of the axes a boundary names components on.
func (v *normalizedView) ReleaseComponentEntryPresent(node string) bool {
	_, present := v.releaseComponents[node]
	return present
}

// ReleaseComponentInfra returns the physical sizing the Release wrote for one
// component, in author order, empty when it wrote none.
func (v *normalizedView) ReleaseComponentInfra(node string) types.OrderedMap[any] {
	return v.releaseComponents[node].Infra
}

// releaseAttachment returns one attachment the Release wrote against a
// component.
func (v *normalizedView) releaseAttachment(node, attachment string) (types.BoundaryFeature, bool) {
	bc := v.releaseComponents[node]
	return bc.Features.Get(attachment)
}

// releaseAttachmentNames returns the names the Release's own attachment block
// declares against a component, in author order.
//
// It is unexported because the axis is what needs it, and the axis holds this
// presentation directly. The rules read an attachment's content through the
// facts keyed by (node, attachment) beside it; nothing declared reads this set,
// so publishing it as a fact would add a surface no rule consults.
func (v *normalizedView) releaseAttachmentNames(node string) []string {
	bc := v.releaseComponents[node]
	return bc.Features.Keys()
}

// ReleaseAuthoredUsesSocketConnections returns the connections the Release's
// edge surface carries for one component, one entry per socket it wrote, in
// author order, each holding that socket's connection count.
//
// An entry counts only when it names a target. On this surface the alias
// identifies which existing connection the entry configures rather than
// supplying a target for a new one, so an entry naming no target draws nothing.
func (v *normalizedView) ReleaseAuthoredUsesSocketConnections(node string) types.OrderedMap[int] {
	out := types.OrderedMap[int]{}
	bc := v.releaseComponents[node]
	for socket, aliases := range bc.Uses.All() {
		count := 0
		for _, entry := range aliases.All() {
			if entry.Target != "" {
				count++
			}
		}
		out.Set(socket, count)
	}
	return out
}

// ReleaseAuthoredUsesSocketEntryAliases returns the aliases the Release's
// edge surface wrote an entry under for one socket, in author order.
func (v *normalizedView) ReleaseAuthoredUsesSocketEntryAliases(node string, socket string) []string {
	aliases, ok := v.releaseUses(node, socket)
	if !ok {
		return nil
	}
	return aliases.Keys()
}

// ReleasePerEdgeTarget returns the component one connection of the Release's
// edge surface names, empty when the entry named none.
func (v *normalizedView) ReleasePerEdgeTarget(node string, socket string, alias string) string {
	entry, ok := v.releaseEdgeEntry(node, socket, alias)
	if !ok {
		return ""
	}
	return entry.Target
}

// ReleasePerEdgeInfra returns the operator's physical sizing for one connection,
// in author order, empty when the operator wrote none.
func (v *normalizedView) ReleasePerEdgeInfra(
	node string, socket string, alias string,
) types.OrderedMap[any] {
	entry, ok := v.releaseEdgeEntry(node, socket, alias)
	if !ok {
		return types.OrderedMap[any]{}
	}
	return entry.Infra
}

// ReleasePerEdgeAttachmentNames returns the names the Release's edge surface
// carries an attachment under on one connection, in author order.
func (v *normalizedView) ReleasePerEdgeAttachmentNames(node string, socket string, alias string) []string {
	entry, ok := v.releaseEdgeEntry(node, socket, alias)
	if !ok {
		return nil
	}
	return entry.Features.Keys()
}

// ReleasePerEdgeAttachmentType returns the Feature type one attachment the
// Release's edge surface carries on one connection names, and whether the entry
// carries an attachment under that name at all.
func (v *normalizedView) ReleasePerEdgeAttachmentType(
	node string, socket string, alias string, attachment string,
) (string, bool) {
	entry, ok := v.releaseEdgeEntry(node, socket, alias)
	if !ok {
		return "", false
	}
	att, ok := entry.Features.Get(attachment)
	if !ok {
		return "", false
	}
	return att.Type, true
}

// releaseEdgeEntry returns one connection's entry on the Release's edge
// surface.
func (v *normalizedView) releaseEdgeEntry(node, socket, alias string) (types.ReleaseUsesEntry, bool) {
	aliases, ok := v.releaseUses(node, socket)
	if !ok {
		return types.ReleaseUsesEntry{}, false
	}
	return aliases.Get(alias)
}

// BoundaryAttachmentDeclaration returns what one scope-wide attachment of the
// Release declares, and whether an attachment of that name exists there.
func (v *normalizedView) BoundaryAttachmentDeclaration(
	boundary string, attachment string,
) (constraint.AttachmentDeclaration, bool) {
	features, ok := v.boundaryFeatures[boundary]
	if !ok {
		return constraint.AttachmentDeclaration{}, false
	}
	bf, ok := features.Get(attachment)
	if !ok {
		return constraint.AttachmentDeclaration{}, false
	}
	return constraint.AttachmentDeclaration{
		FeatureType:         bf.Type,
		AuthoredSocketNames: bf.Uses.Keys(),
	}, true
}

// ReleaseAttachmentType returns the Feature type one attachment the Release
// wrote against a component names, and whether an attachment of that name
// exists.
func (v *normalizedView) ReleaseAttachmentType(node string, attachment string) (string, bool) {
	bf, ok := v.releaseAttachment(node, attachment)
	if !ok {
		return "", false
	}
	return bf.Type, true
}

// ReleaseAttachmentAuthoredUsesSocketNames returns the socket names one such
// attachment's own dependency block wires, in author order.
func (v *normalizedView) ReleaseAttachmentAuthoredUsesSocketNames(
	node string, attachment string,
) []string {
	bf, ok := v.releaseAttachment(node, attachment)
	if !ok {
		return nil
	}
	return bf.Uses.Keys()
}

// ---------------------------------------------------------------- catalog facts

// CatalogComponentPresent reports whether the catalog this build consults holds
// an entry for a component type.
func (v *normalizedView) CatalogComponentPresent(componentType string) bool {
	_, found := lookupComponent(v.catalog, componentType)
	return found
}

// CatalogFeaturePresent reports whether the catalog this build consults holds an
// entry for a Feature type.
func (v *normalizedView) CatalogFeaturePresent(featureType string) bool {
	return lookupFeature(v.catalog, featureType) != nil
}

// MappedRecipeImplements returns what the Recipe or FeatureRecipe a mapping
// reference names declares it implements, and whether that reference resolves in
// the catalog at all.
//
// It is answered here although this presents a compilation and no Manifest,
// because the fact is a catalog read keyed by a reference: the stage this value
// contains declares it, so every fact of that stage is answerable here or a rule
// reading one would refuse at read time rather than fail to compile.
func (v *normalizedView) MappedRecipeImplements(tier string, reference string) (string, bool) {
	return mappedRecipeImplements(v.catalog, tier, reference)
}

// MappedTypeExtendsAncestry returns the mapping keys of every base a component
// type descends from, nearest parent first, and whether the chain could be walked.
//
// It resolves per call and keeps nothing, which is the difference between this and
// the presentation of one Manifest beside the catalog: a rule that needs the
// walk's own fault told apart from an empty ancestry has to reach the value that
// kept it, and this one reports the outcome without holding it.
func (v *normalizedView) MappedTypeExtendsAncestry(componentType string) ([]string, bool) {
	bases, err := componentAncestryOf(v.catalog, componentType)
	if err != nil {
		return nil, false
	}
	return bases, true
}

// ComponentTypeIsLocalPath reports whether a component type is addressed as a
// path beside the authoring document rather than as a catalog identity.
func (v *normalizedView) ComponentTypeIsLocalPath(componentType string) bool {
	return IsRelativePathRef(componentType)
}

// DeclaredSocketNames returns the dependency sockets a component type declares,
// in author order.
func (v *normalizedView) DeclaredSocketNames(componentType string) []string {
	catComp, found := lookupComponent(v.catalog, componentType)
	if !found {
		return nil
	}
	return catComp.Spec.UsesSchema.Keys()
}

// DeclaredSlotNames returns the attachment slots a component type declares, in
// author order.
func (v *normalizedView) DeclaredSlotNames(componentType string) []string {
	catComp, found := lookupComponent(v.catalog, componentType)
	if !found {
		return nil
	}
	return catComp.Spec.FeaturesSchema.Keys()
}

// DeclaredInjectMechanismNames returns the delivery mechanisms a component type
// permits, in author order.
func (v *normalizedView) DeclaredInjectMechanismNames(componentType string) []string {
	catComp, found := lookupComponent(v.catalog, componentType)
	if !found {
		return nil
	}
	return catComp.Spec.Inject
}

// DeclaredProvidesKeys returns the keys a component type publishes, sorted.
//
// It reads the projection of the type's own resolved contract, so a rule over a
// reference into it and a rule over the contract itself read one resolution.
func (v *normalizedView) DeclaredProvidesKeys(componentType string) []string {
	catComp, found := lookupComponent(v.catalog, componentType)
	if !found {
		return nil
	}
	return sortedKeysOf(ComponentProvidesKeys(catComp, v.catalog.SchemaLibraries))
}

// SocketRequired reports whether a dependency socket declared on a component
// type must be populated.
func (v *normalizedView) SocketRequired(componentType string, socket string) bool {
	entry, ok := v.socketEntry(componentType, socket)
	return ok && entry.Required
}

// SocketSelector returns the label constraint a dependency socket declares over
// what may satisfy it, empty when it declares none.
func (v *normalizedView) SocketSelector(componentType string, socket string) string {
	entry, _ := v.socketEntry(componentType, socket)
	return entry.Selector
}

// SocketInjectMechanismNames returns the delivery mechanisms a dependency socket
// permits, in author order.
func (v *normalizedView) SocketInjectMechanismNames(componentType string, socket string) []string {
	entry, ok := v.socketEntry(componentType, socket)
	if !ok {
		return nil
	}
	return entry.Inject
}

// SocketDeclaredSlotNames returns the attachment slots a dependency socket
// declared on a component type lets each connection on it fill, in author order.
//
// It and the two facts below read the socket's slots through
// [SocketAttachmentSlots], the one home the binding a connection attachment
// records is derived through as well, so a rule over a socket's slots and the
// binding it judges read one declaration.
func (v *normalizedView) SocketDeclaredSlotNames(componentType string, socket string) []string {
	slots, _ := SocketAttachmentSlots(v.catalog, componentType, socket)
	return slots.Keys()
}

// SocketSlotSelector returns the label constraint one slot a dependency socket
// declares sets over what may fill it, empty when it declares none.
func (v *normalizedView) SocketSlotSelector(componentType string, socket string, slot string) string {
	slots, _ := SocketAttachmentSlots(v.catalog, componentType, socket)
	entry, _ := slots.Get(slot)
	return entry.Selector
}

// SocketSlotRequired reports whether one slot a dependency socket declares must
// be filled on every connection wired through the socket.
func (v *normalizedView) SocketSlotRequired(componentType string, socket string, slot string) bool {
	slots, _ := SocketAttachmentSlots(v.catalog, componentType, socket)
	entry, declared := slots.Get(slot)
	return declared && entry.Required
}

// SocketValueSchemaText returns the contract a dependency socket declares over
// the developer's configuration for one connection, as authored, empty when it
// declares none.
func (v *normalizedView) SocketValueSchemaText(componentType string, socket string) []byte {
	entry, ok := v.socketEntry(componentType, socket)
	if !ok {
		return nil
	}
	return authoredNodeText(&entry.ValueSchema)
}

// SocketInfraSchemaText returns the contract a dependency socket declares over
// the operator's configuration for one connection, as authored, empty when it
// declares none.
func (v *normalizedView) SocketInfraSchemaText(componentType string, socket string) []byte {
	entry, ok := v.socketEntry(componentType, socket)
	if !ok {
		return nil
	}
	return authoredNodeText(&entry.InfraSchema)
}

// socketEntry returns one dependency socket's declaration on a component type.
func (v *normalizedView) socketEntry(componentType, socket string) (types.UsesSchemaEntry, bool) {
	catComp, found := lookupComponent(v.catalog, componentType)
	if !found {
		return types.UsesSchemaEntry{}, false
	}
	return catComp.Spec.UsesSchema.Get(socket)
}

// SlotRequired reports whether an attachment slot a component type declares must
// end up bound.
func (v *normalizedView) SlotRequired(componentType string, slot string) bool {
	entry, ok := v.slotEntry(componentType, slot)
	return ok && entry.Required
}

// SlotSelector returns the label constraint an attachment slot declares over
// what may fill it, empty when it declares none.
func (v *normalizedView) SlotSelector(componentType string, slot string) string {
	entry, _ := v.slotEntry(componentType, slot)
	return entry.Selector
}

// SlotInjectMechanismNames returns the delivery mechanisms an attachment slot
// permits, in author order.
func (v *normalizedView) SlotInjectMechanismNames(componentType string, slot string) []string {
	entry, ok := v.slotEntry(componentType, slot)
	if !ok {
		return nil
	}
	return entry.Inject
}

// slotEntry returns one attachment slot's declaration on a component type.
func (v *normalizedView) slotEntry(componentType, slot string) (types.AttachmentSlot, bool) {
	catComp, found := lookupComponent(v.catalog, componentType)
	if !found {
		return types.AttachmentSlot{}, false
	}
	return catComp.Spec.FeaturesSchema.Get(slot)
}

// AuthoredComponentSchemaText returns the contract document a component type
// declares over its configuration, as authored, empty when it declares none.
func (v *normalizedView) AuthoredComponentSchemaText(componentType string) []byte {
	catComp, found := lookupComponent(v.catalog, componentType)
	if !found {
		return nil
	}
	return authoredNodeText(&catComp.Spec.Schema)
}

// RequiredAttachmentMandates returns the presence mandates a component type
// declares through its annotations, in author order.
func (v *normalizedView) RequiredAttachmentMandates(
	componentType string,
) []types.RequiredAttachmentType {
	catComp, found := lookupComponent(v.catalog, componentType)
	if !found {
		return nil
	}
	return types.RequiredAttachmentTypes(catComp.Metadata.Annotations)
}

// AttachmentDeclaredSocketNames returns the dependency sockets a Feature type
// declares, in author order.
func (v *normalizedView) AttachmentDeclaredSocketNames(featureType string) []string {
	feat := lookupFeature(v.catalog, featureType)
	if feat == nil {
		return nil
	}
	return feat.Spec.UsesSchema.Keys()
}

// AttachmentSocketSelector returns the label constraint a Feature type's
// dependency socket declares over what may satisfy it, empty when it declares
// none.
func (v *normalizedView) AttachmentSocketSelector(featureType string, socket string) string {
	feat := lookupFeature(v.catalog, featureType)
	if feat == nil {
		return ""
	}
	entry, _ := feat.Spec.UsesSchema.Get(socket)
	return entry.Selector
}

// AttachmentSocketInjectMechanismNames returns the delivery mechanisms a Feature
// type's dependency socket permits a connection wired into it to dictate, in
// author order.
//
// A socket the Feature declares no entry for reads the same as one declaring no
// mechanism, which is the reading the fact's own declaration states: both leave
// nothing permitted, and which of the two a socket is belongs to the fact that
// answers which sockets the Feature declares.
func (v *normalizedView) AttachmentSocketInjectMechanismNames(
	featureType string,
	socket string,
) []string {
	feat := lookupFeature(v.catalog, featureType)
	if feat == nil {
		return nil
	}
	entry, _ := feat.Spec.UsesSchema.Get(socket)
	return entry.Inject
}

// AttachmentFeatureLabels returns the labels a Feature type declares, in author
// order.
func (v *normalizedView) AttachmentFeatureLabels(featureType string) types.OrderedMap[string] {
	feat := lookupFeature(v.catalog, featureType)
	if feat == nil {
		return types.OrderedMap[string]{}
	}
	return feat.Metadata.Labels
}

// FeatureAppliesTo returns the label constraint a Feature type declares over
// what it may attach to, empty when it declares none in that form.
func (v *normalizedView) FeatureAppliesTo(featureType string) string {
	feat := lookupFeature(v.catalog, featureType)
	if feat == nil {
		return ""
	}
	return feat.Spec.AppliesTo
}

// FeatureAppliesToAnyOf returns the alternative label constraints a Feature type
// declares over what it may attach to, in author order.
func (v *normalizedView) FeatureAppliesToAnyOf(featureType string) []types.SelectorTerm {
	feat := lookupFeature(v.catalog, featureType)
	if feat == nil {
		return nil
	}
	return feat.Spec.AppliesToAnyOf
}

// FeatureConflictsWith returns the Features a Feature type declares it cannot
// reach a component alongside, as its author wrote them and in author order.
//
// A Feature the catalog does not hold, one declaring no interoperability block
// and one whose block lists nothing all answer empty: none of them declares a
// conflict, and a rule reading this has nothing to compare in any of the three.
func (v *normalizedView) FeatureConflictsWith(featureType string) []string {
	feat := lookupFeature(v.catalog, featureType)
	if feat == nil || feat.Spec.Interoperability == nil {
		return nil
	}
	return feat.Spec.Interoperability.ConflictsWith
}

// ---------------------------------------------------------------- resolved facts

// CompiledComponentSchema returns the contract a component type declares over
// its configuration, resolved, and whether there was a contract that resolved at
// all.
//
// The resolution happened once, when this presentation was built. A type whose
// contract failed to resolve answers the same way a type declaring none does,
// and the difference between them is held where the resolving happened.
func (v *normalizedView) CompiledComponentSchema(componentType string) (map[string]any, bool) {
	return v.componentContracts[componentType].resolved()
}

// CompiledFeatureSchema returns the contract a Feature type declares over the
// pillars an attachment of it may carry, resolved, and whether there was a
// contract that resolved at all.
//
// It resolves when asked rather than when this value is built, which is the
// opposite of what the component contracts beside it do, and the reason is that
// the set is not knowable here. A component type's contracts are resolved up
// front because the instances name every type in play; the Feature types a rule
// over this fact asks about are named by an annotation binding registry, which is
// a document this presentation does not hold. Resolving every Feature the catalog
// carries instead would pay for contracts nothing reads.
//
// A Feature the catalog does not hold, one declaring no contract, and one whose
// declared contract failed to resolve all answer the same way here, and the
// resolution's own account of which it was is not kept: this presentation
// resolves per call and holds nothing afterwards, so no failure is reported from
// here. The presentation a rule over this fact is invoked on resolves the bound
// Features up front instead and reports that failure once, which is where the
// three outcomes are told apart. That is a difference from the component
// contracts above rather than an inconsistency with them: those are resolved when
// this value is built, so the failure has somewhere to live.
func (v *normalizedView) CompiledFeatureSchema(featureType string) (map[string]any, bool) {
	return CompileFeatureSchema(v.catalog, featureType)
}

// CompiledSocketValueSchema returns the contract a dependency socket declares
// over the developer's configuration for one connection, resolved, and whether
// there was a contract that resolved at all.
func (v *normalizedView) CompiledSocketValueSchema(
	componentType string, socket string,
) (map[string]any, bool) {
	return v.socketContracts[socketPillarKey{
		componentType: componentType, socket: socket, pillar: "values",
	}].resolved()
}

// CompiledSocketInfraSchema returns the contract a dependency socket declares
// over the operator's configuration for one connection, resolved, and whether
// there was a contract that resolved at all.
func (v *normalizedView) CompiledSocketInfraSchema(
	componentType string, socket string,
) (map[string]any, bool) {
	return v.socketContracts[socketPillarKey{
		componentType: componentType, socket: socket, pillar: "infra",
	}].resolved()
}

// ResolvedParameterContext returns what a document's declared parameter contract
// resolved to.
//
// It is always present: an empty map means no slot resolved to a value, never
// that there was no answer.
func (v *normalizedView) ResolvedParameterContext(string) map[string]any {
	if v.parameters == nil {
		return map[string]any{}
	}
	return v.parameters
}

// ---------------------------------------------------------------- shared readers

// lookupComponent reaches a component type through the catalog, answering not
// found where the build runs without one.
//
// The nil-tolerance is the point of the helper rather than a detail of it: the
// resolve runs catalog-less on some paths, and a reader reaching the lookup
// function directly there dereferences nothing and takes the process down.
func lookupComponent(catalog CatalogLookup, componentType string) (types.Component, bool) {
	if catalog.Component == nil {
		return types.Component{}, false
	}
	return catalog.Component(componentType)
}

// catalogComponentFor returns the catalog Component for a type URI, or nil on a
// miss. A miss is silent: an unresolvable type is another guard's report, and an
// advisory lint must not double-report it.
//
// It is the pointer-returning form of [lookupComponent] for a reader that
// branches on nil, and it sits beside it so the two catalog readings cannot come
// to disagree about what a miss is.
func catalogComponentFor(typeURI string, catalog CatalogLookup) *types.Component {
	comp, found := lookupComponent(catalog, typeURI)
	if !found {
		return nil
	}
	return &comp
}

// authoredNodeText serializes an authored node to opaque bytes, empty when the
// author wrote nothing there.
//
// It reproduces the node's content and not the file's formatting, which is why a
// reader of the result must not draw conclusions from layout.
func authoredNodeText(node *yaml.Node) []byte {
	if node == nil || node.Kind == 0 {
		return nil
	}
	out, err := yaml.Marshal(node)
	if err != nil {
		return nil
	}
	return out
}

// injectMechanismPayload returns one mechanism's authored payload from an
// injection block.
//
// It selects from the block's own enumeration rather than testing the mechanism
// name against the three the block names as fields. A second such test would be
// a second statement of which names those are, free to disagree with the first
// the moment either moved.
func injectMechanismPayload(inject *types.InjectBlock, mechanism string) any {
	payloads := InjectMechanismPayloads(inject)
	payload, _ := payloads.Get(mechanism)
	return payload
}

// injectDeliveryKeys returns the keys one mechanism's authored payload delivers,
// in the order the author wrote them where the authored shape carries an order,
// and alphabetically where it does not.
//
// The payload shapes are recognised by the one collector that knows them, so a
// shape that collector walks is a shape this walks — and the order each shape
// yields is settled there too. Sorting again here would be wrong rather than
// redundant: it would flatten the author order the ordered shapes carry, which
// is the order this reading exists to report.
func injectDeliveryKeys(inject *types.InjectBlock, mechanism string) []string {
	collected := map[string][]string{}
	appendInjectDeliveryKeys(collected, mechanism, injectMechanismPayload(inject, mechanism))
	return collected[mechanism]
}

// sortedKeysOf returns a map's keys in sorted order, which is what a fact
// promising a sorted set answers with.
func sortedKeysOf[V any](m map[string]V) []string {
	if len(m) == 0 {
		return nil
	}
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// sortedSocketPillarKeys orders the resolved socket contracts so a walk over
// them is the same walk on every run of the same input.
func sortedSocketPillarKeys(m map[socketPillarKey]resolvedContract) []socketPillarKey {
	if len(m) == 0 {
		return nil
	}
	out := make([]socketPillarKey, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].componentType != out[j].componentType {
			return out[i].componentType < out[j].componentType
		}
		if out[i].socket != out[j].socket {
			return out[i].socket < out[j].socket
		}
		return out[i].pillar < out[j].pillar
	})
	return out
}

// The compiler holds this type to the contract rather than a comment claiming
// it: a signature drifting from the declared facts stops compiling here.
//
// The stage assertions are the half the embedded base cannot supply. Embedding
// makes every fact the chain declares compile, so the view assertion alone would
// accept a presentation answering none of them; naming a stage's own obligation
// is what makes a fact added at that stage fail here. Two stages are named
// because this type writes bodies for two: its own, and the catalog stage's, whose
// facts it answers from the catalog it already holds. Without the second, a fact
// added at that stage would compile here and refuse when a check read it. No
// document kind's obligation is asserted, for the reason the type's own
// documentation gives: this presents a compilation rather than one authored
// document.
var (
	_ constraint.NormalizedView       = (*normalizedView)(nil)
	_ constraint.NormalizedStageFacts = (*normalizedView)(nil)
	_ constraint.CatalogStageFacts    = (*normalizedView)(nil)
)
