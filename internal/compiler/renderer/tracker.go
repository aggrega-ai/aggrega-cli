// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"sort"
	"strings"
	"text/template"
	"text/template/parse"
)

// CollectTemplateFieldAccesses parses a Go template source and returns the set
// of dot-qualified paths it references (e.g., ".values.port", ".infra.replicas",
// ".context.inject.env.X"). Used by ghost-infrastructure tracking to discover
// which .values / .infra keys a valuesMapping template actually reads.
//
// Empty template text returns (nil, nil). Parse errors are returned so callers
// can decide whether to surface them (normal rendering already reports parse
// errors — tracker callers typically swallow them and skip warning emission).
func CollectTemplateFieldAccesses(tmplText string) ([]string, error) {
	if tmplText == "" {
		return nil, nil
	}
	// Minimal FuncMap; we only need the parse tree, not execution.
	tmpl, err := template.New("tracker").Option("missingkey=zero").Parse(tmplText)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	collectFromNode(tmpl.Root, seen)
	paths := make([]string, 0, len(seen))
	for p := range seen {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	return paths, nil
}

// collectFromNode walks a parse.Node tree and records any FieldNode / ChainNode
// it encounters. Dot-qualified paths are joined with "." and prefixed with ".".
func collectFromNode(n parse.Node, seen map[string]bool) {
	if n == nil {
		return
	}
	switch v := n.(type) {
	case *parse.ListNode:
		if v == nil {
			return
		}
		for _, child := range v.Nodes {
			collectFromNode(child, seen)
		}
	case *parse.ActionNode:
		collectFromPipe(v.Pipe, seen)
	case *parse.IfNode:
		collectFromPipe(v.Pipe, seen)
		collectFromNode(v.List, seen)
		collectFromNode(v.ElseList, seen)
	case *parse.RangeNode:
		collectFromPipe(v.Pipe, seen)
		collectFromNode(v.List, seen)
		collectFromNode(v.ElseList, seen)
	case *parse.WithNode:
		collectFromPipe(v.Pipe, seen)
		collectFromNode(v.List, seen)
		collectFromNode(v.ElseList, seen)
	case *parse.TemplateNode:
		collectFromPipe(v.Pipe, seen)
	}
}

func collectFromPipe(p *parse.PipeNode, seen map[string]bool) {
	if p == nil {
		return
	}
	for _, cmd := range p.Cmds {
		for _, arg := range cmd.Args {
			switch a := arg.(type) {
			case *parse.FieldNode:
				if len(a.Ident) > 0 {
					seen["."+strings.Join(a.Ident, ".")] = true
				}
			case *parse.ChainNode:
				// Chain: .foo.bar syntax on an expression result.
				if len(a.Field) > 0 {
					seen["."+strings.Join(a.Field, ".")] = true
				}
			case *parse.PipeNode:
				collectFromPipe(a, seen)
			}
		}
	}
}

// UnreadKeys returns top-level keys of `data` whose `<root>.<key>` path is
// NOT referenced by any entry in `references`. Used to emit "unused key"
// warnings for Ghost Infrastructure — catalog authors add infra/values entries
// that the Recipe's valuesMapping never consumes.
func UnreadKeys(references []string, root string, data map[string]interface{}) []string {
	if len(data) == 0 {
		return nil
	}
	// Reference paths begin with ".". Build the "readset" of top-level subkeys
	// of root that appear in any reference. A reference like ".values.port"
	// marks "port" as read. A reference like ".values" alone (whole-object
	// iteration via `range`) marks every key as read.
	rootWithDot := root
	if !strings.HasPrefix(rootWithDot, ".") {
		rootWithDot = "." + rootWithDot
	}
	readAll := false
	readset := map[string]bool{}
	for _, ref := range references {
		if ref == rootWithDot {
			readAll = true
			break
		}
		if !strings.HasPrefix(ref, rootWithDot+".") {
			continue
		}
		rest := strings.TrimPrefix(ref, rootWithDot+".")
		// Take only the first segment — nested reads (e.g. ".values.db.port") mark "db" as read.
		seg := rest
		if i := strings.Index(rest, "."); i >= 0 {
			seg = rest[:i]
		}
		readset[seg] = true
	}
	if readAll {
		return nil
	}
	var unread []string
	for key := range data {
		if !readset[key] {
			unread = append(unread, key)
		}
	}
	sort.Strings(unread)
	return unread
}
