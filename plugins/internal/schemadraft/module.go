// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package schemadraft

import (
	"fmt"
	"slices"
	"strings"

	"github.com/aggrega-ai/aggrega-pdk/go/pkg/aggrega"
	"github.com/aggrega-ai/aggrega-pdk/go/pkg/importer"
)

// CodeModuleVersionUnpinned warns of a module-wrapping draft whose exact module
// version the source does not state: the drafted Recipe's spec.source.version is
// left empty for the author to write.
const CodeModuleVersionUnpinned = "module-version-unpinned"

// WrappedModule is a published module drafted as a Recipe that sources it whole.
type WrappedModule struct {
	// Type is the resource type the module realizes, which names the Component
	// and gives its category.
	Type *Type
	// SourceKind is the Recipe's source kind, bicep-module-oci for a Bicep
	// module.
	SourceKind string
	// URL is the module's address and Version its version, a tag or a digest;
	// an empty Version leaves the Recipe's source version for the author and
	// warns.
	URL, Version string
	// Inputs are the parameters the source sets, each read from
	// infra.overrides under the parameter's name, or off the inject pipe; the
	// properties are marked Written, since no vendor schema describes them.
	Inputs []Input
	// Fixed are the parameters the Recipe binds itself, in order, which no
	// Release sets; an input of the same name is left out.
	Fixed []importer.Entry
	// Outputs are the module outputs a neighbour reads, each published under
	// its key from the attribute that names the output.
	Outputs []Provide
}

// ModuleWriter drafts the target parts of the Recipe that sources m: its
// RecipeDraft.Source holds the source block, and its ValuesMapping binds each
// input and each fixed parameter. DraftWrapped completes it like any drafted
// Recipe.
type ModuleWriter func(m WrappedModule) (importer.RecipeDraft, []aggrega.Diagnostic)

// DraftWrapped drafts a Component and a Recipe for m, a published module no
// catalog Component maps, in the import in of the format f: the Component is
// named NameFor of m.Type and carries the provides keys of m.Outputs; the Recipe
// targets f.Capability, is named after the Component's last segment, reads its
// inputs from infra.overrides and the inject pipe, and w drafts the rest of it
// from the module. The naming, the shared assertions and the decision are those
// DraftResource takes. A Fixed parameter is never an input.
//
// Without a version the Recipe's source version stays empty and a warning
// module-version-unpinned names the module and the field to write. A module with
// no type, no URL or no source kind halts resource-schema-unreadable. Where it
// halts, or w halts, nothing is drafted and the diagnostics are returned alone.
// Every list is built in m's order, so two runs give one byte sequence.
func DraftWrapped(in aggrega.ImportInput, f Format, m WrappedModule, w ModuleWriter) (Drafted, []aggrega.Diagnostic) {
	if halts := malformedModule(m); len(halts) > 0 {
		return Drafted{}, halts
	}
	m = withoutFixed(m)
	id := m.Type.Source
	if id == "" {
		id = typeName(m.Type)
	}
	name, category := componentName(importer.TargetNamespace(in.Settings), f, m.Type)
	format := in.Format
	if format == "" {
		format = f.Name
	}

	placed, res, rules := placedModule(m)
	spec := recipeSpec{component: name, format: format, res: res, rules: rules, module: &m,
		name: lastSegment(name), implements: name + ":" + draftVersion}
	body, diags := w(m)
	if hasHalt(diags) {
		return Drafted{}, diags
	}
	source := body.Source
	body.Source = ""
	component := importer.DraftComponent(ComponentFor(name, m.Type, placed, format, f.Rules))
	recipe := importer.DraftRecipe(recipeDraft(f, spec, body))
	recipe.Entity.Body = withSource(recipe.Entity.Body, source)

	decision := draftDecision(in.Catalog, f, id, m.Type, name, placed, res)
	decision.Rationale = moduleRationale(f, m, name, placed, category == fallbackCategory)
	drafts := []importer.CatalogDraft{component, recipe}
	referTo(drafts, id)
	if m.Version == "" {
		diags = append(diags, moduleVersionUnpinned(m, id))
	}
	return Drafted{Name: name, Recipe: spec.name, Entities: drafts, Decision: decision}, diags
}

// malformedModule halts on a module a draft cannot be written for: no type, no
// URL or no source kind.
func malformedModule(m WrappedModule) []aggrega.Diagnostic {
	var out []aggrega.Diagnostic
	halt := func(fault string) {
		out = append(out, aggrega.Diagnostic{
			Severity: aggrega.SeverityHalt,
			Code:     CodeResourceSchemaUnreadable,
			Message: fmt.Sprintf("the module %q %s, and a Recipe sourcing it cannot be written: state it in the source "+
				"the module is read from", m.URL, fault),
		})
	}
	if m.Type == nil || m.Type.Name == "" {
		halt("realizes no resource type")
	}
	if m.URL == "" {
		halt("has no address")
	}
	if m.SourceKind == "" {
		halt("has no source kind")
	}
	return out
}

// Parameter is the module parameter an input of a wrapped module sets: the key
// under infra.overrides, which is the parameter's name, or for an input read off
// the inject pipe, whose key is the pipe's, the name of its property.
func (in Input) Parameter() string {
	if in.Source.Pillar == PillarInject {
		return in.Property.Name
	}
	return in.Source.Key
}

// withoutFixed returns m without the inputs a Fixed parameter of the same name
// binds.
func withoutFixed(m WrappedModule) WrappedModule {
	var kept []Input
	for _, in := range m.Inputs {
		if !slices.ContainsFunc(m.Fixed, func(e importer.Entry) bool { return e.Key == in.Parameter() }) {
			kept = append(kept, in)
		}
	}
	m.Inputs = kept
	return m
}

// placedModule places the inputs and outputs of m as Split places a type's
// properties: an override or a property on the inject pipe for each input, in
// m's order, a provides key for each output; with the Resource a writer reads
// and the rule of each input.
func placedModule(m WrappedModule) ([]Placed, Resource, []string) {
	res := Resource{Type: m.Type, Inputs: slices.Clone(m.Inputs), Provides: slices.Clone(m.Outputs)}
	var placed []Placed
	var rules []string
	for _, in := range m.Inputs {
		rule := ruleOverride
		if in.Source.Pillar == PillarInject {
			rule = injectRule(in.Property)
		}
		rules = append(rules, rule)
		placed = append(placed, Placed{Property: in.Property, Pillar: in.Source.Pillar, Key: in.Source.Key,
			Rule: rule, Required: in.Required})
	}
	for _, pr := range m.Outputs {
		attribute := pr.Attribute
		if len(attribute) == 0 {
			attribute = []string{pr.Key}
		}
		p := &Property{Name: attribute[len(attribute)-1], Path: slices.Clone(attribute), Kinds: []Kind{KindString},
			ReadOnly: true, Written: true}
		placed = append(placed, Placed{Property: p, Pillar: PillarProvides, Key: pr.Key, Rule: ruleProvides})
	}
	return placed, res, rules
}

// withSource writes block as spec.source of the Recipe body, after the line
// naming what the Recipe implements.
func withSource(body, block string) string {
	if block == "" {
		return body
	}
	var b strings.Builder
	b.WriteString("  source:\n")
	for _, line := range strings.Split(strings.TrimSuffix(block, "\n"), "\n") {
		if line == "" {
			b.WriteString("\n")
			continue
		}
		b.WriteString("    " + line + "\n")
	}
	rows := strings.SplitAfter(body, "\n")
	at := slices.IndexFunc(rows, func(r string) bool { return strings.HasPrefix(r, "  implements:") })
	if at < 0 {
		return body + b.String()
	}
	return strings.Join(rows[:at+1], "") + b.String() + strings.Join(rows[at+1:], "")
}

// moduleVersionUnpinned warns that the module of m states no version, so the
// Recipe drafted for it leaves spec.source.version empty.
func moduleVersionUnpinned(m WrappedModule, id string) aggrega.Diagnostic {
	return aggrega.Diagnostic{
		Severity: aggrega.SeverityWarn,
		Code:     CodeModuleVersionUnpinned,
		Message: fmt.Sprintf("the source states no version of the module %s, so the drafted Recipe leaves "+
			"spec.source.version empty and does not validate until it is written: set spec.source.version to the "+
			"module's version, a tag or a sha256: manifest digest", m.URL),
		SourceRef: id,
	}
}

// moduleRationale states what a Component and Recipe drafted for the module m
// hold, counting the inputs by where they are read, naming the module and each
// input and output, and where they hold, that the category is the fallback
// segment.
func moduleRationale(f Format, m WrappedModule, name string, placed []Placed, fallback bool) string {
	var overrides, injected, provided []string
	for _, pl := range placed {
		switch pl.Pillar {
		case PillarOverrides:
			overrides = append(overrides, pl.Key)
		case PillarInject:
			injected = append(injected, pl.Key)
		case PillarProvides:
			provided = append(provided, pl.Key)
		}
	}
	var fixed []string
	for _, e := range m.Fixed {
		fixed = append(fixed, e.Key)
	}
	counts := nonZero(
		counted(len(overrides), "parameter read from infra.overrides ("+andList(overrides)+")",
			"parameters read from infra.overrides ("+andList(overrides)+")"),
		counted(len(injected), "parameter a neighbour supplies on the "+MechanismProperty+" inject mechanism ("+
			andList(injected)+")", "parameters neighbours supply on the "+MechanismProperty+" inject mechanism ("+
			andList(injected)+")"),
		counted(len(provided), "provides key ("+andList(provided)+")", "provides keys ("+andList(provided)+")"),
	)
	text := fmt.Sprintf("drafted the Component %s and its Recipe on %s, which sources the published module %s whole "+
		"and reads no vendor schema: %s", name, f.Capability, m.URL, counts)
	if len(fixed) > 0 {
		text += "; the Recipe binds " + andList(fixed) + " itself, which no Release sets"
	}
	if fallback {
		text += "; " + fallbackNote(m.Type)
	}
	return text
}
