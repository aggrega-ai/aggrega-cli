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

// A resolve's advisories are what the declared check raised, which is what makes
// the production path the framework's path rather than something that resembles
// it.
//
// The expectations are written out rather than compared against a second run of
// the framework, so the test says nothing that could be satisfied by the
// framework agreeing with itself. Four things fail it: taking the check out of
// the invocation, which empties the slice; changing the sequence away from the
// order the contract declares; re-typing the finding's message beside the rule's
// identity instead of deriving it; and dropping what the check raised the
// objection ABOUT, which is the half a registered message cannot carry — one
// static string per rule cannot name the parameter this advisory is about, so a
// message that were only that text would leave every unread parameter reading
// identically.
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
		registered := ErrUnreadParameter().Error()
		rendered := a.Error()
		if !strings.HasPrefix(rendered, registered) {
			t.Errorf("advisory = %q; want it to open with %q, the text the identity "+
				"was registered with", rendered, registered)
		}
		if n := strings.Count(rendered, registered); n != 1 {
			t.Errorf("advisory = %q carries the registered text %d times; want exactly 1 — "+
				"the composition reads it off the identity, so a value list carrying it too "+
				"states it twice", rendered, n)
		}
		parameter := strings.TrimPrefix(a.Field, ".spec.parameters.")
		if !strings.Contains(rendered, `"`+parameter+`"`) {
			t.Errorf("advisory = %q; want it to name %q, the parameter the check "+
				"raised about — a message that is only the registered text reads the same "+
				"for every unread parameter", rendered, parameter)
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
		for _, want := range []string{"entity-assertion-name-set", "Component document"} {
			if !strings.Contains(message, want) {
				t.Errorf("refusal does not name %q — the fact it cannot answer and the "+
					"document kind that carries it: %s", want, message)
			}
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
