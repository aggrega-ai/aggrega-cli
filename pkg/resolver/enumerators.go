// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver

import (
	"slices"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
)

// coordinateSet supplies the instance sets the constraint traversals walk for
// the rules this package declares over the normalized documents.
//
// One value satisfies every axis those rules are keyed by, because the axes
// share one source: the presentation the rules read their facts through. Reading
// the coordinates off the documents instead would let the walk and the facts
// disagree about which coordinates exist, and that disagreement has no loud
// form — a rule is simply never asked about something the author wrote, and
// every build stays green.
//
// # An axis a fact already names is delegated to that fact
//
// Several axes are named by a fact as well as by an enumerator. Those delegate
// rather than recompute, so the coordinate a rule is invoked for and the content
// it then reads come from the same reading and cannot drift apart. An axis both
// documents can write to delegates to the fact naming each document's own names
// and merges them, which is still one reading per document rather than a second
// derivation of either. The axes no fact names are derived once, when this value
// is built, from the ordered documents.
//
// # Order
//
// Every axis declares the order it returns, and the declaration is part of the
// contract rather than a note about the implementation. A rule is invoked once
// per instance and raises in the order it is invoked, so enumeration order
// reaches the diagnostics a build prints; identical input yields byte-identical
// output only because each axis promises an order. No axis here ranges a Go map.
type coordinateSet struct {
	view  *normalizedView
	nodes []constraint.NodeInstance
	edges []constraint.EdgeInstance
}

// socketAlias identifies one connection within one node: the socket it wires and
// the alias naming it there. It is the identity the two authoring documents are
// merged on, so a connection both wrote is enumerated once.
type socketAlias struct {
	socket string
	alias  string
}

// newCoordinateSet derives the coordinates the presentation's documents declare.
//
// The node walk is the outer one and every derived set hangs off it, which is
// what makes the node order the outermost order of the enumeration as a whole.
func newCoordinateSet(view *normalizedView) *coordinateSet {
	c := &coordinateSet{view: view}
	for _, p := range collectComponentInstances(view.aggregate) {
		node := constraint.NodeInstance{
			ID:                   p.name,
			AuthoredName:         p.name,
			SubsystemPath:        p.subsystemPath,
			SynthesizedSubsystem: p.synthesized,
		}
		c.nodes = append(c.nodes, node)
		c.edges = append(c.edges, connectionsOf(view, node)...)
	}
	return c
}

// connectionsOf projects the connections both authoring documents wrote for one
// node into one instance per connection.
//
// It reads both documents because the rules over a connection are split across
// them: one measures what the developer wrote against the socket's contract, the
// next measures what the deployment wrote against a separate contract for the
// same socket. Each rule narrows to its own document's entries on its own, so an
// axis carrying only one document's connections would leave the other rule
// invoked for none of the connections it exists to judge — silently, because a
// rule never asked raises nothing.
//
// A connection both documents wrote is one connection and is enumerated once.
// Enumerating it per document would invoke every rule over it twice and report
// one authoring fault as two.
func connectionsOf(view *normalizedView, node constraint.NodeInstance) []constraint.EdgeInstance {
	var out []constraint.EdgeInstance
	seen := map[socketAlias]bool{}
	emit := func(socket, alias string, synthesizedAlias bool) {
		key := socketAlias{socket: socket, alias: alias}
		if seen[key] {
			return
		}
		seen[key] = true
		out = append(out, constraint.EdgeInstance{
			Node:             node,
			Socket:           socket,
			Alias:            alias,
			SynthesizedAlias: synthesizedAlias,
		})
	}

	authored := view.AuthoredUsesSocketConnections(node.ID)
	for _, socket := range authored.Keys() {
		// The short authoring form lists targets and gives each connection the
		// target's own name as its alias, so no key in the file spells that alias
		// and the locator must not name one. Which form a socket was written in
		// is read from the form's own fact rather than guessed from the alias.
		shortForm := len(view.CompactSocketTargets(node.ID, socket)) > 0
		for _, alias := range view.AuthoredUsesSocketEntryAliases(node.ID, socket) {
			emit(socket, alias, shortForm)
		}
	}
	deployed := view.ReleaseAuthoredUsesSocketConnections(node.ID)
	for _, socket := range deployed.Keys() {
		// The deployment's edge surface names an alias per entry as a key, so an
		// alias reached here is always one the operator wrote.
		for _, alias := range view.ReleaseAuthoredUsesSocketEntryAliases(node.ID, socket) {
			emit(socket, alias, false)
		}
	}
	return out
}

// Nodes returns every component instance the application declares, in instance
// -name order.
//
// The order is the name sort the rest of this package's diagnostics already read
// in, taken from the one projection that produces it, so a rule raised through
// the framework and a rule raised beside it order their findings alike.
func (c *coordinateSet) Nodes() []constraint.NodeInstance { return c.nodes }

// Edges returns every connection both authoring documents wrote, in node order,
// then the developer document's sockets in author order followed by any socket
// only the deployment wrote, and within each socket that document's aliases in
// author order.
//
// The developer document comes first because it is the document that declares
// the architecture; the deployment's entries follow, contributing the
// connections it wired that the developer did not.
func (c *coordinateSet) Edges() []constraint.EdgeInstance { return c.edges }

// Subsystems returns every scope the application's author declared, at every
// depth, in author order with an enclosing scope before the scopes nested inside
// it.
//
// A scope normalization synthesized is absent: the author declared none, so
// there is no site to report an attachment of it at.
//
// The coordinates carry no source file. This presentation holds the documents
// rather than the paths they were read from, and naming a file it does not hold
// would put a plausible wrong answer where an empty one routes a consumer to the
// document it already has.
func (c *coordinateSet) Subsystems() []constraint.AuthoredCoordinate {
	return authoredCoordinates(c.view.subsystemPaths)
}

// Boundaries returns every scope the deployment declares, at every depth, in
// author order with an enclosing scope before the scopes nested inside it.
//
// It is the sibling of [coordinateSet.Subsystems] on the other authoring
// document, and carries no source file for the same reason.
func (c *coordinateSet) Boundaries() []constraint.AuthoredCoordinate {
	return authoredCoordinates(c.view.boundaryPaths)
}

// authoredCoordinates presents recorded scope paths as the coordinate shape the
// scope traversals take, preserving the order they were recorded in.
func authoredCoordinates(paths []string) []constraint.AuthoredCoordinate {
	if len(paths) == 0 {
		return nil
	}
	out := make([]constraint.AuthoredCoordinate, 0, len(paths))
	for _, p := range paths {
		out = append(out, constraint.AuthoredCoordinate{Value: p})
	}
	return out
}

// Attachments returns the attachments declared against one node by either
// authoring document: the application's own block in author order, then any name
// the deployment declares that the application does not, in the deployment's
// author order.
//
// Both documents are read because the rules over an attachment are split across
// them, exactly as the connection axis beside this one is. One measures what the
// application declared, the next measures what the deployment declared, and each
// narrows to its own document's block on its own — so an axis carrying only the
// application's names would leave the deployment-side rule invoked for none of
// the attachments it exists to judge. That absence has no loud form: a rule never
// asked raises nothing and every build stays green.
//
// A name both documents declare is one attachment and is enumerated once.
// Enumerating it per document would invoke every rule over it twice and report
// one authoring fault as two.
func (c *coordinateSet) Attachments(node string) []string {
	names := c.view.AuthoredAttachmentNames(node)
	for _, name := range c.view.releaseAttachmentNames(node) {
		if !slices.Contains(names, name) {
			names = append(names, name)
		}
	}
	return names
}

// AttachmentSockets returns the sockets one attachment of one node WIRES —
// never the set the Feature it attaches declares — read from either authoring
// document: the application's own entries in author order, then any socket the
// deployment wires that the application does not, in the deployment's author
// order.
//
// Which of the two sets the axis means is the axis's contract rather than a
// detail of it, and the wired set is the one a rule over these sockets needs. A
// socket wired against a Feature that declares no entry for it is the fault such
// a rule reports, so an axis enumerating what the Feature declares would never
// present that rule its own subject: the offending socket is by construction
// absent from the declared set. The declared set is a fact on the view instead,
// keyed by the Feature type, and the rule compares the socket it is invoked for
// against it.
//
// Both documents are read for the reason the attachment axis above this one
// reads both. The rules over what an attachment wires are split across the
// documents, each narrowing to its own document's entries in its own body, so an
// axis carrying only the application's sockets would leave the deployment-side
// rule invoked for none of the sockets it exists to judge — and that absence has
// no loud form: a rule never asked raises nothing and every build stays green.
//
// A socket both documents wire is one socket and is enumerated once. Enumerating
// it per document would invoke every rule over it twice and report one authoring
// fault as two.
func (c *coordinateSet) AttachmentSockets(node string, attachment string) []string {
	names := c.view.AttachmentAuthoredUsesSocketNames(node, attachment)
	for _, name := range c.view.ReleaseAttachmentAuthoredUsesSocketNames(node, attachment) {
		if !slices.Contains(names, name) {
			names = append(names, name)
		}
	}
	return names
}

// Mechanisms returns the delivery mechanisms one node's injection block
// declares, in author order, delegated to the fact that names them.
func (c *coordinateSet) Mechanisms(node string) []string {
	return c.view.AuthoredInjectMechanismNames(node)
}

// DeliveryKeys returns the keys one node delivers through one mechanism,
// delegated to the fact that names them: in author order where the authored
// shape carries one, and alphabetically where it does not.
func (c *coordinateSet) DeliveryKeys(node string, mechanism string) []string {
	return c.view.AuthoredInjectDeliveryKeys(node, mechanism)
}

// SubsystemAttachments returns the attachments one declared scope of the
// application carries, in author order.
//
// It reads the same block the fact keyed by that scope reads, so the scope a
// rule is invoked for is a scope that fact can answer about.
func (c *coordinateSet) SubsystemAttachments(subsystem string) []string {
	features := c.view.subsystemFeatures[subsystem]
	return features.Keys()
}

// BoundaryAttachments returns the attachments one declared scope of the
// deployment carries, in author order, read from the same block the fact keyed
// by that scope reads.
func (c *coordinateSet) BoundaryAttachments(boundary string) []string {
	features := c.view.boundaryFeatures[boundary]
	return features.Keys()
}

// The presentation is held to every axis the rules this package declares are
// keyed by, so an axis that stops being satisfied fails to compile here rather
// than at the invocation that needed it.
var (
	_ constraint.NodeEnumerator                = (*coordinateSet)(nil)
	_ constraint.EdgeEnumerator                = (*coordinateSet)(nil)
	_ constraint.SubsystemEnumerator           = (*coordinateSet)(nil)
	_ constraint.BoundaryEnumerator            = (*coordinateSet)(nil)
	_ constraint.AttachmentEnumerator          = (*coordinateSet)(nil)
	_ constraint.AttachmentSocketEnumerator    = (*coordinateSet)(nil)
	_ constraint.MechanismEnumerator           = (*coordinateSet)(nil)
	_ constraint.DeliveryKeyEnumerator         = (*coordinateSet)(nil)
	_ constraint.SubsystemAttachmentEnumerator = (*coordinateSet)(nil)
	_ constraint.BoundaryAttachmentEnumerator  = (*coordinateSet)(nil)
)
