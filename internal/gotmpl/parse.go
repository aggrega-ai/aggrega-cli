// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package gotmpl

import (
	"text/template"
	"text/template/parse"
)

// ParseTree parses template text with the shared function set and the given
// delimiters and returns the tree's root node. It exists for the surfaces that
// walk authored text rather than a tree they already hold — a pre-check that runs
// before the template is dispatched, and the field-access census — while the
// evaluator walks the tree it is about to execute and never re-parses. Sharing
// the function set is what keeps every one of them agreeing with the evaluation
// about which templates parse at all.
func ParseTree(text, leftDelim, rightDelim string) (parse.Node, error) {
	tmpl, err := template.New("root-walk").
		Option("missingkey=zero").
		Delims(leftDelim, rightDelim).
		Funcs(FuncMap()).
		Parse(text)
	if err != nil {
		return nil, err
	}
	return tmpl.Tree.Root, nil
}
