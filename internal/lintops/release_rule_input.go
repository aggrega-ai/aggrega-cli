// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package lintops

import (
	"encoding/json"
	"fmt"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/internal/compilerops"
	"github.com/aggrega-ai/aggrega-cli/pkg/entitykind"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/validator"
)

// releaseRuleDocuments is what a rule reading a whole Release is handed: the
// Aggregate the Release deploys, as its author wrote it; the Release, with its
// extends chain folded in and its parameters block removed; and the Component
// each of the Aggregate's component instances is an instance of, keyed by the
// instance name in the Aggregate's own order.
//
// It is assembled once per release-scoped run, from the closure the run already
// resolved, so the documents a rule reads are the ones the build reads rather
// than a second resolution of them.
type releaseRuleDocuments struct {
	Aggregate  json.RawMessage
	Release    json.RawMessage
	Components types.OrderedMap[json.RawMessage]
}

// releaseRuleDocumentsFor assembles those documents from a resolved closure.
// componentByURI resolves an instance's type the way the closure's catalog view
// does, pins included, so an instance reaches the Component a build would use.
//
// An instance whose type does not resolve is left out of Components rather than
// failing the run: the documents feed advisories, and a rule reads an absent
// entry as unavailable. Only the instances the Aggregate itself declares are
// walked — a module imported under the Aggregate's aggregates surface carries a
// type reference rather than components of its own, so its instances are not
// among these.
//
// A name declared twice keeps its first declaration. Two subsystems declaring one
// component name is an Aggregate the build refuses, because a subsystem
// contributes nothing to a component's identity; keeping the first makes the
// documents deterministic for it anyway.
func releaseRuleDocumentsFor(rc *compilerops.ResolvedClosure, componentByURI func(uri string) (types.Component, bool)) (releaseRuleDocuments, error) {
	if rc == nil {
		return releaseRuleDocuments{}, fmt.Errorf("aggrega lint: no resolved Release to hand a release-scoped rule")
	}
	aggregateJSON, _, err := yamlToLinterJSON(rc.AggBytes)
	if err != nil {
		return releaseRuleDocuments{}, fmt.Errorf("aggrega lint: parse aggregate %s: %w", rc.AggregatePath, err)
	}
	releaseJSON, err := releaseLinterJSON(rc.Release)
	if err != nil {
		return releaseRuleDocuments{}, err
	}
	docs := releaseRuleDocuments{Aggregate: aggregateJSON, Release: releaseJSON}
	if componentByURI == nil {
		return docs, nil
	}
	for _, inst := range types.Normalize(rc.Aggregate).ComponentInstances() {
		if _, seen := docs.Components.Get(inst.Name); seen || inst.Instance.Type == "" {
			continue
		}
		comp, ok := componentByURI(inst.Instance.Type)
		if !ok {
			continue
		}
		if compJSON := componentLinterJSON(comp); compJSON != nil {
			docs.Components.Set(inst.Name, compJSON)
		}
	}
	return docs, nil
}

// releaseLinterJSON converts a Release to the JSON a linter plugin receives,
// through the same YAML-to-JSON conversion the proposed entity goes through, and
// with its parameters block removed.
//
// The block is removed because a parameter's value can be the credential a
// Release supplies — a realization that delivers a value reads it from a
// parameter — and no rule reads it. Removing it here, on the one path that hands
// a Release to a plugin, is what lets the socket's description promise that no
// parameter value reaches a linter plugin through that field.
func releaseLinterJSON(rel *types.Release) (json.RawMessage, error) {
	if rel == nil {
		return nil, fmt.Errorf("aggrega lint: no Release to hand a release-scoped rule")
	}
	doc, err := yaml.Marshal(rel)
	if err != nil {
		return nil, fmt.Errorf("aggrega lint: encode release %s: %w", rel.Metadata.Name, err)
	}
	var raw any
	if err := yaml.Unmarshal(doc, &raw); err != nil {
		return nil, fmt.Errorf("aggrega lint: parse release %s: %w", rel.Metadata.Name, err)
	}
	tree := convertYAMLMaps(raw)
	withoutReleaseParameters(tree)
	out, err := json.Marshal(tree)
	if err != nil {
		return nil, fmt.Errorf("aggrega lint: convert release %s: %w", rel.Metadata.Name, err)
	}
	return out, nil
}

// withoutReleaseParameters removes spec.parameters from a parsed Release
// document, in place. A document with no spec, or a spec carrying no
// parameters, is left as it is.
func withoutReleaseParameters(tree any) {
	root, ok := tree.(map[string]any)
	if !ok {
		return
	}
	spec, ok := root["spec"].(map[string]any)
	if !ok {
		return
	}
	delete(spec, "parameters")
}

// buildReleaseRuleInputs builds one LinterInput per rule that reads a whole
// Release. Each carries the three documents and the effective policies, and none
// of the per-entity fields: there is no previous version, no source path and no
// implemented Component, because the input describes a Release rather than one
// file.
func buildReleaseRuleInputs(rules []string, docs releaseRuleDocuments, policies json.RawMessage) []validator.LinterInput {
	inputs := make([]validator.LinterInput, 0, len(rules))
	for _, r := range rules {
		inputs = append(inputs, validator.LinterInput{
			RuleID:     r,
			EntityKind: string(entitykind.Aggregate),
			Proposed:   docs.Aggregate,
			Release:    docs.Release,
			Components: docs.Components,
			Policies:   policies,
		})
	}
	return inputs
}
