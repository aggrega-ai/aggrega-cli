// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package cloudimport

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/plugins/internal/schemadraft"
	"github.com/aggrega-ai/aggrega-pdk/go/pkg/aggrega"
)

// draftModule drafts the Component and the Recipe of grp, the nodes of a
// module form no Component maps that take its name, once: with the format's
// module writer, a Recipe sourcing whole the module the first node by ID
// calls, as the format's Module locates it, at the version that node states.
func (c *classifier) draftModule(grp *draftGroup) {
	first := grp.nodes[0]
	var ref ModuleRef
	if first.Module != nil {
		ref = *first.Module
	}
	located := c.f.Module(ref)
	m := schemadraft.WrappedModule{
		Type:       &schemadraft.Type{Name: first.Type, Source: first.ID},
		SourceKind: located.Kind,
		URL:        located.URL,
		Version:    ref.Version,
		Inputs:     append(c.moduleParameters(grp), grp.injectInputs()...),
		Fixed:      slices.Clone(located.Fixed),
		Outputs:    slices.Clone(grp.provides),
	}
	drafted, diags := schemadraft.DraftWrapped(c.in, c.f.Draft, m, c.f.Wrap)
	// A halt on a module no Recipe can source names no node: it points at the
	// node whose module the Recipe would source.
	for i := range diags {
		if diags[i].SourceRef == "" {
			diags[i].SourceRef = first.ID
		}
	}
	c.adopt(grp, drafted, diags, modulesTogether(grp.nodes))
}

// moduleParameters returns, for each parameter a node of grp sets, the input
// its Recipe reads it from: infra.overrides under the parameter's name. Each
// parameter is read once, matched regardless of case under the spelling the
// first node by ID writes it in, and typed from the value written; a parameter
// a neighbour's value is delivered into whole is read off the inject pipe
// instead, and a member with no name sets no parameter.
func (c *classifier) moduleParameters(grp *draftGroup) []schemadraft.Input {
	w := schemadraft.WrittenResource{Type: grp.nodes[0].Type}
	var names []string
	for _, n := range grp.nodes {
		for _, p := range n.Body {
			if p.Name == "" || slices.ContainsFunc(names, func(s string) bool { return strings.EqualFold(s, p.Name) }) {
				continue
			}
			names = append(names, p.Name)
			if _, injected := grp.injected[foldedPath([]string{p.Name})]; injected {
				continue
			}
			w.Properties = append(w.Properties, schemadraft.WrittenProperty{Path: []string{p.Name},
				Value: c.parameterValue(p)})
		}
	}
	_, inputs := schemadraft.FromWritten(w)
	return inputs
}

// parameterValue is the JSON the parameter p is typed from: its leaf's, as a
// drafted property's, or an empty object or array where p holds members or
// items.
func (c *classifier) parameterValue(p Prop) json.RawMessage {
	switch {
	case p.Value != nil:
		raw, _ := c.typedValue(p.Value)
		return raw
	case len(p.Props) > 0:
		return json.RawMessage("{}")
	case len(p.Items) > 0:
		return json.RawMessage("[]")
	}
	return nil
}

// adopt records drafted, the Component and the Recipe drafted once for grp, on
// each of its nodes in ID order, with diags, what drafting them reports, and
// notes, what the decision on each node says of drafting them together; the
// first node holds the drafts. Where diags halt, or nothing is drafted, each
// node is classed drafted with nothing drafted.
func (c *classifier) adopt(grp *draftGroup, drafted schemadraft.Drafted, diags []aggrega.Diagnostic, notes string) {
	c.diags = append(c.diags, diags...)
	if halts(diags) || len(drafted.Entities) == 0 {
		for _, n := range grp.nodes {
			c.unwritten(n, grp.name)
		}
		return
	}
	for i, n := range grp.nodes {
		d := drafted.Decision
		d.SourceNode.ID, d.SourceNode.Type = n.ID, n.Type
		d.Candidates = slices.Clone(d.Candidates)
		if d.Chosen != nil {
			chosen := *d.Chosen
			d.Chosen = &chosen
		}
		d.Rationale = joinNonEmpty("; ", "drafted: "+c.why(n), c.parentNote(n), notes, c.says(n),
			drafted.Decision.Rationale)
		entity := drafted.Entities[0].Entity
		entity.SourceRef, entity.DecisionRef = n.ID, "decision:"+n.ID
		cl := c.drafted(n)
		cl.Name, cl.Decision, cl.Entity, cl.Inputs = drafted.Name, d, entity, drafted.Inputs
		cl.Recipe = drafted.Recipe
		if i == 0 {
			cl.Drafts = drafted.Entities
		}
	}
}

// modulesTogether says, for the modules drafted as one Component, that the
// draft reads every parameter any of them sets, and which module its Recipe
// sources where they call several.
func modulesTogether(nodes []*Node) string {
	if len(nodes) < 2 {
		return ""
	}
	ids := make([]string, len(nodes))
	first := moduleText(nodes[0].Module)
	var others []string
	for i, n := range nodes {
		ids[i] = n.ID
		if called := moduleText(n.Module); i > 0 && called != first {
			others = append(others, n.ID+" calls "+called)
		}
	}
	text := fmt.Sprintf("the Component and its Recipe are drafted once for %s, the modules of %s, reading every "+
		"parameter any of them sets, so each instance's Release entry sets its own", andList(ids), nodes[0].Type)
	if len(others) > 0 {
		text += fmt.Sprintf("; the Recipe sources the module %s calls, %s, where %s", nodes[0].ID, first,
			andList(others))
	}
	return text
}

// moduleText names the module ref calls and the version it states.
func moduleText(ref *ModuleRef) string {
	var path, version string
	if ref != nil {
		path, version = ref.Path, ref.Version
	}
	if path == "" {
		path = "a module of no stated path"
	}
	if version == "" {
		return path + " at no stated version"
	}
	return path + " at version " + version
}

// sharedNames halts source-refused where nodes of a module form and resources
// no Component maps take one Component name: one Component cannot both source
// a module whole and declare a resource, and the drafts of both would take one
// path. A mapping_overrides entry mapping their types to a Component the
// catalog holds makes each an instance of it.
func (c *classifier) sharedNames() *aggrega.Diagnostic {
	var clashes, types []string
	for _, grp := range c.groups {
		if !grp.module {
			continue
		}
		i := slices.IndexFunc(c.groups, func(o *draftGroup) bool { return !o.module && o.name == grp.name })
		if i < 0 {
			continue
		}
		clashes = append(clashes, fmt.Sprintf("%s, calling a module, and %s, a resource, both draft the Component %s",
			andList(typedIDs(grp.nodes)), andList(typedIDs(c.groups[i].nodes)), grp.name))
		for _, n := range append(slices.Clone(grp.nodes), c.groups[i].nodes...) {
			if !slices.Contains(types, n.Type) {
				types = append(types, n.Type)
			}
		}
	}
	if len(clashes) == 0 {
		return nil
	}
	d := Report(CodeSourceRefused, strings.Join(clashes, "; ")+", and one Component cannot both source a module "+
		"whole and declare a resource: map "+andList(types)+" through the importer's mapping_overrides setting to "+
		"a Component the catalog holds, so each is an instance of it")
	return &d
}

// typedIDs names each of nodes by its ID and type.
func typedIDs(nodes []*Node) []string {
	out := make([]string, len(nodes))
	for i, n := range nodes {
		out[i] = n.ID + " (" + n.Type + ")"
	}
	return out
}
