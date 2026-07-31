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
// framework. It derives its data from the code — the declarations and the
// function bodies — and never from a hand-maintained inventory: that inventory
// marks its own coordinates approximate and asks to be verified against the code,
// so it disclaims exactly the precision an assertion needs. It is a useful shape
// template for which fields matter and an unusable source for what their values
// are.
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
const sentinelEmissionBlocks = false

// sentinelName matches the declaration convention for a sentinel: an exported
// package-level error variable named for the rule it stands for.
var sentinelName = regexp.MustCompile(`^Err[A-Z][A-Za-z0-9]*$`)

// scannedRoots are the subtrees the census reads, relative to this package.
var scannedRoots = []string{"../../../pkg", "../../../internal"}

// frameworkPackageSuffix identifies the framework's own dispatch: a function
// declared here is part of it, and every other function naming a sentinel is not.
const frameworkPackageSuffix = "internal/compiler/constraint"

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
			if !strings.Contains(site, frameworkPackageSuffix) {
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

	if len(offFramework) > 0 {
		report("%d sentinel(s) are emitted outside the framework's dispatch.\n"+
			"INVARIANT: a sentinel's emission sites are exactly the framework's dispatch, because that is "+
			"what makes a check written outside the framework detectable — reusing a sentinel shows up as "+
			"a second emission site, and minting one shows up as a sentinel no registered check emits.\n"+
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

// sentinelCensus returns the declared sentinel names, sorted, and the functions
// naming each one.
//
// The emission criterion is the one the covering measurement used: a non-test
// function whose body names a sentinel identifier. It counts a function rather
// than an occurrence, so a function raising one sentinel twice is one site, and
// it is an upper bound on checks — a function that wraps a sentinel while
// re-raising it counts as naming it.
func sentinelCensus(t *testing.T) (declared []string, emitters map[string][]string) {
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

	emitters = map[string][]string{}
	for path, file := range files {
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			site := filepath.ToSlash(filepath.Dir(path)) + "." + fn.Name.Name
			named := map[string]bool{}
			ast.Inspect(fn.Body, func(node ast.Node) bool {
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
	}

	declared = make([]string, 0, len(names))
	for name := range names {
		declared = append(declared, name)
	}
	sort.Strings(declared)
	for name := range emitters {
		sort.Strings(emitters[name])
	}
	return declared, emitters
}

func head(ss []string, n int) []string {
	sorted := append([]string(nil), ss...)
	sort.Strings(sorted)
	if len(sorted) <= n {
		return sorted
	}
	return sorted[:n]
}
