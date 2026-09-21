// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package ir

import (
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/topology"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// ConnectionOriginKey addresses one origin's connection: the consuming node,
// the surface that drew the connection, and the socket and alias it wires.
//
// The origin is in the key because the node, socket and alias are not an
// identity. Two attachments on one node may each write one socket and one alias,
// and the component itself may write the same pair, so a key without the origin
// names up to three separately authored blocks at once — and a rule keyed that
// way reads one of them for all three and reports the other two as approved.
type ConnectionOriginKey struct {
	Node       string
	Kind       string
	Attachment string
	Socket     string
	Alias      string

	// Target is the node the connection resolves to. It is in the key because
	// two origins writing one socket and one alias may name different targets,
	// and each delivers against the one it named — so a key without it would
	// answer one origin's question with the other's target.
	Target string
}

// OriginatedConnections is the compilation product the originated-connection
// axis and its fact are both read from: every connection the composition drew,
// paired with the surface that drew it and with the delivery that connection
// dictates as the author wrote it.
//
// # Why it is held rather than derived at each reader
//
// Two of the three things it carries are gone by the time anything downstream
// could ask for them.
//
// The delivery AS AUTHORED is dropped in the conversion into the finalised
// graph: an edge's delivery is stored there as one string per delivery key, so a
// literal and a reference to a published key become the same shape. A rule
// refusing a reference to a key nothing publishes, read off the converted form,
// would refuse every literal as well.
//
// The site an origin's own block was written at is settled while the authoring
// tiers are read and is recorded nowhere afterwards. The finalised graph records
// which attachment instance drew an edge; which surface DECLARED that attachment
// — the component's own block, a scope in either document, or a component entry
// in the Release — it does not record at all. Recovering it later would mean
// re-walking the authoring documents and matching an edge back to a block, which
// is an elimination over the surfaces that can draw a connection, and an
// elimination is only ever as complete as that enumeration.
//
// # What it holds, and what bounds it
//
// One entry per distinct origin key, in the order the composition drew the
// connections, plus the authored delivery payloads under each key. It holds no
// authoring document and no edge: the entries are coordinate values and small
// sites, and the payloads are the delivery blocks alone.
//
// A key naming more than one connection is possible and is answered completely
// rather than partially. One attachment instance name declared at two scopes
// draws one connection from each, and the composition records the two
// identically, so the key cannot separate them — but every payload under it is
// retained, so a rule over the fact examines both.
type OriginatedConnections struct {
	// entries is the enumeration, one per distinct origin key, in the order
	// the composition drew the connections. It is the order the connections
	// reach the rules in, so it is the order their findings are reported in.
	entries []OriginatedConnectionEntry

	// delivery is every authored delivery payload each origin key names, in
	// the same order. Addressed by key only and never iterated, so no order it
	// holds reaches the output.
	delivery map[ConnectionOriginKey][]types.OrderedMap[any]

	// byOrigin is each origin's own record, keyed by the node and the origin
	// identity alone. It serves the edge axis, which is keyed by node, socket
	// and alias and so cannot address the key above. Addressed by key only and
	// never iterated.
	byOrigin map[originIdentity]constraint.ConnectionOrigin
}

// originIdentity is one origin on one node, without the connection it drew.
// Every connection one origin drew on one node was written in that origin's
// single block, whatever socket it wires, so this addresses the site outright.
type originIdentity struct {
	Node       string
	Kind       string
	Attachment string
}

// OriginatedConnectionEntry is one origin's connection as this product records
// it: the coordinate values a rule is invoked for, and the provenance a locator
// is built from.
type OriginatedConnectionEntry struct {
	// Node is the consuming node, by the identity every keyed fact takes.
	Node string

	// Origin is the surface that drew the connection, with the site that
	// surface's own block was written at.
	Origin constraint.ConnectionOrigin

	// Socket and Alias are the connection within its consumer, and Target is the
	// node it resolves to.
	Socket string
	Alias  string
	Target string

	// SynthesizedAlias reports whether the alias was composed by the compiler
	// rather than written by the author as a key.
	SynthesizedAlias bool
}

// Entries is the enumeration this product holds, in the order the composition
// drew the connections.
func (c *OriginatedConnections) Entries() []OriginatedConnectionEntry {
	if c == nil {
		return nil
	}
	return c.entries
}

// deliveryOf is every authored delivery payload one origin key names, in the
// order the composition drew them, and nil when the product holds none for it.
//
// Every reader addresses the product through here rather than composing the key
// where it reads, for the reason [AttachmentSites.siteOf] gives: the key is a
// composite, and a field added to it is silently defaulted in whatever keyed
// literal does not set it, so a second reader spelling its own key would go on
// compiling while addressing a different entry than the writer wrote.
func (c *OriginatedConnections) deliveryOf(node, kind, attachment, socket, alias, target string) []types.OrderedMap[any] {
	if c == nil {
		return nil
	}
	return c.delivery[ConnectionOriginKey{
		Node:       node,
		Kind:       kind,
		Attachment: attachment,
		Socket:     socket,
		Alias:      alias,
		Target:     target,
	}]
}

// originOfEdge is the origin recorded for one finalised edge, addressed by the
// coordinates that edge carries.
//
// It exists so the edge axis and the originated-connection axis position their
// findings from ONE record of where each origin's block was written. The edge
// axis is keyed by node, socket and alias and cannot address this product's own
// key, so it asks by the origin alone — which is enough, because every
// connection one origin drew on one node was written in that origin's single
// block whatever socket it wires.
//
// An origin the product holds no entry for yields the zero origin, which names
// no site and leaves the locator where it was before origins were tracked: at
// the consuming component's own dependency block.
func (c *OriginatedConnections) originOfEdge(node, kind, attachment string) constraint.ConnectionOrigin {
	if c == nil {
		return constraint.ConnectionOrigin{}
	}
	return c.byOrigin[originIdentity{Node: node, Kind: kind, Attachment: attachment}]
}

// NewOriginatedConnections pairs every connection the composition drew with the
// surface that drew it and with the delivery that connection dictates as
// authored.
//
// The connection set is the composition's own, taken whole rather than re-read
// from the authoring documents: every surface that draws a connection has
// contributed to it by the time the composition finishes, and a second reading
// of those documents could name connections the composition does not hold.
//
// The origin's site is read from the tiers that declare attachments, through
// the same collectors the attachment cascade reads them with, so a site recorded
// here and a site recorded for one of that attachment's configuration leaves
// come from one reading of one document.
//
// It returns nil when the composition or the graph is absent. A product built
// from nothing would enumerate no connection at all, and a rule invoked zero
// times reports exactly what a rule invoked and satisfied reports.
func NewOriginatedConnections(
	graph *types.IRGraph,
	topo *topology.TopologyResult,
	res *resolver.ResolveResult,
) *OriginatedConnections {
	if graph == nil || topo == nil {
		return nil
	}
	out := &OriginatedConnections{
		delivery: map[ConnectionOriginKey][]types.OrderedMap[any]{},
		byOrigin: map[originIdentity]constraint.ConnectionOrigin{},
	}
	// One site table per node, folded on first sight, because the edge walk
	// below reaches a node's connections in composition order and several of
	// them routinely share an origin.
	origins := map[string]map[string]constraint.ConnectionOrigin{}
	seen := map[ConnectionOriginKey]bool{}

	for _, edge := range topo.Edges {
		topoNode, held := topo.Nodes.Get(edge.Source)
		if !held || topoNode == nil {
			// A connection whose consumer is not in the node set has no block
			// for its locator to be named relative to. The composition does not
			// produce that shape; the guard keeps a rule from being handed a
			// coordinate whose provenance would have to be invented.
			continue
		}
		if _, folded := origins[edge.Source]; !folded {
			origins[edge.Source] = attachmentOrigins(edge.Source, topoNode, topo, res)
		}
		origin := originOf(edge, edge.Source, topoNode, origins[edge.Source], res)
		key := ConnectionOriginKey{
			Node:       edge.Source,
			Kind:       origin.Kind,
			Attachment: origin.Attachment,
			Socket:     edge.Socket,
			Alias:      edge.Alias,
			Target:     edge.Target,
		}
		out.byOrigin[originIdentity{
			Node:       edge.Source,
			Kind:       origin.Kind,
			Attachment: origin.Attachment,
		}] = origin
		if !seen[key] {
			seen[key] = true
			out.entries = append(out.entries, OriginatedConnectionEntry{
				Node:             edge.Source,
				Origin:           origin,
				Socket:           edge.Socket,
				Alias:            edge.Alias,
				Target:           edge.Target,
				SynthesizedAlias: aliasIsDerived(graph, edge.Source, edge.Socket, origin.Kind),
			})
		}
		out.delivery[key] = append(out.delivery[key], resolver.InjectMechanismPayloads(edge.Inject))
	}
	return out
}

// originOf is the surface one connection was drawn from, with the site that
// surface's own block was written at.
//
// The three kinds are read off the marks the composition stamped on the
// connection and never inferred from its content: the Release's edge surface
// stamps its own origin, an attachment's block stamps the attachment instance it
// came from, and a connection carrying neither mark was drawn by the consuming
// component's own dependency block. So the kind is a reading of what the
// composition recorded rather than an elimination over the surfaces that could
// have drawn it.
//
// A connection an attachment drew whose attachment the site table holds no entry
// for keeps the origin's identity and carries no site. That is the mechanical
// form of not inventing a locator: the coordinate still reaches the rule, and the
// finding lands at file level rather than at a path the file does not contain.
func originOf(
	edge topology.NormalizedEdge,
	nodeID string,
	topoNode *topology.TopologyNode,
	origins map[string]constraint.ConnectionOrigin,
	res *resolver.ResolveResult,
) constraint.ConnectionOrigin {
	switch {
	case edge.OriginRelease:
		return constraint.ConnectionOrigin{
			Kind:       constraint.OriginRelease,
			Site:       constraint.AuthoredSite{ScopePath: topoNode.BoundaryPath, Component: nodeID},
			Document:   constraint.AuthoringDocumentRelease,
			SourceFile: releasePathOf(res),
		}
	case edge.OriginFeatureInstance != "" || edge.OriginFeature != "":
		origin, recorded := origins[edge.OriginFeatureInstance]
		if !recorded {
			return constraint.ConnectionOrigin{
				Kind:       constraint.OriginAttachment,
				Attachment: edge.OriginFeatureInstance,
			}
		}
		return origin
	default:
		return constraint.ConnectionOrigin{
			Kind:       constraint.OriginComponent,
			Site:       componentOwnSite(topoNode, nodeID),
			Document:   constraint.AuthoringDocumentAggregate,
			SourceFile: topoNode.SourceFile,
		}
	}
}

// attachmentOrigins is, per attachment instance name a node carries, where that
// attachment's own block was written.
//
// It walks the same four declaring tiers the attachment cascade walks, through
// the same collectors, in the same order — a component's own attachment block,
// then the Aggregate's scopes outward-in, then the Release's scopes, then
// the Release's own entry for the component. Reading them through the same
// collectors is what keeps this answer and the cascade's per-leaf answer from
// being two readings free to disagree.
//
// The FIRST tier to declare an instance name wins the entry, which mirrors the
// order the cascade appends an attachment's name in. Where two tiers declare one
// name the cascade folds them into a single attachment while the composition
// still draws a connection from each block, and the two connections are
// identical in everything the composition records — so no site can be chosen per
// connection, and either tier's site is a real place that attachment is
// declared. Recording the first is the choice; naming neither would lose a
// locator that is correct for every case but that one.
func attachmentOrigins(
	nodeID string,
	topoNode *topology.TopologyNode,
	topo *topology.TopologyResult,
	res *resolver.ResolveResult,
) map[string]constraint.ConnectionOrigin {
	out := map[string]constraint.ConnectionOrigin{}
	record := func(instance string, origin constraint.ConnectionOrigin) {
		if instance == "" {
			return
		}
		if _, held := out[instance]; held {
			return
		}
		out[instance] = origin
	}

	for _, fa := range topo.FeatureAttachments[nodeID] {
		record(fa.InstanceName, constraint.ConnectionOrigin{
			Kind:       constraint.OriginAttachment,
			Attachment: fa.InstanceName,
			Site:       componentOwnSite(topoNode, nodeID),
			Document:   constraint.AuthoringDocumentAggregate,
			SourceFile: topoNode.SourceFile,
		})
	}
	if res == nil {
		return out
	}
	if topoNode.SubsystemPath != "" && res.Aggregate != nil {
		for _, sf := range collectSubsystemFeatures(types.Normalize(res.Aggregate), topoNode.SubsystemPath) {
			record(sf.name, constraint.ConnectionOrigin{
				Kind:       constraint.OriginAttachment,
				Attachment: sf.name,
				Site:       constraint.AuthoredSite{ScopePath: sf.scopePath},
				Document:   constraint.AuthoringDocumentAggregate,
				SourceFile: res.AggregatePath,
			})
		}
	}
	if res.Release == nil {
		return out
	}
	if topoNode.BoundaryPath != "" {
		for _, bf := range collectBoundaryWideFeatures(res.Release.Spec.Boundaries, topoNode.BoundaryPath) {
			record(bf.name, constraint.ConnectionOrigin{
				Kind:       constraint.OriginAttachment,
				Attachment: bf.name,
				Site:       constraint.AuthoredSite{ScopePath: bf.scopePath},
				Document:   constraint.AuthoringDocumentRelease,
				SourceFile: res.ReleasePath,
			})
		}
	}
	for _, cf := range collectReleaseComponentFeatures(res.Release.Spec.Boundaries, nodeID) {
		record(cf.name, constraint.ConnectionOrigin{
			Kind:       constraint.OriginAttachment,
			Attachment: cf.name,
			Site:       constraint.AuthoredSite{ScopePath: cf.scopePath, Component: nodeID},
			Document:   constraint.AuthoringDocumentRelease,
			SourceFile: res.ReleasePath,
		})
	}
	return out
}

// releasePathOf is the Release's file, empty when no resolve result is in
// hand. An empty one leaves a Release-drawn connection unpositioned rather
// than positioned in a document it was not written in.
func releasePathOf(res *resolver.ResolveResult) string {
	if res == nil {
		return ""
	}
	return res.ReleasePath
}
