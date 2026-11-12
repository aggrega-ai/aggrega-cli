// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

// Package completion implements the aggrega/completion handler's
// per-cursor-context candidate generators. The daemon's
// dispatchCompletion calls HandleCompletion which classifies the
// cursor and routes to one of 14 per-context handlers.
//
// The package is organized per-cursor-context (not per-entity-kind)
// because the natural axis of variation is the cursor position, not
// the host file's kind — a `metadata.labels.<key>:` cursor produces
// the same candidate set regardless of whether the host is a
// Component or an Aggregate.
package completion

import (
	"fmt"
	"strings"

	"go.yaml.in/yaml/v3"
)

// CompletionItem mirrors the LSP CompletionItem wire shape. Defined
// in this package rather than importing from internal/serve to keep
// the package boundary clean — the dispatcher in internal/serve
// converts to internal/serve's CompletionItem type at the boundary.
type CompletionItem struct {
	Label         string
	Kind          int
	Detail        string
	Documentation string
	SortText      string
}

// LSP CompletionItemKind enum values (subset; full spec lists 25).
const (
	KindText      = 1
	KindMethod    = 2
	KindField     = 5
	KindClass     = 7
	KindModule    = 9
	KindValue     = 12
	KindEnum      = 13
	KindKeyword   = 14
	KindSnippet   = 15
	KindFile      = 17
	KindReference = 18
	KindFolder    = 19
)

// buildItem assembles a CompletionItem with the supplied label, LSP
// kind, detail, and sortText. Used by per-context handlers to keep
// item construction uniform.
func buildItem(label string, kind int, detail, sortText string) CompletionItem {
	return CompletionItem{
		Label:    label,
		Kind:     kind,
		Detail:   detail,
		SortText: sortText,
	}
}

// orderedItems converts an ordered slice of label strings into
// CompletionItems with author-order sortText (prefix "<NN> " where NN
// is the zero-padded slice index). Used by closed-set handlers whose
// authored order is the desired ranking.
func orderedItems(labels []string, kind int, detailFor func(string) string) []CompletionItem {
	out := make([]CompletionItem, len(labels))
	for i, label := range labels {
		detail := ""
		if detailFor != nil {
			detail = detailFor(label)
		}
		out[i] = buildItem(label, kind, detail, fmt.Sprintf("%03d %s", i, label))
	}
	return out
}

// alphabeticalItems builds CompletionItems for the supplied labels
// sorted alphabetically. Both the returned slice's order AND each
// item's sortText use alphabetical ordering of the label — clients
// that sort by sortText AND clients that respect server-side ordering
// see the same canonical order. Used by handlers whose desired order
// is alphabetical (JSON Schema keywords, URI candidates, path
// candidates, local-name candidates).
func alphabeticalItems(labels []string, kind int, detailFor func(string) string) []CompletionItem {
	sorted := make([]string, len(labels))
	copy(sorted, labels)
	stringSliceSort(sorted)
	out := make([]CompletionItem, len(sorted))
	for i, label := range sorted {
		detail := ""
		if detailFor != nil {
			detail = detailFor(label)
		}
		out[i] = buildItem(label, kind, detail, label)
	}
	return out
}

// schemaExtensionItems builds CompletionItems for Aggrega schema
// extensions with sortText prefix "z " so they sort after vanilla
// JSON Schema keywords (which use the keyword text as sortText).
func schemaExtensionItems(labels []string, kind int, detailFor func(string) string) []CompletionItem {
	out := make([]CompletionItem, len(labels))
	for i, label := range labels {
		detail := ""
		if detailFor != nil {
			detail = detailFor(label)
		}
		out[i] = buildItem(label, kind, detail, "z "+label)
	}
	return out
}

// filterByPrefix returns items whose Label starts with prefix. Used
// for the trigger-character "/" prefix-narrowing behavior — when the
// operator types "core/" + triggers on "/", the candidate list
// narrows to URIs starting with "core/".
//
// Empty prefix returns items unchanged.
func filterByPrefix(items []CompletionItem, prefix string) []CompletionItem {
	if prefix == "" {
		return items
	}
	out := make([]CompletionItem, 0, len(items))
	for _, item := range items {
		if strings.HasPrefix(item.Label, prefix) {
			out = append(out, item)
		}
	}
	return out
}

// documentRoot unwraps a yaml.DocumentNode to its single content
// child. Returns nil when the document is empty.
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

// scalarValueAtPath walks doc following the supplied mapping keys and
// returns the scalar text at the end of the path. Returns "" when any
// intermediate key is missing or the terminal node is not a scalar.
// Mirrors the helper in internal/serve/hover/common.go.
func scalarValueAtPath(doc *yaml.Node, path ...string) string {
	n := documentRoot(doc)
	for _, key := range path {
		if n == nil || n.Kind != yaml.MappingNode {
			return ""
		}
		var next *yaml.Node
		for i := 0; i+1 < len(n.Content); i += 2 {
			if n.Content[i].Value == key {
				next = n.Content[i+1]
				break
			}
		}
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

// EnumerateLocalComponentKeys is the exported alias for the
// intra-document walker used by the dispatcher's UsesTarget branch.
// Distinct from workspace enumerators — walks ONE document, no
// parse-cache or workspace-walker interaction.
func EnumerateLocalComponentKeys(sourceDoc *yaml.Node) []string {
	return enumerateLocalComponentKeys(sourceDoc)
}

// enumerateLocalComponentKeys walks the source document's spec for
// Component instance keys (`spec.components.<key>:` + recursively
// nested `spec.subsystems.<*>.components.<key>:`). Returns the
// deduped sorted list of keys. Used by the UsesTarget completion
// handler.
//
// Distinct from workspace enumerators — this walks ONE document, not
// the workspace. No parse-cache or workspace-walker interaction.
func enumerateLocalComponentKeys(sourceDoc *yaml.Node) []string {
	root := documentRoot(sourceDoc)
	spec := mappingChild(root, "spec")
	if spec == nil {
		return nil
	}
	seen := map[string]bool{}
	collectComponentKeys(spec, seen)
	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	// Sort alphabetically.
	stringSliceSort(out)
	return out
}

// collectComponentKeys recurses spec subtree: adds keys from
// `spec.components.<key>:` + descends `spec.subsystems.<*>.<subtree>`.
// Idempotent via the seen map.
func collectComponentKeys(specMap *yaml.Node, seen map[string]bool) {
	if specMap == nil || specMap.Kind != yaml.MappingNode {
		return
	}
	if components := mappingChild(specMap, "components"); components != nil && components.Kind == yaml.MappingNode {
		for i := 0; i+1 < len(components.Content); i += 2 {
			seen[components.Content[i].Value] = true
		}
	}
	if subsystems := mappingChild(specMap, "subsystems"); subsystems != nil && subsystems.Kind == yaml.MappingNode {
		for i := 0; i+1 < len(subsystems.Content); i += 2 {
			collectComponentKeys(subsystems.Content[i+1], seen)
		}
	}
}

// stringSliceSort sorts s ascending in place. Avoids importing sort
// at the call site.
func stringSliceSort(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j-1] > s[j]; j-- {
			s[j-1], s[j] = s[j], s[j-1]
		}
	}
}
