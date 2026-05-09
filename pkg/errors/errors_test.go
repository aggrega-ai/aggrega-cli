// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package errors_test

import (
	stderrors "errors"
	"strings"
	"testing"

	cerrors "github.com/cognitio/aggrega-cli/pkg/errors"
)

func TestCompilerError_Error_FormatWithSource(t *testing.T) {
	t.Parallel()
	e := cerrors.New(
		cerrors.CatalogError,
		cerrors.SourceLocation{File: "recipes/api.yaml", Line: 12, Column: 3},
		"workload requires workloadSchema",
		nil,
		nil,
	)
	got := e.Error()
	for _, want := range []string{"[CATALOG_ERROR]", "recipes/api.yaml:12:3", "workload requires workloadSchema"} {
		if !strings.Contains(got, want) {
			t.Errorf("Error() = %q; missing substring %q", got, want)
		}
	}
}

func TestCompilerError_Error_FormatWithoutSource(t *testing.T) {
	t.Parallel()
	e := cerrors.New(cerrors.TranslationError, cerrors.SourceLocation{}, "compose service collision", nil, nil)
	got := e.Error()
	if !strings.HasPrefix(got, "[TRANSLATION_ERROR]") {
		t.Errorf("Error() = %q; expected to start with [TRANSLATION_ERROR]", got)
	}
}

func TestCompilerError_Unwrap_PreservesChain(t *testing.T) {
	t.Parallel()
	sentinel := stderrors.New("inner sentinel")
	e := cerrors.Wrap(sentinel, cerrors.OpsError, cerrors.SourceLocation{}, "wrapped message")
	if !stderrors.Is(e, sentinel) {
		t.Errorf("errors.Is should traverse the wrap chain to the sentinel")
	}
	var ce *cerrors.CompilerError
	if !stderrors.As(e, &ce) {
		t.Errorf("errors.As should match *CompilerError")
	}
}

func TestCompilerError_CategoryPartitions(t *testing.T) {
	t.Parallel()
	// Validates that the four-Category enum is distinct and stable. If a
	// future change collapses or renames a category, this test surfaces
	// the rename before it propagates through the audit-slice surface.
	all := []cerrors.Category{
		cerrors.DeveloperError,
		cerrors.OpsError,
		cerrors.CatalogError,
		cerrors.TranslationError,
	}
	seen := map[cerrors.Category]bool{}
	for _, c := range all {
		if seen[c] {
			t.Errorf("category %q appears twice", c)
		}
		seen[c] = true
		if string(c) == "" {
			t.Errorf("category cannot be empty")
		}
	}
}

func TestCompilerError_Nil_Safe(t *testing.T) {
	t.Parallel()
	var e *cerrors.CompilerError
	if got := e.Error(); got != "<nil>" {
		t.Errorf("nil CompilerError.Error() = %q; want <nil>", got)
	}
	if got := e.Unwrap(); got != nil {
		t.Errorf("nil CompilerError.Unwrap() = %v; want nil", got)
	}
}
