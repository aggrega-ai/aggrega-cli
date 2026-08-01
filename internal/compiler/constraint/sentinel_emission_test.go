// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package constraint

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
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
// Reporting is not nothing: the counts appear in every test run, so the
// off-framework population is visible and its direction of travel is legible
// while the migration is under way.
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
const sentinelEmissionBlocks = false

// sentinelName matches the declaration convention for a sentinel: an exported
// package-level error variable named for the rule it stands for.
var sentinelName = regexp.MustCompile(`^Err[A-Z][A-Za-z0-9]*$`)

// scannedRoots are the subtrees the census reads, relative to this package.
var scannedRoots = []string{"../../../pkg", "../../../internal"}

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
			"minted for a check that was never written.", len(unemitted), strings.Join(head(unemitted, 12), ", "))
	}

	if !sentinelEmissionBlocks {
		t.Logf("this assertion reports and does not block. It becomes blocking when the last emission "+
			"path outside the framework's dispatch is removed; until then it would fail on the shipped "+
			"tree on every run. Off-framework population at this run: %d of %d sentinels.",
			len(offFramework), len(declared))
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

	fset := token.NewFileSet()
	files := map[string]*ast.File{}
	for _, root := range scannedRoots {
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			switch {
			case err != nil:
				return err
			case d.IsDir():
				if d.Name() == "testdata" {
					return fs.SkipDir
				}
				return nil
			case !strings.HasSuffix(path, ".go"), strings.HasSuffix(path, "_test.go"):
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
			strings.Join(scannedRoots, ", "))
	}

	names := map[string]bool{}
	for _, file := range files {
		for _, decl := range file.Decls {
			gen, ok := decl.(*ast.GenDecl)
			if !ok || gen.Tok != token.VAR {
				continue
			}
			for _, spec := range gen.Specs {
				vs, ok := spec.(*ast.ValueSpec)
				if !ok {
					continue
				}
				for _, id := range vs.Names {
					if sentinelName.MatchString(id.Name) {
						names[id.Name] = true
					}
				}
			}
		}
	}

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

// recordNamed attributes site to every declared sentinel the subtree names.
func recordNamed(emitters map[string][]emissionSite, names map[string]bool, site emissionSite, subtree ast.Node) {
	named := map[string]bool{}
	ast.Inspect(subtree, func(node ast.Node) bool {
		switch x := node.(type) {
		case *ast.Ident:
			if names[x.Name] {
				named[x.Name] = true
			}
		case *ast.SelectorExpr:
			if names[x.Sel.Name] {
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

func head(ss []string, n int) []string {
	sorted := append([]string(nil), ss...)
	sort.Strings(sorted)
	if len(sorted) <= n {
		return sorted
	}
	return sorted[:n]
}
