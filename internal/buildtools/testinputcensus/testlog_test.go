// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"bytes"
	"errors"
	"fmt"
	"go/format"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeLog puts a test log on disk with the leading marker line the toolchain
// writes, so a fixture cannot accidentally exercise the missing-marker path.
func writeLog(t *testing.T, entries ...string) string {
	t.Helper()
	return writeLogAt(t, filepath.Join(t.TempDir(), "testlog.txt"), entries...)
}

// writeLogAt is writeLog at a caller-chosen path, for the cases that need the log
// to sit exactly where the census will look for it.
func writeLogAt(t *testing.T, path string, entries ...string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("making the log fixture's directory: %v", err)
	}
	body := logMagic + "\n"
	for _, e := range entries {
		body += e + "\n"
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("writing the log fixture: %v", err)
	}
	return path
}

func TestClassifyBothDirections(t *testing.T) {
	const tree = "/w"
	const root = "/w/mod"
	const pkgDir = "/w/mod/internal/deep/pkg"

	cases := []struct {
		name        string
		entry       string
		wantShared  []string
		wantOutside int
	}{
		{
			name:  "a relative read inside the module stays inside",
			entry: "open testdata/fixture.yaml",
		},
		{
			name:       "a relative read that climbs into a sibling module counts",
			entry:      "open ../../../../sibling/json/schema.json",
			wantShared: []string{"/w/sibling/json/schema.json"},
		},
		{
			name:  "a relative read that climbs and comes back stays inside",
			entry: "open ../../../pkg/types/testdata/x.json",
		},
		{
			name:  "an absolute read inside the module stays inside",
			entry: "open /w/mod/go.mod",
		},
		{
			name:        "a read outside the tree is reported separately, not counted",
			entry:       "open /etc/hosts",
			wantOutside: 1,
		},
		{
			name:       "a stat is an input too, not only an open",
			entry:      "stat ../../../../sibling/json",
			wantShared: []string{"/w/sibling/json"},
		},
		{
			name:  "the module root itself is inside",
			entry: "open /w/mod",
		},
		{
			name:       "a sibling directory sharing a name prefix is outside the module",
			entry:      "open /w/module/other.json",
			wantShared: []string{"/w/module/other.json"},
		},
		{
			name:  "an environment read has no location and is not counted",
			entry: "getenv HOME",
		},
		{
			name:  "the tree root itself counts as inside the tree, not outside it",
			entry: "open /w/.gitmodules",
			// Inside the tree, outside the module: the shared population.
			wantShared: []string{"/w/.gitmodules"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := classify(writeLog(t, tc.entry), pkgDir, root, tree)
			if err != nil {
				t.Fatalf("classify: %v", err)
			}
			if !equalStrings(got.SharedTree, tc.wantShared) {
				t.Errorf("shared-tree inputs = %q, want %q", got.SharedTree, tc.wantShared)
			}
			if got.OutsideTree != tc.wantOutside {
				t.Errorf("outside-the-tree inputs = %d, want %d", got.OutsideTree, tc.wantOutside)
			}
		})
	}
}

// TestClassifyRefusesAModuleOutsideTheTree guards the one way the split could
// collapse silently: given a tree root that does not contain the module, every
// escaping input would land in the ungated bucket and the gated count would read
// zero while still looking like a measurement.
func TestClassifyRefusesAModuleOutsideTheTree(t *testing.T) {
	_, err := classify(writeLog(t, "open ../../elsewhere/x.json"), "/a/mod/pkg", "/a/mod", "/b")
	if err == nil {
		t.Fatal("classify accepted a module root outside the tree root")
	}
	if !strings.Contains(err.Error(), "not inside tree root") {
		t.Errorf("the error should say the roots do not nest, got %v", err)
	}
}

// TestClassifyFollowsChdir pins the one part of the walk that a reader is most
// likely to leave out: the working directory a relative entry is resolved
// against is not fixed at the package directory, because a chdir entry moves it.
// Leaving it out would classify the same log two different ways.
func TestClassifyFollowsChdir(t *testing.T) {
	const tree = "/w"
	const root = "/w/mod"
	const pkgDir = "/w/mod/internal/pkg"

	// Before the chdir the relative read is inside the module; after it, the
	// identical entry resolves outside. One log exercises both.
	got, err := classify(writeLog(t,
		"open testdata/before.yaml",
		"chdir /w/elsewhere",
		"open testdata/after.yaml",
	), pkgDir, root, tree)
	if err != nil {
		t.Fatalf("classify: %v", err)
	}
	want := []string{"/w/elsewhere/testdata/after.yaml"}
	if !equalStrings(got.SharedTree, want) {
		t.Fatalf("outside the root = %q, want %q", got.SharedTree, want)
	}
	if got.Inputs != 2 {
		t.Errorf("inputs = %d, want 2", got.Inputs)
	}
}

func TestClassifyDeduplicatesRepeatedReads(t *testing.T) {
	got, err := classify(writeLog(t,
		"open ../../outside/a.json",
		"stat ../../outside/a.json",
		"open ../../outside/a.json",
		"open ../../outside/b.json",
	), "/w/mod/pkg", "/w/mod", "/w")
	if err != nil {
		t.Fatalf("classify: %v", err)
	}
	want := []string{"/w/outside/a.json", "/w/outside/b.json"}
	if !equalStrings(got.SharedTree, want) {
		t.Fatalf("outside the root = %q, want %q", got.SharedTree, want)
	}
	if got.Inputs != 4 {
		t.Errorf("inputs = %d, want 4 — de-duplication is for the report, not for the input tally", got.Inputs)
	}
}

// TestClassifyRefusesWhatItCannotRead is the honesty half. Each of these inputs
// could plausibly be read as "this package read nothing", and each must be an
// error instead, because a confident zero is the failure this census exists to
// avoid making.
func TestClassifyRefusesWhatItCannotRead(t *testing.T) {
	t.Run("a file with no marker line is not a test log", func(t *testing.T) {
		p := filepath.Join(t.TempDir(), "log")
		if err := os.WriteFile(p, []byte("open ../x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := classify(p, "/w/mod/pkg", "/w/mod", "/w"); !errors.Is(err, errNoMagic) {
			t.Fatalf("error = %v, want errNoMagic", err)
		}
	})

	t.Run("an empty file is not a test log", func(t *testing.T) {
		p := filepath.Join(t.TempDir(), "log")
		if err := os.WriteFile(p, nil, 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := classify(p, "/w/mod/pkg", "/w/mod", "/w"); !errors.Is(err, errNoMagic) {
			t.Fatalf("error = %v, want errNoMagic", err)
		}
	})

	t.Run("an absent file is an error, never an empty result", func(t *testing.T) {
		missing := filepath.Join(t.TempDir(), "never-written.txt")
		if _, err := classify(missing, "/w/mod/pkg", "/w/mod", "/w"); err == nil {
			t.Fatal("classify accepted an absent log")
		}
	})

	t.Run("an unknown verb must be taught, not skipped", func(t *testing.T) {
		_, err := classify(writeLog(t, "readlink ../outside/a.json"), "/w/mod/pkg", "/w/mod", "/w")
		if err == nil {
			t.Fatal("classify accepted an unknown verb")
		}
		if !strings.Contains(err.Error(), "readlink") {
			t.Errorf("the error should name the verb it did not know, got %v", err)
		}
	})

	t.Run("an entry with no space is malformed", func(t *testing.T) {
		if _, err := classify(writeLog(t, "open"), "/w/mod/pkg", "/w/mod", "/w"); err == nil {
			t.Fatal("classify accepted an entry with no name")
		}
	})

	t.Run("a relative package directory is refused", func(t *testing.T) {
		if _, err := classify(writeLog(t, "open x"), "pkg", "/w/mod", "/w"); err == nil {
			t.Fatal("classify accepted a relative package directory")
		}
	})
}

func TestHasFilePathPrefixStopsAtComponentBoundaries(t *testing.T) {
	cases := []struct {
		s, prefix string
		want      bool
	}{
		{"/a/b", "/a/b", true},
		{"/a/b/c", "/a/b", true},
		{"/a/bc", "/a/b", false},
		{"/a", "/a/b", false},
		{"/a/b/c", "/a/b/", true},
		{"/ab/c", "/a", false},
	}
	for _, tc := range cases {
		if got := hasFilePathPrefix(tc.s, tc.prefix); got != tc.want {
			t.Errorf("hasFilePathPrefix(%q, %q) = %v, want %v", tc.s, tc.prefix, got, tc.want)
		}
	}
}

func TestReachComponentsNameTheCorpusNotTheFile(t *testing.T) {
	cases := []struct {
		name   string
		shared []string
		want   []string
	}{
		{
			name:   "the first component beneath the tree root is the answer",
			shared: []string{"/w/aggrega-docs/language-reference/x.md"},
			want:   []string{"aggrega-docs"},
		},
		{
			name: "several reads into one corpus collapse to one component",
			shared: []string{
				"/w/aggrega-schemas/json/a.json",
				"/w/aggrega-schemas/json/b.json",
			},
			want: []string{"aggrega-schemas"},
		},
		{
			name: "components are sorted, not in the order the reads arrived",
			shared: []string{
				"/w/aggrega-schemas/json/a.json",
				"/w/.gitmodules",
				"/w/aggrega-docs/x.md",
			},
			want: []string{".gitmodules", "aggrega-docs", "aggrega-schemas"},
		},
		{
			name:   "a read of the tree root directory itself is its own component",
			shared: []string{"/w"},
			want:   []string{"."},
		},
		{
			// classify cannot produce this, so it is here as a guard rather than a
			// case: relativising an outside path succeeds and yields a leading
			// "..", and reducing that to its first component would record a bare
			// ".." and lose everything the entry said.
			name:   "a path outside the tree contradicts the contract and is kept whole",
			shared: []string{"/elsewhere/x.json"},
			want:   []string{"/elsewhere/x.json"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := reachComponents(tc.shared, "/w"); !equalStrings(got, tc.want) {
				t.Errorf("reach components = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestTestLogPathMirrorsThePackageDirectory pins the derivation the collecting
// shim holds the other half of.
//
// The injectivity row is the one that matters and it is why this test exists: the
// obvious simplification is to flatten the package path into a single filename by
// replacing separators, and that is not injective — two different packages can
// flatten to one name, share one log file, truncate each other, and leave the
// census reading one package's inputs as the other's. That is a silently WRONG
// measurement, which no refusal anywhere else would catch, so the property is
// asserted here rather than left to a comment.
func TestTestLogPathMirrorsThePackageDirectory(t *testing.T) {
	root := t.TempDir()
	logRoot := filepath.Join(root, "logs")

	t.Run("the log sits under the log root and is named for the toolchain's file", func(t *testing.T) {
		pkg := filepath.Join(root, "mod", "pkg")
		if err := os.MkdirAll(pkg, 0o755); err != nil {
			t.Fatal(err)
		}
		got := testLogPath(logRoot, pkg)
		if !strings.HasPrefix(got, logRoot+string(filepath.Separator)) {
			t.Errorf("the log path %q should sit under the log root %q", got, logRoot)
		}
		if filepath.Base(got) != "testlog.txt" {
			t.Errorf("the log should be named testlog.txt, got %q", filepath.Base(got))
		}
	})

	t.Run("two packages a flattened name would collide are kept apart", func(t *testing.T) {
		// These two paths differ only in where the separator falls, so any scheme
		// substituting a single character for the separator maps them to one name.
		a := filepath.Join(root, "a", "b_c")
		b := filepath.Join(root, "a_b", "c")
		for _, d := range []string{a, b} {
			if err := os.MkdirAll(d, 0o755); err != nil {
				t.Fatal(err)
			}
		}
		if testLogPath(logRoot, a) == testLogPath(logRoot, b) {
			t.Fatalf("two distinct package directories map to one log path: %q", testLogPath(logRoot, a))
		}
	})
}

// TestCensusPackageTellsAnUnmeasuredPackageFromAMeasuredZero is the fail-loud
// contract, asserted in both directions because only the pair pins it.
//
// A package that ran and read nothing writes a log with the marker line and no
// entries, which is a MEASURED zero. A package whose log is absent was not
// measured at all. Reading the second as the first would report a confident zero
// for a package nobody looked at — and since the gate compares sets, a carrier
// reporting zero reads as a carrier that stopped carrying, which moves no exit
// code. A test asserting only the zero case would pass on an implementation that
// makes exactly that mistake.
func TestCensusPackageTellsAnUnmeasuredPackageFromAMeasuredZero(t *testing.T) {
	tree := t.TempDir()
	logRoot := filepath.Join(tree, "logs")
	modRoot := filepath.Join(tree, "mod")
	pkgDir := filepath.Join(modRoot, "pkg")
	if err := os.MkdirAll(pkgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	p := listedPackage{ImportPath: "example.test/mod/pkg", Dir: pkgDir, Root: modRoot}
	o := runOpts{treeRoot: tree}

	t.Run("an absent log is an error naming the package, never an empty result", func(t *testing.T) {
		got := censusPackage(p, logRoot, o)
		if got.Err == nil {
			t.Fatal("censusPackage reported a package whose log was never written as measured")
		}
		if !strings.Contains(got.Err.Error(), p.ImportPath) {
			t.Errorf("the error should name the package it could not measure, got %v", got.Err)
		}
		if !strings.Contains(got.Err.Error(), "NOT measured") {
			t.Errorf("the error should say the package was not measured, got %v", got.Err)
		}
		// The refusal has to travel, not merely be returned: unmeasured is what
		// the report and the recording path both read.
		problems := unmeasured([]modResult{{Rel: "mod", Packages: []pkgResult{got}}})
		if len(problems) != 1 {
			t.Fatalf("unmeasured problems = %d, want 1 — the refusal did not reach the gate", len(problems))
		}
	})

	t.Run("a marker-only log is a measured zero", func(t *testing.T) {
		writeLogAt(t, testLogPath(logRoot, pkgDir))
		got := censusPackage(p, logRoot, o)
		if got.Err != nil {
			t.Fatalf("a log holding only the marker line should measure as zero, got %v", got.Err)
		}
		if len(got.SharedTree) != 0 || got.Inputs != 0 {
			t.Errorf("shared=%v inputs=%d, want no reads at all", got.SharedTree, got.Inputs)
		}
	})

	t.Run("a shared read in the collected log is classified", func(t *testing.T) {
		writeLogAt(t, testLogPath(logRoot, pkgDir), "open "+filepath.Join(tree, "sibling", "golden.json"))
		got := censusPackage(p, logRoot, o)
		if got.Err != nil {
			t.Fatalf("classifying the collected log: %v", got.Err)
		}
		if len(got.SharedTree) != 1 {
			t.Fatalf("shared reads = %v, want the one read outside the module root", got.SharedTree)
		}
	})
}

// TestReportKeepsAnUnmeasuredCarrierApartFromAStaleOne pins the reporting half of
// the same contract. Both an unmeasured package and a measured-clean one carry
// zero, and the recorded set's two directions are asymmetric — growth fails,
// shrinkage is only reported — so calling an unmeasured carrier stale would
// invite deleting the record entry that this run's refusal was protecting.
func TestReportKeepsAnUnmeasuredCarrierApartFromAStaleOne(t *testing.T) {
	results := []modResult{{
		Rel:  "m",
		Root: "/w/m",
		Packages: []pkgResult{
			{ImportPath: "m/unread", Err: errors.New("no test log was collected for m/unread")},
			{ImportPath: "m/clean", Inputs: 3},
		},
	}}
	expect := set(recorded("m", false, "m/unread", "m/clean"))

	var buf bytes.Buffer
	err := report(&buf, "/w", results, expect, false, "m")
	if err == nil {
		t.Fatal("a census that could not read part of its scope must not pass")
	}
	out := buf.String()
	if !strings.Contains(out, "m/unread") {
		t.Fatalf("the report should name the package it could not read:\n%s", out)
	}
	if !strings.Contains(out, "were NOT measured by this run") {
		t.Errorf("the report should say the unread carrier was not measured:\n%s", out)
	}
	// m/clean WAS read and found to carry nothing, so it is legitimately stale;
	// m/unread must not appear in that same sentence.
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "no longer read a shared input") && strings.Contains(line, "m/unread") {
			t.Errorf("an unmeasured carrier was reported as having stopped carrying:\n  %s", line)
		}
	}
}

// mod builds a synthetic module measurement: escaping test packages that read a
// shared input, and clean ones that read nothing outside their module.
//
// Every synthetic package gets its OWN import path. Under a set comparison a
// shared path would collapse several packages into one element, so a case meaning
// "three carriers against two recorded" would silently mean "one against one".
func mod(rel string, escaping, clean int) modResult {
	m := modResult{Rel: rel, Root: "/w/" + rel}
	for i := 0; i < escaping; i++ {
		m.Packages = append(m.Packages, pkgResult{
			ImportPath: fmt.Sprintf("%s/esc%d", rel, i),
			SharedTree: []string{"/w/outside/x"},
			Inputs:     1,
		})
	}
	for i := 0; i < clean; i++ {
		m.Packages = append(m.Packages, pkgResult{
			ImportPath: fmt.Sprintf("%s/clean%d", rel, i),
			Inputs:     1,
		})
	}
	return m
}

// recorded builds one module's recorded entry from the import paths it carries.
func recorded(rel string, defeated bool, imports ...string) expectedModule {
	m := expectedModule{Rel: rel, CacheDefeated: defeated}
	for _, ip := range imports {
		m.Carriers = append(m.Carriers, expectedCarrier{ImportPath: ip, Reach: []string{"outside"}})
	}
	return m
}

func set(mods ...expectedModule) testInputExpectation {
	return testInputExpectation{Measured: "2026-01-01", Modules: mods}
}

// TestReportFailsOnGrowthOnly pins the gate's verdict: a carrier the record does
// not hold fails and is NAMED, an exact match passes, a carrier that has gone is
// reported without failing, and anything unmeasured fails because a partial
// census cannot certify the part it missed.
func TestReportFailsOnGrowthOnly(t *testing.T) {
	cases := []struct {
		name    string
		results []modResult
		expect  testInputExpectation
		wantErr bool
		wantOut []string
		// wantInGrowth is asserted against the GROWTH SECTION alone, not the whole
		// report. Asserting a carrier's name against the whole buffer proves
		// nothing: the per-module listing above names every carrier it measured, so
		// a growth message that had dropped the name entirely would still pass.
		// Established by needle — the unscoped form did exactly that.
		wantInGrowth []string
	}{
		{
			name:    "a carrier set matching the record passes",
			results: []modResult{mod("m", 2, 3)},
			expect:  set(recorded("m", false, "m/esc0", "m/esc1")),
		},
		{
			name:         "an added carrier fails, and the message names it and where it reached",
			results:      []modResult{mod("m", 3, 3)},
			expect:       set(recorded("m", false, "m/esc0", "m/esc1")),
			wantErr:      true,
			wantOut:      []string{"the carrier set grew"},
			wantInGrowth: []string{"m/esc2", "reaches outside"},
		},
		{
			name:         "two added carriers are both named, and the count of additions is stated",
			results:      []modResult{mod("m", 4, 0)},
			expect:       set(recorded("m", false, "m/esc0", "m/esc1")),
			wantErr:      true,
			wantOut:      []string{"2 test package(s) read a shared input"},
			wantInGrowth: []string{"m/esc2", "m/esc3"},
		},
		{
			name:         "a module with carriers and no record entry fails and names them",
			results:      []modResult{mod("m", 1, 0)},
			expect:       set(),
			wantErr:      true,
			wantOut:      []string{"the carrier set grew"},
			wantInGrowth: []string{"m/esc0", "reaches outside"},
		},
		{
			name:    "a carrier the record holds and this run did not measure is stale, and does not fail",
			results: []modResult{mod("m", 1, 3)},
			expect:  set(recorded("m", false, "m/esc0", "m/esc1")),
			wantOut: []string{"now stale", "no longer read a shared input: m/esc1"},
		},
		{
			name:    "a recorded module this census was not given does not fail",
			results: []modResult{mod("m", 1, 0)},
			expect:  set(recorded("m", false, "m/esc0"), recorded("gone", false, "gone/esc0")),
			wantOut: []string{"read as stale: gone/esc0"},
		},
		{
			name:    "a module that could not be measured fails",
			results: []modResult{{Rel: "m", Err: errors.New("go list exploded")}},
			expect:  set(),
			wantErr: true,
			wantOut: []string{"certifies nothing"},
		},
		{
			name: "a package that could not be measured fails even when the sets match",
			results: []modResult{{Rel: "m", Root: "/w/m", Packages: []pkgResult{
				{ImportPath: "m/broken", Err: errors.New("binary crashed")},
			}}},
			expect:  set(),
			wantErr: true,
			wantOut: []string{"certifies nothing"},
		},
		{
			name:    "a recorded module whose carriers have all gone is stale, and does not fail",
			results: []modResult{{Rel: "m", Root: "/w/m"}},
			expect:  set(recorded("m", false, "m/esc0")),
			wantOut: []string{"now stale", "no longer read a shared input: m/esc0"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			err := report(&buf, "/w", tc.results, tc.expect, false, "")
			if tc.wantErr != (err != nil) {
				t.Fatalf("error = %v, wantErr = %v\n%s", err, tc.wantErr, buf.String())
			}
			for _, want := range tc.wantOut {
				if !strings.Contains(buf.String(), want) {
					t.Errorf("output does not contain %q:\n%s", want, buf.String())
				}
			}
			if len(tc.wantInGrowth) > 0 {
				_, growth, found := strings.Cut(buf.String(), "the carrier set grew")
				if !found {
					t.Fatalf("no growth section in:\n%s", buf.String())
				}
				for _, want := range tc.wantInGrowth {
					if !strings.Contains(growth, want) {
						t.Errorf("the growth section does not contain %q:\n%s", want, growth)
					}
				}
			}
		})
	}
}

// TestReportSaysWhetherEnrolmentWasSupplied keeps the two zeros apart: a module
// that is known not to be cache-defeated, and a module whose enrolment nobody
// told the census about.
func TestReportSaysWhetherEnrolmentWasSupplied(t *testing.T) {
	res := []modResult{{Rel: "m", Root: "/w/m", Packages: []pkgResult{
		{ImportPath: "m/esc", SharedTree: []string{"/w/outside/x"}, Inputs: 1},
	}}}
	expect := set(recorded("m", false, "m/esc"))

	var unsupplied, notEnrolled, enrolled bytes.Buffer
	if err := report(&unsupplied, "/w", res, expect, false, ""); err != nil {
		t.Fatal(err)
	}
	if err := report(&notEnrolled, "/w", res, expect, false, "other"); err != nil {
		t.Fatal(err)
	}
	if err := report(&enrolled, "/w", res, set(recorded("m", true, "m/esc")), false, "m,other"); err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(unsupplied.String(), "was not supplied") {
		t.Errorf("an absent enrolment must say so, got:\n%s", unsupplied.String())
	}
	if !strings.Contains(notEnrolled.String(), "does NOT run with caching defeated") {
		t.Errorf("a module outside the enrolment must be named, got:\n%s", notEnrolled.String())
	}
	if !strings.Contains(enrolled.String(), "runs with caching defeated") ||
		strings.Contains(enrolled.String(), "does NOT run") {
		t.Errorf("an enrolled module must read as covered, got:\n%s", enrolled.String())
	}
}

// TestReportNotesEnrolmentDivergenceWithoutFailing pins the one thing the
// recorded enrolment is NOT: a second authority on it. The record describes the
// condition its measurement was taken under, so when it disagrees with what the
// caller now reports, the record is stale on that point and the exit code stays
// where the carrier comparison put it.
func TestReportNotesEnrolmentDivergenceWithoutFailing(t *testing.T) {
	res := []modResult{{Rel: "m", Root: "/w/m", Packages: []pkgResult{
		{ImportPath: "m/esc", SharedTree: []string{"/w/outside/x"}, Inputs: 1},
	}}}
	// Recorded as having run with caching defeated; the caller reports otherwise.
	expect := set(recorded("m", true, "m/esc"))

	var buf bytes.Buffer
	if err := report(&buf, "/w", res, expect, false, "other"); err != nil {
		t.Fatalf("an enrolment divergence must not move the exit code, got %v\n%s", err, buf.String())
	}
	if !strings.Contains(buf.String(), "the record is stale on that point") {
		t.Errorf("a divergence must be named, got:\n%s", buf.String())
	}
}

// TestNewExpectationRecordsOnlyModulesThatCarry pins the semantic that lets a
// module's ABSENCE mean "expected to carry none": a module measured with no
// carrier is left out, so a newly added module needs no bookkeeping until one of
// its packages actually reads a shared input.
func TestNewExpectationRecordsOnlyModulesThatCarry(t *testing.T) {
	e := newExpectation("/w", []modResult{mod("carries", 1, 2), mod("clean", 0, 3)},
		map[string]bool{}, "2026-01-02")
	if len(e.Modules) != 1 || e.Modules[0].Rel != "carries" {
		t.Fatalf("recorded modules = %+v, want only \"carries\"", e.Modules)
	}
}

// TestWriteExpectationFileEmitsDeterministicGofmtCleanGo drives the writer the
// way the recording mode does — build the record from a measurement, then emit it
// — and reads the file it produced.
func TestWriteExpectationFileEmitsDeterministicGofmtCleanGo(t *testing.T) {
	// Given out of order, and with one module that carries nothing, so the
	// emitted ordering and the omission are both readable from the output.
	e := newExpectation("/w",
		[]modResult{mod("z-mod", 1, 0), mod("a-mod", 2, 1), mod("no-carrier", 0, 2)},
		map[string]bool{"a-mod": true}, "2026-01-02")

	dir := t.TempDir()
	first, second := filepath.Join(dir, "first.go"), filepath.Join(dir, "second.go")
	if err := writeExpectationFile(first, e); err != nil {
		t.Fatalf("writing the record: %v", err)
	}
	if err := writeExpectationFile(second, e); err != nil {
		t.Fatalf("writing the record a second time: %v", err)
	}

	got, err := os.ReadFile(first)
	if err != nil {
		t.Fatal(err)
	}
	again, err := os.ReadFile(second)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, again) {
		t.Errorf("two writes of one measurement produced different bytes:\n%s\n---\n%s", got, again)
	}

	if _, err := parser.ParseFile(token.NewFileSet(), first, got, parser.AllErrors); err != nil {
		t.Fatalf("the written record does not parse as Go: %v\n%s", err, got)
	}
	formatted, err := format.Source(got)
	if err != nil {
		t.Fatalf("formatting the written record: %v", err)
	}
	if !bytes.Equal(got, formatted) {
		t.Errorf("the written record is not gofmt-clean:\n%s", got)
	}

	body := string(got)
	for _, want := range []string{
		`Measured: "2026-01-02"`,
		`Rel:           "a-mod"`,
		`CacheDefeated: true`,
		`{ImportPath: "a-mod/esc0", Reach: []string{"outside"}}`,
		"DO NOT EDIT",
		"--write-test-input-expectation",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the written record does not contain %q:\n%s", want, body)
		}
	}
	if strings.Contains(body, "no-carrier") {
		t.Errorf("a module carrying nothing must not be recorded:\n%s", body)
	}
	if i, j := strings.Index(body, `"a-mod"`), strings.Index(body, `"z-mod"`); i < 0 || j < 0 || i > j {
		t.Errorf("modules must be emitted in path order whatever order they were measured in:\n%s", body)
	}
}

// TestWriteMeasuredWritesAtTheConstantSiteAndSaysWhatMoved drives the recording
// path the way the flag does — into a synthetic tree root — and reads both the
// file it landed and the account it gave.
//
// The write target is the one path constant joined onto the resolved tree root, so
// a synthetic root exercises that derivation without writing into the checkout.
// Proving it here matters out of proportion to its size: the real recording rides
// a whole-suite measurement, so a mistake in the derivation would surface only
// after minutes of work, at the one moment there is nothing left to retry.
func TestWriteMeasuredWritesAtTheConstantSiteAndSaysWhatMoved(t *testing.T) {
	root := t.TempDir()
	site := filepath.Join(root, filepath.FromSlash(expectationSite))
	if err := os.MkdirAll(filepath.Dir(site), 0o755); err != nil {
		t.Fatal(err)
	}

	// Measured: m/esc0 and m/esc1. Recorded: m/esc0 and m/gone. So one carrier is
	// added and one is dropped, and both must be accounted for.
	//
	// The shared reads are built UNDER root rather than borrowed from mod, because
	// classify's contract is that every shared read lies inside the tree root and a
	// fixture that broke it would exercise a state production cannot reach.
	results := []modResult{{Rel: "m", Root: filepath.Join(root, "m"), Packages: []pkgResult{
		{ImportPath: "m/esc0", SharedTree: []string{filepath.Join(root, "outside", "x")}, Inputs: 1},
		{ImportPath: "m/esc1", SharedTree: []string{filepath.Join(root, "outside", "x")}, Inputs: 1},
		{ImportPath: "m/clean0", Inputs: 1},
	}}}

	var buf bytes.Buffer
	if err := writeMeasured(&buf, root, results,
		set(recorded("m", false, "m/esc0", "m/gone")), runOpts{cacheDefeated: "m"}); err != nil {
		t.Fatalf("recording the measured set: %v", err)
	}

	written, err := os.ReadFile(site)
	if err != nil {
		t.Fatalf("the record was not written at the site the constant names: %v", err)
	}
	if _, err := parser.ParseFile(token.NewFileSet(), site, written, parser.AllErrors); err != nil {
		t.Fatalf("the written record does not parse as Go: %v\n%s", err, written)
	}
	if !strings.Contains(string(written), "CacheDefeated: true") {
		t.Errorf("the record must carry the enrolment the caller supplied:\n%s", written)
	}

	for _, want := range []string{
		"added:   m: m/esc1 — reaches outside",
		"dropped: m: m/gone",
		"nothing here moved the exit code",
	} {
		if !strings.Contains(buf.String(), want) {
			t.Errorf("the account of what moved does not contain %q:\n%s", want, buf.String())
		}
	}
}

// TestWriteMeasuredRefusesAPartialCensus pins the refusal that keeps the record
// honest: a set recorded from a census that could not read part of its scope
// would hold no carrier for the modules it missed, so the next run would read
// every one of them as newly grown — turning this run's owed refusal into a
// recorded fact.
func TestWriteMeasuredRefusesAPartialCensus(t *testing.T) {
	var buf bytes.Buffer
	err := writeMeasured(&buf, "/w",
		[]modResult{{Rel: "m", Err: errors.New("go list exploded")}},
		testInputExpectation{}, runOpts{})
	if err == nil {
		t.Fatal("the writer recorded a set from a census that could not measure its scope")
	}
	if !strings.Contains(err.Error(), "nothing was recorded") {
		t.Errorf("the refusal should say that nothing was recorded, got %v", err)
	}
	// The refusal reverts to gating, so the operator is told WHICH part could not be
	// measured rather than only that something was not.
	for _, want := range []string{"certifies nothing", "go list exploded"} {
		if !strings.Contains(buf.String(), want) {
			t.Errorf("the refusal must print what it could not measure, missing %q:\n%s", want, buf.String())
		}
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
