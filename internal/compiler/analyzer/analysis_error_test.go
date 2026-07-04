// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package analyzer_test

import (
	stderrors "errors"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/analyzer"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
)

// TestAnalysisError_Error_FormatsSelectorViolation verifies that a
// selector-class AnalysisError (Socket non-empty) formats as the
// "<source> -> <target> (<socket>): <message>" shape the previous
// stderr-print site emitted. Preserves operator-facing presentation.
func TestAnalysisError_Error_FormatsSelectorViolation(t *testing.T) {
	t.Parallel()
	e := analyzer.AnalysisError{
		Source:   "frontend",
		Target:   "backend",
		Socket:   "apis",
		Selector: "component.aggrega.ai/type=server",
		Message:  "no matching component found",
	}
	got := e.Error()
	for _, want := range []string{"frontend", "backend", "apis", "no matching component found"} {
		if !strings.Contains(got, want) {
			t.Errorf("Error() output missing %q: %q", want, got)
		}
	}
	// Selector-shape uses the arrow:
	if !strings.Contains(got, "->") {
		t.Errorf("selector-violation Error() should contain arrow: %q", got)
	}
}

// TestAnalysisError_Error_FormatsAppliesToViolation verifies the
// appliesTo-class shape (Socket empty) formats as
// "<source> attaches Feature <target>: <message>" — distinct from the
// selector-class shape.
func TestAnalysisError_Error_FormatsAppliesToViolation(t *testing.T) {
	t.Parallel()
	e := analyzer.AnalysisError{
		Source:  "frontend",
		Target:  "dns-resolver",
		Socket:  "",
		Message: "Feature constraint not met",
	}
	got := e.Error()
	if !strings.Contains(got, "attaches Feature") {
		t.Errorf("appliesTo Error() should contain 'attaches Feature': %q", got)
	}
	if strings.Contains(got, "->") {
		t.Errorf("appliesTo Error() should not use arrow shape: %q", got)
	}
}

// TestAnalysisError_ToCompilerError_RoutesToDeveloper verifies the
// AnalysisError → CompilerError conversion always routes to
// DeveloperError (selector and appliesTo violations are authoring-time
// problems on the developer's Aggregate).
func TestAnalysisError_ToCompilerError_RoutesToDeveloper(t *testing.T) {
	t.Parallel()
	e := &analyzer.AnalysisError{
		Source:  "frontend",
		Target:  "backend",
		Socket:  "apis",
		Message: "selector violation",
	}
	ce := e.ToCompilerError()
	if ce == nil {
		t.Fatal("ToCompilerError returned nil")
	}
	if ce.Category != cerrors.DeveloperError {
		t.Errorf("Category = %v, want %v", ce.Category, cerrors.DeveloperError)
	}
	if ce.Source.File != "frontend" {
		t.Errorf("Source.File = %q, want %q (violating-node ID)", ce.Source.File, "frontend")
	}
	if !strings.Contains(ce.Message, "selector violation") {
		t.Errorf("Message missing original violation text: %q", ce.Message)
	}
}

// TestAnalysisError_ToCompilerError_AppliesToRoutesToDeveloper verifies an
// appliesTo-shaped violation (Socket empty, no policy Kind) still routes to
// DeveloperError — the Feature attachment is developer-authored on the
// Aggregate.
func TestAnalysisError_ToCompilerError_AppliesToRoutesToDeveloper(t *testing.T) {
	t.Parallel()
	e := &analyzer.AnalysisError{
		Source:  "frontend",
		Target:  "dns-resolver",
		Socket:  "",
		Message: "Feature constraint not met",
	}
	ce := e.ToCompilerError()
	if ce == nil {
		t.Fatal("ToCompilerError returned nil")
	}
	if ce.Category != cerrors.DeveloperError {
		t.Errorf("Category = %v, want %v", ce.Category, cerrors.DeveloperError)
	}
}

// TestAnalysisError_ToCompilerError_PolicyRoutesToPolicyError verifies a
// policy-verdict violation (Kind AnalysisErrorPolicy) routes to PolicyError —
// a governance rejection from an operator-attached policy-validator plugin, not
// a developer authoring mistake — while keeping its plugin-supplied message and
// its ErrPolicyViolation sentinel recoverable via errors.Is.
func TestAnalysisError_ToCompilerError_PolicyRoutesToPolicyError(t *testing.T) {
	t.Parallel()
	e := &analyzer.AnalysisError{
		Kind:     analyzer.AnalysisErrorPolicy,
		Message:  "policy plugin \"house-rules\" rejected the build: image must be digest-pinned",
		Sentinel: analyzer.ErrPolicyViolation,
	}
	ce := e.ToCompilerError()
	if ce == nil {
		t.Fatal("ToCompilerError returned nil")
	}
	if ce.Category != cerrors.PolicyError {
		t.Errorf("Category = %v, want %v", ce.Category, cerrors.PolicyError)
	}
	if !strings.Contains(ce.Message, "image must be digest-pinned") {
		t.Errorf("Message missing plugin-supplied verdict text: %q", ce.Message)
	}
	if !stderrors.Is(ce, analyzer.ErrPolicyViolation) {
		t.Errorf("errors.Is should traverse to ErrPolicyViolation through the envelope chain")
	}
}
