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
// Aggregate the Release deploys, as its author wrote it but for its parameter
// values and any key outside the declaration vocabulary inside a parameter
// declaration; the Release, with its extends chain folded in and its parameters
// block removed; and the Component each of the Aggregate's component instances is
// an instance of, keyed by the instance name in the Aggregate's own order.
//
// No parameter value reaches a rule through these documents. Each is converted by
// the one conversion every document a linter plugin receives goes through (see
// yamlToLinterJSON), so the defaults the Aggregate's parameter declarations give,
// the values it passes to each module it imports, and the Release's fulfilment
// are all withheld before anything is handed over, exactly as they are for an
// entity linted on its own. A template that reads a parameter keeps its text, so a
// rule still sees which parameter an authored value reads.
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
// through the one conversion every document a linter plugin receives goes
// through, which removes its parameters block: a parameter's value can be the
// credential a Release supplies, and the folded Release carries an imported
// parameters file's values inline.
//
// The document is converted as a Release whatever kind the value names, because
// this is the field that carries the Release; the conversion keys what it
// withholds on the kind a document declares, and only a Release's reading — the
// whole block removed — is right for the values a Release holds.
func releaseLinterJSON(rel *types.Release) (json.RawMessage, error) {
	if rel == nil {
		return nil, fmt.Errorf("aggrega lint: no Release to hand a release-scoped rule")
	}
	asRelease := *rel
	asRelease.Kind = string(entitykind.Release)
	doc, err := yaml.Marshal(&asRelease)
	if err != nil {
		return nil, fmt.Errorf("aggrega lint: encode release %s: %w", rel.Metadata.Name, err)
	}
	out, _, err := yamlToLinterJSON(doc)
	if err != nil {
		return nil, fmt.Errorf("aggrega lint: convert release %s: %w", rel.Metadata.Name, err)
	}
	return out, nil
}

// buildReleaseRuleInputs builds one LinterInput per rule that reads a whole
// Release. Each carries the three documents and the effective policies, and none
// of the per-entity fields: there is no previous version, no source path and no
// implemented Component, because the input describes a Release rather than one
// file.
func buildReleaseRuleInputs(rules []string, docs releaseRuleDocuments, policies validator.Policies) []validator.LinterInput {
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
