// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package constraint

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// The sentinel-emission assertion.
//
// What makes something a check is not what it reads but that its output can stop
// or warn a build. So the surface that makes an off-framework check detectable is
// the currency of halting: every sentinel's emission sites should be exactly the
// framework's dispatch. A check written outside the framework then has two
// options and both are visible. Reuse an existing sentinel, and the sentinel
// gains a second emission site. Mint a new one, and there is a sentinel no
// registered check emits.
//
// This is enforcement rather than documentation, which is why it lives with the
// framework. It derives its data from the code — the sentinel declarations, the
// function bodies and the check declarations — and never from a hand-maintained
// inventory: that inventory marks its own coordinates approximate and asks to be
// verified against the code, so it disclaims exactly the precision an assertion
// needs. It is a useful shape template for which fields matter and an unusable
// source for what their values are.
//
// # Enforcement is deliberately gated, and the gate is not a formality
//
// The assertion CANNOT block yet, and the reason is structural rather than a
// matter of confidence in it. Until the checks that raise these sentinels are
// written against the framework and the paths that raise them today are removed,
// every sentinel in the tree is emitted outside the framework's dispatch — so a
// blocking assertion would fail on the shipped tree, every run, and would be
// switched off rather than fixed. It therefore reports, and the change that
// removes the last off-framework emission path flips [sentinelEmissionBlocks] to
// true in the same edit that makes the flip pass.
//
// Reporting is weaker than it sounds, and saying so here is the point: nothing
// about a report-only assertion distinguishes one whose finding reaches a reader
// from one whose finding reaches nobody, because the code plainly writes the
// message either way. A Go package that passes prints nothing in a routine run —
// not the framework's log, not a write to standard error, not a write to
// standard output — so while this constant is false the counts reach a reader
// only under a verbose run (`go test -v`) of this package. The channel
// measurement behind that claim is recorded once, beside
// [codeLiteralGuardBlocks], rather than restated here.
//
// What still blocks at false is the instrument itself: a declared population of
// zero is fatal below, so a census that has stopped working is loud even while a
// population it reports is quiet.
//
// # What counts as the framework's dispatch, and why not by address
//
// A check in the framework's prescribed shape is a package-level value of
// [Check], and its predicate is a function literal rather than a declared
// function. Such a declaration belongs to the dispatch wherever it lives, and
// the reason is the framework's own contract rather than its address: a check
// returns a raise, a raise can only be produced from a subject the framework's
// traversal supplied, and the framework — not the check — builds whatever each
// caller needs from it. The halting currency at that site is framework-issued by
// construction, which is exactly what reading the declaring package's path was
// standing in for. A plain declared function is still read by its path: written
// inside this package it belongs to the dispatch, written anywhere else it does
// not. So expressing a rule as a check is what moves it, and no rule is
// amnestied by where its file sits.
//
// # The blind spot this admission carries
//
// The census reads declarations and cannot observe invocation, so a check that
// is declared and never invoked is attributed to the dispatch exactly as one
// that runs. The within-framework figure therefore answers "is this rule
// expressed as a check?" and never "does this rule run?" — and the distinction
// is live rather than theoretical, because a check's declaration and its wiring
// arrive separately. It is stated here, in the census output and in the failure
// message because an unstated heuristic that reads as a proof is the failure
// class this framework exists to remove, and an instrument is the most expensive
// place to leave one: every count downstream inherits its silence.
//
// # The census criterion, and what it deliberately excludes
//
// A sentinel is a distinct name matching [sentinelName] bound by a package-level
// declaration in a non-test .go file of this module: a function declaration, or
// a var or const specification. The asserted population is those declared under
// [scannedRoots]. Every count this file reports is a count of that population and
// of nothing else, so the criterion is written down here: a count whose criterion
// is implicit invites two readers to compare two different measurements and
// conclude the population moved.
//
// The exclusions, each a choice about what the population IS rather than a
// convenience, so that none of them is later "fixed" as an oversight:
//
//   - A _test.go file. A sentinel declared in a test is scaffolding rather than
//     a rule the product raises, and including them would additionally make
//     this assertion assert over its own instrument.
//   - A testdata directory. Fixture Go is example data, not specification.
//   - A directory carrying its own go.mod, and a vendor directory. Their
//     sentinels belong to another module, so reporting one would be a finding
//     nobody working in this module could act on.
//   - A directory whose name begins with "." or "_", which is what the Go tool
//     itself skips, so a file there is in no package this module builds.
//   - A var bound inside a function body. The census reads file-level
//     declarations only, because the convention a sentinel follows is a
//     package-level exported name.
//   - A method. A name bound to a receiver is reached through a value rather
//     than through the package, so it is not a name a consumer matches against.
//
// And three deliberate inclusions, recorded because they are the ones a later
// edit is most likely to undo.
//
// A function declaration counts, and it is the shape most sentinels take: the
// identity itself is a value no caller can construct, published by an exported
// accessor standing beside the registration that declares it. The accessor's
// name is the name a consumer matches against, so the accessor is the
// declaration this census counts.
//
// A const or var declaration counts. A sentinel is the distinct exported name a
// consumer matches against and the rule that name stands for, not the kind of
// declaration that binds it, so an identity bound to a value is a sentinel
// exactly as one published by an accessor is. Reading one shape alone would make
// this a count of that shape rather than of the rules this module raises, and a
// rule whose declaration changed shape would then read as a rule that
// disappeared — the one reading a population count must never produce, because
// it is indistinguishable from a rule that was genuinely dropped.
//
// A forwarding declaration, of either shape —
//
//	func ErrX() error { return otherpkg.ErrY() }
//
// — counts. [sentinelName] is matched against the declared name and nothing
// constrains what the declaration yields, which is deliberate: the forwarding
// name is a distinct exported name a consumer can match against, so it is a
// sentinel of this module even though the identity it carries originates
// elsewhere. Constraining the right-hand side to a registration would read as
// tightening the criterion and would silently drop every re-export.
const sentinelEmissionBlocks = false

// sentinelName matches the declaration convention for a sentinel: an exported
// package-level error variable named for the rule it stands for.
var sentinelName = regexp.MustCompile(`^Err[A-Z][A-Za-z0-9]*$`)

// scannedRoots are the subtrees whose declarations form the asserted
// population, relative to this package: every tree in this module that holds
// Go.
//
// The set is asserted to cover the module rather than assumed to —
// [TestAssertedSentinelPopulationCoversTheModule] reads the whole module with
// the identical criterion and fails on anything these roots do not reach. A
// root that declares no sentinel today is still listed, because a tree absent
// from this set is precisely where a later sentinel arrives unobserved: the
// census would report a clean population while saying nothing about the file it
// never opened.
var scannedRoots = []string{
	"../../../pkg",
	"../../../internal",
	"../../../cmd",
	"../../../plugins",
}

// moduleRoot is this module's root, relative to this package. It is read for one
// purpose only — establishing that [scannedRoots] covers it — and never as the
// population itself: an explicit root list is what makes the asserted scope
// legible, and widening the scope to the module root would trade that legibility
// for a scope that silently absorbs whatever tree appears next.
const moduleRoot = "../../.."

// frameworkPackageSuffix locates the framework's own package, which is how a
// plain declared function's membership is read: a function declared here is part
// of the dispatch, and every other function naming a sentinel is not. A check
// declaration's membership is not read this way — see [checkTypeName].
const frameworkPackageSuffix = "internal/compiler/constraint"

// checkTypeName is the type whose package-level values are the framework's
// dispatch by construction. The census matches it as syntax — the name a
// composite literal constructs, with any generic instantiation and package
// qualifier unwrapped — and deliberately resolves no types: a type-resolving
// census is a different instrument with a different cost, and the name is
// unambiguous here because the framework declares the only type that bears it.
const checkTypeName = "Check"

func TestSentinelEmissionSitesAreTheFrameworkDispatch(t *testing.T) {
	t.Parallel()

	declared, emitters := sentinelCensus(t)
	if len(declared) == 0 {
		t.Fatalf("no sentinel declaration was found under %s, so a clean result certifies nothing",
			strings.Join(scannedRoots, ", "))
	}

	var offFramework, unemitted, withinFramework []string
	for _, name := range declared {
		sites := emitters[name]
		if len(sites) == 0 {
			unemitted = append(unemitted, name)
			continue
		}
		outside := false
		for _, site := range sites {
			if !site.framework {
				outside = true
			}
		}
		if outside {
			offFramework = append(offFramework, name)
		} else {
			withinFramework = append(withinFramework, name)
		}
	}

	report := t.Logf
	if sentinelEmissionBlocks {
		report = t.Errorf
	}

	t.Logf("sentinel census: %d declared; %d emitted only by the framework's dispatch; "+
		"%d emitted outside it; %d with no emission site at all",
		len(declared), len(withinFramework), len(offFramework), len(unemitted))
	t.Logf("the within-framework figure is counted from declarations: a rule expressed as a %s value "+
		"counts there wherever it is declared, and it counts whether or not anything invokes it yet. "+
		"It measures how many rules are expressed as checks, never how many run.", checkTypeName)
	if len(withinFramework) > 0 {
		t.Logf("emitted only by the framework's dispatch, each named beside the declaration it was "+
			"attributed from, so the attribution can be checked rather than taken: %s",
			strings.Join(head(namedWithSites(withinFramework, emitters), 12), "; "))
	}

	if len(offFramework) > 0 {
		report("%d sentinel(s) are emitted outside the framework's dispatch.\n"+
			"INVARIANT: a sentinel's emission sites are exactly the framework's dispatch, because that is "+
			"what makes a check written outside the framework detectable — reusing a sentinel shows up as "+
			"a second emission site, and minting one shows up as a sentinel no registered check emits.\n"+
			"ATTRIBUTION: a sentinel named in a package-level check declaration belongs to the dispatch "+
			"wherever that declaration lives, because the halting currency there is framework-issued by "+
			"construction; a plain declared function is read by its path instead. That reads declarations "+
			"and cannot observe invocation, so a check declared and never invoked is attributed to the "+
			"dispatch too.\n"+
			"CONSEQUENCE while this reports rather than blocks: an off-framework check gets no computed "+
			"placement, no caller-coverage guarantee and no framework-supplied provenance, and nothing "+
			"halts on its absence.\n"+
			"First 12, alphabetically: %s", len(offFramework), strings.Join(head(offFramework, 12), ", "))
	}
	if len(unemitted) > 0 {
		report("%d sentinel(s) have no emission site at all: %s.\n"+
			"A sentinel nothing emits is either a rule that was removed without its name, or a name "+
			"minted for a check that was never written.", len(unemitted), strings.Join(sortedNames(unemitted), ", "))
	}

	if !sentinelEmissionBlocks {
		t.Logf("this assertion reports and does not block. It becomes blocking when the last emission "+
			"path outside the framework's dispatch is removed; until then it would fail on the shipped "+
			"tree on every run. Off-framework population at this run: %d of %d sentinels.",
			len(offFramework), len(declared))
	}
}

// TestAssertedSentinelPopulationCoversTheModule asserts that [scannedRoots]
// reaches every sentinel declaration in the module, so the population every other
// count in this file is taken over is the whole population rather than whatever
// the root list happens to name.
//
// This is the completeness half of the census, and it exists because the failure
// it catches is silent in both directions. A sentinel declared in a tree no root
// reaches is not reported as missing — it is not reported at all, and the census
// prints a clean population while saying nothing about the file it never opened.
// The reverse reading is worse: a reader comparing two counts taken under two
// root lists sees the population move and concludes sentinels were added or
// removed, when the criterion changed underneath them.
//
// # Why this one blocks while the emission assertion does not
//
// [sentinelEmissionBlocks] gates the emission assertion because that assertion
// fails on the shipped tree today: the migration it measures is under way and a
// blocking form would be switched off rather than fixed. Coverage carries no such
// backlog — the roots cover the module, so this assertion passes — and there is
// nothing for a grace period to protect. It therefore fails rather than reports,
// which matters more here than elsewhere in this file: a passing Go test's log
// output is discarded unless the run asks for verbose output, so an assertion
// that only logs is an assertion whose finding no build mode shows.
//
// The remedy when it fails is one of two edits, and the failure names both: add
// the tree to [scannedRoots] if its sentinels belong to the asserted population,
// or move the declaration into a tree already listed. Neither is a suppression —
// both make the population say what it means.
func TestAssertedSentinelPopulationCoversTheModule(t *testing.T) {
	t.Parallel()

	assertedFiles, _ := collectGoFiles(t, scannedRoots, excludeTestFiles)
	asserted := declaredSentinels(assertedFiles)
	if len(asserted) == 0 {
		t.Fatalf("no sentinel declaration was found under %s, so a clean coverage result certifies nothing",
			strings.Join(scannedRoots, ", "))
	}

	moduleFiles, _ := collectGoFiles(t, []string{moduleRoot}, excludeTestFiles)
	moduleWide := declaredSentinels(moduleFiles)

	var unreadFiles []string
	for path := range moduleFiles {
		if _, ok := assertedFiles[path]; !ok {
			unreadFiles = append(unreadFiles, filepath.ToSlash(path))
		}
	}
	sort.Strings(unreadFiles)

	var escaped []string
	for name, path := range moduleWide {
		if _, ok := asserted[name]; !ok {
			escaped = append(escaped, name+" at "+filepath.ToSlash(path))
		}
	}
	sort.Strings(escaped)

	t.Logf("sentinel population coverage: %d sentinels over %d files under the asserted roots (%s); "+
		"%d sentinels over %d files read across the whole module (%s). This assertion blocks: unlike the "+
		"emission assertion in this file, coverage carries no backlog to grow out of.",
		len(asserted), len(assertedFiles), strings.Join(scannedRoots, ", "),
		len(moduleWide), len(moduleFiles), moduleRoot)

	if len(escaped) > 0 {
		t.Errorf("%d sentinel(s) are declared in this module but sit outside the asserted population.\n"+
			"CONSEQUENCE: no count in this file describes them. They are not reported as missing an "+
			"emission site, not reported as emitted outside the framework's dispatch, and not counted in "+
			"the declared total — the census reads a population that does not include them and prints a "+
			"clean result.\n"+
			"REMEDY: add the tree to scannedRoots if these sentinels belong to the asserted population, or "+
			"move the declaration into a tree already listed.\n"+
			"First 12, alphabetically: %s", len(escaped), strings.Join(head(escaped, 12), "; "))
	}

	// The name-level finding above cannot see a declaration whose name is ALSO
	// declared inside the roots: the population is a set of names, so a duplicate
	// name is not absent from it while its file still goes unread. Asserting the
	// file set closes that hole and makes the escape impossible rather than merely
	// detected.
	if len(unreadFiles) > 0 {
		t.Errorf("%d non-test Go file(s) in this module sit under no asserted root, so any sentinel they "+
			"declare is outside every count this file reports.\n"+
			"This fails even when those files declare no sentinel today, deliberately: the tree is where "+
			"the next one lands, and a root added after the fact does not retroactively make the "+
			"intervening counts true.\n"+
			"REMEDY: add the tree to scannedRoots, or exclude it through the documented criterion if its "+
			"Go belongs to another module.\n"+
			"First 12, alphabetically: %s", len(unreadFiles), strings.Join(head(unreadFiles, 12), ", "))
	}
}

// emissionSite is one place a sentinel is named, and whether that place is the
// framework's dispatch. Membership travels with the site rather than being
// recomputed from its name, because the two kinds of site establish it
// differently: a declared function by its package, a check declaration by what
// it constructs.
type emissionSite struct {
	name      string
	framework bool
}

// sentinelCensus returns the declared sentinel names, sorted, and the sites
// naming each one.
//
// Two shapes count as a site. A non-test declared function whose body names a
// sentinel identifier — the criterion the covering measurement used — and a
// package-level [Check] declaration whose value expression names one. Both count
// the site rather than the occurrence, so naming one sentinel twice is one site,
// and both are an upper bound on checks: a function that wraps a sentinel while
// re-raising it counts as naming it.
//
// A check declaration is walked whole, from the composite literal down, rather
// than only at the function literal holding the predicate. A check names its
// sentinel on the value's own field as well as inside the predicate, so a walk
// scoped to the predicate would see the second and miss the first — and would
// break outright on a check that names its sentinel only on the field. Walking
// the value expression also needs no knowledge of which field holds the
// predicate, so the field set can grow without the census following it.
//
// The walk stops at the check shape and does not extend to package-level
// variables in general, which is a deliberate bound rather than an oversight. A
// sentinel is routinely named in the declaration of another sentinel that wraps
// it, and in diagnostic tables keyed by sentinel; counting those as emission
// sites would answer a different question than the one this assertion asks, and
// would reclassify sentinels on the strength of a declaration that raises
// nothing.
func sentinelCensus(t *testing.T) (declared []string, emitters map[string][]emissionSite) {
	t.Helper()

	files, _ := collectGoFiles(t, scannedRoots, excludeTestFiles)
	names := declaredSentinels(files)

	emitters = map[string][]emissionSite{}
	for path, file := range files {
		dir := filepath.ToSlash(filepath.Dir(path))
		inFrameworkPackage := strings.Contains(dir, frameworkPackageSuffix)
		for _, decl := range file.Decls {
			switch d := decl.(type) {
			case *ast.FuncDecl:
				if d.Body == nil {
					continue
				}
				recordNamed(emitters, names, emissionSite{
					name:      dir + "." + d.Name.Name,
					framework: inFrameworkPackage,
				}, d.Body)
			case *ast.GenDecl:
				if d.Tok != token.VAR {
					continue
				}
				for _, spec := range d.Specs {
					vs, ok := spec.(*ast.ValueSpec)
					if !ok {
						continue
					}
					for i, value := range vs.Values {
						if i >= len(vs.Names) || !isCheckValue(value) {
							continue
						}
						recordNamed(emitters, names, emissionSite{
							name:      dir + "." + vs.Names[i].Name,
							framework: true,
						}, value)
					}
				}
			}
		}
	}

	declared = make([]string, 0, len(names))
	for name := range names {
		declared = append(declared, name)
	}
	sort.Strings(declared)
	for name := range emitters {
		sites := emitters[name]
		sort.Slice(sites, func(a, b int) bool { return sites[a].name < sites[b].name })
	}
	return declared, emitters
}

// testFileScope states whether a walk admits _test.go files.
//
// It is a named type rather than a bare bool so the choice reads at the call
// site. The populations this package walks differ in exactly this one respect,
// and a literal true or false in the argument list would say which population is
// meant only to a reader who first opens [collectGoFiles].
type testFileScope bool

const (
	// excludeTestFiles is the sentinel census's scope. A sentinel declared in a
	// test is scaffolding rather than a rule this module raises, which is the
	// criterion recorded beside [sentinelEmissionBlocks].
	excludeTestFiles testFileScope = false

	// includeTestFiles is the code-literal guard's scope. That guard reads a
	// match written against a registered code wherever it appears, and a test is
	// where one is most likely to be written: asserting on the code string is
	// the readiest way to reach for the wire identifier from Go.
	includeTestFiles testFileScope = true
)

// collectGoFiles parses every file the criterion admits under roots, keyed by
// path, and returns the file set those positions are recorded in so a caller can
// report a finding by line.
//
// It is one half of the criterion's single home — the scope half. Both the
// asserted-population read and the module-wide coverage read pass the identical
// scope, so neither of them can acquire a scope rule the other lacks; a caller
// whose population is genuinely different states that difference in the
// argument, where it is visible, rather than by walking the tree a second time.
//
// A file that will not parse is skipped rather than fatal, because a census is
// not a compiler and a tree mid-edit should not turn an unrelated assertion red.
func collectGoFiles(t *testing.T, roots []string, tests testFileScope) (map[string]*ast.File, *token.FileSet) {
	t.Helper()

	fset := token.NewFileSet()
	files := map[string]*ast.File{}
	for _, root := range roots {
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			switch {
			case err != nil:
				return err
			case d.IsDir():
				if skipDir(path, root, d.Name()) {
					return fs.SkipDir
				}
				return nil
			case !strings.HasSuffix(path, ".go"):
				return nil
			case tests == excludeTestFiles && strings.HasSuffix(path, "_test.go"):
				return nil
			}
			parsed, perr := parser.ParseFile(fset, path, nil, 0)
			if perr != nil {
				return nil // a file this census cannot parse is skipped, not fatal
			}
			files[path] = parsed
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", root, err)
		}
	}
	if len(files) == 0 {
		t.Fatalf("no Go file was read under %s; the census instrument is not working",
			strings.Join(roots, ", "))
	}
	return files, fset
}

// skipDir reports whether the walk stops at a directory. The exclusions are the
// scope half of the census criterion and each is documented with its reason
// beside [sentinelEmissionBlocks]; a root is never skipped for its own sake,
// which is what keeps a root spelled with a leading ".." from matching the
// dot-prefix rule and emptying the walk.
func skipDir(path, root, name string) bool {
	if path == root {
		return false
	}
	if name == "testdata" || name == "vendor" ||
		strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_") {
		return true
	}
	_, err := os.Stat(filepath.Join(path, "go.mod"))
	return err == nil
}

// declaredSentinels returns each declared sentinel name and one path declaring
// it. It is the other half of the criterion's single home — the name half — so
// the asserted population and the module-wide coverage read cannot drift into two
// criteria that happen to agree today.
//
// Both declaration shapes are read: the exported accessor that publishes an
// identity, and a var or const bound to one. A walk reading only the second
// would report a population of the handful of sentinels still declared as values
// and say nothing about the rest — and it would report it as a clean count
// rather than as the blindness it is, which is exactly the reading a population
// count must never produce.
//
// Where a name is declared more than once the lexicographically first path wins,
// so a reported path is the same on every run rather than whichever the map
// handed back first.
//
// A second recogniser of the same declaration shapes lives in the published-pages
// parity assertion under cmd/aggrega, and the two are deliberately NOT unified.
// This one parses the syntax tree; that one scans source text. They fail in
// different directions, which is the whole value: a text scan reads a shape this
// walk cannot express, and this walk reads a shape no pattern can bound. Unifying
// them would replace two independent readings of one population with a single
// point of blindness, so a criterion change must be carried to both sites by hand
// — which is the cost this duplication is knowingly paying.
func declaredSentinels(files map[string]*ast.File) map[string]string {
	names := map[string]string{}
	record := func(name, path string) {
		if !sentinelName.MatchString(name) {
			return
		}
		if prior, seen := names[name]; !seen || path < prior {
			names[name] = path
		}
	}
	for path, file := range files {
		for _, decl := range file.Decls {
			switch d := decl.(type) {
			case *ast.FuncDecl:
				// A method is reached through a value rather than through the
				// package, so its name is not one a consumer matches against.
				if d.Recv != nil {
					continue
				}
				record(d.Name.Name, path)
			case *ast.GenDecl:
				if d.Tok != token.VAR && d.Tok != token.CONST {
					continue
				}
				for _, spec := range d.Specs {
					vs, ok := spec.(*ast.ValueSpec)
					if !ok {
						continue
					}
					for _, id := range vs.Names {
						record(id.Name, path)
					}
				}
			}
		}
	}
	return names
}

// recordNamed attributes site to every declared sentinel the subtree names.
func recordNamed(emitters map[string][]emissionSite, names map[string]string, site emissionSite, subtree ast.Node) {
	named := map[string]bool{}
	ast.Inspect(subtree, func(node ast.Node) bool {
		switch x := node.(type) {
		case *ast.Ident:
			if _, ok := names[x.Name]; ok {
				named[x.Name] = true
			}
		case *ast.SelectorExpr:
			if _, ok := names[x.Sel.Name]; ok {
				named[x.Sel.Name] = true
			}
		}
		return true
	})
	for name := range named {
		emitters[name] = append(emitters[name], site)
	}
}

// namedWithSites renders each sentinel beside the sites naming it, so a reader
// can reach the declaration an attribution came from. A check declaration's site
// carries the variable's own name, which is what a reader greps for; a declared
// function's carries the function name.
func namedWithSites(sentinels []string, emitters map[string][]emissionSite) []string {
	out := make([]string, 0, len(sentinels))
	for _, sentinel := range sentinels {
		sites := make([]string, 0, len(emitters[sentinel]))
		for _, site := range emitters[sentinel] {
			sites = append(sites, site.name)
		}
		out = append(out, sentinel+" at "+strings.Join(sites, " and "))
	}
	return out
}

// isCheckValue reports whether expr constructs a [Check].
func isCheckValue(expr ast.Expr) bool {
	lit, ok := expr.(*ast.CompositeLit)
	return ok && constructedTypeName(lit.Type) == checkTypeName
}

// constructedTypeName names the type a composite literal constructs, unwrapping
// a generic instantiation and a package qualifier. It reads syntax alone, so it
// answers for the framework's own unqualified spelling and for a consuming
// package's qualified one without either being enumerated.
func constructedTypeName(expr ast.Expr) string {
	switch x := expr.(type) {
	case *ast.IndexExpr: // instantiated with one type argument
		return constructedTypeName(x.X)
	case *ast.IndexListExpr: // instantiated with two or more
		return constructedTypeName(x.X)
	case *ast.SelectorExpr:
		return x.Sel.Name
	case *ast.Ident:
		return x.Name
	}
	return ""
}

// sortedNames returns ss in alphabetical order, leaving ss itself untouched, so
// a report names the same entries in the same order on every run.
//
// A report names the whole of a bucket whose every entry is individually
// actionable and caps one that is not; [head] is the capped form, and a capped
// list says so in the sentence introducing it. The two are chosen deliberately,
// because a list whose introduction states a larger count than the list contains
// reads as a discrepancy in the instrument rather than as a cap.
func sortedNames(ss []string) []string {
	sorted := append([]string(nil), ss...)
	sort.Strings(sorted)
	return sorted
}

// head returns the first n of [sortedNames], for a bucket too large to name
// entry by entry.
func head(ss []string, n int) []string {
	sorted := sortedNames(ss)
	if len(sorted) <= n {
		return sorted
	}
	return sorted[:n]
}
