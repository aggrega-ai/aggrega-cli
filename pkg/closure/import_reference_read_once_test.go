// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package closure_test

import (
	"context"
	"errors"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// axisFixture binds one template axis to the fragment surface it admits and to
// the accessor for the closure section it accumulates into, so each assertion
// below runs on all three rather than on whichever one someone reached for.
type axisFixture struct {
	name     string
	section  string
	fragment string
	resolved func(*fakeLookup) map[string]closure.LibraryResolution
	libs     func(closure.Closure) []closure.ResolvedLibrary
}

func templateAxes() []axisFixture {
	return []axisFixture{
		{
			name: "MappingLibraries", section: "MappingLibraries", fragment: "#/mappings/Web",
			resolved: func(f *fakeLookup) map[string]closure.LibraryResolution { return f.mappingLibs },
			libs:     func(c closure.Closure) []closure.ResolvedLibrary { return c.MappingLibraries },
		},
		{
			name: "WorkloadLibraries", section: "WorkloadLibraries", fragment: "#/workloads/Web",
			resolved: func(f *fakeLookup) map[string]closure.LibraryResolution { return f.workloadLibs },
			libs:     func(c closure.Closure) []closure.ResolvedLibrary { return c.WorkloadLibraries },
		},
		{
			name: "ArtifactLibraries", section: "ArtifactLibraries", fragment: "#/artifacts/Web",
			resolved: func(f *fakeLookup) map[string]closure.LibraryResolution { return f.artifactsLibs },
			libs:     func(c closure.Closure) []closure.ResolvedLibrary { return c.ArtifactLibraries },
		},
	}
}

func lookupWithLibBeta() *fakeLookup {
	lib := map[string]closure.LibraryResolution{"LibBeta": resolvedLib("1.0.0", "sha256:beta")}
	return &fakeLookup{
		mappingLibs:   lib,
		workloadLibs:  lib,
		artifactsLibs: lib,
	}
}

func noBodies(closure.ResolvedLibrary) ([]string, bool, error) { return nil, false, nil }

// TestAuthoredReferenceReachesDiscoveryAsItWasRead is the property this file
// exists for: a reference an entity's own field carries is recorded from the
// value the document parser produced, so the library the render side resolves is
// the library the closure records.
//
// The pinned version is asserted exactly, not merely for presence. A version
// carrying so much as a trailing space names a version no artifact publishes, so
// a check for "a row exists" would pass on a lockfile nothing can be rebuilt
// from.
func TestAuthoredReferenceReachesDiscoveryAsItWasRead(t *testing.T) {
	t.Parallel()
	for _, axis := range templateAxes() {
		t.Run(axis.name, func(t *testing.T) {
			t.Parallel()
			var c closure.Closure
			err := c.DiscoverTemplateLibraries(context.Background(),
				closure.LibraryAxisNamed(axis.section), lookupWithLibBeta(),
				[]closure.TemplateConsumer{{URI: "store-recipe:1.0.0", Reference: "LibBeta:1.0.0" + axis.fragment}},
				noBodies)
			if err != nil {
				t.Fatalf("well-formed authored reference refused: %v", err)
			}
			libs := axis.libs(c)
			if len(libs) != 1 {
				t.Fatalf("expected exactly one library edge; got %+v", libs)
			}
			if libs[0].URI != "LibBeta" {
				t.Errorf("library = %q; want LibBeta", libs[0].URI)
			}
			if libs[0].ResolvedVersion != "1.0.0" {
				t.Errorf("resolved version = %q; want %q", libs[0].ResolvedVersion, "1.0.0")
			}
		})
	}
}

// TestAuthoredReferenceWhoseRawTextDiffersFromItsFirstLineIsRefused is the
// regression the corpus had no test for: a reference whose raw text differs from
// its own trimmed first line.
//
// Both spellings are one authored character away from a reference that resolves,
// and each used to part the two readers of it in a different direction — the line
// break hid the dependency from the closure entirely, the space carried a version
// with whitespace in it into the pin. Neither produced a diagnostic, which is
// what this asserts: the walk REFUSES, under the condition naming the
// malformation, rather than recording something or nothing quietly.
func TestAuthoredReferenceWhoseRawTextDiffersFromItsFirstLineIsRefused(t *testing.T) {
	t.Parallel()
	forms := map[string]string{
		"line break before the fragment": "LibBeta:1.0.0\n",
		"space before the fragment":      "LibBeta:1.0.0 ",
	}
	for _, axis := range templateAxes() {
		for formName, head := range forms {
			t.Run(axis.name+"/"+formName, func(t *testing.T) {
				t.Parallel()
				var c closure.Closure
				err := c.DiscoverTemplateLibraries(context.Background(),
					closure.LibraryAxisNamed(axis.section), lookupWithLibBeta(),
					[]closure.TemplateConsumer{{URI: "store-recipe:1.0.0", Reference: head + axis.fragment}},
					noBodies)
				if err == nil {
					t.Fatalf("a malformed reference was accepted; closure recorded %+v", axis.libs(c))
				}
				if !errors.Is(err, types.ErrLibraryReferenceWhitespace()) {
					t.Errorf("refused under the wrong condition: %v", err)
				}
			})
		}
	}
}

// TestLibraryBodyReferenceCarryingWhitespaceIsRefused covers the reference no
// document read ever sees: one lifted out of a discovered library's OWN body
// during transitive descent. Its directive is a line in a document, so the first
// line is the reference — and what is left after that line is taken must still be
// a reference, which a trailing space is not.
func TestLibraryBodyReferenceCarryingWhitespaceIsRefused(t *testing.T) {
	t.Parallel()
	for _, axis := range templateAxes() {
		t.Run(axis.name, func(t *testing.T) {
			t.Parallel()
			lookup := lookupWithLibBeta()
			lookup.mappingLibs["LibGamma"] = resolvedLib("2.0.0", "sha256:gamma")
			lookup.workloadLibs["LibGamma"] = resolvedLib("2.0.0", "sha256:gamma")
			lookup.artifactsLibs["LibGamma"] = resolvedLib("2.0.0", "sha256:gamma")

			bodies := func(lib closure.ResolvedLibrary) ([]string, bool, error) {
				if lib.URI == "LibBeta" {
					return []string{"$import: LibGamma:2.0.0 " + axis.fragment}, true, nil
				}
				return nil, false, nil
			}

			var c closure.Closure
			err := c.DiscoverTemplateLibraries(context.Background(),
				closure.LibraryAxisNamed(axis.section), lookup,
				[]closure.TemplateConsumer{{URI: "store-recipe:1.0.0", Reference: "LibBeta:1.0.0" + axis.fragment}},
				bodies)
			if err == nil {
				t.Fatalf("a malformed transitive reference was accepted; closure recorded %+v", axis.libs(c))
			}
			if !errors.Is(err, types.ErrLibraryReferenceWhitespace()) {
				t.Errorf("refused under the wrong condition: %v", err)
			}
		})
	}
}

// TestBodyBorneDirectiveStillReadsItsFirstLine is the positive control beside the
// refusal above: a library entry whose body is a field-root `$import:` followed by
// nothing else still descends, so the refusal is about the whitespace inside the
// reference and not about the body scan having stopped working.
func TestBodyBorneDirectiveStillReadsItsFirstLine(t *testing.T) {
	t.Parallel()
	for _, axis := range templateAxes() {
		t.Run(axis.name, func(t *testing.T) {
			t.Parallel()
			lookup := lookupWithLibBeta()
			lookup.mappingLibs["LibGamma"] = resolvedLib("2.0.0", "sha256:gamma")
			lookup.workloadLibs["LibGamma"] = resolvedLib("2.0.0", "sha256:gamma")
			lookup.artifactsLibs["LibGamma"] = resolvedLib("2.0.0", "sha256:gamma")

			bodies := func(lib closure.ResolvedLibrary) ([]string, bool, error) {
				if lib.URI == "LibBeta" {
					return []string{"$import: LibGamma:2.0.0" + axis.fragment + "\n"}, true, nil
				}
				return nil, false, nil
			}

			var c closure.Closure
			err := c.DiscoverTemplateLibraries(context.Background(),
				closure.LibraryAxisNamed(axis.section), lookup,
				[]closure.TemplateConsumer{{URI: "store-recipe:1.0.0", Reference: "LibBeta:1.0.0" + axis.fragment}},
				bodies)
			if err != nil {
				t.Fatalf("transitive descent refused a well-formed body reference: %v", err)
			}
			seen := map[string]string{}
			for _, lib := range axis.libs(c) {
				seen[lib.URI] = lib.ResolvedVersion
			}
			if seen["LibBeta"] != "1.0.0" || seen["LibGamma"] != "2.0.0" {
				t.Errorf("expected LibBeta 1.0.0 and LibGamma 2.0.0; got %+v", seen)
			}
		})
	}
}
