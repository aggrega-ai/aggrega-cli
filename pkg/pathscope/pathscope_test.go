// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package pathscope_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path"
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

// TestReportedName pins the spelling a path gets in an answer somebody outside
// this process reads.
//
// The rows the pin exists for are the outside ones. A path inside the root is the
// control — without it, an implementation that reported the absolute path for
// everything would satisfy every outside row and this table would assert nothing
// about the rule. The outside rows assert the absolute form rather than merely a
// non-empty one, because "not empty" is satisfied by the climb this replaced.
func TestReportedName(t *testing.T) {
	const root = "/srv/app"
	tests := []struct {
		name string
		root string
		path string
		want string
	}{
		// Inside the root: the workspace-relative name, which is the coordinate
		// the rest of such an answer is in. These are the controls.
		{"absolute inside", root, "/srv/app/config.yaml", "config.yaml"},
		{"absolute nested inside", root, "/srv/app/a/b/c.yaml", "a/b/c.yaml"},
		{"relative inside is taken as it stands", root, "catalog/base/x.yaml", "catalog/base/x.yaml"},
		{"two dots then a letter is an ordinary name", root, "..config/x.yaml", "..config/x.yaml"},

		// Outside the root: the absolute path, never a climb and never empty. A
		// climb spells one path two ways depending on where it is read from.
		{"absolute outside", root, "/srv/shared/catalog/x.yaml", "/srv/shared/catalog/x.yaml"},
		{"absolute prefix sibling", root, "/srv/app-old/secret.yaml", "/srv/app-old/secret.yaml"},
		{"a climb is reported at where it lands", root, "../shared/catalog/x.yaml", "/srv/shared/catalog/x.yaml"},
		{"a climb landing back inside is still where it lands", root, "../app/ok.yaml", "/srv/app/ok.yaml"},

		// The root itself has no name inside itself, so it is reported at the one
		// spelling that names it.
		{"the root itself", root, "/srv/app", "/srv/app"},

		// The empty path is the one input with no name to report.
		{"empty path", root, "", ""},
		{"empty path and empty root", "", "", ""},

		// With no root to judge against, what the caller was handed is the most
		// that can honestly be said about the path.
		{"empty root, absolute path", "", "/srv/app/x.yaml", "/srv/app/x.yaml"},
		{"empty root, relative path", "", "catalog/x.yaml", "catalog/x.yaml"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := pathscope.ReportedName(tc.root, filepath.FromSlash(tc.path))
			if got != filepath.FromSlash(tc.want) {
				t.Fatalf("ReportedName(%q, %q) = %q; want %q", tc.root, tc.path, got, tc.want)
			}
		})
	}
}

// TestReportedNameNeverWithholdsAPathItWasGiven is the property the surfaces rely
// on, asserted over every input the containment table feeds Relative — including
// the ones Relative refuses, which is the whole point.
//
// A withheld path leaves a reader holding an empty field that reads exactly like a
// field nobody filled in, so a working system and a broken one look alike from
// where they sit. The empty path is the one honest exception and is excluded by
// name rather than by a condition, so an implementation that started returning
// empty for anything else is named here rather than passing.
func TestReportedNameNeverWithholdsAPathItWasGiven(t *testing.T) {
	const root = "/srv/app"
	for _, p := range []string{
		"config.yaml", "a/b/c.yaml", "a/../b", "..config", "..cache/x",
		"..", "../elsewhere", "a/../../elsewhere", "../app/ok", "../app-old/secret",
		"/etc/passwd", "/srv/app", ".", "a/..",
	} {
		got := pathscope.ReportedName(root, filepath.FromSlash(p))
		if got == "" {
			t.Errorf("ReportedName(%q, %q) reported nothing; a non-empty path always has a spelling to report",
				root, p)
		}
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

// TestNamesFileBeneath pins the stricter question a name nested under a
// directory of its own is asked: one row per spelling refused and per spelling
// admitted.
//
// The refused rows split into the two ways a name can fail that IsLocalSlash
// admits — naming the directory itself, and naming a file a second spelling
// names too — and the IsLocalSlash column states, per row, that IsLocalSlash
// still admits it. That column is what makes each refusal attributable to the
// stricter question rather than to the containment one, and what shows
// IsLocalSlash's own verdicts did not move. The empty name and ".." stay as
// controls: both predicates refuse them, so a NamesFileBeneath that stopped
// asking IsLocalSlash at all would be caught here.
func TestNamesFileBeneath(t *testing.T) {
	tests := []struct {
		name       string
		in         string
		want       bool
		localSlash bool
	}{
		// Admitted, as IsLocalSlash admits them.
		{"plain name", "config.yaml", true, true},
		{"nested layout written plainly", "rbac/role.yaml", true, true},
		{"deeper nested layout", "a/b/c.yaml", true, true},
		{"leading two dots then a letter", "..config", true, true},
		{"two dots inside a segment", "a/b..c/d", true, true},
		{"a dotted name", "a.b", true, true},
		{"backslash is an ordinary character", `a\b`, true, true},

		// Refused: the name is the directory itself.
		{"dot", ".", false, true},
		{"dot slash", "./", false, true},
		{"a climb back onto the directory", "x/..", false, true},
		{"a climb back onto the directory, with a trailing separator", "x/../", false, true},
		{"a deeper climb back onto the directory", "a/b/../..", false, true},

		// Refused: the name is a second spelling of a file beneath the directory.
		{"trailing separator", "x/", false, true},
		{"double separator", "a//b", false, true},
		{"leading dot segment", "./a", false, true},
		{"interior dot segment", "a/./b", false, true},
		{"interior climb", "a/../b", false, true},
		{"trailing separator after a nested name", "a/b/", false, true},

		// Refused by both: the containment controls.
		{"empty", "", false, false},
		{"bare climb", "..", false, false},
		{"climb out", "../x", false, false},
		{"climb out after descending", "a/../../x", false, false},
		{"absolute", "/etc/passwd", false, false},
		{"climb through backslashes", `..\x`, false, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := pathscope.NamesFileBeneath(tc.in); got != tc.want {
				t.Errorf("NamesFileBeneath(%q) = %v; want %v", tc.in, got, tc.want)
			}
			if got := pathscope.IsLocalSlash(tc.in); got != tc.localSlash {
				t.Errorf("IsLocalSlash(%q) = %v; want %v — its verdicts are not this question's to move", tc.in, got, tc.localSlash)
			}
		})
	}
}

// TestNamesFileBeneathAdmitsNoTwoSpellingsOfOneFile is the property the stricter
// question exists for, asked over every name of up to four characters drawn from
// the alphabet the refusals are about: no two admitted names clean to one name,
// and no admitted name cleans to the directory itself. A refusal list that missed
// a spelling would pass the table above and fail here.
func TestNamesFileBeneathAdmitsNoTwoSpellingsOfOneFile(t *testing.T) {
	alphabet := []string{"a", "b", ".", "/"}
	names := []string{""}
	for length := 1; length <= 4; length++ {
		var next []string
		for _, n := range names {
			if len(n) != length-1 {
				continue
			}
			for _, c := range alphabet {
				next = append(next, n+c)
			}
		}
		names = append(names, next...)
	}
	byCleaned := map[string]string{}
	admitted := 0
	for _, n := range names {
		if !pathscope.NamesFileBeneath(n) {
			continue
		}
		admitted++
		cleaned := path.Clean(n)
		if cleaned == "." {
			t.Errorf("NamesFileBeneath(%q) admitted a name that cleans to the directory itself", n)
		}
		if other, seen := byCleaned[cleaned]; seen {
			t.Errorf("NamesFileBeneath admitted %q and %q, two spellings of %q", other, n, cleaned)
		}
		byCleaned[cleaned] = n
	}
	// The walk is the evidence the property was asked of something: a predicate
	// refusing everything would satisfy both assertions above over nothing.
	if admitted == 0 {
		t.Fatal("NamesFileBeneath admitted none of the generated names; the property above was asked of nothing")
	}
	for _, n := range []string{"a", "a/b", "..a", "a..", "a.b"} {
		if !pathscope.NamesFileBeneath(n) {
			t.Errorf("NamesFileBeneath(%q) refused an ordinary name the walk relies on being admitted", n)
		}
	}
}

// TestIsLocalSlashIsPlatformInvariant pins the property that makes the logical
// algebra a separate entry point rather than a flag on the operating-system one.
//
// A backslash is a separator in a Windows filesystem path and in no Linux one. A
// name judged with the host's own filesystem arithmetic would therefore get one
// verdict on Windows and another on Linux, so the same rendered output or archive
// would be accepted on one machine and refused on the next. IsLocalSlash gives
// each of these names one verdict wherever it runs: a backslash between ordinary
// parts is accepted, and a climb spelled with backslashes is refused, on Linux as
// on Windows.
func TestIsLocalSlashIsPlatformInvariant(t *testing.T) {
	for _, in := range []string{`a\b`, `a\b\c.yaml`} {
		if !pathscope.IsLocalSlash(in) {
			t.Errorf("IsLocalSlash(%q) refused; a backslash between ordinary parts stays under the directory on every platform", in)
		}
	}
	for _, in := range []string{`..\..\etc`, `a\..\..\b`} {
		if pathscope.IsLocalSlash(in) {
			t.Errorf("IsLocalSlash(%q) accepted; a name climbing through backslashes leaves its directory when a Windows join reads them, so it is refused on every platform", in)
		}
	}
}

// TestIsLocalSlashImpliesContainedJoin asserts the property the archive
// extractors rely on to skip a second check after joining: an accepted name
// cannot join to a location outside the root it is joined onto.
func TestIsLocalSlashImpliesContainedJoin(t *testing.T) {
	// Spelled for the host, so the prefix comparison below reads the separator a
	// join on that host writes.
	root := filepath.FromSlash("/srv/dest")
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
// IsLocalSlash confines a logical name in slash arithmetic, NamesFileBeneath asks
// of such a name whether it names a file of its own beneath the directory, in the
// one spelling that file has — the question a caller nesting the name under a
// directory of its own asks, where an archive reader asks only the climb —
// ResolveSelected
// resolves a path a person chose and confines nothing, Relative answers with the
// name a path has relative to a root rather than the location to open it at — the
// question a caller about to report a path asks, where the first three serve a
// caller about to use one — ReportedName answers the question after that one,
// which spelling to put in an answer, for a caller that owes its reader a path
// either way — ResolvesInside answers where a path leads on disk once its links
// are followed, the one question here a spelling cannot settle — SamePlace answers
// whether two paths are one place on disk, by the file system's own identity for
// what each leads to, which neither a spelling nor inside-ness settles where the
// file system folds case — and WalkDir walks the tree beneath a root, a root that is
// itself a link walked where it leads as ResolvesInside resolves such a root, which
// no question of scope covers. PlaceBeneath answers where a name a writer did not
// choose is written, composing IsLocalSlash's verdict with what a write owes beyond
// it — a root made a directory path, and the refusal of a name Windows cannot hold
// where the process runs on Windows — so a writer reaches a path through one call
// rather than through a composition of its own; DirectoryPath answers the part of
// that a caller needs before it holds any name, making a directory somebody named a
// path a name can be joined onto, so where it reports writing is where it writes;
// and ErrNameWindowsCannotHold is the accessor of the one
// condition PlaceBeneath raises, which matches a refusal and judges no path. A
// further export is either one of those twelve wearing a second name or a class
// nobody named — and if a genuinely new class is being added, this count moves in
// the same change that names it, which is exactly the deliberation the pin exists
// to force.
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
	if exported != 12 {
		t.Errorf("package exports %d functions; the classes are meant to be exactly eleven entry points (Resolve, IsLocalSlash, NamesFileBeneath, ResolveSelected, Relative, ReportedName, ResolvesInside, SamePlace, WalkDir, PlaceBeneath, DirectoryPath) and the accessor of the one condition PlaceBeneath raises (ErrNameWindowsCannotHold), so a thirteenth is either a mode in disguise or a class nobody named", exported)
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
