// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

import (
	"context"
	"encoding/json"
	"sort"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/pkg/selector"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// AttachCandidate is one Feature in the aggrega/attachCandidates
// response: a Feature whose declared applicability the addressed entity's
// labels satisfy.
//
// The identity is deliberately minimal — kind, name and source file, the
// three fields that address the document. Everything else a palette wants
// to show about a Feature is already reported by aggrega/catalogIndex for
// the same kind/name/file triple, so repeating it here would put a second
// copy of that description on the wire, and the two copies would be free
// to disagree. A client joins the two responses instead.
//
// Kind is carried even though every candidate is a Feature today, so the
// triple a client joins on has the same shape in both responses.
type AttachCandidate struct {
	Kind string `json:"kind"`
	Name string `json:"name"`
	File string `json:"file"`
}

// attachCandidatesParams is the aggrega/attachCandidates request shape.
// Node names the entity whose labels the candidates are matched against,
// by its metadata.name — the same name aggrega/catalogIndex reports.
type attachCandidatesParams struct {
	Node string `json:"node"`
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
type attachCandidatesResult struct {
	Node       string            `json:"node"`
	NodeLabels map[string]string `json:"nodeLabels"`
	Candidates []AttachCandidate `json:"candidates"`
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
// Read-only: it mutates nothing, resolves nothing, and reaches no
// registry or network.
//
// A request naming no entity, or naming one the workspace does not
// declare, is an error rather than an empty candidate list — those two
// answers mean different things to a client, and collapsing them into an
// empty array would report a typo as a node with nothing to offer.
func (s *Server) dispatchAttachCandidates(ctx context.Context, req *jsonRPCRequest) error {
	var params attachCandidatesParams
	if len(req.Params) > 0 {
		if err := json.Unmarshal(req.Params, &params); err != nil {
			return s.writeError(ctx, req.ID, jsonRPCInvalidParams, "invalid params: "+err.Error())
		}
	}
	if params.Node == "" {
		return s.writeError(ctx, req.ID, jsonRPCInvalidParams, "params.node is required")
	}
	if err := ctx.Err(); err != nil {
		return s.writeError(ctx, req.ID, jsonRPCInternalError, err.Error())
	}
	result, found := s.attachCandidates(ctx, params.Node)
	if !found {
		return s.writeError(ctx, req.ID, errEntityNotResolvable,
			"no workspace entity is named "+params.Node)
	}
	return s.writeResult(ctx, req.ID, result)
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
// Every workspace-local document whose kind is Feature and which declares
// a metadata.name, since those two are what make it addressable
// afterwards. A Feature declaring neither applicability surface is a
// candidate for every node: an absent surface declares no requirement,
// which is the same "no constraint declared" contract every selector
// surface carries.
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

	type featureDecl struct {
		entity    AttachCandidate
		appliesTo string
		anyOf     []types.SelectorTerm
		parsed    bool
	}
	var features []featureDecl

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
			// which need not be a workspace file at all. An entity outside
			// the root is not workspace-local, and reporting it would also
			// put an absolute host path on the wire.
			return true
		}
		entity := AttachCandidate{Kind: kind, Name: name, File: rel}
		if name == node {
			named = append(named, entity)
			namedLabels[rel] = stringMapAtPathOnDoc(doc.node, []string{"metadata", "labels"})
		}
		if kind == "Feature" {
			decl := featureDecl{entity: entity, parsed: true}
			// An absent appliesTo and one authored as something other than a
			// scalar are different states and must not collapse into each
			// other. Absent declares no requirement; a mapping or a list
			// there is a declaration with no string form to evaluate, and
			// reading it as absent would let a mis-authored surface widen
			// what is offered rather than narrow it.
			if at := nodeAtPathOnDoc(doc.node, []string{"spec", "appliesTo"}); at != nil {
				if at.Kind != yaml.ScalarNode {
					decl.parsed = false
				} else {
					decl.appliesTo = at.Value
				}
			}
			if anyOf := nodeAtPathOnDoc(doc.node, []string{"spec", "appliesToAnyOf"}); anyOf != nil {
				if err := anyOf.Decode(&decl.anyOf); err != nil {
					// The list is authored in a shape the selector term
					// cannot read, so there is no declaration to evaluate.
					// Not a candidate, for the same reason an unparseable
					// selector string is not one.
					decl.parsed = false
				}
			}
			features = append(features, decl)
		}
		return true
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
	matchable := orderedLabels(labels)

	candidates := []AttachCandidate{}
	for _, decl := range features {
		if !decl.parsed {
			continue
		}
		ok, err := selector.MatchApplicability(decl.appliesTo, decl.anyOf, matchable)
		if err != nil || !ok {
			continue
		}
		candidates = append(candidates, decl.entity)
	}
	sortCandidates(candidates)

	return attachCandidatesResult{
		Node:       subject.Name,
		NodeLabels: labels,
		Candidates: candidates,
	}, true
}

// sortCandidates puts entities into the shared workspace-entity order,
// which is the order every projection over the workspace traversal reports
// in.
func sortCandidates(entities []AttachCandidate) {
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
