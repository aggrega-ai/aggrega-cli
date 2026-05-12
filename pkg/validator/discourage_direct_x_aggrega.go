// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package validator

import (
	"log"
	"sort"

	"gopkg.in/yaml.v3"
)

// DiscourageDirectXAggregaInSchema walks a Component or Feature's
// Spec.Schema content looking for x-aggrega-selector or x-aggrega-inject
// extension keywords authored inline. When found, the function emits a
// warn-level log naming the entity and the recommended authoring path
// (usesSchema for dependency sockets; secretsSchema for credential
// bindings).
//
// The check is observation-only: it does not return an error and does
// not block load. The function name is captured as the call site for a
// future publishing-discipline policy plugin that enforces the
// discouragement at publish time. Catalog content that needs the
// inline form (e.g., teams migrating from existing JSON Schema imports,
// programmatic schema-generation tooling) can opt into the inline form
// today and policy in the future controls when the warning becomes a
// hard error.
//
// Logged keys are emitted in sorted order so the output is byte-stable
// across runs of the same input.
func DiscourageDirectXAggregaInSchema(entityName string, schema *yaml.Node) {
	if schema == nil || schema.Kind == 0 {
		return
	}
	found := map[string]bool{}
	walkForXAggregaExtensions(schema, found)
	if len(found) == 0 {
		return
	}
	keys := make([]string, 0, len(found))
	for k := range found {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		recommended := recommendedAuthoringPath(k)
		log.Printf("warning: entity %q authors %s directly in schema content; recommended path is %s",
			entityName, k, recommended)
	}
}

// walkForXAggregaExtensions recursively walks the YAML tree collecting
// any mapping key starting with "x-aggrega-" into the found set.
func walkForXAggregaExtensions(n *yaml.Node, found map[string]bool) {
	if n == nil {
		return
	}
	switch n.Kind {
	case yaml.DocumentNode, yaml.SequenceNode:
		for _, child := range n.Content {
			walkForXAggregaExtensions(child, found)
		}
	case yaml.MappingNode:
		for i := 0; i+1 < len(n.Content); i += 2 {
			keyNode := n.Content[i]
			valNode := n.Content[i+1]
			if keyNode.Kind == yaml.ScalarNode && len(keyNode.Value) >= len("x-aggrega-") &&
				keyNode.Value[:len("x-aggrega-")] == "x-aggrega-" {
				found[keyNode.Value] = true
			}
			walkForXAggregaExtensions(valNode, found)
		}
	}
}

// recommendedAuthoringPath returns the user-facing recommendation string
// for a given x-aggrega-* extension keyword.
func recommendedAuthoringPath(extKey string) string {
	switch extKey {
	case "x-aggrega-selector":
		return "the usesSchema field's declarative socket table"
	case "x-aggrega-inject":
		return "the usesSchema or secretsSchema declarative tables"
	default:
		return "the appropriate declarative table on spec"
	}
}
