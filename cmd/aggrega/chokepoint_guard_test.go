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
//  2. Every pullConfig / pushConfig / signConfig — and the relocated
//     lintops.Config — built in production code sets its workspace field.
//     Production must pass the context-resolved workspace; the nil →
//     resolve-from-CWD fallback exists only so tests can invoke the command and
//     core helpers directly. A production caller leaving the workspace field
//     unset would silently re-resolve from CWD and ignore --workspace-root —
//     reintroducing per-command divergence.
//
// Test files are exempt (the fallback is theirs to use).
func TestWorkspaceResolutionChokepointGuard(t *testing.T) {
	dirs := []string{".", filepath.Join("..", "..", "internal", "serve")}
	cfgTypes := map[string]bool{"pullConfig": true, "pushConfig": true, "signConfig": true}

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
					// The config-struct commands must thread the context-resolved
					// workspace into their config literal. The same-package configs
					// (pull/push/sign) are bare identifiers carrying a `ws` field; the
					// relocated lint config is a `lintops.Config` selector carrying a
					// `Workspace` field. Either way, a production literal that omits the
					// workspace field would silently re-resolve from CWD and ignore
					// --workspace-root.
					typeName, wsField := "", ""
					switch tp := v.Type.(type) {
					case *ast.Ident:
						if cfgTypes[tp.Name] {
							typeName, wsField = tp.Name, "ws"
						}
					case *ast.SelectorExpr:
						if pkg, ok := tp.X.(*ast.Ident); ok && pkg.Name == "lintops" && tp.Sel.Name == "Config" {
							typeName, wsField = "lintops.Config", "Workspace"
						}
					}
					if typeName == "" {
						return true
					}
					hasWS := false
					for _, elt := range v.Elts {
						if kv, ok := elt.(*ast.KeyValueExpr); ok {
							if k, ok := kv.Key.(*ast.Ident); ok && k.Name == wsField {
								hasWS = true
							}
						}
					}
					if !hasWS {
						t.Errorf("%s: %s literal without a %s field — production must thread the context-resolved workspace (workspaceFromContext); the nil/CWD fallback is test-only", fset.Position(v.Pos()), typeName, wsField)
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

// TestEgressConstructionChokepointGuard enforces the single network-egress
// construction path structurally. Every network client and transport is
// constructed inside pkg/egress (the offline-gate home) so a package cannot
// build its own client and egress around the offline decision. The guard is
// deny-by-default over a whole-module walk: it enumerates every package and
// asserts the negative — no raw http.Client / http.Transport composite literal,
// no http.DefaultClient / http.DefaultTransport reference, no http.Get / Post /
// Head / PostForm call, and no net.Dial / DialContext / DialTimeout call —
// exempting only pkg/egress (the gate) and pkg/oci (which owns the oras-specific
// repository/registry construction and receives its client from the gate). A
// package that does not exist today is covered the moment it is added: the walk
// visits it and the assertion is negative-by-default, so a future package that
// writes http.Get or builds &http.Client{} trips this guard with zero edits
// here — there is no offender list to maintain, only the two-name exemption set.
//
// Test files are exempt: a test legitimately builds an httptest-backed client
// (or a raw client against a local server) to exercise a real code path, and
// that independence is intentional.
func TestEgressConstructionChokepointGuard(t *testing.T) {
	root := filepath.Join("..", "..")
	egressDir := filepath.Join(root, "pkg", "egress")
	ociDir := filepath.Join(root, "pkg", "oci")

	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			base := d.Name()
			if p == egressDir || p == ociDir || base == "vendor" || base == "testdata" ||
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
			switch v := n.(type) {
			case *ast.CompositeLit:
				// &http.Client{} / http.Client{} and the same for http.Transport:
				// a raw network-client or transport construction outside the gate.
				if sel, ok := v.Type.(*ast.SelectorExpr); ok {
					if id, ok := sel.X.(*ast.Ident); ok && id.Name == "http" &&
						(sel.Sel.Name == "Client" || sel.Sel.Name == "Transport") {
						t.Errorf("%s: raw http.%s construction outside pkg/egress — obtain the client/transport from pkg/egress", fset.Position(v.Pos()), sel.Sel.Name)
					}
				}
			case *ast.SelectorExpr:
				// http.DefaultClient / http.DefaultTransport: the ambient client
				// that egresses without going through the gate.
				if id, ok := v.X.(*ast.Ident); ok && id.Name == "http" &&
					(v.Sel.Name == "DefaultClient" || v.Sel.Name == "DefaultTransport") {
					t.Errorf("%s: http.%s referenced outside pkg/egress — obtain the client/transport from pkg/egress", fset.Position(v.Pos()), v.Sel.Name)
				}
			case *ast.CallExpr:
				sel, ok := v.Fun.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				id, ok := sel.X.(*ast.Ident)
				if !ok {
					return true
				}
				switch id.Name {
				case "http":
					if sel.Sel.Name == "Get" || sel.Sel.Name == "Post" ||
						sel.Sel.Name == "Head" || sel.Sel.Name == "PostForm" {
						t.Errorf("%s: http.%s outside pkg/egress — route the fetch through a client obtained from pkg/egress", fset.Position(v.Pos()), sel.Sel.Name)
					}
				case "net":
					if sel.Sel.Name == "Dial" || sel.Sel.Name == "DialContext" ||
						sel.Sel.Name == "DialTimeout" {
						t.Errorf("%s: net.%s outside pkg/egress — network dialing belongs behind the egress gate", fset.Position(v.Pos()), sel.Sel.Name)
					}
				}
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
}

// TestEgressLibraryImportChokepointGuard enforces the single home for each
// library-owned network transport. Three libraries construct their own HTTP
// client internally, so a raw-http-construction matcher cannot see their egress:
// go-git (the clone transport), sigstore-go's keyless signing, and the helm
// getter. The offline gate threads its transport into each library at its home
// package; this guard keeps a NEW package from importing one of these libraries
// and egressing around the gate.
//
// It is deny-by-default over a whole-module walk with a per-library home
// exemption: each library import is permitted only in pkg/egress (the gate) and
// the one domain package that owns that library's use. A package that does not
// exist today is covered the moment it is added — a future package importing
// go-git or the helm getter trips this guard with zero edits here. The live-TUF
// package is the strict case: it is permitted only in pkg/egress/livetrust,
// because the live-trust-root fetch is single-sourced there
// (livetrust.TrustedRoot). That gate is a sibling of the egress core rather
// than part of it, so the sigstore TUF machinery stays off the build/render path.
//
// The go-git matcher targets the network package path exactly
// (github.com/go-git/go-git/v5, the CloneContext home), not the module prefix,
// so the non-network gitignore subpackage imported by the workspace walker does
// not trip. Test files are exempt: a test legitimately imports a library to
// build a fixture.
func TestEgressLibraryImportChokepointGuard(t *testing.T) {
	root := filepath.Join("..", "..")
	// Each banned import path maps to the set of package directories (relative to
	// the module root, slash-separated) permitted to import it.
	banned := map[string]map[string]bool{
		"github.com/go-git/go-git/v5": {
			"pkg/egress":                   true,
			"pkg/sourceresolver/terraform": true,
		},
		"github.com/sigstore/sigstore-go/pkg/sign": {
			"pkg/egress":  true,
			"pkg/signing": true,
		},
		"github.com/sigstore/sigstore-go/pkg/tuf": {
			"pkg/egress/livetrust": true,
		},
		"helm.sh/helm/v3/pkg/getter": {
			"pkg/egress":              true,
			"pkg/sourceresolver/helm": true,
		},
	}

	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			base := d.Name()
			if base == "vendor" || base == "testdata" || (p != root && strings.HasPrefix(base, ".")) {
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
			return nil
		}
		rel, rerr := filepath.Rel(root, filepath.Dir(p))
		if rerr != nil {
			t.Fatalf("rel %s: %v", p, rerr)
		}
		pkgDir := filepath.ToSlash(rel)

		fset := token.NewFileSet()
		node, perr := parser.ParseFile(fset, p, nil, parser.ImportsOnly)
		if perr != nil {
			t.Fatalf("parse %s: %v", p, perr)
		}
		for _, imp := range node.Imports {
			path := strings.Trim(imp.Path.Value, `"`)
			allowed, isBanned := banned[path]
			if !isBanned {
				continue
			}
			if !allowed[pkgDir] {
				t.Errorf("%s: package %q imports the library-owned network package %q outside its egress-gated home — route the transport through pkg/egress or the library's home package", fset.Position(imp.Pos()), pkgDir, path)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
}

// TestResolveCatalogDirsChokepointGuard enforces the single home for the
// catalog-dir precedence stack structurally. The flag-dirs-prepend-config
// first-win assembly lives once, exported from pkg/workspaceconfig as
// ResolveCatalogDirs; a command or the daemon re-rolling a local
// resolveCatalogDirs would reintroduce the byte-identical copy this
// consolidation removed, where the two drift silently the moment one is
// touched. The guard fails if a func resolveCatalogDirs declaration reappears
// in the CLI or MCP packages.
//
// Test files are exempt: this guard file names the symbol in prose, and the
// unit test for the mechanic lives with its home in pkg/workspaceconfig.
func TestResolveCatalogDirsChokepointGuard(t *testing.T) {
	dirs := []string{".", filepath.Join("..", "..", "internal", "mcp")}

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
				fn, ok := n.(*ast.FuncDecl)
				if !ok {
					return true
				}
				if fn.Recv == nil && fn.Name.Name == "resolveCatalogDirs" {
					t.Errorf("%s: func resolveCatalogDirs re-rolled outside its home — route catalog-dir assembly through workspaceconfig.ResolveCatalogDirs", fset.Position(fn.Pos()))
				}
				return true
			})
		}
	}
}
