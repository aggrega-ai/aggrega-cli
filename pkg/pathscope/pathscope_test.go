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

// TestRelative pins the name a path has relative to a root, which is the form a
// caller reports rather than the form it opens.
//
// Both input spellings are in the table: an absolute path is decomposed against
// the root, and one already relative to the root is taken as it stands. They are
// one entry point because a caller holding a recorded provenance string does not
// know which spelling it holds, and anchoring at the call site is the step that
// gets forgotten.
func TestRelative(t *testing.T) {
	const root = "/srv/app"
	tests := []struct {
		name string
		root string
		path string
		want string // empty means refusal
	}{
		// An absolute path inside the root, decomposed.
		{"absolute inside", root, "/srv/app/config.yaml", "config.yaml"},
		{"absolute nested", root, "/srv/app/a/b/c.yaml", "a/b/c.yaml"},
		{"absolute unclean", root, "/srv/app/a/../b.yaml", "b.yaml"},
		{"unclean root", "/srv/app/", "/srv/app/x.yaml", "x.yaml"},

		// A path already relative to the root, taken as it stands. This is the
		// form a catalog records an authored document's location in.
		{"relative inside", root, "catalog/base/x.yaml", "catalog/base/x.yaml"},
		{"relative unclean", root, "catalog/../x.yaml", "x.yaml"},
		{"relative two dots then a letter is an ordinary name", root, "..config/x.yaml", "..config/x.yaml"},

		// Outside the root: no name relative to it that a reader could use.
		{"relative climb", root, "../elsewhere/catalog/x.yaml", ""},
		{"relative bare climb", root, "..", ""},
		{"absolute outside", root, "/etc/passwd", ""},
		{"absolute prefix sibling", root, "/srv/app-old/secret.yaml", ""},
		{"relative prefix sibling", root, "../app-old/secret.yaml", ""},

		// The root itself is not a name inside itself. A caller reporting "." for
		// it would answer with a directory where its reader expects a file.
		{"absolute root itself", root, "/srv/app", ""},
		{"relative root itself", root, ".", ""},
		{"relative resolving to the root", root, "a/..", ""},

		// Neither an empty root nor an empty path names anything, and joining
		// against an empty root would address the process working directory.
		{"empty root", "", "/srv/app/x.yaml", ""},
		{"empty path", root, "", ""},
		{"both empty", "", "", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := pathscope.Relative(tc.root, filepath.FromSlash(tc.path))
			if tc.want == "" {
				if ok {
					t.Fatalf("Relative(%q, %q) = %q, accepted; want refusal", tc.root, tc.path, got)
				}
				if got != "" {
					t.Fatalf("Relative(%q, %q) refused but returned %q; a refusal returns the empty name",
						tc.root, tc.path, got)
				}
				return
			}
			if !ok {
				t.Fatalf("Relative(%q, %q) refused; want %q", tc.root, tc.path, tc.want)
			}
			if got != tc.want {
				t.Fatalf("Relative(%q, %q) = %q; want %q", tc.root, tc.path, got, tc.want)
			}
		})
	}
}

// TestRelativeAndResolveAgreeOnContainment is the property that lets one of these
// answer a caller about to read a file and the other a caller about to report a
// name: they must not disagree about which paths are inside the root at all.
//
// A disagreement would mean a path the product opens but will not name, or names
// but will not open — and either is a state no caller could explain to its reader.
func TestRelativeAndResolveAgreeOnContainment(t *testing.T) {
	const root = "/srv/app"
	rels := []string{
		"config.yaml", "a/b/c.yaml", "a/../b", "..config", "..cache/x",
		"..", "../elsewhere", "a/../../elsewhere", "../app/ok", "../app-old/secret",
		"/etc/passwd", "",
	}
	for _, rel := range rels {
		// The root itself is deliberately excluded: Resolve accepts "." because
		// the root is a directory it can open, and Relative refuses it because the
		// root has no name inside itself. That difference is a property of what
		// each answers, not a disagreement about containment, and it is asserted
		// as its own row in the table above.
		if rel == "." || rel == "a/.." {
			continue
		}
		_, resolveOK := pathscope.Resolve(root, rel)
		_, relativeOK := pathscope.Relative(root, rel)
		if resolveOK != relativeOK {
			t.Errorf("Resolve(%q, %q) accepted=%v but Relative accepted=%v; the two disagree about "+
				"whether that path is inside the root", root, rel, resolveOK, relativeOK)
		}
	}
}

func TestResolveSelected(t *testing.T) {
	const root = "/srv/app"
	tests := []struct {
		name string
		root string
		rel  string
		want string // empty means refusal
	}{
		// Inside the root, a selection resolves exactly as a workspace-scoped
		// path does. The two classes differ in what they REFUSE, not in where
		// they land, so a selection naming a file in the tree must not arrive
		// somewhere else.
		{"plain name", root, "config.yaml", "/srv/app/config.yaml"},
		{"nested", root, "a/b/c.yaml", "/srv/app/a/b/c.yaml"},
		{"dot names the root", root, ".", "/srv/app"},
		{"interior climb resolves inside", root, "a/../b", "/srv/app/b"},
		{"leading two dots then a letter is an ordinary name", root, "..config", "/srv/app/..config"},
		{"unclean root is cleaned", "/srv/app/", "x", "/srv/app/x"},
		{"relative root", "dist", "a.yaml", "dist/a.yaml"},

		// The two acceptances that separate this entry point from Resolve.
		{"absolute selection", root, "/home/dev/build/plugin.wasm", "/home/dev/build/plugin.wasm"},
		{"absolute selection is cleaned", root, "/home/dev/./build/../build/p.wasm", "/home/dev/build/p.wasm"},
		{"absolute selection ignores the root entirely", "/srv/other", "/etc/thing.yaml", "/etc/thing.yaml"},
		{"absolute selection needs no root", "", "/home/dev/p.wasm", "/home/dev/p.wasm"},
		{"bare climb", root, "..", "/srv"},
		{"climb out", root, "../elsewhere", "/srv/elsewhere"},
		{"climb out after descending", root, "a/../../elsewhere", "/srv/elsewhere"},
		{"climb to a prefix sibling", root, "../app-old/secret", "/srv/app-old/secret"},

		// The two refusals, carried over from Resolve for Resolve's own reasons.
		{"empty selection", root, "", ""},
		{"empty selection with an empty root", "", "", ""},
		{"relative selection with an empty root", "", "x", ""},
		{"relative selection with an empty root, nested", "", "a/b", ""},
		{"dot with an empty root", "", ".", ""},
		{"climbing selection with an empty root", "", "../up", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := pathscope.ResolveSelected(tc.root, tc.rel)
			if tc.want == "" {
				if ok {
					t.Fatalf("ResolveSelected(%q, %q) = %q, accepted; want refusal", tc.root, tc.rel, got)
				}
				if got != "" {
					t.Fatalf("ResolveSelected(%q, %q) returned path %q; a refusal returns the empty path", tc.root, tc.rel, got)
				}
				return
			}
			if !ok {
				t.Fatalf("ResolveSelected(%q, %q) refused; want %q", tc.root, tc.rel, tc.want)
			}
			if got != filepath.FromSlash(tc.want) {
				t.Fatalf("ResolveSelected(%q, %q) = %q; want %q", tc.root, tc.rel, got, tc.want)
			}
		})
	}
}

// TestResolveSelectedAcceptsWhatResolveRefuses is the paired assertion that
// makes each entry point's contract attributable to that entry point rather than
// to the input.
//
// Read alone, a test showing ResolveSelected accepts an absolute path proves
// only that some function accepts it, and a test showing Resolve refuses one
// proves only that some function refuses it. What the design actually claims is
// that ONE input meets two answers depending on which question was asked, so the
// two answers are asserted over the same values in one place. It is also the
// guard against the failure this item exists to prevent: relaxing Resolve to
// serve the selected class would make both halves here agree, and this is the
// assertion that would go red.
func TestResolveSelectedAcceptsWhatResolveRefuses(t *testing.T) {
	const root = "/srv/app"
	for _, in := range []string{
		"/etc/passwd",
		"/home/dev/build/plugin.wasm",
		"..",
		"../elsewhere",
		"../app-old/secret",
	} {
		if _, ok := pathscope.Resolve(root, in); ok {
			t.Errorf("Resolve(%q, %q) accepted; the workspace-scoped rule must not have been relaxed to serve a selected path — add a second entry point instead", root, in)
		}
		if _, ok := pathscope.ResolveSelected(root, in); !ok {
			t.Errorf("ResolveSelected(%q, %q) refused; a path a person selected names wherever they pointed", root, in)
		}
	}
}

// TestResolveSelectedRefusesWhatNamesNoPath pins the two refusals as refusals of
// this entry point, so the permissive class is not read as accepting everything.
//
// Both are asserted against ResolveSelected directly rather than through a
// caller, for the reason TestResolveRefusesEmptyRoot states: every caller in the
// tree rejects an empty value earlier today, so a test routed through one would
// pass whatever this function did.
func TestResolveSelectedRefusesWhatNamesNoPath(t *testing.T) {
	if got, ok := pathscope.ResolveSelected("/srv/app", ""); ok {
		t.Errorf("ResolveSelected accepted an empty selection, landing at %q; an empty path names nothing", got)
	}
	if got, ok := pathscope.ResolveSelected("", "plugin.wasm"); ok {
		t.Errorf("ResolveSelected(%q, %q) accepted, landing at %q; a relative selection against an empty root resolves against the process working directory, which is a different directory than the caller named", "", "plugin.wasm", got)
	}

	// The positive control: the same relative selection against a named root is
	// accepted, so the refusal above is attributable to the empty root and not
	// to the path.
	if _, ok := pathscope.ResolveSelected("/srv/app", "plugin.wasm"); !ok {
		t.Fatal(`ResolveSelected("/srv/app", "plugin.wasm") was refused; the empty-root case above proves nothing if a named root refuses too`)
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

// TestNoExportedSymbolTakesAMode is the structural half of keeping the classes
// apart. Documentation saying "use the right one" is advice; a package whose
// exported surface offers no way to express the wrong one is a guarantee.
//
// It parses the package's own source and asserts that no exported function
// carries a parameter a caller could use to switch behaviour — a bool, or a
// named type whose name reads like a mode — and that the shared primitive is not
// exported at all, so a caller cannot reach past the entry points to a function
// that does not know which arithmetic applies.
//
// The count is pinned because it is the assertion a mode in disguise trips. Each
// entry point answers a question the others do not, and the names say which:
// Resolve confines a workspace-scoped path in operating-system arithmetic,
// IsLocalSlash confines a logical name in slash arithmetic, ResolveSelected
// resolves a path a person chose and confines nothing, and Relative answers with
// the name a path has relative to a root rather than the location to open it at —
// the question a caller about to report a path asks, where the other three serve a
// caller about to use one. A further export is either one of those four wearing a
// second name or a class nobody named — and if a genuinely new class is being
// added, this count moves in the same change that names it, which is exactly the
// deliberation the pin exists to force.
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
	if exported != 4 {
		t.Errorf("package exports %d functions; the classes are meant to be exactly four entry points (Resolve, IsLocalSlash, ResolveSelected, Relative), so a fifth is either a mode in disguise or a class nobody named", exported)
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
