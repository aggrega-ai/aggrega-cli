// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

import (
	"context"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/ir"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/yamlpos"
)

// ConnectionAtPositionResult is which connection's entry a position in an
// authored document falls inside, on every surface that carries the answer.
//
// Exactly one half is filled. GraphNode and Connection name the connection, in
// the terms the candidacy and container answers take it — the node by the id the
// compiled graph keys it under, and the connection by its socket, its alias and
// the document the position was read in — so the pair is handed on to those
// requests with nothing composed in between. NoConnectionReason says why no
// connection is named, and is empty wherever one is.
type ConnectionAtPositionResult struct {
	GraphNode          string                `json:"graphNode,omitempty"`
	Connection         *ConnectionCoordinate `json:"connection,omitempty"`
	NoConnectionReason NoConnectionReason    `json:"noConnectionReason,omitempty"`
}

// NoConnectionReason says why no connection is reported at a position, and is
// empty wherever one is.
//
// It is an answer rather than a refusal: the document was read and the position
// is one it carries, so "no connection is written here" is a fact about the
// document, and the reason is what lets a reader tell the cases apart. The
// values are in the descriptive register — lowercase words joined by dashes —
// and two of them take the spelling this package already gives the same fact:
// one the slot answer publishes, one the identifier a request addressing a
// connection records its refusal under.
type NoConnectionReason string

const (
	// NoConnectionReasonNoEntry reports a position inside no connection entry an
	// attachment is written in: neither a connection a component draws, written
	// under its alias in the document the component was authored in, nor a
	// per-edge entry the Release writes.
	NoConnectionReasonNoEntry NoConnectionReason = "no-connection-entry"

	// NoConnectionReasonFeatureCreated reports a position on the Release's entry
	// for a connection a Feature drew. It is the reason the slot answer gives for
	// the same connection, in the same words.
	NoConnectionReasonFeatureCreated NoConnectionReason = NoConnectionReason(NoSlotReasonFeatureCreatedConnection)

	// NoConnectionReasonUnknownConnection reports a position on an entry the
	// Release writes for a connection the compiled graph does not carry. It takes
	// the identifier the requests addressing a connection record their refusal of
	// such a connection under, so one condition keeps one spelling.
	NoConnectionReasonUnknownConnection NoConnectionReason = NoConnectionReason(ConnectionRefusedUnknownConnection)
)

// Sentence renders the reason as a clause a surface reading it out to a person
// embeds, and is the single home of that wording. An empty reason renders as the
// empty string.
func (r NoConnectionReason) Sentence() string {
	switch r {
	case NoConnectionReasonNoEntry:
		return "the position is on no connection entry an attachment is written in: an entry a component draws a " +
			"connection with, written under the connection's alias, or a per-edge entry in the Release"
	case NoConnectionReasonFeatureCreated:
		return NoSlotReasonFeatureCreatedConnection.Sentence()
	case NoConnectionReasonUnknownConnection:
		return "the Release's entry there is for a connection the compiled graph does not carry: nothing draws one " +
			"from that component through that socket under that alias"
	default:
		return ""
	}
}

// connectionEntry is one connection entry a document may carry: the connection it
// is written for, and the structural path to the entry inside the document.
type connectionEntry struct {
	node     string
	socket   string
	alias    string
	segments []string
}

// ConnectionAtPosition reports which connection's entry a position in an
// authored document falls inside, over an already-compiled graph: the graph node
// and the connection, or the reason none is reported.
//
// # It is the attachment container read the other way
//
// The entries a position is matched against are exactly the ones
// [ConnectionAttachmentContainer] names a container inside, each spelled by the
// rule that names that container, so an answer here is a connection whose
// container that request places inside the very entry the position fell in. In
// the Aggregate they are the connections a component draws, each at its entry in
// the document the component was authored in — which for a component a
// sub-aggregate contributes is that module's own document, not the Aggregate the
// Release names. In the Release they are the per-edge entries it writes, read
// through the one fold the rules over those entries read. Which of the two the
// named document is follows from the Release: its own file is the Release, and
// every other file is read for the Aggregate entries the graph records there.
//
// # What it reads, and what it never reads
//
// Where each entry sits is what the compiler recorded about the connection, and
// the document is consulted only to find how far that entry reaches in its
// bytes. Nothing here reads a key's name, a type or a label to decide whether
// something is a connection, so a key the compiler did not record as an entry is
// never taken for one however it is spelled.
//
// A position inside more than one entry is answered for the innermost of them —
// the one whose path is longest — and a position inside a connection's own
// attachments block is inside that connection's entry. A position on a socket's
// key, above the entries written under it, is inside none of them.
//
// The graph is the caller's rather than this function's, for the reason
// [AttachmentContainer] gives. The document is read the way the insertion-point
// request reads one: inside the workspace, links resolved, from disk.
func ConnectionAtPosition(root string, graph *types.IRGraph, release *types.Release, releasePath, file string,
	pos yamlpos.Position, enc yamlpos.PositionEncoding) (ConnectionAtPositionResult, error) {
	return connectionAtPositionIn(root, graph, release, releasePath, file, pos, enc, nil)
}

// connectionAtPositionIn is the whole of ConnectionAtPosition with the
// fresher-than-disk overlay supplied, which only a caller holding a session has.
func connectionAtPositionIn(root string, graph *types.IRGraph, release *types.Release, releasePath, file string,
	pos yamlpos.Position, enc yamlpos.PositionEncoding,
	overlay func(abs string) ([]byte, bool)) (ConnectionAtPositionResult, error) {

	rel, content, doc, err := workspaceDocumentIn(root, file, overlay)
	if err != nil {
		return ConnectionAtPositionResult{}, err
	}
	idx := yamlpos.BuildLineIndexWithEncoding(content, enc)

	releaseFile := ""
	if releasePath != "" {
		releaseFile = catalog.RelativeToWorkspace(root, releasePath)
	}
	document, entries := constraint.AuthoringDocumentAggregate, aggregateConnectionEntries(graph, rel)
	if releaseFile != "" && rel == releaseFile {
		document, entries = constraint.AuthoringDocumentRelease, releaseConnectionEntries(release, releaseFile)
	}

	held, found := innermostEntryAt(doc, idx, entries, pos)
	if !found {
		return ConnectionAtPositionResult{NoConnectionReason: NoConnectionReasonNoEntry}, nil
	}
	_, edge, refusal := graphConnection(graph, held.node, ConnectionCoordinate{Socket: held.socket, Alias: held.alias})
	switch {
	case refusal != ConnectionAnswered:
		return ConnectionAtPositionResult{NoConnectionReason: NoConnectionReasonUnknownConnection}, nil
	case edge.OriginFeature != "":
		return ConnectionAtPositionResult{NoConnectionReason: NoConnectionReasonFeatureCreated}, nil
	}
	return ConnectionAtPositionResult{
		GraphNode:  held.node,
		Connection: &ConnectionCoordinate{Socket: held.socket, Alias: held.alias, Document: document},
	}, nil
}

// aggregateConnectionEntries is every entry a component wrote one of its
// connections in, in the document named file: the connections the graph records
// as drawn by a component rather than by a Feature or the Release, on nodes the
// graph records as authored in that file, in the order the graph holds them.
//
// Each entry is the one [ConnectionAttachmentContainer] names an Aggregate
// container inside, through the same site, so the two cannot come to name
// different entries for one connection.
func aggregateConnectionEntries(graph *types.IRGraph, file string) []connectionEntry {
	if graph == nil {
		return nil
	}
	var out []connectionEntry
	for _, edge := range graph.Spec.Edges {
		if edge.OriginFeature != "" || edge.OriginRelease {
			continue
		}
		irNode, ok := graph.Spec.Nodes.Get(edge.Source)
		if !ok || irNode.SourceFile != file {
			continue
		}
		site := constraint.ConnectionAttachmentSiteInAggregate(ir.NodeInstanceOf(edge.Source, irNode),
			edge.Socket, edge.Alias, "")
		segments := constraint.ConnectionEntrySegments(site)
		if len(segments) == 0 {
			continue
		}
		out = append(out, connectionEntry{node: edge.Source, socket: edge.Socket, alias: edge.Alias,
			segments: segments})
	}
	return out
}

// releaseConnectionEntries is every per-edge entry the Release writes, read
// through the one fold the rules over those entries read, whether the entry
// draws its connection or configures one the Aggregate drew.
//
// Each entry is the one [ConnectionAttachmentContainer] names a Release
// container inside, through the same site.
func releaseConnectionEntries(release *types.Release, releaseFile string) []connectionEntry {
	var out []connectionEntry
	for _, entry := range resolver.ReleaseEdgeConfigurations(release, releaseFile) {
		segments := constraint.ConnectionEntrySegments(constraint.ConnectionAttachmentSiteInRelease(entry, ""))
		if len(segments) == 0 {
			continue
		}
		out = append(out, connectionEntry{node: entry.Configuration.Node, socket: entry.Configuration.Socket,
			alias: entry.Configuration.Alias, segments: segments})
	}
	return out
}

// innermostEntryAt is the entry whose range in the document holds the position,
// the innermost where more than one does — the one whose path is longest, and of
// two equally long the first in the order given.
//
// An entry the document does not carry has no range and holds nothing: the
// compiler may record a connection whose entry an unsaved edit has since removed,
// and a position cannot be inside an entry that is not there.
func innermostEntryAt(doc *yaml.Node, idx yamlpos.LineIndex, entries []connectionEntry,
	pos yamlpos.Position) (connectionEntry, bool) {
	var held connectionEntry
	found := false
	for _, entry := range entries {
		span, ok := yamlpos.EntryRange(doc, idx, entry.segments)
		if !ok || !span.Contains(pos) {
			continue
		}
		if found && len(entry.segments) <= len(held.segments) {
			continue
		}
		held, found = entry, true
	}
	return held, found
}

// connectionAtPositionParams is the aggrega/connectionAtPosition request shape.
//
// File names the document, as the insertion-point request names one: relative to
// the workspace root, by absolute path, or by its file address. Position is the
// place in it, in the encoding the session settled on, and it is a pointer so a
// request that left it out is refused rather than read as the document's first
// character. Release names the Release whose compiled graph records the
// connections, because a graph node and a connection exist only on one.
type connectionAtPositionParams struct {
	File     string            `json:"file"`
	Position *yamlpos.Position `json:"position"`
	Release  string            `json:"release"`
}

// dispatchConnectionAtPosition reports which connection's entry a position in
// an Aggregate or a Release falls inside, on the named Release's compiled graph.
//
// # Where the answer sits relative to the neighbouring methods
//
// It is the step before the candidacy and container answers for a connection:
// a caller holding a place in a document — a cursor, a selection — learns the
// graph node and the connection written there, and passes both to those methods
// unchanged. It answers which connection and nothing else; what may attach to it
// and where the text goes are theirs.
//
// # It reports and never writes
//
// Read-only in the strongest sense, as the requests beside it are: it composes
// nothing and writes nothing to any document.
//
// # What it resolves, and what it reads the document from
//
// Building the graph resolves the Release's closure exactly as the container
// request does, with nothing rendered and with the same lockfile precondition.
// The document is read from the daemon's parse cache first and from disk
// otherwise, as the insertion-point request reads one, and the position is
// measured in the session's negotiated encoding.
func (s *Server) dispatchConnectionAtPosition(ctx context.Context, req *jsonRPCRequest) error {
	var params connectionAtPositionParams
	if len(req.Params) > 0 {
		if err := decodeParams(req, &params); err != nil {
			return s.writeRefusal(ctx, req.ID, jsonRPCInvalidParams, "invalid params: ", err)
		}
	}
	if params.Position == nil {
		return s.writeError(ctx, req.ID, jsonRPCInvalidParams,
			"invalid params: name the place in the document with params.position — a line and a character, both "+
				"counted from zero — and the document with params.file")
	}
	if err := ctx.Err(); err != nil {
		return s.writeRefusal(ctx, req.ID, jsonRPCInternalError, "", err)
	}

	result, errEnvelope := s.connectionAtPosition(ctx, params.File, *params.Position, params.Release)
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

// connectionAtPosition builds the named Release's graph and reports the
// connection whose entry the position falls inside, over the session's own view
// of the workspace: the negotiated position encoding, and the parse cache as the
// fresher-than-disk overlay.
func (s *Server) connectionAtPosition(ctx context.Context, file string, pos yamlpos.Position,
	release string) (ConnectionAtPositionResult, *jsonRPCError) {
	// Nothing rendered: this reads where the graph records each connection's entry
	// and discards everything else, and the graph is byte-identical whether or not
	// the render ran.
	preview, errEnvelope := s.previewRelease(ctx, release, pipeline.Options{IncludeTrace: false, SkipRender: true})
	if errEnvelope != nil {
		return ConnectionAtPositionResult{}, errEnvelope
	}
	var rel *types.Release
	if preview.resolved != nil {
		rel = preview.resolved.Release
	}
	result, err := connectionAtPositionIn(s.workspaceRoot, preview.pipeline.Completed().IRGraph, rel,
		preview.releasePath, file, pos, s.positionEncoding(), s.documentOverlay())
	if err != nil {
		// Every refusal here says the request's document is not one a position
		// can be read in, which is what this code means: a retry changes nothing
		// until the file changes.
		return ConnectionAtPositionResult{}, &jsonRPCError{Code: jsonRPCInvalidParams, Message: err.Error(), cause: err}
	}
	return result, nil
}
