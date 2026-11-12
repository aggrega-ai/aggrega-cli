// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package main

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
)

// Mutation describes one identified mutation site in a Go source file.
// The struct is intentionally flat — the AST node that produced it is not
// retained because mutation application is byte-level (an AST round-trip
// would rewrite comments and whitespace, breaking the deterministic-output
// invariant the rest of the codebase relies on).
type Mutation struct {
	File        string         // absolute path to the .go file
	Operator    string         // human-readable mutation name (e.g. "equality_to_inequality")
	Position    token.Position // line/column for reporting
	Original    string         // original byte sequence at the site (e.g. "==")
	Replacement string         // mutant byte sequence (e.g. "!=")
	StartOffset int            // byte offset into file content
	EndOffset   int            // byte offset (exclusive)
}

// mutationOperator detects a mutation site at one AST node. Returns
// (tokenPos, original, replacement, true) when the node matches; the
// caller derives byte offsets and Position via the FileSet.
type mutationOperator struct {
	name   string
	detect func(n ast.Node) (pos token.Pos, original, replacement string, ok bool)
}

// operators enumerates every mutation the binary applies. Each operator is
// AST-aware (matches a specific AST shape rather than a textual pattern), so
// mutations inside string literals, comments, and struct tags are never
// produced — the AST walker doesn't descend into those positions.
var operators = []mutationOperator{
	{
		name: "equality_to_inequality",
		detect: func(n ast.Node) (token.Pos, string, string, bool) {
			be, ok := n.(*ast.BinaryExpr)
			if !ok || be.Op != token.EQL {
				return 0, "", "", false
			}
			return be.OpPos, "==", "!=", true
		},
	},
	{
		name: "inequality_to_equality",
		detect: func(n ast.Node) (token.Pos, string, string, bool) {
			be, ok := n.(*ast.BinaryExpr)
			if !ok || be.Op != token.NEQ {
				return 0, "", "", false
			}
			return be.OpPos, "!=", "==", true
		},
	},
	{
		name: "greater_to_greater_or_equal",
		detect: func(n ast.Node) (token.Pos, string, string, bool) {
			be, ok := n.(*ast.BinaryExpr)
			if !ok || be.Op != token.GTR {
				return 0, "", "", false
			}
			return be.OpPos, ">", ">=", true
		},
	},
	{
		name: "less_to_less_or_equal",
		detect: func(n ast.Node) (token.Pos, string, string, bool) {
			be, ok := n.(*ast.BinaryExpr)
			if !ok || be.Op != token.LSS {
				return 0, "", "", false
			}
			return be.OpPos, "<", "<=", true
		},
	},
	{
		name: "greater_or_equal_to_greater",
		detect: func(n ast.Node) (token.Pos, string, string, bool) {
			be, ok := n.(*ast.BinaryExpr)
			if !ok || be.Op != token.GEQ {
				return 0, "", "", false
			}
			return be.OpPos, ">=", ">", true
		},
	},
	{
		name: "less_or_equal_to_less",
		detect: func(n ast.Node) (token.Pos, string, string, bool) {
			be, ok := n.(*ast.BinaryExpr)
			if !ok || be.Op != token.LEQ {
				return 0, "", "", false
			}
			return be.OpPos, "<=", "<", true
		},
	},
	{
		name: "logical_and_to_or",
		detect: func(n ast.Node) (token.Pos, string, string, bool) {
			be, ok := n.(*ast.BinaryExpr)
			if !ok || be.Op != token.LAND {
				return 0, "", "", false
			}
			return be.OpPos, "&&", "||", true
		},
	},
	{
		name: "logical_or_to_and",
		detect: func(n ast.Node) (token.Pos, string, string, bool) {
			be, ok := n.(*ast.BinaryExpr)
			if !ok || be.Op != token.LOR {
				return 0, "", "", false
			}
			return be.OpPos, "||", "&&", true
		},
	},
	{
		name: "return_true_to_false",
		detect: func(n ast.Node) (token.Pos, string, string, bool) {
			rs, ok := n.(*ast.ReturnStmt)
			if !ok || len(rs.Results) != 1 {
				return 0, "", "", false
			}
			id, ok := rs.Results[0].(*ast.Ident)
			if !ok || id.Name != "true" {
				return 0, "", "", false
			}
			return id.Pos(), "true", "false", true
		},
	},
	{
		name: "return_false_to_true",
		detect: func(n ast.Node) (token.Pos, string, string, bool) {
			rs, ok := n.(*ast.ReturnStmt)
			if !ok || len(rs.Results) != 1 {
				return 0, "", "", false
			}
			id, ok := rs.Results[0].(*ast.Ident)
			if !ok || id.Name != "false" {
				return 0, "", "", false
			}
			return id.Pos(), "false", "true", true
		},
	},
}

// FindMutations parses src as Go source and returns every mutation site
// identified by the operator list. The src slice is the byte content of
// filename; both are needed because token.FileSet positions are 1-based
// line/column plus a 0-based byte offset into the parsed source.
func FindMutations(filename string, src []byte) ([]Mutation, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, filename, src, parser.SkipObjectResolution)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", filename, err)
	}

	var muts []Mutation
	ast.Inspect(f, func(n ast.Node) bool {
		if n == nil {
			return false
		}
		for _, op := range operators {
			pos, original, replacement, ok := op.detect(n)
			if !ok {
				continue
			}
			position := fset.Position(pos)
			muts = append(muts, Mutation{
				File:        filename,
				Operator:    op.name,
				Position:    position,
				Original:    original,
				Replacement: replacement,
				StartOffset: position.Offset,
				EndOffset:   position.Offset + len(original),
			})
		}
		return true
	})
	return muts, nil
}

// ApplyMutation returns a new byte slice with the mutation's Original
// bytes at StartOffset..EndOffset replaced by Replacement. The src slice
// is not modified — the caller writes the returned slice back to disk.
//
// Returns the source unchanged when the mutation's byte range is out of
// bounds or the bytes at the site no longer match m.Original (defensive
// against a stale mutation computed against an earlier version of src).
func ApplyMutation(src []byte, m Mutation) []byte {
	if m.StartOffset < 0 || m.EndOffset > len(src) || m.StartOffset > m.EndOffset {
		return src
	}
	if !bytes.Equal(src[m.StartOffset:m.EndOffset], []byte(m.Original)) {
		return src
	}
	out := make([]byte, 0, len(src)+len(m.Replacement)-len(m.Original))
	out = append(out, src[:m.StartOffset]...)
	out = append(out, []byte(m.Replacement)...)
	out = append(out, src[m.EndOffset:]...)
	return out
}

// collectMutationsForPackages walks the target package directories,
// reads each non-test, non-generated .go file, parses it, collects every
// mutation site, and returns the flat list. The aggregaCliRoot anchors
// relative package paths from the manifest.
func collectMutationsForPackages(aggregaCliRoot string, packageRelPaths []string) ([]Mutation, error) {
	var all []Mutation
	for _, rel := range packageRelPaths {
		dir := filepath.Join(aggregaCliRoot, rel)
		entries, err := os.ReadDir(dir)
		if err != nil {
			return nil, fmt.Errorf("read package dir %s: %w", dir, err)
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") {
				continue
			}
			path := filepath.Join(dir, e.Name())
			if shouldSkipFile(path) {
				continue
			}
			src, err := os.ReadFile(path)
			if err != nil {
				return nil, fmt.Errorf("read %s: %w", path, err)
			}
			muts, err := FindMutations(path, src)
			if err != nil {
				return nil, err
			}
			all = append(all, muts...)
		}
	}
	return all, nil
}

// shouldSkipFile reports whether the file is excluded from mutation. Test
// files don't get mutated because they exercise production code, not the
// other way around. Generated files don't get mutated because their
// behavior is governed by the generator, not by tests against the
// generated output.
func shouldSkipFile(path string) bool {
	if strings.HasSuffix(path, "_test.go") {
		return true
	}
	src, err := os.ReadFile(path)
	if err != nil {
		// Read failure surfaces later; for skip purposes, treat as
		// non-skipped so the caller's read attempt produces the actual
		// error rather than this function masking it.
		return false
	}
	prefix := src
	if len(prefix) > 200 {
		prefix = prefix[:200]
	}
	return bytes.Contains(prefix, []byte("// Code generated"))
}
