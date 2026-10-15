// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package analyzer

import (
	"context"
	"testing"

	"github.com/aggrega-ai/aggrega-abi-go/pkg/abi"

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
func advisoriesRaisedOver(t *testing.T, nodes enumeratedNodes, detail ...string) []abi.Diagnostic {
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

// advisoryMessagesRaisedOver is advisoriesRaisedOver reduced to the displayed
// sentences, so the whole-string equalities below read the line a terminal
// prints and nothing else.
func advisoryMessagesRaisedOver(t *testing.T, nodes enumeratedNodes, detail ...string) []string {
	t.Helper()
	raised := advisoriesRaisedOver(t, nodes, detail...)
	out := make([]string, 0, len(raised))
	for _, d := range raised {
		out = append(out, d.Message)
	}
	return out
}

// TestAnAdvisoryNamesItsDocumentItsKeyAndItsFinding pins the whole sentence an
// advisory displays, for each shape of provenance a finding can carry.
//
// The assertions are whole-string equalities rather than substring checks, and
// that is the point of this test existing at all. The displayed sentence is what
// a terminal has to work with: its document, its authored key and its wording are
// one string there, so every defect in how those three are composed — a missing
// separator, a swapped order, a dropped half — leaves each part still present and
// a substring check still green.
//
// The entry's own position field is asserted separately, by
// [TestAnAdvisoryCarriesItsAuthoredKeyOnItsLocation]. Splitting the two is what
// keeps this one honest about what it covers: an entry could carry a correct
// location and a mangled sentence, or the reverse, and one assertion over both
// would let either defect hide behind the other half passing.
func TestAnAdvisoryNamesItsDocumentItsKeyAndItsFinding(t *testing.T) {
	t.Parallel()

	const registered = "annotation contribution overridden by an explicit value"

	t.Run("a finding carrying both reads as a document, a key and the wording", func(t *testing.T) {
		t.Parallel()
		got := advisoryMessagesRaisedOver(t, enumeratedNodes{{
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
		got := advisoryMessagesRaisedOver(t, enumeratedNodes{{
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
		got := advisoryMessagesRaisedOver(t, enumeratedNodes{{
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
		got := advisoryMessagesRaisedOver(t, enumeratedNodes{
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
			t.Errorf("advisories = %+v, want nil", got)
		}
	})
}

// TestAnAdvisoryPositionReadsAsAFileAndAKey pins the composition of the two
// halves an advisory's DISPLAYED position is built from.
//
// The displayed position is composed into the sentence, which is why the join
// matters: a locator opens with a separator, so running the pair together
// produces one string that reads as a single file name with a nonsense extension
// and gives a reader no way to see where the document ends and the authored key
// begins.
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

// TestAnAdvisoryCarriesItsAuthoredKeyOnItsLocation proves the half a displayed
// sentence cannot deliver: the authored key as a field a consumer reads, so a
// surface holding the parsed document walks to the offending key instead of
// placing the finding on the line that names the whole document.
//
// It is asserted alongside the sentence in every case rather than instead of it,
// because carrying the key structurally is only correct if the printed line is
// untouched — a conversion that moved the position out of the message would
// satisfy the location assertion and change what a build prints.
func TestAnAdvisoryCarriesItsAuthoredKeyOnItsLocation(t *testing.T) {
	t.Parallel()

	const registered = "annotation contribution overridden by an explicit value"

	t.Run("a finding naming an authored key carries it as a field and in the sentence", func(t *testing.T) {
		t.Parallel()
		got := advisoriesRaisedOver(t, enumeratedNodes{{
			ID:                   "backend",
			SourceFile:           "/workspace/aggregate.yaml",
			AuthoredName:         "backend",
			SubsystemPath:        "default",
			SynthesizedSubsystem: true,
		}}, "replicas")
		if len(got) != 1 {
			t.Fatalf("advisories = %d, want 1", len(got))
		}
		if got[0].Location == nil {
			t.Fatalf("the advisory carries no location; the rule computed an authored key, so the position was available and was lost")
		}
		if want := ".spec.components.backend"; got[0].Location.KeyPath != want {
			t.Errorf("key path = %q, want %q", got[0].Location.KeyPath, want)
		}
		if want := `/workspace/aggregate.yaml at .spec.components.backend: ` + registered + ` ("replicas")`; got[0].Message != want {
			t.Errorf("message = %q, want %q — the displayed line must not move when the key is carried structurally", got[0].Message, want)
		}
		if got[0].Severity != abi.SeverityWarn {
			t.Errorf("severity = %q, want %q", got[0].Severity, abi.SeverityWarn)
		}
	})

	t.Run("a finding with no document still carries the authored key", func(t *testing.T) {
		t.Parallel()
		got := advisoriesRaisedOver(t, enumeratedNodes{{
			ID:                   "backend",
			AuthoredName:         "backend",
			SubsystemPath:        "default",
			SynthesizedSubsystem: true,
		}}, "replicas")
		if len(got) != 1 || got[0].Location == nil {
			t.Fatalf("advisories = %+v; want one carrying a location, because the key is what a consumer navigates by and the document is not", got)
		}
		if want := ".spec.components.backend"; got[0].Location.KeyPath != want {
			t.Errorf("key path = %q, want %q", got[0].Location.KeyPath, want)
		}
	})

	// A finding about a component the author never named still has a position,
	// because the container block holding that component is itself a key the
	// document spells. The locator gives up only the segment it cannot name and
	// keeps the ones it can, so what reaches the entry is the most specific
	// authored key that exists rather than nothing at all.
	//
	// Withholding the location here is the tempting reading and it is wrong: an
	// entry carrying no location lands on the line naming the whole document, which
	// says nothing about where in it the finding is. A container key resolves, so
	// it beats that line even though it is coarser than a leaf. The
	// case that genuinely carries no location is the one whose provenance names
	// no file and no locator, where there is no key to walk to at all — a
	// different input, not a coarser one.
	//
	// The halting side of the boundary positions the same finding at the same
	// container key, so withholding it on this side would also split one
	// provenance into two different positions depending on severity.
	t.Run("a finding naming no component carries the container key the document does spell", func(t *testing.T) {
		t.Parallel()
		got := advisoriesRaisedOver(t, enumeratedNodes{{ID: "backend"}}, "replicas")
		if len(got) != 1 {
			t.Fatalf("advisories = %d, want 1", len(got))
		}
		if got[0].Location == nil {
			t.Fatalf("the advisory carries no location; the container block is an authored key a consumer can walk to, so the position was available and was lost")
		}
		if want := ".spec.components"; got[0].Location.KeyPath != want {
			t.Errorf("key path = %q, want %q", got[0].Location.KeyPath, want)
		}
		if want := `.spec.components: ` + registered + ` ("replicas")`; got[0].Message != want {
			t.Errorf("message = %q, want %q — the displayed line must not move when the key is carried structurally", got[0].Message, want)
		}
	})
}
