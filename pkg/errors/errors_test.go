// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package errors_test

import (
	stderrors "errors"
	"fmt"
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
	e := cerrors.Unidentified(
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
	e := cerrors.Unidentified(cerrors.TranslationError, cerrors.SourceLocation{}, "compose service collision", nil, nil)
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
		cerrors.TrustError,
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

func TestTrustErrorCategoryValue(t *testing.T) {
	t.Parallel()
	if string(cerrors.TrustError) != "TRUST_ERROR" {
		t.Errorf("TrustError = %q; want \"TRUST_ERROR\"", string(cerrors.TrustError))
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

// The identity this file registers, declared the way a consuming package writes
// one. Its short code sits in the same high band the other test files use and
// clear of their contiguous runs, so an addition to either does not collide: the
// registry is process-wide and a repeated identifier panics at initialisation.
// It is not an allocation and must not be read off the published mapping.
var identityEmbeddedSubject = cerrors.Register("embedded-identity-subject",
	cerrors.Meta{ShortCode: "E9221", Message: "the embedded-identity subject was reached"})

// embeddedForgery is the one route the compile gate structurally cannot cover.
// It embeds the identity interface, which promotes the unexported marker onto
// it, so it satisfies cerrors.Identity from this package without registering
// anything — and it COMPILES, which is why the assertion below is a run-time one
// and why no row for it exists in the compile-gate table.
//
// The embedded value is left nil in one case and set to a registered identity in
// the other. Both matter: the nil one is what an author writes, and the
// non-nil one is the reason the constructor's check is a concrete-type assertion
// rather than a test of the value. Nothing readable off the second — not
// nil-ness, not any of the three exported reads, all of which it answers —
// separates it from the condition it is impersonating.
type embeddedForgery struct {
	cerrors.Identity
}

func (embeddedForgery) Error() string          { return "the forged condition was reached" }
func (embeddedForgery) ErrorCode() string      { return "forged-condition" }
func (embeddedForgery) ErrorShortCode() string { return "E0000" }

// TestRaise_AdmitsOnlyARegisteredIdentity is the run-time half of the seal the
// compile gate holds the rest of.
//
// The property: a diagnostic that renders as though it names a halting condition
// can be built only from an identity a Register call produced. The harm the
// property prevents is specific — a forged short code is either a number that
// resolves to nothing, or worse, a number a real condition holds, which sends a
// reader quoting it off a terminal to a condition their build never raised.
//
// The row that must be ACCEPTED is not decoration. Without it a constructor that
// refused everything would satisfy every other row, and the table would report a
// seal while the package had stopped working.
func TestRaise_AdmitsOnlyARegisteredIdentity(t *testing.T) {
	t.Parallel()

	for _, c := range []struct {
		name string
		// condition is what reaches the constructor.
		condition cerrors.Identity
		// wantPanic is the fragment the refusal must carry. Empty means the
		// call must be admitted. The two refusals assert different fragments
		// deliberately: a fragment they shared would let either case keep
		// passing on the other's fault, and they are refused for different
		// reasons.
		wantPanic string
	}{
		{
			name:      "the identity a Register call returned is admitted",
			condition: identityEmbeddedSubject,
		},
		{
			name:      "a struct embedding the identity interface is refused",
			condition: embeddedForgery{},
			wantPanic: "no Register call produced",
		},
		{
			name:      "embedding a registered identity and overriding its reads is refused",
			condition: embeddedForgery{Identity: identityEmbeddedSubject},
			wantPanic: "no Register call produced",
		},
		{
			name:      "a nil condition is refused, and says so in its own words",
			condition: nil,
			wantPanic: "nil condition",
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			recovered, built := raiseRecovering(c.condition)

			if c.wantPanic == "" {
				if recovered != nil {
					t.Fatalf("the constructor refused an identity Register produced, so the "+
						"seal is refusing legitimate callers: %v", recovered)
				}
				code, ok := cerrors.CodeOf(built)
				if !ok || code != "embedded-identity-subject" {
					t.Errorf("the admitted diagnostic does not carry its condition: "+
						"CodeOf = %q, %v", code, ok)
				}
				return
			}

			if recovered == nil {
				t.Fatalf("the constructor admitted a condition no Register call produced, so a " +
					"diagnostic can name a condition nobody declared — the short code it " +
					"prints resolves to nothing, or to whichever condition holds that number")
			}
			got := fmt.Sprint(recovered)
			if !strings.Contains(got, c.wantPanic) {
				t.Errorf("the constructor refused, but not for the reason this case pins — "+
					"a refusal that fires on the wrong fault stops covering its own.\n"+
					"want the refusal to contain %q\ngot: %s", c.wantPanic, got)
			}
		})
	}
}

// raiseRecovering calls the constructor and reports what it panicked with, or
// nil together with what it built.
func raiseRecovering(condition cerrors.Identity) (recovered any, built *cerrors.CompilerError) {
	defer func() { recovered = recover() }()
	built = cerrors.Raise(condition, cerrors.CatalogError,
		cerrors.SourceLocation{File: "recipes/api.yaml"}, "the recipe is at fault", nil)
	return
}
