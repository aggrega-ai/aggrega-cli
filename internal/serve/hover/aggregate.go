// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package hover

import (
	"fmt"

	"go.yaml.in/yaml/v3"
)

// Aggregate renders the hover content for an Aggregate target. The
// rendered shape surfaces representative items (top 3 per category)
// rather than bare counts — operator value-of-summary is "what's
// here" rather than "is this big or small."
//
//   - **Aggregate** `<URI>` `v<version>`
//   - <Description>
//   - ---
//   - **Components**: N
//   - `<key1>` (`<type-URI>`)
//   - `<key2>` (`<type-URI>`)
//   - `<key3>` (`<type-URI>`)
//     ...and N-3 more
//   - **Subsystems**: N (when present)
//   - top 3 keys + truncation
//   - **Sub-aggregates**: N (when present)
//   - top 3 keys + type URIs + truncation
func Aggregate(e Entity) string {
	name := scalarValueAtPath(e.Doc, "metadata", "name")
	version := scalarValueAtPath(e.Doc, "metadata", "version")
	desc := scalarValueAtPath(e.Doc, "metadata", "description")

	body := []string{}
	if section := componentsSection(e.Doc); section != "" {
		body = append(body, section)
	}
	if section := subsystemsSection(e.Doc); section != "" {
		body = append(body, section)
	}
	if section := subAggregatesSection(e.Doc); section != "" {
		body = append(body, section)
	}

	return Assemble(
		Header("Aggregate", name, version),
		Description(desc),
		Separator(),
		joinLines(body),
	)
}

// componentsSection renders the spec.components inventory: header
// line with count + bullet list with top-3 truncation.
func componentsSection(doc *yaml.Node) string {
	components := mappingNodeAtPath(doc, "spec", "components")
	if components == nil {
		return ""
	}
	count := mappingPairCount(components)
	if count == 0 {
		return ""
	}
	items := componentInstanceItems(components)
	return fmt.Sprintf("**Components**: %d\n%s", count, indentBullets(truncateList(items, 3)))
}

// subsystemsSection renders spec.subsystems as a top-level summary
// (top 3 subsystem keys). Sub-subsystem structure is not enumerated
// at this hover surface; operator opens the file for details.
func subsystemsSection(doc *yaml.Node) string {
	subsystems := mappingNodeAtPath(doc, "spec", "subsystems")
	if subsystems == nil {
		return ""
	}
	count := mappingPairCount(subsystems)
	if count == 0 {
		return ""
	}
	keys := mappingKeys(subsystems)
	items := make([]string, len(keys))
	for i, k := range keys {
		items[i] = fmt.Sprintf("`%s`", k)
	}
	return fmt.Sprintf("**Subsystems**: %d\n%s", count, indentBullets(truncateList(items, 3)))
}

// subAggregatesSection renders spec.aggregates (sub-Aggregate
// imports). Each sub-aggregate has a key and a type URI.
func subAggregatesSection(doc *yaml.Node) string {
	subaggs := mappingNodeAtPath(doc, "spec", "aggregates")
	if subaggs == nil {
		return ""
	}
	count := mappingPairCount(subaggs)
	if count == 0 {
		return ""
	}
	items := make([]string, 0, count)
	for i := 0; i+1 < len(subaggs.Content); i += 2 {
		keyNode := subaggs.Content[i]
		valNode := subaggs.Content[i+1]
		typeURI := ""
		if valNode != nil && valNode.Kind == yaml.MappingNode {
			typeURI = directScalarChild(valNode, "type")
		}
		if typeURI != "" {
			items = append(items, fmt.Sprintf("`%s` (`%s`)", keyNode.Value, typeURI))
		} else {
			items = append(items, fmt.Sprintf("`%s`", keyNode.Value))
		}
	}
	return fmt.Sprintf("**Sub-aggregates**: %d\n%s", count, indentBullets(truncateList(items, 3)))
}

// componentInstanceItems builds the per-Component line list:
// "`<key>` (`<type-URI>`)" for each entry in the components mapping.
func componentInstanceItems(components *yaml.Node) []string {
	items := make([]string, 0, len(components.Content)/2)
	for i := 0; i+1 < len(components.Content); i += 2 {
		keyNode := components.Content[i]
		valNode := components.Content[i+1]
		typeURI := ""
		if valNode != nil && valNode.Kind == yaml.MappingNode {
			typeURI = directScalarChild(valNode, "type")
		}
		if typeURI != "" {
			items = append(items, fmt.Sprintf("`%s` (`%s`)", keyNode.Value, typeURI))
		} else {
			items = append(items, fmt.Sprintf("`%s`", keyNode.Value))
		}
	}
	return items
}

// mappingKeys returns the author-order list of keys in a mapping
// node (already-narrowed; not a path walk).
func mappingKeys(m *yaml.Node) []string {
	if m == nil || m.Kind != yaml.MappingNode {
		return nil
	}
	out := make([]string, 0, len(m.Content)/2)
	for i := 0; i+1 < len(m.Content); i += 2 {
		out = append(out, m.Content[i].Value)
	}
	return out
}

// directScalarChild returns the scalar value at the direct child key
// of a mapping node (one-step lookup, no path walk).
func directScalarChild(m *yaml.Node, key string) string {
	if m == nil || m.Kind != yaml.MappingNode {
		return ""
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			v := m.Content[i+1]
			if v != nil && v.Kind == yaml.ScalarNode {
				return v.Value
			}
			return ""
		}
	}
	return ""
}

// indentBullets adds the "- " prefix to each line of a multi-line
// items string. Empty input returns empty output.
func indentBullets(items string) string {
	if items == "" {
		return ""
	}
	lines := splitLines(items)
	out := ""
	for i, line := range lines {
		if i > 0 {
			out += "\n"
		}
		out += "- " + line
	}
	return out
}

// splitLines splits s on "\n", returning a slice of lines (no
// trailing empty when s ends with a newline).
func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) {
		out = append(out, s[start:])
	}
	return out
}
