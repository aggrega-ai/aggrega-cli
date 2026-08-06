// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeLog puts a test log on disk with the leading marker line the toolchain
// writes, so a fixture cannot accidentally exercise the missing-marker path.
func writeLog(t *testing.T, entries ...string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "testlog.txt")
	body := logMagic + "\n"
	for _, e := range entries {
		body += e + "\n"
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatalf("writing the log fixture: %v", err)
	}
	return p
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

// TestReportFailsOnGrowthOnly pins the gate's verdict: growth fails, an exact
// match passes, a shrink passes with a note, and anything unmeasured fails
// because a partial census cannot certify the part it missed.
func TestReportFailsOnGrowthOnly(t *testing.T) {
	mod := func(rel string, escaping, clean int) modResult {
		m := modResult{Rel: rel, Root: "/w/" + rel}
		for i := 0; i < escaping; i++ {
			m.Packages = append(m.Packages, pkgResult{ImportPath: rel + "/esc", SharedTree: []string{"/w/outside/x"}, Inputs: 1})
		}
		for i := 0; i < clean; i++ {
			m.Packages = append(m.Packages, pkgResult{ImportPath: rel + "/clean", Inputs: 1})
		}
		return m
	}

	cases := []struct {
		name    string
		results []modResult
		expect  map[string]int
		wantErr bool
		wantOut string
	}{
		{
			name:    "a count matching its expectation passes",
			results: []modResult{mod("m", 2, 3)},
			expect:  map[string]int{"m": 2},
		},
		{
			name:    "a count above its expectation fails",
			results: []modResult{mod("m", 3, 3)},
			expect:  map[string]int{"m": 2},
			wantErr: true,
			wantOut: "the count grew",
		},
		{
			name:    "a module with no expectation is expected to have none",
			results: []modResult{mod("m", 1, 0)},
			expect:  map[string]int{},
			wantErr: true,
			wantOut: "the count grew",
		},
		{
			name:    "a count below its expectation passes and says the expectation is stale",
			results: []modResult{mod("m", 1, 3)},
			expect:  map[string]int{"m": 2},
			wantOut: "now stale",
		},
		{
			name:    "a module that could not be measured fails",
			results: []modResult{{Rel: "m", Err: errors.New("go list exploded")}},
			expect:  map[string]int{},
			wantErr: true,
			wantOut: "certifies nothing",
		},
		{
			name: "a package that could not be measured fails even when the counts match",
			results: []modResult{{Rel: "m", Root: "/w/m", Packages: []pkgResult{
				{ImportPath: "m/broken", Err: errors.New("binary crashed")},
			}}},
			expect:  map[string]int{},
			wantErr: true,
			wantOut: "certifies nothing",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			err := report(&buf, "/w", tc.results, tc.expect, false, "")
			if tc.wantErr != (err != nil) {
				t.Fatalf("error = %v, wantErr = %v\n%s", err, tc.wantErr, buf.String())
			}
			if tc.wantOut != "" && !strings.Contains(buf.String(), tc.wantOut) {
				t.Errorf("output does not contain %q:\n%s", tc.wantOut, buf.String())
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
	expect := map[string]int{"m": 1}

	var unsupplied, notEnrolled, enrolled bytes.Buffer
	if err := report(&unsupplied, "/w", res, expect, false, ""); err != nil {
		t.Fatal(err)
	}
	if err := report(&notEnrolled, "/w", res, expect, false, "other"); err != nil {
		t.Fatal(err)
	}
	if err := report(&enrolled, "/w", res, expect, false, "m,other"); err != nil {
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
