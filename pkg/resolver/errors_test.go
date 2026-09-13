// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver_test

import (
	stderrors "errors"
	"strings"
	"testing"

	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
)

// TestResolverError_ToCompilerError_AggregateRoutesToDeveloper verifies
// that a ResolverError with Entity prefix "Aggregate" converts to a
// CompilerError with DeveloperError category. Covers the developer-side
// of the structured-error coverage.
func TestResolverError_ToCompilerError_AggregateRoutesToDeveloper(t *testing.T) {
	re := &resolver.ResolverError{
		Entity:  "Aggregate my-app",
		Field:   ".spec.components.api.values.replicas",
		Message: "replicas must be an integer",
		Err:     resolver.ErrSchemaConformance(),
	}
	ce := re.ToCompilerError()
	if ce == nil {
		t.Fatal("ToCompilerError returned nil")
	}
	if ce.Category() != cerrors.DeveloperError {
		t.Errorf("Category = %v, want %v", ce.Category(), cerrors.DeveloperError)
	}
	if ce.Source().File != ".spec.components.api.values.replicas" {
		t.Errorf("Source.File = %q, want %q", ce.Source().File, ".spec.components.api.values.replicas")
	}
	// Inner error chain preserved: errors.Is against the resolver sentinel
	// matches through the converted CompilerError.
	if !stderrors.Is(ce, resolver.ErrSchemaConformance()) {
		t.Errorf("errors.Is(ce, ErrSchemaConformance) = false, want true (chain preservation)")
	}
}

// TestResolverError_ToCompilerError_ReleaseRoutesToOps verifies the
// Release entity prefix routes to OpsError category.
func TestResolverError_ToCompilerError_ReleaseRoutesToOps(t *testing.T) {
	re := &resolver.ResolverError{
		Entity:  "Release prod",
		Field:   ".spec.boundaries.frontend.parameters.bad-key",
		Message: "parameter type mismatch",
		Err:     resolver.ErrInvalidParameterRef(),
	}
	ce := re.ToCompilerError()
	if ce.Category() != cerrors.OpsError {
		t.Errorf("Category = %v, want %v", ce.Category(), cerrors.OpsError)
	}
}

// TestResolverError_ToCompilerError_RecipeRoutesToCatalog verifies the
// Recipe entity prefix routes to CatalogError category.
func TestResolverError_ToCompilerError_RecipeRoutesToCatalog(t *testing.T) {
	re := &resolver.ResolverError{
		Entity:  "Recipe my-recipe",
		Field:   ".spec.implements",
		Message: "unknown component type",
		Err:     resolver.ErrYAMLLoad(),
	}
	ce := re.ToCompilerError()
	if ce.Category() != cerrors.CatalogError {
		t.Errorf("Category = %v, want %v", ce.Category(), cerrors.CatalogError)
	}
}

// TestResolverError_ComposesItsConditionsSentenceOnce pins the composition every
// resolver finding renders through: the condition's registered sentence opens the
// line, exactly once, and the subject and the occurrence's own detail follow it.
//
// The "exactly once" is the load-bearing half. The sentence is read off the
// wrapped condition by the composer and is never handed to it as text, so a raise
// site cannot contribute a second copy. A composition that instead took a
// pre-rendered message as one of its values would print the sentence twice for
// every raise path whose message already opened with it — and several do, because
// the surfaces that compose correctly all lead with the registered sentence by
// construction.
//
// INVARIANT: a finding's rendered text opens with its condition's registered
// sentence and carries it once.
// CONSEQUENCE: without the leading, a reader scanning a column of diagnostics
// reads an entity name they already know instead of the condition that halted the
// build. Without the once, the same sentence is printed twice in one line.
func TestResolverError_ComposesItsConditionsSentenceOnce(t *testing.T) {
	t.Parallel()

	registered := resolver.ErrSchemaConformance().Error()
	re := &resolver.ResolverError{
		Entity:  "Aggregate my-app",
		Field:   ".spec.components.api.values.replicas",
		Message: `replicas: "two" is not an integer`,
		Err:     resolver.ErrSchemaConformance(),
	}

	for _, row := range []struct {
		surface  string
		rendered string
		subject  bool
	}{
		{"Error", re.Error(), true},
		{"MessageWithoutSubject", re.MessageWithoutSubject(), false},
		{"ToCompilerError.Message", re.ToCompilerError().Message(), true},
	} {
		if !strings.HasPrefix(row.rendered, registered) {
			t.Errorf("%s = %q; want it to open with %q, the condition's registered sentence",
				row.surface, row.rendered, registered)
		}
		if n := strings.Count(row.rendered, registered); n != 1 {
			t.Errorf("%s = %q carries the registered sentence %d times; want exactly 1",
				row.surface, row.rendered, n)
		}
		if !strings.Contains(row.rendered, re.Message) {
			t.Errorf("%s = %q; want it to carry %q, what this occurrence supplied — the "+
				"sentence is one static string per condition and cannot name the offending value",
				row.surface, row.rendered, re.Message)
		}
		if got := strings.Contains(row.rendered, re.Entity); got != row.subject {
			t.Errorf("%s = %q names the entity: %v; want %v", row.surface, row.rendered, got, row.subject)
		}
	}
}

// TestResolverError_WrappingAComposedDiagnosticDoesNotStateItTwice pins the arm a
// finding takes when it wraps an error that is ALREADY a composed diagnostic for
// the same condition — the shape the entity-envelope gate produces, where the
// wrapped report opens with the very sentence this composition would prepend.
//
// Such a finding supplies no Message, and that is what selects the arm: the
// choice follows from the field the raise site populated and never from reading
// the wrapped text. A composer that instead inspected whether a message already
// began with its sentence would be a heuristic over user-visible text — it would
// fire on a message that legitimately quotes the sentence, and miss one the
// wording of which was edited by a character.
//
// INVARIANT: wrapping an already-composed diagnostic neither restates its
// sentence nor drops its body.
// CONSEQUENCE: restating it prints the same sentence twice in one line;
// re-composing without it silently discards every finding the wrapped report
// carried.
func TestResolverError_WrappingAComposedDiagnosticDoesNotStateItTwice(t *testing.T) {
	t.Parallel()

	condition := resolver.ErrSchemaConformance()
	registered := condition.Error()
	// A stand-in for a gate that composes its own grouped report: the registered
	// sentence, its own subject, then the findings beneath it.
	composed := cerrors.ComposeMessage(condition, []string{"Release prod", "at ./release.yaml"}, "") +
		"; 12:3: .spec.boundaries: unexpected property"
	wrapped := &resolver.ResolverError{
		Entity: "Release",
		Field:  "./release.yaml",
		Err:    &composedDiagnostic{text: composed, condition: condition},
	}

	rendered := wrapped.Error()
	if n := strings.Count(rendered, registered); n != 1 {
		t.Errorf("rendered = %q carries the registered sentence %d times; want exactly 1.\n"+
			"INVARIANT: a finding wrapping an already-composed diagnostic renders it rather "+
			"than composing a second time.\n"+
			"CONSEQUENCE: the reader is told the same thing twice in one line.", rendered, n)
	}
	if !strings.HasPrefix(rendered, registered) {
		t.Errorf("rendered = %q; want it to open with %q", rendered, registered)
	}
	if want := "; 12:3: .spec.boundaries: unexpected property"; !strings.Contains(rendered, want) {
		t.Errorf("rendered = %q; want it to keep %q — the wrapped report's findings are the "+
			"detail, and dropping them to avoid restating the sentence loses what the author "+
			"has to act on", rendered, want)
	}
	if !stderrors.Is(wrapped, condition) {
		t.Error("identity did not survive the wrap; errors.Is must still reach the condition")
	}
}

// composedDiagnostic stands in for a gate whose own error is a fully composed
// diagnostic for the condition it carries: the text leads with the registered
// sentence, and the condition is reachable through the chain.
type composedDiagnostic struct {
	text      string
	condition error
}

func (c *composedDiagnostic) Error() string { return c.text }
func (c *composedDiagnostic) Unwrap() error { return c.condition }
