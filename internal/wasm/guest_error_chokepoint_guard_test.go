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

// nonConstructingCalls are the calls that take the sentinel without building an
// error around it, keyed by the callee's own FUNCTION name rather than by its
// package-qualified spelling.
//
// Two kinds belong here and both use the identity as intended. A CLASSIFYING
// call — Is, As — matches the taxonomy, which is what the taxonomy exists for. A
// READING call — CodeOf, ShortCodeOf, MessageOf — asks the identity what it
// reports; reading a value off a condition constructs nothing, and it is how
// every surface that prints or publishes a code obtains one.
//
// The key is the bare function name because the package spelling is not
// stable at the call site: a file may import the standard errors package under
// an alias, and this project's own error package is conventionally imported
// aliased. Keyed on the qualified spelling, such a call read as a construction
// site — measured, on files importing the standard package as stderrors — so the
// guard reported a clean tree as dirty and the next author's repair would have
// been to loosen the check itself. The bound is stated because it is real: a
// function of one of these names in some unrelated package is admitted too. That
// is accepted, because the failure it opens is a wrap named Is, As or CodeOf,
// which is a shape review sees and which no idiom produces.
//
// Registration needs no entry here. It declares the identity against a code
// value rather than against the identity's own name, so it never reads as a use
// of the sentinel in the first place — and it could not become the second copy
// this guard exists to catch in any case, because registering one code twice
// panics at start-up.
var nonConstructingCalls = map[string]bool{
	"Is":          true,
	"As":          true,
	"CodeOf":      true,
	"ShortCodeOf": true,
	"MessageOf":   true,
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
			if nonConstructingCalls[calleeName(call.Fun)] {
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

// calleeName renders a call's callee as its own FUNCTION name, dropping whatever
// package qualifier the call site wrote — "Errorf", "Is", "ShortCodeOf".
//
// It is the form nonConstructingCalls is keyed on, and it is separate from
// callName because the two answer different questions: the allowlist must be
// blind to an import alias, while a finding printed for a human wants the call as
// it was written.
func calleeName(fun ast.Expr) string {
	switch f := fun.(type) {
	case *ast.Ident:
		return f.Name
	case *ast.SelectorExpr:
		return f.Sel.Name
	}
	return ""
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
//
// The identity is reached through its accessor, so the argument a caller writes
// is a call rather than a bare name. The call is unwrapped to the name it
// invokes: reading only the bare form would find no site at all, and a guard
// that stops finding anything reports a clean tree while guarding nothing.
func referencesSentinel(arg ast.Expr, needle string) bool {
	switch a := arg.(type) {
	case *ast.Ident:
		return a.Name == needle
	case *ast.SelectorExpr:
		return a.Sel.Name == needle
	case *ast.CallExpr:
		return len(a.Args) == 0 && referencesSentinel(a.Fun, needle)
	}
	return false
}
