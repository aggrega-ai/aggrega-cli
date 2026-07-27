// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"sort"
	"strings"
	"text/template/parse"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// CollectTemplateFieldAccesses parses a Go template source with the
// default `{{ }}` delimiters and returns the set of dot-qualified paths
// it references (e.g., ".values.port", ".infra.replicas",
// ".context.inject.env.X"). Thin wrapper over
// CollectTemplateFieldAccessesWithDelims preserving the default-delimiter
// call shape for ghost-infrastructure tracking and other callers that
// don't carry custom delimiters.
//
// Empty template text returns (nil, nil). Parse errors are returned so
// callers can decide whether to surface them (normal rendering already
// reports parse errors — tracker callers typically swallow them and skip
// warning emission).
func CollectTemplateFieldAccesses(tmplText string) ([]string, error) {
	return CollectTemplateFieldAccessesWithDelims(tmplText, "{{", "}}")
}

// CollectTemplateFieldAccessesWithDelims parses a Go template with
// caller-controlled delimiters and returns the dot-qualified field-access
// set. Used by Phase B.3's pre-check when an external-handler engine
// (today: helm-chart) declares non-default delimiters via
// ArtifactsTemplateDelims — the pre-check must parse with the same
// delimiters Aggrega's pre-templating uses, so downstream-template syntax
// inside the file (e.g., Helm's `{{ ... }}` blocks when the engine's
// Aggrega-side delimiters are `[[ ]]`) is treated as opaque text rather
// than as parse-error-inducing template directives.
func CollectTemplateFieldAccessesWithDelims(tmplText, leftDelim, rightDelim string) ([]string, error) {
	if tmplText == "" {
		return nil, nil
	}
	// The tracker parses through the same helper the reference checks use, so a
	// template referencing catalog functions (trimSuffix, toJson, until, int,
	// default, …) parses here identically to the way it renders. Go's parser fails
	// on any undefined function identifier, so a tracker parsing with a bare
	// funcmap would reject templates the evaluator accepts — which is why the parse
	// has one home rather than one copy per surface. The funcmap only affects
	// whether Parse succeeds; the FieldNode/ChainNode walk below is unchanged.
	root, err := parseTemplateForRootWalk(tmplText, leftDelim, rightDelim)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	collectFromNode(root, seen)
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
func UnreadKeys(references []string, root string, data types.OrderedMap[any]) []string {
	if data.Len() == 0 {
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
	for _, key := range data.Keys() {
		if !readset[key] {
			unread = append(unread, key)
		}
	}
	sort.Strings(unread)
	return unread
}
