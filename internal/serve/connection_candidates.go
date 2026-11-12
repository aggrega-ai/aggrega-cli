// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package serve

import (
	"context"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/pkg/pathscope"
	"github.com/aggrega-ai/aggrega-cli/pkg/selector"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// SocketCandidacy is one socket in the aggrega/connectionCandidates
// response, with the components whose declarations satisfy it.
//
// Declaration is the whole point of the shape, and it carries
// types.SocketDeclarationDeclared or types.SocketDeclarationAdHoc — the
// same two values an edge in the IR graph carries for the same question,
// so a client reads one vocabulary for "where did this socket come from"
// wherever it meets the question. Declared means the addressed entity
// lists the socket in its usesSchema; ad-hoc means it does not, and the
// socket is one the request named.
//
// Selector is the label constraint the socket declares over what may
// satisfy it, and it is what Targets was computed from — carried so a
// client can explain any candidate, or the absence of one, from the
// response alone. Empty means the socket declares no constraint, which
// leaves every component a valid target rather than none; that is the
// contract the constraint language attaches to an empty selector, and it
// is the answer a build reaches for the same socket.
//
// Required reports whether the socket must end up bound. It is declared
// alongside the selector on the same usesSchema entry, so it is read from
// the same place rather than from a second lookup that could disagree.
//
// Targets is always an array on the wire, empty rather than null, so a
// client iterates without a nil check. Each entry is the joinable
// identity triple and nothing else: the labels that decided the match are
// already reported by aggrega/catalogIndex for the same triple, and a
// second copy here would be free to disagree with it.
type SocketCandidacy struct {
	Socket      string               `json:"socket"`
	Declaration string               `json:"declaration"`
	Selector    string               `json:"selector"`
	Required    bool                 `json:"required"`
	Targets     []WorkspaceEntityRef `json:"targets"`
}

// connectionCandidatesParams is the aggrega/connectionCandidates request
// shape.
//
// Node names the entity whose sockets are reported, by its metadata.name —
// the same name aggrega/catalogIndex reports and the same coordinate
// aggrega/attachCandidates takes.
//
// Socket is optional and selects between the two questions a client asks.
// Omitted, the response reports every socket the entity declares. Named,
// the response reports that one socket and only it — which is the only way
// to ask about a socket the entity does NOT declare, since such a socket
// appears in no declaration to enumerate. That is not a convenience: an
// author may write a socket the catalog entity never declared, so a client
// composing such a connection has to be able to ask about it, and a
// surface that could only enumerate declarations could not answer.
type connectionCandidatesParams struct {
	Node   string `json:"node"`
	Socket string `json:"socket"`
}

// ConnectionCandidatesResult is the connection-candidacy answer, on every
// surface that carries one.
//
// AdHocSocketsAccepted reports whether the addressed entity's dependency
// surface accepts a socket its own declaration does not list. It is the
// field that keeps the socket half of this answer honest: without it a
// client holding a list of declared sockets cannot tell a complete set of
// legal names from an open one, and would present the declarations as the
// only choices — teaching the author that their freedom is narrower than
// it is.
//
// It is not a constant, and the two answers come from two different rules.
// A Component's own dependency surface accepts an ad-hoc socket: the
// author of an Aggregate owns its topology and may name a socket the
// catalog did not anticipate, so the name is accepted and a near-miss on
// it is reported as an advisory rather than refused. A Feature's
// attachment surface does not: an attachment participates in the topology
// the attached Feature declares rather than authoring one, so that
// Feature's usesSchema is the whole of what its sockets may be, and a
// socket absent from it halts. Any other kind declares no dependency
// surface at all, so there is nothing for an ad-hoc socket to be accepted
// onto.
//
// Sockets is always an array on the wire, empty rather than null.
type ConnectionCandidatesResult struct {
	Node                 string            `json:"node"`
	AdHocSocketsAccepted bool              `json:"adHocSocketsAccepted"`
	Sockets              []SocketCandidacy `json:"sockets"`
}

// dispatchConnectionCandidates reports what the named entity may connect
// to: which sockets it declares, and for each one the components whose
// declared labels satisfy the constraint that socket declares over its
// targets.
//
// # It reports and never creates
//
// Read-only in the strongest sense: it mutates nothing, resolves nothing,
// reaches no registry or network, and writes nothing to any document. In
// particular it does not create an edge and does not propose the text of
// one. It answers a question and stops; the editor composes and applies
// whatever edit follows, because the editor is the surface that owns the
// buffer and the undo history the edit lands in. A capability that wrote
// source from here would be deciding an author's text from a process with
// no view of either.
//
// # The two halves of the answer are not equally complete, and the
// # response says which is which
//
// A consumer may invent a connection but may only consume what a producer
// declares, and that asymmetry reaches the wire rather than being smoothed
// over. The target half is complete: the components reported for a socket
// are every workspace-local Component whose declared labels satisfy that
// socket's declared constraint, so a client offering them offers the set
// the same constraint would admit during a build. The socket half is not,
// and cannot be: an author may write a socket the catalog entity never
// declared, so the declared sockets are a helpful start rather than the
// set of legal names. Each socket therefore carries where it came from,
// and the response carries whether an undeclared one is accepted at all.
//
// A request naming no entity, or naming one the workspace does not
// declare, is an error rather than an empty socket list — the same
// distinction aggrega/attachCandidates draws, for the same reason: those
// two answers mean different things to a client, and collapsing them would
// report a typo as an entity with nothing to offer.
func (s *Server) dispatchConnectionCandidates(ctx context.Context, req *jsonRPCRequest) error {
	var params connectionCandidatesParams
	if len(req.Params) > 0 {
		if err := decodeParams(req, &params); err != nil {
			return s.writeRefusal(ctx, req.ID, jsonRPCInvalidParams, "invalid params: ", err)
		}
	}
	if params.Node == "" {
		return s.writeError(ctx, req.ID, jsonRPCInvalidParams, "params.node is required")
	}
	if err := ctx.Err(); err != nil {
		return s.writeRefusal(ctx, req.ID, jsonRPCInternalError, "", err)
	}
	result, found := s.connectionCandidates(ctx, params.Node, params.Socket)
	if !found {
		return s.writeError(ctx, req.ID, errEntityNotResolvable,
			"no workspace entity is named "+params.Node)
	}
	return s.writeResult(ctx, req.ID, result)
}

// connectionCandidates projects the workspace document traversal down to
// the addressed entity's sockets and their valid targets, in a
// deterministic order. The second return reports whether the workspace
// declares the named entity at all.
//
// One traversal serves every part of the answer: the addressed entity's
// own usesSchema and every candidate Component's labels are collected in a
// single pass, so both are read from one view of the workspace rather than
// from two.
//
// # Which constraint a target is measured against
//
// The socket's declared selector, read from the usesSchema entry the
// addressed entity declares the socket under, and evaluated against a
// candidate's declared labels through the one selector entry point every
// surface evaluates a label constraint through. That shared evaluation is
// what keeps the set an editor offers and the set a build admits from
// diverging: a projection that re-implemented the match would agree on the
// ordinary case and part company on the awkward one.
//
// An empty selector is one answer and not two. A socket declared without a
// constraint, and a socket no declaration covers at all, both leave the
// target unconstrained — so both report every candidate, and neither has
// to be told apart from the other here. That is the same reading the
// build's own rule over this constraint takes.
//
// # Which labels the match is evaluated against
//
// The labels each candidate document declares at metadata.labels, read
// through the same helper aggrega/catalogIndex reports them through, so a
// palette built from the index and a target list built from this method
// cannot disagree about what labels a component carries.
//
// Those are the labels the document declares, which is what a read-only
// query over source can see. A build evaluates a socket's constraint
// against the finalised label set of the graph node at the far end of the
// edge, and that set is reached differently: a node takes the labels of
// the component type it resolves to rather than of a document sharing its
// name, and a label-transform attachment the workspace configuration
// carries may then add to, change or remove them. So this reports the
// match over one document's declared labels; it is not a prediction of
// what a build will decide.
//
// # Which entities are candidate targets
//
// Every workspace-local document whose kind is Component and which
// declares a metadata.name, since those two are what make it addressable
// afterwards. The addressed entity is not excluded from its own target
// list: a component wiring a socket to another instance of its own type is
// authoring the product accepts, and removing it here would narrow the
// offer on a judgement this method is not entitled to make.
//
// # Ordering
//
// Sockets in the order the entity declares them, because that is the order
// their author wrote and the order every other surface reads a usesSchema
// in. Targets by kind, then name, then source file, through the one
// comparison every workspace-entity projection orders by — the traversal's
// own delivery order is not that order, so the sort is what makes the
// response bytes a function of the workspace content rather than of its
// file names.
func (s *Server) connectionCandidates(ctx context.Context, node, socket string) (ConnectionCandidatesResult, bool) {
	return connectionCandidatesIn(ctx, s.workspaceRoot, s.wsConfig, s.overlayDocs(), node, socket)
}

// ConnectionCandidates is the whole of connectionCandidates with the root and the
// workspace's configuration supplied rather than read off a session, so the
// session-bound method and a caller holding no session are one walk rather than
// two that agree today. cfg is read for the Releases it names, as the workspace
// documents are read for every surface; see WorkspaceEntityIndex.
//
// It takes no catalog lookup, and that is a property of the answer rather than an
// omission: the target set is the workspace-local Components, and the sockets are
// read from the addressed document's own usesSchema.
//
// What a caller without a session does not supply is the overlay of documents an
// editor is holding unsaved; with none, the answer is read from what is on disk,
// which is what such a caller can see anyway.
func ConnectionCandidates(ctx context.Context, root string, cfg workspaceconfig.Config, node, socket string) (ConnectionCandidatesResult, bool) {
	return connectionCandidatesIn(ctx, root, cfg, nil, node, socket)
}

// connectionCandidatesIn is the whole of ConnectionCandidates with the
// fresher-than-disk overlay supplied, which only a caller holding a session has.
func connectionCandidatesIn(ctx context.Context, root string, cfg workspaceconfig.Config, overlay []workspaceDoc, node, socket string) (ConnectionCandidatesResult, bool) {
	// A workspace may declare the requested name on more than one document
	// — two kinds may each carry it. Every match is collected and the total
	// order below picks between them, so the answer does not depend on
	// which one the traversal happened to reach first.
	var named []WorkspaceEntityRef
	namedSchemas := map[string]types.OrderedMap[types.UsesSchemaEntry]{}

	type componentDecl struct {
		entity WorkspaceEntityRef
		labels types.OrderedMap[string]
	}
	var components []componentDecl

	rangeWorkspaceDocsIn(ctx, root, cfg, overlay, func(doc workspaceDoc) bool {
		kind := scalarAtPathOnDoc(doc.node, []string{"kind"})
		if kind == "" {
			return true
		}
		name, _ := metadataNameOf(doc.node)
		if name == "" {
			return true
		}
		rel, inside := pathscope.Relative(root, doc.path)
		if !inside {
			// The parse cache holds whatever file the editor last sent,
			// which need not be a workspace file at all. An entity outside
			// the root is not workspace-local, so it is not a member of the
			// population this walk reports.
			return true
		}
		// The bare identity, with no origin beside it: the guard above has
		// already dropped every document outside the root, so every entity
		// this method reports is a workspace document with a workspace file.
		// A key whose value one response can only ever fill one way tells
		// that response's client nothing, so the sourced shape is reserved
		// for the responses whose entities genuinely come from two places.
		entity := WorkspaceEntityRef{Kind: kind, Name: name, File: rel}
		if name == node {
			named = append(named, entity)
			namedSchemas[rel] = usesSchemaOnDoc(doc.node)
		}
		if kind == "Component" {
			components = append(components, componentDecl{
				entity: entity,
				labels: orderedLabels(stringMapAtPathOnDoc(doc.node, []string{"metadata", "labels"})),
			})
		}
		return true
	})

	if len(named) == 0 {
		return ConnectionCandidatesResult{}, false
	}
	sortCandidates(named)
	subject := named[0]

	declared := namedSchemas[subject.File]
	result := ConnectionCandidatesResult{
		Node:                 subject.Name,
		AdHocSocketsAccepted: adHocSocketsAccepted(subject.Kind),
		Sockets:              []SocketCandidacy{},
	}

	appendSocket := func(name string, entry types.UsesSchemaEntry, declaration string) {
		candidacy := SocketCandidacy{
			Socket:      name,
			Declaration: declaration,
			Selector:    entry.Selector,
			Required:    entry.Required,
			Targets:     []WorkspaceEntityRef{},
		}
		// A socket the surface will not accept has no legal target through
		// it, so reporting the components its (absent) constraint would
		// admit would offer a connection the build refuses outright. The
		// declaration marker alone would leave a client to infer that, and
		// a target list is the part a client acts on.
		if declaration == types.SocketDeclarationDeclared || result.AdHocSocketsAccepted {
			for _, c := range components {
				match, err := selector.MatchString(entry.Selector, c.labels)
				if err != nil || !match {
					continue
				}
				candidacy.Targets = append(candidacy.Targets, c.entity)
			}
			sortCandidates(candidacy.Targets)
		}
		result.Sockets = append(result.Sockets, candidacy)
	}

	if socket != "" {
		entry, ok := declared.Get(socket)
		declaration := types.SocketDeclarationAdHoc
		if ok {
			declaration = types.SocketDeclarationDeclared
		}
		appendSocket(socket, entry, declaration)
		return result, true
	}

	for name, entry := range declared.All() {
		appendSocket(name, entry, types.SocketDeclarationDeclared)
	}
	return result, true
}

// adHocSocketsAccepted reports whether an entity of the given kind accepts
// a dependency socket its own usesSchema does not declare.
//
// The two answers are properties of two different rules rather than a
// preference. An Aggregate's own topology is the author's to write, so a
// Component's dependency surface accepts a socket the catalog entity never
// declared. An attachment participates in the topology the attached
// Feature declares rather than authoring one, so a Feature's usesSchema is
// the whole of what its sockets may be. A kind with no dependency surface
// has nothing for a socket to be accepted onto, which reads as the same
// value for a different reason and is why the switch is closed rather than
// defaulting to the Component answer.
func adHocSocketsAccepted(kind string) bool {
	return kind == "Component"
}

// usesSchemaOnDoc reads the dependency-socket declarations a document
// carries at spec.usesSchema, preserving the order they were authored in.
//
// A document declaring none, and one whose declaration is authored in a
// shape the entry cannot be read as, both yield an empty set. The second
// direction is deliberate and matches what a build does with the same
// declaration: an unreadable declaration is treated as declaring nothing
// rather than as declaring anything, so a mis-authored surface cannot
// widen what is offered.
func usesSchemaOnDoc(doc *yaml.Node) types.OrderedMap[types.UsesSchemaEntry] {
	var out types.OrderedMap[types.UsesSchemaEntry]
	n := nodeAtPathOnDoc(doc, []string{"spec", "usesSchema"})
	if n == nil || n.Kind != yaml.MappingNode {
		return out
	}
	if err := n.Decode(&out); err != nil {
		var empty types.OrderedMap[types.UsesSchemaEntry]
		return empty
	}
	return out
}
