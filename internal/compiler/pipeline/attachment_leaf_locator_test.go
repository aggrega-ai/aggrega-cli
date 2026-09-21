// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package pipeline

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/analyzer"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
)

// The documents the locator proof compiles.
//
// The two Releases are separate files because the shapes are mutually
// exclusive: one routing its components at the top level declares no boundary,
// and one nesting them under boundaries declares nothing at the top level. A
// single fixture could show only one of the two.
//
// They share one Aggregate, and it is one that pulls in no module. That is
// forced rather than chosen: a Release routing components at its own top
// level has no surface on which to place a module, so an Aggregate carrying
// one cannot be deployed in that form at all. Sharing the Aggregate keeps the
// difference between the two compilations the Release shape and nothing else.
const (
	locatorAggregatePath         = "../../../internal/compiler/testdata/fullstack-app/aggregates/annotation-release-side.yaml"
	locatorFlatReleasePath       = "../../../internal/compiler/testdata/fullstack-app/releases/attachment-locator-flat.yaml"
	locatorBoundariedReleasePath = "../../../internal/compiler/testdata/fullstack-app/releases/attachment-locator-boundaried.yaml"
)

// leafPosition is one invocation of the attachment-pillar-leaf traversal reduced
// to the position it carried.
type leafPosition struct {
	node       string
	attachment string
	pillar     string
	sourceFile string
	locator    string
}

func (p leafPosition) String() string {
	return fmt.Sprintf("%s|%s", p.sourceFile, p.locator)
}

// collectLeafPositions runs the framework's own attachment-pillar-leaf traversal
// over a real compilation and returns the position every invocation carried.
//
// It goes through the traversal rather than reading the enumerator, because the
// position a rule receives is assembled by the traversal: an assertion over the
// enumerator's return value would pass on a traversal that then spelled the
// position some other way.
func collectLeafPositions(
	t *testing.T,
	releasePath string,
) []leafPosition {
	t.Helper()

	fixture := workspaceFixture{
		aggregatePath: locatorAggregatePath,
		releasePath:   releasePath,
		manifestPath:  invocationManifestPath,
		catalogPath:   invocationCatalogPath,
	}
	out, catalogLookup := compileWorkspace(t, fixture, bytesOf(t, locatorAggregatePath))
	coordinates := coordinatesOf(t, out, catalogLookup)

	var seen []leafPosition
	_, err := constraint.RunOnNodeAttachmentPillarLeaves(
		nil,
		coordinates,
		coordinates,
		coordinates,
		coordinates,
		out.GraphView,
		constraint.Check[constraint.GraphView, constraint.NodeAttachmentPillarLeafSubject]{
			Sentinel: analyzer.ErrOverriddenAnnotation(),
			Severity: constraint.SeverityWarning,
			Run: func(_ constraint.GraphView, s constraint.NodeAttachmentPillarLeafSubject) []constraint.Objection {
				seen = append(seen, leafPosition{
					node:       s.NodeID(),
					attachment: s.AttachmentName(),
					pillar:     s.PillarName(),
					sourceFile: s.SourceFile(),
					locator:    s.Locator(),
				})
				return nil
			},
		},
	)
	if err != nil {
		t.Fatalf("the traversal returned %v over a walk nothing cancelled", err)
	}
	if len(seen) == 0 {
		t.Fatalf("the traversal was invoked zero times over %s; a rule over this kind would report "+
			"exactly what a rule that ran and approved reports", releasePath)
	}
	return seen
}

// resolveInDocument opens a document and walks a dotted locator through it,
// reporting whether the locator names a key the document actually contains,
// what the walk reached before giving up, and the keys that were available
// there.
//
// This is the assertion the whole proof turns on, and it is a walk rather than a
// string comparison for a reason: the class of defect here is a position that
// reads plausibly and resolves nowhere, and every locator that has ever been
// wrong looked exactly like one that was right. Comparing the string against an
// expected string only moves the question to whether the expectation is correct.
func resolveInDocument(t *testing.T, path, locator string) (bool, string, []string) {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var doc map[string]any
	if err := yaml.Unmarshal(data, &doc); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}

	var cur any = doc
	var reached []string
	for _, segment := range strings.Split(strings.TrimPrefix(locator, "."), ".") {
		mapping, isMapping := cur.(map[string]any)
		if !isMapping {
			return false, "." + strings.Join(reached, "."), nil
		}
		next, present := mapping[segment]
		if !present {
			return false, "." + strings.Join(reached, "."), keysOf(mapping)
		}
		cur = next
		reached = append(reached, segment)
	}
	return true, locator, nil
}

func keysOf(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// assertResolves fails unless the locator names a key the document contains.
func assertResolves(t *testing.T, form string, p leafPosition) {
	t.Helper()
	if p.sourceFile == "" {
		t.Errorf("%s: leaf %q on node %q carries no file; nothing could be opened at all", form, p.locator, p.node)
		return
	}
	if p.locator == "" {
		t.Errorf("%s: leaf on node %q in %q carries no locator", form, p.node, p.sourceFile)
		return
	}
	ok, reached, available := resolveInDocument(t, p.sourceFile, p.locator)
	if ok {
		return
	}
	t.Errorf("%s: the position %s names no key the document contains.\n"+
		"  the walk stopped after %q; the keys there are %v\n"+
		"INVARIANT: a locator names a site the author actually wrote.\n"+
		"CONSEQUENCE: a reader who trusts the filename follows a path that resolves nowhere inside it, "+
		"which is a worse failure than no position at all.",
		form, p, reached, available)
}

// TestAttachmentLeafLocatorResolvesInTheDocumentItNames is the proof that an
// attachment leaf's position is a place that exists.
//
// Three authoring forms, and the third is the one the proof exists for. A
// Release routing its components at the top level writes an attachment under
// `.spec.components`; one nesting them under boundaries writes it under the
// boundary chain; and one attaching a feature to a boundary itself writes no
// component key at all. The first is the shape a position built from the node's
// own segments happens to match, so a corpus of only that form passes with the
// defect fully intact.
//
// Every position is checked by opening the document and walking to it. The
// defect class is a position that reads plausibly and resolves nowhere, which no
// assertion on the position string can catch — an expected string is just a
// second copy of whatever the code produces, right or wrong.
func TestAttachmentLeafLocatorResolvesInTheDocumentItNames(t *testing.T) {
	t.Run("the walker reports a position that resolves nowhere", func(t *testing.T) {
		// The instrument is calibrated before anything is measured with it. A
		// walk that silently returned true would pass every case below while
		// checking nothing, and the shape planted here is the exact one this
		// change removes: a component-rooted path against a document that
		// nests its components under boundaries.
		const planted = ".spec.components.app1.features.cross-namespace-routing.infra.target_var"
		ok, reached, available := resolveInDocument(t, locatorBoundariedReleasePath, planted)
		if ok {
			t.Fatalf("the walker resolved %q in a document that nests its components under boundaries; "+
				"it cannot distinguish a good position from a bad one and no result below means anything", planted)
		}
		if reached != ".spec" {
			t.Errorf("the walk stopped after %q, want %q; the planted position is not failing where it should",
				reached, ".spec")
		}
		if len(available) == 0 {
			t.Error("the walker reported no available keys at the point it stopped; a failure it produces would not say why")
		}
	})

	t.Run("a Release with no boundaries", func(t *testing.T) {
		positions := collectLeafPositions(t, locatorFlatReleasePath)
		var infra []leafPosition
		for _, p := range positions {
			assertResolves(t, "flat", p)
			if p.pillar == "infra" {
				infra = append(infra, p)
			}
		}
		if len(infra) == 0 {
			t.Fatal("no infrastructure leaf was enumerated; the Release-side shape this subtest exists for was never reached")
		}
		// Stated as well as walked, because the walk alone cannot say the
		// position names the *right* site: the generated scope holding a
		// top-level component is a key this document does not contain, so a
		// position naming it would fail the walk, but a position naming some
		// other real key would pass it.
		const want = ".spec.components.app1.features.cross-namespace-routing.infra.target_var"
		if got := infra[0].locator; got != want {
			t.Errorf("locator = %q, want %q", got, want)
		}
	})

	t.Run("a Release nesting its components under boundaries", func(t *testing.T) {
		positions := collectLeafPositions(t, locatorBoundariedReleasePath)

		var componentScoped, boundaryWide []leafPosition
		for _, p := range positions {
			assertResolves(t, "boundaried", p)
			switch p.attachment {
			case "cross-namespace-routing":
				componentScoped = append(componentScoped, p)
			case "traffic-shaping":
				boundaryWide = append(boundaryWide, p)
			}
		}

		if len(componentScoped) == 0 {
			t.Fatal("no leaf of the component-scoped attachment was enumerated")
		}
		const wantComponentScoped = ".spec.boundaries.edge.boundaries.web.components.app1" +
			".features.cross-namespace-routing.infra.target_var"
		if got := componentScoped[0].locator; got != wantComponentScoped {
			t.Errorf("component-scoped locator = %q, want %q", got, wantComponentScoped)
		}

		// The case a component-rooted position cannot express at all. It is the
		// acceptance rather than an edge of it: an attachment written on the
		// boundary names no component, so there is no component segment to
		// spell and inventing one would name a key the document does not
		// contain — while still reading like an ordinary position.
		if len(boundaryWide) == 0 {
			t.Fatal("no leaf of the boundary-wide attachment was enumerated; the case this proof exists for was never reached")
		}
		const wantBoundaryWide = ".spec.boundaries.edge.boundaries.web" +
			".features.traffic-shaping.infra.shaping_mode"
		for _, p := range boundaryWide {
			if p.locator != wantBoundaryWide {
				t.Errorf("boundary-wide locator on node %q = %q, want %q", p.node, p.locator, wantBoundaryWide)
			}
			if strings.Contains(p.locator, ".components.") {
				t.Errorf("boundary-wide locator on node %q names a component: %q.\n"+
					"INVARIANT: an attachment written on a boundary names no component.\n"+
					"CONSEQUENCE: the position points at one of the many places the attachment takes "+
					"effect rather than at the one place anyone can edit it.", p.node, p.locator)
			}
		}

		// The boundary-wide attachment reaches every component the boundary
		// holds, so it is enumerated once per node and every one of those
		// invocations names the same single editable site. One node alone
		// could not show that the position is the scope's rather than the
		// node's.
		nodes := map[string]bool{}
		for _, p := range boundaryWide {
			nodes[p.node] = true
		}
		if len(nodes) < 2 {
			t.Errorf("the boundary-wide attachment was enumerated on %d node(s); it reaches every component "+
				"the boundary holds, so a single node cannot show the position is the scope's", len(nodes))
		}
	})
}
