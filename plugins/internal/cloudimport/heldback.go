// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package cloudimport

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/plugins/internal/schemadraft"
	"github.com/aggrega-ai/aggrega-pdk/go/pkg/aggrega"
)

// held returns the Component the catalog view the request carries holds
// under name. The view never holds what this import drafted before, so a
// Component it holds under a name a draft would take is one this import did
// not draft.
func (c *classifier) held(name string) (aggrega.CatalogComponent, bool) {
	if c.in.Catalog == nil {
		return aggrega.CatalogComponent{}, false
	}
	for _, comp := range c.in.Catalog.Components {
		if comp.Name == name {
			return comp, true
		}
	}
	return aggrega.CatalogComponent{}, false
}

// heldBack holds n back: its draft would take the name of held, a Component
// the catalog holds and this import did not draft, whose projection carries no
// infra key and no override the import could fill a Release entry with. It is
// neither drafted nor referenced, and a warning prints the mapping_overrides
// entry that maps its type to the Component.
func (c *classifier) heldBack(n *Node, held aggrega.CatalogComponent) {
	format := c.in.Format
	if format == "" {
		format = c.f.Name
	}
	remedy := "to reference it, write in the importer's settings the entry " + overrideEntry(format, n.Type, held) +
		", then fill the instance's infra in the Release"
	if held.Labels[schemadraft.LabelCategory] == "" {
		remedy = fmt.Sprintf("%s carries no %s label, and the importer kit references the Component a mapping "+
			"names only where that label matches the mapping's category: label it in the catalog; %s", held.Name,
			schemadraft.LabelCategory, remedy)
	}
	d := decisionFor(n, joinNonEmpty("; ", "held back: the Component "+held.Name+" a draft of "+n.Type+" takes "+
		"is in the catalog, and this import did not draft it, so the resource is neither drafted nor referenced",
		c.parentNote(n), c.says(n)))
	d.SwapHint = remedy
	c.set(n, &Classified{Class: ClassHeldBack, Name: held.Name, Decision: d})
	c.note(n, Report(CodeComponentExists, fmt.Sprintf("%s (%s) is held back: the catalog holds the Component %s "+
		"a draft of it takes, which this import did not draft, so the resource is neither drafted nor referenced; "+
		"%s", n.ID, n.Type, held.Name, remedy)))
}

// overrideMapping is one entry of the mapping_overrides setting, in the shape
// the importer kit reads. It deliberately restates the shape of the kit's own
// entry, which the kit decodes and does not export; the test applying a
// printed entry through the kit's Decide cross-checks the two.
type overrideMapping struct {
	Component string            `json:"component"`
	Category  string            `json:"category,omitempty"`
	Traits    map[string]string `json:"traits,omitempty"`
	Vendor    string            `json:"vendor,omitempty"`
}

// overrideEntry is the settings document mapping typ to held for the format,
// built from what the catalog holds of the Component, so the importer kit
// references it under its default reuse mode and threshold where the Component
// carries a category label: its name, its category label, its other labels as
// traits — its category label where it carries no other — and the last word of
// its name as the vendor token.
func overrideEntry(format, typ string, held aggrega.CatalogComponent) string {
	entry := overrideMapping{Component: held.Name, Category: held.Labels[schemadraft.LabelCategory]}
	for _, key := range slices.Sorted(maps.Keys(held.Labels)) {
		if key != schemadraft.LabelCategory {
			if entry.Traits == nil {
				entry.Traits = map[string]string{}
			}
			entry.Traits[key] = held.Labels[key]
		}
	}
	if entry.Traits == nil && entry.Category != "" {
		entry.Traits = map[string]string{schemadraft.LabelCategory: entry.Category}
	}
	last := held.Name[strings.LastIndexByte(held.Name, '/')+1:]
	entry.Vendor = last[strings.LastIndexByte(last, '-')+1:]

	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(map[string]map[string]map[string]overrideMapping{"mapping_overrides": {format: {typ: entry}}})
	return strings.TrimSpace(b.String())
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
