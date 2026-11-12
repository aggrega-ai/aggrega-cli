// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package oci_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

const (
	orasAuthImport   = "oras.land/oras-go/v2/registry/remote/auth"
	orasRemoteImport = "oras.land/oras-go/v2/registry/remote"
	// targetHome is the one file allowed to build an oras registry target, relative
	// to the module root.
	targetHome = "pkg/oci/client.go"
)

// TestEveryRegistryClientGoesThroughTheBusyRegistryHandling reads every non-test
// Go source file of the module and refuses a registry client built around the
// network client directly rather than through oci.RegistryClient, the one place
// that decides how a request reacts to a registry asking it to slow down:
//
//   - every oras authenticating client (auth.Client) literal sets its Client field
//     to a call of RegistryClient — the authenticating client is what every oras
//     registry target sends its requests through;
//   - no oras registry target is built anywhere but the one home that wraps it
//     (pkg/oci/client.go): no remote.NewRepository or remote.NewRegistry call, and
//     no remote.Repository or remote.Registry literal, elsewhere.
//
// Planting the bypass — `Client: httpClient,` in place of
// `Client: RegistryClient(httpClient),` in remoteClient's auth.Client literal, or a
// remote.NewRepository call in any other file — fails it, naming the file and line.
// The one registry request made outside a target, the read of a registry's
// advertised trust policy, is held by its own test in the registry operations
// package.
func TestEveryRegistryClientGoesThroughTheBusyRegistryHandling(t *testing.T) {
	t.Parallel()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	authLiterals := 0
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, werr error) error {
		if werr != nil {
			return werr
		}
		if d.IsDir() {
			switch d.Name() {
			case "testdata", "vendor", "node_modules", ".git":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		file, perr := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if perr != nil {
			return perr
		}
		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)
		authName, remoteName := importNames(file)
		if authName == "" && remoteName == "" {
			return nil
		}
		ast.Inspect(file, func(n ast.Node) bool {
			switch n := n.(type) {
			case *ast.CompositeLit:
				switch {
				case isSelector(n.Type, authName, "Client"):
					authLiterals++
					if !clientFieldWrapped(n) {
						t.Errorf("%s: an oras authenticating client is built around a client not wrapped by RegistryClient, so its registry requests bypass the busy-registry handling",
							fset.Position(n.Pos()))
					}
				case isSelector(n.Type, remoteName, "Repository"), isSelector(n.Type, remoteName, "Registry"):
					t.Errorf("%s: an oras registry target is built as a literal; build it in %s, which wraps its client", fset.Position(n.Pos()), targetHome)
				}
			case *ast.CallExpr:
				if (isSelector(n.Fun, remoteName, "NewRepository") || isSelector(n.Fun, remoteName, "NewRegistry")) && rel != targetHome {
					t.Errorf("%s: an oras registry target is built outside %s, which wraps its client", fset.Position(n.Pos()), targetHome)
				}
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatalf("walk the module: %v", err)
	}
	// The guard read nothing if it found no authenticating client at all: the
	// module builds at least the one in remoteClient.
	if authLiterals == 0 {
		t.Fatal("no oras authenticating client literal was found, so the scan read nothing it guards")
	}
}

// importNames returns the names file refers to the oras auth and remote packages
// by, or "" for one it does not import.
func importNames(file *ast.File) (authName, remoteName string) {
	for _, spec := range file.Imports {
		path, err := strconv.Unquote(spec.Path.Value)
		if err != nil {
			continue
		}
		name := ""
		switch path {
		case orasAuthImport:
			name = "auth"
		case orasRemoteImport:
			name = "remote"
		default:
			continue
		}
		if spec.Name != nil {
			name = spec.Name.Name
		}
		if path == orasAuthImport {
			authName = name
		} else {
			remoteName = name
		}
	}
	return authName, remoteName
}

// isSelector reports whether expr is pkg.name, for a pkg the file imports.
func isSelector(expr ast.Expr, pkg, name string) bool {
	if pkg == "" {
		return false
	}
	sel, ok := expr.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != name {
		return false
	}
	id, ok := sel.X.(*ast.Ident)
	return ok && id.Name == pkg
}

// clientFieldWrapped reports whether an auth.Client literal sets its Client field
// to a call of RegistryClient, written bare inside the oci package or as
// oci.RegistryClient outside it.
func clientFieldWrapped(lit *ast.CompositeLit) bool {
	for _, elt := range lit.Elts {
		kv, ok := elt.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		key, ok := kv.Key.(*ast.Ident)
		if !ok || key.Name != "Client" {
			continue
		}
		call, ok := kv.Value.(*ast.CallExpr)
		if !ok {
			return false
		}
		switch fn := call.Fun.(type) {
		case *ast.Ident:
			return fn.Name == "RegistryClient"
		case *ast.SelectorExpr:
			return fn.Sel.Name == "RegistryClient"
		}
		return false
	}
	// An authenticating client with no Client of its own sends its requests
	// through the library's default client, which bypasses the handling too.
	return false
}
