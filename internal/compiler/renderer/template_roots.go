// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"fmt"
	"reflect"
	"slices"
	"strings"
	"text/template"
	"text/template/parse"

	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
)

// templateRootRef is one field access a template makes against its context
// root. Segs holds the path segments with the root first; Path holds the
// authored dot-path a diagnostic quotes back to the author.
type templateRootRef struct {
	Segs []string
	Path string
}

// collectTemplateRootRefs returns every field access a parsed template makes
// against its context root, deduplicated by path and in first-appearance order
// so the reference a diagnostic names is the same one on every run.
//
// Only root-anchored accesses are collected, which is what makes the result
// usable as a root-name check rather than as a general field census:
//
//   - .values.port is anchored at the context root and contributes root
//     "values".
//   - (default dict .values.tls).ca is anchored at the parenthesised
//     expression's result, not at the context root, so the trailing .ca
//     contributes nothing while the inner .values.tls still contributes
//     "values". This is what keeps a defensively-written optional key from
//     reading as a reference to a root named "ca".
//   - inside a range or with body the dot is rebound to the element, so .name
//     there names a field of that element and contributes nothing. The
//     range/with pipeline itself is evaluated against the surrounding dot and
//     does contribute. An else branch runs with the dot unchanged and so
//     contributes exactly as its surrounding scope does.
//   - $ names the context root under any rebinding, so $.values.x contributes
//     "values" even inside a rebound body. Every other variable names a value
//     the template itself declared and contributes nothing.
//
// A template invoked by name receives whatever the invocation passes as its
// dot, so the invocation's pipeline contributes and the invoked body does not.
//
// This is the walk every root ALLOW-list shares — the evaluator's context-root
// check and the provides self-containment check both call it and differ only in
// the set they compare against. Under-collecting is the fail-safe direction for
// an allow-list: a reference this walk does not attribute to the context root is
// simply not checked, whereas attributing one wrongly would refuse legitimate
// authoring. collectTopLevelFields is the deliberately over-collecting
// counterpart the deny-lists use; see the note on it for why the two are not
// folded into one.
func collectTemplateRootRefs(root parse.Node) []templateRootRef {
	var refs []templateRootRef
	seen := map[string]bool{}
	walkTemplateRoots(root, true, &refs, seen)
	return refs
}

// parseTemplateForRootWalk parses template text with the evaluator's funcmap and
// delimiters and returns the tree's root node. It exists for the surfaces that
// check roots from authored text rather than from a tree they already hold; the
// evaluator itself walks the tree it is about to execute and never re-parses.
// Sharing the funcmap is what keeps the walk and the evaluation agreeing on
// which templates parse at all.
func parseTemplateForRootWalk(text, leftDelim, rightDelim string) (parse.Node, error) {
	tmpl, err := template.New("root-walk").
		Option("missingkey=zero").
		Delims(leftDelim, rightDelim).
		Funcs(aggregaFuncMap()).
		Parse(text)
	if err != nil {
		return nil, err
	}
	return tmpl.Tree.Root, nil
}

func walkTemplateRoots(n parse.Node, dotIsRoot bool, refs *[]templateRootRef, seen map[string]bool) {
	if n == nil {
		return
	}
	switch v := n.(type) {
	case *parse.ListNode:
		if v == nil {
			return
		}
		for _, child := range v.Nodes {
			walkTemplateRoots(child, dotIsRoot, refs, seen)
		}
	case *parse.ActionNode:
		walkTemplateRootsPipe(v.Pipe, dotIsRoot, refs, seen)
	case *parse.IfNode:
		walkTemplateRootsPipe(v.Pipe, dotIsRoot, refs, seen)
		walkTemplateRoots(v.List, dotIsRoot, refs, seen)
		walkTemplateRoots(v.ElseList, dotIsRoot, refs, seen)
	case *parse.RangeNode:
		walkTemplateRootsPipe(v.Pipe, dotIsRoot, refs, seen)
		walkTemplateRoots(v.List, false, refs, seen)
		walkTemplateRoots(v.ElseList, dotIsRoot, refs, seen)
	case *parse.WithNode:
		walkTemplateRootsPipe(v.Pipe, dotIsRoot, refs, seen)
		walkTemplateRoots(v.List, false, refs, seen)
		walkTemplateRoots(v.ElseList, dotIsRoot, refs, seen)
	case *parse.TemplateNode:
		walkTemplateRootsPipe(v.Pipe, dotIsRoot, refs, seen)
	}
}

func walkTemplateRootsPipe(p *parse.PipeNode, dotIsRoot bool, refs *[]templateRootRef, seen map[string]bool) {
	if p == nil {
		return
	}
	for _, cmd := range p.Cmds {
		if cmd == nil {
			continue
		}
		for _, arg := range cmd.Args {
			walkTemplateRootsArg(arg, dotIsRoot, refs, seen)
		}
	}
}

func walkTemplateRootsArg(arg parse.Node, dotIsRoot bool, refs *[]templateRootRef, seen map[string]bool) {
	switch a := arg.(type) {
	case *parse.FieldNode:
		if dotIsRoot {
			recordTemplateRootRef(refs, seen, a.Ident, "."+strings.Join(a.Ident, "."))
		}
	case *parse.VariableNode:
		if len(a.Ident) > 1 && a.Ident[0] == "$" {
			recordTemplateRootRef(refs, seen, a.Ident[1:], "$."+strings.Join(a.Ident[1:], "."))
		}
	case *parse.ChainNode:
		// The chained fields hang off the receiver's value, not off the dot;
		// only the receiver can reach the context root.
		walkTemplateRootsArg(a.Node, dotIsRoot, refs, seen)
	case *parse.PipeNode:
		walkTemplateRootsPipe(a, dotIsRoot, refs, seen)
	}
}

func recordTemplateRootRef(refs *[]templateRootRef, seen map[string]bool, segs []string, path string) {
	if len(segs) == 0 || segs[0] == "" || seen[path] {
		return
	}
	seen[path] = true
	*refs = append(*refs, templateRootRef{Segs: segs, Path: path})
}

// contextRootNames returns the roots a render context exposes to a template, in
// declaration order. The names come from fieldJSONName — the same tag reading
// that names the keys toLowercaseMap delivers to the template — so the admitted
// set and the delivered set are one derivation rather than a hand-written list
// beside the type, and renaming a pillar cannot leave the two disagreeing.
//
// ok is false when the context is not a struct: its root set is not derivable
// from a type, and no root check applies.
func contextRootNames(data any) ([]string, bool) {
	t := reflect.TypeOf(data)
	for t != nil && (t.Kind() == reflect.Pointer || t.Kind() == reflect.Interface) {
		t = t.Elem()
	}
	if t == nil || t.Kind() != reflect.Struct {
		return nil, false
	}
	roots := make([]string, 0, t.NumField())
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}
		name, _, skip := fieldJSONName(f)
		if skip || name == "" {
			continue
		}
		roots = append(roots, name)
	}
	return roots, true
}

// checkTemplateRoots halts when a template references a root its render context
// does not define, naming the offending path and the roots that are available.
//
// The check is a positive allowlist over root NAMES taken from the context type,
// never over the context's runtime contents, because a root can be legitimately
// defined and still absent from the delivered data: a pillar declared
// omitempty is dropped when the node carries nothing for it, so a node with no
// attachments delivers no attachment pillar at all. Runtime absence therefore
// cannot tell a retired root from an empty one, and only the type can.
//
// Because the halt is a root-name comparison, it leaves a genuinely optional key
// under a valid root working: an absent key still resolves through the
// evaluator's missing-key policy, and a defensive wrapper around it still
// renders. What it removes is the ability of such a wrapper to launder an
// unknown root into an empty value — an unknown root is an authoring error at
// every nesting, wrapped or bare. The comparison looks only at a root's name and
// never at what any value means.
func checkTemplateRoots(name string, tree parse.Node, data any) error {
	roots, ok := contextRootNames(data)
	if !ok {
		return nil
	}
	for _, ref := range collectTemplateRootRefs(tree) {
		if !slices.Contains(roots, ref.Segs[0]) {
			return templateUnknownRootErr(name, ref.Path, roots)
		}
	}
	return nil
}

// templateRootsPhrase renders a root set as the prose list the unknown-root
// diagnostic quotes. Derived from the set the check consults rather than
// restated, so what the build refuses and what the author is told cannot
// disagree.
func templateRootsPhrase(roots []string) string {
	out := make([]string, 0, len(roots))
	for _, root := range roots {
		out = append(out, "."+root)
	}
	return strings.Join(out, ", ")
}

func templateUnknownRootErr(name, expr string, roots []string) error {
	return cerrors.Wrap(ErrTemplateUnknownRoot, cerrors.CatalogError, cerrors.SourceLocation{File: name},
		fmt.Sprintf("template %q references %q — the render context defines %s", name, expr, templateRootsPhrase(roots)))
}
