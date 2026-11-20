// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package serve

import (
	"context"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/ir"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/selector"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// ConnectionCoordinate addresses one connection a graph node's component draws,
// on every surface that answers for one: the socket the connection is wired
// through, the alias it carries there, and the document an attachment on it is
// written in.
//
// It rides beside a graph node rather than replacing one, because a connection
// exists only on a compiled graph and is named relative to the node drawing it.
// The socket and the alias are what the author wrote, so they are matched exactly
// and never inferred from anything else.
//
// Document is one of the two authoring documents, spelled as the compiler names
// them — [constraint.AuthoringDocumentAggregate] and
// [constraint.AuthoringDocumentRelease] — and it is what selects where an
// attachment on the connection is written. A question that does not depend on
// the document, such as which Features may fill a slot, accepts it and answers
// the same either way.
type ConnectionCoordinate struct {
	Socket   string `json:"socket"`
	Alias    string `json:"alias"`
	Document string `json:"document"`
}

// Fault reports the first field of the coordinate a request cannot be answered
// with, and the reason, or two empty strings where the coordinate is sound.
//
// It names the field rather than wording the whole refusal, because each surface
// spells an argument its own way and words a refusal in its own channel; what
// must not differ between them is which coordinates are refused, so that is
// decided here once.
//
// A document outside the two is refused wherever it is sent, including where the
// answer does not depend on it: a value accepted and ignored reads to its sender
// as a value that was honoured.
func (c ConnectionCoordinate) Fault(documentRequired bool) (field, reason string) {
	switch {
	case strings.TrimSpace(c.Socket) == "":
		return "socket", "is required: a connection is addressed by the socket it is wired through"
	case strings.TrimSpace(c.Alias) == "":
		return "alias", "is required: a connection is addressed by the alias it carries within its socket"
	case c.Document == "" && documentRequired:
		return "document", "is required here: name the document an attachment on the connection is written in, " +
			constraint.AuthoringDocumentAggregate + " or " + constraint.AuthoringDocumentRelease
	case c.Document != "" && c.Document != constraint.AuthoringDocumentAggregate &&
		c.Document != constraint.AuthoringDocumentRelease:
		return "document", "names neither document an attachment on a connection is written in; send " +
			constraint.AuthoringDocumentAggregate + " or " + constraint.AuthoringDocumentRelease
	}
	return "", ""
}

// SlotCandidacy is one slot a connection's socket declares, in the
// attach-candidates answer for that connection: the slot exactly as the consuming
// Component declares it on the socket, and the Features that may fill it.
//
// The declaration's fields are carried rather than left to a second request,
// because they are what a client explains a candidate, or the absence of one,
// with: the selector the candidates were narrowed by, whether the slot must be
// filled, what the Component says it is for, and whether what fills it handles
// secret material.
//
// Inject and Candidates are always lists on the wire, empty rather than null, so
// a client iterates without a nil check.
type SlotCandidacy struct {
	Slot        string            `json:"slot"`
	Selector    string            `json:"selector"`
	Required    bool              `json:"required"`
	Inject      []string          `json:"inject"`
	Sensitive   bool              `json:"sensitive"`
	Description string            `json:"description"`
	Candidates  []AttachCandidate `json:"candidates"`
}

// NoSlotReason says why no slot may be filled on a connection, in the
// attach-candidates answer for it, and is empty wherever a slot may be.
//
// It exists so that "nothing may attach here" and "nothing matched" are two
// answers rather than one: both carry an empty slot list, and only this field
// tells them apart. The values are in the descriptive register — lowercase words
// joined by dashes — like every other platform-defined identifier that is not a
// halting condition's short code.
type NoSlotReason string

const (
	// NoSlotReasonFeatureCreatedConnection reports a connection a Feature drew. An
	// attachment is written only on a connection a component or the Release draws,
	// so such a connection is never a place one goes, whatever its socket declares.
	NoSlotReasonFeatureCreatedConnection NoSlotReason = "feature-created-connection"

	// NoSlotReasonAdHocSocket reports a socket the consuming Component does not
	// declare: the author named it, and an undeclared socket declares no slot.
	NoSlotReasonAdHocSocket NoSlotReason = "ad-hoc-socket"

	// NoSlotReasonNoSlotDeclared reports a socket the Component declares with no
	// slot on it.
	NoSlotReasonNoSlotDeclared NoSlotReason = "no-slot-declared"
)

// Sentence renders the reason as a clause a surface reading it out to a person
// embeds, and is the single home of that wording. An empty reason renders as the
// empty string.
func (r NoSlotReason) Sentence() string {
	switch r {
	case NoSlotReasonFeatureCreatedConnection:
		return "a Feature drew this connection, and an attachment is written only on a connection a component " +
			"or the Release draws"
	case NoSlotReasonAdHocSocket:
		return "the Component does not declare this socket, so it declares no slot for a connection to fill"
	case NoSlotReasonNoSlotDeclared:
		return "the Component declares this socket with no slot on it"
	default:
		return ""
	}
}

// ConnectionRefusal names a reason a request about a connection is not answered,
// or is empty where it is.
//
// It is a value rather than an error identity for the reason
// [AttachmentContainerRefusal] is: nothing halts and nothing is compiled, and
// each refuses one request about one connection. Carrying it as data lets every
// surface report the same refusal in its own channel, and makes
// [ConnectionRefusal.Reason] the single home of the wording.
type ConnectionRefusal string

const (
	// ConnectionAnswered reports that the request was answered. It is the zero
	// value, so a caller that forgets to check reads a refusal rather than an
	// answer.
	ConnectionAnswered ConnectionRefusal = ""

	// ConnectionRefusedUnknownNode reports a node identifier the graph does not
	// carry.
	ConnectionRefusedUnknownNode ConnectionRefusal = "unknown-graph-node"

	// ConnectionRefusedUnknownConnection reports a node the graph carries with no
	// connection under that socket and alias.
	ConnectionRefusedUnknownConnection ConnectionRefusal = "unknown-connection"

	// ConnectionRefusedSlotsUnknown reports a node whose component type does not
	// resolve, so the slots its socket declares cannot be read. That is an unknown
	// set rather than an empty one, and reporting it as empty would state a
	// determination nothing made.
	ConnectionRefusedSlotsUnknown ConnectionRefusal = "socket-slots-unknown"

	// ConnectionRefusedFeatureCreated reports a connection a Feature drew, asked
	// where an attachment on it belongs: nowhere, because none is written on it.
	ConnectionRefusedFeatureCreated ConnectionRefusal = "feature-created-connection"

	// ConnectionRefusedDocumentUnnamed reports a request for where an attachment
	// belongs that names neither authoring document.
	ConnectionRefusedDocumentUnnamed ConnectionRefusal = "document-not-named"

	// ConnectionRefusedReleaseDrawn reports a connection the Release drew, asked
	// where an attachment on it belongs in the Aggregate, which holds no entry for
	// it.
	ConnectionRefusedReleaseDrawn ConnectionRefusal = "release-drawn-connection"

	// ConnectionRefusedNoReleaseEntry reports a Release that writes no entry for the
	// node, so there is no entry of its own for a per-edge one to sit under.
	ConnectionRefusedNoReleaseEntry ConnectionRefusal = "no-release-component-entry"

	// ConnectionRefusedNoSourceDocument reports a connection whose document is not
	// recorded, so there is no file to name.
	ConnectionRefusedNoSourceDocument ConnectionRefusal = "no-source-document"

	// ConnectionRefusedUnlocatableEntry reports a connection entry whose site cannot
	// be named in its document. Nothing is reported rather than a path assembled
	// from what is known, because a path missing the entry it belongs under names a
	// place the file does not contain.
	ConnectionRefusedUnlocatableEntry ConnectionRefusal = "connection-entry-not-locatable"
)

// Reason renders the refusal as a clause a surface embeds in whatever refusal
// channel it has. It is the single home of the wording, so two surfaces declining
// the same request cannot describe it differently. An answered request renders as
// the empty string.
func (r ConnectionRefusal) Reason() string {
	switch r {
	case ConnectionRefusedUnknownNode:
		return AttachmentContainerRefusedUnknownNode.Reason()
	case ConnectionRefusedUnknownConnection:
		return "the node carries no connection through that socket under that alias on the compiled graph; a " +
			"connection is named by the socket its component wires it through and the alias it carries there"
	case ConnectionRefusedSlotsUnknown:
		return "the node's component type does not resolve, so the slots the socket declares cannot be read"
	case ConnectionRefusedFeatureCreated:
		return NoSlotReasonFeatureCreatedConnection.Sentence()
	case ConnectionRefusedDocumentUnnamed:
		return "the request names neither document an attachment on a connection is written in, and either may " +
			"hold one; name " + constraint.AuthoringDocumentAggregate + " or " + constraint.AuthoringDocumentRelease
	case ConnectionRefusedReleaseDrawn:
		return "the Release drew this connection, so the Aggregate holds no entry for it; an attachment on it is " +
			"written in the Release"
	case ConnectionRefusedNoReleaseEntry:
		return "the Release writes no entry for that node, so there is no entry of its own for a per-edge one to " +
			"sit under; place the node in one of the Release's boundaries first"
	case ConnectionRefusedNoSourceDocument:
		return "no document is recorded for the connection's entry, and where an attachment belongs is a fact about " +
			"a document"
	case ConnectionRefusedUnlocatableEntry:
		return "the connection's entry cannot be named in the document it is written in, so there is no container " +
			"for an attachment to go inside"
	default:
		return ""
	}
}

// graphConnection finds the node and the connection a request addresses on an
// already-compiled graph.
//
// The connection is the edge drawn from the node through the socket under the
// alias, matched on what the author wrote and nothing else. One socket and alias
// can carry two edges — a component's own connection and one an attachment's
// dependency block drew — and the request addresses an authoring site, which only
// a connection a component or the Release drew has. So that edge is the one
// answered for wherever the graph carries one, and a connection only a Feature
// drew is answered for as such.
func graphConnection(graph *types.IRGraph, node string, connection ConnectionCoordinate) (types.IRNode, types.IREdge, ConnectionRefusal) {
	if graph == nil {
		return types.IRNode{}, types.IREdge{}, ConnectionRefusedUnknownNode
	}
	irNode, ok := graph.Spec.Nodes.Get(node)
	if !ok {
		return types.IRNode{}, types.IREdge{}, ConnectionRefusedUnknownNode
	}
	var featureDrawn *types.IREdge
	for i := range graph.Spec.Edges {
		edge := graph.Spec.Edges[i]
		if edge.Source != node || edge.Socket != connection.Socket || edge.Alias != connection.Alias {
			continue
		}
		if edge.OriginFeature == "" {
			return irNode, edge, ConnectionAnswered
		}
		if featureDrawn == nil {
			featureDrawn = &graph.Spec.Edges[i]
		}
	}
	if featureDrawn != nil {
		return irNode, *featureDrawn, ConnectionAnswered
	}
	return types.IRNode{}, types.IREdge{}, ConnectionRefusedUnknownConnection
}

// AttachCandidatesForConnection reports, for one connection the named node of an
// already-compiled graph draws, which Features may fill each slot the
// connection's socket declares — beside the answer for the node itself, which it
// carries unchanged.
//
// # Which slots, and why no slot is three answers
//
// The slots are read through [resolver.SocketAttachmentSlots], the one home every
// rule over a socket's slots reads them through, so what a client is offered and
// what a build accepts are read from one declaration. Where no slot may be
// filled, the slot list is empty and [NoSlotReason] says which of three things
// holds, each read from the graph's own record of the connection: a Feature drew
// it, its socket is one the Component does not declare, or the declared socket
// declares no slot. That is an answer, never a refusal, because the connection
// exists; a refusal is reserved for a connection the graph does not carry.
//
// # Which Features may fill a slot
//
// A Feature filling a slot is held to two rules when the build loads, and both
// are applied here, each through the entry point the build evaluates it with:
// its declared applicability against the node's finalised labels, which every
// attachment the node carries is held to, and the slot's selector against the
// Feature's own labels. The first is the very match the node's own candidates
// are computed by — one population, admitted once — and the second narrows it per
// slot. A Feature whose labels the slot's selector cannot be evaluated against is
// not offered, because a build refuses rather than admits it.
//
// The graph is the caller's rather than this function's, for the reason
// [AttachCandidatesForGraphNode] gives: what a surface promises about the lockfile
// is a property of its own operation, and everything downstream of the graph is
// one implementation here.
func AttachCandidatesForConnection(ctx context.Context, root string, cfg workspaceconfig.Config, view *catalog.LazyLookup,
	reach workspaceconfig.ReleaseReach, graph *types.IRGraph, node string,
	connection ConnectionCoordinate) (AttachCandidatesResult, ConnectionRefusal) {
	return attachCandidatesForConnectionIn(ctx, root, cfg, view, reach, nil, graph, node, connection)
}

// attachCandidatesForConnectionIn is the whole of AttachCandidatesForConnection
// with the fresher-than-disk overlay supplied, which only a caller holding a
// session has.
func attachCandidatesForConnectionIn(ctx context.Context, root string, cfg workspaceconfig.Config, view *catalog.LazyLookup,
	reach workspaceconfig.ReleaseReach, overlay []workspaceDoc, graph *types.IRGraph, node string,
	connection ConnectionCoordinate) (AttachCandidatesResult, ConnectionRefusal) {
	irNode, edge, refusal := graphConnection(graph, node, connection)
	if refusal != ConnectionAnswered {
		return AttachCandidatesResult{}, refusal
	}

	decls := featureDeclsIn(ctx, root, cfg, view, reach, overlay)
	result := graphNodeCandidacy(node, irNode, decls)
	// Built rather than appended to, so the list is on the wire whatever follows.
	result.Slots = []SlotCandidacy{}

	switch {
	case edge.OriginFeature != "":
		result.NoSlotReason = NoSlotReasonFeatureCreatedConnection
		return result, ConnectionAnswered
	case edge.SocketDeclaration == types.SocketDeclarationAdHoc:
		result.NoSlotReason = NoSlotReasonAdHocSocket
		return result, ConnectionAnswered
	}

	if view == nil {
		return AttachCandidatesResult{}, ConnectionRefusedSlotsUnknown
	}
	// The slots are those of the Component the node compiled from: its type at the
	// version the graph records for it, which the type as written does not name
	// where it names no version or a range. A node no Component was found for
	// records no version and is asked as written.
	compiledAs := irNode.Component
	if irNode.ResolvedVersion != "" {
		compiledAs = catalog.ReferenceAt(irNode.Component, irNode.ResolvedVersion)
	}
	slots, known := resolver.SocketAttachmentSlots(view.AsCatalogLookup(), compiledAs, edge.Socket)
	if !known {
		return AttachCandidatesResult{}, ConnectionRefusedSlotsUnknown
	}
	if slots.Len() == 0 {
		result.NoSlotReason = NoSlotReasonNoSlotDeclared
		return result, ConnectionAnswered
	}

	admitted := admittedDecls(decls, irNode.Labels)
	for name, slot := range slots.All() {
		result.Slots = append(result.Slots, slotCandidacy(name, slot, admitted))
	}
	return result, ConnectionAnswered
}

// attachCandidatesForConnection builds the named Release's graph and reports the
// candidacy for one connection a node of it draws. It runs the preview the
// graph-node answer runs, with its lockfile precondition and with nothing
// rendered, and draws the candidates in the same Release's terms, so the node
// half of this answer is the graph-node answer for the same node.
func (s *Server) attachCandidatesForConnection(ctx context.Context, node, release string,
	connection ConnectionCoordinate) (AttachCandidatesResult, *jsonRPCError) {
	preview, errEnvelope := s.previewRelease(ctx, release, pipeline.Options{IncludeTrace: false, SkipRender: true})
	if errEnvelope != nil {
		return AttachCandidatesResult{}, errEnvelope
	}
	terms, termsErr := s.termsFor(release)
	if termsErr != nil {
		return AttachCandidatesResult{}, termsErr
	}
	result, refusal := attachCandidatesForConnectionIn(ctx, s.workspaceRoot, s.wsConfig, terms.lookup, terms.reach,
		s.overlayDocs(), preview.pipeline.Completed().IRGraph, node, connection)
	if refusal != ConnectionAnswered {
		return AttachCandidatesResult{}, &jsonRPCError{
			Code:    errEntityNotResolvable,
			Message: connectionRefusalMessage("no candidates are reported for", node, release, connection, refusal),
		}
	}
	return result, nil
}

// connectionAttachmentContainerForGraphNode builds the named Release's graph and
// reports where an attachment on one connection a node of it draws belongs, in
// the document the coordinate names.
func (s *Server) connectionAttachmentContainerForGraphNode(ctx context.Context, node, release string,
	connection ConnectionCoordinate) (AttachmentContainerResult, *jsonRPCError) {
	preview, errEnvelope := s.previewRelease(ctx, release, pipeline.Options{IncludeTrace: false, SkipRender: true})
	if errEnvelope != nil {
		return AttachmentContainerResult{}, errEnvelope
	}
	var rel *types.Release
	if preview.resolved != nil {
		rel = preview.resolved.Release
	}
	result, refusal := ConnectionAttachmentContainer(s.workspaceRoot, preview.pipeline.Completed().IRGraph, rel,
		preview.releasePath, node, connection)
	if refusal != ConnectionAnswered {
		return AttachmentContainerResult{}, &jsonRPCError{
			Code:    errEntityNotResolvable,
			Message: connectionRefusalMessage("no container is reported for", node, release, connection, refusal),
		}
	}
	return result, nil
}

// connectionRefusalMessage words a refusal about a connection, naming the
// connection as the request addressed it: the graph, the node, the socket and
// the alias, then the reason.
func connectionRefusalMessage(lead, node, release string, connection ConnectionCoordinate, refusal ConnectionRefusal) string {
	return lead + " the connection " + connection.Alias + " through socket " + connection.Socket + " of " + node +
		" on the compiled graph of " + release + ": " + refusal.Reason()
}

// slotCandidacy is one slot's declaration and the admitted Features its selector
// admits, in the shared workspace-entity order.
func slotCandidacy(name string, slot types.AttachmentSlot, admitted []featureDecl) SlotCandidacy {
	candidates := []AttachCandidate{}
	for _, decl := range admitted {
		match, err := selector.MatchString(slot.Selector, decl.labels)
		if err != nil || !match {
			continue
		}
		candidates = append(candidates, decl.entity)
	}
	sortCandidates(candidates)
	inject := append([]string{}, slot.Inject...)
	return SlotCandidacy{
		Slot:        name,
		Selector:    slot.Selector,
		Required:    slot.Required,
		Inject:      inject,
		Sensitive:   slot.Sensitive,
		Description: slot.Description,
		Candidates:  candidates,
	}
}

// ConnectionAttachmentContainer reports where an attachment on one connection the
// named node of an already-compiled graph draws belongs, in the document the
// coordinate names: which file, and the connection entry's own `features`
// container inside it.
//
// # The two documents, and what each refuses
//
// In the Aggregate, the container is the connection's entry in the document the
// node was authored in, named through the same site a finding about an
// attachment on that entry is positioned by. A connection the Release drew has no
// entry there, and is refused rather than given a path the file does not carry.
//
// In the Release, the container is the per-edge entry for the connection under
// the Release's own entry for the node, spelled through the one fold the rules
// over the Release's per-edge entries read — whether or not the Release writes
// that per-edge entry yet, which is the ordinary case for a connection the
// Aggregate drew. A Release writing no entry for the node is refused: the entry a
// per-edge one sits under is the operator's to write, and composing it here would
// be deciding where the node is placed. The file is the Release document,
// workspace-relative through the helper the build names it with.
//
// A connection a Feature drew is refused in either document, because an
// attachment is written only on a connection a component or the Release draws.
//
// It reports and never writes, and nothing here reads a name, a type or a label
// to decide where something goes: the answer is read from what the graph records
// about the connection and from the documents' own structure.
func ConnectionAttachmentContainer(root string, graph *types.IRGraph, release *types.Release, releasePath, node string,
	connection ConnectionCoordinate) (AttachmentContainerResult, ConnectionRefusal) {
	irNode, edge, refusal := graphConnection(graph, node, connection)
	if refusal != ConnectionAnswered {
		return AttachmentContainerResult{}, refusal
	}
	if edge.OriginFeature != "" {
		return AttachmentContainerResult{}, ConnectionRefusedFeatureCreated
	}

	var site constraint.AttachmentSite
	switch connection.Document {
	case constraint.AuthoringDocumentRelease:
		file := ""
		if releasePath != "" {
			file = catalog.RelativeToWorkspace(root, releasePath)
		}
		entry, found := resolver.ReleaseConnectionEntry(release, file, node, edge.Socket, edge.Alias)
		if !found {
			return AttachmentContainerResult{}, ConnectionRefusedNoReleaseEntry
		}
		site = constraint.ConnectionAttachmentSiteInRelease(entry, "")
	case constraint.AuthoringDocumentAggregate:
		if edge.OriginRelease {
			return AttachmentContainerResult{}, ConnectionRefusedReleaseDrawn
		}
		site = constraint.ConnectionAttachmentSiteInAggregate(ir.NodeInstanceOf(node, irNode), edge.Socket, edge.Alias, "")
	default:
		// Either document can hold the entry for a connection the Aggregate drew,
		// so answering for an unnamed one would be a guess.
		return AttachmentContainerResult{}, ConnectionRefusedDocumentUnnamed
	}
	if site.SourceFile == "" {
		return AttachmentContainerResult{}, ConnectionRefusedNoSourceDocument
	}
	container := constraint.ConnectionAttachmentContainerSegments(site)
	if len(container) == 0 {
		return AttachmentContainerResult{}, ConnectionRefusedUnlocatableEntry
	}
	return AttachmentContainerResult{
		Node:      node,
		File:      site.SourceFile,
		Container: container,
	}, ConnectionAnswered
}
