// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package hover

import (
	"fmt"

	"gopkg.in/yaml.v3"
)

// Manifest renders the hover content for a Manifest target. The
// rendered shape surfaces representative mapping pairs (top 3) plus a
// summary of legacy provider-keyed mappings (when present).
//
//   - **Manifest** `<URI>` `v<version>`
//   - <Description>
//   - ---
//   - **Flat mappings**: N entries
//   - `<component-type-URI>` -> `<recipe-URI>`
//   - ...top 3 then truncation
//   - **Legacy provider-keyed mappings**: N entries (when present)
//   - `<provider-name>`: M entries
//   - ...one-line-per-provider
//   - **Deprecated**: yes (when set)
func Manifest(e Entity) string {
	name := scalarValueAtPath(e.Doc, "metadata", "name")
	version := scalarValueAtPath(e.Doc, "metadata", "version")
	desc := scalarValueAtPath(e.Doc, "metadata", "description")
	deprecated := boolValueAtPath(e.Doc, "metadata", "deprecated")

	body := []string{}
	if section := flatMappingsSection(e.Doc); section != "" {
		body = append(body, section)
	}
	if section := legacyMappingsSection(e.Doc); section != "" {
		body = append(body, section)
	}
	if dep := Deprecated(deprecated); dep != "" {
		body = append(body, dep)
	}

	return Assemble(
		Header("Manifest", name, version),
		Description(desc),
		Separator(),
		joinLines(body),
	)
}

// flatMappingsSection enumerates spec.mappings.{components,features}.{type,name}
// pairs in author-order across the union (top 3 + truncation).
func flatMappingsSection(doc *yaml.Node) string {
	mappings := mappingNodeAtPath(doc, "spec", "mappings")
	if mappings == nil {
		return ""
	}
	var pairs []string
	for _, group := range []string{"components", "features"} {
		groupNode := mappingChild(mappings, group)
		if groupNode == nil {
			continue
		}
		for _, tier := range []string{"type", "name"} {
			tierNode := mappingChild(groupNode, tier)
			if tierNode == nil || tierNode.Kind != yaml.MappingNode {
				continue
			}
			for i := 0; i+1 < len(tierNode.Content); i += 2 {
				keyNode := tierNode.Content[i]
				valNode := tierNode.Content[i+1]
				if valNode != nil && valNode.Kind == yaml.ScalarNode {
					pairs = append(pairs, fmt.Sprintf("`%s` -> `%s`", keyNode.Value, valNode.Value))
				}
			}
		}
	}
	if len(pairs) == 0 {
		return ""
	}
	return fmt.Sprintf("**Flat mappings**: %d entries\n%s", len(pairs), indentBullets(truncateList(pairs, 3)))
}

// legacyMappingsSection summarizes provider-keyed legacy mappings
// (one line per provider with its total entry count across
// components + features + their type / name tiers).
func legacyMappingsSection(doc *yaml.Node) string {
	mappings := mappingNodeAtPath(doc, "spec", "mappings")
	if mappings == nil {
		return ""
	}
	var lines []string
	for i := 0; i+1 < len(mappings.Content); i += 2 {
		keyNode := mappings.Content[i]
		valNode := mappings.Content[i+1]
		if keyNode.Value == "components" || keyNode.Value == "features" {
			continue
		}
		if valNode == nil || valNode.Kind != yaml.MappingNode {
			continue
		}
		count := 0
		for _, group := range []string{"components", "features"} {
			groupNode := mappingChild(valNode, group)
			if groupNode == nil {
				continue
			}
			for _, tier := range []string{"type", "name"} {
				tierNode := mappingChild(groupNode, tier)
				if tierNode != nil && tierNode.Kind == yaml.MappingNode {
					count += mappingPairCount(tierNode)
				}
			}
		}
		lines = append(lines, fmt.Sprintf("`%s`: %d entries", keyNode.Value, count))
	}
	if len(lines) == 0 {
		return ""
	}
	return fmt.Sprintf("**Legacy provider-keyed mappings**: %d providers\n%s", len(lines), indentBullets(joinLines(lines)))
}
