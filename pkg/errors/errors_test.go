// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package errors_test

import (
	stderrors "errors"
	"strings"
	"testing"

	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
)

// TestSourceLocation_String_ComposesAPosition pins every cell of the grid
// SourceLocation.String() decides over, because the two axes interact: a
// document with a line and a column is one shape, an authoring key within a
// document is another, and either half may arrive on its own.
//
// The rows carrying no locator are the load-bearing half. They are what bounds
// the change to the surfaces that supply one: a caller passing no locator must
// render exactly the bytes it rendered before the field existed, so every such
// row here is the pre-existing shape asserted unchanged rather than a new
// expectation.
func TestSourceLocation_String_ComposesAPosition(t *testing.T) {
	t.Parallel()

	for _, c := range []struct {
		name string
		in   cerrors.SourceLocation
		want string
	}{
		// No locator: every one of these is the shape this produced before a
		// locator could be carried at all.
		{
			"a document alone reads as the document",
			cerrors.SourceLocation{File: "recipes/api.yaml"},
			"recipes/api.yaml",
		},
		{
			"a document with a line reads as document and line",
			cerrors.SourceLocation{File: "recipes/api.yaml", Line: 12},
			"recipes/api.yaml:12",
		},
		{
			"a document with a line and a column reads as all three",
			cerrors.SourceLocation{File: "recipes/api.yaml", Line: 12, Column: 3},
			"recipes/api.yaml:12:3",
		},
		{
			"a column with no line does not reach the reading",
			cerrors.SourceLocation{File: "recipes/api.yaml", Column: 3},
			"recipes/api.yaml",
		},
		{
			"a pointer carrying nothing has no position at all",
			cerrors.SourceLocation{},
			"",
		},
		// A locator, which is the axis the located class supplies.
		{
			"a document and an authoring key read as the key inside the document",
			cerrors.SourceLocation{File: "workspace/aggregate.yaml", Locator: ".spec.components.checkout"},
			"workspace/aggregate.yaml at .spec.components.checkout",
		},
		{
			"an authoring key with no document stands alone",
			cerrors.SourceLocation{Locator: ".spec.components.checkout"},
			".spec.components.checkout",
		},
		{
			"a line and a column keep their place before the authoring key",
			cerrors.SourceLocation{File: "workspace/aggregate.yaml", Locator: ".spec.parameters.port", Line: 12, Column: 3},
			"workspace/aggregate.yaml:12:3 at .spec.parameters.port",
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if got := c.in.String(); got != c.want {
				t.Errorf("String() = %q, want %q", got, c.want)
			}
		})
	}
}

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
	// Validates that the Category values are distinct and stable. If a
	// future change collapses or renames a category, this test surfaces
	// the rename before it propagates through the audit-slice surface.
	//
	// The list below is the statement of what is covered, so a category
	// added to the package and not added here is one this test does not
	// check — and it keeps passing, because agreeing on a shorter list is
	// still agreement.
	all := []cerrors.Category{
		cerrors.DeveloperError,
		cerrors.OpsError,
		cerrors.CatalogError,
		cerrors.TranslationError,
		cerrors.FetchError,
		cerrors.PolicyError,
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

func TestFetchErrorCategoryValue(t *testing.T) {
	t.Parallel()
	if string(cerrors.FetchError) != "FETCH_ERROR" {
		t.Errorf("FetchError = %q; want \"FETCH_ERROR\"", string(cerrors.FetchError))
	}
}

func TestPolicyErrorCategoryValue(t *testing.T) {
	t.Parallel()
	if string(cerrors.PolicyError) != "POLICY_ERROR" {
		t.Errorf("PolicyError = %q; want \"POLICY_ERROR\"", string(cerrors.PolicyError))
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

// TestCauseChain_CarriesEveryFrame pins the property the single-line contexts
// depend on: a wrap chain whose outermost frame describes only the step that
// failed still yields the inner frame that names WHAT failed. Error() on a
// CompilerError deliberately renders one frame, so a caller that used it alone
// would report "unmarshal Component" and lose the constraint underneath.
func TestCauseChain_CarriesEveryFrame(t *testing.T) {
	t.Parallel()
	inner := stderrors.New("const: something does not equal inherited")
	middle := cerrors.Wrap(inner, cerrors.CatalogError, cerrors.SourceLocation{File: "c.yaml"}, "envelope refused the document")
	outer := cerrors.Wrap(middle, cerrors.CatalogError, cerrors.SourceLocation{File: "c.yaml"}, "unmarshal Component")

	if got := outer.Error(); strings.Contains(got, "does not equal inherited") {
		t.Fatalf("precondition: Error() must render one frame only; got %q", got)
	}
	got := cerrors.CauseChain(outer)
	for _, want := range []string{"unmarshal Component", "envelope refused the document", "does not equal inherited"} {
		if !strings.Contains(got, want) {
			t.Errorf("CauseChain dropped %q:\n%s", want, got)
		}
	}
}

// TestCauseChain_DropsRedundantFrames pins that a frame merely re-stating its
// cause is not said twice — the common shape where a wrap sets its message to
// the inner error's own text.
func TestCauseChain_DropsRedundantFrames(t *testing.T) {
	t.Parallel()
	inner := stderrors.New("the specific fault")
	outer := cerrors.Wrap(inner, cerrors.CatalogError, cerrors.SourceLocation{}, inner.Error())
	got := cerrors.CauseChain(outer)
	if n := strings.Count(got, "the specific fault"); n != 1 {
		t.Errorf("CauseChain stated the same text %d times; want 1:\n%s", n, got)
	}
}

func TestCauseChain_NilAndSingleFrame(t *testing.T) {
	t.Parallel()
	if got := cerrors.CauseChain(nil); got != "" {
		t.Errorf("CauseChain(nil) = %q; want empty", got)
	}
	lone := stderrors.New("only frame")
	if got := cerrors.CauseChain(lone); got != "only frame" {
		t.Errorf("CauseChain(single) = %q; want %q", got, "only frame")
	}
}
