// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
)

// TestWorkspaceResolutionChokepointGuard enforces the single workspace
// resolution path structurally. Two drift classes the compiler does not catch
// on its own (workspaceconfig's low-level loaders are already unexported, so a
// direct call to them is a compile error):
//
//  1. No os.Getwd() in the CLI or daemon packages. Workspace-root discovery
//     belongs to workspaceconfig.Resolve; a command re-rolling CWD discovery is
//     exactly the divergence this work removed.
//  2. Every pullConfig / pushConfig / lintConfig built in production code sets
//     the ws field. Production must pass the context-resolved workspace; the
//     nil → resolve-from-CWD fallback in workspaceOrResolve exists only so tests
//     can invoke the command helpers directly. A production caller leaving ws
//     unset would silently re-resolve from CWD and ignore --workspace-root —
//     reintroducing per-command divergence.
//
// Test files are exempt (the fallback is theirs to use).
func TestWorkspaceResolutionChokepointGuard(t *testing.T) {
	dirs := []string{".", filepath.Join("..", "..", "internal", "serve")}
	cfgTypes := map[string]bool{"pullConfig": true, "pushConfig": true, "lintConfig": true, "signConfig": true}

	for _, dir := range dirs {
		matches, err := filepath.Glob(filepath.Join(dir, "*.go"))
		if err != nil {
			t.Fatalf("glob %s: %v", dir, err)
		}
		for _, file := range matches {
			if strings.HasSuffix(file, "_test.go") {
				continue
			}
			fset := token.NewFileSet()
			node, err := parser.ParseFile(fset, file, nil, 0)
			if err != nil {
				t.Fatalf("parse %s: %v", file, err)
			}
			ast.Inspect(node, func(n ast.Node) bool {
				switch v := n.(type) {
				case *ast.CallExpr:
					if sel, ok := v.Fun.(*ast.SelectorExpr); ok {
						if id, ok := sel.X.(*ast.Ident); ok && id.Name == "os" && sel.Sel.Name == "Getwd" {
							t.Errorf("%s: os.Getwd() outside the workspaceconfig.Resolve chokepoint — route workspace-root discovery through Resolve", fset.Position(v.Pos()))
						}
					}
					// The param-threaded commands (build, inspect) pass the
					// resolved workspace straight into workspaceOrResolve rather
					// than via a config struct. A literal nil here in production
					// would silently re-resolve from CWD and ignore
					// --workspace-root — the same footgun the ws-field check
					// guards for the struct commands.
					if id, ok := v.Fun.(*ast.Ident); ok && id.Name == "workspaceOrResolve" {
						if len(v.Args) == 1 {
							if arg, ok := v.Args[0].(*ast.Ident); ok && arg.Name == "nil" {
								t.Errorf("%s: workspaceOrResolve(nil) in production — pass the context-resolved workspace (workspaceFromContext); the nil/CWD fallback is test-only", fset.Position(v.Pos()))
							}
						}
					}
				case *ast.CompositeLit:
					id, ok := v.Type.(*ast.Ident)
					if !ok || !cfgTypes[id.Name] {
						return true
					}
					hasWS := false
					for _, elt := range v.Elts {
						if kv, ok := elt.(*ast.KeyValueExpr); ok {
							if k, ok := kv.Key.(*ast.Ident); ok && k.Name == "ws" {
								hasWS = true
							}
						}
					}
					if !hasWS {
						t.Errorf("%s: %s literal without a ws field — production must thread the context-resolved workspace (workspaceFromContext); the nil/CWD fallback is test-only", fset.Position(v.Pos()), id.Name)
					}
				}
				return true
			})
		}
	}
}

// TestOCIRemoteConstructionChokepointGuard enforces the single registry /
// repository construction path structurally. Raw remote.NewRegistry and
// remote.NewRepository re-roll the loopback plain-HTTP decision and the
// Docker-credential auth wiring that oci.NewRemoteRegistry / oci.NewRemoteRepository
// single-source; a production call site bypassing those homes reintroduces the
// exact transport-wiring divergence this consolidation removed. The one place
// raw construction is allowed is the home itself (pkg/oci).
//
// Test files are exempt: test helpers build deliberately-independent registries
// (plain-HTTP against an httptest server, no credential store) to exercise the
// real enumeration path, and that independence is intentional.
func TestOCIRemoteConstructionChokepointGuard(t *testing.T) {
	root := filepath.Join("..", "..")
	ociDir := filepath.Join(root, "pkg", "oci")

	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			base := d.Name()
			if p == ociDir || base == "vendor" || base == "testdata" ||
				(p != root && strings.HasPrefix(base, ".")) {
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
			return nil
		}
		fset := token.NewFileSet()
		node, perr := parser.ParseFile(fset, p, nil, 0)
		if perr != nil {
			t.Fatalf("parse %s: %v", p, perr)
		}
		ast.Inspect(node, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			pkgID, ok := sel.X.(*ast.Ident)
			if !ok || pkgID.Name != "remote" {
				return true
			}
			if sel.Sel.Name == "NewRegistry" || sel.Sel.Name == "NewRepository" {
				t.Errorf("%s: raw remote.%s outside pkg/oci — route registry/repository construction through oci.NewRemoteRegistry / oci.NewRemoteRepository", fset.Position(call.Pos()), sel.Sel.Name)
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
}
