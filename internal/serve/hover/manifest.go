// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package hover

import (
	"fmt"

	"go.yaml.in/yaml/v3"
)

// Manifest renders the hover content for a Manifest target, surfacing
// representative mapping pairs (top 3).
//
//   - **Manifest** `<URI>` `v<version>`
//   - <Description>
//   - ---
//   - **Mappings**: N entries
//   - `<component-type-URI>` -> `<recipe-URI>`
//   - ...top 3 then truncation
func Manifest(e Entity) string {
	name := scalarValueAtPath(e.Doc, "metadata", "name")
	version := scalarValueAtPath(e.Doc, "metadata", "version")
	desc := scalarValueAtPath(e.Doc, "metadata", "description")

	body := []string{}
	if section := mappingsSection(e.Doc); section != "" {
		body = append(body, section)
	}

	return Assemble(
		Header("Manifest", name, version),
		Description(desc),
		Separator(),
		joinLines(body),
	)
}

// mappingsSection enumerates spec.mappings.{components,features}.{type,name}
// pairs in author-order across the union (top 3 + truncation).
func mappingsSection(doc *yaml.Node) string {
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
	return fmt.Sprintf("**Mappings**: %d entries\n%s", len(pairs), indentBullets(truncateList(pairs, 3)))
}
