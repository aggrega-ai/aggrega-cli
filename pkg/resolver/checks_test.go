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

// The framework's reading and the reading the resolve already produces must agree
// about the same document.
//
// This duplicates neither side's definition of a read: both reach one census, and
// this compares their conclusions. It is kept as an independent cross-check rather
// than folded into the assertion above, because a cross-check that shares a
// definition with what it checks cannot see the two drift apart.
func TestDeclaredParameterReadAgreesWithTheAdvisoryTheResolveProduces(t *testing.T) {
	t.Parallel()

	presented, aggregatePath, result := presentRealAggregate(t, "typed-params-pass")

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
	fromResolve := fieldsOf(result.LintAdvisories)

	if len(fromResolve) == 0 {
		t.Fatal("the resolve produced no advisory, so there is nothing to agree with and this " +
			"comparison would pass over an empty set")
	}
	if !equalStrings(fromFramework, fromResolve) {
		t.Errorf("locators disagree: framework %v, resolve %v", fromFramework, fromResolve)
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
