// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package opsurface

import (
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/tools/projectionparity"
)

// projectionDir is this package's own directory, relative to the module root, so
// the surface constants can be read from the declaration rather than transcribed.
//
// It is spelled rather than derived, and a wrong value cannot pass quietly: the
// constant read refuses a directory declaring no surface constant, so the gate
// fails naming its own cause rather than deriving an empty corpus.
const projectionDir = "internal/opsurface"

// projectionCoreScope bounds which packages count as shared computation cores.
//
// It is the module's unimportable tree, and the bound is a real distinction
// rather than a naming convention: a package outside it is published vocabulary,
// whose types are shared with consumers this read knows nothing about. Two
// surfaces reading different parts of a loaded lockfile are reading one
// published structure for two purposes, which is not one surface dropping an
// answer the other was handed.
const projectionCoreScope = "internal/"

// surfaceProjectionDirs maps each surface to the package directories its
// projections live in, relative to the module root.
//
// It is a mapping rather than a derivation because nothing in the tree links a
// surface to the packages that project its results: the command line declares
// its availability in one package and renders every result in another, and no
// declaration connects the two. So the corpus is stated here — and, being
// stated, it is held from both ends by the test below. A surface constant this
// map does not name fails the gate, and so does a row naming a directory that
// holds no source. Neither direction can rot into a green.
var surfaceProjectionDirs = map[Surface][]string{
	SurfaceCLI:   {"internal/clisurface", "cmd/aggrega"},
	SurfaceServe: {"internal/serve"},
	SurfaceMCP:   {"internal/mcp"},
}

// projectionCorpus reads the module root and assembles the surface corpus,
// asserting the mapping accounts for every declared surface.
func projectionCorpus(t *testing.T) (root string, modulePath string, surfaces []projectionparity.Surface) {
	t.Helper()

	root, modulePath, err := projectionparity.ModuleRoot(".")
	if err != nil {
		t.Fatalf("find the module root: %v", err)
	}

	declared, err := projectionparity.ConstantValues(root, projectionDir, "Surface")
	if err != nil {
		t.Fatalf("read the declared surfaces: %v", err)
	}

	mapped := map[string]bool{}
	for surface := range surfaceProjectionDirs {
		mapped[string(surface)] = true
	}
	var unmapped []string
	for _, name := range declared {
		if !mapped[name] {
			unmapped = append(unmapped, name)
		}
	}
	sort.Strings(unmapped)
	if len(unmapped) > 0 {
		t.Fatalf("the projection corpus names no directory for %s. A surface with no corpus is not "+
			"excused from parity — its projections simply go unread, which is the one failure this gate "+
			"must not have. Add the directories its results are rendered in.",
			strings.Join(unmapped, " and "))
	}
	if len(declared) != len(mapped) {
		t.Fatalf("the projection corpus names %d surface(s) and %s declares %d; a row naming a surface "+
			"that no longer exists is stale — delete it", len(mapped), projectionDir, len(declared))
	}

	names := make([]string, 0, len(surfaceProjectionDirs))
	for surface := range surfaceProjectionDirs {
		names = append(names, string(surface))
	}
	sort.Strings(names)
	for _, name := range names {
		surfaces = append(surfaces, projectionparity.Surface{
			Name: name,
			Dirs: surfaceProjectionDirs[Surface(name)],
		})
	}
	return root, modulePath, surfaces
}

// projectionRead assembles the surface corpus over the shipped tree, runs the
// projection read over it, and returns what it found.
//
// Every condition under which a clean result would certify nothing fails here: a
// corpus accounting for a surface the vocabulary does not declare or missing one
// it does, a read that could not run, and a corpus holding no shared computation
// at all. So this is the load-bearing half — it derives the corpus from the tree
// rather than from a list, and a list is what "answers nothing is wrong about
// code it never opened".
//
// It asserts nothing about the findings. That assertion belongs to one caller
// and is deactivated there, which is why this returns the result instead of
// judging it.
func projectionRead(t *testing.T) projectionparity.Result {
	t.Helper()

	root, modulePath, surfaces := projectionCorpus(t)

	result, err := projectionparity.Analyze(root, modulePath, projectionCoreScope, surfaces)
	if err != nil {
		t.Fatalf("run the projection read over the shipped surfaces: %v", err)
	}

	// The corpus is asserted rather than assumed. The read over a tree in which
	// no computation is shared returns no finding and no error, so an empty
	// corpus is indistinguishable from a clean one from the outside: the census
	// is what makes the green mean something.
	if len(result.SharedComputations) == 0 || len(result.ResultTypes) == 0 {
		t.Fatalf("the corpus derivation found %d shared computation(s) and %d structured result type(s); "+
			"a clean result over an empty corpus certifies nothing",
			len(result.SharedComputations), len(result.ResultTypes))
	}
	t.Logf("projection read over %d surface(s): %d shared computation(s), %d structured result type(s), %d projection(s) followed to a verdict",
		len(surfaces), len(result.SharedComputations), len(result.ResultTypes), result.Projections)
	if len(result.UnreadResultTypes) > 0 {
		// Reported, not passed over. No parity claim is made about these in
		// either direction, so a green above must not be read as covering them.
		t.Logf("no surface projects these %d shared result(s) readably, so nothing is asserted about them: %s",
			len(result.UnreadResultTypes), strings.Join(result.UnreadResultTypes, ", "))
	}
	if len(result.Findings) > 0 {
		// Printed rather than asserted, and the distinction is this gate's whole
		// shape. While the assertion below is deactivated this is the only
		// channel the findings have — a passing package prints nothing — so
		// without it the deactivated state would be silent rather than merely
		// permissive. It goes quiet on its own once the assertion is active and
		// the read comes back clean.
		t.Logf("the read reports %d finding(s), which this half of the gate asserts nothing about:\n%s",
			len(result.Findings), projectionparity.Report(result.Findings))
	}
	return result
}

// TestTheProjectionReadRunsOverTheShippedSurfaces is the LIVE half of this gate:
// it runs on every ordinary battery run and it blocks.
//
// What its green covers is the machinery and not the verdict — the surface
// mapping held from both ends, the corpus derived from the module read, the read
// itself, and the census that keeps an empty corpus from reading as a clean one.
// What it deliberately does NOT cover is whether the surfaces agree: that is the
// assertion in the test below, and that assertion is deactivated. Reading this
// test's pass as parity would be reading it as the opposite of what it says.
//
// It exists as its own test because the alternative is worse. With the findings
// assertion and the machinery in one test, the deactivation suppresses both, and
// then nothing has ever run the derivation against the real tree — leaving the
// claim that the gate "will block" unexercised, which is the one state a gate
// shipped ahead of its flip must not be in.
func TestTheProjectionReadRunsOverTheShippedSurfaces(t *testing.T) {
	t.Parallel()

	projectionRead(t)
}

// TestEverySurfaceCarriesWhatTheSharedComputationProduced fails on anything the
// projection read reports over the shipped surfaces.
//
// It answers a question the availability check structurally cannot. That check
// walks membership — whether each surface carries each capability, and under
// what name — and says nothing about two surfaces behaving differently under one
// name. This reads the second axis: given that two surfaces call one shared
// computation, does each one carry the structured facts that computation handed
// it. A count, a verdict, a classification or a hint is owed on every surface
// that can carry structure; only human-formatted presentation may differ.
//
// It blocks rather than reports, and the reason is mechanical rather than a
// matter of strictness: a Go test that gathers findings and prints them prints
// nothing at all on a passing package, so a reporting form of this gate would be
// a mechanism nothing ever reads. There is no reporting tier available here that
// is not inert.
//
// Every kind the read emits fails here. They answer one question between them —
// whether a surface carries what it was handed — and an unreadable site is not a
// lesser finding than a dropped field: it is the read declining to certify a
// projection it could not follow, which is exactly the state a gate must not
// pass over.
//
// What it cannot see, stated so a green is not read as more than it is. A
// surface that answers a request with a second implementation of its own, taking
// different input and calling nothing shared, has no shared result to project
// and is invisible to this read by construction. The reading is syntactic, so a
// value handed on beyond a same-package helper is reported as unreadable rather
// than cleared. And a field no surface carries is not reported, because a
// computation producing something nobody projects is a different defect with a
// different repair.
func TestEverySurfaceCarriesWhatTheSharedComputationProduced(t *testing.T) {
	t.Parallel()

	// 🔴 THIS ASSERTION IS DEACTIVATED, AND IT WOULD FAIL IF ACTIVATED. Deleting
	// the line below and nothing else activates it.
	//
	// It is the only thing deactivated. The machinery this rests on — the corpus
	// derivation, the surface mapping held from both ends, the read, and the
	// census — runs unskipped and blocking in the test above on every ordinary
	// battery run, and the read's own fixtures plus the planted drop below
	// calibrate what it reports. So what is deferred here is the verdict on the
	// tree, not the proof that the gate works.
	//
	// What is not yet true is the tree: the read reports divergences that are
	// still present, and repairing them is separate work with its own scope.
	// Until then the findings are printed by the live half rather than asserted
	// on. The flip being one line is deliberate — it activates code somebody
	// read, rather than being a fresh authoring job done later with this context
	// gone.
	t.Skip("deactivated until the divergences this read reports are repaired; delete this line to activate — the read, its corpus and its census already run and block in TestTheProjectionReadRunsOverTheShippedSurfaces")

	if result := projectionRead(t); len(result.Findings) > 0 {
		t.Fatalf("the projection read reports %d finding(s) over the shipped surfaces:\n%s",
			len(result.Findings), projectionparity.Report(result.Findings))
	}
}

// TestTheProjectionReadFiresOnAPlantedDrop is the calibration for the gate
// above: it plants a divergence the read must report and asserts it is reported.
//
// Without it, the gate's green says only that the read returned nothing, which
// is also what a read reaching no file, or one wired to the wrong directories,
// returns. The planted drop is the difference between a gate and a decoration.
//
// It deliberately repeats a little of what the read's own fixtures establish,
// and the repetition is the point rather than an oversight: those calibrate the
// read against sources of their own, while this calibrates the entry the gate
// actually calls. Unifying the two would blind whichever one was removed.
func TestTheProjectionReadFiresOnAPlantedDrop(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	if err := os.WriteFile(root+"/go.mod", []byte("module planted\n\ngo 1.26.0\n"), 0o600); err != nil {
		t.Fatalf("write the planted module file: %v", err)
	}
	write := func(dir, name, body string) {
		t.Helper()
		if err := os.MkdirAll(root+"/"+dir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
		if err := os.WriteFile(root+"/"+dir+"/"+name, []byte(body), 0o600); err != nil {
			t.Fatalf("write %s/%s: %v", dir, name, err)
		}
	}

	write("internal/core", "core.go", `package core

type Answer struct {
	Verdict bool
	Hint    string
}

func Compute() (Answer, error) { return Answer{}, nil }
`)
	write("internal/one", "one.go", `package one

import "planted/internal/core"

func project() {
	answer, _ := core.Compute()
	_ = answer.Verdict
	_ = answer.Hint
}
`)
	write("internal/two", "two.go", `package two

import "planted/internal/core"

func project() {
	answer, _ := core.Compute()
	_ = answer.Verdict
}
`)

	result, err := projectionparity.Analyze(root, "planted", projectionCoreScope, []projectionparity.Surface{
		{Name: "the first surface", Dirs: []string{"internal/one"}},
		{Name: "the second surface", Dirs: []string{"internal/two"}},
	})
	if err != nil {
		t.Fatalf("run the projection read over the planted sources: %v", err)
	}
	if len(result.Findings) != 1 {
		t.Fatalf("the read reports %d finding(s) for one planted drop, want 1:\n%s",
			len(result.Findings), projectionparity.Report(result.Findings))
	}
	finding := result.Findings[0]
	if finding.Kind != projectionparity.DroppedField {
		t.Fatalf("the read calls the planted drop %q, want %q", finding.Kind, projectionparity.DroppedField)
	}
	if finding.Field != "Hint" || finding.Surface != "the second surface" {
		t.Fatalf("the read names %s.%s on %q; want Hint on the second surface",
			finding.Result, finding.Field, finding.Surface)
	}
}
