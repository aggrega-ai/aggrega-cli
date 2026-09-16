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
// The parameter names the application's contract declares are the one such fact
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

	// aggregate is the normalized application document.
	aggregate types.NormalizedAggregate

	// release is the deployment document, nil when the caller holds none.
	release *types.Release

	// catalog is how a declared type is reached.
	catalog CatalogLookup

	// parameters is the resolved parameter context every document in this
	// presentation shares.
	parameters map[string]any

	// instances is every component instance the application declares, by the
	// identity every keyed fact takes.
	instances map[string]types.ComponentInstance

	// releaseComponents is every component the deployment wrote against, by the
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

	// componentContracts is the resolved contract of each component type in
	// play, produced once when this value was built.
	componentContracts map[string]resolvedContract

	// socketContracts is the resolved per-edge contract of each socket pillar in
	// play, produced once when this value was built and keyed by the type, the
	// socket and the pillar together.
	socketContracts map[socketPillarKey]resolvedContract
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
		componentContracts: map[string]resolvedContract{},
		socketContracts:    map[socketPillarKey]resolvedContract{},
	}

	for _, p := range collectComponentInstances(na) {
		v.instances[p.name] = p.inst
	}
	if agg := na.Aggregate(); agg != nil {
		collectSubsystemFeatures("", agg.Spec.Subsystems, v.subsystemFeatures, &v.subsystemPaths)
	}
	if release != nil {
		collectBoundaryContent(
			types.NormalizeRelease(release), v.releaseComponents, v.boundaryFeatures, &v.boundaryPaths)
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
func collectSubsystemFeatures(
	path string,
	subsystems types.OrderedMap[types.Subsystem],
	out map[string]types.OrderedMap[types.FeatureInstance],
	order *[]string,
) {
	for name, sub := range subsystems.All() {
		child := name
		if sub.Synthesized {
			child = path
		} else if path != "" {
			child = path + "." + name
		}
		if !sub.Synthesized {
			out[child] = sub.Features
			*order = append(*order, child)
		}
		collectSubsystemFeatures(child, sub.Subsystems, out, order)
	}
}

// collectBoundaryContent records every component the deployment wrote against
// and every scope-wide attachment it declared, descending the boundary tree, and
// appends each declared scope's path to order in the order the document declares
// it.
//
// A synthesized scope contributes neither a block nor an entry in the order, on
// the same reasoning its counterpart on the other authoring document does: the
// operator declared no such scope, so naming one would report at a key the file
// does not contain.
func collectBoundaryContent(
	nr types.NormalizedRelease,
	components map[string]types.BoundaryComponent,
	features map[string]types.OrderedMap[types.BoundaryFeature],
	order *[]string,
) {
	rel := nr.Release()
	if rel == nil {
		return
	}
	var walk func(path string, b types.Boundary)
	walk = func(path string, b types.Boundary) {
		if !b.Synthesized {
			features[path] = b.Features
			*order = append(*order, path)
		}
		declared := types.BoundaryComponents(b)
		for name, bc := range declared.All() {
			components[name] = bc
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

// resolveContracts resolves every declared contract this presentation can be
// asked about, once each.
//
// It walks the component types the application names rather than the whole
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
			"a component instance authors the %q pillar only; %q is authored on the deployment document "+
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

// releaseUses returns one socket's authored entries from the deployment's edge
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
// application declares, and whether an attachment of that name exists there.
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

// ---------------------------------------------------------------- deployment facts

// ReleaseComponentEntryPresent reports whether the deployment wrote an entry
// against one component, on either of the axes a boundary names components on.
func (v *normalizedView) ReleaseComponentEntryPresent(node string) bool {
	_, present := v.releaseComponents[node]
	return present
}

// ReleaseComponentInfra returns the physical sizing the deployment wrote for one
// component, in author order, empty when it wrote none.
func (v *normalizedView) ReleaseComponentInfra(node string) types.OrderedMap[any] {
	return v.releaseComponents[node].Infra
}

// releaseAttachment returns one attachment the deployment wrote against a
// component.
func (v *normalizedView) releaseAttachment(node, attachment string) (types.BoundaryFeature, bool) {
	bc := v.releaseComponents[node]
	return bc.Features.Get(attachment)
}

// releaseAttachmentNames returns the names the deployment's own attachment block
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

// ReleaseAuthoredUsesSocketConnections returns the connections the deployment's
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

// ReleaseAuthoredUsesSocketEntryAliases returns the aliases the deployment's
// edge surface wrote an entry under for one socket, in author order.
func (v *normalizedView) ReleaseAuthoredUsesSocketEntryAliases(node string, socket string) []string {
	aliases, ok := v.releaseUses(node, socket)
	if !ok {
		return nil
	}
	return aliases.Keys()
}

// ReleasePerEdgeTarget returns the component one connection of the deployment's
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

// releaseEdgeEntry returns one connection's entry on the deployment's edge
// surface.
func (v *normalizedView) releaseEdgeEntry(node, socket, alias string) (types.ReleaseUsesEntry, bool) {
	aliases, ok := v.releaseUses(node, socket)
	if !ok {
		return types.ReleaseUsesEntry{}, false
	}
	return aliases.Get(alias)
}

// BoundaryAttachmentDeclaration returns what one scope-wide attachment of the
// deployment declares, and whether an attachment of that name exists there.
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

// ReleaseAttachmentType returns the Feature type one attachment the deployment
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
// The second assertion is the half the embedded base cannot supply. Embedding
// makes every fact the chain declares compile, so the view assertion alone would
// accept a presentation answering none of them; naming this stage's own
// obligation is what makes a fact added at this stage fail here. No document
// kind's obligation is asserted, for the reason the type's own documentation
// gives: this presents a compilation rather than one authored document.
var (
	_ constraint.NormalizedView       = (*normalizedView)(nil)
	_ constraint.NormalizedStageFacts = (*normalizedView)(nil)
)
