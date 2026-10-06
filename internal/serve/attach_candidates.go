// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

import (
	"context"
	"sort"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/pathscope"
	"github.com/aggrega-ai/aggrega-cli/pkg/selector"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// WorkspaceEntityRef is the identity one entity is addressed by on the
// wire: kind, name and source file, the three fields that identify it.
//
// The identity is deliberately minimal. Everything else a palette wants to
// show about an entity is already reported by aggrega/catalogIndex for the
// same kind/name/file triple, so repeating it here would put a second copy
// of that description on the wire, and the two copies would be free to
// disagree. A client joins the two responses instead.
//
// Kind is carried even where one response's candidates are all of a single
// kind today, so the triple a client joins on has the same shape in every
// response that carries one.
//
// The triple has exactly one declaration, and a response that must say more
// about an entity embeds this type rather than restating its fields — see
// SourcedEntityRef. That is what keeps the field names a client joins on
// identical by construction rather than by agreement, and it is why a
// response reports this shape unadorned wherever it has nothing to add: the
// entities it names then arrive under the three keys and no more.
// sortCandidates orders any reference shape.
type WorkspaceEntityRef struct {
	Kind string `json:"kind"`
	Name string `json:"name"`
	File string `json:"file"`
}

// SourcedEntityRef is the entity identity together with where the daemon
// read the entity from — the shape a response reports when its entities can
// come from more than one place.
//
// It embeds the identity rather than restating it, so its wire shape is the
// triple's three keys and origin in one flat object, and a client joining a
// sourced reference against a bare one joins on the same field names by
// construction. A response whose entities all come from a single place keeps
// reporting the bare identity, so origin travels only where it distinguishes
// something.
//
// Origin names where the entity was read from, and it is what keeps the
// identity honest once a response can carry entities from two places. A
// workspace document reports its workspace-relative path in File; an entity
// resolved from the configured catalog directories reports an empty File,
// because the only path it has is an absolute host path that would mean
// nothing to a client and would disclose the daemon machine's layout. An
// empty File alone could not carry that meaning — it would be
// indistinguishable from a build that failed to fill the field in — so the
// origin says it explicitly rather than leaving it to be inferred.
type SourcedEntityRef struct {
	WorkspaceEntityRef
	Origin string `json:"origin"`
}

// entityIdentity is answered by every reference shape: the bare identity
// answers with itself, and a shape embedding it promotes that answer. One
// ordering therefore serves every candidate list whatever shape carries it,
// rather than a second comparison that would be free to disagree with the
// first.
type entityIdentity interface {
	identity() WorkspaceEntityRef
}

func (r WorkspaceEntityRef) identity() WorkspaceEntityRef { return r }

// AttachCandidate is one Feature in the aggrega/attachCandidates
// response: a Feature whose declared applicability the addressed entity's
// labels satisfy. It carries an origin because the candidates come from two
// places — the workspace documents and the configured catalog directories —
// so a client cannot otherwise tell which of them it is looking at.
type AttachCandidate = SourcedEntityRef

// Node-label provenance values, reported as labelSource so a client knows
// which of three genuinely different label sets a match was evaluated
// against.
//
// The distinction is not cosmetic and it must not be smoothed away. A
// document's declared labels are what a read over source can see. A graph
// node's are the finalised set the build itself evaluates applicability
// against: the labels the resolved component type declares, after any
// label-transform attachment the workspace configuration carries has added
// to, changed or removed them. An answer computed over the second is a
// statement about what the build will do; one computed over the first is
// not, and a response that looked identical for both would invite a client
// to read the weaker answer as the stronger one.
//
// The third says the daemon read no labels at all. Its strength is the
// caller's to know, which is exactly why it is reported rather than left to
// be inferred from which parameter was sent.
const (
	// NodeLabelsDeclared marks a match evaluated against the labels a workspace
	// document declares at metadata.labels.
	NodeLabelsDeclared = "declared"
	// NodeLabelsResolved marks a match evaluated against the finalised labels a
	// compiled graph node carries.
	NodeLabelsResolved = "resolved"
	// NodeLabelsSupplied marks a match evaluated against a label set the request
	// carried, which the daemon read from no document and cannot vouch for.
	NodeLabelsSupplied = "supplied"
)

// attachCandidatesParams is the aggrega/attachCandidates request shape.
//
// The three coordinates address three different things and exactly one is
// given.
//
// Node names a workspace document by its metadata.name — the same name
// aggrega/catalogIndex reports. GraphNode names a node in the compiled graph
// of a Release, by the id that graph keys it under, and Release says which
// Release's graph to build. Labels carries a label set outright, and names
// nothing at all.
//
// The first two are separate parameters because their identifier spaces
// OVERLAP. A graph node id is constrained to a pattern; a document's
// metadata.name carries no pattern at all, so a name legal as one is
// frequently legal as the other and a single parameter carrying both would be
// ambiguous by construction. Deciding which space to search by inspecting the
// value would be the compiler guessing meaning out of a string, which is
// exactly what it does not do anywhere else. So the caller says which question
// it is asking, and the daemon answers that one.
//
// Labels is the shape underneath both of them. The match this method performs
// is a function of a label set and the Feature declarations, and of nothing
// else, so a caller that already holds a label set has no reason to name
// something for the daemon to look the labels back up from. That matters most
// for a caller holding a rendered graph: the labels it holds are the finalised
// ones, which are better data than either name-addressed path can reach on its
// own, and passing them costs no round trip and no graph rebuild.
type attachCandidatesParams struct {
	Node      string            `json:"node"`
	GraphNode string            `json:"graphNode"`
	Release   string            `json:"release"`
	Labels    map[string]string `json:"labels"`
}

// coordinates names the addressing parameters the request carried, in the
// order the shape declares them, so a refusal can say which ones arrived
// rather than describing the shape back at a client that already has it.
//
// A label set counts as given when the key is present at all, the empty
// object included. An empty label set is a subject — a node carrying no
// labels, which every Feature declaring no requirement admits — so reading it
// as "no coordinate" would refuse a legitimate question. Absent and null both
// decode to a nil map and neither is a coordinate; that is the distinction
// this rests on, and it is the reason the field is a map rather than a
// count or a flag.
func (p attachCandidatesParams) coordinates() []string {
	var given []string
	if p.Node != "" {
		given = append(given, "params.node")
	}
	if p.GraphNode != "" {
		given = append(given, "params.graphNode")
	}
	if p.Labels != nil {
		given = append(given, "params.labels")
	}
	return given
}

// AttachCandidatesResult is the attach-candidacy answer, on every surface that
// carries one.
//
// NodeLabels carries the label pairs the match was actually evaluated
// against, so the answer is auditable from the response alone and a
// client never has to read the entity's labels itself to explain the
// result. A client reading them separately is how its idea of a node's
// labels and the daemon's come apart.
//
// Both collections are always present on the wire — an empty object and
// an empty array rather than null — so a client iterates without a nil
// check.
//
// LabelSource names which of the three label sets NodeLabels holds, so the
// strength of the answer travels with it rather than having to be inferred
// from which request parameter was sent.
//
// Node is the coordinate the answer was computed for, and it is EMPTY when
// the request supplied a label set, because such a request addressed no node
// and there is nothing to repeat back. An empty value there is not a field
// the build failed to fill: LabelSource says which case it is, the same way
// origin says why a catalog-resolved entity reports no file. The field stays
// present rather than being omitted so the response shape is the same
// whichever coordinate was sent.
type AttachCandidatesResult struct {
	Node        string            `json:"node"`
	NodeLabels  map[string]string `json:"nodeLabels"`
	LabelSource string            `json:"labelSource"`
	Candidates  []AttachCandidate `json:"candidates"`
}

// dispatchAttachCandidates reports which of the workspace's Features
// declare an applicability that the named entity's labels satisfy — the
// set an editor may offer as drop targets when a Feature is dragged onto
// a node on a canvas.
//
// What it reports is a label-matching result and nothing more. The
// Features it names are the ones whose declared applicability the node's
// labels satisfy; it does not rule on whether attaching one is a good
// idea, does not read any meaning out of a label key or value, and does
// not infer anything from a name. A label is opaque metadata here, as it
// is everywhere else that consumes one.
//
// Read-only in every mode: it mutates nothing and writes nothing.
//
// What it RESOLVES differs by coordinate, and the difference is stated
// rather than smoothed over. Addressing a workspace document resolves
// nothing and reaches no registry or network: the candidates come from
// the workspace walk and from the catalog directory index, both reads of
// content already on disk. Supplying a label set resolves less still — it
// looks nothing up at all. Addressing a graph node builds the Release's
// graph through the same computation the graph query runs, so it resolves
// that Release's closure through the ordinary catalog cascade — which
// serves from the local directories and the artifact cache
// first, and can reach the configured registry for an entity neither
// carries. A surface that claimed otherwise for every coordinate would be
// describing only the cheaper one.
//
// A request naming an entity the workspace does not declare is an error
// rather than an empty candidate list — those two answers mean different
// things to a client, and collapsing them into an empty array would report
// a typo as a node with nothing to offer. A request that names nothing
// because it supplied a label set has no such hazard and is answered: there
// is no name to have typed wrongly, so an empty list there means what it
// says.
//
// # Addressing a node on a compiled graph
//
// A canvas draws the compiled graph, whose nodes are keyed by a topological
// id rather than by any document's name, so it addresses this method with
// graphNode and the release whose graph it is drawing. That path builds the
// Release's graph through the same computation the graph query runs, and
// therefore carries the same lockfile precondition — but only when it is
// used. A request addressing a workspace document by name needs no lockfile
// and is answered exactly as it was before this path existed, and a request
// supplying a label set builds no graph either.
func (s *Server) dispatchAttachCandidates(ctx context.Context, req *jsonRPCRequest) error {
	var params attachCandidatesParams
	if len(req.Params) > 0 {
		if err := decodeParams(req, &params); err != nil {
			return s.writeRefusal(ctx, req.ID, jsonRPCInvalidParams, "invalid params: ", err)
		}
	}
	// The coordinates are counted rather than compared in pairs, and they are
	// refused together rather than silently ranked. A client that sent two has
	// a bug whichever one it meant, and picking one for it would answer a
	// question it did not ask — on a surface where two of the spaces overlap,
	// so the wrong pick can still produce a plausible-looking answer. Counting
	// is what keeps that true as coordinates are added: a chain of pairwise
	// comparisons admits whichever pair nobody thought to write, and it grows
	// quadratically while the thing being asserted stays "exactly one".
	given := params.coordinates()
	switch {
	case len(given) == 0:
		return s.writeError(ctx, req.ID, jsonRPCInvalidParams,
			"invalid params: address a subject with exactly one of params.node (a workspace entity), "+
				"params.graphNode with params.release (a node on a compiled graph), or params.labels (a label set)")
	case len(given) > 1:
		return s.writeError(ctx, req.ID, jsonRPCInvalidParams,
			"invalid params: "+strings.Join(given, ", ")+" address different things; send exactly one")
	case params.Release != "" && params.GraphNode == "":
		return s.writeError(ctx, req.ID, jsonRPCInvalidParams,
			"invalid params: params.release applies to params.graphNode; a request sending "+given[0]+" builds no graph")
	}
	if err := ctx.Err(); err != nil {
		return s.writeRefusal(ctx, req.ID, jsonRPCInternalError, "", err)
	}

	if params.Labels != nil {
		result, answered := s.attachCandidatesForLabels(ctx, params.Labels)
		if !answered {
			// Cancelled mid-traversal (shutdown). Drop the response; the
			// dispatcher exits via the drain timer.
			return nil
		}
		return s.writeResult(ctx, req.ID, result)
	}

	if params.GraphNode != "" {
		result, errEnvelope := s.attachCandidatesForGraphNode(ctx, params.GraphNode, params.Release)
		if ctx.Err() != nil {
			// Cancelled mid-pipeline (shutdown). Drop the response; the
			// dispatcher exits via the drain timer.
			return nil
		}
		if errEnvelope != nil {
			return s.writeErrorEnvelope(ctx, req.ID, errEnvelope)
		}
		return s.writeResult(ctx, req.ID, result)
	}

	result, found := s.attachCandidates(ctx, params.Node)
	if !found {
		return s.writeError(ctx, req.ID, errEntityNotResolvable,
			"no workspace entity is named "+params.Node)
	}
	return s.writeResult(ctx, req.ID, result)
}

// attachCandidatesForGraphNode reports which Features may attach to a node on
// the compiled graph of the named Release.
//
// # Where the node set and the node's labels come from
//
// Both come from the Release's compiled graph, built through the one preview
// computation the graph query also runs. Nothing here reconstructs a node id
// or re-derives a label set: the ids are the graph's own keys, so a node
// contributed by a sub-aggregate is addressed by exactly the qualified id the
// graph published for it, and the labels are the node's finalised set.
//
// That is what makes this answer stronger than the name-addressed one rather
// than merely differently spelled. A document's declared labels are what a
// read over source can see; a node's finalised labels are what the build
// evaluates applicability against, so a match computed here is a statement
// about what a build would decide. The response says which of the two it
// used, so the difference reaches the client instead of being smoothed over.
//
// # Why it takes the lockfile gate and the name-addressed path does not
//
// Building the graph means resolving what the Release reaches, and an editor
// draws the result for a reader to reason about — a graph built from bytes
// nothing vouches for would be drawn exactly like one that was checked. So
// this path refuses when the lockfile is absent, naming what to run, the same
// way the graph query does. The gate rides on the computation rather than on
// the method: a request that builds no graph crosses no such precondition,
// which is why addressing a workspace document by name still answers on a
// workspace nobody has prepared yet.
func (s *Server) attachCandidatesForGraphNode(ctx context.Context, node, release string) (AttachCandidatesResult, *jsonRPCError) {
	// Nothing rendered: this reads the node's labels out of the graph and
	// discards everything else, and the graph is byte-identical whether or
	// not the render ran. Skipping it keeps the answer from loading a
	// rendering plugin to compute a label match.
	preview, errEnvelope := s.previewRelease(ctx, release, pipeline.Options{IncludeTrace: false, SkipRender: true})
	if errEnvelope != nil {
		return AttachCandidatesResult{}, errEnvelope
	}

	// The graph comes off the finished payload: the envelope above answered every
	// refusal, so the run this reads labels from is one that ended.
	//
	// The candidates are drawn in this Release's terms: the catalog-directory
	// Features from the lookup the graph was built from — the one over this
	// Release's own catalog stack — and the workspace documents from those this
	// Release's reach holds, so a Feature the Release could not reach is never
	// offered for a node of its graph. The parameter resolved above, inside the
	// preview, and the Release it names was read there, so asking for its terms
	// here cannot refuse.
	terms, termsErr := s.termsFor(release)
	if termsErr != nil {
		return AttachCandidatesResult{}, termsErr
	}
	result, found := attachCandidatesForGraphNodeIn(ctx, s.workspaceRoot, s.wsConfig, terms.lookup, terms.reach, s.overlayDocs(),
		preview.pipeline.Completed().IRGraph, node)
	if !found {
		// Named the graph rather than the workspace, because that is what the
		// caller addressed. A client told "no workspace entity is named X"
		// after asking about a graph node would look for a document it never
		// meant.
		return AttachCandidatesResult{}, &jsonRPCError{
			Code:    errEntityNotResolvable,
			Message: "the compiled graph of " + release + " has no node with id " + node,
		}
	}
	return result, nil
}

// AttachCandidatesForGraphNode reports which Features may attach to the named
// node of an already-compiled graph. The second return reports whether the graph
// carries a node with that id at all.
//
// The graph is the caller's rather than this function's, and that split is the
// decision. Which preview a surface runs — and therefore what it promises about
// the lockfile — is a property of the operation that surface is performing, so
// each surface builds the graph through the operation shaped for its own
// contract. What must NOT differ is everything downstream of the graph: the node
// lookup, the label read and the match are one implementation here, so two
// surfaces answering about one node cannot offer two different Feature sets.
//
// Nothing reconstructs a node id or re-derives a label set. The ids are the
// graph's own keys, so a node contributed by a sub-aggregate is addressed by
// exactly the qualified id the graph published for it, and the labels are that
// node's finalised set — the set a build evaluates applicability against, which
// is what makes this answer a statement about what a build would decide rather
// than merely a differently spelled one. The result says so through its label
// source.
//
// The candidates are drawn in the terms of the Release whose graph it is: view is
// the lookup over that Release's catalog search stack, the one the graph was built
// through, and reach is that Release's reach, so of the workspace documents only
// those the Release's answer holds are candidates — a Feature kept under a catalog
// directory only another Release reads is never offered for a node of this graph.
// The zero reach holds every document.
//
// What a caller without a session does not supply is the overlay of documents an
// editor is holding unsaved; with none, the candidate population is read from
// what is on disk, which is what such a caller can see anyway.
//
// cfg is the workspace's configuration, read for the Releases it names, as the
// workspace documents are read for every surface; see WorkspaceEntityIndex.
func AttachCandidatesForGraphNode(ctx context.Context, root string, cfg workspaceconfig.Config, view *catalog.LazyLookup,
	reach workspaceconfig.ReleaseReach, graph *types.IRGraph, node string) (AttachCandidatesResult, bool) {
	return attachCandidatesForGraphNodeIn(ctx, root, cfg, view, reach, nil, graph, node)
}

// attachCandidatesForGraphNodeIn is the whole of AttachCandidatesForGraphNode
// with the fresher-than-disk overlay supplied, which only a caller holding a
// session has.
func attachCandidatesForGraphNodeIn(ctx context.Context, root string, cfg workspaceconfig.Config, view *catalog.LazyLookup,
	reach workspaceconfig.ReleaseReach, overlay []workspaceDoc, graph *types.IRGraph, node string) (AttachCandidatesResult, bool) {
	if graph == nil {
		return AttachCandidatesResult{}, false
	}
	irNode, ok := graph.Spec.Nodes.Get(node)
	if !ok {
		return AttachCandidatesResult{}, false
	}

	labels := map[string]string{}
	for k, v := range irNode.Labels.All() {
		labels[k] = v
	}

	return AttachCandidatesResult{
		Node:        node,
		NodeLabels:  labels,
		LabelSource: NodeLabelsResolved,
		Candidates:  matchingFeaturesIn(ctx, root, cfg, view, reach, overlay, irNode.Labels),
	}, true
}

// attachCandidatesForLabels reports which Features may attach to a node
// carrying the supplied label set. The second return is false when the
// traversal was interrupted, in which case there is no answer to report.
//
// # It resolves nothing, and that is the point of the shape
//
// The match is a function of the label set and the Feature declarations, and
// of nothing else — the two name-addressed paths differ from this one only in
// where they obtain a label set before performing the identical match. So a
// caller that already holds one has nothing to name, nothing to be refused
// for mistyping, and no lookup to wait on. A caller holding a rendered graph
// holds the finalised labels, which is the stronger of the two sets either
// name-addressed path can reach, so this answers as strongly as the graph-node
// coordinate does without rebuilding the graph to do it.
//
// # Where the responsibility for the label set goes
//
// The name-addressed path reads a document's labels through the same helper
// aggrega/catalogIndex reports them through, and that shared read is what
// keeps a palette built from the index and a drop filter built from that path
// from disagreeing about what labels a node carries. This path has no such
// guarantee to offer, because the daemon reads nothing: the label set is
// whatever the caller sent, and the caller is the only party that knows where
// it came from.
//
// So the guarantee moves to the client, and what the daemon does instead is
// make the move legible rather than pretend it did not happen. Two things
// carry it. The response reports labelSource "supplied", so an answer computed
// over a caller's own label set can never be read as one the daemon vouched
// for. And the response echoes the label set the match was actually evaluated
// against, so a client can compare what it sent against what was used and
// audit the answer from the response alone.
//
// What that leaves is a rule for the client rather than a mechanism in the
// daemon: a client passing back a label set the daemon itself reported —
// a graph node's labels from aggrega/irGraph, or an entity's from
// aggrega/catalogIndex — agrees with the daemon by construction, because the
// set never left the daemon's own answer. A client that assembles a label set
// from somewhere else is asking a hypothetical question, which is a legitimate
// thing to ask and is answered honestly; it is simply not a statement about
// any node the workspace holds, and labelSource is what says so.
//
// # An empty answer is honest here, and elsewhere it would not be
//
// A label set no Feature admits comes back as an empty candidate list rather
// than a refusal. The name-addressed paths refuse an unknown coordinate
// precisely because an empty list would report a typo as a node with nothing
// to offer; there is no name here to have typed wrongly, so the empty list
// carries no such ambiguity. A malformed request is still refused, before any
// of this runs.
func (s *Server) attachCandidatesForLabels(ctx context.Context, labels map[string]string) (AttachCandidatesResult, bool) {
	return attachCandidatesForLabelsIn(ctx, s.workspaceRoot, s.wsConfig, s.lazyCatalog, s.overlayDocs(), labels)
}

// AttachCandidatesForLabels is the whole of attachCandidatesForLabels with the
// root, the workspace's configuration and the catalog lookup supplied rather than
// read off a session. cfg is read for the Releases it names, as
// AttachCandidatesForGraphNode reads it.
//
// It is the shape underneath the two name-addressed coordinates: both obtain a
// label set and then perform this identical match, so a caller that already holds
// one has nothing to name, nothing to be refused for mistyping, and no lookup to
// wait on. That makes it the coordinate needing no coordinate — the one a caller
// holding labels reaches for first — rather than an advanced form of the others.
func AttachCandidatesForLabels(ctx context.Context, root string, cfg workspaceconfig.Config, view *catalog.LazyLookup,
	labels map[string]string) (AttachCandidatesResult, bool) {
	return attachCandidatesForLabelsIn(ctx, root, cfg, view, nil, labels)
}

// attachCandidatesForLabelsIn is the whole of AttachCandidatesForLabels with the
// fresher-than-disk overlay supplied, which only a caller holding a session has.
func attachCandidatesForLabelsIn(ctx context.Context, root string, cfg workspaceconfig.Config, view *catalog.LazyLookup,
	overlay []workspaceDoc, labels map[string]string) (AttachCandidatesResult, bool) {
	// A label set names no Release, so every workspace document is in its terms.
	candidates := matchingFeaturesIn(ctx, root, cfg, view, workspaceconfig.ReleaseReach{}, overlay, orderedLabels(labels))
	if ctx.Err() != nil {
		// The traversal yields nothing under a cancelled context, and on this
		// path that would surface as a SUCCESSFUL response carrying an empty
		// candidate list — a definite answer no completed read reached. The
		// name-addressed path degrades into a refusal instead, because an
		// empty traversal also loses the subject; this one has no subject to
		// lose, so the check has to be here.
		return AttachCandidatesResult{}, false
	}
	return AttachCandidatesResult{
		Node:        "",
		NodeLabels:  labels,
		LabelSource: NodeLabelsSupplied,
		Candidates:  candidates,
	}, true
}

// attachCandidates projects the workspace document traversal down to the
// Features whose declared applicability the named entity's labels
// satisfy, in a deterministic order. The second return reports whether
// the workspace declares the named entity at all.
//
// One traversal serves both halves: the addressed entity's labels and
// every Feature's applicability declaration are collected in a single
// pass, so the two are read from one view of the workspace rather than
// from two.
//
// # Which labels the match is evaluated against
//
// The labels are the ones the addressed document declares at
// metadata.labels, read through the same helper aggrega/catalogIndex
// reports them through. That shared read is what keeps a palette built
// from the index and a drop filter built from this method from disagreeing
// about what labels a node carries.
//
// Those are the labels the document declares, which is what a read-only
// query over source can see. A build evaluates a Feature's applicability
// against the finalised label set of the graph node it produces, and that
// set is reached differently: a node takes the labels of the component
// type it resolves to rather than of a document sharing its name, and a
// label-transform attachment the workspace configuration carries may then
// add to, change or remove them. So this reports the match over one
// document's declared labels; it is not a prediction of what a build will
// decide.
//
// # Which Features are candidates
//
// Every Feature the daemon can see, which is two populations rather than
// one: the workspace-local documents whose kind is Feature and which declare
// a metadata.name, and the Features the configured catalog directories
// declare. The second population is the one an ordinary workspace actually
// consumes — a workspace that keeps its Features in a catalog rather than in
// its own tree has none of the first kind at all, and reporting only the
// first would answer such a workspace with an empty list, which reads as
// "nothing may attach here" rather than as "this surface cannot see them".
//
// A Feature declaring neither applicability surface is a candidate for every
// node: an absent surface declares no requirement, which is the same "no
// constraint declared" contract every selector surface carries.
//
// A Feature whose applicability declaration cannot be parsed is not a
// candidate. That direction matches what a build does with the same
// declaration — it refuses rather than admits — and it is the only safe
// one: treating an unparseable selector as satisfied would let a typo
// widen what the editor offers.
//
// # Ordering
//
// By kind, then name, then source file, through the one comparison every
// workspace-entity projection orders by. The traversal's own delivery
// order is not that order, so the sort is what makes the response bytes a
// function of the workspace content rather than of its file names.
func (s *Server) attachCandidates(ctx context.Context, node string) (AttachCandidatesResult, bool) {
	return attachCandidatesForNodeIn(ctx, s.workspaceRoot, s.wsConfig, s.lazyCatalog, s.overlayDocs(), node)
}

// AttachCandidatesForNode is the whole of attachCandidates with the root, the
// workspace's configuration and the catalog lookup supplied rather than read off
// a session. cfg is read for the Releases it names, as
// AttachCandidatesForGraphNode reads it.
//
// It answers about a workspace document addressed by its metadata.name, over the
// labels that document declares — which is what a read over source can see, and
// is weaker than what a build evaluates. The result says so through its label
// source, so the difference reaches the caller instead of being smoothed over.
func AttachCandidatesForNode(ctx context.Context, root string, cfg workspaceconfig.Config, view *catalog.LazyLookup,
	node string) (AttachCandidatesResult, bool) {
	return attachCandidatesForNodeIn(ctx, root, cfg, view, nil, node)
}

// attachCandidatesForNodeIn is the whole of AttachCandidatesForNode with the
// fresher-than-disk overlay supplied, which only a caller holding a session has.
func attachCandidatesForNodeIn(ctx context.Context, root string, cfg workspaceconfig.Config, view *catalog.LazyLookup,
	overlay []workspaceDoc, node string) (AttachCandidatesResult, bool) {
	// A workspace may declare the requested name on more than one
	// document — two kinds may each carry it. Every match is collected and
	// the total order below picks between them, so the answer does not
	// depend on which one the traversal happened to reach first.
	var named []AttachCandidate
	namedLabels := map[string]map[string]string{}

	// A workspace document named by its metadata.name names no Release, so every
	// workspace document is in its terms.
	features, workspaceNames := workspaceFeatureDeclsIn(ctx, root, cfg, overlay, workspaceconfig.ReleaseReach{}, func(doc workspaceDoc, entity AttachCandidate) {
		if entity.Name == node {
			named = append(named, entity)
			namedLabels[entity.File] = stringMapAtPathOnDoc(doc.node, []string{"metadata", "labels"})
		}
	})

	if len(named) == 0 {
		return AttachCandidatesResult{}, false
	}
	sortCandidates(named)
	subject := named[0]

	labels := namedLabels[subject.File]
	if labels == nil {
		labels = map[string]string{}
	}

	features = append(features, catalogFeatureDeclsFrom(view, workspaceNames)...)

	return AttachCandidatesResult{
		Node:        subject.Name,
		NodeLabels:  labels,
		LabelSource: NodeLabelsDeclared,
		Candidates:  matchFeatures(features, orderedLabels(labels)),
	}, true
}

// featureDecl is one Feature's applicability declaration, reduced to the two
// surfaces a match evaluates and a flag saying whether they could be read at
// all.
//
// The flag exists because "declares no requirement" and "declares something
// unreadable" are opposite answers that must not collapse into each other:
// the first admits every node, the second admits none.
type featureDecl struct {
	entity    AttachCandidate
	appliesTo string
	anyOf     []types.SelectorTerm
	parsed    bool
}

// workspaceFeatureDecls walks the workspace once and returns the
// applicability declaration of every workspace-local Feature, plus the set of
// names those Features occupy.
//
// The visitor runs for every addressable workspace document, not only the
// Features, so a caller that also needs something about the addressed entity
// reads it in this same pass. That is what keeps one traversal serving a
// whole answer: a second walk could observe a different state of the tree,
// and the two halves of the answer would then describe two workspaces.
//
// The returned name set is what keeps a Feature from being reported twice
// when the workspace root is itself one of the configured catalog
// directories, which is a common enough layout that the collision is the
// normal case rather than an edge one.
func (s *Server) workspaceFeatureDecls(ctx context.Context, visit func(workspaceDoc, AttachCandidate)) ([]featureDecl, map[string]bool) {
	return workspaceFeatureDeclsIn(ctx, s.workspaceRoot, s.wsConfig, s.overlayDocs(), workspaceconfig.ReleaseReach{}, visit)
}

// workspaceFeatureDeclsIn is the whole of workspaceFeatureDecls with the root, the
// workspace's configuration, the fresher-than-disk overlay and the reach supplied
// rather than read off a session, so the session-bound form above and the
// sessionless entry points below are one walk rather than two that agree today.
//
// Only the documents reach holds are visited and reported, so an answer given in a
// Release's terms neither offers nor counts toward its name set a Feature kept
// under a catalog directory only another Release reads. Leaving such a name out of
// the set is what lets the Release's own catalog lookup still answer for it.
func workspaceFeatureDeclsIn(ctx context.Context, root string, cfg workspaceconfig.Config, overlay []workspaceDoc, reach workspaceconfig.ReleaseReach,
	visit func(workspaceDoc, AttachCandidate)) ([]featureDecl, map[string]bool) {
	var features []featureDecl
	names := map[string]bool{}

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
			// which need not be a workspace file at all. A document outside
			// the root is not workspace-local, so it is not a member of the
			// population this walk reports — a statement about membership
			// and not about the path. Features that legitimately live
			// outside the root reach the answer through catalog resolution
			// instead, which addresses them by name.
			return true
		}
		if !reach.Holds(doc.path) {
			return true
		}
		entity := AttachCandidate{
			WorkspaceEntityRef: WorkspaceEntityRef{Kind: kind, Name: name, File: rel},
			Origin:             EntityOriginWorkspace,
		}
		if visit != nil {
			visit(doc, entity)
		}
		if kind == "Feature" {
			names[name] = true
			features = append(features, featureDeclFromDoc(entity, doc.node))
		}
		return true
	})

	return features, names
}

// featureDeclFromDoc reads a workspace document's applicability declaration
// off the authored YAML.
func featureDeclFromDoc(entity AttachCandidate, doc *yaml.Node) featureDecl {
	decl := featureDecl{entity: entity, parsed: true}
	// An absent appliesTo and one authored as something other than a
	// scalar are different states and must not collapse into each
	// other. Absent declares no requirement; a mapping or a list
	// there is a declaration with no string form to evaluate, and
	// reading it as absent would let a mis-authored surface widen
	// what is offered rather than narrow it.
	if at := nodeAtPathOnDoc(doc, []string{"spec", "appliesTo"}); at != nil {
		if at.Kind != yaml.ScalarNode {
			decl.parsed = false
		} else {
			decl.appliesTo = at.Value
		}
	}
	if anyOf := nodeAtPathOnDoc(doc, []string{"spec", "appliesToAnyOf"}); anyOf != nil {
		if err := anyOf.Decode(&decl.anyOf); err != nil {
			// The list is authored in a shape the selector term
			// cannot read, so there is no declaration to evaluate.
			// Not a candidate, for the same reason an unparseable
			// selector string is not one.
			decl.parsed = false
		}
	}
	return decl
}

// catalogFeatureDecls is the applicability declaration of every Feature the
// configured catalog directories carry, minus the names already reported from
// the workspace.
//
// These declarations arrive already parsed, and that is a property of how
// they were obtained rather than an assumption: catalog resolution unmarshals
// the document into the Feature type, so a declaration in a shape the type
// cannot hold fails there and the Feature never resolves. A resolved Feature
// therefore has a readable declaration by construction, which is the same
// standard the authored-YAML path applies with its own reader.
func (s *Server) catalogFeatureDecls(alreadyReported map[string]bool) []featureDecl {
	return catalogFeatureDeclsFrom(s.lazyCatalog, alreadyReported)
}

// catalogFeatureDeclsFrom is the whole of catalogFeatureDecls with the lookup
// supplied rather than read off a session.
func catalogFeatureDeclsFrom(view *catalog.LazyLookup, alreadyReported map[string]bool) []featureDecl {
	resolved := catalogFeaturesFrom(view, alreadyReported)
	out := make([]featureDecl, 0, len(resolved))
	for _, feat := range resolved {
		out = append(out, featureDecl{
			entity:    catalogFeatureRef(feat),
			appliesTo: feat.Spec.AppliesTo,
			anyOf:     feat.Spec.AppliesToAnyOf,
			parsed:    true,
		})
	}
	return out
}

// matchFeatures evaluates every declaration against a label set and reports
// the Features that admit it, in the shared workspace-entity order.
//
// One evaluation entry point serves both addressing modes and both candidate
// populations, so a Feature offered for a graph node and the same Feature
// offered for a document are decided by the same rule. A second matcher would
// agree on the ordinary case and part company on the awkward one.
func matchFeatures(decls []featureDecl, labels types.OrderedMap[string]) []AttachCandidate {
	candidates := []AttachCandidate{}
	for _, decl := range decls {
		if !decl.parsed {
			continue
		}
		ok, err := selector.MatchApplicability(decl.appliesTo, decl.anyOf, labels)
		if err != nil || !ok {
			continue
		}
		candidates = append(candidates, decl.entity)
	}
	sortCandidates(candidates)
	return candidates
}

// matchingFeatures reports every Feature the daemon can see whose declared
// applicability the supplied label set satisfies, drawing on both the
// workspace and the configured catalog directories.
//
// It is what both label-first paths match against — the graph-node coordinate
// and a supplied label set. Neither has a workspace document to anchor on, so
// each needs the candidate population without the subject lookup the
// name-addressed path performs in the same walk.
func (s *Server) matchingFeatures(ctx context.Context, labels types.OrderedMap[string]) []AttachCandidate {
	return matchingFeaturesIn(ctx, s.workspaceRoot, s.wsConfig, s.lazyCatalog, workspaceconfig.ReleaseReach{}, s.overlayDocs(), labels)
}

// matchingFeaturesIn is the whole of matchingFeatures with the root, the
// workspace's configuration, the catalog lookup, the reach and the
// fresher-than-disk overlay supplied rather than read off a session. The
// workspace documents are those reach holds, and the catalog-directory Features
// are those view's directories declare, so the two populations are read in one
// Release's terms or both in the workspace's.
func matchingFeaturesIn(ctx context.Context, root string, cfg workspaceconfig.Config, view *catalog.LazyLookup,
	reach workspaceconfig.ReleaseReach, overlay []workspaceDoc, labels types.OrderedMap[string]) []AttachCandidate {
	features, workspaceNames := workspaceFeatureDeclsIn(ctx, root, cfg, overlay, reach, nil)
	features = append(features, catalogFeatureDeclsFrom(view, workspaceNames)...)
	return matchFeatures(features, labels)
}

// sortCandidates puts entities into the shared workspace-entity order,
// which is the order every projection over the workspace traversal reports
// in. It serves every candidate list, whichever reference shape carries it,
// so two responses a client joins against each other cannot order the same
// entities differently.
func sortCandidates[T entityIdentity](entities []T) {
	sort.Slice(entities, func(i, j int) bool {
		a, b := entities[i].identity(), entities[j].identity()
		return entityOrderLess(a.Kind, a.Name, a.File, b.Kind, b.Name, b.File)
	})
}

// orderedLabels lifts a flat label map into the ordered form the selector
// surfaces take.
//
// Keys are inserted in sorted order rather than in map-iteration order,
// which keeps the construction itself deterministic. The choice of order
// cannot affect a match: every selector surface reads a label set by key,
// so the result is order-independent by construction. Sorting is here so
// that no map-iteration order can leak into anything built from the
// result later.
func orderedLabels(labels map[string]string) types.OrderedMap[string] {
	keys := make([]string, 0, len(labels))
	for k := range labels {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var out types.OrderedMap[string]
	for _, k := range keys {
		out.Set(k, labels[k])
	}
	return out
}
