// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"errors"
	"strings"
	"testing"
)

// TestEvaluateAssertion_PassesOnEmptyTemplate pins the happy path: a
// template with no fail call and no output renders cleanly.
func TestEvaluateAssertion_PassesOnEmptyTemplate(t *testing.T) {
	t.Parallel()
	ctx := AssertionContext{Values: map[string]any{"port": 8080}}
	if err := EvaluateAssertion("rule", `{{- /* noop */ -}}`, ctx); err != nil {
		t.Errorf("expected nil; got %v", err)
	}
}

// TestEvaluateAssertion_FailsWithCustomMessage pins the substantive fail
// path: the operator-supplied message reaches the error chain.
func TestEvaluateAssertion_FailsWithCustomMessage(t *testing.T) {
	t.Parallel()
	ctx := AssertionContext{Values: map[string]any{"replicas": 0}}
	err := EvaluateAssertion("min-replicas", `{{- if lt (.Values.replicas | int) 1 -}}{{- fail "replicas must be positive" -}}{{- end -}}`, ctx)
	if err == nil {
		t.Fatal("expected error; got nil")
	}
	if !errors.Is(err, ErrAssertionFailed) {
		t.Errorf("expected ErrAssertionFailed in chain; got %v", err)
	}
	if !strings.Contains(err.Error(), "replicas must be positive") {
		t.Errorf("expected custom message in error; got %q", err.Error())
	}
}

// TestEvaluateAssertion_PreservesErrAssertionFailedChain pins the
// errors.Is contract — callers using errors.Is(err, ErrAssertionFailed)
// at wire-in sites match regardless of intermediate wrapping.
func TestEvaluateAssertion_PreservesErrAssertionFailedChain(t *testing.T) {
	t.Parallel()
	ctx := AssertionContext{Values: map[string]any{}}
	err := EvaluateAssertion("rule", `{{ fail "halt" }}`, ctx)
	if err == nil {
		t.Fatal("expected error; got nil")
	}
	if !errors.Is(err, ErrAssertionFailed) {
		t.Errorf("errors.Is(err, ErrAssertionFailed) = false; want true")
	}
}

// TestEvaluateAssertion_MissingKeyProducesError pins the new strict-policy
// behavior. A reference to .Values.typo against a context without that key
// halts with a no-such-key diagnostic from template execution.
func TestEvaluateAssertion_MissingKeyProducesError(t *testing.T) {
	t.Parallel()
	ctx := AssertionContext{Values: map[string]any{"port": 8080}}
	err := EvaluateAssertion("typo-catch", `{{ if .Values.typo }}{{ fail "should not reach" }}{{ end }}`, ctx)
	if err == nil {
		t.Fatal("expected missing-key error; got nil")
	}
	// missing-key errors are not ErrAssertionFailed — they're template-engine errors.
	if errors.Is(err, ErrAssertionFailed) {
		t.Errorf("did not expect ErrAssertionFailed for missing-key; got %v", err)
	}
	if !strings.Contains(err.Error(), "typo") {
		t.Errorf("expected missing-key error to name the bad reference; got %q", err.Error())
	}
}

// TestEvaluateAssertion_IndexFuncWorksAroundMissingKey pins the author
// workaround for optional-field idioms: Go template's index function plus
// Sprig's default returns a fallback value when the referenced field is
// missing. (Sprig's default alone does NOT work under missingkey=error —
// the reference itself halts before default can fire; index returns nil
// for missing keys and default can then take effect.)
func TestEvaluateAssertion_IndexFuncWorksAroundMissingKey(t *testing.T) {
	t.Parallel()
	ctx := AssertionContext{Values: map[string]any{"port": 8080}}
	// .Values.replicas is absent; index returns nil, default 1 supplies the fallback.
	err := EvaluateAssertion("optional",
		`{{ if lt ((index .Values "replicas") | default 1 | int) 1 }}{{ fail "below 1" }}{{ end }}`, ctx)
	if err != nil {
		t.Errorf("expected nil (index+default fallback); got %v", err)
	}
}

// TestEvaluateAssertion_SprigHasKeyWorksAroundMissingKey pins the
// alternative author workaround: hasKey gates the reference.
func TestEvaluateAssertion_SprigHasKeyWorksAroundMissingKey(t *testing.T) {
	t.Parallel()
	ctx := AssertionContext{Values: map[string]any{"port": 8080}}
	err := EvaluateAssertion("optional-haskey",
		`{{ if and (hasKey .Values "replicas") (lt (.Values.replicas | int) 1) }}{{ fail "below 1" }}{{ end }}`, ctx)
	if err != nil {
		t.Errorf("expected nil (hasKey gate); got %v", err)
	}
}

// TestEvaluateAssertion_NameAppearsInErrorMessage pins that the supplied
// name surfaces in the error chain — diagnostic provenance via the
// OrderedMap key as a structural identifier.
func TestEvaluateAssertion_NameAppearsInErrorMessage(t *testing.T) {
	t.Parallel()
	ctx := AssertionContext{Values: map[string]any{}}
	err := EvaluateAssertion("specific-rule-name", `{{ fail "x" }}`, ctx)
	if err == nil {
		t.Fatal("expected error; got nil")
	}
	if !strings.Contains(err.Error(), "specific-rule-name") {
		t.Errorf("expected assertion name in error; got %q", err.Error())
	}
}

// TestEvaluateAssertion_FailMessagePropagates pins the operator-supplied
// fail-call argument reaches the error message intact.
func TestEvaluateAssertion_FailMessagePropagates(t *testing.T) {
	t.Parallel()
	ctx := AssertionContext{}
	err := EvaluateAssertion("rule", `{{ fail "the-exact-message-text" }}`, ctx)
	if err == nil {
		t.Fatal("expected error; got nil")
	}
	if !strings.Contains(err.Error(), "the-exact-message-text") {
		t.Errorf("expected fail message intact; got %q", err.Error())
	}
}
