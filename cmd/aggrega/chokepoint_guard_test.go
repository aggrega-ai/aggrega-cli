// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

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
// resolution path structurally. Three drift classes the compiler does not catch
// on its own (workspaceconfig's low-level loaders are already unexported, so a
// direct call to them is a compile error):
//
//  1. No os.Getwd() in the CLI or daemon packages. Workspace-root discovery
//     belongs to workspaceconfig.Resolve; a command re-rolling CWD discovery is
//     exactly the divergence this work removed.
//  2. Every command config carrying a workspace field — and every relocated
//     operations-layer Config — sets that field where production code builds it.
//     Production must pass the context-resolved workspace. A caller that leaves
//     the field unset reaches workspaceOrResolve with a nil, which refuses; what
//     this check adds is the site — it names the offending literal at parse time,
//     rather than leaving a command to fail at run time with nothing to point at.
//  3. No production call hands a literal nil to a helper that threads its
//     workspace parameter into workspaceOrResolve. Checking only the
//     workspaceOrResolve call itself misses this: the commands that take the
//     workspace as a parameter pass a named value at that call, so a nil
//     introduced one hop earlier is invisible there.
//
// 🔴 Both covered sets are DERIVED from the parsed source, never listed here. A
// hand-written list of config type names is a second copy of the type
// declarations, and the copy is what goes stale: a new command config with a
// workspace field would be silently uncovered on the day it is added, which is
// precisely when the check is worth having. The config set is every struct type
// whose name ends in "Config" and which declares a `ws` field; the helper set is
// every function that passes one of its own parameters into workspaceOrResolve,
// closed over functions forwarding a parameter into one of those.
//
// Test files are exempt: a nil workspace refuses at run time, so a test that
// fails to declare one already fails on its own, and there is nothing for a
// parse-time check to add.
// opsLayerConfigPackages names the operations layers whose Config carries the
// resolved workspace, so a production literal of one is held to the same
// requirement a same-package command config is.
var opsLayerConfigPackages = map[string]bool{
	"lintops":  true,
	"buildops": true,
}

func TestWorkspaceResolutionChokepointGuard(t *testing.T) {
	dirs := []string{".", filepath.Join("..", "..", "internal", "serve")}

	// Pre-pass over the CLI package: workspaceOrResolve and the command config
	// types are unexported members of it, so nothing outside it can name them.
	cliFset := token.NewFileSet()
	cliFiles := map[string]*ast.File{}
	matches, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("glob .: %v", err)
	}
	for _, file := range matches {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		node, perr := parser.ParseFile(cliFset, file, nil, 0)
		if perr != nil {
			t.Fatalf("parse %s: %v", file, perr)
		}
		cliFiles[file] = node
	}

	cfgTypes := workspaceBearingConfigTypes(cliFiles)
	if len(cfgTypes) == 0 {
		t.Fatal("derived no command config type carrying a ws field — the derivation is broken, not the source")
	}
	threaded := workspaceThreadingHelpers(cliFiles)
	if len(threaded) == 0 {
		t.Fatal("derived no helper threading a workspace parameter into workspaceOrResolve — the derivation is broken, not the source")
	}

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
					id, ok := v.Fun.(*ast.Ident)
					if !ok {
						return true
					}
					// A literal nil at the chokepoint's own call site.
					if id.Name == "workspaceOrResolve" {
						if len(v.Args) == 1 {
							if arg, ok := v.Args[0].(*ast.Ident); ok && arg.Name == "nil" {
								t.Errorf("%s: workspaceOrResolve(nil) in production — pass the context-resolved workspace (workspaceFromContext); a nil is refused there", fset.Position(v.Pos()))
							}
						}
						return true
					}
					// A literal nil one hop earlier, at a helper that threads its
					// workspace parameter into the chokepoint.
					if idx, isThreaded := threaded[id.Name]; isThreaded && idx < len(v.Args) {
						if arg, ok := v.Args[idx].(*ast.Ident); ok && arg.Name == "nil" {
							t.Errorf("%s: %s(nil, …) in production — pass the context-resolved workspace (workspaceFromContext); that parameter reaches workspaceOrResolve, which refuses a nil", fset.Position(v.Pos()), id.Name)
						}
					}
				case *ast.CompositeLit:
					// The config-struct commands must thread the context-resolved
					// workspace into their config literal. The same-package configs
					// are bare identifiers carrying a `ws` field; a relocated
					// operations-layer config is a `<layer>.Config` selector
					// carrying a `Workspace` field. Either way, a production literal
					// that omits the workspace field leaves the command with no
					// workspace to resolve and no --workspace-root honoured.
					//
					// 🔴 The operations-layer package names are the one HAND-WRITTEN
					// set in this guard, and everything else here is derived for the
					// reason stated above. They are written out because the layers
					// live outside the two directories this walk parses, so their
					// declarations are not in reach to derive from. A new operations
					// layer whose Config carries a workspace is therefore uncovered
					// until its name is added here — add it in the same change that
					// introduces the layer.
					typeName, wsField := "", ""
					switch tp := v.Type.(type) {
					case *ast.Ident:
						if cfgTypes[tp.Name] {
							typeName, wsField = tp.Name, "ws"
						}
					case *ast.SelectorExpr:
						if pkg, ok := tp.X.(*ast.Ident); ok && opsLayerConfigPackages[pkg.Name] && tp.Sel.Name == "Config" {
							typeName, wsField = pkg.Name+".Config", "Workspace"
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
						t.Errorf("%s: %s literal without a %s field — production must thread the context-resolved workspace (workspaceFromContext); an absent workspace is refused at resolution", fset.Position(v.Pos()), typeName, wsField)
					}
				}
				return true
			})
		}
	}
}

// workspaceBearingConfigTypes returns the command config types that carry a
// resolved workspace, derived from their declarations. A type qualifies when its
// name ends in "Config" and it declares a field named `ws`. The suffix is what
// separates a command config, which RECEIVES the resolved workspace from its
// caller, from the lazy resolver, whose same-named field is the cache it fills
// itself and is therefore absent from its construction site by design.
func workspaceBearingConfigTypes(files map[string]*ast.File) map[string]bool {
	types := map[string]bool{}
	for _, node := range files {
		ast.Inspect(node, func(n ast.Node) bool {
			ts, ok := n.(*ast.TypeSpec)
			if !ok {
				return true
			}
			st, ok := ts.Type.(*ast.StructType)
			if !ok || !strings.HasSuffix(ts.Name.Name, "Config") {
				return true
			}
			for _, fld := range st.Fields.List {
				for _, nm := range fld.Names {
					if nm.Name == "ws" {
						types[ts.Name.Name] = true
					}
				}
			}
			return true
		})
	}
	return types
}

// workspaceThreadingHelpers returns every function that can carry a caller's nil
// through to workspaceOrResolve, mapped to the index of the parameter that gets
// there. A function qualifies directly when it passes one of its own parameters
// into workspaceOrResolve, and transitively when it forwards one of its own
// parameters into that position of a function already known to qualify. The
// transitive closure is what makes the check survive a new wrapper: the dispatch
// helpers are layered, so a caller is frequently two hops from the chokepoint
// rather than one.
func workspaceThreadingHelpers(files map[string]*ast.File) map[string]int {
	threaded := map[string]int{}
	// Iterate to a fixed point rather than once: a forwarder may be declared in
	// a file parsed before the function it forwards to.
	for {
		grew := false
		for _, node := range files {
			for _, d := range node.Decls {
				fd, ok := d.(*ast.FuncDecl)
				if !ok || fd.Body == nil {
					continue
				}
				params := parameterIndex(fd)
				ast.Inspect(fd.Body, func(n ast.Node) bool {
					ce, ok := n.(*ast.CallExpr)
					if !ok {
						return true
					}
					id, ok := ce.Fun.(*ast.Ident)
					if !ok {
						return true
					}
					target := -1
					switch {
					case id.Name == "workspaceOrResolve" && len(ce.Args) == 1:
						target = 0
					default:
						if idx, isThreaded := threaded[id.Name]; isThreaded && idx < len(ce.Args) {
							target = idx
						}
					}
					if target < 0 {
						return true
					}
					arg, ok := ce.Args[target].(*ast.Ident)
					if !ok {
						return true
					}
					if idx, isParam := params[arg.Name]; isParam {
						if prev, seen := threaded[fd.Name.Name]; !seen || prev != idx {
							threaded[fd.Name.Name] = idx
							grew = true
						}
					}
					return true
				})
			}
		}
		if !grew {
			return threaded
		}
	}
}

// parameterIndex maps each named parameter of fd to its position in the flattened
// parameter list, so a call's argument at that position can be checked against
// the parameter the callee threads onward.
func parameterIndex(fd *ast.FuncDecl) map[string]int {
	params := map[string]int{}
	if fd.Type.Params == nil {
		return params
	}
	i := 0
	for _, fl := range fd.Type.Params.List {
		if len(fl.Names) == 0 {
			i++
			continue
		}
		for _, nm := range fl.Names {
			params[nm.Name] = i
			i++
		}
	}
	return params
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
			"pkg/egress":                 true,
			"pkg/sourceresolver/gitrepo": true,
		},
		"github.com/sigstore/sigstore-go/pkg/sign": {
			"pkg/egress":  true,
			"pkg/signing": true,
			// The signing package's own test-support package builds keyed
			// envelope fixtures with an ephemeral key and no certificate
			// provider, timestamp authority or transparency log, so the library
			// signs in memory and reaches no network. It is a non-test file only
			// because several packages' tests share it.
			"pkg/signing/signingtest": true,
		},
		"github.com/sigstore/sigstore-go/pkg/tuf": {
			"pkg/egress/livetrust": true,
		},
		"helm.sh/helm/v4/pkg/getter": {
			"pkg/egress":              true,
			"pkg/sourceresolver/helm": true,
		},
	}
	// seen records each banned path some package imports. A banned path nothing
	// imports guards nothing — a library moved to a new major version keeps its
	// network package under another import path — so the guard fails on it rather
	// than passing over an import it can no longer see.
	seen := make(map[string]bool, len(banned))

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
			seen[path] = true
			if !allowed[pkgDir] {
				t.Errorf("%s: package %q imports the library-owned network package %q outside its egress-gated home — route the transport through pkg/egress or the library's home package", fset.Position(imp.Pos()), pkgDir, path)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	for path := range banned {
		if !seen[path] {
			t.Errorf("no package imports the banned network package %q, so this guard no longer sees that library's egress — key it to the import path its home package uses now", path)
		}
	}
}

// TestWriteDestinationChokepointGuard enforces the single write-target site
// structurally. A config-mutating command resolves the file it writes from the
// workspace resolution it already holds — rw.WriteDestination — so the write
// lands in the file that run READ its configuration from. The package-level
// workspaceconfig.WriteDestination takes the root and the environment reader as
// parameters, so a caller can hand it a reader the resolution never used, or nil,
// which reads the live process environment; the destination then resolves against
// the machine's ambient configuration while the command reports success naming a
// file the operator never asked for. Its home is pkg/workspaceconfig, where the
// method delegates to it and its policy is unit-tested directly.
//
// The guard is deny-by-default over a whole-module walk, exempting only that home,
// so a package that does not exist today is covered the moment it is added: there
// is no offender list to maintain. It names the offending site at parse time,
// which a compile error could not do — the call type-checks, and its wrongness is
// which environment it consults.
//
// GlobalConfigPath is deliberately NOT covered, and that is a scope decision
// rather than an oversight: it is the read primitive beneath this policy, its
// callers are display surfaces rather than writers, and a write cannot reach a
// file through it without also deciding the operation's nature — which is the
// decision WriteDestination owns and this guard fences.
//
// Test files are exempt: a test that resolves a destination against a frozen
// environment is exercising the policy, not bypassing it.
func TestWriteDestinationChokepointGuard(t *testing.T) {
	root := filepath.Join("..", "..")
	homeDir := filepath.Join(root, "pkg", "workspaceconfig")

	visited := 0
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			base := d.Name()
			if p == homeDir || base == "vendor" || base == "testdata" ||
				(p != root && strings.HasPrefix(base, ".")) {
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
			return nil
		}
		visited++
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
			if !ok || pkgID.Name != "workspaceconfig" {
				return true
			}
			if sel.Sel.Name == "WriteDestination" {
				t.Errorf("%s: package-level workspaceconfig.WriteDestination in production — resolve the destination off the workspace resolution (rw.WriteDestination), so the write lands in the file this run read its configuration from", fset.Position(call.Pos()))
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	// The walk's own reach is asserted rather than assumed. A negative-by-default
	// guard whose walk visited nothing passes exactly as a clean tree does, so a
	// relocated module root or a mistaken skip would retire this check in silence.
	if visited == 0 {
		t.Fatalf("the walk from %s visited no production Go file — the guard is inert, not the tree clean", root)
	}
}

// TestResolveCatalogDirsChokepointGuard enforces the single home for the
// catalog-dir precedence stack structurally. The whole assembly — flag dirs
// prepended to the configured list, the workspace's conventional catalog
// directory appended last, the authored empty list read as the off-switch —
// lives once, as (*workspaceconfig.ResolvedWorkspace).ResolveCatalogDirs; a
// command or the daemon re-rolling a local resolveCatalogDirs would reintroduce
// the copy this consolidation removed, where the two drift silently the moment
// one is touched. The guard fails if a func resolveCatalogDirs declaration
// reappears in the CLI or MCP packages.
//
// The compiler cannot catch this and never will: a package-level function
// nobody calls compiles cleanly, so a re-rolled copy type-checks and a build
// stays green. This guard is the only thing standing between the re-roll and
// the tree, which is why its own reach is asserted below rather than assumed.
//
// Test files are exempt: this guard file names the symbol in prose, and the
// unit test for the mechanic lives with its home in pkg/workspaceconfig.
func TestResolveCatalogDirsChokepointGuard(t *testing.T) {
	dirs := []string{".", filepath.Join("..", "..", "internal", "mcp")}

	inspected := 0
	for _, dir := range dirs {
		matches, err := filepath.Glob(filepath.Join(dir, "*.go"))
		if err != nil {
			t.Fatalf("glob %s: %v", dir, err)
		}
		for _, file := range matches {
			if strings.HasSuffix(file, "_test.go") {
				continue
			}
			inspected++
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
					t.Errorf("%s: func resolveCatalogDirs re-rolled outside its home — route catalog-dir assembly through the resolved workspace's ResolveCatalogDirs method", fset.Position(fn.Pos()))
				}
				return true
			})
		}
	}
	// A negative-by-default guard whose walk visited nothing passes exactly as a
	// clean tree does, so a relocated package or a mistaken skip would retire this
	// check in silence. Both directories hold production files, so a zero here is
	// the guard reporting on itself.
	if inspected == 0 {
		t.Fatalf("the walk over %v inspected no production Go file — the guard is inert, not the tree clean", dirs)
	}
}

// deniedRootContextConstructors are the context-package calls that hand back a
// context carrying no caller cancellation: the two root constructors, and the
// one that strips cancellation off a context that had it. WithoutCancel is
// denied alongside the roots because a guard covering only the roots would leave
// the identical defect reachable by a second spelling.
var deniedRootContextConstructors = map[string]bool{
	"Background":    true,
	"TODO":          true,
	"WithoutCancel": true,
}

// rootContextSite is one denied construction with the verdict the derived
// predicates reached for it.
type rootContextSite struct {
	pos    token.Position
	call   string
	exempt bool
}

// rootContextPackage accumulates one package directory's sites plus the single
// package-scoped fact the entry-point leniency turns on.
type rootContextPackage struct {
	name    string
	hasRoot bool
	sites   []rootContextSite
}

// TestCancellationRootChokepointGuard enforces structurally that a cancellation
// signal can reach the work. Go offers four ways for a function to hold a
// context — a parameter, a struct field, a package-level variable, or a call
// that manufactures one — and only the last discards the caller's cancellation:
// a function that manufactures a fresh root instead of declaring a parameter is
// unreachable by the stop its caller is holding. All four are syntactic, which
// is what makes the class decidable at parse time.
//
// The guard is deny-by-default over a whole-module walk: it visits every
// production Go file, flags every context.Background, context.TODO and
// context.WithoutCancel call, and exempts only what four DERIVED predicates
// admit. There is no offender list, so a package that does not exist today is
// covered the moment it is added, and a function written tomorrow that
// manufactures a root instead of taking a parameter trips this guard with zero
// edits here.
//
// The four predicates, none of which names a path:
//
//   - The sibling forwarder. The enclosing function F passes the manufactured
//     context to a callee named FContext. That pair is the deliberate
//     cancellation-free overload of a cancellable sibling: the cancellable path
//     exists, is named, and a caller that holds a context reaches it. The name
//     relationship is what makes this a derivation rather than a list — a
//     function forwarding a fresh root to some OTHER Context-suffixed callee is
//     not exempt. The names are compared and the receiver is not, so a forward to
//     a different type's identically-named sibling is admitted as well.
//
//   - The cancellable root. The manufactured context is bound alongside a second
//     value that the same function invokes — ctx, stop := …(context.Background())
//     followed by defer stop(). The creator holds the cancellation, so nothing is
//     discarded. 🔴 The far commoner result, err := Work(context.Background())
//     shape does NOT qualify, and that is the whole tightening: an error is never
//     invoked, and neither is a blank, which is exactly what separates a cancel
//     handle from an ordinary second return value.
//
//   - The entry-point leniency, and it is the weakest of the four. A main package
//     that declares no cancellable root of its own is a program with no
//     cancellation story at all: nothing sits above a process entry point, so a
//     bare root there discards nothing. The leniency ENDS the moment such a
//     package declares a cancellable root — from then on a second, bare root in it
//     routes around the signal the package itself holds, which is the defect. That
//     is what keeps the command layer held strictly instead of exempted wholesale
//     for being package main, and the strict-main assertion below fails loudly if
//     that root ever disappears and the leniency silently widens to cover it.
//
//   - The test-support leniency. A package whose name ends in "test" has the
//     standing every guard in this file already gives a _test.go file: test
//     support is deliberately independent, and a test that fails to cancel fails
//     on its own. The naming convention is the whole discriminator, so a package
//     whose name merely ends in those four letters is exempted too.
//
// A context named in a COMMENT needs no predicate — the parse mode drops
// comments, so the walk structurally cannot see one.
//
// 🔴 WHAT THIS GUARD DOES NOT PROVE, AND THE DISTINCTION IS THE WHOLE OF IT. It
// proves a context ARRIVED. It cannot prove that cancelling it stops the work: a
// cancellable context wired to nothing — never read, never carried into a
// blocking call, never selected on — passes this guard, and whether cancelling it
// ends anything is a runtime property no parse-time walk can reach. That second
// property has no cheap general proof and is established
// per operation, by handing the operation an already-cancelled context and
// asserting it refuses with the cancellation sentinel and no side effect. A green
// here is never "cancellation works".
//
// 🔴 THREE FURTHER BOUNDS, STATED BECAUSE AN UNSTATED BOUND READS AS FULL
// COVERAGE.
//
//  1. The cancellable-root predicate reads syntax, not types. A zero-argument
//     cleanup handle bound beside a manufactured context is indistinguishable
//     from a cancel handle without type information, so that shape is exempted
//     too. Closing it needs a type-checking load, which is a different mechanism
//     from the parse-only walk every guard in this file shares.
//  2. The test-file exemption this guard inherits removes the test corpus — by
//     far the larger population — from scope without a single exemption entry.
//     That is the same trade the six guards above make, for the same reason.
//  3. A guard living in a Go test is reached by whatever test selection its caller
//     makes. A selection narrowed to the packages a change touched runs this guard
//     only when this package is among them, so a change elsewhere is covered at
//     the next unnarrowed run rather than at its own. That is a latency, not a
//     hole, and no package placement removes it: the guard lives in exactly one
//     package, and any package it lives in is equally absent from a selection that
//     excludes it.
//
// Test files are exempt: a test manufactures a root context deliberately, because
// it IS the caller, and there is nothing above it to inherit one from.
func TestCancellationRootChokepointGuard(t *testing.T) {
	root := filepath.Join("..", "..")

	pkgs := map[string]*rootContextPackage{}
	// The directory order is kept alongside the map so the reported sites are in
	// walk order rather than in map-iteration order.
	order := []string{}
	visited := 0

	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			base := d.Name()
			if base == "vendor" || base == "testdata" ||
				(p != root && strings.HasPrefix(base, ".")) {
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
			return nil
		}
		visited++
		fset := token.NewFileSet()
		node, perr := parser.ParseFile(fset, p, nil, 0)
		if perr != nil {
			t.Fatalf("parse %s: %v", p, perr)
		}
		dir := filepath.Dir(p)
		pkg, seen := pkgs[dir]
		if !seen {
			pkg = &rootContextPackage{name: node.Name.Name}
			pkgs[dir] = pkg
			order = append(order, dir)
		}
		ctxNames := contextPackageNames(t, fset, node)
		if len(ctxNames) > 0 {
			collectRootContextSites(fset, node, ctxNames, pkg)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}

	strictMains, found := 0, 0
	for _, dir := range order {
		pkg := pkgs[dir]
		found += len(pkg.sites)
		lenient := false
		switch {
		case pkg.name == "main":
			// The entry-point leniency, which a declared cancellable root ends.
			lenient = !pkg.hasRoot
			if pkg.hasRoot {
				strictMains++
			}
		case strings.HasSuffix(pkg.name, "test"):
			lenient = true
		}
		if lenient {
			continue
		}
		for _, s := range pkg.sites {
			if s.exempt {
				continue
			}
			t.Errorf("%s: %s manufactures a context no caller can cancel — declare a context.Context parameter and pass the caller's, so a stop reaches this work; a fresh root belongs at the process entry point, or beside the cancel handle that ends it, or in the cancellation-free overload of a Context-suffixed sibling", s.pos, s.call)
		}
	}

	// The walk's own reach is asserted rather than assumed. A negative-by-default
	// guard whose walk visited nothing passes exactly as a clean tree does, so a
	// relocated module root or a mistaken skip would retire this check in silence.
	if visited == 0 {
		t.Fatalf("the walk from %s visited no production Go file — the guard is inert, not the tree clean", root)
	}
	// The matcher's own reach, for the same reason one step in: a walk that visits
	// every file and recognises no construction at all also passes silently. A
	// program that uses contexts builds a root somewhere, so a zero here is the
	// matcher having stopped matching.
	if found == 0 {
		t.Fatalf("the walk from %s recognised no context-root construction anywhere — the matcher is broken, not the tree clean", root)
	}
	// The entry-point leniency's own reach. It is package-scoped and self-cancelling
	// by design, which means losing the command layer's cancellable root would widen
	// it to cover that whole package — silently, since a widened exemption reports
	// nothing. This is what makes that widening loud.
	if strictMains == 0 {
		t.Fatalf("no main package declares a cancellable root, so the entry-point leniency now covers every main package and this guard no longer reaches the command layer — restore the cancellable root at the process entry point")
	}
}

// contextPackageNames returns the local identifiers bound to the context package
// in node, so the matcher keys on what the file actually calls the package rather
// than on the literal "context". An aliased import would otherwise evade every
// selector match in this guard. A dot import is reported rather than tolerated:
// it would put the constructors in file scope as bare identifiers, which this
// matcher cannot see, and denying the evasion is the point of a deny-by-default
// walk.
func contextPackageNames(t *testing.T, fset *token.FileSet, node *ast.File) map[string]bool {
	t.Helper()
	names := map[string]bool{}
	for _, imp := range node.Imports {
		if strings.Trim(imp.Path.Value, `"`) != "context" {
			continue
		}
		if imp.Name == nil {
			names["context"] = true
			continue
		}
		switch imp.Name.Name {
		case "_":
			// A blank import binds no name, so nothing here can be called.
		case ".":
			t.Errorf("%s: dot import of context — import it under a name, so the root-context constructors stay visible to the cancellation guard", fset.Position(imp.Pos()))
		default:
			names[imp.Name.Name] = true
		}
	}
	return names
}

// collectRootContextSites records every denied construction in node against its
// package, marking the ones the two function-scoped predicates admit. A
// construction outside any function body — a package-level variable initialiser —
// has no enclosing function, so neither predicate can reach it and it is recorded
// unexempted.
func collectRootContextSites(fset *token.FileSet, node *ast.File, ctxNames map[string]bool, pkg *rootContextPackage) {
	record := func(call *ast.CallExpr, name string, exempt bool) {
		pkg.sites = append(pkg.sites, rootContextSite{
			pos:    fset.Position(call.Pos()),
			call:   name,
			exempt: exempt,
		})
	}
	for _, decl := range node.Decls {
		fd, isFunc := decl.(*ast.FuncDecl)
		if !isFunc || fd.Body == nil {
			ast.Inspect(decl, func(n ast.Node) bool {
				if call, ok := n.(*ast.CallExpr); ok {
					if name := deniedRootContextCall(call, ctxNames); name != "" {
						record(call, name, false)
					}
				}
				return true
			})
			continue
		}
		forwarded := forwardedRootContexts(fd, ctxNames)
		rooted := boundCancellableRoots(fd.Body, ctxNames, invokedNames(fd.Body))
		if len(rooted) > 0 {
			pkg.hasRoot = true
		}
		ast.Inspect(fd.Body, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			name := deniedRootContextCall(call, ctxNames)
			if name == "" {
				return true
			}
			record(call, name, forwarded[call.Pos()] || rooted[call.Pos()])
			return true
		})
	}
}

// deniedRootContextCall returns the call's source spelling when it constructs a
// context carrying no caller cancellation, and the empty string otherwise.
func deniedRootContextCall(call *ast.CallExpr, ctxNames map[string]bool) string {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return ""
	}
	id, ok := sel.X.(*ast.Ident)
	if !ok || !ctxNames[id.Name] || !deniedRootContextConstructors[sel.Sel.Name] {
		return ""
	}
	return id.Name + "." + sel.Sel.Name + "()"
}

// forwardedRootContexts returns the positions of the denied constructions fd
// hands to its own Context-suffixed sibling. The sibling's name is derived from
// fd's — a function named F may forward to FContext and to nothing else — so the
// pair is recognised by the relationship rather than by a list of known pairs.
func forwardedRootContexts(fd *ast.FuncDecl, ctxNames map[string]bool) map[token.Pos]bool {
	forwarded := map[token.Pos]bool{}
	sibling := fd.Name.Name + "Context"
	ast.Inspect(fd.Body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || calleeName(call.Fun) != sibling {
			return true
		}
		for _, arg := range call.Args {
			inner, isCall := arg.(*ast.CallExpr)
			if !isCall {
				continue
			}
			if deniedRootContextCall(inner, ctxNames) != "" {
				forwarded[inner.Pos()] = true
			}
		}
		return true
	})
	return forwarded
}

// boundCancellableRoots returns the positions of the denied constructions that
// are bound as a cancellable root: a two-value assignment whose second value the
// same function invokes. Requiring the invocation is what separates a cancel
// handle from an ordinary second return value — an error is never called, and
// neither is a blank — which a bare two-value bind would have admitted.
func boundCancellableRoots(body *ast.BlockStmt, ctxNames map[string]bool, invoked map[string]bool) map[token.Pos]bool {
	rooted := map[token.Pos]bool{}
	ast.Inspect(body, func(n ast.Node) bool {
		assign, ok := n.(*ast.AssignStmt)
		if !ok || len(assign.Lhs) != 2 || len(assign.Rhs) != 1 {
			return true
		}
		handle := exprText(assign.Lhs[1])
		if handle == "" || !invoked[handle] {
			return true
		}
		ast.Inspect(assign.Rhs[0], func(m ast.Node) bool {
			inner, isCall := m.(*ast.CallExpr)
			if isCall && deniedRootContextCall(inner, ctxNames) != "" {
				rooted[inner.Pos()] = true
			}
			return true
		})
		return true
	})
	return rooted
}

// invokedNames returns the source spelling of everything body calls, so a bound
// handle can be tested against it. A cancel handle is, by definition, something
// the holder calls.
func invokedNames(body *ast.BlockStmt) map[string]bool {
	invoked := map[string]bool{}
	ast.Inspect(body, func(n ast.Node) bool {
		if call, ok := n.(*ast.CallExpr); ok {
			if text := exprText(call.Fun); text != "" {
				invoked[text] = true
			}
		}
		return true
	})
	return invoked
}

// exprText renders an identifier or a dotted selector chain as it is written in
// the source, so a field-held cancel handle (s.serverCancel) matches its own
// invocation site as readily as a local one does.
func exprText(e ast.Expr) string {
	switch v := e.(type) {
	case *ast.Ident:
		return v.Name
	case *ast.SelectorExpr:
		x := exprText(v.X)
		if x == "" {
			return ""
		}
		return x + "." + v.Sel.Name
	}
	return ""
}

// calleeName returns the final name of a call's target — the selector's own name
// for a method call, the identifier for a plain one, looking through a generic
// instantiation.
func calleeName(e ast.Expr) string {
	switch v := e.(type) {
	case *ast.Ident:
		return v.Name
	case *ast.SelectorExpr:
		return v.Sel.Name
	case *ast.IndexExpr:
		return calleeName(v.X)
	case *ast.IndexListExpr:
		return calleeName(v.X)
	}
	return ""
}
