// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package cloudimport

import (
	"strings"

	"github.com/aggrega-ai/aggrega-pdk/go/pkg/aggrega"
	"github.com/aggrega-ai/aggrega-pdk/go/pkg/importer"
)

// modeReference is the mode of an entity that references a catalog Component.
const modeReference = "reference"

// Mapping returns r as a row of the importer kit's mapping arm: its
// Component, its category and its traits, and the last segment of the
// Component's name as the vendor token, which the kit weighs against the last
// segment of a candidate's name. The Compose importer reads its image rows'
// vendor the same way; the rule is one expression, and neither importer
// exports it.
func (r Row) Mapping() importer.Mapping {
	return importer.Mapping{
		Component: r.Component,
		Category:  r.Category,
		Traits:    r.Traits,
		Vendor:    r.Component[strings.LastIndexByte(r.Component, '/')+1:],
	}
}

// Decide decides n as the importer kit's Decide decides a node of every
// importer: the kit maps n's type through a one-row arm holding row's
// [Row.Mapping], with the mapping_overrides setting of the request's format
// layered over it, scores n against the Components of the catalog view the
// request carries, and references the best candidate where the reuse mode and
// the reuse threshold reference it, drafting a Component with the kit's hints
// otherwise. row is n's row, nil where no row maps n, so only a
// mapping_overrides entry can map its type. ref names n in the decision and
// the entity.
//
// It returns the decision, the entity — the reference to the chosen
// Component, or the kit's draft — and whether n is mapped: whether the
// decision references a Component. A catalog view lacking the row's Component
// leaves n unmapped. A mapping_overrides entry keyed by n's type takes
// precedence over row, and the Component it names is the one referenced: the
// row's Recipe realizes the reference only where the entity names the row's
// Component.
func Decide(in aggrega.ImportInput, n *Node, row *Row, ref string) (aggrega.ImportDecision, aggrega.EmittedEntity, bool) {
	var node importer.Node
	if n != nil {
		node = importer.Node{ID: n.ID, Type: n.Type}
	}
	served := importer.Format{Name: in.Format}
	if row != nil {
		served.Mappings = map[string]importer.Mapping{node.Type: row.Mapping()}
	}
	decision, entity, _ := importer.Decide(in, served, node, ref)
	return decision, entity, entity.Mode == modeReference
}
