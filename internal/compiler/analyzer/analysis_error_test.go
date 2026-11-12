// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package analyzer_test

import (
	stderrors "errors"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/analyzer"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
)

// TestAnalysisError_Error_ReturnsTheRuleMessageVerbatim pins that the rendered
// text is exactly what the rule said and nothing composed around it.
//
// It matters because two surfaces render a finding differently — an editor
// positions it from the structured locator, a terminal prints the text — and a
// composition here would put a coordinate in one of them and not the other. The
// message is therefore the whole diagnostic, and where the finding is travels on
// its own fields.
func TestAnalysisError_Error_ReturnsTheRuleMessageVerbatim(t *testing.T) {
	t.Parallel()
	const message = `selector violation ("component.aggrega.ai/type=stateful-server", "frontend")`
	e := analyzer.AnalysisError{
		Kind:       analyzer.AnalysisErrorLocated,
		Message:    message,
		OriginFile: "aggregate.yaml",
		Field:      ".spec.components.backend.uses.databases",
	}
	if got := e.Error(); got != message {
		t.Errorf("Error() = %q; want the rule's own message %q", got, message)
	}
	// The locator is not folded into the text. A consumer walking the source
	// document reads it off the field, and a consumer rendering the text gets
	// exactly what the rule composed.
	if strings.Contains(e.Error(), e.Field) {
		t.Errorf("Error() folded the locator into the text: %q", e.Error())
	}
}

// TestAnalysisError_ToCompilerError_LocatedRoutesToDeveloper verifies the
// conversion routes a rule's violation to DeveloperError and points the source
// location at both halves of where the finding is — the document and the
// authoring key within it.
//
// The document stays on its own half of the pointer rather than being folded in
// with the key, and that is the property worth pinning: a caller acting on the
// pointer opens a file with it, so a pointer carrying anything that is not a path
// names nothing on disk. The key travels beside it, which is what lets the
// reading name a component at all.
func TestAnalysisError_ToCompilerError_LocatedRoutesToDeveloper(t *testing.T) {
	t.Parallel()
	const locator = ".spec.components.frontend.uses.apis"
	e := &analyzer.AnalysisError{
		Kind:       analyzer.AnalysisErrorLocated,
		Message:    "selector violation",
		OriginFile: "workspace/aggregate.yaml",
		Field:      locator,
	}
	ce := e.ToCompilerError()
	if ce == nil {
		t.Fatal("ToCompilerError returned nil")
	}
	if ce.Category() != cerrors.DeveloperError {
		t.Errorf("Category = %v, want %v", ce.Category(), cerrors.DeveloperError)
	}
	if ce.Source().File != "workspace/aggregate.yaml" {
		t.Errorf("Source.File = %q, want the document the finding is about", ce.Source().File)
	}
	if ce.Source().Locator != locator {
		t.Errorf("Source.Locator = %q, want the authoring key the rule named %q", ce.Source().Locator, locator)
	}
	// The reading a terminal shows names both halves as one position.
	if got, want := ce.Source().String(), "workspace/aggregate.yaml at "+locator; got != want {
		t.Errorf("source pointer reads as %q, want %q", got, want)
	}
	if !strings.Contains(ce.Message(), "selector violation") {
		t.Errorf("Message missing original violation text: %q", ce.Message())
	}
	// The locator is still on the violation's own field after the conversion.
	// A consumer walking the parsed document to a range reads it there, and it
	// would have no range to compute from if the conversion had moved it.
	if e.Field != locator {
		t.Errorf("the conversion disturbed the violation's locator field: %q", e.Field)
	}
}

// TestAnalysisError_ToCompilerError_PolicyRoutesToPolicyError verifies a
// policy-verdict violation (Kind AnalysisErrorPolicy) routes to PolicyError —
// a governance rejection from an operator-attached policy plugin, not
// a developer authoring mistake — while keeping its plugin-supplied message and
// its ErrPolicyViolation sentinel recoverable via errors.Is.
//
// Its source pointer is empty, and that is the class's own shape rather than an
// omission: a verdict is about the graph rather than about one document, so
// naming a file would name one the verdict never read.
func TestAnalysisError_ToCompilerError_PolicyRoutesToPolicyError(t *testing.T) {
	t.Parallel()
	e := &analyzer.AnalysisError{
		Kind:     analyzer.AnalysisErrorPolicy,
		Message:  "policy plugin \"house-rules\" rejected the build: image must be digest-pinned",
		Sentinel: analyzer.ErrPolicyViolation(),
	}
	ce := e.ToCompilerError()
	if ce == nil {
		t.Fatal("ToCompilerError returned nil")
	}
	if ce.Category() != cerrors.PolicyError {
		t.Errorf("Category = %v, want %v", ce.Category(), cerrors.PolicyError)
	}
	if ce.Source().File != "" {
		t.Errorf("Source.File = %q; a verdict names no document", ce.Source().File)
	}
	if ce.Source().Locator != "" {
		t.Errorf("Source.Locator = %q; a verdict is about the graph, not about one authoring key", ce.Source().Locator)
	}
	if !strings.Contains(ce.Message(), "image must be digest-pinned") {
		t.Errorf("Message missing plugin-supplied verdict text: %q", ce.Message())
	}
	if !stderrors.Is(ce, analyzer.ErrPolicyViolation()) {
		t.Errorf("errors.Is should traverse to ErrPolicyViolation through the envelope chain")
	}
}
