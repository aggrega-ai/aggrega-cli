// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package serve

import (
	"context"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/ir"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// AttachmentContainerResult is where a Feature attachment for one node of a
// compiled graph belongs in the document that node was authored in, on every
// surface that carries one.
//
// Node echoes the graph node the answer was computed for, so a caller batching
// several questions pairs each answer with its question without keeping its own
// order.
//
// File is the document the node was authored in, workspace-relative. It is part
// of the answer rather than something the caller already knows: a node a
// sub-aggregate contributes was written in that module's own document, not in
// the Aggregate the Release names, so an attachment for it belongs in a file the
// caller never named.
//
// Container is the structural path to the container the attachment goes inside,
// from that document's root — the shape the insertion-point request takes,
// element for element, so the pair is handed on without composing anything.
//
// It is a segment LIST rather than a joined path, and that is the point of the
// shape rather than a detail of it. A component's enclosing scope and a node's
// identifier are each composed with the same separator, so any joined form is
// ambiguous the moment a caller tries to read structure back out of it — which
// is exactly what a caller would have to do to build this itself. Handing over
// the segments leaves nothing to split.
type AttachmentContainerResult struct {
	Node      string   `json:"node"`
	File      string   `json:"file"`
	Container []string `json:"container"`
}

// AttachmentContainerRefusal names a reason no container is reported for a node,
// or is empty where one is.
//
// It is a value rather than an error identity, for the reason the neighbouring
// refusals here are: nothing halts, nothing is being compiled, and each of these
// refuses one REQUEST about one node. Carrying the reason as data lets every
// surface report the same refusal in its own channel, and lets
// [AttachmentContainerRefusal.Reason] be the single home of the wording.
//
// The values are in the descriptive register — lowercase words joined by dashes
// — like every other platform-defined identifier that is not a halting
// condition's short code.
type AttachmentContainerRefusal string

const (
	// AttachmentContainerReported reports that a container was found. It is the
	// zero value, so a caller that forgets to check reads a refusal rather than
	// an answer.
	AttachmentContainerReported AttachmentContainerRefusal = ""

	// AttachmentContainerRefusedUnknownNode reports a node identifier the graph
	// does not carry.
	AttachmentContainerRefusedUnknownNode AttachmentContainerRefusal = "unknown-graph-node"

	// AttachmentContainerRefusedNoSourceDocument reports a node the graph carries
	// no source document for. Where an attachment belongs is a fact about a
	// document, so a node with no document has no answer rather than an empty
	// one.
	AttachmentContainerRefusedNoSourceDocument AttachmentContainerRefusal = "no-source-document"

	// AttachmentContainerRefusedUnlocatableComponent reports a node whose own
	// block cannot be named in that document. Nothing is reported rather than a
	// container path assembled from what is known, because a path missing the
	// component it belongs under names a place the file does not contain.
	AttachmentContainerRefusedUnlocatableComponent AttachmentContainerRefusal = "component-block-not-locatable"
)

// Reason renders the refusal as a clause a surface embeds in whatever refusal
// channel it has. It is the single home of the wording, so two surfaces
// declining the same request cannot describe it differently.
//
// A reported container has no reason and renders as the empty string.
func (r AttachmentContainerRefusal) Reason() string {
	switch r {
	case AttachmentContainerRefusedUnknownNode:
		return "the compiled graph carries no node with that id; node ids are the graph's own keys, and a node " +
			"contributed by a sub-aggregate carries a qualified id"
	case AttachmentContainerRefusedNoSourceDocument:
		return "the graph records no document for that node, and where an attachment belongs is a fact about a " +
			"document"
	case AttachmentContainerRefusedUnlocatableComponent:
		return "the node's own block cannot be named in the document it was authored in, so there is no container " +
			"for an attachment to go inside"
	default:
		return ""
	}
}

// AttachmentContainer reports where a Feature attachment for the named node of
// an already-compiled graph belongs: which document, and which container inside
// it.
//
// # Why the compiler answers this rather than the caller composing it
//
// A caller holding a graph node holds two dotted strings and can tell neither
// apart from the other. The identifier of a node a sub-aggregate contributed
// carries that module's prefix ahead of the authored name, joined with a dot;
// the enclosing scope is a path of scope names, joined with a dot as well. So a
// caller reading either one back into segments is guessing which dots are
// structure and which are part of a name — and a wrong guess produces a path
// that still looks like a path, addressing a key nobody wrote. The compiler
// holds the rule that composed both, so the compiler splits them.
//
// # It reads structure and never meaning
//
// The container is derived from what the graph records about how the node was
// reached and from nothing else: the document it was authored in, the scope it
// sits in, whether that scope is one the author declared or one normalization
// generated, and the name the author typed. Nothing here reads a component's
// name, its type, or any label to decide where something ought to go. A scope
// the author declared is named level by level, because that is how the document
// spells it; a component the author wrote at the top level names the top-level
// shape, because the scope it arrives carrying is one no file contains.
//
// # It reports and never writes
//
// It answers where, and the caller composes and applies whatever edit follows.
// Nothing here decides WHAT may attach — a separate answer reports that — and
// nothing here writes.
//
// The graph is the caller's rather than this function's, and that split is the
// decision. Which preview a surface runs, and therefore what it promises about
// the lockfile, is a property of the operation that surface is performing. What
// must not differ is everything downstream of the graph: the node lookup and the
// container derivation are one implementation here, so two surfaces answering
// about one node cannot name two different places.
func AttachmentContainer(graph *types.IRGraph, node string) (AttachmentContainerResult, AttachmentContainerRefusal) {
	if graph == nil {
		return AttachmentContainerResult{}, AttachmentContainerRefusedUnknownNode
	}
	irNode, ok := graph.Spec.Nodes.Get(node)
	if !ok {
		return AttachmentContainerResult{}, AttachmentContainerRefusedUnknownNode
	}
	if irNode.SourceFile == "" {
		return AttachmentContainerResult{}, AttachmentContainerRefusedNoSourceDocument
	}

	// Named through the same derivation a diagnostic's locator is built from,
	// rather than assembled here. A locally spelled path would agree on the day
	// it was written, and the two would then be free to name different places
	// for one component with nothing comparing them.
	container := constraint.AttachmentContainerSegments(ir.NodeInstanceOf(node, irNode))
	if len(container) == 0 {
		return AttachmentContainerResult{}, AttachmentContainerRefusedUnlocatableComponent
	}

	return AttachmentContainerResult{
		Node:      node,
		File:      irNode.SourceFile,
		Container: container,
	}, AttachmentContainerReported
}

// attachmentContainerParams is the aggrega/attachmentContainer request shape.
//
// GraphNode names a node on the compiled graph of a Release, by the id that
// graph keys it under, and Release says which Release's graph to build. There is
// deliberately no name-addressed form: a workspace document's metadata.name
// names an entity rather than an instance of one, and the same entity may be
// instantiated at several places in one Aggregate, so a name has no single
// container to report.
//
// Connection names one connection the graph node draws, when the attachment is
// for that connection rather than for the node, and its document is required
// here: an attachment on a connection the Aggregate drew may be written in
// either document, so an answer for an unnamed one would be a guess. A pointer,
// so a request that did not send it is answered exactly as it was before the
// field existed.
type attachmentContainerParams struct {
	GraphNode  string                `json:"graphNode"`
	Release    string                `json:"release"`
	Connection *ConnectionCoordinate `json:"connection"`
}

// dispatchAttachmentContainer reports where a Feature attachment for a node on a
// Release's compiled graph belongs in the document that node was authored in.
//
// # Where the answer sits relative to the neighbouring methods
//
// It answers WHERE an attachment goes for a node, and nothing else: which
// Features may attach is a separate answer, and where in the file's bytes the
// text lands is another again. A caller writing an attachment onto a canvas
// takes all three — what may attach, which container it belongs in, and the
// position in that container's block — and this is the middle one, which is the
// only one of the three a caller cannot derive from what it already holds.
//
// # It reports and never writes
//
// Read-only in the strongest sense: it mutates nothing and writes nothing to any
// document. It composes no entry text and applies no edit, because the caller is
// the surface that owns the buffer and the undo history the edit lands in.
//
// # A connection the node draws
//
// A request naming a connection beside the node is answered for that connection
// instead: the connection entry's own `features` container, in the document the
// request names — see [ConnectionAttachmentContainer] for what each document
// answers and refuses.
//
// # What it resolves
//
// Building the graph resolves the Release's closure through the ordinary catalog
// cascade, exactly as the graph query does, and it carries that query's lockfile
// precondition for the same reason: an editor draws the result for a reader to
// reason about, and a graph built from bytes nothing vouches for would be drawn
// exactly like one that was checked.
func (s *Server) dispatchAttachmentContainer(ctx context.Context, req *jsonRPCRequest) error {
	var params attachmentContainerParams
	if len(req.Params) > 0 {
		if err := decodeParams(req, &params); err != nil {
			return s.writeRefusal(ctx, req.ID, jsonRPCInvalidParams, "invalid params: ", err)
		}
	}
	if params.GraphNode == "" {
		return s.writeError(ctx, req.ID, jsonRPCInvalidParams,
			"invalid params: address a node with params.graphNode, and name the Release whose graph carries it "+
				"with params.release")
	}
	if params.Connection != nil {
		if field, reason := params.Connection.Fault(true); field != "" {
			return s.writeError(ctx, req.ID, jsonRPCInvalidParams,
				"invalid params: params.connection."+field+" "+reason)
		}
	}
	if err := ctx.Err(); err != nil {
		return s.writeRefusal(ctx, req.ID, jsonRPCInternalError, "", err)
	}

	if params.Connection != nil {
		result, errEnvelope := s.connectionAttachmentContainerForGraphNode(ctx, params.GraphNode, params.Release,
			*params.Connection)
		if ctx.Err() != nil {
			// Cancelled mid-pipeline (shutdown). Drop the response; the dispatcher
			// exits via the drain timer.
			return nil
		}
		if errEnvelope != nil {
			return s.writeErrorEnvelope(ctx, req.ID, errEnvelope)
		}
		return s.writeResult(ctx, req.ID, result)
	}

	result, errEnvelope := s.attachmentContainerForGraphNode(ctx, params.GraphNode, params.Release)
	if ctx.Err() != nil {
		// Cancelled mid-pipeline (shutdown). Drop the response; the dispatcher
		// exits via the drain timer.
		return nil
	}
	if errEnvelope != nil {
		return s.writeErrorEnvelope(ctx, req.ID, errEnvelope)
	}
	return s.writeResult(ctx, req.ID, result)
}

// attachmentContainerForGraphNode builds the named Release's graph and reports
// where an attachment for one of its nodes belongs.
func (s *Server) attachmentContainerForGraphNode(ctx context.Context, node, release string) (AttachmentContainerResult, *jsonRPCError) {
	// Nothing rendered: this reads how the node was reached and discards
	// everything else, and the graph is byte-identical whether or not the render
	// ran. Skipping it keeps the answer from loading a rendering plugin to
	// report a container path.
	preview, errEnvelope := s.previewRelease(ctx, release, pipeline.Options{IncludeTrace: false, SkipRender: true})
	if errEnvelope != nil {
		return AttachmentContainerResult{}, errEnvelope
	}

	// The graph comes off the finished payload: the envelope above answered every
	// refusal, so the run this reads the reach from is one that ended.
	result, refusal := AttachmentContainer(preview.pipeline.Completed().IRGraph, node)
	if refusal != AttachmentContainerReported {
		// Named the graph rather than the workspace, because that is what the
		// caller addressed.
		return AttachmentContainerResult{}, &jsonRPCError{
			Code: errEntityNotResolvable,
			Message: "no container is reported for " + node + " on the compiled graph of " + release + ": " +
				refusal.Reason(),
		}
	}
	return result, nil
}
