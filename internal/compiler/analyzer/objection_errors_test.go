// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package analyzer

import (
	"context"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
)

// enumeratedNodes is a node axis a proof names outright, so an advisory's
// provenance can be varied one field at a time.
type enumeratedNodes []constraint.NodeInstance

func (n enumeratedNodes) Nodes() []constraint.NodeInstance { return n }

// objectsAboutEveryNode raises once per node it is invoked for, carrying the
// supplied detail. It exists so the objections below come from the framework's
// own raise path rather than from a value a test assembled: an objection's
// provenance is what the conversion under proof reads, and a hand-built one
// would be the test's account of that provenance rather than the framework's.
func objectsAboutEveryNode(detail ...string) constraint.Check[constraint.GraphView, constraint.NodeSubject] {
	return constraint.Check[constraint.GraphView, constraint.NodeSubject]{
		Sentinel: ErrOverriddenAnnotation(),
		Severity: constraint.SeverityWarning,
		Run: func(_ constraint.GraphView, s constraint.NodeSubject) []constraint.Objection {
			return []constraint.Objection{s.Raise(ErrOverriddenAnnotation(), detail...)}
		},
	}
}

// advisoriesRaisedOver runs the check above over the supplied node axis and
// converts what it raised, which is the whole path an advisory takes from a
// rule to the warning slice a caller reads.
func advisoriesRaisedOver(t *testing.T, nodes enumeratedNodes, detail ...string) []string {
	t.Helper()
	objections, err := constraint.RunOnNodes(
		context.Background().Err,
		nodes,
		constraint.GraphView(nil),
		objectsAboutEveryNode(detail...),
	)
	if err != nil {
		t.Fatalf("traversal: %v", err)
	}
	if len(objections) != len(nodes) {
		t.Fatalf("the check was invoked %d time(s) for %d node(s); the conversion would be proved over the wrong set", len(objections), len(nodes))
	}
	return AdvisoriesFromObjections(objections)
}

// TestAnAdvisoryNamesItsDocumentItsKeyAndItsFinding pins the whole string an
// advisory reaches a caller as, for each shape of provenance a finding can
// carry.
//
// The assertions are whole-string equalities rather than substring checks, and
// that is the point of this test existing at all. A warning has no fields: its
// document, its authored key and its wording are one string by the time anything
// reads it, so every defect in how those three are composed — a missing
// separator, a swapped order, a dropped half — leaves each part still present
// and a substring check still green.
func TestAnAdvisoryNamesItsDocumentItsKeyAndItsFinding(t *testing.T) {
	t.Parallel()

	const registered = "annotation contribution overridden by an explicit value"

	t.Run("a finding carrying both reads as a document, a key and the wording", func(t *testing.T) {
		t.Parallel()
		got := advisoriesRaisedOver(t, enumeratedNodes{{
			ID:                   "backend",
			SourceFile:           "/workspace/aggregate.yaml",
			AuthoredName:         "backend",
			SubsystemPath:        "default",
			SynthesizedSubsystem: true,
		}}, "replicas")
		want := `/workspace/aggregate.yaml at .spec.components.backend: ` + registered + ` ("replicas")`
		if len(got) != 1 || got[0] != want {
			t.Errorf("advisories = %q, want [%q]", got, want)
		}
	})

	t.Run("a finding with no document names the key alone", func(t *testing.T) {
		t.Parallel()
		got := advisoriesRaisedOver(t, enumeratedNodes{{
			ID:                   "backend",
			AuthoredName:         "backend",
			SubsystemPath:        "default",
			SynthesizedSubsystem: true,
		}}, "replicas")
		want := `.spec.components.backend: ` + registered + ` ("replicas")`
		if len(got) != 1 || got[0] != want {
			t.Errorf("advisories = %q, want [%q]", got, want)
		}
	})

	t.Run("a finding carrying no detail reads as the wording alone", func(t *testing.T) {
		t.Parallel()
		got := advisoriesRaisedOver(t, enumeratedNodes{{
			ID:                   "backend",
			SourceFile:           "/workspace/aggregate.yaml",
			AuthoredName:         "backend",
			SubsystemPath:        "default",
			SynthesizedSubsystem: true,
		}})
		want := `/workspace/aggregate.yaml at .spec.components.backend: ` + registered
		if len(got) != 1 || got[0] != want {
			t.Errorf("advisories = %q, want [%q]", got, want)
		}
	})

	t.Run("advisories keep the order the rule raised them in", func(t *testing.T) {
		t.Parallel()
		got := advisoriesRaisedOver(t, enumeratedNodes{
			{ID: "b", SourceFile: "/workspace/aggregate.yaml", AuthoredName: "b", SubsystemPath: "default", SynthesizedSubsystem: true},
			{ID: "a", SourceFile: "/workspace/aggregate.yaml", AuthoredName: "a", SubsystemPath: "default", SynthesizedSubsystem: true},
		}, "replicas")
		want := []string{
			`/workspace/aggregate.yaml at .spec.components.b: ` + registered + ` ("replicas")`,
			`/workspace/aggregate.yaml at .spec.components.a: ` + registered + ` ("replicas")`,
		}
		if len(got) != len(want) {
			t.Fatalf("advisories = %q, want %q", got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Errorf("advisory %d = %q, want %q", i, got[i], want[i])
			}
		}
	})

	t.Run("nothing raised converts to nothing", func(t *testing.T) {
		t.Parallel()
		if got := AdvisoriesFromObjections(nil); got != nil {
			t.Errorf("advisories = %q, want nil", got)
		}
	})
}

// TestAnAdvisoryPositionReadsAsAFileAndAKey pins the composition of the two
// halves an advisory's position is built from.
//
// A warning is a plain string, so its position is composed into the text rather
// than carried on fields of its own — which is exactly why the join matters: a
// locator opens with a separator, so running the pair together produces one
// string that reads as a single file name with a nonsense extension and gives a
// reader no way to see where the document ends and the authored key begins.
func TestAnAdvisoryPositionReadsAsAFileAndAKey(t *testing.T) {
	t.Parallel()

	for _, c := range []struct {
		name       string
		sourceFile string
		locator    string
		want       string
	}{
		{
			"a finding carrying both names the key inside the document",
			"/workspace/aggregate.yaml",
			".spec.components.backend.uses.databases",
			"/workspace/aggregate.yaml at .spec.components.backend.uses.databases",
		},
		{
			"a finding whose authored key cannot be named still names the document",
			"/workspace/aggregate.yaml",
			"",
			"/workspace/aggregate.yaml",
		},
		{
			"a finding with no document names the key alone",
			"",
			".spec.components.backend",
			".spec.components.backend",
		},
		{
			"a finding carrying neither has no position at all",
			"",
			"",
			"",
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if got := advisoryPosition(c.sourceFile, c.locator); got != c.want {
				t.Errorf("position = %q, want %q", got, c.want)
			}
		})
	}
}
