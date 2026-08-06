// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package hover renders LSP MarkupContent for cursor positions across
// the entity-kind set. The daemon's aggrega/hover dispatcher classifies
// the cursor, resolves the target entity, reads the target's kind, and
// invokes the corresponding per-kind renderer from this package.
//
// Each renderer accepts an Entity (parsed YAML root + raw bytes) and
// returns a markdown string. The dispatcher wraps the string in
// MarkupContent{Kind: "markdown", Value: <string>}.
package hover

import (
	"fmt"
	"strings"

	"go.yaml.in/yaml/v3"
)

// Entity bundles the parsed YAML root + raw bytes of a hover target.
// Renderers consume the parsed node for typed field access and the
// bytes for any sub-tree YAML re-emission (schema excerpt).
type Entity struct {
	Doc     *yaml.Node
	Content []byte
}

// Header renders the bold-kind + URI + version line that opens every
// hover content block. When version is empty the version-fence is
// omitted.
func Header(kind, uri, version string) string {
	if version == "" {
		return fmt.Sprintf("**%s** `%s`", kind, uri)
	}
	return fmt.Sprintf("**%s** `%s` `v%s`", kind, uri, version)
}

// Description renders the description paragraph when desc is
// non-empty; returns empty string otherwise.
func Description(desc string) string {
	if desc == "" {
		return ""
	}
	return desc
}

// Separator returns the horizontal-rule line markdown renderers use
// between hover sections.
func Separator() string {
	return "---"
}

// Extends locates spec.extends and renders the **Extends**: line when
// the value is non-empty; returns empty string otherwise.
func Extends(doc *yaml.Node) string {
	parent := scalarValueAtPath(doc, "spec", "extends")
	if parent == "" {
		return ""
	}
	return fmt.Sprintf("**Extends**: `%s`", parent)
}

// SchemaExcerpt locates spec.schema in doc and renders the first
// maxLines lines as a ```yaml fence. Returns empty string when
// spec.schema is absent. Lines beyond the cutoff are signalled with an
// appended ellipsis line inside the fence.
func SchemaExcerpt(doc *yaml.Node, maxLines int) string {
	schemaNode := mappingChild(documentRoot(doc), "spec")
	if schemaNode != nil {
		schemaNode = mappingChild(schemaNode, "schema")
	}
	if schemaNode == nil {
		return ""
	}
	out, err := yaml.Marshal(schemaNode)
	if err != nil {
		return ""
	}
	lines := strings.Split(strings.TrimRight(string(out), "\n"), "\n")
	truncated := false
	if len(lines) > maxLines {
		lines = lines[:maxLines]
		truncated = true
	}
	var b strings.Builder
	b.WriteString("**Schema** (excerpt)\n\n```yaml\n")
	b.WriteString(strings.Join(lines, "\n"))
	b.WriteString("\n")
	if truncated {
		b.WriteString("# ...\n")
	}
	b.WriteString("```")
	return b.String()
}

// Assemble concatenates the supplied non-empty sections with paragraph
// breaks (\n\n). Empty sections are dropped silently. VS Code's
// markdown renderer respects double-newline as a paragraph break,
// single-newline as a soft break — the double-newline convention is
// the load-bearing one for hover content.
func Assemble(sections ...string) string {
	nonEmpty := make([]string, 0, len(sections))
	for _, s := range sections {
		if s != "" {
			nonEmpty = append(nonEmpty, s)
		}
	}
	return strings.Join(nonEmpty, "\n\n")
}

// truncateList renders a top-N items + ellipsis-count line for the
// Aggregate / Manifest representative-item lists. Items beyond the
// max are summarized as "...and N-max more". Returns empty string for
// an empty input slice.
func truncateList(items []string, max int) string {
	if len(items) == 0 {
		return ""
	}
	if len(items) <= max {
		return strings.Join(items, "\n")
	}
	head := strings.Join(items[:max], "\n")
	return fmt.Sprintf("%s\n...and %d more", head, len(items)-max)
}

// documentRoot unwraps a yaml.DocumentNode to its single content
// child. Non-document nodes pass through unchanged. Returns nil when
// the document is empty.
func documentRoot(doc *yaml.Node) *yaml.Node {
	if doc == nil {
		return nil
	}
	if doc.Kind == yaml.DocumentNode {
		if len(doc.Content) == 0 {
			return nil
		}
		return doc.Content[0]
	}
	return doc
}

// mappingChild returns the value node for key in the supplied
// mapping, or nil when n is not a mapping or key is absent.
func mappingChild(n *yaml.Node, key string) *yaml.Node {
	if n == nil || n.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		if n.Content[i].Value == key {
			return n.Content[i+1]
		}
	}
	return nil
}

// scalarValueAtPath walks doc following the supplied mapping keys and
// returns the scalar text at the end of the path. Returns "" when any
// intermediate key is missing or the terminal node is not a scalar.
func scalarValueAtPath(doc *yaml.Node, path ...string) string {
	n := documentRoot(doc)
	for _, key := range path {
		next := mappingChild(n, key)
		if next == nil {
			return ""
		}
		n = next
	}
	if n != nil && n.Kind == yaml.ScalarNode {
		return n.Value
	}
	return ""
}

// mappingChildKeysFrom returns the author-order list of keys in the
// mapping at path. Returns nil when the path is absent or the
// terminal node is not a mapping.
func mappingChildKeysFrom(doc *yaml.Node, path ...string) []string {
	n := documentRoot(doc)
	for _, key := range path {
		next := mappingChild(n, key)
		if next == nil {
			return nil
		}
		n = next
	}
	if n == nil || n.Kind != yaml.MappingNode {
		return nil
	}
	out := make([]string, 0, len(n.Content)/2)
	for i := 0; i+1 < len(n.Content); i += 2 {
		out = append(out, n.Content[i].Value)
	}
	return out
}

// mappingNodeAtPath returns the mapping node at the end of path or
// nil. Used by renderers that need to count entries or enumerate
// child keys at a known mapping location.
func mappingNodeAtPath(doc *yaml.Node, path ...string) *yaml.Node {
	n := documentRoot(doc)
	for _, key := range path {
		next := mappingChild(n, key)
		if next == nil {
			return nil
		}
		n = next
	}
	if n == nil || n.Kind != yaml.MappingNode {
		return nil
	}
	return n
}

// sequenceLenAtPath returns the length of the sequence at path or 0.
// Used by renderers that summarize sequence-shaped fields (Recipe
// pipeline step count, etc.).
func sequenceLenAtPath(doc *yaml.Node, path ...string) int {
	n := documentRoot(doc)
	for _, key := range path {
		next := mappingChild(n, key)
		if next == nil {
			return 0
		}
		n = next
	}
	if n == nil || n.Kind != yaml.SequenceNode {
		return 0
	}
	return len(n.Content)
}
