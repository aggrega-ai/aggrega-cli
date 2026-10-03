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
// Three surfaces carry authored pillar data and each is covered: a node's own
// values and infra, an attachment's payloads, and a boundary attachment's
// infra. A node whose markings are empty has its payloads passed through
// untouched, so a graph declaring no sensitivity emits the bytes it emitted
// before this existed.
//
// The command line calls it when it prints the graph, and the governance
// socket calls it when it hands the graph to a policy plugin. Each calls it
// rather than walking the markings itself, which is what keeps every surface
// that withholds from the graph withholding the same leaves.
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
	return &out
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
