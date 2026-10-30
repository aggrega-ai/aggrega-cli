// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package redact

import (
	"encoding/json"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// The pillar prefixes a per-leaf coordinate carries. A coordinate is the pillar
// name, a dot, and the flat-dotted path of the datum within that pillar, which
// is what makes one list able to name leaves in two pillars without a reader
// having to know which list it came from.
const (
	valuesPillar = "values"
	infraPillar  = "infra"
)

// Graph returns a copy of an IR graph with every leaf the graph's OWN
// per-leaf markings name replaced by the withheld marker.
//
// It consults nothing outside the graph, and that is the property worth
// stating: the markings were computed at IR-build time from the declarations —
// the node's Component schema, an attachment's Feature schema — so the graph
// already carries the whole answer, and a surface withholding from it needs no
// catalog, no resolver and no second reading of any declaration. Two surfaces
// withholding from the same graph therefore withhold exactly the same leaves,
// rather than agreeing only for as long as two readings of the declarations
// happen to agree.
//
// Four surfaces carry authored pillar data and each is covered: a node's own
// values and infra, an attachment's payloads, a boundary attachment's infra,
// and a connection's values and infra. A connection's configuration sits in two
// positions — the edge's own payloads, and the consuming node's authored `uses`
// entry for the same connection, which carries the values as written — and
// both are withheld by the one marking the edge carries, so neither can show
// what the other withholds. A node, edge or entry whose marking is empty is
// passed through untouched, so a graph declaring no sensitivity emits the bytes
// it emitted before this existed.
//
// The command line calls it when it prints the graph, the daemon when it
// answers a request for the graph, and the governance socket when it hands the
// graph to a policy plugin. Each calls it rather than walking the markings
// itself, which is what keeps every surface that withholds from the graph
// withholding the same leaves.
//
// The input is never mutated. The graph this reads is the one the pipeline
// produced and hands onward, so an in-place substitution here would reach every
// later consumer of it — the copy is what keeps the withholding on the side of
// the surface handing the graph out rather than upstream of rendering.
//
// The delivery pipe is deliberately NOT reached, and the omission is not one to
// complete later. A delivery entry maps a delivery key to the provides key it
// references, so both halves are names an author chose in order to locate a
// value — the value itself is resolved downstream and is nowhere in this graph.
// Withholding a locator hides the name a reader needs in order to find the
// declaration it came from, while hiding no datum at all. The marking that
// names those entries is carried beside them for a reader that wants to say
// which delivery keys inherited sensitivity, which is a different job from
// withholding.
func Graph(in *types.IRGraph) *types.IRGraph {
	if in == nil {
		return nil
	}
	out := *in
	out.Spec.ValuesMapping = rawPillars(in.Spec.ValuesMapping, in.Spec.Nodes, valuesPillar)
	out.Spec.InfraMapping = rawPillars(in.Spec.InfraMapping, in.Spec.Nodes, infraPillar)
	out.Spec.FeatureValuesMapping = attachmentPillars(in.Spec.FeatureValuesMapping)
	out.Spec.BoundaryFeatures = boundaryPillars(in.Spec.BoundaryFeatures)
	out.Spec.Edges = edgePillars(in.Spec.Edges)
	out.Spec.Nodes = authoredConnectionValues(in.Spec.Nodes, in.Spec.Edges)
	return &out
}

// ConnectionKeys names, for one connection, the leaves of its values and infra
// the edge's own marking names, each keyed by the flat-dotted path from its
// pillar root — the spelling Pillar and the template coordinates read.
type ConnectionKeys struct {
	Values map[string]bool
	Infra  map[string]bool
}

// Any reports whether the marking names any leaf of either pillar.
func (c ConnectionKeys) Any() bool {
	return len(c.Values) > 0 || len(c.Infra) > 0
}

// ConnectionOf returns the leaves one edge's marking names, split by pillar. It
// is how a surface displaying a connection's configuration somewhere other than
// in the graph — a template's view of the connection — reads the same marking
// this package withholds the graph by, so the two surfaces withhold the same
// leaves of the same connection.
func ConnectionOf(edge types.IREdge) ConnectionKeys {
	return ConnectionKeys{
		Values: pillarPaths(edge.SensitivePayloadLeaves, valuesPillar),
		Infra:  pillarPaths(edge.SensitivePayloadLeaves, infraPillar),
	}
}

// edgePillars withholds inside every edge's values and infra, reading each
// edge's own marking. An edge list naming no leaf is returned as it is; any
// other is copied, because the edges are values in a slice the input shares.
func edgePillars(in []types.IREdge) []types.IREdge {
	marked := false
	for _, e := range in {
		if len(e.SensitivePayloadLeaves) > 0 {
			marked = true
			break
		}
	}
	if !marked {
		return in
	}
	out := make([]types.IREdge, len(in))
	for i, e := range in {
		keys := ConnectionOf(e)
		e.Values = PillarStructured(e.Values, keys.Values, WithheldDeclared)
		e.Infra = PillarStructured(e.Infra, keys.Infra, WithheldDeclared)
		out[i] = e
	}
	return out
}

// authoredConnectionValues withholds inside the values each node's authored
// `uses` block writes for a connection, reading the marking of the edge that
// connection drew. The block is the author's text rather than the resolved
// payload, so every leaf it writes is a leaf the edge carries — an authored
// value wins over a declared default — and the edge's marking names each one the
// socket's contract declares sensitive.
//
// The edge is found by the node it starts at, the socket and the alias: a built
// node's connections never share an alias, so the three name one connection. An
// entry whose connection drew no edge carries no marking, and the configuration
// it writes was measured against no contract; it is shown as written.
//
// Only a node whose block writes values for a marked connection is copied, and
// only that entry's values are replaced, so every other byte of the block is the
// one the author wrote.
func authoredConnectionValues(nodes types.OrderedMap[types.IRNode], edges []types.IREdge) types.OrderedMap[types.IRNode] {
	marked := map[string]map[string]bool{}
	for _, e := range edges {
		if keys := ConnectionOf(e); len(keys.Values) > 0 {
			marked[e.Source+"\x00"+e.Socket+"\x00"+e.Alias] = keys.Values
		}
	}
	if len(marked) == 0 || nodes.Len() == 0 {
		return nodes
	}
	out := types.OrderedMap[types.IRNode]{}
	for nodeID, node := range nodes.All() {
		node.Uses = withheldUses(nodeID, node.Uses, marked)
		out.Set(nodeID, node)
	}
	return out
}

// withheldUses returns one node's authored `uses` block with each marked
// connection's values withheld, or the block itself when no entry it writes is
// marked.
func withheldUses(nodeID string, uses types.OrderedMap[types.UsesSocketValue], marked map[string]map[string]bool) types.OrderedMap[types.UsesSocketValue] {
	carries := false
	for socket, sv := range uses.All() {
		for alias, entry := range sv.Verbose.All() {
			if entry.Values.Len() > 0 && len(marked[nodeID+"\x00"+socket+"\x00"+alias]) > 0 {
				carries = true
			}
		}
	}
	if !carries {
		return uses
	}
	out := types.OrderedMap[types.UsesSocketValue]{}
	for socket, sv := range uses.All() {
		copied := types.UsesSocketValue{Compact: sv.Compact}
		for alias, entry := range sv.Verbose.All() {
			entry.Values = PillarStructured(entry.Values, marked[nodeID+"\x00"+socket+"\x00"+alias], WithheldDeclared)
			copied.Verbose.Set(alias, entry)
		}
		out.Set(socket, copied)
	}
	return out
}

// pillarPaths selects, from one pillar-prefixed coordinate list, the leaf paths
// that fall in the named pillar, with the prefix stripped so they join the
// pillar's own key space.
//
// A coordinate carrying no recognised pillar prefix selects nothing rather than
// being treated as a bare path. Guessing that an unprefixed coordinate meant
// this pillar would withhold a leaf in one pillar because a coordinate named a
// same-named leaf in the other.
func pillarPaths(coordinates []string, pillar string) map[string]bool {
	prefix := pillar + "."
	var paths map[string]bool
	for _, c := range coordinates {
		if !strings.HasPrefix(c, prefix) {
			continue
		}
		path := strings.TrimPrefix(c, prefix)
		if path == "" {
			continue
		}
		if paths == nil {
			paths = map[string]bool{}
		}
		paths[path] = true
	}
	return paths
}

// rawPillars withholds inside the per-node pillar payloads the graph carries as
// raw JSON, reading each node's own marking for the named pillar.
func rawPillars(in types.OrderedMap[json.RawMessage], nodes types.OrderedMap[types.IRNode], pillar string) types.OrderedMap[json.RawMessage] {
	if in.Len() == 0 {
		return in
	}
	out := types.OrderedMap[json.RawMessage]{}
	for nodeID, payload := range in.All() {
		node, ok := nodes.Get(nodeID)
		if !ok {
			// A payload keyed by a node the graph does not carry cannot be
			// judged: the marking lives on the node, so there is nothing to read
			// the declarations from. Withhold it whole rather than emit it.
			out.Set(nodeID, rawMarker())
			continue
		}
		out.Set(nodeID, rawPillar(payload, pillarPaths(node.SensitivePayloadLeaves, pillar)))
	}
	return out
}

// rawPillar withholds inside one pillar payload carried as raw JSON.
//
// The payload is decoded into the ordered form, walked by the same descent the
// text surface walks, and re-encoded. Decoding through the ordered map rather
// than a plain map is what keeps author order — the order the graph's own
// determinism contract is stated in — across the round trip.
//
// A payload that does not decode is withheld WHOLE. Failing to read a payload
// is indistinguishable from reading one whose every leaf is public, so the only
// safe reading of a decode failure is that no leaf under it could be judged.
// Over-withholding costs a reader one payload they must find another way;
// under-withholding prints a secret.
func rawPillar(in json.RawMessage, paths map[string]bool) json.RawMessage {
	if len(paths) == 0 {
		return in
	}
	var decoded types.OrderedMap[any]
	if err := json.Unmarshal(in, &decoded); err != nil {
		return rawMarker()
	}
	replaced := PillarStructured(decoded, paths, WithheldDeclared)
	encoded, err := json.Marshal(replaced)
	if err != nil {
		return rawMarker()
	}
	return encoded
}

// rawMarker is the withheld marker encoded as the raw JSON a whole-payload
// withholding leaves behind. A marshal that cannot fail — the value is one
// string keyed by one string — is reported as the marker rather than as an
// error, because a payload this function could not encode is exactly the
// payload that must not be returned unwithheld.
func rawMarker() json.RawMessage {
	encoded, err := json.Marshal(WithheldMarker(WithheldUnreadable))
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return encoded
}

// attachmentPillars withholds inside every attachment's values and infra,
// reading each attachment's own marking.
func attachmentPillars(in types.OrderedMap[types.OrderedMap[types.IRFeatureEntry]]) types.OrderedMap[types.OrderedMap[types.IRFeatureEntry]] {
	if in.Len() == 0 {
		return in
	}
	out := types.OrderedMap[types.OrderedMap[types.IRFeatureEntry]]{}
	for nodeID, attachments := range in.All() {
		replaced := types.OrderedMap[types.IRFeatureEntry]{}
		for instance, entry := range attachments.All() {
			entry.Values = PillarStructured(entry.Values, pillarPaths(entry.SensitivePayloadLeaves, valuesPillar), WithheldDeclared)
			entry.Infra = PillarStructured(entry.Infra, pillarPaths(entry.SensitivePayloadLeaves, infraPillar), WithheldDeclared)
			replaced.Set(instance, entry)
		}
		out.Set(nodeID, replaced)
	}
	return out
}

// boundaryPillars withholds inside every boundary attachment's infra, reading
// each attachment's own marking.
//
// Only the infra pillar is reached, because a boundary-level attachment carries
// no values payload to author into — the Release schema bans a values block at
// boundary level, and the shape honours that by having no field rather than by
// leaving one empty.
func boundaryPillars(in types.OrderedMap[types.OrderedMap[types.IRBoundaryFeature]]) types.OrderedMap[types.OrderedMap[types.IRBoundaryFeature]] {
	if in.Len() == 0 {
		return in
	}
	out := types.OrderedMap[types.OrderedMap[types.IRBoundaryFeature]]{}
	for boundaryPath, attachments := range in.All() {
		replaced := types.OrderedMap[types.IRBoundaryFeature]{}
		for instance, attachment := range attachments.All() {
			attachment.Infra = PillarStructured(attachment.Infra, pillarPaths(attachment.SensitivePayloadLeaves, infraPillar), WithheldDeclared)
			replaced.Set(instance, attachment)
		}
		out.Set(boundaryPath, replaced)
	}
	return out
}
