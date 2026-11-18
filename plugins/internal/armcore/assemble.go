// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package armcore

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/rendercore"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderctx"
	"github.com/aggrega-ai/aggrega-cli/internal/plugincondition"
)

// Contribution is what one node of a dispatch group contributes to the template
// the group assembles into.
type Contribution struct {
	// NodeID is the node's id. NameBasis is the name basis its render carried;
	// together they give the identity the node's output names and halts use
	// (ContributorIdentity).
	NodeID, NameBasis string
	// Files are the fragments render_node returned and Paths the paths they were
	// returned under, position for position.
	Files [][]byte
	Paths []string
	// SyncWave is the deployment wave of the node's boundary: the resources the
	// node contributes are ordered after the resources of the preceding
	// populated wave.
	SyncWave int
	// Annotations are the node's merged annotations. Assemble reads
	// AnnotationDeploymentScope, AnnotationSubscription and
	// AnnotationResourceGroup from them (placementAnnotations).
	Annotations map[string]string
}

// placementAnnotations are the annotations Assemble reads to place a
// contribution's content: the scope its template deploys at, and the
// subscription and resource group its nested content deploys to.
var placementAnnotations = []string{AnnotationDeploymentScope, AnnotationSubscription, AnnotationResourceGroup}

// belongsToNoComponent reports whether a contribution carries no annotations and
// is named by a name basis other than its node id, as the contribution of a
// Feature attached to a whole boundary is: it belongs to no component, so no
// component's annotations reach it. A component's own contribution is named by
// its node id.
func belongsToNoComponent(c Contribution) bool {
	return len(c.Annotations) == 0 && c.NameBasis != "" && c.NameBasis != c.NodeID
}

// withBoundaryPlacement returns contribs with each contribution that belongs to
// no component (belongsToNoComponent) given every placement annotation the other
// contributions all resolve to one value, so a boundary-wide Feature's content
// is assembled at its boundary's scope and placed where its boundary's
// components are. An annotation the others resolve to different values, or
// leave absent, is not given. contribs itself is not changed.
func withBoundaryPlacement(contribs []Contribution) []Contribution {
	given := map[string]string{}
	for _, key := range placementAnnotations {
		value, agreed := "", true
		for _, c := range contribs {
			if belongsToNoComponent(c) {
				continue
			}
			v := c.Annotations[key]
			if v == "" || (value != "" && v != value) {
				agreed = false
				break
			}
			value = v
		}
		if agreed && value != "" {
			given[key] = value
		}
	}
	out := slices.Clone(contribs)
	for i := range out {
		if belongsToNoComponent(out[i]) && len(given) > 0 {
			out[i].Annotations = maps.Clone(given)
		}
	}
	return out
}

// ContributorIdentity returns the identity a contribution is named by: its name
// basis, falling back to its node id. The output names Assemble writes and the
// contributors its halts name are built from it.
func ContributorIdentity(c Contribution) string {
	return rendercore.ContributorNameBasis(c.NodeID, c.NameBasis)
}

// carriedSections are the sections whose entries keep the names an author wrote
// and are compared without regard to case, as ARM reads those names.
var carriedSections = []string{sectionDefinitions, sectionParameters, sectionVariables, sectionFunctions, sectionMetadata}

// mergedSections are the sections written as an object of entries, in the order
// the template syntax documents them; resources are written separately because
// the template always carries them.
var mergedSections = []string{sectionDefinitions, sectionParameters, sectionVariables, sectionMetadata, sectionOutputs}

// Assemble merges the fragments of a dispatch group into one template, written
// as arm-template.json, at the scope its boundary deploys at.
//
// The scope is the one every contribution's AnnotationDeploymentScope resolves
// to (boundaryScopeOf): a resource group where none names one. A contribution
// that belongs to no component, a boundary-wide Feature's, first takes the
// placement annotations the others agree on (withBoundaryPlacement). A fragment
// written for a wider scope than its boundary's, by its $schema, halts wrapping
// ErrPluginCannotExpressInput.
//
// Every fragment of every contribution is read before any entry is inserted.
// Content written for the boundary's scope lands at the template's top level.
// Content written for a narrower scope lands in a level of its own, a nested
// deployment (nestLevel), assembled on the same terms as the top level: at
// subscription scope the resource group content, in LevelResourceGroupSymbol,
// and at management group scope the subscription content, in
// LevelSubscriptionSymbol, and the resource group content, in
// LevelResourceGroupSymbol, side by side. The wave order is written onto the
// nested deployments, and an order they cannot carry halts (orderLevels).
//
// Within a level, resources merge by symbolic name; parameters, variables,
// definitions, functions and metadata keys merge by the name written; each
// output lands under OutputName of the contributor's identity and the entry's
// name, keeping its type. A name restated with an identical body merges once. A
// name restated with a different body, or a carried name spelled differently
// from another contributor's but equal without regard to case, halts wrapping
// ErrPluginOutputNameCollision and names both contributors and the section.
//
// Delivered values inside literal text are composed, and every reference is
// checked, on each merged level (composeLiterals, checkReferences), so a
// reference reaches only its own level. The resources of each wave then depend
// on the resources of the preceding populated wave, appended after the dependsOn
// the author wrote; a resource declared existing: true takes no part. Last, the
// template is checked against the limits ARM sets (CheckLimits).
//
// functions is written as a list of namespaces sorted by name, each with its
// members sorted by name; a section no fragment wrote is not written, and
// resources always is. The output is a function of the input alone.
func Assemble(contribs []Contribution) (rendercore.AssembleBoundaryOutput, error) {
	contribs = withBoundaryPlacement(contribs)
	scope, err := boundaryScopeOf(contribs)
	if err != nil {
		return rendercore.AssembleBoundaryOutput{}, err
	}
	var topReads []read
	nested := map[Scope][]read{}
	var diags []renderctx.Diagnostic
	for _, c := range contribs {
		identity := ContributorIdentity(c)
		for _, content := range c.Files {
			frag, fragDiags, err := ReadFragment(content)
			if err != nil {
				return rendercore.AssembleBoundaryOutput{}, err
			}
			if scopeRank(frag.Scope) > scopeRank(scope) {
				return rendercore.AssembleBoundaryOutput{}, widerContent(identity, frag.Scope, scope)
			}
			diags = append(diags, fragDiags...)
			r := read{identity: identity, wave: c.SyncWave, annotations: c.Annotations, frag: frag}
			if frag.Scope == scope {
				topReads = append(topReads, r)
			} else {
				nested[frag.Scope] = append(nested[frag.Scope], r)
			}
		}
	}

	top := newLevelBuild()
	if err := top.mergeReads(topReads); err != nil {
		return rendercore.AssembleBoundaryOutput{}, err
	}
	composed, err := top.checkLevel()
	if err != nil {
		return rendercore.AssembleBoundaryOutput{}, err
	}
	diags = append(diags, composed...)
	for _, content := range []Scope{ScopeSubscription, ScopeResourceGroup} {
		if len(nested[content]) == 0 {
			continue
		}
		levelDiags, err := nestLevel(top, scope, content, nested[content])
		if err != nil {
			return rendercore.AssembleBoundaryOutput{}, err
		}
		diags = append(diags, levelDiags...)
	}
	if err := orderLevels(top); err != nil {
		return rendercore.AssembleBoundaryOutput{}, err
	}
	if err := applyWaveOrdering(top.l.sections[sectionResources], top.waves); err != nil {
		return rendercore.AssembleBoundaryOutput{}, err
	}

	template := top.template(scope)
	content, err := encodeTemplate(template)
	if err != nil {
		return rendercore.AssembleBoundaryOutput{}, err
	}
	if err := CheckLimits(template, content); err != nil {
		return rendercore.AssembleBoundaryOutput{}, err
	}
	return rendercore.AssembleBoundaryOutput{
		Files:       []rendercore.RenderedFile{{Path: TemplateFileName, Content: content, Format: "json"}},
		Diagnostics: diags,
	}, nil
}

// read is one fragment of one contribution, read and ready to merge.
type read struct {
	identity    string
	wave        int
	annotations map[string]string
	frag        Fragment
}

// levelBuild is one template level under assembly, with what merging records
// beside it: the spelling each carried name was first written in, keyed by
// section and by the name in lower case; the wave of the contributor that wrote
// each resource; and where each function was written.
type levelBuild struct {
	l         *level
	folded    map[string]map[string]string
	waves     map[string]int
	functions map[string]FunctionSource
	// levels are the nested deployments added to the level, by symbolic name.
	levels map[string]nestedLevel
}

// newLevelBuild returns a level build holding no entries.
func newLevelBuild() *levelBuild {
	return &levelBuild{l: newLevel(), folded: map[string]map[string]string{}, waves: map[string]int{}, functions: map[string]FunctionSource{}, levels: map[string]nestedLevel{}}
}

// mergeReads inserts every entry of every read into the level, in the order of
// the reads and, within one, by section and name.
func (b *levelBuild) mergeReads(reads []read) error {
	for _, r := range reads {
		for _, section := range sortedKeys(r.frag.Sections) {
			entries := r.frag.Sections[section]
			for _, name := range sortedKeys(entries) {
				key, pointer := name, "/"+section+"/"+escapePointer(name)
				if section == sectionOutputs {
					key = OutputName(r.identity, name)
				}
				if section == sectionFunctions {
					pointer = r.frag.Functions[name].Pointer
				}
				entry := &levelEntry{body: entries[name], contributor: r.identity, pointer: pointer}
				if section == sectionFunctions {
					if _, seen := b.functions[key]; !seen {
						b.functions[key] = r.frag.Functions[name]
					}
				}
				if err := b.insert(section, key, entry); err != nil {
					return err
				}
				if section == sectionResources {
					if _, seen := b.waves[key]; !seen {
						b.waves[key] = r.wave
					}
				}
			}
		}
	}
	return nil
}

// insert lands one entry in the level, as the package-level insert does.
func (b *levelBuild) insert(section, key string, e *levelEntry) error {
	return insert(b.l, b.folded, section, key, e)
}

// checkLevel composes the delivered values inside the level's literal text and
// checks its references, returning the composition diagnostics.
func (b *levelBuild) checkLevel() ([]renderctx.Diagnostic, error) {
	composed, err := b.l.composeLiterals()
	if err != nil {
		return nil, err
	}
	if err := b.l.checkReferences(); err != nil {
		return nil, err
	}
	return composed, nil
}

// finishLevel checks the level (checkLevel) and orders its resources by wave.
func (b *levelBuild) finishLevel() ([]renderctx.Diagnostic, error) {
	composed, err := b.checkLevel()
	if err != nil {
		return nil, err
	}
	if err := applyWaveOrdering(b.l.sections[sectionResources], b.waves); err != nil {
		return nil, err
	}
	return composed, nil
}

// template returns the level as a template for scope.
func (b *levelBuild) template(scope Scope) map[string]any {
	return b.l.template(scope, b.functions)
}

// insert lands one entry in the level, halting on a name the level already
// carries with different content, or, in a carried section, with a different
// spelling of the same name.
func insert(l *level, folded map[string]map[string]string, section, key string, e *levelEntry) error {
	if slices.Contains(carriedSections, section) {
		if folded[section] == nil {
			folded[section] = map[string]string{}
		}
		lower := strings.ToLower(key)
		if spelled, taken := folded[section][lower]; taken && spelled != key {
			other := l.sections[section][spelled]
			return nameCollision(section, fmt.Sprintf("declares %q, which %q already declares as %q; ARM reads these names without regard to case, so rename one", key, other.contributor, spelled), e, other)
		}
		folded[section][lower] = key
	}
	if other, taken := l.sections[section][key]; taken {
		if reflect.DeepEqual(other.body, e.body) {
			return nil
		}
		return nameCollision(section, fmt.Sprintf("declares %q, which %q already declares with different content; rename one so each name addresses one thing", key, other.contributor), e, other)
	}
	l.put(section, key, e)
	return nil
}

// nameCollision is the halt for two contributors writing one name in a section.
func nameCollision(section, what string, e, other *levelEntry) error {
	return refuse(plugincondition.ErrPluginOutputNameCollision,
		fmt.Sprintf("in %s, the fragment of %q (at JSON pointer %s) %s (at JSON pointer %s in the fragment of %q)",
			section, e.contributor, e.pointer, what, other.pointer, other.contributor))
}

// template returns the level as the value tree a template for scope is written
// from.
func (l *level) template(scope Scope, functions map[string]FunctionSource) map[string]any {
	t := map[string]any{
		sectionSchema:          schemaOf(scope),
		sectionLanguageVersion: LanguageVersion,
		sectionContentVersion:  ContentVersion,
		sectionResources:       l.object(sectionResources),
	}
	for _, section := range mergedSections {
		if len(l.sections[section]) > 0 {
			t[section] = l.object(section)
		}
	}
	if len(l.sections[sectionFunctions]) > 0 {
		t[sectionFunctions] = l.functionList(functions)
	}
	return t
}

// object returns the entries of a section as an object of their bodies.
func (l *level) object(section string) map[string]any {
	out := map[string]any{}
	for _, name := range sortedKeys(l.sections[section]) {
		out[name] = l.sections[section][name].body
	}
	return out
}

// functionList returns the functions as a list of namespaces sorted by name,
// each holding its members. functions says where each entry was written, so the
// namespace and the member are the ones the author wrote.
func (l *level) functionList(functions map[string]FunctionSource) []any {
	byNamespace := map[string]map[string]any{}
	for _, full := range sortedKeys(l.sections[sectionFunctions]) {
		src := functions[full]
		if byNamespace[src.Namespace] == nil {
			byNamespace[src.Namespace] = map[string]any{}
		}
		byNamespace[src.Namespace][src.Member] = l.sections[sectionFunctions][full].body
	}
	out := make([]any, 0, len(byNamespace))
	for _, namespace := range sortedKeys(byNamespace) {
		out = append(out, map[string]any{"namespace": namespace, "members": byNamespace[namespace]})
	}
	return out
}

// encodeTemplate writes a template, or any value of one, as JSON: sorted keys, two-space indent, no
// HTML escaping and one trailing newline.
func encodeTemplate(template any) ([]byte, error) {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(template); err != nil {
		return nil, fmt.Errorf("arm-provider: write template: %w", err)
	}
	return b.Bytes(), nil
}

// applyWaveOrdering appends to each resource of a wave the resources of the
// preceding populated wave, sorted by symbolic name, after the dependsOn the
// author wrote. waves maps a symbolic name to the wave of the contributor that
// wrote it. A resource declared existing: true is neither a dependent nor a
// target. With one populated wave nothing is written. A dependsOn that is neither
// a list nor text halts wrapping ErrPluginCannotExpressInput.
func applyWaveOrdering(resources map[string]*levelEntry, waves map[string]int) error {
	var symbols []string
	for _, symbol := range sortedKeys(resources) {
		if !isExisting(resources[symbol].body) {
			symbols = append(symbols, symbol)
		}
	}
	byIndex := make([]int, len(symbols))
	for i, symbol := range symbols {
		byIndex[i] = waves[symbol]
	}
	predecessors := rendercore.WavePredecessors(byIndex)
	for i, symbol := range symbols {
		if len(predecessors) == 0 || len(predecessors[i]) == 0 {
			continue
		}
		body, ok := resources[symbol].body.(map[string]any)
		if !ok {
			continue
		}
		var depends []any
		switch authored := body["dependsOn"].(type) {
		case nil:
		case []any:
			depends = slices.Clone(authored)
		case string:
			depends = []any{authored}
		default:
			return refuse(plugincondition.ErrPluginCannotExpressInput,
				fmt.Sprintf("the dependsOn at JSON pointer %s/dependsOn in the fragment of %q is neither a list nor a symbolic name, so the order its wave declares cannot be added to it; write dependsOn as a list of symbolic names",
					resources[symbol].pointer, resources[symbol].contributor))
		}
		for _, j := range predecessors[i] {
			if !slices.Contains(depends, any(symbols[j])) {
				depends = append(depends, symbols[j])
			}
		}
		out := clone(body)
		out["dependsOn"] = depends
		resources[symbol].body = out
	}
	return nil
}

// isExisting reports whether a resource body declares existing: true.
func isExisting(body any) bool {
	m, _ := body.(map[string]any)
	existing, _ := m["existing"].(bool)
	return existing
}
