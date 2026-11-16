// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package armcore

import (
	"bytes"
	"encoding/json"
	"fmt"
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
	// Annotations are the node's merged annotations. Assemble does not read them.
	Annotations map[string]string
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

// Assemble merges the fragments of a dispatch group into one resource group
// scope template, written as arm-template.json.
//
// Every fragment of every contribution is read before any entry is inserted.
// Resources merge by symbolic name; parameters, variables, definitions, functions
// and metadata keys merge by the name written; each output lands under
// OutputName of the contributor's identity and the entry's name, keeping its
// type. A name restated with an identical body merges once. A name restated with
// a different body, or a carried name spelled differently from another
// contributor's but equal without regard to case, halts wrapping
// ErrPluginOutputNameCollision and names both contributors and the section. A
// fragment written for a wider scope than a resource group halts wrapping
// ErrPluginCannotExpressInput.
//
// Delivered values inside literal text are composed, and every reference is
// checked, on the merged level (composeLiterals, checkReferences). The resources
// of each wave then depend on the resources of the preceding populated wave,
// appended after the dependsOn the author wrote; a resource declared
// existing: true takes no part. Last, the template is checked against the limits
// ARM sets (CheckLimits).
//
// functions is written as a list of namespaces sorted by name, each with its
// members sorted by name; a section no fragment wrote is not written, and
// resources always is. The output is a function of the input alone.
func Assemble(contribs []Contribution) (rendercore.AssembleBoundaryOutput, error) {
	type read struct {
		identity string
		wave     int
		frag     Fragment
	}
	var reads []read
	var diags []renderctx.Diagnostic
	for _, c := range contribs {
		identity := ContributorIdentity(c)
		for _, content := range c.Files {
			frag, fragDiags, err := ReadFragment(content)
			if err != nil {
				return rendercore.AssembleBoundaryOutput{}, err
			}
			if frag.Scope != ScopeResourceGroup {
				return rendercore.AssembleBoundaryOutput{}, refuse(plugincondition.ErrPluginCannotExpressInput,
					fmt.Sprintf("the fragment of %q is written for a %s scope template; the arm provider assembles a boundary at resource group scope, so write the resources of this boundary in a resource group template, or remove %s to write one",
						identity, frag.Scope, sectionSchema))
			}
			diags = append(diags, fragDiags...)
			reads = append(reads, read{identity, c.SyncWave, frag})
		}
	}

	l := newLevel()
	folded := map[string]map[string]string{}
	waves := map[string]int{}
	for _, r := range reads {
		for _, section := range sortedKeys(r.frag.Sections) {
			entries := r.frag.Sections[section]
			for _, name := range sortedKeys(entries) {
				key, pointer := name, "/"+section+"/"+escapePointer(name)
				if section == sectionOutputs {
					key = OutputName(r.identity, name)
				}
				entry := &levelEntry{body: entries[name], contributor: r.identity, pointer: pointer}
				if err := insert(l, folded, section, key, entry); err != nil {
					return rendercore.AssembleBoundaryOutput{}, err
				}
				if section == sectionResources {
					if _, seen := waves[key]; !seen {
						waves[key] = r.wave
					}
				}
			}
		}
	}

	composed, err := l.composeLiterals()
	if err != nil {
		return rendercore.AssembleBoundaryOutput{}, err
	}
	diags = append(diags, composed...)
	if err := l.checkReferences(); err != nil {
		return rendercore.AssembleBoundaryOutput{}, err
	}
	applyWaveOrdering(l.sections[sectionResources], waves)

	template := l.template()
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

// template returns the level as the value tree the template is written from.
func (l *level) template() map[string]any {
	t := map[string]any{
		sectionSchema:          SchemaResourceGroup,
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
		t[sectionFunctions] = l.functionList()
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
// each holding its members. An entry is keyed "namespace.member"; a member name
// holds no dot, so the namespace is everything before the last one.
func (l *level) functionList() []any {
	byNamespace := map[string]map[string]any{}
	for _, full := range sortedKeys(l.sections[sectionFunctions]) {
		i := strings.LastIndexByte(full, '.')
		namespace, member := full[:i], full[i+1:]
		if byNamespace[namespace] == nil {
			byNamespace[namespace] = map[string]any{}
		}
		byNamespace[namespace][member] = l.sections[sectionFunctions][full].body
	}
	out := make([]any, 0, len(byNamespace))
	for _, namespace := range sortedKeys(byNamespace) {
		out = append(out, map[string]any{"namespace": namespace, "members": byNamespace[namespace]})
	}
	return out
}

// encodeTemplate writes a template as JSON: sorted keys, two-space indent, no
// HTML escaping and one trailing newline.
func encodeTemplate(template map[string]any) ([]byte, error) {
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
// target. With one populated wave nothing is written.
func applyWaveOrdering(resources map[string]*levelEntry, waves map[string]int) {
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
		case []any:
			depends = slices.Clone(authored)
		case string:
			depends = []any{authored}
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
}

// isExisting reports whether a resource body declares existing: true.
func isExisting(body any) bool {
	m, _ := body.(map[string]any)
	existing, _ := m["existing"].(bool)
	return existing
}
