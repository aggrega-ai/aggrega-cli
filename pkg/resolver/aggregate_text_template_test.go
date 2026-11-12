// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package resolver

import (
	"errors"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
	"github.com/aggrega-ai/aggrega-cli/internal/gotmpl"
)

// The rule that refuses an Aggregate whose authored text is not a Go template,
// driven through the presentation the resolve builds.
//
// What this file proves is the relation between the rule and the render rather
// than the rule alone: they parse the same text, so they have to reach the same
// verdict on every input, and the one way they could stop doing so is by asking
// through two function sets. The pinned asymmetry at the bottom is that failure
// mode made observable, so a later change routing the rule through the shared
// template helper fails here instead of shipping a rule that accepts documents
// the render refuses.

// refusalsOverAuthoredText invokes the rule the way the resolve invokes it —
// once per presented document, over that document's authored text — and returns
// what it raised.
//
// It goes through the presentation and the framework's own entry rather than
// calling the rule's Run directly, because the invocation is half of what these
// assertions are about: a rule handed the wrong subject, or a presentation
// answering from something other than the authored text, would both pass a
// direct call.
func refusalsOverAuthoredText(t *testing.T, raw []byte) []constraint.Objection {
	t.Helper()

	const document = "composed/aggregate.yaml"
	var presented constraint.DocumentView = newAggregateDocumentView(
		document, raw, contractOf())
	return constraint.RunOnDocument(document, presented, aggregateTextParsesAsATemplate)
}

// The rule and the render answer the same question, so they answer it the same
// way on every input. The table drives both over one text and compares the two
// verdicts rather than asserting each against a written-down expectation: an
// expectation would be a third copy of the answer, and the property under proof
// is that the first two agree.
//
// The rows are the classes a parse lands in — text with no action at all, text
// whose actions parse, and text whose actions do not — because a rule agreeing
// with the render on well-formed input alone would prove nothing about the case
// it exists for.
//
// Every row's parameters are supplied, so no row's render reaches an execution
// failure. That is deliberate: the render refuses a parse failure and an
// execution failure under one condition, and only the first is the rule's, so a
// row whose execution failed would read as a disagreement that is not one.
func TestAggregateTextRuleAgreesWithTheRenderOnWhatIsATemplate(t *testing.T) {
	t.Parallel()

	for _, row := range []struct {
		name    string
		raw     string
		refused bool
	}{
		{
			name: "no action at all",
			raw: `spec:
  components:
    api:
      values:
        fixed: "always"
`,
		},
		{
			name: "an action that parses",
			raw: `spec:
  components:
    api:
      values:
        region: "{{ .parameters.region }}"
`,
		},
		{
			name: "a pipeline through the render's own function set",
			raw: `spec:
  components:
    api:
      values:
        name: '{{ .parameters.given | upper | trimAll "x" }}'
`,
		},
		{
			name: "an action left open",
			raw: `spec:
  components:
    api:
      values:
        region: '{{ .parameters.region '
`,
			refused: true,
		},
		{
			name: "a name neither set holds",
			raw: `spec:
  components:
    api:
      values:
        region: '{{ noSuchFunction .parameters.region }}'
`,
			refused: true,
		},
	} {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()

			raw := []byte(row.raw)
			objections := refusalsOverAuthoredText(t, raw)
			_, renderErr := InterpolateParameters(raw, map[string]any{
				"region": "eu-west-1", "given": "v",
			})
			renderRefused := renderErr != nil && errors.Is(renderErr, ErrInvalidParameterRef())

			if (len(objections) > 0) != renderRefused {
				verdict := "accepted"
				if renderRefused {
					verdict = "refused"
				}
				t.Fatalf("the rule raised %d objection(s) while the render %s: the two parse the "+
					"same text and must reach the same verdict (render error: %v)",
					len(objections), verdict, renderErr)
			}
			if (len(objections) > 0) != row.refused {
				t.Fatalf("both refused=%t; the row expects refused=%t", len(objections) > 0, row.refused)
			}
			if !row.refused {
				return
			}
			if got := objections[0].Sentinel(); !errors.Is(got, ErrInvalidParameterRef()) {
				t.Errorf("objection sentinel = %v, want the invalid-parameter-reference condition", got)
			}
			// The parser's own account of where it stopped is what the author acts
			// on, so it travels in the detail rather than being reduced to the fact
			// that something failed.
			detail := strings.Join(objections[0].Detail(), " ")
			if !strings.Contains(detail, "template parse error") {
				t.Errorf("objection detail = %q, want the parser's own report", detail)
			}
		})
	}
}

// The rule's subject is the document, so the finding is positioned at the file
// and carries no locator inside it. Asserted rather than left implicit, because a
// later change giving this rule a subject with a locator would be changing where
// a reader is sent, and that is a decision rather than a refinement.
func TestAggregateTextRuleIsPositionedAtTheDocument(t *testing.T) {
	t.Parallel()

	objections := refusalsOverAuthoredText(t, []byte("a: '{{ .parameters.x '\n"))
	if len(objections) != 1 {
		t.Fatalf("objections = %d, want 1 — the rule is invoked once per document", len(objections))
	}
	if got := objections[0].SourceFile(); got != "composed/aggregate.yaml" {
		t.Errorf("source file = %q, want the presented document", got)
	}
	if got := objections[0].Locator(); got != "" {
		t.Errorf("locator = %q, want empty: the fault is the file's rather than an element's", got)
	}
}

// THE PINNED ASYMMETRY. The template helper the rest of the compiler parses
// through accepts strictly more than the render of an Aggregate's own text does,
// because it layers Aggrega's helpers over the render's set and Go's parser fails
// on a function name it does not hold.
//
// That direction is why the rule above parses through the render's own parse
// rather than through the shared helper, and it is pinned here rather than
// recorded in prose because prose cannot fail. A change routing the rule through
// the shared helper — which reads like a simplification, since every other
// surface parses that way — fails here, naming the document class it would start
// accepting and the render would go on refusing.
//
// It asserts the direction and not the membership of either set: what matters is
// that one accepts text the other does not, which is what would make a rule built
// on the wider one under-report.
func TestTheSharedTemplateHelperAcceptsTextTheRenderRefuses(t *testing.T) {
	t.Parallel()

	// A call to a helper the shared set binds and the render's set does not.
	raw := []byte(`a: '{{ optional .parameters "zone" "fallback" }}'` + "\n")

	if _, err := gotmpl.ParseTree(string(raw), "{{", "}}"); err != nil {
		t.Fatalf("the shared helper refused the planted text, so it is the wrong needle "+
			"and this assertion measures nothing: %v", err)
	}

	_, renderErr := InterpolateParameters(raw, map[string]any{})
	if renderErr == nil {
		t.Fatal("the render accepted text naming a helper its own function set does not bind, " +
			"so the two sets no longer differ and the rule's choice of parse is no longer load-bearing")
	}
	if !errors.Is(renderErr, ErrInvalidParameterRef()) {
		t.Errorf("render error = %v, want the invalid-parameter-reference condition", renderErr)
	}

	// The rule follows the render rather than the shared helper, which is the
	// whole point of the parse having one home.
	if objections := refusalsOverAuthoredText(t, raw); len(objections) != 1 {
		t.Fatalf("the rule raised %d objection(s) over text the render refuses; want 1 — a rule "+
			"parsing through the shared helper would accept this document and leave the build to "+
			"halt later with a finding naming no file", len(objections))
	}
}
