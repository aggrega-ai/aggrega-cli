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

// groupModule adds n, a node of a module form no Component maps, to the group
// of name where the format's Module locates its module, recording the source
// it gives; where that source has no kind or no address, n is not carried.
func (c *classifier) groupModule(n *Node, name string) {
	ref := moduleRef(n)
	located := c.f.Module(ref)
	if located.Kind == "" || located.URL == "" {
		c.unlocated(n, ref)
		return
	}
	c.located[n.ID] = located
	c.group(n, name)
}

// moduleRef is the module n calls, the empty one where its reader names none.
func moduleRef(n *Node) ModuleRef {
	if n.Module == nil {
		return ModuleRef{}
	}
	return *n.Module
}

// unlocated classes n, a node of a module form calling ref, not carried: the
// format locates no source for the module, so no Recipe sourcing it is
// drafted, and a linked-source-not-read warning names n and the module.
func (c *classifier) unlocated(n *Node, ref ModuleRef) {
	module := moduleName(ref)
	c.set(n, &Classified{Class: ClassNotCarried, Decision: decisionFor(n, joinNonEmpty("; ", "not carried: "+
		c.why(n)+", and the importer's format locates no source for "+module+", so no Recipe sourcing it is "+
		"drafted and nothing is drafted for it", c.parentNote(n), c.says(n)))})
	c.note(n, Report(CodeLinkedSourceNotRead, fmt.Sprintf("%s (%s) calls %s, whose source the importer's format "+
		"does not locate, and no Component maps it, so it is not carried and nothing is drafted for it: where it is "+
		"wanted, map %s through the importer's mapping_overrides setting to a Component the catalog holds, or write "+
		"its instance in the Aggregate", n.ID, n.Type, module, n.Type)))
}

// moduleName names the module ref calls, by its path, else by its prefix,
// and the version it states.
func moduleName(ref ModuleRef) string {
	name := "a module of no stated path"
	switch {
	case ref.Path != "":
		name = "the module " + ref.Path
	case ref.Prefix != "":
		name = "the module of the prefix " + ref.Prefix
	}
	if ref.Version == "" {
		return name + " at no stated version"
	}
	return name + " at version " + ref.Version
}

// holdBackShared holds back the nodes of each module group whose Component
// name a resource group of this import takes, and drafts nothing for that
// module group: one Component takes one drafted Recipe, which cannot both
// declare the resources and source a module whole.
func (c *classifier) holdBackShared() {
	var held []*draftGroup
	for _, grp := range c.groups {
		if !grp.module {
			continue
		}
		i := slices.IndexFunc(c.groups, func(o *draftGroup) bool { return !o.module && o.name == grp.name })
		if i < 0 {
			continue
		}
		for _, n := range grp.nodes {
			delete(c.groupOf, n.ID)
			c.heldBeside(n, grp.name, c.groups[i].nodes)
		}
		held = append(held, grp)
	}
	c.groups = slices.DeleteFunc(c.groups, func(grp *draftGroup) bool { return slices.Contains(held, grp) })
}

// heldBeside holds back n, a node of a module form whose drafted Component
// would take name, the Component this import drafts from resources: neither
// drafted nor referenced, warned component-exists, its warning and its
// decision's swap hint saying how the module is deployed by hand.
func (c *classifier) heldBeside(n *Node, name string, resources []*Node) {
	ids := make([]string, len(resources))
	for i, r := range resources {
		ids[i] = r.ID
	}
	url := c.located[n.ID].URL
	why := fmt.Sprintf("the Component %s a draft of %s takes is the one this import drafts from %s, and one "+
		"Component takes one drafted Recipe, which cannot both declare a resource and source the module %s whole, "+
		"so the module is neither drafted nor referenced", name, n.Type, andList(ids), url)
	remedy := "where the module is wanted, write in the Aggregate an instance of a Component of another name, with " +
		"a Recipe implementing it that sources the module " + url
	d := decisionFor(n, joinNonEmpty("; ", "held back: "+why, c.parentNote(n), c.says(n)))
	d.SwapHint = remedy
	c.set(n, &Classified{Class: ClassHeldBack, Name: name, Decision: d})
	c.note(n, Report(CodeComponentExists, fmt.Sprintf("%s (%s) is held back: %s; %s", n.ID, n.Type, why, remedy)))
}

// draftModule drafts the Component and the Recipe of grp, the nodes of a
// module form no Component maps that take its name, once: with the format's
// module writer, a Recipe sourcing whole the module the first node by ID
// calls, from where the format's Module locates it, at the version that node
// states. Each parameter a secret feeds is left out of the Recipe, and
// value-not-carried warns of it on each node setting it.
func (c *classifier) draftModule(grp *draftGroup) {
	first := grp.nodes[0]
	located := c.located[first.ID]
	inputs, secrets := c.moduleParameters(grp)
	m := schemadraft.WrappedModule{
		Type:       &schemadraft.Type{Name: first.Type, Source: first.ID},
		SourceKind: located.Kind,
		URL:        located.URL,
		Version:    moduleRef(first).Version,
		Inputs:     append(inputs, grp.injectInputs()...),
		Fixed:      slices.Clone(located.Fixed),
		Outputs:    slices.Clone(grp.provides),
	}
	for _, n := range grp.nodes {
		c.leaveOutSecrets(n, grp.name, secrets)
	}
	drafted, diags := schemadraft.DraftWrapped(c.in, c.f.Draft, m, c.f.Wrap)
	// A halt on a module no Recipe can source names no node: it points at the
	// node whose module the Recipe would source.
	for i := range diags {
		if diags[i].SourceRef == "" {
			diags[i].SourceRef = first.ID
		}
	}
	c.adopt(grp, drafted, diags, joinNonEmpty("; ", modulesTogether(grp.nodes), secretsText(secrets)))
}

// moduleParameters returns, for each parameter a node of grp sets, the input
// its Recipe reads it from: infra.overrides under the parameter's name. Each
// parameter is read once, matched regardless of case under the spelling the
// first node by ID writes it in, and typed from the value written; a parameter
// a neighbour's value is delivered into whole is read off the inject pipe
// instead, and a member with no name sets no parameter. A parameter a secret
// feeds on any node — a secure parameter, a credential, or a value the
// format's secret rule names — is no input, the draft never carrying a
// secret's value: it is returned beside the inputs, under that spelling.
func (c *classifier) moduleParameters(grp *draftGroup) (inputs []schemadraft.Input, secrets []string) {
	w := schemadraft.WrittenResource{Type: grp.nodes[0].Type}
	var names []string
	fed := map[string]bool{}
	for _, n := range grp.nodes {
		for _, p := range n.Body {
			if p.Name == "" {
				continue
			}
			if c.secretFed(p) {
				fed[foldedPath([]string{p.Name})] = true
			}
			if slices.ContainsFunc(names, func(s string) bool { return strings.EqualFold(s, p.Name) }) {
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
	typ, written := schemadraft.FromWritten(w)
	secret := c.f.Draft.Rules.Secret
	for _, in := range written {
		if fed[foldedPath(in.Property.Path)] || secret != nil && secret(typ, in.Property) {
			secrets = append(secrets, in.Property.Name)
			continue
		}
		inputs = append(inputs, in)
	}
	return inputs, secrets
}

// secretFed reports whether a secret feeds the parameter p: a secure
// parameter or a credential read anywhere in its value.
func (c *classifier) secretFed(p Prop) bool {
	for _, l := range leaves([]Prop{p}) {
		if _, secret := c.typedValue(l.value); secret {
			return true
		}
	}
	return false
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

// leaveOutSecrets warns value-not-carried of each parameter n sets that is one
// of secrets, matched regardless of case: the Recipe drafted for component
// does not read it, and the author binds it in that Recipe.
func (c *classifier) leaveOutSecrets(n *Node, component string, secrets []string) {
	var warned []string
	for _, p := range n.Body {
		named := func(s string) bool { return strings.EqualFold(s, p.Name) }
		if p.Name == "" || slices.ContainsFunc(warned, named) || !slices.ContainsFunc(secrets, named) {
			continue
		}
		warned = append(warned, p.Name)
		c.note(n, Report(CodeValueNotCarried, fmt.Sprintf("%s sets the parameter %s of the module %s, which a secret "+
			"feeds and the draft never carries, so the Recipe drafted for %s does not read it: bind %s in that "+
			"Recipe, from where the secret is kept", n.ID, p.Name, c.located[n.ID].URL, component, p.Name)))
	}
}

// secretsText says which parameters the Recipe drafted for a module leaves
// out, a secret feeding each, empty where it leaves out none.
func secretsText(secrets []string) string {
	if len(secrets) == 0 {
		return ""
	}
	return "the Recipe does not read " + andList(secrets) + ", which a secret feeds, for the author to bind in it"
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
	first := moduleName(moduleRef(nodes[0]))
	var others []string
	for i, n := range nodes {
		ids[i] = n.ID
		if called := moduleName(moduleRef(n)); i > 0 && called != first {
			others = append(others, n.ID+" calls "+called)
		}
	}
	text := fmt.Sprintf("the Component and its Recipe are drafted once for %s, the modules of %s, reading every "+
		"parameter any of them sets, so each instance's Release entry sets its own", andList(ids), nodes[0].Type)
	if len(others) > 0 {
		text += fmt.Sprintf("; the Recipe sources what %s calls, %s, where %s", nodes[0].ID, first,
			andList(others))
	}
	return text
}
