// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"reflect"
	"strings"
	"testing"
)

// TestACallIsAttributedToItsContainingFunction asserts the property the reach
// derivation rests on: a call belongs to the function whose body encloses it,
// including a call inside a function literal that function passes onward.
//
// Attribution by the nearest declaration above a call would agree with this on
// tidy source and disagree silently on source that is not, which is why the
// property is asserted rather than assumed.
func TestACallIsAttributedToItsContainingFunction(t *testing.T) {
	t.Parallel()

	const src = `package p

import "x/other"

func outer(opts Options) error {
	before()
	return other.Wrap(func() error {
		inside()
		if opts.Skip {
			return nil
		}
		after()
		return nil
	})
}

func sibling() { siblingOnly() }
`
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "d/f.go", src, 0)
	if err != nil {
		t.Fatalf("parse the fixture: %v", err)
	}
	scope := map[string]string{"other": "vendorpkg"}

	got := map[string][]string{}
	cuts := map[string][]string{}
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		node := &funcNode{Dir: "d", Name: fn.Name.Name}
		collectCalls(fn.Body, "d", scope, parameterNames(fn), map[string]bool{}, node)
		for _, e := range node.Calls {
			got[fn.Name.Name] = append(got[fn.Name.Name], e.Target)
		}
		for _, cut := range node.Cuts {
			cuts[fn.Name.Name] = append(cuts[fn.Name.Name], cut.Field)
		}
	}

	wantCalls := map[string][]string{
		// The literal's own calls belong to outer: a continuation a function
		// passes is still that function's own code.
		"outer":   {"d::before", "vendorpkg::Wrap", "d::inside", "d::after"},
		"sibling": {"d::siblingOnly"},
	}
	if !reflect.DeepEqual(got, wantCalls) {
		t.Errorf("attributed calls:\n got %+v\nwant %+v", got, wantCalls)
	}

	wantCuts := map[string][]string{"outer": {"Skip"}}
	if !reflect.DeepEqual(cuts, wantCuts) {
		t.Errorf("caller-controlled cuts:\n got %+v\nwant %+v", cuts, wantCuts)
	}
}

// TestACallThroughABoundNameNamesTheFunctionTheBodyNames asserts the reading a
// surface's attribution rests on when the body holds its operation in a variable:
// the call is attributed to the function the same body binds that name to.
//
// The negative rows are what keep the reading from becoming "find a call
// everywhere". A name the body also binds to something this read cannot name
// carries an unresolved edge beside the function it names, so the reach stays a
// lower bound instead of resting on the one binding that was legible; a name the
// caller supplied is followed nowhere at all; and a name holding no function is
// not a binding, so the call through it resolves exactly as any bare call does.
func TestACallThroughABoundNameNamesTheFunctionTheBodyNames(t *testing.T) {
	t.Parallel()

	type edge struct {
		target   string
		viaValue bool
	}

	const src = `package p

import "x/other"

func heldInOneName() error {
	run := other.Entry
	return run()
}

func heldBesideASeam(s *server) error {
	run := s.installed
	if run == nil {
		run = other.Entry
	}
	return run()
}

func heldBesideALiteral() error {
	run := other.Entry
	run = func() error { return nil }
	return run()
}

func declaredThenBound() error {
	var run func() error
	run = other.Entry
	return run()
}

func suppliedByTheCaller(run func() error) error {
	return run()
}

func holdingNoFunction() error {
	count := 2
	_ = count
	return sibling()
}
`
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "d/f.go", src, 0)
	if err != nil {
		t.Fatalf("parse the fixture: %v", err)
	}
	scope := map[string]string{"other": "vendorpkg"}

	got := map[string][]edge{}
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		node := &funcNode{Dir: "d", Name: fn.Name.Name}
		collectCalls(fn.Body, "d", scope, parameterNames(fn), map[string]bool{}, node)
		for _, e := range node.Calls {
			got[fn.Name.Name] = append(got[fn.Name.Name], edge{target: e.Target, viaValue: e.ViaValue})
		}
	}

	want := map[string][]edge{
		"heldInOneName": {{target: "vendorpkg::Entry", viaValue: true}},
		// The seam the body installs is a field this read cannot name, so the call
		// carries it as unresolved rather than claiming the named function is the
		// only thing that can run.
		"heldBesideASeam":    {{target: "vendorpkg::Entry", viaValue: true}, {}},
		"heldBesideALiteral": {{target: "vendorpkg::Entry", viaValue: true}, {}},
		"declaredThenBound":  {{target: "vendorpkg::Entry", viaValue: true}},
		// A parameter holds whatever the caller passed. Reading it as a call into a
		// same-package declaration of that name would attribute the reach of a
		// function the parameter merely shadows.
		"suppliedByTheCaller": {{}},
		// A name holding no function is no binding at all, so the bare call beside
		// it resolves into the calling file's own package as any other does.
		"holdingNoFunction": {{target: "d::sibling"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("attributed calls:\n got %+v\nwant %+v", got, want)
	}
}

// TestACutIsDistinguishedFromEveryOtherBranch asserts the two halves that make an
// `if` a cut a caller controls: the condition reads a field of a value the caller
// supplied, and the body returns.
//
// The negative rows are the point. A fault path and an optional step are both
// conditions the code decides, and reading either as a cut would report a stage as
// option-dependent when no option decides it.
func TestACutIsDistinguishedFromEveryOtherBranch(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		src  string
		want []string
	}{
		{
			name: "a parameter field guarding a return is a cut",
			src:  "func f(opts O) error { if opts.SkipRender { return nil }; more(); return nil }",
			want: []string{"SkipRender"},
		},
		{
			name: "a receiver field guarding a return is a cut, because a method's receiver is supplied too",
			src:  "func (s *S) f() error { if s.Disabled { return nil }; more(); return nil }",
			want: []string{"Disabled"},
		},
		{
			name: "a parameter field whose body does not return is a branch, not a cut",
			src:  "func f(in I) error { if in.Tolerate { relax() }; more(); return nil }",
			want: nil,
		},
		{
			name: "an error test is not a cut, however it returns",
			src:  "func f(in I) error { if err := g(); err != nil { return err }; return nil }",
			want: nil,
		},
		{
			name: "a nil comparison on a parameter field is not a cut",
			src:  "func f(opts O) error { if opts.Verifier != nil { return opts.Verifier() }; return nil }",
			want: nil,
		},
		{
			name: "a field of a local is not a cut, because no caller supplied it",
			src:  "func f() error { local := compute(); if local.Done { return nil }; more(); return nil }",
			want: nil,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			fset := token.NewFileSet()
			file, err := parser.ParseFile(fset, "d/f.go", "package p\n"+tc.src+"\n", 0)
			if err != nil {
				t.Fatalf("parse the fixture: %v", err)
			}
			fn := file.Decls[0].(*ast.FuncDecl)
			node := &funcNode{Dir: "d", Name: fn.Name.Name}
			collectCalls(fn.Body, "d", nil, parameterNames(fn), map[string]bool{}, node)

			var got []string
			for _, cut := range node.Cuts {
				got = append(got, cut.Field)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("cuts = %+v, want %+v", got, tc.want)
			}
		})
	}
}

// TestATraversalCallOutsideEveryBodyRefuses asserts the projection's own
// cross-check. The reach derivation follows calls from one function to another, so
// a traversal call with no containing function has no caller to attribute it to —
// and reporting the smaller number would undercount the sites with nothing saying
// so.
func TestATraversalCallOutsideEveryBodyRefuses(t *testing.T) {
	t.Parallel()

	const src = `package p

import "x/constraint"

var objections, _ = constraint.RunOnNodes(nil, nil, nil, Rule)
`
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "d/f.go", src, 0)
	if err != nil {
		t.Fatalf("parse the fixture: %v", err)
	}
	_, err = traversalCallSites(sourceFile{Dir: "d", Path: "d/f.go", File: file},
		map[string]bool{"RunOnNodes": true})
	if err == nil {
		t.Fatal("a traversal call at package level was accepted; the site listing would then be " +
			"one short of the whole-file count with nothing reporting the difference")
	}
	if !strings.Contains(err.Error(), "d/f.go") {
		t.Errorf("the refusal does not name the file it read: %q", err)
	}
}

// TestTheCallerReadReachesTheTree asserts the read over the real tree is a
// measurement rather than an empty instrument, and that what it reports is
// internally consistent.
//
// The read's own refusals cover the vacuity shapes, so a nil error is already the
// assertion that the corpus was reached. What this adds are the properties a
// clean-but-wrong read would violate.
func TestTheCallerReadReachesTheTree(t *testing.T) {
	t.Parallel()

	c, err := deriveChain()
	if err != nil {
		t.Fatalf("derive the stage chain: %v", err)
	}
	cov, err := readCallerCoverage(c)
	if err != nil {
		t.Fatalf("the caller read did not complete, so the placement table cannot state which "+
			"stages a surface reaches: %v", err)
	}

	stages := map[string]bool{}
	for _, s := range c.order {
		stages[s.Name] = true
	}
	for _, site := range cov.Sites {
		if !stages[site.Stage] {
			t.Errorf("rule site %s invokes %q at stage %q, which the chain does not order",
				site.Func, site.Check, site.Stage)
		}
	}

	var withNoStage, withAnOption int
	for _, r := range cov.Rows {
		if r.Checks > cov.InvokedChecks {
			t.Errorf("%s reaches %d invoked check(s) of %d, which is more than exist",
				r.File, r.Checks, cov.InvokedChecks)
		}
		if r.FirmChecks > r.Checks {
			t.Errorf("%s reaches %d check(s) whatever the caller sets and %d in total; the "+
				"unconditional reach cannot exceed the whole reach", r.File, r.FirmChecks, r.Checks)
		}
		declared := map[string]bool{}
		for _, s := range r.Stages {
			declared[s] = true
			if !stages[s] {
				t.Errorf("%s reports reaching stage %q, which the chain does not order", r.File, s)
			}
		}
		for _, s := range r.ConditionalStages {
			if !declared[s] {
				t.Errorf("%s reports %q as option-dependent without reporting it as reachable",
					r.File, s)
			}
		}
		// A named value is what makes an option-dependent stage readable; without
		// it the cell says a depth is uncertain and nothing about what decides it.
		if len(r.ConditionalStages) > 0 && len(r.Gates) == 0 {
			t.Errorf("%s reports an option-dependent stage and names no value deciding it", r.File)
		}
		if len(r.ConditionalStages) == 0 && len(r.Gates) > 0 {
			t.Errorf("%s names a deciding value with no option-dependent stage to decide", r.File)
		}
		if len(r.Stages) == 0 {
			withNoStage++
		}
		if len(r.ConditionalStages) > 0 {
			withAnOption++
		}
	}

	// Both irreducible values are reported rather than smoothed away, and both
	// exist in this tree — so the cells that carry them are exercised rather than
	// merely implemented.
	if withNoStage == 0 {
		t.Error("no surface reports reaching no stage; the tree carries a publishing path that " +
			"resolves documents and invokes no rule, so a zero here means the read is not " +
			"finding it rather than that it is gone")
	}
	if withAnOption == 0 {
		t.Error("no surface reports an option-dependent stage; the pipeline stops before render " +
			"on a value the caller supplies, so a zero here means the cut is not being read")
	}
}

// TestATestSupportPackageIsNotASurface asserts the exclusion that keeps a harness
// out of the surface listing, and asserts it is doing work rather than passing
// because there is nothing to exclude.
func TestATestSupportPackageIsNotASurface(t *testing.T) {
	t.Parallel()

	m, err := readModuleSource()
	if err != nil {
		t.Fatalf("the module read did not complete: %v", err)
	}
	if len(m.TestOnlyDirs) == 0 {
		t.Fatal("the read excluded no package as test support; this tree carries a compilation " +
			"harness whose files carry no test suffix, so an empty exclusion set means the " +
			"criterion is not firing rather than that nothing needs excluding")
	}

	c, err := deriveChain()
	if err != nil {
		t.Fatalf("derive the stage chain: %v", err)
	}
	cov, err := readCallerCoverage(c)
	if err != nil {
		t.Fatalf("the caller read did not complete: %v", err)
	}
	for _, r := range cov.Rows {
		for _, dir := range m.TestOnlyDirs {
			if strings.HasPrefix(r.File, dir+"/") {
				t.Errorf("%s is reported as a surface reaching the compiler, and nothing outside a "+
					"test imports %s: a harness is not a surface a user reaches", r.File, dir)
			}
		}
		if strings.HasSuffix(r.File, "_test.go") {
			t.Errorf("%s is reported as a surface, and it is a test file", r.File)
		}
	}
}

// TestTheEntryLadderIsNested asserts the shape the reach derivation reports over
// the pipeline package: an entry deeper in the ladder reaches a superset of what a
// shallower one reaches.
//
// It is asserted rather than described because the whole read rests on it. Two
// entries whose reaches merely overlapped would mean a surface's depth did not
// follow from which entry it called, and every row would then be reporting a
// coincidence.
func TestTheEntryLadderIsNested(t *testing.T) {
	t.Parallel()

	c, err := deriveChain()
	if err != nil {
		t.Fatalf("derive the stage chain: %v", err)
	}
	cov, err := readCallerCoverage(c)
	if err != nil {
		t.Fatalf("the caller read did not complete: %v", err)
	}

	reachOf := map[string]map[string]bool{}
	for _, r := range cov.Rows {
		if r.EntryPackage != cov.PipelinePackage {
			continue
		}
		set := map[string]bool{}
		for _, s := range r.Stages {
			set[s] = true
		}
		if prior, seen := reachOf[r.Entry]; seen && !reflect.DeepEqual(prior, set) {
			t.Errorf("entry %q reports one reach at one call site and a different one at another; "+
				"the reach is a property of the entry, so two callers cannot disagree", r.Entry)
		}
		reachOf[r.Entry] = set
	}
	if len(reachOf) < 2 {
		t.Fatalf("the read found %d entry(ies) in %s with a caller; the ladder cannot be checked "+
			"for nesting with fewer than two", len(reachOf), cov.PipelinePackage)
	}
	for a, sa := range reachOf {
		for b, sb := range reachOf {
			if a >= b {
				continue
			}
			if !subsetOrSuperset(sa, sb) {
				t.Errorf("entries %q and %q reach overlapping but neither-containing stage sets "+
					"(%v and %v); a surface's depth would then not follow from which entry it calls",
					a, b, sortedStages(sa, c), sortedStages(sb, c))
			}
		}
	}
}

func subsetOrSuperset(a, b map[string]bool) bool {
	return contains(a, b) || contains(b, a)
}

func contains(outer, inner map[string]bool) bool {
	for k := range inner {
		if !outer[k] {
			return false
		}
	}
	return true
}

func sortedStages(set map[string]bool, c chain) []string {
	var out []string
	for _, s := range c.order {
		if set[s.Name] {
			out = append(out, s.Name)
		}
	}
	return out
}
