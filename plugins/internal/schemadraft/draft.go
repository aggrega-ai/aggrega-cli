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

// Drafted is one resource drafted for an importer.
type Drafted struct {
	// Name is the Component's name.
	Name string
	// Recipe is the Recipe's name, under f.Capability.
	Recipe string
	// Entities are the Component and the Recipe, in that order, laid out by
	// importer.CatalogFiles.
	Entities []importer.CatalogDraft
	// Decision is the decision drafting them.
	Decision aggrega.ImportDecision
}

// NameFor is the Component name a draft of typeName takes in the import in, for
// the format f, as Import names a type requested with no component:
// <target_namespace>/<category>/<slug>, under the target_namespace in's
// settings name, the category f guesses where it is one of Categories and the
// kit's fallback segment otherwise, the slug typeName through the kit's Slug.
func NameFor(in aggrega.ImportInput, f Format, typeName string) string {
	name, _ := componentName(importer.TargetNamespace(in.Settings), f, &Type{Name: typeName})
	return name
}

// DraftResource drafts a Component and a Recipe for w, a resource as its source
// writes it, for an importer of the format in.Format: through the steps Import
// takes a type through once its properties are placed, so the naming, the
// envelope, the shared assertions and the target writer are Import's.
//
// The Type and inputs are FromWritten's, placed with f's secret rule, less
// each written property f.Rules.LeftOut leaves out, and each provide is
// published under its own key. The Component is named NameFor of w.Type and
// described as drafted from a source in in.Format, f.Name where the request
// names none; the Recipe targets f.Capability, named after the Component's last
// segment, and f.Write drafts its body from the resource. The caller keeps a
// name the catalog it is handed holds already from reaching this draft:
// DraftResource drafts whatever the catalog holds.
//
// The decision, under w.ID, or the type where w names no ID, is Import's,
// against in.Catalog: draft mode, the drafted Component chosen. Its rationale
// counts the properties by the rule that placed each, names each property a
// neighbour supplies, each secret and, where secrets are deploy-time
// parameters, its parameter, each key renamed and why, and each written
// property left out with why; its swap hint names the schema import of the
// type, aggrega import --format <f.Name> <f.SchemaFile> with the type in its
// resource_types setting, which carries the vendor's descriptions, constraints
// and required properties that a source does not state. Beside what Split and
// the writer report, an info resource-drafted-from-source records the draft,
// naming the resource, the Component, the Recipe and each property left out.
//
// A resource with no type, a property or provide at a path with no segment or
// an empty one, or a provide with no key halts resource-schema-unreadable,
// naming the resource and the fault, one halt for each. Where it halts, or the
// writer halts, nothing is drafted and the diagnostics are returned alone.
//
// Every list is built in w's order, so two runs over one resource give one
// byte sequence.
func DraftResource(in aggrega.ImportInput, f Format, w WrittenResource) (Drafted, []aggrega.Diagnostic) {
	if halts := malformed(w); len(halts) > 0 {
		return Drafted{}, halts
	}
	d := placeWritten(w, f.Rules)
	name, category := componentName(importer.TargetNamespace(in.Settings), f, d.t)
	format := in.Format
	if format == "" {
		format = f.Name
	}
	spec, drafts, written := draftType(f, format, name, d.t, d.placed)
	diags := append(slices.Clone(d.diags), written...)
	if hasHalt(written) {
		return Drafted{}, diags
	}

	id := w.ID
	if id == "" {
		id = typeName(d.t)
	}
	decision := draftDecision(in.Catalog, f, id, d.t, name, d.placed, spec.res)
	decision.Rationale = writtenRationale(f, d, name, category == fallbackCategory)
	decision.SwapHint = schemaImportHint(f, d.t)
	referTo(drafts, id)
	diags = append(diags, draftedFromSource(f, d, id, name, spec.name))
	return Drafted{Name: name, Recipe: spec.name, Entities: drafts, Decision: decision}, diags
}

// malformed halts on each fault of w a draft cannot be named or placed with: no
// type, a property or provide at a path with no segment or an empty one, and a
// provide with no key.
func malformed(w WrittenResource) []aggrega.Diagnostic {
	resource := w.ID
	if resource == "" {
		resource = w.Type
	}
	halt := func(fault string) aggrega.Diagnostic {
		return aggrega.Diagnostic{
			Severity: aggrega.SeverityHalt,
			Code:     CodeResourceSchemaUnreadable,
			Message: fmt.Sprintf("the resource %q, as the source writes it, %s, and a draft of it cannot be named or "+
				"placed: write it in the source with what it lacks", resource, fault),
			SourceRef: w.ID,
		}
	}
	emptySegment := func(path []string) bool { return len(path) == 0 || slices.Contains(path, "") }
	var out []aggrega.Diagnostic
	if w.Type == "" {
		out = append(out, halt("has no type"))
	}
	for _, wp := range w.Properties {
		if emptySegment(wp.Path) {
			out = append(out, halt(fmt.Sprintf("writes a property at the path %q, which has no segment or an empty one",
				strings.Join(wp.Path, "."))))
		}
	}
	for _, pr := range w.Provides {
		if pr.Key == "" {
			out = append(out, halt(fmt.Sprintf("provides the attribute %q under no key", strings.Join(pr.Attribute, "."))))
		}
		if len(pr.Attribute) > 0 && emptySegment(pr.Attribute) {
			out = append(out, halt(fmt.Sprintf("provides %s from the attribute %q, which has an empty segment", pr.Key,
				strings.Join(pr.Attribute, "."))))
		}
	}
	return out
}

// writtenRationale states what a Component drafted for d holds, counting its
// keys by the rule that placed them and naming each property a neighbour
// supplies, each secret and, where secrets are deploy-time parameters, its
// parameter, each provides key, and where they hold, that its category is the
// fallback segment, each key that is not its property's name and why, and each
// written property left out, with why.
func writtenRationale(f Format, d writtenDraft, name string, fallback bool) string {
	var identity, placement, overrides int
	var secrets, injected, provided, parameters, renamed []string
	for _, pl := range d.placed {
		if pl.Renamed != "" {
			renamed = append(renamed, pl.Renamed)
		}
		switch pl.Pillar {
		case PillarValues:
			identity++
		case PillarInfra:
			placement++
		case PillarOverrides:
			overrides++
		case PillarSecret:
			secrets = append(secrets, vendorPath(pl.Property))
			parameters = append(parameters, fmt.Sprintf("%s is the parameter <instance>%s", vendorPath(pl.Property),
				SecretParameter(pl.Property)))
		case PillarInject:
			injected = append(injected, vendorPath(pl.Property))
		case PillarProvides:
			provided = append(provided, pl.Key)
		}
	}
	counts := nonZero(
		counted(identity, "identity key in values", "identity keys in values"),
		counted(placement, "placement key in infra", "placement keys in infra"),
		counted(overrides, "override under infra.overrides", "overrides under infra.overrides"),
		countedSecrets(secrets, f.Rules.SecretSlots),
		counted(len(injected), "property a neighbour supplies on the "+MechanismProperty+" inject mechanism ("+
			andList(injected)+")", "properties neighbours supply on the "+MechanismProperty+" inject mechanism ("+
			andList(injected)+")"),
		counted(len(provided), "provides key ("+andList(provided)+")", "provides keys ("+andList(provided)+")"),
	)
	text := fmt.Sprintf("drafted the Component %s and its Recipe on %s from %s as the source writes it: %s", name,
		f.Capability, typeName(d.t), counts)
	if !f.Rules.SecretSlots && len(parameters) > 0 {
		text += "; each secret is a deploy-time parameter named after the instance in capitalized words, written " +
			"<instance>, and supplied when the template is deployed: " + andList(parameters)
	}
	if fallback {
		text += "; " + fallbackNote(d.t)
	}
	for _, note := range renamed {
		text += "; " + note
	}
	for _, l := range d.left {
		text += "; " + l.note()
	}
	return text
}

// schemaImportHint offers the schema import of t, which drafts it from the
// vendor's schema of format f.
func schemaImportHint(f Format, t *Type) string {
	file := f.SchemaFile
	if file == "" {
		file = "schema file"
	}
	return fmt.Sprintf("to draft %s from the vendor's schema, with the descriptions, constraints and required "+
		"properties a source does not state, run `aggrega import --format %s <%s>` with %s in the importer's "+
		"resource_types setting", t.Name, f.Name, file, typeName(t))
}

// draftedFromSource records the resource id drafted from its source as the
// Component name and the Recipe recipe, naming each written property left out.
func draftedFromSource(f Format, d writtenDraft, id, name, recipe string) aggrega.Diagnostic {
	msg := fmt.Sprintf("the resource %s of the type %s is drafted from the source as the Component %s and the "+
		"Recipe %s on %s, carrying the properties the source writes; the decision's swap hint names the import "+
		"drafting it from the vendor's schema", id, typeName(d.t), name, recipe, f.Capability)
	for _, l := range d.left {
		msg += "; " + l.note()
	}
	return aggrega.Diagnostic{
		Severity:  aggrega.SeverityInfo,
		Code:      CodeResourceDraftedFromSource,
		Message:   msg,
		SourceRef: id,
	}
}
