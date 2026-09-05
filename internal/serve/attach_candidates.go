// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

import (
	"context"
	"encoding/json"
	"sort"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline"
	"github.com/aggrega-ai/aggrega-cli/pkg/selector"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// WorkspaceEntityRef addresses one workspace-local document on the wire:
// kind, name and source file, the three fields that identify it.
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
// Every candidate-reporting method reports its candidates as this type, so
// the joinable triple is one declaration rather than a shape each response
// repeats and could drift from. sortCandidates orders any of them.
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
type WorkspaceEntityRef struct {
	Kind   string `json:"kind"`
	Name   string `json:"name"`
	File   string `json:"file"`
	Origin string `json:"origin"`
}

// AttachCandidate is one Feature in the aggrega/attachCandidates
// response: a Feature whose declared applicability the addressed entity's
// labels satisfy.
type AttachCandidate = WorkspaceEntityRef

// Node-label provenance values, reported as labelSource so a client knows
// which of two genuinely different label sets a match was evaluated against.
//
// The distinction is not cosmetic and it must not be smoothed away. A
// document's declared labels are what a read over source can see; a graph
// node's are the finalised set the build itself evaluates applicability
// against, after a composition or a Release has contributed whatever it
// contributes. An answer computed over the second is a statement about what
// the build will do; one computed over the first is not, and a response that
// looked identical for both would invite a client to read the weaker answer
// as the stronger one.
const (
	// NodeLabelsDeclared marks a match evaluated against the labels a workspace
	// document declares at metadata.labels.
	NodeLabelsDeclared = "declared"
	// NodeLabelsResolved marks a match evaluated against the finalised labels a
	// compiled graph node carries.
	NodeLabelsResolved = "resolved"
)

// attachCandidatesParams is the aggrega/attachCandidates request shape.
//
// The two coordinates address two different things and exactly one is given.
//
// Node names a workspace document by its metadata.name — the same name
// aggrega/catalogIndex reports. GraphNode names a node in the compiled graph
// of a Release, by the id that graph keys it under, and Release says which
// Release's graph to build.
//
// They are separate parameters because the two identifier spaces OVERLAP. A
// graph node id is constrained to a pattern; a document's metadata.name
// carries no pattern at all, so a name legal as one is frequently legal as
// the other and a single parameter carrying both would be ambiguous by
// construction. Deciding which space to search by inspecting the value would
// be the compiler guessing meaning out of a string, which is exactly what it
// does not do anywhere else. So the caller says which question it is asking,
// and the daemon answers that one.
type attachCandidatesParams struct {
	Node      string `json:"node"`
	GraphNode string `json:"graphNode"`
	Release   string `json:"release"`
}

// attachCandidatesResult is the aggrega/attachCandidates response.
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
// LabelSource names which of the two label sets NodeLabels holds, so the
// strength of the answer travels with it rather than having to be inferred
// from which request parameter was sent.
type attachCandidatesResult struct {
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
// content already on disk. Addressing a graph node builds the Release's
// graph through the same computation the graph query runs, so it resolves
// that Release's closure through the ordinary catalog cascade — which
// serves from the local directories and the content-addressed cache
// first, and can reach the configured registry for an entity neither
// carries. A surface that claimed otherwise for both coordinates would be
// describing only the cheaper one.
//
// A request naming no entity, or naming one the workspace does not
// declare, is an error rather than an empty candidate list — those two
// answers mean different things to a client, and collapsing them into an
// empty array would report a typo as a node with nothing to offer.
//
// # Addressing a node on a compiled graph
//
// A canvas draws the compiled graph, whose nodes are keyed by a topological
// id rather than by any document's name, so it addresses this method with
// graphNode and the release whose graph it is drawing. That path builds the
// Release's graph through the same computation the graph query runs, and
// therefore carries the same lockfile precondition — but only when it is
// used. A request addressing a workspace document by name needs no lockfile
// and is answered exactly as it was before this path existed.
func (s *Server) dispatchAttachCandidates(ctx context.Context, req *jsonRPCRequest) error {
	var params attachCandidatesParams
	if len(req.Params) > 0 {
		if err := json.Unmarshal(req.Params, &params); err != nil {
			return s.writeError(ctx, req.ID, jsonRPCInvalidParams, "invalid params: "+err.Error())
		}
	}
	// The two coordinates are refused together rather than silently ranked.
	// A client that sent both has a bug whichever one it meant, and picking
	// one for it would answer a question it did not ask — on a surface where
	// the two spaces overlap, so the wrong pick can still produce a
	// plausible-looking answer.
	switch {
	case params.Node == "" && params.GraphNode == "":
		return s.writeError(ctx, req.ID, jsonRPCInvalidParams,
			"invalid params: name a workspace entity with params.node, or a node on a compiled graph with params.graphNode and params.release")
	case params.Node != "" && params.GraphNode != "":
		return s.writeError(ctx, req.ID, jsonRPCInvalidParams,
			"invalid params: params.node and params.graphNode address different things; send one, not both")
	case params.Node != "" && params.Release != "":
		return s.writeError(ctx, req.ID, jsonRPCInvalidParams,
			"invalid params: params.release applies to params.graphNode; a request naming params.node builds no graph")
	}
	if err := ctx.Err(); err != nil {
		return s.writeError(ctx, req.ID, jsonRPCInternalError, err.Error())
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
func (s *Server) attachCandidatesForGraphNode(ctx context.Context, node, release string) (attachCandidatesResult, *jsonRPCError) {
	// Nothing rendered: this reads the node's labels out of the graph and
	// discards everything else, and the graph is byte-identical whether or
	// not the render ran. Skipping it keeps the answer from loading a
	// rendering plugin to compute a label match.
	preview, errEnvelope := s.previewRelease(ctx, release, pipeline.Options{IncludeTrace: false, SkipRender: true})
	if errEnvelope != nil {
		return attachCandidatesResult{}, errEnvelope
	}

	irNode, ok := preview.pipeline.IRGraph.Spec.Nodes.Get(node)
	if !ok {
		// Named the graph rather than the workspace, because that is what the
		// caller addressed. A client told "no workspace entity is named X"
		// after asking about a graph node would look for a document it never
		// meant.
		return attachCandidatesResult{}, &jsonRPCError{
			Code:    errEntityNotResolvable,
			Message: "the compiled graph of " + release + " has no node with id " + node,
		}
	}

	labels := map[string]string{}
	for k, v := range irNode.Labels.All() {
		labels[k] = v
	}

	return attachCandidatesResult{
		Node:        node,
		NodeLabels:  labels,
		LabelSource: NodeLabelsResolved,
		Candidates:  s.matchingFeatures(ctx, irNode.Labels),
	}, nil
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
// against the finalised label set of the graph node it produces, and a
// composition or a Release may contribute labels to that set which no
// single document declares. So this reports the match over declared
// labels; it is not a prediction of what a build will decide for a node
// whose labels are contributed from above.
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
func (s *Server) attachCandidates(ctx context.Context, node string) (attachCandidatesResult, bool) {
	// A workspace may declare the requested name on more than one
	// document — two kinds may each carry it. Every match is collected and
	// the total order below picks between them, so the answer does not
	// depend on which one the traversal happened to reach first.
	var named []AttachCandidate
	namedLabels := map[string]map[string]string{}

	features, workspaceNames := s.workspaceFeatureDecls(ctx, func(doc workspaceDoc, entity AttachCandidate) {
		if entity.Name == node {
			named = append(named, entity)
			namedLabels[entity.File] = stringMapAtPathOnDoc(doc.node, []string{"metadata", "labels"})
		}
	})

	if len(named) == 0 {
		return attachCandidatesResult{}, false
	}
	sortCandidates(named)
	subject := named[0]

	labels := namedLabels[subject.File]
	if labels == nil {
		labels = map[string]string{}
	}

	features = append(features, s.catalogFeatureDecls(workspaceNames)...)

	return attachCandidatesResult{
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
	var features []featureDecl
	names := map[string]bool{}

	s.rangeWorkspaceDocs(ctx, func(doc workspaceDoc) bool {
		kind := scalarAtPathOnDoc(doc.node, []string{"kind"})
		if kind == "" {
			return true
		}
		name, _ := metadataNameOf(doc.node)
		if name == "" {
			return true
		}
		rel, inside := relInsideRoot(s.workspaceRoot, doc.path)
		if !inside {
			// The parse cache holds whatever file the editor last sent,
			// which need not be a workspace file at all. A document outside
			// the root is not workspace-local, and reporting it under a
			// workspace-relative path would be a lie about where it came
			// from. Features that legitimately live outside the root reach
			// the answer through catalog resolution instead, which addresses
			// them by name and discloses no host path.
			return true
		}
		entity := AttachCandidate{Kind: kind, Name: name, File: rel, Origin: EntityOriginWorkspace}
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
	resolved := s.catalogFeatures(alreadyReported)
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
// It is what the graph-node path matches against: that path has no workspace
// document to anchor on, so it needs the candidate population without the
// subject lookup the name-addressed path performs in the same walk.
func (s *Server) matchingFeatures(ctx context.Context, labels types.OrderedMap[string]) []AttachCandidate {
	features, workspaceNames := s.workspaceFeatureDecls(ctx, nil)
	features = append(features, s.catalogFeatureDecls(workspaceNames)...)
	return matchFeatures(features, labels)
}

// sortCandidates puts entities into the shared workspace-entity order,
// which is the order every projection over the workspace traversal reports
// in. It serves every candidate list, so two responses a client joins
// against each other cannot order the same entities differently.
func sortCandidates(entities []WorkspaceEntityRef) {
	sort.Slice(entities, func(i, j int) bool {
		a, b := entities[i], entities[j]
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
