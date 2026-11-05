// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/ir"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/trace"
	"github.com/aggrega-ai/aggrega-cli/internal/redact"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// declarationReaders reads what a Component type and a Feature type declare
// sensitive, each answer memoised per type for the lifetime of one reader.
//
// The schema compile behind each answer is uncached and every node or attachment
// of one type reads the same answer, which is why the memo exists; its lifetime is
// one walk — one render, or one evaluation of the graph's assertions — so a
// catalog change between builds is never carried over. The resolution of each
// type goes through the one home for compiling that kind's schema, so these
// readers, the pillar-conformance gate and every surface withholding a declared
// datum cannot disagree about what an entity declares.
type declarationReaders struct {
	lookup     resolver.CatalogLookup
	components map[string]resolver.SensitiveLeaves
	features   map[string]resolver.SensitiveLeaves
}

func newDeclarationReaders(lookup resolver.CatalogLookup) *declarationReaders {
	return &declarationReaders{
		lookup:     lookup,
		components: map[string]resolver.SensitiveLeaves{},
		features:   map[string]resolver.SensitiveLeaves{},
	}
}

// component returns what a Component type declares sensitive. A catalog-less
// render — the shape several renderer-level callers use — supplies a zero-value
// lookup whose accessors are nil; nothing is declared in that world, so the
// question is answered here rather than asked of a schema reader that has no
// catalog to read.
func (r *declarationReaders) component(componentType string) resolver.SensitiveLeaves {
	if r.lookup.Component == nil || componentType == "" {
		return resolver.SensitiveLeaves{}
	}
	if d, cached := r.components[componentType]; cached {
		return d
	}
	var declared resolver.SensitiveLeaves
	if compiled, ok := resolver.CompileComponentSchema(r.lookup, componentType); ok {
		declared = resolver.DeclaredSensitiveLeaves(compiled)
	}
	r.components[componentType] = declared
	return declared
}

// feature returns what a Feature type declares sensitive.
func (r *declarationReaders) feature(featureType string) resolver.SensitiveLeaves {
	if featureType == "" {
		return resolver.SensitiveLeaves{}
	}
	if d, cached := r.features[featureType]; cached {
		return d
	}
	var declared resolver.SensitiveLeaves
	if compiled, ok := ir.CompileFeatureSchema(r.lookup, featureType); ok {
		declared = resolver.DeclaredSensitiveLeaves(compiled)
	}
	r.features[featureType] = declared
	return declared
}

// nodeDeclaration is what one node's own entities declare sensitive, read once
// before anything is evaluated: its Component's declarations, those of each Feature
// attached to the node itself, and which of its bound Recipe's locals and source
// parts are computed from a declared leaf. None of it depends on what a template
// produces, so it is in hand for the first template the node evaluates.
type nodeDeclaration struct {
	component resolver.SensitiveLeaves
	own       map[string]redact.AttachmentKeys
	derived   redact.Derived
}

// pillarKeys is the set for a template reading the node's own values and infra
// and its locals and nothing else: a locals entry, a provides entry, the node's
// source.
func (d nodeDeclaration) pillarKeys() redact.Keys {
	return redact.Keys{Values: d.component.Values, Infra: d.component.Infra, Locals: d.derived.Locals}
}

// assertionKeys is the set for an assertion evaluated against the node's own
// pillars — a Component's or a Recipe's: its values and infra, the values of the
// Features attached to it, the provides its Component declares and the locals
// computed from a declared leaf. A pillar a stage's context does not carry
// matches nothing, so one set serves both stages.
func (d nodeDeclaration) assertionKeys() redact.Keys {
	return withOwnDeclarations(d.pillarKeys(), d.own, d.component.Provides)
}

// withheldSets returns the two sets one assembly of the node's render context
// yields: the set a capture is withheld by, and the set a failure's text is
// withheld by.
//
// They are one set. The failure's is the assembled set widened by the node's own
// declarations; the capture's is that same set passed through the trace mode, whose
// escape hatch empties it for one invocation. A failure's text does not pass
// through the mode, because what a refusal says must not change with a switch
// scoped to the trace.
//
// The capture's set is read from the failure's rather than assembled beside it,
// because the capture shows a re-render of the workload template, and that
// template reads the node's own attachments and its own provides as readily as a
// template that fails. A narrower capture set would show, in the clear, a datum the
// same template's refusal withholds.
func (d nodeDeclaration) withheldSets(assembled redact.Keys, mode trace.Mode) (capture, failure redact.Keys) {
	failure = withOwnDeclarations(assembled, d.own, d.component.Provides)
	return mode.Keys(failure), failure
}

// refusalDeclarations answers what each node's own entities declare sensitive,
// and what a Feature type declares. Its zero value declares nothing, which is the
// set a caller rendering data no declaration governs passes.
type refusalDeclarations struct {
	nodes   map[string]nodeDeclaration
	feature func(featureType string) resolver.SensitiveLeaves
}

// node returns one node's declarations; a node the set does not hold declares
// nothing.
func (r refusalDeclarations) node(nodeID string) nodeDeclaration {
	return r.nodes[nodeID]
}

// attachment returns what a Feature type declares.
func (r refusalDeclarations) attachment(featureType string) resolver.SensitiveLeaves {
	return declaredLeavesOf(r.feature, featureType)
}

// readRefusalDeclarations reads every node's declarations through one reader, so
// each Component and Feature type is compiled once for the walk. recipes is the
// Recipe bound to each node as the catalog holds it — its authored template text is
// what the derived reading reads, and it has to be read before anything replaces a
// Recipe's source with the value its template produced.
func readRefusalDeclarations(graph *types.IRGraph, recipes map[string]*types.Recipe, readers *declarationReaders) refusalDeclarations {
	out := refusalDeclarations{nodes: map[string]nodeDeclaration{}, feature: readers.feature}
	if graph == nil {
		return out
	}
	for nodeID, node := range graph.Spec.Nodes.All() {
		declared := readers.component(node.Component)
		attachments, _ := graph.Spec.FeatureValuesMapping.Get(nodeID)
		out.nodes[nodeID] = nodeDeclaration{
			component: declared,
			own:       ownAttachmentKeys(attachments, readers.feature),
			derived:   derivedSensitiveKeys(recipes[nodeID], declared.Values, declared.Infra),
		}
	}
	return out
}

// declaredLeavesOf asks a Feature reader about one type, a nil reader declaring
// nothing.
func declaredLeavesOf(read func(featureType string) resolver.SensitiveLeaves, featureType string) resolver.SensitiveLeaves {
	if read == nil {
		return resolver.SensitiveLeaves{}
	}
	return read(featureType)
}

// ownAttachmentKeys returns what each Feature attached to the node itself declares
// sensitive, keyed by the instance name a template reads the attachment under at
// .features.<name>. An attachment one of the node's connections carries is not
// among them — a template reads it under that connection, and the node's set
// names it there — and an attachment whose Feature declares nothing contributes
// nothing.
func ownAttachmentKeys(attachments types.OrderedMap[types.IRFeatureEntry], feature func(featureType string) resolver.SensitiveLeaves) map[string]redact.AttachmentKeys {
	var out map[string]redact.AttachmentKeys
	if feature == nil {
		return out
	}
	for name, entry := range attachments.All() {
		if entry.OnConnection() {
			continue
		}
		leaves := feature(entry.Type)
		if !leaves.Any() {
			continue
		}
		if out == nil {
			out = map[string]redact.AttachmentKeys{}
		}
		out[name] = redact.AttachmentKeys{Values: leaves.Values, Infra: leaves.Infra, Provides: leaves.Provides}
	}
	return out
}

// withOwnDeclarations widens a node's set by the two pillars of the node's own a
// template reads that the per-pass pillar projection does not show: the
// declarations of the Features attached to the node itself, and the provides keys
// the node's Component declares. A template's refusal and the trace's re-render of
// the workload template are both withheld by the widened set.
func withOwnDeclarations(keys redact.Keys, own map[string]redact.AttachmentKeys, declaredProvides map[string]bool) redact.Keys {
	keys.Features = own
	keys.Provides = declaredProvides
	return keys
}

// attachmentContextKeys returns the set for a template rendered against one
// attachment's own pillars on a copy of the host node's render context — a
// FeatureRecipe's mapping, its locals, its source, its values mapping, its
// workload and its artifacts.
//
// Every pillar the copy carries over from the host is governed as the host's
// set governs it. The source part of the set is the host's, which is what an
// injectMapping reads, since it runs on the copy before any attachment's source is
// evaluated; a render that publishes the attachment's own source on its copy
// replaces that part with the attachment's through [publishFeatureRecipeSource]
// before any of its templates runs. The values and infra pillars are the attachment's own, so they
// are governed by the attachment's Feature instead. The locals are the host's
// resolved block with the FeatureRecipe's own merged over it, so an entry is
// withheld when either side computed it from a declared datum. The provides the
// copy carries are the attachment's own resolved provides where its FeatureRecipe
// publishes any and the host's otherwise, so both declarations are honoured — a
// key both name is withheld, which is the side of that overlap that cannot leak.
func attachmentContextKeys(host redact.Keys, leaves resolver.SensitiveLeaves, frLocals map[string]bool) redact.Keys {
	out := host
	out.Values = leaves.Values
	out.Infra = leaves.Infra
	out.Locals = unionSet(host.Locals, frLocals)
	out.Provides = unionSet(host.Provides, leaves.Provides)
	return out
}

// withOwnSource returns keys governing a context whose source pillar carries the
// source of the FeatureRecipe that authored source, in place of whatever source the
// context carried before: the parts of that source whose templates read a datum the
// rest of keys names.
//
// A FeatureRecipe's render context publishes its own evaluated source at .source,
// on a copy that carried another source there before — the host's, on a
// per-component attachment — or none. What of the pillar is withheld has to describe
// the value standing at it, so the set's source coordinates are replaced wherever the
// value is published — [publishFeatureRecipeSource] does both: kept from the host,
// they would withhold parts of a source the context no longer carries and leave the
// parts of the one it does carry unread.
//
// The source's templates are read against keys without its source coordinates,
// since a source template is evaluated before the source it produces is published,
// so no source stands on its context to be read. A set naming nothing else
// names no source part either, so this never makes a set withhold anything where it
// withheld nothing before.
func withOwnSource(keys redact.Keys, authored any) redact.Keys {
	keys.Source = nil
	keys.Source = sourcePartsReadingWithheld(authored, keys)
	return keys
}

// hostWithheld is what an author declared sensitive in a host node's render
// context and in each attachment's own pillars, for the templates rendered against
// a copy of that context with one attachment's pillars in place of the host's.
//
// host is the node's set. attachment answers what an attachment's Feature declares,
// by the instance name the attachment is held under on the node; nil declares
// nothing. The zero value declares nothing at all.
type hostWithheld struct {
	host       redact.Keys
	attachment func(instanceName string) resolver.SensitiveLeaves
}

// forAttachment returns the set for one attachment's templates. pillarsFound
// reports whether the attachment's own values and infra replaced the host's on the
// copy: where they did not, the copy still carries the host's and the host's
// declarations govern them. frLocals is the FeatureRecipe's own block, read for
// the entries computed from a declared leaf.
func (w hostWithheld) forAttachment(instanceName string, pillarsFound bool, frLocals types.OrderedMap[string]) redact.Keys {
	leaves := w.leaves(instanceName, pillarsFound)
	return attachmentContextKeys(w.host, leaves, derivedLocals(frLocals, leaves.Values, leaves.Infra))
}

// leaves returns what governs an attachment's values and infra on its copy of the
// context: its Feature's declarations where its own pillars are in place, the
// host's where they are not.
func (w hostWithheld) leaves(instanceName string, pillarsFound bool) resolver.SensitiveLeaves {
	if !pillarsFound {
		return resolver.SensitiveLeaves{Values: w.host.Values, Infra: w.host.Infra}
	}
	if w.attachment == nil {
		return resolver.SensitiveLeaves{}
	}
	return w.attachment(instanceName)
}

// boundaryFRKeys returns the set for a boundary-scope FeatureRecipe's templates.
// Their context carries the boundary attachment's own infra, governed by the
// Feature the attachment names, an empty values pillar, the FeatureRecipe's own
// locals, and the boundary's view. leaves is what that Feature declares.
//
// The boundary's view carries its connections and its attachments as lists, each
// entry governed by its own entity: a connection's target publishes its provides
// on the entry, and an attachment carries its own infra. view is the view the
// templates read, and the entries are read off it, so each position the set names
// is the entry it was read for. A local the FeatureRecipe computes from one of
// those entries is withheld with it.
func boundaryFRKeys(fr *types.FeatureRecipe, leaves resolver.SensitiveLeaves, view *types.BoundaryContext, lists listDeclarations) redact.Keys {
	if fr == nil {
		return redact.Keys{}
	}
	keys := attachmentPillarKeys(leaves, derivedLocals(fr.Spec.Locals, leaves.Values, leaves.Infra))
	return withListEntries(keys, boundaryListEntries(view, lists), fr.Spec.Locals)
}

// The paths a template reads each list at, spelled as every coordinate is — the
// json tags of the fields carrying them, joined by dots.
// TestListPathsAreTheTemplateContextTags holds each to those tags.
const (
	listBoundaryInternalEdges         = "boundary.internal_uses_edges"
	listBoundaryOutgoingCrossingEdges = "boundary.outgoing_cross_boundary_uses_edges"
	listBoundaryIncomingCrossingEdges = "boundary.incoming_cross_boundary_uses_edges"
	listBoundaryCrossingEdges         = "boundary.cross_boundary_uses_edges"
	listBoundaryAttachments           = "boundary.feature_attachments"
	listUsedBy                        = "context.used_by"
)

// listDeclarations answers what governs each entry of a list a template reads: the
// Component of the node an entry names, read through the graph that names it, and
// the Feature an attachment entry names. Its zero value declares nothing.
type listDeclarations struct {
	graph     *types.IRGraph
	component func(componentType string) resolver.SensitiveLeaves
	feature   func(featureType string) resolver.SensitiveLeaves
}

// listDeclarationsOf returns the answers a render's input carries, against graph.
func listDeclarationsOf(input RendererInput, graph *types.IRGraph) listDeclarations {
	return listDeclarations{graph: graph, component: input.declaredComponentLeaves, feature: input.declaredFeatureLeaves}
}

// nodeProvides returns the provides keys the Component of the node nodeID
// declares sensitive; a node the graph does not hold declares nothing.
func (d listDeclarations) nodeProvides(nodeID string) map[string]bool {
	if d.graph == nil || d.component == nil {
		return nil
	}
	node, ok := d.graph.Spec.Nodes.Get(nodeID)
	if !ok {
		return nil
	}
	return d.component(node.Component).Provides
}

// boundaryListEntries returns, for each list of a boundary's view, what each of
// its entries carries that its governing entity declares sensitive: the provides
// each connection's target declares, and what each attachment's Feature declares.
// A list no entry of which declares anything is left out.
func boundaryListEntries(view *types.BoundaryContext, d listDeclarations) map[string][]redact.AttachmentKeys {
	if view == nil {
		return nil
	}
	out := map[string][]redact.AttachmentKeys{}
	for _, list := range []struct {
		path  string
		edges []types.EnrichedEdge
	}{
		{listBoundaryInternalEdges, view.InternalUsesEdges},
		{listBoundaryOutgoingCrossingEdges, view.OutgoingCrossBoundaryUsesEdges},
		{listBoundaryIncomingCrossingEdges, view.IncomingCrossBoundaryUsesEdges},
		{listBoundaryCrossingEdges, view.CrossBoundaryUsesEdges},
	} {
		addListEntries(out, list.path, len(list.edges), func(i int) redact.AttachmentKeys {
			return redact.AttachmentKeys{Provides: d.nodeProvides(list.edges[i].Target)}
		})
	}
	addListEntries(out, listBoundaryAttachments, len(view.FeatureAttachments), func(i int) redact.AttachmentKeys {
		leaves := declaredLeavesOf(d.feature, view.FeatureAttachments[i].Type)
		return redact.AttachmentKeys{Values: leaves.Values, Infra: leaves.Infra, Provides: leaves.Provides}
	})
	if len(out) == 0 {
		return nil
	}
	return out
}

// usedByListEntries returns what each connection made to a node carries that the
// connecting node's Component declares sensitive among its provides, in the order
// the connections are listed. usedBy is the list the template's context carries.
func usedByListEntries(usedBy []types.InboundEdge, d listDeclarations) map[string][]redact.AttachmentKeys {
	out := map[string][]redact.AttachmentKeys{}
	addListEntries(out, listUsedBy, len(usedBy), func(i int) redact.AttachmentKeys {
		return redact.AttachmentKeys{Provides: d.nodeProvides(usedBy[i].ComponentID)}
	})
	if len(out) == 0 {
		return nil
	}
	return out
}

// addListEntries records the n entries of the list at path, each as entry reads
// it, when at least one of them declares something.
func addListEntries(out map[string][]redact.AttachmentKeys, path string, n int, entry func(i int) redact.AttachmentKeys) {
	entries := make([]redact.AttachmentKeys, n)
	declared := false
	for i := range entries {
		entries[i] = entry(i)
		declared = declared || entries[i].Any()
	}
	if declared {
		out[path] = entries
	}
}

// withListEntries widens keys by what the lists a template reads carry, entry by
// entry, and by the entries of locals — the block the same templates read at
// .locals — computed from one of those entries, directly or through an earlier
// entry. With no entry declaring anything, keys is returned as it is.
func withListEntries(keys redact.Keys, entries map[string][]redact.AttachmentKeys, locals types.OrderedMap[string]) redact.Keys {
	if len(entries) == 0 {
		return keys
	}
	keys.Lists.Entries = entries
	readable := redact.Keys{Values: keys.Values, Infra: keys.Infra, Lists: redact.ListKeys{Entries: entries}}
	for name := range localsReading(locals, readable) {
		if keys.Locals[name] {
			continue
		}
		if keys.Lists.Locals == nil {
			keys.Lists.Locals = map[string]bool{}
		}
		keys.Lists.Locals[name] = true
	}
	return keys
}

// attachmentPillarKeys returns the set for a template that reads nothing but one
// attachment's own pillars and its FeatureRecipe's locals — the locals block
// itself, and the provides an attachment's FeatureRecipe publishes.
func attachmentPillarKeys(leaves resolver.SensitiveLeaves, frLocals map[string]bool) redact.Keys {
	return redact.Keys{Values: leaves.Values, Infra: leaves.Infra, Locals: frLocals}
}

// unionSet returns the union of two sets, or nil when both are empty.
func unionSet(a, b map[string]bool) map[string]bool {
	if len(a) == 0 && len(b) == 0 {
		return nil
	}
	out := make(map[string]bool, len(a)+len(b))
	for k := range a {
		out[k] = true
	}
	for k := range b {
		out[k] = true
	}
	return out
}
