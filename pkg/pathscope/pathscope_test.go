// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package pathscope_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/pathscope"
)

// TestResolveRefusesEmptyRoot is the whole reason Resolve takes a root at all
// rather than being a bare predicate over the relative path.
//
// filepath.Join("", "x") is "x", so a caller that joined against an empty root
// would silently address the process working directory — a different directory
// than it named, and generally not one it may write to. The refusal is asserted
// against Resolve itself rather than through any caller: every caller in the
// tree happens to reject an empty root earlier today, which would make a test
// routed through one of them pass whatever Resolve did here.
func TestResolveRefusesEmptyRoot(t *testing.T) {
	for _, rel := range []string{"x", "a/b", ".", "", "../up"} {
		got, ok := pathscope.Resolve("", rel)
		if ok {
			t.Errorf("Resolve(%q, %q) accepted; an empty root must be refused, not joined against the working directory", "", rel)
		}
		if got != "" {
			t.Errorf("Resolve(%q, %q) returned path %q; a refusal returns the empty path", "", rel, got)
		}
	}

	// The same relative path against a named root is accepted, so the refusal
	// above is attributable to the empty root and not to the path.
	if _, ok := pathscope.Resolve("/srv/app", "x"); !ok {
		t.Fatal(`Resolve("/srv/app", "x") was refused; the empty-root cases above prove nothing if a named root refuses too`)
	}
}

func TestResolve(t *testing.T) {
	const root = "/srv/app"
	tests := []struct {
		name string
		root string
		rel  string
		want string // empty means refusal
	}{
		{"plain name", root, "config.yaml", "/srv/app/config.yaml"},
		{"nested", root, "a/b/c.yaml", "/srv/app/a/b/c.yaml"},
		{"dot names the root", root, ".", "/srv/app"},
		{"interior climb resolves inside", root, "a/../b", "/srv/app/b"},
		{"trailing climb resolves to the root", root, "a/..", "/srv/app"},
		{"leading two dots then a letter is an ordinary name", root, "..config", "/srv/app/..config"},
		{"two dots then a letter, nested", root, "..cache/x", "/srv/app/..cache/x"},
		{"four dots is an ordinary name", root, "....", "/srv/app/...."},
		{"bare climb", root, "..", ""},
		{"climb out", root, "../elsewhere", ""},
		{"climb out after descending", root, "a/../../elsewhere", ""},
		{"climb out and back to the same root", root, "../app/ok", ""},
		{"absolute rel", root, "/etc/passwd", ""},
		{"empty rel", root, "", ""},
		{"empty root", "", "x", ""},
		{"unclean root is cleaned", "/srv/app/", "x", "/srv/app/x"},
		{"relative root", "dist", "a.yaml", "dist/a.yaml"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := pathscope.Resolve(tc.root, tc.rel)
			if tc.want == "" {
				if ok {
					t.Fatalf("Resolve(%q, %q) = %q, accepted; want refusal", tc.root, tc.rel, got)
				}
				return
			}
			if !ok {
				t.Fatalf("Resolve(%q, %q) refused; want %q", tc.root, tc.rel, tc.want)
			}
			if got != filepath.FromSlash(tc.want) {
				t.Fatalf("Resolve(%q, %q) = %q; want %q", tc.root, tc.rel, got, tc.want)
			}
		})
	}
}

// TestResolveRefusesPrefixSibling is the case a bare string-prefix comparison
// against the root gets wrong: "/srv/app-old" starts with "/srv/app" as a
// string but is a sibling directory, not a descendant.
func TestResolveRefusesPrefixSibling(t *testing.T) {
	if got, ok := pathscope.Resolve("/srv/app", "../app-old/secret"); ok {
		t.Fatalf("Resolve accepted %q, landing at %q; a sibling sharing the root's name prefix is outside the root", "../app-old/secret", got)
	}
}

func TestIsLocalSlash(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want bool
	}{
		{"plain name", "config.yaml", true},
		{"nested", "a/b/c.yaml", true},
		{"dot", ".", true},
		{"dot slash", "./a", true},
		{"interior climb", "a/../b", true},
		{"trailing climb to the root", "a/..", true},
		{"leading two dots then a letter", "..config", true},
		{"two dots then a letter, nested", "..cache/x", true},
		{"two dots inside a segment", "a/b..c/d", true},
		{"four dots", "....", true},
		{"double separator", "a//b", true},
		{"backslash is an ordinary character", `a\b`, true},
		{"empty", "", false},
		{"bare climb", "..", false},
		{"climb out", "../x", false},
		{"climb out after descending", "a/../../x", false},
		{"absolute", "/etc/passwd", false},
		{"absolute after cleaning", "//etc", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := pathscope.IsLocalSlash(tc.in); got != tc.want {
				t.Fatalf("IsLocalSlash(%q) = %v; want %v", tc.in, got, tc.want)
			}
		})
	}
}

// TestIsLocalSlashIsPlatformInvariant pins the property that makes the logical
// algebra a separate entry point rather than a flag on the operating-system one.
//
// A backslash is an ordinary character in a logical name and a separator in a
// Windows filesystem path. A name judged with filesystem arithmetic would
// therefore be refused on Windows and accepted on Linux, so the same rendered
// output or archive would be accepted on one machine and refused on the next.
// IsLocalSlash reads it as an ordinary character on every platform.
func TestIsLocalSlashIsPlatformInvariant(t *testing.T) {
	for _, in := range []string{`a\b`, `..\..\etc`, `a\..\..\b`} {
		if !pathscope.IsLocalSlash(in) {
			t.Errorf("IsLocalSlash(%q) refused; a backslash is an ordinary character in a slash-separated name on every platform", in)
		}
	}
}

// TestIsLocalSlashImpliesContainedJoin asserts the property the archive
// extractors rely on to skip a second check after joining: an accepted name
// cannot join to a location outside the root it is joined onto.
func TestIsLocalSlashImpliesContainedJoin(t *testing.T) {
	const root = "/srv/dest"
	names := []string{
		"a.txt", "a/b/c.txt", "..config", "a/../b", "....", "./x", "a//b", "a/..",
	}
	for _, n := range names {
		if !pathscope.IsLocalSlash(n) {
			t.Fatalf("IsLocalSlash(%q) = false; the accepted set this test reasons about no longer holds", n)
		}
		joined := filepath.Clean(filepath.Join(root, filepath.FromSlash(n)))
		if joined != root && !strings.HasPrefix(joined, root+string(filepath.Separator)) {
			t.Errorf("IsLocalSlash(%q) accepted but the join landed at %q, outside %q", n, joined, root)
		}
	}
}

// TestNoExportedSymbolTakesAMode is the structural half of keeping the two
// algebras apart. Documentation saying "use the right one" is advice; a package
// whose exported surface offers no way to express the wrong one is a guarantee.
//
// It parses the package's own source and asserts that no exported function
// carries a parameter a caller could use to switch algebras — a bool, or a named
// type whose name reads like a mode — and that the shared primitive is not
// exported at all, so a caller cannot reach past the two entry points to a
// function that does not know which arithmetic applies.
func TestNoExportedSymbolTakesAMode(t *testing.T) {
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, ".", func(fi fs.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, 0)
	if err != nil {
		t.Fatalf("parse package source: %v", err)
	}
	pkg, ok := pkgs["pathscope"]
	if !ok {
		t.Fatalf("package pathscope not found in parsed set %v; this test reads the wrong directory", keys(pkgs))
	}

	exported := 0
	for _, file := range pkg.Files {
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || !fn.Name.IsExported() {
				continue
			}
			exported++
			for _, param := range fn.Type.Params.List {
				typeName := exprName(param.Type)
				if typeName == "bool" {
					t.Errorf("exported func %s takes a bool parameter; a boolean parameter is how one entry point comes to serve two algebras", fn.Name.Name)
				}
				for _, n := range param.Names {
					if looksLikeMode(n.Name) || looksLikeMode(typeName) {
						t.Errorf("exported func %s takes parameter %s %s, which reads as a mode selector", fn.Name.Name, n.Name, typeName)
					}
				}
			}
		}
	}
	if exported != 2 {
		t.Errorf("package exports %d functions; the two algebras are meant to be exactly two entry points, so a third is either a mode in disguise or an algebra nobody named", exported)
	}
}

func keys(m map[string]*ast.Package) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func exprName(e ast.Expr) string {
	if id, ok := e.(*ast.Ident); ok {
		return id.Name
	}
	return ""
}

func looksLikeMode(s string) bool {
	l := strings.ToLower(s)
	for _, w := range []string{"mode", "kind", "flag", "algebra", "variant", "style", "os", "slash"} {
		if l == w {
			return true
		}
	}
	return false
}
