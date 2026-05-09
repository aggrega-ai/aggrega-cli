// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	stderrors "errors"
	"strings"
	"testing"

	cerrors "github.com/cognitio/aggrega-cli/pkg/errors"
)

// TestFormatCompilerError_RichShape verifies the formatter renders the
// documented multi-line shape for a CompilerError: category tag in
// brackets, source pointer, message, expression, active pillars list.
func TestFormatCompilerError_RichShape(t *testing.T) {
	t.Parallel()
	e := &cerrors.CompilerError{
		Category:   cerrors.CatalogError,
		Source:     cerrors.SourceLocation{File: "recipes/api.yaml", Line: 12, Column: 3},
		Message:    "FeatureRecipe template references per-component-only field",
		Expression: ".context.instance_id",
		Context: map[string]any{
			".values": "...",
			".infra":  "...",
		},
	}
	got := formatCompilerError(e)
	for _, want := range []string{
		"[CATALOG_ERROR]",
		"recipes/api.yaml:12:3",
		"FeatureRecipe template references per-component-only field",
		".context.instance_id",
		"active pillars",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("formatted output missing substring %q\n--- got ---\n%s", want, got)
		}
	}
}

// TestFormatCompilerError_NonCompilerError_Falls back to the err.Error()
// representation when the underlying error is not a CompilerError.
func TestFormatCompilerError_NonCompilerError(t *testing.T) {
	t.Parallel()
	plain := stderrors.New("plain error message")
	got := formatCompilerError(plain)
	if got != "plain error message" {
		t.Errorf("formatCompilerError fallback = %q; want %q", got, "plain error message")
	}
}

// TestFormatCompilerError_Nil returns empty string for nil input.
func TestFormatCompilerError_Nil(t *testing.T) {
	t.Parallel()
	if got := formatCompilerError(nil); got != "" {
		t.Errorf("formatCompilerError(nil) = %q; want empty", got)
	}
}
