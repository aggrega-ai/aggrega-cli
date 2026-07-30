// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package wasm

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// sentinelHome is the one file allowed to build an error around the
// guest-error sentinel. The guard asserts an EXACT match against this entry
// rather than "at least this one": a subset check would pass while a second
// surface quietly spelled its own wrap, which is the whole failure the guard
// exists to catch.
const sentinelHome = "internal/wasm/errors.go"

// sentinelName is composed from fragments so this file can search for the
// identifier without the census having to special-case the guard itself. The
// seam is deliberate.
func sentinelName() string { return "ErrWasm" + "GuestError" }

// readOnlyCalls are the calls that merely CLASSIFY an error against the
// sentinel. They are legitimate on any surface — the taxonomy exists to be
// matched — so the guard admits them everywhere and judges everything else.
var readOnlyCalls = map[string]bool{
	"errors.Is": true,
	"errors.As": true,
}

// TestGuestErrorSentinelHasOneWrapSite is the chokepoint guard for the
// guest-returned-error check. It walks every Go file in the module and requires
// that the guest-error sentinel is built into an error in exactly one file —
// the primitive's own home — while leaving classification against it free
// anywhere.
//
// The distinction is the point. A caller asking "is this a guest error?" is
// using the taxonomy as intended. A caller CONSTRUCTING the wrapped error
// itself has written a second copy of the primitive, and the two agreeing on
// the day the copy is written is exactly what hides the divergence later: one
// path would report a guest failure the other swallows, or the two would name
// the failing export differently.
//
// The check is exact on both axes. An extra file means a surface hand-rolled
// the wrap instead of calling GuestReturnedError. A missing or renamed home
// means the single home moved without the guard following it, which is equally
// a failure — a guard that silently stops guarding is worse than no guard.
func TestGuestErrorSentinelHasOneWrapSite(t *testing.T) {
	t.Parallel()

	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolve module root: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("module root %q does not carry a go.mod; the guard is looking in the wrong place: %v", root, err)
	}

	needle := sentinelName()
	sites := map[string][]string{}

	walkErr := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "testdata", "vendor":
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(d.Name(), ".go") {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)

		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if err != nil {
			return fmt.Errorf("parse %s: %w", rel, err)
		}

		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			if readOnlyCalls[callName(call.Fun)] {
				return true
			}
			for _, arg := range call.Args {
				if !referencesSentinel(arg, needle) {
					continue
				}
				pos := fset.Position(arg.Pos())
				sites[rel] = append(sites[rel],
					fmt.Sprintf("%s:%d: %s(...)", rel, pos.Line, callName(call.Fun)))
			}
			return true
		})
		return nil
	})
	if walkErr != nil {
		t.Fatalf("walk module: %v", walkErr)
	}

	got := make([]string, 0, len(sites))
	for file := range sites {
		got = append(got, file)
	}
	sort.Strings(got)

	want := []string{sentinelHome}
	if len(got) != len(want) || got[0] != want[0] {
		var detail strings.Builder
		for _, file := range got {
			for _, site := range sites[file] {
				detail.WriteString("\n    " + site)
			}
		}
		t.Fatalf("the guest-error sentinel must be built into an error in exactly one file.\n  want: %v\n  got:  %v%s\nEvery other surface reaches it through GuestReturnedError; classifying with errors.Is stays free.",
			want, got, detail.String())
	}
	if n := len(sites[sentinelHome]); n != 1 {
		t.Errorf("%s must build the sentinel into an error exactly once; got %d site(s): %v", sentinelHome, n, sites[sentinelHome])
	}
}

// callName renders a call's callee as written — "fmt.Errorf", "errors.Is", or a
// bare function name. It is textual on purpose: the guard reads source shape,
// not resolved types, so it needs no build of the package under inspection.
func callName(fun ast.Expr) string {
	switch f := fun.(type) {
	case *ast.Ident:
		return f.Name
	case *ast.SelectorExpr:
		if x, ok := f.X.(*ast.Ident); ok {
			return x.Name + "." + f.Sel.Name
		}
		return f.Sel.Name
	}
	return ""
}

// referencesSentinel reports whether an argument names the sentinel, either
// bare within its own package or qualified from outside it.
func referencesSentinel(arg ast.Expr, needle string) bool {
	switch a := arg.(type) {
	case *ast.Ident:
		return a.Name == needle
	case *ast.SelectorExpr:
		return a.Sel.Name == needle
	}
	return false
}
