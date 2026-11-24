// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package schemadraft

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/aggrega-ai/aggrega-pdk/go/pkg/aggrega"
	"github.com/aggrega-ai/aggrega-pdk/go/pkg/importer"
)

// ParseFunc parses source as the syntactic format named syntax into its
// canonical tree: the host's parse, which an importer's import export wires in.
type ParseFunc func(syntax string, source []byte) (json.RawMessage, error)

// Format is everything Import needs that depends on the vendor format.
type Format struct {
	// Name is the format's name, the <format> of the import:<format>
	// capability the importer advertises. Capability is the provider every
	// Recipe the format drafts targets, whatever target the request names.
	// Syntax is the syntactic format a source is parsed as.
	Name, Capability, Syntax string
	// FoldCase and CountOnly are passed to Select.
	FoldCase, CountOnly bool
	// List reads the types the request's sources hold, without lifting them,
	// halting on a source it cannot read.
	List func(in aggrega.ImportInput, parse ParseFunc) ([]TypeRef, []aggrega.Diagnostic)
	// Resolve chooses among the types a requested name matches more than once;
	// nil where names are unique.
	Resolve func(name string, refs []TypeRef) (TypeRef, []aggrega.Diagnostic)
	// Lift reads a selected type whole, or halts.
	Lift func(ref TypeRef) (*Type, []aggrega.Diagnostic)
	// Refuse returns the resource-type-refused halt for a type the provider
	// cannot realize, nil to draft it; nil refuses none.
	Refuse func(t *Type) *aggrega.Diagnostic
	// Rules are the format's placement rules, passed to Split.
	Rules Rules
	// Category guesses the category of a type: one of Categories, or "" where
	// it has none, which names the Component under the kit's fallback segment.
	// Nil guesses none.
	Category func(t *Type) string
	// Write drafts the target parts of a type's Recipe.
	Write Writer
}

// fallbackCategory is the segment a drafted Component is named under where the
// format guesses no category. It restates the importer kit's own fallback,
// which the kit does not export, so a type drafted here and a node the kit
// drafts take one segment.
const fallbackCategory = "component"

// Import turns one import request into the Components, Recipes, decisions and
// diagnostics it drafts for the format f, and lays the drafts out under
// catalog/ as importer.CatalogFiles does.
//
// It reads the settings, lists the types the sources hold, selects those the
// resource_types setting names, and for each selected type, once however often
// it is requested under one component, lifts it, offers it to f.Refuse, places
// its properties with Split, names its Component and its Recipe, guards the
// name, decides it, and drafts the Component with ComponentFor and the Recipe
// from f.Write. The Recipe targets f.Capability, whatever in.Target names.
//
// A type requested with no component is drafted as the Component
// <target_namespace>/<category>/<slug> and the Recipe <slug>, the slug being
// the type's name through the kit's Slug. Where the catalog the request carries
// holds that name with values keys other than the draft's, the import halts
// component-exists; with the same keys the draft refreshes it. A type requested
// with a component the catalog holds is drafted as a Recipe alone implementing
// it at the version the catalog holds, named <last segment>-<slug>; with one the
// catalog does not hold, as a Component of that name and its Recipe named after
// its last segment, warning component-drafted-new. Two requests drafting one
// path halt component-exists naming both.
//
// Each decision is the kit's Decide against the catalog the request carries,
// with no mapping arm and no setting of the request, so no candidate is ever
// referenced: the chosen target is always the drafted or implemented Component,
// in draft mode.
//
// Any halt, raised anywhere, drafts nothing: the output carries the
// diagnostics alone. Where the sources hold no type and none is requested, the
// import warns no-nodes for each source and drafts nothing.
func Import(in aggrega.ImportInput, f Format, parse ParseFunc) aggrega.ImportOutput {
	settings, halt := ReadSettings(in.Settings)
	if halt != nil {
		return aggrega.ImportOutput{Diagnostics: []aggrega.Diagnostic{*halt}}
	}
	if in.Format != f.Name {
		return aggrega.ImportOutput{Diagnostics: noTypes(in, f)}
	}
	refs, diags := f.List(in, parse)
	if hasHalt(diags) {
		return aggrega.ImportOutput{Diagnostics: diags}
	}
	selected, d := Select(refs, settings.ResourceTypes, f.FoldCase, f.CountOnly, f.Resolve)
	diags = append(diags, d...)
	if hasHalt(diags) {
		return aggrega.ImportOutput{Diagnostics: diags}
	}
	if len(selected) == 0 {
		return aggrega.ImportOutput{Diagnostics: append(diags, noTypes(in, f)...)}
	}

	run := &importRun{in: in, f: f, settings: settings, diags: diags, drafted: map[string]string{}}
	selected = distinct(selected)
	for _, sel := range selected {
		run.draft(sel, selected)
	}
	if hasHalt(run.diags) {
		return aggrega.ImportOutput{Diagnostics: run.diags}
	}
	out := aggrega.ImportOutput{Diagnostics: run.diags, Decisions: run.decisions}
	for _, d := range run.drafts {
		out.Entities = append(out.Entities, d.Entity)
	}
	out.Files = importer.CatalogFiles(in.Settings, run.drafts, run.decisions)
	return out
}

// noTypes warns no-nodes for each source of in, none of which holds a type the
// import drafts.
func noTypes(in aggrega.ImportInput, f Format) []aggrega.Diagnostic {
	var out []aggrega.Diagnostic
	for _, src := range importer.Sources(in) {
		out = append(out, importer.NoNodes(importer.Named(in, src), f.Name))
	}
	return out
}

// importRun is one import in progress: what it has drafted, decided and
// reported so far.
type importRun struct {
	in        aggrega.ImportInput
	f         Format
	settings  Settings
	diags     []aggrega.Diagnostic
	drafts    []importer.CatalogDraft
	decisions []aggrega.ImportDecision
	// drafted names the request that drafted each path, to find two drafting
	// one.
	drafted map[string]string
}

// draft drafts the selected type sel, one of selected.
func (r *importRun) draft(sel Selected, selected []Selected) {
	t, d := r.f.Lift(sel.Ref)
	r.diags = append(r.diags, d...)
	if hasHalt(d) {
		return
	}
	if t == nil {
		r.diags = append(r.diags, noBody(sel.Ref))
		return
	}
	if r.f.Refuse != nil {
		if refusal := r.f.Refuse(t); refusal != nil {
			r.diags = append(r.diags, *refusal)
			return
		}
	}
	placed, splitDiags := Split(t, r.f.Rules)
	request := requestText(t, sel.Component)
	slug := importer.Slug(t.Name)

	name, category := sel.Component, ""
	if name == "" {
		category = fallbackCategory
		if r.f.Category != nil {
			if guessed := r.f.Category(t); slices.Contains(Categories, guessed) {
				category = guessed
			}
		}
		name = r.settings.TargetNamespace + "/" + category + "/" + slug
	}
	held, holds := heldComponent(r.in.Catalog, name)
	recipeOnly := sel.Component != "" && holds
	refresh := false
	switch {
	case sel.Component == "" && holds:
		if drafted := placedKeys(placed, PillarValues); !slices.Equal(sortedCopy(held.Values), sortedCopy(drafted)) {
			r.diags = append(r.diags, componentExists(name, held.Values, drafted, t))
			return
		}
		refresh = true
	case sel.Component != "" && !holds:
		r.diags = append(r.diags, componentDraftedNew(name, t))
	}

	spec := recipeSpec{component: name, secretSlots: r.f.Rules.SecretSlots}
	var drafts []importer.CatalogDraft
	if recipeOnly {
		res, rules, match := recipeOnlyResource(t, placed, held)
		spec.res, spec.rules, spec.recipeOnly = res, rules, &match
		spec.name = lastSegment(name) + "-" + slug
		spec.implements = name
		if held.Version != "" {
			spec.implements += ":" + held.Version
		}
		for _, u := range res.Unmapped {
			r.diags = append(r.diags, providesUnmapped(name, t, u))
		}
	} else {
		r.diags = append(r.diags, splitDiags...)
		spec.res, spec.rules = resourceFor(t, placed)
		spec.name = lastSegment(name)
		spec.implements = name + ":" + draftVersion
		drafts = append(drafts, importer.DraftComponent(ComponentFor(name, t, placed, r.f.Name, r.f.Rules)))
	}
	body, d := r.f.Write(spec.res)
	r.diags = append(r.diags, d...)
	if hasHalt(d) {
		return
	}
	drafts = append(drafts, importer.DraftRecipe(recipeDraft(r.f, spec, body)))

	for _, draft := range drafts {
		path := draftedPath(draft)
		if earlier, ok := r.drafted[path]; ok {
			r.diags = append(r.diags, twoRequestsDraftOnePath(earlier, request, path))
			return
		}
		r.drafted[path] = request
	}

	id := decisionID(t, sel, name, selected)
	decision := r.decide(id, t, name, placed, spec)
	if recipeOnly {
		decision.Rationale = recipeOnlyRationale(r.f, t, name, spec)
	} else {
		decision.Rationale = draftRationale(r.f, t, name, placed, refresh, category == fallbackCategory)
	}
	r.decisions = append(r.decisions, decision)
	for _, draft := range drafts {
		draft.Entity.SourceRef, draft.Entity.DecisionRef = id, "decision:"+id
		r.drafts = append(r.drafts, draft)
	}
}

// decide is the decision for the type t, drafted as the Component name or as a
// Recipe implementing it, under the identifier id: the kit's Decide against the
// catalog the request carries, less name itself, with no mapping arm and no
// setting of the request, so it weighs the catalog by category and shape alone
// and references nothing. Its chosen target is name, in draft mode.
func (r *importRun) decide(id string, t *Type, name string, placed []Placed, spec recipeSpec) aggrega.ImportDecision {
	node := importer.Node{ID: id, Type: t.Name}
	node.Fields = append(placedKeys(placed, PillarValues), placedKeys(placed, PillarInfra)...)
	node.Secrets = placedKeys(placed, PillarSecret)
	in := aggrega.ImportInput{Format: r.f.Name, Catalog: without(r.in.Catalog, name)}
	decision, _, _ := importer.Decide(in, importer.Format{Name: r.f.Name}, node, id)

	tier := ""
	if decision.Chosen != nil {
		tier = decision.Chosen.Tier
	}
	decision.Chosen = &aggrega.DecisionChoice{Target: name, Mode: "draft", Tier: tier}
	decision.SourceNode.ParamsSummary = paramsSummary(spec.res)
	decision.SwapHint = ""
	if len(decision.Candidates) > 0 {
		decision.SwapHint = fmt.Sprintf("resembles %s; draft a Recipe implementing it with `component`",
			decision.Candidates[0].ComponentName)
	}
	return decision
}

// paramsSummary counts the inputs of res by pillar, and the keys it provides.
func paramsSummary(res Resource) string {
	counts := map[Pillar]int{}
	for _, in := range res.Inputs {
		counts[in.Source.Pillar]++
	}
	return fmt.Sprintf("%d values, %d infra, %d overrides, %d secrets, %d provides", counts[PillarValues],
		counts[PillarInfra], counts[PillarOverrides], counts[PillarSecret], len(res.Provides))
}

// draftRationale states what a Component drafted for t holds, counting its keys
// by the rule that placed them and naming each secret, and where they hold, that
// it refreshes the Component the catalog holds, that its category is the
// fallback segment, each read-back property the format leaves out of provides
// with its reason, and each property whose key carries a numeric suffix.
func draftRationale(f Format, t *Type, name string, placed []Placed, refresh, fallback bool) string {
	var identity, placement, required, overrides, provided, withheld, notProvided int
	var renamed, secrets, leftOut []string
	for _, pl := range placed {
		switch pl.Pillar {
		case PillarValues:
			identity++
		case PillarInfra:
			if strings.HasPrefix(pl.Rule, rulePlacement) {
				placement++
			} else {
				required++
			}
		case PillarOverrides:
			overrides++
		case PillarSecret:
			secrets = append(secrets, pl.Property.Name)
		case PillarProvides:
			provided++
		case PillarWithheld:
			withheld++
		case PillarNotProvided:
			if why, ok := strings.CutPrefix(pl.Rule, ruleLeftOut); ok {
				leftOut = append(leftOut, fmt.Sprintf("%s is %s%s", pl.Key, ruleLeftOut, why))
			} else {
				notProvided++
			}
		}
		if pl.Renamed != "" {
			renamed = append(renamed, pl.Renamed)
		}
	}
	counts := nonZero(
		counted(identity, "identity key in values", "identity keys in values"),
		counted(placement, "placement key in infra", "placement keys in infra"),
		counted(required, "required key in infra", "required keys in infra"),
		counted(overrides, "override under infra.overrides", "overrides under infra.overrides"),
		countedSecrets(secrets, f.Rules.SecretSlots),
		counted(provided, "read-back attribute in provides", "read-back attributes in provides"),
		counted(withheld, "sensitive read-back attribute withheld", "sensitive read-back attributes withheld"),
		counted(notProvided, "read-back attribute not provided, not being a scalar",
			"read-back attributes not provided, not being scalars"),
	)
	text := fmt.Sprintf("drafted the Component %s and its Recipe on %s from %s: %s", name, f.Capability,
		typeName(t), counts)
	if refresh {
		text += fmt.Sprintf("; the catalog holds %s with the same values keys, so the draft refreshes it", name)
	}
	if fallback {
		text += fmt.Sprintf("; the format names no category for %s, so the Component takes the category segment %s",
			typeName(t), fallbackCategory)
	}
	for _, r := range append(leftOut, renamed...) {
		text += "; " + r
	}
	return text
}

// recipeOnlyRationale states what a Recipe drafted for the catalog Component
// name matched of it, counting the keys it reads by rule and naming each secret.
func recipeOnlyRationale(f Format, t *Type, name string, spec recipeSpec) string {
	m := spec.recipeOnly
	var overrides int
	var secrets []string
	for _, in := range spec.res.Inputs {
		switch in.Source.Pillar {
		case PillarOverrides:
			overrides++
		case PillarSecret:
			secrets = append(secrets, in.Property.Name)
		}
	}
	var unmapped []string
	for _, u := range spec.res.Unmapped {
		unmapped = append(unmapped, u.Key)
	}
	matched, required, nothing := andList(m.Matched), andList(m.Required), andList(unmapped)
	counts := nonZero(
		counted(len(m.Matched), "values key read by name ("+matched+")", "values keys read by name ("+matched+")"),
		counted(len(m.Required),
			"required property read from infra.overrides until the Release sets it ("+required+")",
			"required properties read from infra.overrides until the Release sets them ("+required+")"),
		counted(overrides-len(m.Required), "other override under infra.overrides",
			"other overrides under infra.overrides"),
		countedSecrets(secrets, f.Rules.SecretSlots),
		counted(len(spec.res.Provides), "provides key mapped to a read-back attribute",
			"provides keys mapped to read-back attributes"),
		counted(len(unmapped), "provides key mapped to nothing, halting the build until mapped ("+nothing+")",
			"provides keys mapped to nothing, halting the build until mapped ("+nothing+")"),
	)
	text := fmt.Sprintf("drafted a Recipe on %s implementing %s from %s: %s", f.Capability, spec.implements,
		typeName(t), counts)
	if len(m.Unmatched) > 0 {
		text += fmt.Sprintf("; the values keys of %s no property matches by name, which the Recipe reads nothing "+
			"for: %s", name, andList(m.Unmatched))
	}
	return text
}

// counted is n followed by the phrase singular or plural as n is one or not;
// empty for none.
func counted(n int, singular, plural string) string {
	switch n {
	case 0:
		return ""
	case 1:
		return "1 " + singular
	}
	return fmt.Sprintf("%d %s", n, plural)
}

// countedSecrets counts the secrets of the vendor names given, delivered on
// slots or declared as deploy-time parameters, and names each.
func countedSecrets(names []string, slots bool) string {
	text := counted(len(names), "secret as a deploy-time parameter", "secrets as deploy-time parameters")
	if slots {
		text = counted(len(names), "secret on a featuresSchema slot", "secrets on featuresSchema slots")
	}
	if text == "" {
		return ""
	}
	return text + " (" + andList(names) + ")"
}

// nonZero joins the counts that are not empty, or says no property was placed.
func nonZero(parts ...string) string {
	var kept []string
	for _, p := range parts {
		if p != "" {
			kept = append(kept, p)
		}
	}
	if len(kept) == 0 {
		return "no property"
	}
	return strings.Join(kept, ", ")
}

// distinct keeps the first of each identical selection, same type and same
// component, in request order.
func distinct(selected []Selected) []Selected {
	var out []Selected
	for _, s := range selected {
		if !slices.ContainsFunc(out, func(o Selected) bool { return o.Component == s.Component && sameRef(o.Ref, s.Ref) }) {
			out = append(out, s)
		}
	}
	return out
}

// sameRef reports whether a and b name one type of one source.
func sameRef(a, b TypeRef) bool {
	if a.Name != b.Name || a.APIVersion != b.APIVersion || a.Source != b.Source {
		return false
	}
	if a.Provider == nil || b.Provider == nil {
		return a.Provider == b.Provider
	}
	return *a.Provider == *b.Provider
}

// decisionID names the decision for the type t drafted as name: its name, and
// where selected drafts it more than once, the Component too.
func decisionID(t *Type, sel Selected, name string, selected []Selected) string {
	n := 0
	for _, s := range selected {
		if sameRef(s.Ref, sel.Ref) {
			n++
		}
	}
	if n > 1 {
		return typeName(t) + "#" + name
	}
	return typeName(t)
}

// requestText names the request drafting t for a message.
func requestText(t *Type, component string) string {
	if component == "" {
		return "the request for " + typeName(t)
	}
	return "the request for " + typeName(t) + " with the component " + component
}

// heldComponent returns the Component the catalog view holds under name.
func heldComponent(view *aggrega.CatalogProjection, name string) (aggrega.CatalogComponent, bool) {
	if view == nil {
		return aggrega.CatalogComponent{}, false
	}
	for _, c := range view.Components {
		if c.Name == name {
			return c, true
		}
	}
	return aggrega.CatalogComponent{}, false
}

// without is view less the Component named name.
func without(view *aggrega.CatalogProjection, name string) *aggrega.CatalogProjection {
	if view == nil {
		return nil
	}
	out := &aggrega.CatalogProjection{}
	for _, c := range view.Components {
		if c.Name != name {
			out.Components = append(out.Components, c)
		}
	}
	return out
}

// placedKeys returns the keys placed holds in pillar, in its order.
func placedKeys(placed []Placed, pillar Pillar) []string {
	var out []string
	for _, pl := range placed {
		if pl.Pillar == pillar {
			out = append(out, pl.Key)
		}
	}
	return out
}

func sortedCopy(keys []string) []string {
	out := slices.Clone(keys)
	slices.Sort(out)
	return out
}

// lastSegment is the segment of a Component name after its last slash.
func lastSegment(name string) string {
	return name[strings.LastIndex(name, "/")+1:]
}

// draftedPath is the version directory a draft is written to, as
// importer.CatalogFiles places it.
//
// It deliberately repeats the layout CatalogFiles writes: the kit exports the
// files it lays out, not the directory of one draft, so a draft is named here
// before its files exist. A change of the kit's layout changes this too.
func draftedPath(d importer.CatalogDraft) string {
	if d.Entity.Kind == "Recipe" {
		return "catalog/recipe/" + d.Capability + "/" + d.Entity.Name + "/" + d.Version
	}
	return "catalog/component/" + d.Entity.Name + "/" + d.Version
}

// componentExists halts on a Component name the catalog holds with values keys
// other than the draft of t would give it.
func componentExists(name string, held, drafted []string, t *Type) aggrega.Diagnostic {
	return aggrega.Diagnostic{
		Severity: aggrega.SeverityHalt,
		Code:     CodeComponentExists,
		Message: fmt.Sprintf("the catalog holds the Component %s with the values keys %s, and the draft of %s would "+
			"give it the values keys %s: write {type: %s, component: <namespace>/<category>/<name>} in "+
			"resource_types to draft it under another name, or name %s as its component to draft a Recipe alone "+
			"implementing the Component the catalog holds", name, andList(held), typeName(t), andList(drafted),
			typeName(t), name),
		SourceRef: t.Source,
	}
}

// twoRequestsDraftOnePath halts on two requests drafting one path.
func twoRequestsDraftOnePath(earlier, later, path string) aggrega.Diagnostic {
	return aggrega.Diagnostic{
		Severity: aggrega.SeverityHalt,
		Code:     CodeComponentExists,
		Message: fmt.Sprintf("%s and %s both draft %s: give one of them another component in resource_types",
			earlier, later, path),
	}
}

// noBody halts on a type its format lifted no body for.
func noBody(ref TypeRef) aggrega.Diagnostic {
	return aggrega.Diagnostic{
		Severity:  aggrega.SeverityHalt,
		Code:      CodeResourceSchemaUnreadable,
		Message:   fmt.Sprintf("the source holds no body the format reads for the resource type %s", ref.Name),
		SourceRef: ref.Source,
	}
}
