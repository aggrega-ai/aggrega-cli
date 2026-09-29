// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package constraint

import (
	"go/ast"
	"go/parser"
	"go/token"
	"sort"
	"strings"
	"testing"
)

// The attribution-by-reference rule, proven both ways over synthetic source.
//
// The census attributes a package-level function or value to the dispatch when
// only checks reach it, and [indexHostReach] records how it reads that without
// resolving a type. A census is only as good as the zero it reports, so each row
// below that keeps a host OUTSIDE is paired with a row that moves it INSIDE and
// differs from it in the one property under test. A rule that moved nothing
// would fail every leaving row, and a rule that moved everything would fail every
// staying row, so neither reading of the rule can pass this table.
//
// The rows are driven through [censusOver] — the census itself over a file set it
// is handed — rather than through the index alone, so what they prove is the
// verdict a site actually carries, by the code the census runs. The source is
// parsed from memory and never read from the tree; it need not compile, because
// the census reads syntax and nothing here type-checks.

const (
	reachRoot       = "m"
	reachModulePath = "example.test/m"
)

// The shared synthetic files. Package a declares the condition every row raises,
// a helper raising it, a check calling that helper, and a plain function calling
// it; each row composes the ones its scenario needs and replaces the one its
// property changes.
const (
	reachErrors = `package a

import "errors"

func ErrThing() error { return errThing }

var errThing = errors.New("thing")
`

	reachHelper = `package a

import "example.test/m/internal/compiler/constraint"

func helper(s constraint.NodeSubject) []constraint.Objection {
	return []constraint.Objection{s.Raise(ErrThing())}
}
`

	reachCheckCallsHelper = `package a

import "example.test/m/internal/compiler/constraint"

var thingCheck = constraint.Check[constraint.NodeView, constraint.NodeSubject]{
	Sentinel: ErrThing(),
	Run: func(v constraint.NodeView, s constraint.NodeSubject) []constraint.Objection {
		return helper(s)
	},
}
`

	reachPlainCallsHelper = `package a

import "example.test/m/internal/compiler/constraint"

func plainCaller(s constraint.NodeSubject) []constraint.Objection {
	return helper(s)
}
`

	// The cross-package shape: package lib holds an exported helper raising the
	// condition package a declares, and package b's check reaches it through an
	// aliased import.
	reachLibHelper = `package lib

import (
	"example.test/m/internal/a"
	"example.test/m/internal/compiler/constraint"
)

func Helper(s constraint.NodeSubject) []constraint.Objection {
	return []constraint.Objection{s.Raise(a.ErrThing())}
}
`

	reachAliasedCheck = `package b

import (
	"example.test/m/internal/a"
	"example.test/m/internal/compiler/constraint"
	thinglib "example.test/m/internal/lib"
)

var thingCheck = constraint.Check[constraint.NodeView, constraint.NodeSubject]{
	Sentinel: a.ErrThing(),
	Run: func(v constraint.NodeView, s constraint.NodeSubject) []constraint.Objection {
		return thinglib.Helper(s)
	},
}
`
)

// reachRow is one scenario: the synthetic module, the site whose verdict it
// pins, and that verdict. reason is a fragment the site's reachedFrom (for a site
// in the dispatch) or heldBy (for one outside it) must carry; an empty reason on
// an outside row means the rule must record nothing for the site at all, because
// no check reaches it. pairedWith names, on an outside row, the leaving row that
// differs from it in the one property under test.
type reachRow struct {
	name       string
	files      map[string]string
	site       string
	framework  bool
	reason     string
	pairedWith string
}

func TestSentinelEmissionAttributesAHostByWhatReachesIt(t *testing.T) {
	t.Parallel()

	const (
		helperSite    = "m/internal/a.helper"
		innerSite     = "m/internal/a.inner"
		valueSite     = "m/internal/a.thingDiagnostic"
		libHelperSite = "m/internal/lib.Helper"
		pkgHelperSite = "m/pkg/lib.Helper"
	)

	transitiveHelpers := `package a

import "example.test/m/internal/compiler/constraint"

func outer(s constraint.NodeSubject) []constraint.Objection {
	return inner(s)
}

func inner(s constraint.NodeSubject) []constraint.Objection {
	return []constraint.Objection{s.Raise(ErrThing())}
}
`
	checkCallsOuter := strings.Replace(reachCheckCallsHelper, "return helper(s)", "return outer(s)", 1)
	plainCallsOuter := strings.Replace(reachPlainCallsHelper, "return helper(s)", "return outer(s)", 1)

	methodHelper := `package a

import "example.test/m/internal/compiler/constraint"

type holder struct{}

func (holder) helper(s constraint.NodeSubject) []constraint.Objection {
	return []constraint.Objection{s.Raise(ErrThing())}
}
`
	checkCallsMethod := strings.Replace(reachCheckCallsHelper, "return helper(s)", "return holder{}.helper(s)", 1)

	checkRaisesDirectly := strings.Replace(reachCheckCallsHelper, "return helper(s)",
		"return []constraint.Objection{s.Raise(ErrThing())}", 1)

	predicateHelper := `package a

import "example.test/m/internal/compiler/constraint"

func helper(v constraint.NodeView, s constraint.NodeSubject) []constraint.Objection {
	return []constraint.Objection{s.Raise(ErrThing())}
}
`
	checkTakesHelperAsValue := `package a

import "example.test/m/internal/compiler/constraint"

var thingCheck = constraint.Check[constraint.NodeView, constraint.NodeSubject]{
	Sentinel: ErrThing(),
	Run:      helper,
}
`
	plainTakesHelperAsValue := `package a

func table() any {
	return helper
}
`

	genericHelper := `package a

import "example.test/m/internal/compiler/constraint"

func helper[S constraint.Subject](s S) []constraint.Objection {
	return []constraint.Objection{s.Raise(ErrThing())}
}
`
	checkInstantiatesHelper := strings.Replace(reachCheckCallsHelper, "return helper(s)",
		"return helper[constraint.NodeSubject](s)", 1)
	plainInstantiatesHelper := strings.Replace(reachPlainCallsHelper, "return helper(s)",
		"return helper[constraint.NodeSubject](s)", 1)

	plainCallsThroughAlias := `package b

import (
	"example.test/m/internal/compiler/constraint"
	thinglib "example.test/m/internal/lib"
)

func plainCaller(s constraint.NodeSubject) []constraint.Objection {
	return thinglib.Helper(s)
}
`
	dotImportsLib := `package c

import . "example.test/m/internal/lib"
`
	namedImportsLib := `package c

import thinglib "example.test/m/internal/lib"
`
	importableCheck := strings.Replace(reachAliasedCheck, `"example.test/m/internal/lib"`,
		`"example.test/m/pkg/lib"`, 1)

	frameworkCallsLib := `package constraint

import thinglib "example.test/m/internal/lib"

func runAll(s NodeSubject) []Objection {
	return thinglib.Helper(s)
}
`

	recursiveHelper := `package a

import "example.test/m/internal/compiler/constraint"

func helper(s constraint.NodeSubject) []constraint.Objection {
	if s == nil {
		return helper(s)
	}
	return []constraint.Objection{s.Raise(ErrThing())}
}
`

	plainShadowsHelper := `package a

func plainCaller() int {
	helper := 1
	return helper
}
`
	plainSpellsOtherwise := strings.ReplaceAll(plainShadowsHelper, "helper", "count")

	valueHost := `package a

var thingDiagnostic = ErrThing()
`
	checkReadsValue := strings.Replace(reachCheckCallsHelper, "return helper(s)",
		"return []constraint.Objection{s.Raise(thingDiagnostic)}", 1)
	plainReadsValue := `package a

func plainCaller() error {
	return thingDiagnostic
}
`

	rows := []reachRow{
		{
			name: "a helper reached only from a check literal's predicate is the dispatch",
			files: map[string]string{
				"m/internal/a/errors.go": reachErrors,
				"m/internal/a/helper.go": reachHelper,
				"m/internal/a/check.go":  reachCheckCallsHelper,
			},
			site: helperSite, framework: true, reason: "check.go:8, in the check thingCheck",
		},
		{
			name: "the same helper also called from a plain function stays outside, named by that call",
			files: map[string]string{
				"m/internal/a/errors.go": reachErrors,
				"m/internal/a/helper.go": reachHelper,
				"m/internal/a/check.go":  reachCheckCallsHelper,
				"m/internal/a/plain.go":  reachPlainCallsHelper,
			},
			site: helperSite, framework: false, reason: "plain.go:6, in the body of plainCaller",
			pairedWith: "a helper reached only from a check literal's predicate is the dispatch",
		},
		{
			name: "a helper reached only through a check-only helper is the dispatch",
			files: map[string]string{
				"m/internal/a/errors.go": reachErrors,
				"m/internal/a/helper.go": transitiveHelpers,
				"m/internal/a/check.go":  checkCallsOuter,
			},
			site: innerSite, framework: true, reason: "helper.go:6, in the body of outer",
		},
		{
			name: "a helper reached through a helper a plain function also calls stays outside",
			files: map[string]string{
				"m/internal/a/errors.go": reachErrors,
				"m/internal/a/helper.go": transitiveHelpers,
				"m/internal/a/check.go":  checkCallsOuter,
				"m/internal/a/plain.go":  plainCallsOuter,
			},
			site: innerSite, framework: false, reason: "helper.go:6, in the body of outer",
			pairedWith: "a helper reached only through a check-only helper is the dispatch",
		},
		{
			name: "a method reached only from a check literal stays outside, because a method is never a host",
			files: map[string]string{
				"m/internal/a/errors.go": reachErrors,
				"m/internal/a/helper.go": methodHelper,
				"m/internal/a/check.go":  checkCallsMethod,
			},
			site: helperSite, framework: false, reason: "",
			pairedWith: "a helper reached only from a check literal's predicate is the dispatch",
		},
		{
			name: "a helper nothing references stays outside",
			files: map[string]string{
				"m/internal/a/errors.go": reachErrors,
				"m/internal/a/helper.go": reachHelper,
				"m/internal/a/check.go":  checkRaisesDirectly,
			},
			site: helperSite, framework: false, reason: "",
			pairedWith: "a helper reached only from a check literal's predicate is the dispatch",
		},
		{
			name: "a function taken as a check's predicate value is the dispatch",
			files: map[string]string{
				"m/internal/a/errors.go": reachErrors,
				"m/internal/a/helper.go": predicateHelper,
				"m/internal/a/check.go":  checkTakesHelperAsValue,
			},
			site: helperSite, framework: true, reason: "check.go:7, in the check thingCheck",
		},
		{
			name: "the same predicate function also taken as a value by a plain function stays outside",
			files: map[string]string{
				"m/internal/a/errors.go": reachErrors,
				"m/internal/a/helper.go": predicateHelper,
				"m/internal/a/check.go":  checkTakesHelperAsValue,
				"m/internal/a/plain.go":  plainTakesHelperAsValue,
			},
			site: helperSite, framework: false, reason: "plain.go:4, in the body of table",
			pairedWith: "a function taken as a check's predicate value is the dispatch",
		},
		{
			name: "a generic helper instantiated from a check literal is the dispatch",
			files: map[string]string{
				"m/internal/a/errors.go": reachErrors,
				"m/internal/a/helper.go": genericHelper,
				"m/internal/a/check.go":  checkInstantiatesHelper,
			},
			site: helperSite, framework: true, reason: "check.go:8, in the check thingCheck",
		},
		{
			name: "the same generic helper also instantiated from a plain function stays outside",
			files: map[string]string{
				"m/internal/a/errors.go": reachErrors,
				"m/internal/a/helper.go": genericHelper,
				"m/internal/a/check.go":  checkInstantiatesHelper,
				"m/internal/a/plain.go":  plainInstantiatesHelper,
			},
			site: helperSite, framework: false, reason: "plain.go:6, in the body of plainCaller",
			pairedWith: "a generic helper instantiated from a check literal is the dispatch",
		},
		{
			name: "a helper reached through an aliased import from another package's check is the dispatch",
			files: map[string]string{
				"m/internal/a/errors.go": reachErrors,
				"m/internal/lib/lib.go":  reachLibHelper,
				"m/internal/b/check.go":  reachAliasedCheck,
			},
			site: libHelperSite, framework: true, reason: "m/internal/b/check.go:12, in the check thingCheck",
		},
		{
			name: "the same helper also called through the alias from a plain function in that package stays outside",
			files: map[string]string{
				"m/internal/a/errors.go": reachErrors,
				"m/internal/lib/lib.go":  reachLibHelper,
				"m/internal/b/check.go":  reachAliasedCheck,
				"m/internal/b/plain.go":  plainCallsThroughAlias,
			},
			site: libHelperSite, framework: false, reason: "m/internal/b/plain.go:9, in the body of plainCaller",
			pairedWith: "a helper reached through an aliased import from another package's check is the dispatch",
		},
		{
			name: "a helper whose package another file imports by name is still the dispatch",
			files: map[string]string{
				"m/internal/a/errors.go": reachErrors,
				"m/internal/lib/lib.go":  reachLibHelper,
				"m/internal/b/check.go":  reachAliasedCheck,
				"m/internal/c/c.go":      namedImportsLib,
			},
			site: libHelperSite, framework: true, reason: "m/internal/b/check.go:12, in the check thingCheck",
		},
		{
			name: "a helper whose package another file dot-imports stays outside",
			files: map[string]string{
				"m/internal/a/errors.go": reachErrors,
				"m/internal/lib/lib.go":  reachLibHelper,
				"m/internal/b/check.go":  reachAliasedCheck,
				"m/internal/c/c.go":      dotImportsLib,
			},
			site: libHelperSite, framework: false, reason: "the dot import of its package at m/internal/c/c.go:3",
			pairedWith: "a helper whose package another file imports by name is still the dispatch",
		},
		{
			name: "an exported helper of a package another module can import stays outside",
			files: map[string]string{
				"m/internal/a/errors.go": reachErrors,
				"m/pkg/lib/lib.go":       reachLibHelper,
				"m/internal/b/check.go":  importableCheck,
			},
			site: pkgHelperSite, framework: false, reason: "its export from a package another module can import",
			pairedWith: "a helper reached through an aliased import from another package's check is the dispatch",
		},
		{
			name: "a helper a plain function in the framework's own package calls stays outside",
			files: map[string]string{
				"m/internal/a/errors.go":                reachErrors,
				"m/internal/lib/lib.go":                 reachLibHelper,
				"m/internal/b/check.go":                 reachAliasedCheck,
				"m/internal/compiler/constraint/run.go": frameworkCallsLib,
			},
			site: libHelperSite, framework: false,
			reason:     "m/internal/compiler/constraint/run.go:6, in the body of runAll",
			pairedWith: "a helper reached through an aliased import from another package's check is the dispatch",
		},
		{
			name: "a helper that calls itself stays outside",
			files: map[string]string{
				"m/internal/a/errors.go": reachErrors,
				"m/internal/a/helper.go": recursiveHelper,
				"m/internal/a/check.go":  reachCheckCallsHelper,
			},
			site: helperSite, framework: false, reason: "helper.go:7, in the body of helper, which is its own",
			pairedWith: "a helper reached only from a check literal's predicate is the dispatch",
		},
		{
			name: "a local of a plain function spelled otherwise leaves the helper in the dispatch",
			files: map[string]string{
				"m/internal/a/errors.go": reachErrors,
				"m/internal/a/helper.go": reachHelper,
				"m/internal/a/check.go":  reachCheckCallsHelper,
				"m/internal/a/plain.go":  plainSpellsOtherwise,
			},
			site: helperSite, framework: true, reason: "check.go:8, in the check thingCheck",
		},
		{
			name: "a local of a plain function spelling the helper's name holds it outside",
			files: map[string]string{
				"m/internal/a/errors.go": reachErrors,
				"m/internal/a/helper.go": reachHelper,
				"m/internal/a/check.go":  reachCheckCallsHelper,
				"m/internal/a/plain.go":  plainShadowsHelper,
			},
			site: helperSite, framework: false, reason: "plain.go:4, in the body of plainCaller",
			pairedWith: "a local of a plain function spelled otherwise leaves the helper in the dispatch",
		},
		{
			name: "a value read only from a check literal is the dispatch",
			files: map[string]string{
				"m/internal/a/errors.go": reachErrors,
				"m/internal/a/value.go":  valueHost,
				"m/internal/a/check.go":  checkReadsValue,
			},
			site: valueSite, framework: true, reason: "check.go:8, in the check thingCheck",
		},
		{
			name: "the same value also read by a plain function stays outside",
			files: map[string]string{
				"m/internal/a/errors.go": reachErrors,
				"m/internal/a/value.go":  valueHost,
				"m/internal/a/check.go":  checkReadsValue,
				"m/internal/a/plain.go":  plainReadsValue,
			},
			site: valueSite, framework: false, reason: "plain.go:4, in the body of plainCaller",
			pairedWith: "a value read only from a check literal is the dispatch",
		},
	}

	// The pairing is asserted rather than described: an outside row naming no
	// leaving row, or one naming a row that does not leave, is a row a rule that
	// moves nothing would pass.
	leaving := map[string]bool{}
	for _, row := range rows {
		if row.framework {
			leaving[row.name] = true
		}
	}
	for _, row := range rows {
		if !row.framework && !leaving[row.pairedWith] {
			t.Errorf("row %q keeps its host outside and is paired with %q, which is not a row that moves a host "+
				"into the dispatch. Every staying row needs a leaving row that differs from it in the one "+
				"property under test, or a rule that reattributes nothing passes it.", row.name, row.pairedWith)
		}
	}

	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()

			files, fset := parseSyntheticModule(t, row.files)
			_, _, emitters, _, _, _ := censusOver(t, files, fset, reachRoot, reachModulePath)

			var found []emissionSite
			for _, site := range emitters["ErrThing"] {
				if site.name == row.site {
					found = append(found, site)
				}
			}
			if len(found) != 1 {
				t.Fatalf("the census attributes ErrThing to %d site(s) named %s, and the row needs exactly one; "+
					"it attributes it to: %s", len(found), row.site, describeSites(siteNames(emitters["ErrThing"])))
			}
			site := found[0]

			if site.framework != row.framework {
				t.Errorf("%s is attributed with framework=%v, want %v (reached from %q, held by %q)",
					row.site, site.framework, row.framework, site.reachedFrom, site.heldBy)
			}
			recorded, other := site.heldBy, site.reachedFrom
			if row.framework {
				recorded, other = site.reachedFrom, site.heldBy
			}
			if other != "" {
				t.Errorf("%s carries both a reach and a hold (reached from %q, held by %q); a site is one or the "+
					"other", row.site, site.reachedFrom, site.heldBy)
			}
			switch {
			case row.reason == "" && recorded != "":
				t.Errorf("%s records %q, and no check reaches this host, so the rule should record nothing",
					row.site, recorded)
			case row.reason != "" && !strings.Contains(recorded, row.reason):
				t.Errorf("%s records %q, which does not name %q", row.site, recorded, row.reason)
			}
		})
	}
}

// parseSyntheticModule parses in-memory sources keyed by the module-relative
// path each is filed under, so a row's module is exactly the text it spells and
// nothing on disk.
func parseSyntheticModule(t *testing.T, sources map[string]string) (map[string]*ast.File, *token.FileSet) {
	t.Helper()

	paths := make([]string, 0, len(sources))
	for path := range sources {
		paths = append(paths, path)
	}
	sort.Strings(paths)

	fset := token.NewFileSet()
	files := map[string]*ast.File{}
	for _, path := range paths {
		file, err := parser.ParseFile(fset, path, sources[path], 0)
		if err != nil {
			t.Fatalf("the synthetic source %s does not parse, so the row cannot say anything: %v", path, err)
		}
		files[path] = file
	}
	return files, fset
}

// siteNames returns the names of sites, for a report naming where the census
// attributed a sentinel instead.
func siteNames(sites []emissionSite) []string {
	out := make([]string, 0, len(sites))
	for _, site := range sites {
		out = append(out, site.name)
	}
	return out
}
