// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
)

// presentRealAggregate builds the presentation of a real Aggregate: the document
// resolves through the same boundary every command resolves behind, and both
// halves the presentation carries are produced by the functions the resolve itself
// uses. Nothing here stands in for a compilation product.
//
// It returns the presentation, the document's path, and the resolve result, so a
// caller can hold the framework's reading and the advisory the resolve already
// produced side by side.
func presentRealAggregate(t *testing.T, fixture string) (constraint.DocumentView, string, *ResolveResult) {
	t.Helper()

	dir := filepath.Join("testdata", fixture)
	aggregatePath := filepath.Join(dir, "aggregate.yaml")

	result, err := resolveForTest(t, ResolveConfig{
		AggregatePath: aggregatePath,
		ReleasePath:   filepath.Join(dir, "release.yaml"),
		ManifestPath:  filepath.Join(dir, "manifest.yaml"),
	})
	if err != nil {
		t.Fatalf("resolve failed: %v", err)
	}

	raw, err := os.ReadFile(aggregatePath)
	if err != nil {
		t.Fatalf("read aggregate: %v", err)
	}
	contract, err := LoadAggregateParameterContractFromBytes(raw, aggregatePath)
	if err != nil {
		t.Fatalf("load parameter contract: %v", err)
	}

	return newAggregateDocumentView(aggregatePath, raw, contract), aggregatePath, result
}

// The framework invocation over a real compilation product.
//
// The assertion is the objection set, not merely that the call returned: a
// predicate that never objects, or a declaration taken out of the invocation,
// leaves this test with an empty set and it fails. That is what makes it a proof
// of invocation rather than of reachability.
func TestDeclaredParameterReadObjectsOverARealAggregate(t *testing.T) {
	t.Parallel()

	presented, aggregatePath, _ := presentRealAggregate(t, "typed-params-pass")

	declared := presented.DeclaredParameterKeys(aggregatePath)
	if want := []string{"cache_ttl_seconds", "display_name", "env"}; !equalStrings(declared, want) {
		t.Fatalf("DeclaredParameterKeys = %v; want %v", declared, want)
	}

	objections := constraint.RunOnDocumentParameters(
		aggregatePath, declared, presented, declaredParameterRead)

	if len(objections) != 1 {
		t.Fatalf("objections = %d (%v); want exactly 1, for the declared parameter no reference reads",
			len(objections), detailsOf(objections))
	}
	got := objections[0]
	if !errors.Is(got.Sentinel(), ErrUnreadParameter()) {
		t.Errorf("objection sentinel = %v; want ErrUnreadParameter", got.Sentinel())
	}
	if want := []string{"env"}; !equalStrings(got.Detail(), want) {
		t.Errorf("objection detail = %v; want %v", got.Detail(), want)
	}
	if want := ".spec.parameters.env"; got.Locator() != want {
		t.Errorf("objection locator = %q; want %q", got.Locator(), want)
	}
	if got.SourceFile() != aggregatePath {
		t.Errorf("objection source file = %q; want %q", got.SourceFile(), aggregatePath)
	}
	// The two parameters the Aggregate's own text reads must not be objected
	// about, or the reading is inverted and every document would light up.
	for _, d := range detailsOf(objections) {
		if strings.Contains(d, "cache_ttl_seconds") || strings.Contains(d, "display_name") {
			t.Errorf("objected about a parameter the text reads: %s", d)
		}
	}
}

// handWrittenReading runs the hand-written function over the same fixture the
// presentation is built from, through the same two loads the resolve performs:
// the file's bytes, and the contract loaded from those bytes.
//
// It reads the document itself rather than taking the resolve's findings, which
// is what keeps it an independent side — see the cross-check below for why that
// matters.
func handWrittenReading(t *testing.T, fixture, aggregateName string) []*ResolverError {
	t.Helper()

	aggregatePath := filepath.Join("testdata", fixture, "aggregate.yaml")
	raw, err := os.ReadFile(aggregatePath)
	if err != nil {
		t.Fatalf("read aggregate: %v", err)
	}
	contract, err := LoadAggregateParameterContractFromBytes(raw, aggregatePath)
	if err != nil {
		t.Fatalf("load parameter contract: %v", err)
	}
	return LintUnreadParameters(raw, contract, aggregateName)
}

// The framework's reading and the hand-written reading beside it must agree
// about the same document.
//
// 🔴 It calls the hand-written function DIRECTLY, and that is the whole point of
// the test rather than an incidental choice. The resolve now produces its
// advisories THROUGH the framework, so a comparison against the resolve's own
// findings would compare the framework with itself: it would pass forever while
// proving nothing about coverage.
//
// The hand-written function is therefore a deliberate near-duplicate, kept as an
// independent cross-check. It must not be folded into the declared check or
// re-expressed in terms of it: a cross-check sharing an implementation with what
// it checks cannot see the two drift apart. What the two legitimately share is
// one definition of what counts as a read — both reach the same reference census
// — and what is compared here are their conclusions.
//
// The two sides report in different ORDERS, and that follows from each side's own
// contract rather than from an implementation slip: the hand-written function
// sorts by parameter key, as its own documentation says, while the framework
// invokes the check once per presented name and raises in that order, and the
// names are presented in the order the contract declares them. Coverage is the
// claim that matters, so the finding SET is asserted equal; each sequence is
// asserted against its own written expectation as well, so the difference is
// pinned rather than left to be rediscovered.
func TestDeclaredParameterReadAgreesWithTheHandWrittenReading(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		fixture string
		// authorOrder is the sequence the framework raises in: the order the
		// contract declares the parameters.
		authorOrder []string
		// sortedOrder is the sequence the hand-written function reports in:
		// sorted by parameter key.
		sortedOrder []string
		// ordersDiffer records whether this row is able to tell the two apart at
		// all, and is asserted rather than trusted.
		ordersDiffer bool
	}{
		{
			fixture:     "typed-params-pass",
			authorOrder: []string{".spec.parameters.env"},
			sortedOrder: []string{".spec.parameters.env"},
		},
		{
			// Two unread declarations, authored against their alphabetical order.
			// With a single finding every ordering agrees, which is exactly why
			// the row above cannot carry the ordering claim and this one can.
			fixture:      "unread-params-multi",
			authorOrder:  []string{".spec.parameters.zone", ".spec.parameters.alpha"},
			sortedOrder:  []string{".spec.parameters.alpha", ".spec.parameters.zone"},
			ordersDiffer: true,
		},
	} {
		t.Run(tc.fixture, func(t *testing.T) {
			t.Parallel()

			if tc.ordersDiffer && equalStrings(tc.authorOrder, tc.sortedOrder) {
				t.Fatalf("this row's two expected sequences are identical (%v), so it cannot "+
					"detect an ordering change at all", tc.authorOrder)
			}

			presented, aggregatePath, result := presentRealAggregate(t, tc.fixture)

			objections := constraint.RunOnDocumentParameters(
				aggregatePath,
				presented.DeclaredParameterKeys(aggregatePath),
				presented,
				declaredParameterRead,
			)
			fromFramework := make([]string, 0, len(objections))
			for _, o := range objections {
				fromFramework = append(fromFramework, o.Locator())
			}
			fromHandWritten := fieldsOf(
				handWrittenReading(t, tc.fixture, result.Aggregate.Metadata.Name))

			if len(fromHandWritten) == 0 {
				t.Fatal("the hand-written reading found nothing, so the comparison below would " +
					"pass over an empty set")
			}

			// Coverage: the two readings find the same parameters at the same
			// locators. Compared as sets, because the sequences differ by contract
			// and are asserted separately.
			frameworkSet := sortedCopy(fromFramework)
			handWrittenSet := sortedCopy(fromHandWritten)
			if !equalStrings(frameworkSet, handWrittenSet) {
				t.Errorf("the two readings disagree about WHICH parameters are unread:\n"+
					"  framework:    %v\n  hand-written: %v", frameworkSet, handWrittenSet)
			}

			if !equalStrings(fromFramework, tc.authorOrder) {
				t.Errorf("framework sequence = %v; want %v, the order the contract declares",
					fromFramework, tc.authorOrder)
			}
			if !equalStrings(fromHandWritten, tc.sortedOrder) {
				t.Errorf("hand-written sequence = %v; want %v, sorted by parameter key",
					fromHandWritten, tc.sortedOrder)
			}
		})
	}
}

// A resolve's advisories are what the declared check raised, which is what makes
// the production path the framework's path rather than something that resembles
// it.
//
// The expectations are written out rather than compared against a second run of
// the framework, so the test says nothing that could be satisfied by the
// framework agreeing with itself. Three things fail it: taking the check out of
// the invocation, which empties the slice; changing the sequence away from the
// order the contract declares; and re-typing the finding's message beside the
// rule's identity instead of deriving it, which is what the message assertion
// pins.
func TestResolveAdvisoriesAreWhatTheDeclaredCheckRaised(t *testing.T) {
	t.Parallel()

	result, err := resolveForTest(t, ResolveConfig{
		AggregatePath: filepath.Join("testdata", "unread-params-multi", "aggregate.yaml"),
		ReleasePath:   filepath.Join("testdata", "unread-params-multi", "release.yaml"),
		ManifestPath:  filepath.Join("testdata", "unread-params-multi", "manifest.yaml"),
	})
	if err != nil {
		t.Fatalf("resolve failed: %v", err)
	}

	want := []string{".spec.parameters.zone", ".spec.parameters.alpha"}
	if got := fieldsOf(result.LintAdvisories); !equalStrings(got, want) {
		t.Fatalf("resolve advisories = %v; want %v, the order the contract declares", got, want)
	}
	for _, a := range result.LintAdvisories {
		if !errors.Is(a, ErrUnreadParameter()) {
			t.Errorf("advisory does not carry the rule's identity: %v", a)
		}
		if want := ErrUnreadParameter().Error(); a.Message != want {
			t.Errorf("advisory message = %q; want %q, the text the identity was registered with",
				a.Message, want)
		}
		if !strings.Contains(a.Entity, "unread-params-multi") {
			t.Errorf("Entity does not name the Aggregate: %q", a.Entity)
		}
	}
}

// A presentation carries one document, so an accessor asked about another refuses.
// Returning this document's fact under another document's name is the failure the
// refusal exists to prevent, and it would be silent.
func TestAggregateDocumentViewRefusesAnotherDocument(t *testing.T) {
	t.Parallel()

	presented, aggregatePath, _ := presentRealAggregate(t, "typed-params-pass")

	for _, accessor := range []struct {
		name string
		call func()
	}{
		{"RawAggregateText", func() { presented.RawAggregateText("other/aggregate.yaml") }},
		{"DeclaredParameterKeys", func() { presented.DeclaredParameterKeys("other/aggregate.yaml") }},
	} {
		t.Run(accessor.name, func(t *testing.T) {
			defer func() {
				recovered := recover()
				if recovered == nil {
					t.Fatalf("%s answered about a document it does not present", accessor.name)
				}
				message, ok := recovered.(string)
				if !ok {
					t.Fatalf("refusal is not a string: %#v", recovered)
				}
				for _, want := range []string{accessor.name, "other/aggregate.yaml", aggregatePath} {
					if !strings.Contains(message, want) {
						t.Errorf("refusal does not name %q: %s", want, message)
					}
				}
			}()
			accessor.call()
		})
	}
}

// The fact an Aggregate cannot carry is refused rather than answered emptily. An
// empty set is the truthful answer for an entity declaring no assertions, so
// answering emptily here would be indistinguishable from that and permanently
// wrong.
func TestAggregateDocumentViewRefusesEntityAssertionNames(t *testing.T) {
	t.Parallel()

	presented, aggregatePath, _ := presentRealAggregate(t, "typed-params-pass")

	defer func() {
		recovered := recover()
		if recovered == nil {
			t.Fatal("EntityAssertionNameSet answered for an Aggregate, which declares no " +
				"entity inheritance chain")
		}
		message, ok := recovered.(string)
		if !ok {
			t.Fatalf("refusal is not a string: %#v", recovered)
		}
		if !strings.Contains(message, "inheritance chain") {
			t.Errorf("refusal does not say why it cannot answer: %s", message)
		}
	}()
	presented.EntityAssertionNameSet(aggregatePath)
}

// detailsOf returns each objection's detail values joined, in the order raised.
func detailsOf(objections []constraint.Objection) []string {
	out := make([]string, 0, len(objections))
	for _, o := range objections {
		out = append(out, strings.Join(o.Detail(), ","))
	}
	return out
}
