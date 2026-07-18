// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package topology

import (
	"encoding/json"
	"fmt"
	"sort"

	jsonpatch "github.com/evanphx/json-patch"

	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// AssembleMutatorInput builds the [MutatorInput] payload the Step-2 mutator
// socket hands its guest: the release-level compilation context, a labels-only
// projection of the flattened topology graph, and the attachment's own inputs —
// the pinned source files and the free-form settings object. Each node projects
// its underlying Component, the resolved Component version (read from the
// catalog, the same source the IR builder stamps onto a node), and its label
// set. Edges, values, and feature payloads are deliberately withheld — the
// mutator's sanctioned job is labelling, not topology reshaping. Nodes project
// in author order via the graph's OrderedMap so the payload is deterministic.
//
// It is the single home that builds the mutator input; the caller supplies the
// attachment inputs already projected — sources as the ordered source entries
// and settings as the authored object — so build-time source content comes from
// the lockfile pin rather than a live host read. The settings object marshals to
// its verbatim JSON form (deterministic sorted keys); an attachment with no
// sources and no settings yields a payload byte-identical to one with neither.
func AssembleMutatorInput(topo *TopologyResult, catalogLookup resolver.CatalogLookup, cc CompilationContext, sources []MutatorSource, settings map[string]any) (MutatorInput, error) {
	nodes := types.NewOrderedMap[MutatorNode]()
	for nodeID, node := range topo.Nodes.All() {
		var version string
		if catalogLookup.Component != nil {
			if catComp, ok := catalogLookup.Component(node.ComponentType); ok {
				version = catComp.Metadata.Version
			}
		}
		nodes.Set(nodeID, MutatorNode{
			Component:       node.ComponentType,
			ResolvedVersion: version,
			Labels:          node.Labels,
		})
	}
	mi := MutatorInput{
		CompilationContext: cc,
		Graph:              MutatorGraph{Nodes: *nodes},
		Sources:            sources,
	}
	if len(settings) > 0 {
		raw, err := json.Marshal(settings)
		if err != nil {
			return MutatorInput{}, fmt.Errorf("marshal mutator settings: %w", err)
		}
		mi.Settings = raw
	}
	return mi, nil
}

// mutatorLabelsNode is the per-node shape of the labels-only document the host
// applies mutator patches against. It exposes exactly one field — labels — so
// a patch that reaches any other node field (component, resolved_version, or a
// key the plugin invents) is caught by the confinement check after apply.
type mutatorLabelsNode struct {
	Labels map[string]string `json:"labels"`
}

// mutatorLabelsDoc is the root of the labels-only patch document:
// {"nodes":{"<id>":{"labels":{...}}}}. It mirrors the MutatorGraph the guest
// saw (patch paths are rooted at /nodes/<id>/labels/<key>) but strips every
// field except labels, so a patch touching a non-label surface fails to apply
// or is rejected by the post-apply confinement check.
type mutatorLabelsDoc struct {
	Nodes map[string]mutatorLabelsNode `json:"nodes"`
}

// ApplyMutatorPatches applies a mutator plugin's JSON Patch documents to the
// node-label surface of topo, in the order the plugin emitted them, and merges
// the resulting labels back onto the topology nodes. It is the narrow
// auto-tagging exception (Invariant 4): the mutator may add, change, or remove
// node labels and nothing else.
//
// Confinement is enforced by construction and by a post-apply check. The
// patches apply to a labels-only projection ({"nodes":{"<id>":{"labels":{}}}}),
// so a patch that replaces or removes a non-label field (component,
// resolved_version) fails to apply and halts. A patch that adds a field outside
// the label surface — a new node, a new per-node key, a top-level key, or a
// non-string label value — succeeds against the loose projection but is caught
// by the confinement check: the mutated document must still carry exactly the
// original node set, each node must carry exactly its labels, and every label
// value must be a string. Any breach halts with [ErrMutatorPatchRejected]
// rather than silently absorbing an unauthorized mutation.
//
// The label re-merge is deterministic: for each node, labels the plugin left in
// place keep their original author-order position, and labels the plugin added
// append in sorted key order. Nodes the plugin did not touch keep their labels
// unchanged. An empty patch list is a no-op.
func ApplyMutatorPatches(topo *TopologyResult, out MutatorOutput) error {
	if len(out.Patches) == 0 {
		return nil
	}

	doc := mutatorLabelsDoc{Nodes: make(map[string]mutatorLabelsNode, topo.Nodes.Len())}
	for nodeID, node := range topo.Nodes.All() {
		labels := make(map[string]string, node.Labels.Len())
		for k, v := range node.Labels.All() {
			labels[k] = v
		}
		doc.Nodes[nodeID] = mutatorLabelsNode{Labels: labels}
	}

	docBytes, err := json.Marshal(doc)
	if err != nil {
		return fmt.Errorf("%w: assemble labels projection: %v", ErrMutatorPatchRejected, err)
	}

	for i, raw := range out.Patches {
		patch, err := jsonpatch.DecodePatch(raw)
		if err != nil {
			return fmt.Errorf("%w: patch %d does not decode as a JSON Patch document: %v", ErrMutatorPatchRejected, i, err)
		}
		docBytes, err = patch.Apply(docBytes)
		if err != nil {
			return fmt.Errorf("%w: patch %d does not apply to the node-label surface: %v", ErrMutatorPatchRejected, i, err)
		}
	}

	patched, err := confinedLabels(topo, docBytes)
	if err != nil {
		return err
	}

	for nodeID, node := range topo.Nodes.All() {
		labels := patched[nodeID]
		merged := types.NewOrderedMap[string]()
		for k := range node.Labels.All() {
			if v, ok := labels[k]; ok {
				merged.Set(k, v)
			}
		}
		var added []string
		for k := range labels {
			if _, existed := node.Labels.Get(k); !existed {
				added = append(added, k)
			}
		}
		sort.Strings(added)
		for _, k := range added {
			merged.Set(k, labels[k])
		}
		node.Labels = *merged
	}
	return nil
}

// confinedLabels re-parses the post-apply document and enforces the mutator's
// label-only confinement, returning the per-node label maps keyed by node ID.
// The document must carry exactly one top-level key (nodes), the exact original
// node set (no additions or removals), exactly a labels field per node, and
// only string label values. Any deviation is an [ErrMutatorPatchRejected] halt.
func confinedLabels(topo *TopologyResult, docBytes []byte) (map[string]map[string]string, error) {
	var top map[string]json.RawMessage
	if err := json.Unmarshal(docBytes, &top); err != nil {
		return nil, fmt.Errorf("%w: mutated document is not a JSON object: %v", ErrMutatorPatchRejected, err)
	}
	if len(top) != 1 {
		return nil, fmt.Errorf("%w: mutated document added a top-level key outside %q", ErrMutatorPatchRejected, "nodes")
	}
	rawNodes, ok := top["nodes"]
	if !ok {
		return nil, fmt.Errorf("%w: mutated document dropped the %q key", ErrMutatorPatchRejected, "nodes")
	}

	var nodes map[string]json.RawMessage
	if err := json.Unmarshal(rawNodes, &nodes); err != nil {
		return nil, fmt.Errorf("%w: mutated node set is not a JSON object: %v", ErrMutatorPatchRejected, err)
	}
	if len(nodes) != topo.Nodes.Len() {
		return nil, fmt.Errorf("%w: mutated node set changed size (a mutator may not add or remove nodes)", ErrMutatorPatchRejected)
	}

	out := make(map[string]map[string]string, len(nodes))
	for id, rawNode := range nodes {
		if _, exists := topo.Nodes.Get(id); !exists {
			return nil, fmt.Errorf("%w: mutated node set introduced node %q (a mutator may not add nodes)", ErrMutatorPatchRejected, id)
		}
		var nodeObj map[string]json.RawMessage
		if err := json.Unmarshal(rawNode, &nodeObj); err != nil {
			return nil, fmt.Errorf("%w: node %q is not a JSON object after patching: %v", ErrMutatorPatchRejected, id, err)
		}
		if len(nodeObj) != 1 {
			return nil, fmt.Errorf("%w: node %q gained a field outside %q (a mutator may only touch labels)", ErrMutatorPatchRejected, id, "labels")
		}
		rawLabels, ok := nodeObj["labels"]
		if !ok {
			return nil, fmt.Errorf("%w: node %q lost its %q field", ErrMutatorPatchRejected, id, "labels")
		}
		var labels map[string]string
		if err := json.Unmarshal(rawLabels, &labels); err != nil {
			return nil, fmt.Errorf("%w: node %q carries a non-string label value after patching: %v", ErrMutatorPatchRejected, id, err)
		}
		out[id] = labels
	}
	return out, nil
}
