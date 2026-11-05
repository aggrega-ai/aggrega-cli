// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package catalog_test

import (
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/entitykind"
)

// nameShapeConstPattern matches the declaration form name_shape.go documents as
// load-bearing: `const <Kind>NameShape = ` followed by a backtick-quoted string. It
// is the same extraction the linter's cross-language symmetry test performs, applied
// here for a different question — not whether the two languages agree, but whether
// the set they agree on covers the kinds it has to.
var nameShapeConstPattern = regexp.MustCompile("(?m)^const ([A-Za-z0-9]+)NameShape = `([^`]*)`")

// declaredNameShapes reads name_shape.go as source text and returns the kind each
// declaration names, mapped to its pattern.
//
// The file is read rather than imported deliberately. It declares patterns and
// nothing else — no Go code in this module matches against them — and a Go-side
// lookup table added just to make this assertion possible would be another copy of
// the kind set, which is the failure this assertion exists to catch.
func declaredNameShapes(t *testing.T) map[entitykind.Kind]string {
	t.Helper()
	source, err := os.ReadFile("name_shape.go")
	if err != nil {
		t.Fatalf("read name_shape.go: %v", err)
	}
	matches := nameShapeConstPattern.FindAllStringSubmatch(string(source), -1)
	shapes := map[entitykind.Kind]string{}
	for _, m := range matches {
		shapes[entitykind.Kind(m[1])] = m[2]
	}
	return shapes
}

// TestNameShapeExtractionFindsTheDeclarations guards the instrument before anything
// is concluded from it.
//
// Every assertion in this file rests on a text extraction, and a text extraction
// that stops matching reports an empty set — which reads as "no kind declares a name
// shape" and would make each check below pass while measuring nothing. Reflowing a
// declaration across lines or switching it to double quotes is enough to cause that,
// and neither would look like a change to this test's subject.
func TestNameShapeExtractionFindsTheDeclarations(t *testing.T) {
	shapes := declaredNameShapes(t)
	if len(shapes) == 0 {
		t.Fatal("no name-shape declarations were extracted; the declaration form has changed and every assertion in this file would be vacuous")
	}
	// A known declaration, named so a partial extraction is caught as well as a
	// total one.
	if _, ok := shapes[entitykind.Component]; !ok {
		var got []string
		for kind := range shapes {
			got = append(got, string(kind))
		}
		sort.Strings(got)
		t.Fatalf("the Component name shape was not extracted (found %v); the extraction is partial and the assertions below would under-report", got)
	}
}

// TestEveryKindTheEntityLintAdmitsHasANameShape holds the declared name shapes
// against every kind a document handed to the name rule can declare.
//
// The two sets sit on opposite sides of a language boundary — the documents a lint
// submits decide which kinds reach the linter, and the patterns are what the linter's
// name rule applies — so nothing structural connects them. Their disagreement is
// invisible by construction: a kind submitted to a rule that has no pattern for it
// produces no finding, which is indistinguishable from a document whose name is
// correct. This assertion is what makes that difference visible.
//
// The kinds held here are every entity kind, not only those the catalog walks
// examine. Linting one entity by name hands its document to every rule whatever its
// kind, and each rule decides for itself which kinds it describes, so a kind the
// catalog walks pass over still reaches the name rule on that path. Keying this on
// the catalog walks' kinds alone would leave exactly those kinds unwatched.
func TestEveryKindTheEntityLintAdmitsHasANameShape(t *testing.T) {
	// Entity kinds with no documented name shape, and why. A kind here may be
	// checked by other convention rules, but its name is checked by nothing.
	kindsWithoutNameShape := map[entitykind.Kind]string{
		entitykind.Aggregate: "an Aggregate is a developer's application-intent document; the documented name shapes are those of the catalog entity kinds, and none is documented for an Aggregate",
		entitykind.Plugin:    "a Plugin's name is its socket and identity segment, which the reference URI already constrains; no separate shape is documented for the metadata field",
		entitykind.Workload:  "a Workload is written inside a Recipe or a FeatureRecipe rather than addressed on its own, and carries no metadata name of its own to shape",
		entitykind.Manifest:  "a Manifest is an operator's deployment-target document rather than a catalog entity; no name shape is documented for it",
		entitykind.Release:   "a Release is an operator's environment-binding document rather than a catalog entity; no name shape is documented for it",
	}

	shapes := declaredNameShapes(t)
	admitted := entitykind.Kinds()
	if len(admitted) == 0 {
		t.Fatal("no entity kinds were enumerated; every assertion below would be vacuous")
	}

	held := map[entitykind.Kind]bool{}
	for _, kind := range admitted {
		held[kind] = true
		if _, ok := shapes[kind]; ok {
			if reason, named := kindsWithoutNameShape[kind]; named {
				t.Errorf("kind %s is recorded as having no documented name shape (%s), but one is now declared; remove the entry so the record stays truthful", kind, reason)
			}
			continue
		}
		if _, named := kindsWithoutNameShape[kind]; named {
			continue
		}
		t.Errorf("kind %s reaches the name rule when a document of it is linted by name, but declares no name shape and is not recorded as deliberately having none: "+
			"the name rule resolves no pattern for it and returns no finding, so every name of that kind reports clean whatever it is. "+
			"Either declare the shape here, or record the kind above with the reason it has none.", kind)
	}

	// An entry the loop above never reached is not a record of anything: it names a
	// kind no lint hands to the name rule, so it is stale — or the set the loop walks
	// has been narrowed below the kinds a lint actually admits.
	var unreached []string
	for kind, reason := range kindsWithoutNameShape {
		if !held[kind] {
			unreached = append(unreached, string(kind)+" ("+reason+")")
		}
	}
	if len(unreached) > 0 {
		sort.Strings(unreached)
		t.Errorf("these kinds are recorded as having no name shape but this assertion never held them: %v; "+
			"either the entry is stale and should be removed, or the kinds walked above no longer cover every kind a lint by name admits", unreached)
	}
}

// TestDeclaredNameShapesNameRealKinds runs the containment the other way.
//
// A declaration whose kind the vocabulary does not know resolves against nothing on
// every surface that dispatches by kind, so the pattern is never applied to anything
// — a misspelling produces a shape that is enforced against no document, and the
// enforcement it appears to add is imaginary.
func TestDeclaredNameShapesNameRealKinds(t *testing.T) {
	shapes := declaredNameShapes(t)
	for kind, pattern := range shapes {
		if !entitykind.IsKind(kind) {
			t.Errorf("a name shape is declared for %q, which is not a declared entity kind: pattern %q is applied to no document at all", kind, pattern)
		}
	}
}

// TestLibraryKindsShareOneNameShape holds the library kinds to a single shape.
//
// Each library kind declares its pattern separately, so that each can be pinned
// individually against its counterpart in the linter. That separation is what makes
// this assertion worth having: four independent declarations can drift apart one at a
// time, and an author reading any one of them would see nothing wrong. The shape is
// also declared on each library kind's structural envelope, so a divergence here
// would additionally put the author-time check and the parse-time check at odds for
// one kind while they agree for the rest.
func TestLibraryKindsShareOneNameShape(t *testing.T) {
	libraryKinds := []entitykind.Kind{
		entitykind.SchemaLibrary,
		entitykind.MappingLibrary,
		entitykind.WorkloadLibrary,
		entitykind.ArtifactLibrary,
	}

	// The library kinds are read from the vocabulary rather than trusted from the
	// list above, so a library kind added to the vocabulary and not to this test
	// fails rather than being silently left out of the comparison.
	var missing []string
	for _, kind := range entitykind.Kinds() {
		if !strings.HasSuffix(string(kind), "Library") {
			continue
		}
		found := false
		for _, known := range libraryKinds {
			if known == kind {
				found = true
				break
			}
		}
		if !found {
			missing = append(missing, string(kind))
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		t.Fatalf("these library kinds are not covered by this comparison: %v; add them so the shared shape is actually held across every library kind", missing)
	}

	shapes := declaredNameShapes(t)
	want, ok := shapes[entitykind.SchemaLibrary]
	if !ok {
		t.Fatal("no name shape is declared for SchemaLibrary; this comparison has nothing to hold the other library kinds against")
	}
	for _, kind := range libraryKinds {
		got, ok := shapes[kind]
		if !ok {
			t.Errorf("library kind %s declares no name shape, so its name is checked by nothing while every other library kind's is", kind)
			continue
		}
		if got != want {
			t.Errorf("library kind %s declares name shape %q but the library kinds share %q; one of the two is wrong and an author moving content between libraries would meet different rules", kind, got, want)
		}
	}
}
