// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package yamlpos

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

// fieldPathCallers is every production file allowed to turn a dotted field
// path into a source position. The guard asserts an EXACT match rather than
// "at least these": a subset check would pass while another surface quietly
// acquired its own positioning, which is the whole divergence this pins.
//
// Most callers position a validation finding. The configuration-origin surface
// is the one that does not: it positions the key a setting was authored at, and
// it is here for the same reason the others are — a second walk would answer
// the same question about the same document differently.
//
// The list names DIRECT callers, which is not the same as the list of surfaces
// that position findings — and the difference is worth reading before editing an
// entry. A surface reaching the walk through a shared helper appears here as that
// helper rather than under its own name, so the daemon's render-diagnostic file
// stands for every class routing through the one fall-back it owns: the halting
// resolver violations, the static-analysis violations and the advisories alike.
// Consolidating a caller behind that helper therefore MOVES an entry rather than
// removing one, and a surface disappearing from this list has either stopped
// positioning findings or started sharing — which is a distinction to establish
// from the tree rather than infer from the diff.
//
// The two entries named in theTwoSurfaces matter most. One is the terminal
// diagnostic a developer reads; the other is the editor diagnostic for the
// same document. They were measurably unequal — the editor positioned its
// findings and the terminal did not — and they are equal now because both
// resolve the path here.
var fieldPathCallers = []string{
	"internal/mcp/tool_build.go",
	"internal/serve/closure_resolve.go",
	"internal/serve/envelope_diagnostics.go",
	"internal/serve/render_diagnostics.go",
	"pkg/validator/envelope/envelope.go",
	"pkg/workspaceconfig/origin.go",
	"pkg/yamlpos/fieldpath.go",
}

// theTwoSurfaces are the files whose disagreement was the defect. They are
// asserted by name as well as by the exact set above, so dropping one
// produces a failure that says which surface stopped sharing the walk
// rather than only that a count moved.
var theTwoSurfaces = []string{
	"pkg/validator/envelope/envelope.go",
	"internal/serve/envelope_diagnostics.go",
}

// fieldPathEntryPoints returns the names of the shared walk's entry points,
// composed from fragments so this file can census the identifiers without
// having to special-case itself. The seam is deliberate.
func fieldPathEntryPoints() map[string]bool {
	return map[string]bool{
		"Field" + "Node":  true,
		"Field" + "Range": true,
	}
}

// TestFieldPathWalkHasOneImplementation is the chokepoint guard for
// field-path position resolution. It walks every production Go file in the
// module and requires that the shared entry points are called from exactly
// the files listed above.
//
// What it catches: a new surface positioning findings on its own terms, and
// a listed surface silently dropping the shared walk. Both are the shape of
// the defect this guard exists for — the same violation reported at two
// coordinates depending on which surface a reader happened to use.
//
// What it does NOT catch, stated so the guard is not mistaken for total: a
// hand-rolled walker under some other name, which no caller-set census can
// see. A structural fingerprint for that shape (a dotted split plus a
// mapping-node descent) was measured against this module and matched eight
// unrelated files, so it would have been noise rather than a gate. The
// remaining cover for a re-roll is the parity test across the two surfaces
// and the fact that there is now nothing local left to copy from.
func TestFieldPathWalkHasOneImplementation(t *testing.T) {
	t.Parallel()

	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolve module root: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("module root %q does not carry a go.mod; the guard is looking in the wrong place: %v", root, err)
	}

	entryPoints := fieldPathEntryPoints()
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
		name := d.Name()
		// Tests exercise the walk directly and legitimately; the caller set
		// worth pinning is the production one.
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
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
			callee := calleeName(call.Fun)
			if !entryPoints[callee] {
				return true
			}
			pos := fset.Position(call.Pos())
			sites[rel] = append(sites[rel], fmt.Sprintf("%s:%d: %s(...)", rel, pos.Line, callee))
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

	want := append([]string(nil), fieldPathCallers...)
	sort.Strings(want)

	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		var detail strings.Builder
		for _, file := range got {
			for _, site := range sites[file] {
				detail.WriteString("\n    " + site)
			}
		}
		t.Fatalf("field-path position resolution must run through the shared walk, from exactly these files.\n  want: %v\n  got:  %v%s\nA new surface reporting positions calls the shared walk and joins this list; it does not acquire its own walker.",
			want, got, detail.String())
	}

	for _, surface := range theTwoSurfaces {
		if len(sites[surface]) == 0 {
			t.Errorf("%s no longer resolves field paths through the shared walk; the terminal and editor diagnostics can drift apart again", surface)
		}
	}
}

// calleeName renders a call's callee as written — "yamlpos.FieldRange" gives
// "FieldRange", a bare in-package call gives its own name. It is textual on
// purpose: the guard reads source shape, so it needs no build of the
// packages it inspects.
func calleeName(fun ast.Expr) string {
	switch f := fun.(type) {
	case *ast.Ident:
		return f.Name
	case *ast.SelectorExpr:
		return f.Sel.Name
	}
	return ""
}
