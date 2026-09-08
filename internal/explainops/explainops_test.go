// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package explainops

import (
	"bytes"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/haltref"
	"github.com/aggrega-ai/aggrega-cli/pkg/docscache"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
)

// TestComposeReadsNothingForANumberThatIsSpent pins the laziness the read is
// taken as a function for.
//
// A retired number is answered from the retirement record alone, which is
// declared inside the binary. Reading the documentation to answer it would spend
// a registry round trip — and, on a workspace with no cached bundle, an
// ACQUISITION — to learn what the record already said. The count is the
// assertion: "it still answers" would pass on an implementation that read first
// and then discarded what it read.
func TestComposeReadsNothingForANumberThatIsSpent(t *testing.T) {
	t.Parallel()
	reads := 0
	_, err := Compose("E0157", func() (Pages, error) {
		reads++
		return Pages{}, nil
	})
	if err == nil {
		t.Fatal("a retired number was answered as a live condition")
	}
	if reads != 0 {
		t.Errorf("the published pages were read %d time(s) to answer a retired number; the retirement "+
			"record answers it and the read costs a round trip", reads)
	}
	if !strings.Contains(err.Error(), "retired") {
		t.Errorf("the refusal does not say the number is retired: %v", err)
	}
}

// TestComposeReadsThePagesOnceForACodeThatNeedsThem is the other half. Without it
// the count above is satisfied by an implementation that never reads at all, and
// the two pages have to arrive together: reading them separately is what would let
// one answer resolve a code against one version of the documentation and take its
// prose from another.
func TestComposeReadsThePagesOnceForACodeThatNeedsThem(t *testing.T) {
	t.Parallel()
	reads := 0
	explanation, err := Compose(probeShortCode(t), func() (Pages, error) {
		reads++
		return fixturePages(t), nil
	})
	if err != nil {
		t.Fatalf("a code the fixture resolves was refused: %v", err)
	}
	if reads != 1 {
		t.Errorf("the published pages were read %d time(s); both pages come from one read", reads)
	}
	if len(explanation.Entries) != 1 {
		t.Fatalf("expected the one entry the fixture documents; got %d", len(explanation.Entries))
	}
	if explanation.Note != nil {
		t.Errorf("a note was reported beside a walk-through that arrived: %v", explanation.Note)
	}
}

// TestWriteRendersNothingForARefusal pins the property that lets a surface write
// whatever Compose produced and then report its error, without deciding whether
// there was anything to write.
//
// The zero value is what a refusal returns, and a renderer that emitted its
// EMPTY fields would put a condition whose every value is blank in front of a
// reader who is about to be shown a refusal.
func TestWriteRendersNothingForARefusal(t *testing.T) {
	t.Parallel()
	var out, notes bytes.Buffer
	if err := Write(&out, &notes, Explanation{}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if out.Len() != 0 || notes.Len() != 0 {
		t.Errorf("a refusal rendered %d byte(s) of answer and %d of notes; both must be empty.\n"+
			"answer: %q\nnotes: %q", out.Len(), notes.Len(), out.String(), notes.String())
	}
}

// TestWriteKeepsTheConditionWhenTheWalkThroughIsMissing pins the division the
// whole composition exists around: the condition is the answer and does not
// depend on the documentation, so a missing walk-through costs the walk-through
// and not the reply.
//
// The two streams are asserted separately, and that is the property rather than a
// convenience: the reason travels on the notes stream so that a caller piping the
// answer somewhere gets the answer, and the reason is not mixed into it.
func TestWriteKeepsTheConditionWhenTheWalkThroughIsMissing(t *testing.T) {
	t.Parallel()
	explanation, err := Compose(probeShortCode(t), func() (Pages, error) {
		// The reference resolves the code and the halting-conditions page documents
		// no entry for it, which is the "undocumented condition" half.
		pages := fixturePages(t)
		pages.Halts = fixtureHaltsPage(t, false)
		return pages, nil
	})
	if err != nil {
		t.Fatalf("a condition with no entry was refused rather than reported: %v", err)
	}
	if explanation.Note == nil {
		t.Fatal("no note was reported for a condition the page does not document")
	}

	var out, notes bytes.Buffer
	if werr := Write(&out, &notes, explanation); werr != nil {
		t.Fatalf("Write: %v", werr)
	}
	if !strings.Contains(out.String(), probeShortCode(t)) {
		t.Errorf("the condition was not written, and it does not depend on the page:\n%s", out.String())
	}
	if !strings.Contains(notes.String(), "no entry for "+probeShortCode(t)) {
		t.Errorf("the note does not say which condition the page does not document:\n%s", notes.String())
	}
	if strings.Contains(out.String(), "no entry for") {
		t.Errorf("the reason the prose is missing was written onto the answer stream:\n%s", out.String())
	}
}

// probeCondition returns a condition that is really registered in THIS test
// binary, read off the registry rather than quoted.
//
// Both halves of that matter. Quoting a number would pin these cases to one
// package's allocation and turn a legitimate renumbering into a failure in a file
// that has nothing to do with it. And the registry is populated by the packages a
// binary actually links, so a condition declared in a package this one does not
// import is absent here however real it is elsewhere — measured, by a case that
// named one and reported it unregistered. The probe is therefore taken from a
// package this package already depends on for the read itself.
func probeCondition(t *testing.T) cerrors.Condition {
	t.Helper()
	code, ok := cerrors.CodeOf(docscache.ErrPageNotFound())
	if !ok {
		t.Fatal("the probe error reports no code, so these cases would assert nothing")
	}
	condition, err := haltref.Resolve(code)
	if err != nil {
		t.Fatalf("%s is not registered in this binary, so these cases would assert nothing: %v", code, err)
	}
	return condition
}

// probeShortCode is the probe condition's quotable code.
func probeShortCode(t *testing.T) string {
	t.Helper()
	return probeCondition(t).ShortCode
}

// fixturePages renders both published pages in the shape the generator writes,
// with the probe condition resolved and documented.
func fixturePages(t *testing.T) Pages {
	t.Helper()
	return Pages{Reference: fixtureReferencePage(t), Halts: fixtureHaltsPage(t, true)}
}

const fixtureHeading = "The probe condition"

func fixtureReferencePage(t *testing.T) []byte {
	t.Helper()
	condition := probeCondition(t)
	return []byte("# Error sentinels\n\n" +
		"## " + haltref.ExplanationHeading + "\n\n" +
		"<!-- BEGIN GENERATED: " + haltref.ReferenceRegionID + " -->\n" +
		"| Code | Condition | Message |\n| --- | --- | --- |\n" +
		"| `" + condition.ShortCode + "` | `" + condition.Code + "` | `" + condition.Message + "` |\n" +
		"<!-- END GENERATED: " + haltref.ReferenceRegionID + " -->\n")
}

// fixtureHaltsPage renders the halting-conditions page; documented selects
// whether its correspondence names the probe condition, which is what separates a
// condition the page documents from one it does not.
func fixtureHaltsPage(t *testing.T, documented bool) []byte {
	t.Helper()
	condition := probeCondition(t)
	row := "| `E0100` | `something else entirely` | " + fixtureHeading + " |\n"
	if documented {
		row = "| `" + condition.ShortCode + "` | `" + condition.Message + "` | " + fixtureHeading + " |\n"
	}
	return []byte("# Fatal Halts\n\n" +
		"### " + fixtureHeading + "\n\n" +
		"What to change.\n\n" +
		"## Finding a halt\n\n" +
		"<!-- BEGIN GENERATED: " + haltref.RegionID + " -->\n" +
		"| Short code | Opens with | Documented under |\n| --- | --- | --- |\n" +
		row +
		"<!-- END GENERATED: " + haltref.RegionID + " -->\n")
}
