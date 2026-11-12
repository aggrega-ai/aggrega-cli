// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package resolver

import (
	"fmt"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// ReleaseEdgeEntry is one operator-authored per-edge connection collected from a
// Release: the consuming node ID, the socket and alias the entry sits under, the
// entry itself, and where in the document the author wrote it.
//
// It exists because the same Release edge surface is read at several points —
// once for every consuming node whose component type is knowable before
// flattening, once after flattening for the nodes that only exist then, and once
// as the axis a rule over an authored entry is invoked along. All of them read
// the entries through [ReleaseEdgeEntries], so none can disagree with the others
// about which entries a Release declares or in what order.
//
// The authoring site travels with the entry rather than being recovered by a
// second walk of the same tree. It is settled where the walk reaches the entry —
// which boundary held it, and which keys the author typed to reach the component
// — and a consumer matching an entry back against the document afterwards would
// be deriving a second answer to a question this walk already answered.
type ReleaseEdgeEntry struct {
	NodeID string
	Socket string
	Alias  string

	// BoundaryScopePath is the boundary the entry was written in, joined the way
	// that axis joins its levels.
	//
	// It is empty for an entry inside the boundary normalization synthesizes to
	// hold the components an author wrote at the document's top level. That is
	// not a boundary any file spells, so an entry under one is named at the
	// top-level shape the author did write, and the empty path is what says so.
	BoundaryScopePath string

	// ComponentKeys are the authoring keys reaching the consuming component's
	// own block from inside its boundary: the component container and the key
	// the author wrote when the boundary named the component directly, and the
	// routing container, the module, the component container and the local name
	// when a routing entry named it.
	ComponentKeys []string

	Entry types.ReleaseUsesEntry
}

// ReleaseComponentEntry is one component entry a Release's boundaries write: the
// node it names, where in the document the author wrote it, and the entry itself.
//
// It is the unit [ReleaseEdgeEntries] is folded from. A reader asking where a
// per-edge entry for a component belongs — whether or not the Release writes one
// yet — reads the component entry here rather than walking the boundary tree a
// second time, so the entry it names and the entries the guards judge are found
// by one walk.
type ReleaseComponentEntry struct {
	NodeID string

	// BoundaryScopePath is the boundary the entry was written in, joined the way
	// that axis joins its levels, and empty for the boundary normalization
	// synthesizes — exactly as on [ReleaseEdgeEntry].
	BoundaryScopePath string

	// ComponentKeys are the authoring keys reaching the component's own block from
	// inside its boundary, exactly as on [ReleaseEdgeEntry].
	ComponentKeys []string

	Component types.BoundaryComponent
}

// ReleaseComponentEntries walks the Release boundary tree (outer to inner, author
// order) and returns every component entry each boundary writes, whether or not
// it carries a per-edge `uses` block.
//
// The boundary each entry sits in is composed through the one rule that composes
// a boundary's scope path, so the value splits back into the levels a locator
// names rather than resting on a separator spelled here.
//
// The components of one boundary are read through the reconciled per-boundary
// map, which folds an entry a routing entry names together with one the boundary
// names directly. Nothing is lost to that fold: a Release placing one component
// twice is refused when the document loads, so by the time anything walks this
// the two entries cannot both exist.
func ReleaseComponentEntries(release *types.Release) []ReleaseComponentEntry {
	if release == nil {
		return nil
	}
	var out []ReleaseComponentEntry
	var walk func(scopePath string, b types.Boundary)
	walk = func(scopePath string, b types.Boundary) {
		components := types.BoundaryComponents(b)
		for nodeID, bc := range components.All() {
			out = append(out, ReleaseComponentEntry{
				NodeID:            nodeID,
				BoundaryScopePath: scopePath,
				ComponentKeys:     types.BoundaryComponentLocator(b, nodeID),
				Component:         bc,
			})
		}
		for name, nested := range b.Boundaries.All() {
			walk(constraint.JoinBoundaryScope(scopePath, name, nested.Synthesized), nested)
		}
	}
	for name, b := range release.Spec.Boundaries.All() {
		walk(constraint.JoinBoundaryScope("", name, b.Synthesized), b)
	}
	return out
}

// ReleaseEdgeEntries flattens every per-edge `uses` entry on every component
// entry a Release's boundaries write into a deterministic slice: the component
// entries in the order [ReleaseComponentEntries] walks them, and each one's
// entries in author order.
func ReleaseEdgeEntries(release *types.Release) []ReleaseEdgeEntry {
	var out []ReleaseEdgeEntry
	for _, c := range ReleaseComponentEntries(release) {
		for socket, aliases := range c.Component.Uses.All() {
			for alias, e := range aliases.All() {
				out = append(out, ReleaseEdgeEntry{
					NodeID:            c.NodeID,
					Socket:            socket,
					Alias:             alias,
					BoundaryScopePath: c.BoundaryScopePath,
					ComponentKeys:     c.ComponentKeys,
					Entry:             e,
				})
			}
		}
	}
	return out
}

// ReleaseEntryCreatesEdge reports whether a Release per-edge `uses` entry draws
// a new edge, rather than configuring one that already exists.
//
// An entry naming a target creates an edge into a declared-but-unbound socket,
// pointing at an existing component. A targetless entry carries per-edge `infra`
// only and configures an edge some other authoring document already drew, so it
// draws nothing itself.
//
// The rule has one home because two stages ask it from opposite sides and must
// not disagree: the graph-build pass asks it to decide whether to draw the edge,
// and the required-socket rule asks it to decide whether a socket counts as
// bound. Were the two to answer differently, a socket the second omitted would
// be reported as missing its required binding while the graph that gets rendered
// wires it — a document rejected for a connection the build itself makes.
func ReleaseEntryCreatesEdge(entry types.ReleaseUsesEntry) bool {
	return entry.Target != ""
}

// ValidateFeatureUsesAgainstSchema validates an attachment's `uses` block against
// the attached catalog Feature's usesSchema. Two failure modes — the Feature
// declares no usesSchema, so no socket may be named; or it declares one and the
// attachment names a socket absent from it — and a silent skip on an unknown
// catalog Feature type. Used for every attachment on the one `features` surface,
// whatever the attachment produces: they all resolve through the Feature catalog
// kind.
//
// It is exported because it serves the stage the pre-flatten rules cannot reach:
// an attachment authored inside a sub-aggregate module, whose document is loaded
// only as the graph is flattened. Every attachment surface an Aggregate or a
// Release declares while those documents are still in view is measured by the
// rules declared for those surfaces instead, so the coverage boundary between the
// two is a division of labour rather than a hole.
//
// Both halts survive the relaxation on the component's own `uses` surface, and
// their survival is deliberate. An attachment does not author the Aggregate's
// topology; it participates in the topology the attached Feature declares, so the
// Feature's usesSchema is the whole of what its sockets may be. An author naming
// a socket their own component's type declares no entry for is authoring the
// product accepts, and the asymmetry between the two surfaces is what lets one
// relax without the other.
//
// Where each finding is positioned is the caller's, because only the caller
// knows which block wrote the attachment: at names the document's file and
// assembles the locator of one socket's entry. Where at names a component, every
// finding also names the component and the attachment in its detail, because a
// locator through a key holding the dialect's separator stops at the container
// that key sits in, and the detail is then the only place either appears.
func ValidateFeatureUsesAgainstSchema(entity string, at FeatureUsesSite, uses types.OrderedMap[types.FeatureUsesSocketValue], featType string, catalog CatalogLookup) []*ResolverError {
	catFeat, found := catalog.Feature(featType)
	if !found {
		return nil
	}

	if catFeat.Spec.UsesSchema.Len() == 0 {
		var errs []*ResolverError
		for socket := range uses.All() {
			errs = append(errs, &ResolverError{
				Entity:  entity,
				Field:   at.Locate(socket),
				File:    at.File,
				Message: at.subject(socket) + fmt.Sprintf("feature type %q has no usesSchema — uses declarations are not allowed", featType),
				Err:     ErrUnknownUsesSocket(),
			})
		}
		return errs
	}

	var errs []*ResolverError
	for socket := range uses.All() {
		if _, ok := catFeat.Spec.UsesSchema.Get(socket); !ok {
			errs = append(errs, &ResolverError{
				Entity:  entity,
				Field:   at.Locate(socket),
				File:    at.File,
				Message: at.subject("") + fmt.Sprintf("socket %q not defined in feature type %q usesSchema (valid: %v)", socket, featType, catFeat.Spec.UsesSchema.Keys()),
				Err:     ErrUnknownUsesSocket(),
			})
		}
	}
	return errs
}

// FeatureUsesSite says where an attachment's `uses` block was written, for the
// findings [ValidateFeatureUsesAgainstSchema] raises about its sockets.
type FeatureUsesSite struct {
	// File is the document the locators are paths inside, empty where the caller
	// holds none.
	File string

	// Locate returns the locator of one socket's entry in the block.
	Locate func(socket string) string

	// Component and Attachment name the attachment in each finding's detail;
	// both empty leaves the detail to the socket alone.
	Component  string
	Attachment string
}

// subject is the opening of a finding's detail naming the attachment it is
// about, and the socket where the rest of the detail does not name it; empty
// where the site names no component.
func (s FeatureUsesSite) subject(socket string) string {
	if s.Component == "" {
		return ""
	}
	if socket == "" {
		return fmt.Sprintf("component %q attachment %q: ", s.Component, s.Attachment)
	}
	return fmt.Sprintf("component %q attachment %q socket %q: ", s.Component, s.Attachment, socket)
}
