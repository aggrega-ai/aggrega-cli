// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package cloudimport

import (
	"fmt"
)

// holds reports whether the catalog view the request carries holds a
// Component named name. The view never holds what this import drafted before,
// so a Component it holds under a name a draft would take is one this import
// did not draft.
func (c *classifier) holds(name string) bool {
	if c.in.Catalog == nil {
		return false
	}
	for _, comp := range c.in.Catalog.Components {
		if comp.Name == name {
			return true
		}
	}
	return false
}

// heldBack holds n back: its draft would take name, a Component the catalog
// holds and this import did not draft, whose projection carries no infra key
// and no override the import could fill a Release entry with. It is neither
// drafted nor referenced, and a warning names the mapping_overrides entry
// that maps its type to the Component.
func (c *classifier) heldBack(n *Node, name string) {
	format := c.in.Format
	if format == "" {
		format = c.f.Name
	}
	remedy := fmt.Sprintf("map %s to %s through the importer's mapping_overrides setting, under "+
		"mapping_overrides.%s, then fill the instance's infra in the Release", n.Type, name, format)
	d := decisionFor(n, joinNonEmpty("; ", "held back: the Component "+name+" a draft of "+n.Type+" takes is in "+
		"the catalog, and this import did not draft it, so the resource is neither drafted nor referenced", c.says(n)))
	d.SwapHint = remedy
	c.set(n, &Classified{Class: ClassHeldBack, Name: name, Decision: d})
	c.note(n, Report(CodeComponentExists, fmt.Sprintf("%s (%s) is held back: the catalog holds the Component %s "+
		"a draft of it takes, which this import did not draft, so the resource is neither drafted nor referenced: "+
		"%s", n.ID, n.Type, name, remedy)))
}

// writtenPaths are the paths, dotted, of the properties n writes, in its
// order: what the Release entry of a node mapped with no row lists for the
// author to place.
func writtenPaths(n *Node) []string {
	var out []string
	for _, l := range leaves(n.Body) {
		out = append(out, pathText(l.path))
	}
	return out
}
