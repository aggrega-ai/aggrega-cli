// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"fmt"
	"go/ast"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// The invocation read: which declared checks are invoked, and which are only
// declared.
//
// Everything else this generator emits is a projection of the registry, and the
// registry describes facts. It cannot answer the question a reader of the
// placement table actually arrives with — is this rule enforced here — because a
// declared check's placement follows from its view whether or not anything ever
// invokes it. A declaration and a guard read identically from the registry's
// side, and that is the misreading this read exists to close.
//
// So this is the one derivation that reads the module's source rather than the
// registry. It is still a derivation and not a list: the traversal entries, the
// declared checks and the call sites are all found by parsing, so nothing here
// has to be kept in step with the tree by hand. A hand-maintained inventory
// would be a second copy of what the tree already states, and the copy is what
// goes stale — which matters more here than anywhere else in this generator,
// because a stale entry would say a rule is enforced when it is not.
//
// # What makes the emitted statement unable to go stale
//
// The committed table is drift-gated: the gate renders a fresh table and diffs
// it against the committed one. Because the statement is derived from the same
// tree the gate runs over, landing the first invocation changes what this read
// returns, which changes the rendered table, which fails the gate until the
// committed table is regenerated. The prose cannot lag the code, because the
// prose is a function of the code.
//
// # The bounds, which the emitted prose states rather than hides
//
// The read is syntactic. It sees a check named at a call site and cannot follow
// one reaching a traversal through a variable or a parameter; such a site is
// reported as an invocation with no check attributable to it, so the two totals
// disagree and the disagreement is the signal rather than a silent undercount.
//
// It reads every Go file the tree carries, whatever build constraints that file
// declares, and excludes test files, the directories a census never walks, and
// the packages nothing outside a test imports. Reading past build constraints is
// the safe direction for this question: a check invoked only under some build is
// still invoked, and a scan that honoured the current build's constraints would
// answer "not enforced" for it.
//
// # Why a test-support package is excluded, and how that exclusion is drawn
//
// A file-suffix criterion alone counts a test-support package as production,
// because such a package's files carry no test suffix — they must not, or no
// test could import them. That is harmless for the check census, which
// attributes names at traversal call sites and finds none there, and it is not
// harmless for the caller read beside it, which is keyed off calls into the
// compiler and would report a harness among the surfaces a user reaches.
//
// The exclusion is drawn structurally rather than by name or by path: a package
// is test support when at least one file imports it and every importing file is
// a test file. A naming convention would be weaker — a package whose name merely
// ends in the same letters as the convention would be excluded on a spelling —
// and a path list is a second copy of what the import graph already states.
//
// The alternative signal, a non-test file importing the standard testing
// package, was measured against this tree and rejected: it excludes a genuine
// production package, because production code legitimately branches on whether
// it is running inside a test binary. The import-graph criterion leaves such a
// package in, since production files import it.
//
// What the chosen criterion would wrongly exclude, stated rather than left to be
// found: a production package that nothing has imported yet outside a test, and
// a package reached only through a mechanism no import records — a build-tag
// variant compiled in another configuration, or a binary invoking it as a
// subprocess. Both would be reported as absent surfaces rather than as wrong
// ones, and a package imported by nothing at all is deliberately NOT excluded,
// so a command's own entry package stays in.

// declaredCheck is one check value the tree declares, and whether anything
// invokes it.
type declaredCheck struct {
	// Name is the declared variable's name.
	Name string

	// Package is the directory declaring it, relative to the module root.
	Package string

	// View is the view type argument, unqualified — the facts the check reads,
	// and therefore its stage.
	View string

	// Subject is the subject type argument, unqualified.
	Subject string

	// Invoked reports whether some production call site names this check.
	Invoked bool
}

// invocationSite is one call to a framework traversal entry from outside the
// framework: a production invocation.
type invocationSite struct {
	// Entry is the traversal entry called.
	Entry string

	// File is the file holding the call, relative to the module root.
	//
	// It is the file and not the line deliberately. The committed table is
	// drift-gated, so any coordinate it prints becomes a thing an unrelated edit
	// can make stale: a line number would fail the gate every time the containing
	// file grew a line above the call, for no gain to a reader who wants to know
	// which file invokes the framework.
	File string

	// Check is the check the call names, empty when the argument is not a name
	// this syntactic read can attribute to a declaration.
	Check string
}

// invocationState is what the source read establishes.
type invocationState struct {
	// Entries are the traversal entry names the framework declares, derived from
	// its own declarations rather than listed.
	Entries []string

	// Checks are the declared checks, ordered by package then name.
	Checks []declaredCheck

	// Sites are the production invocation sites, in walk order.
	Sites []invocationSite

	// FrameworkSites counts calls to a traversal entry from inside the framework
	// itself. Such a call is not a production invocation — it is the framework
	// reaching its own loop — and it is counted separately rather than dropped so
	// that a zero production count is distinguishable from a zero total, which
	// would mean the read found nothing at all.
	FrameworkSites int

	// FilesRead is how many files the read parsed. A search for something absent
	// certifies nothing unless the search reached a corpus, so this is carried
	// and asserted rather than assumed.
	FilesRead int
}

// DeclaredOnly counts the checks nothing invokes.
func (s invocationState) DeclaredOnly() int {
	n := 0
	for _, c := range s.Checks {
		if !c.Invoked {
			n++
		}
	}
	return n
}

// InvokedCount counts the checks some production call site names.
func (s invocationState) InvokedCount() int { return len(s.Checks) - s.DeclaredOnly() }

// Unattributed counts the production call sites naming no check this read can
// resolve to a declaration.
func (s invocationState) Unattributed() int {
	n := 0
	for _, site := range s.Sites {
		if site.Check == "" {
			n++
		}
	}
	return n
}

// readInvocationState parses the module's non-test Go source and derives the
// traversal entry set, the declared checks and the production invocation sites.
//
// It refuses an empty read rather than reporting a clean one. Every question it
// answers is a search for something that may be absent, so a read that found no
// file, no traversal entry or no declared check has measured the instrument
// rather than the tree, and reporting "nothing is invoked" from it would be the
// exact false reassurance this whole read exists to prevent.
//
// The result is computed once per process. That is a correctness property before
// it is a saving: the placement-table run both renders the section and reports
// what the read reached, and two walks could observe two different trees, which
// would print figures that do not describe the page beside them.
func readInvocationState() (invocationState, error) {
	invocationOnce.Do(func() {
		invocationRead, invocationErr = walkInvocationState()
	})
	return invocationRead, invocationErr
}

var (
	invocationOnce sync.Once
	invocationRead invocationState
	invocationErr  error
)

func walkInvocationState() (invocationState, error) {
	m, err := readModuleSource()
	if err != nil {
		return invocationState{}, err
	}
	root := m.Root
	parsed := m.Files

	state := invocationState{FilesRead: len(parsed)}
	if state.FilesRead == 0 {
		return invocationState{}, fmt.Errorf(
			"the invocation read parsed no Go file under %s, so a report that nothing is invoked would "+
				"describe the instrument rather than the tree", root)
	}

	// The framework's own directory is derived from where the traversal entries
	// are declared, not from this generator's position in the tree. Declaring the
	// path here would be a second statement of where the framework lives, and it
	// would keep compiling after the framework moved.
	entries := map[string]bool{}
	frameworkDirs := map[string]bool{}
	for _, p := range parsed {
		for _, decl := range p.File.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv != nil || !strings.HasPrefix(fn.Name.Name, traversalEntryPrefix) {
				continue
			}
			entries[fn.Name.Name] = true
			frameworkDirs[p.Dir] = true
		}
	}
	if len(entries) == 0 {
		return invocationState{}, fmt.Errorf(
			"the invocation read found no traversal entry declared under %s; with an empty entry set every "+
				"call site is invisible to it and its verdict means nothing", root)
	}
	if len(frameworkDirs) != 1 {
		return invocationState{}, fmt.Errorf(
			"the invocation read found traversal entries declared in %d directories (%s); it distinguishes "+
				"a production invocation from the framework reaching its own loop by which directory the "+
				"call sits in, and that distinction is not drawable across several",
			len(frameworkDirs), strings.Join(sorted(keysOf(frameworkDirs)), ", "))
	}
	state.Entries = sorted(keysOf(entries))

	for _, p := range parsed {
		inFramework := frameworkDirs[p.Dir]
		state.Checks = append(state.Checks, declaredChecksIn(p.Dir, p.File)...)
		if inFramework {
			state.FrameworkSites += countTraversalCalls(p.File, entries)
			continue
		}
		sites, cerr := traversalCallSites(p, entries)
		if cerr != nil {
			return invocationState{}, cerr
		}
		state.Sites = append(state.Sites, sites...)
	}
	if len(state.Checks) == 0 {
		return invocationState{}, fmt.Errorf(
			"the invocation read found no declared check under %s; the framework's own tests declare "+
				"several and the tree ships more, so an empty set means the read is not finding "+
				"declarations rather than that none exist", root)
	}

	sort.Slice(state.Checks, func(i, j int) bool {
		if state.Checks[i].Package != state.Checks[j].Package {
			return state.Checks[i].Package < state.Checks[j].Package
		}
		return state.Checks[i].Name < state.Checks[j].Name
	})

	named := map[string]bool{}
	for _, site := range state.Sites {
		if site.Check != "" {
			named[site.Check] = true
		}
	}
	for i := range state.Checks {
		state.Checks[i].Invoked = named[state.Checks[i].Name]
	}
	return state, nil
}

// traversalEntryPrefix is the prefix the framework's traversal entries share. It
// is the one spelling this read carries, and it is a prefix rather than a list so
// that a traversal added later is found without an edit here.
const traversalEntryPrefix = "RunOn"

// declaredChecksIn returns the check values one file declares at package level.
//
// It reads the composite literal's type arguments rather than any name beside
// them, so the view and subject it reports are the ones the declaration actually
// carries.
func declaredChecksIn(dir string, file *ast.File) []declaredCheck {
	var out []declaredCheck
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok {
			continue
		}
		for _, spec := range gen.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			for i, value := range vs.Values {
				lit, ok := value.(*ast.CompositeLit)
				if !ok || i >= len(vs.Names) {
					continue
				}
				view, subject, ok := checkTypeArgs(lit.Type)
				if !ok {
					continue
				}
				out = append(out, declaredCheck{
					Name:    vs.Names[i].Name,
					Package: dir,
					View:    view,
					Subject: subject,
				})
			}
		}
	}
	return out
}

// checkTypeArgs reports the view and subject arguments of a Check instantiation,
// accepting the framework's own unqualified spelling and an importing package's
// qualified one.
func checkTypeArgs(expr ast.Expr) (view, subject string, ok bool) {
	var args []ast.Expr
	switch t := expr.(type) {
	case *ast.IndexExpr:
		args = []ast.Expr{t.Index}
		expr = t.X
	case *ast.IndexListExpr:
		args = t.Indices
		expr = t.X
	default:
		return "", "", false
	}
	if len(args) != 2 || baseIdent(expr) != "Check" {
		return "", "", false
	}
	return baseIdent(args[0]), baseIdent(args[1]), true
}

// countTraversalCalls counts calls to a traversal entry within one file.
func countTraversalCalls(file *ast.File, entries map[string]bool) int {
	n := 0
	ast.Inspect(file, func(node ast.Node) bool {
		if call, ok := node.(*ast.CallExpr); ok && entries[baseIdent(call.Fun)] {
			n++
		}
		return true
	})
	return n
}

// baseIdent is the identifier an expression ultimately names, reached through a
// package qualifier and through explicit type arguments.
func baseIdent(expr ast.Expr) string {
	switch e := expr.(type) {
	case *ast.Ident:
		return e.Name
	case *ast.SelectorExpr:
		return e.Sel.Name
	case *ast.IndexExpr:
		return baseIdent(e.X)
	case *ast.IndexListExpr:
		return baseIdent(e.X)
	case *ast.StarExpr:
		return baseIdent(e.X)
	default:
		return ""
	}
}

// skipCensusDir reports whether the source walk stops at a directory. A root is
// never skipped for its own sake, so a root spelled with a leading dot does not
// match the dot-prefix rule and empty the walk.
//
// The framework's own guard tests apply the same exclusions to the same tree, and
// this is deliberately a second copy rather than a shared helper: that one lives
// in a _test.go file, which no package can import, so there is nothing to share
// short of introducing a package for it. Do not "unify" the two by moving this
// one — the coupling to reach is the exclusion RULE, and if the two ever need to
// be provably identical the way to get there is a package both import, not one
// calling the other.
func skipCensusDir(path, root, name string) bool {
	if path == root {
		return false
	}
	if name == "testdata" || name == "vendor" ||
		strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_") {
		return true
	}
	// A directory carrying its own module is a different module's source. The
	// tree nests several, and a check declared in one of them is not this
	// module's to report on.
	_, err := os.Stat(filepath.Join(path, "go.mod"))
	return err == nil
}

// moduleRoot walks up from the working directory to the module this generator
// ships in.
//
// It is found rather than spelled. The generator runs from its own directory —
// by a generate directive, by the drift gate, or by hand — so a relative path to
// the module root would be a second statement of where this package sits and
// would keep resolving to the wrong place after a move.
func moduleRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("locate the working directory: %w", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf(
				"no go.mod above the working directory, so the invocation read has no module to walk")
		}
		dir = parent
	}
}

func keysOf(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// reportInvocationInstrument prints what the invocation read reached, on every
// run and whatever the target.
//
// These are figures about the instrument rather than about the tree's rules, so
// they are printed rather than rendered into the drift-gated page: a page
// carrying a module-wide file count would go stale on every file added anywhere,
// and a page that fails its gate for unrelated reasons teaches its readers to
// regenerate without reading. Printed here, an operator who wants to know whether
// the read reached anything can see it at the moment it ran.
//
// A read that fails is reported and does not halt: the caller that needs the read
// already refuses on the same error, and a target that does not need it must not
// be blocked by it.
func reportInvocationInstrument(w *os.File) {
	state, err := readInvocationState()
	if err != nil {
		fmt.Fprintf(w, "factgen: the invocation read did not complete: %v\n", err)
		return
	}
	fmt.Fprintf(w,
		"factgen: invocation read — %d file(s) parsed, %d traversal entr%s found, "+
			"%d declared check(s), %d production invocation site(s), %d framework-internal call(s)\n",
		state.FilesRead, len(state.Entries), plural(len(state.Entries), "y", "ies"),
		len(state.Checks), len(state.Sites), state.FrameworkSites)
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// writeInvocationSection renders the invoked-versus-declared-only section of the
// placement table.
//
// It is the answer to the question the rest of the table cannot answer, and it is
// emitted from the same run so that the two cannot disagree: a reader who takes a
// stage row for a guard is corrected by a section derived from the same tree that
// produced the row.
func writeInvocationSection(b *strings.Builder, c chain) error {
	state, err := readInvocationState()
	if err != nil {
		return err
	}

	stageOfView := make(map[string]string, len(c.order))
	for _, s := range c.order {
		stageOfView[viewName(s.Name)] = s.Name
	}

	b.WriteString("## Which declared checks are invoked\n\n")
	b.WriteString("Derived by parsing this module's own source: the traversal entries the\n")
	b.WriteString("framework declares, every check value declared outside a test, and every call\n")
	b.WriteString("to a traversal entry. Nothing in this section is kept in step with the tree by\n")
	b.WriteString("hand, because an inventory that could lag the tree would be able to say a rule\n")
	b.WriteString("is enforced when it is not.\n\n")

	switch {
	case state.InvokedCount() == 0:
		b.WriteString("**No declared check is invoked from a production path.** Every check below is\n")
		b.WriteString("declared-only, and the declaration is what makes the placement in the rest\n")
		b.WriteString("of this table computable. So a stage row says where a check declaring that\n")
		b.WriteString("stage's view would be invoked, and nothing in this table reports a rule as\n")
		b.WriteString("enforced.\n\n")
	case state.DeclaredOnly() == 0:
		// This arm says nothing about hand-written enforcement, and the omission is
		// deliberate rather than an oversight to be helpfully filled in. The count
		// this arm is chosen by reads declared checks and traversal-entry calls; a
		// hand-written function is neither and names nothing to a traversal entry,
		// so it lies outside the read's corpus by construction. A sentence here
		// asserting that such a function is gone would be an inference from a
		// measurement that cannot see one, printed with the authority of a derived
		// page. What the read establishes is invocation, so invocation is all this
		// says.
		b.WriteString("**Every declared check is invoked from a production path.** Each check below\n")
		b.WriteString("runs at the stage its declared view fixes.\n\n")
	default:
		b.WriteString("**Some declared checks are invoked and some are not, and the column below is\n")
		b.WriteString("the only place that difference is visible.** An invoked check runs at the\n")
		b.WriteString("stage its declared view fixes.\n\n")
	}

	b.WriteString("| Check | Declared in | Stage | Invoked |\n")
	b.WriteString("|---|---|---|---|\n")
	for _, ck := range state.Checks {
		stage := stageOfView[ck.View]
		stageCell := "`" + stage + "`"
		if stage == "" {
			// A check declaring a view this generator did not emit is a check
			// against a locally declared view, which the placement chain does not
			// order. Naming the view is the honest cell; inventing a stage for it
			// would report a placement nothing computed.
			stageCell = "not a generated view (`" + ck.View + "`)"
		}
		invoked := "no — declared-only"
		if ck.Invoked {
			invoked = "yes"
		}
		fmt.Fprintf(b, "| `%s` | `%s` | %s | %s |\n", ck.Name, ck.Package, stageCell, invoked)
	}
	fmt.Fprintf(b, "\nTotal: **%d declared checks** — **%d** invoked, **%d** declared-only, "+
		"across **%d** production invocation site(s).\n\n",
		len(state.Checks), state.InvokedCount(), state.DeclaredOnly(), len(state.Sites))

	if len(state.Sites) > 0 {
		b.WriteString("| Production invocation site | Traversal entry | Check named |\n")
		b.WriteString("|---|---|---|\n")
		for _, site := range state.Sites {
			named := "`" + site.Check + "`"
			if site.Check == "" {
				named = "none this read can attribute"
			}
			fmt.Fprintf(b, "| `%s` | `%s` | %s |\n", site.File, site.Entry, named)
		}
		b.WriteString("\n")
	}

	b.WriteString("The criterion, so the counts are readable rather than merely printed. A check\n")
	b.WriteString("is a package-level value of the framework's check type declared in a Go file\n")
	b.WriteString("this module carries, outside a test. It counts as invoked when some such file\n")
	b.WriteString("outside the framework's own directory passes it, by name, to one of the\n")
	b.WriteString("framework's traversal entries. The read parses every Go file in the module\n")
	b.WriteString("whatever build constraints it declares — a check invoked only under some build\n")
	b.WriteString("is still invoked — and it walks past test files, test data, vendored source and\n")
	b.WriteString("any directory carrying its own module.\n\n")
	b.WriteString("Two bounds, stated rather than left for a reader to discover. The read is\n")
	b.WriteString("syntactic, so a check reaching a traversal through a variable or a parameter\n")
	b.WriteString("instead of being named at the call site is reported as a site with no check\n")
	b.WriteString("attributable to it — a row in the site listing, which is printed whenever any\n")
	b.WriteString("site exists, naming no check. The invoked count and the site count then\n")
	b.WriteString("disagree, and that disagreement is the signal rather than a silent\n")
	b.WriteString("undercount. And a call the framework makes to its own traversal entry is not a\n")
	b.WriteString("production invocation and is excluded, counted separately so that a zero\n")
	b.WriteString("production count is distinguishable from a read that found nothing at all.\n\n")
	b.WriteString("The read refuses to report at all on a corpus it did not reach: no file\n")
	b.WriteString("parsed, no traversal entry found, or no check declared halts generation rather\n")
	b.WriteString("than rendering a clean result. So a zero above is a statement about the tree\n")
	b.WriteString("and not about the instrument. The instrument's own figures — files parsed,\n")
	b.WriteString("entries found, framework-internal calls — are printed by the generator when it\n")
	b.WriteString("runs, and are deliberately absent here: they move with every file added\n")
	b.WriteString("anywhere in the module, and a drift-gated page that churns on unrelated edits\n")
	b.WriteString("trains its readers to regenerate without reading.\n\n")

	return nil
}
