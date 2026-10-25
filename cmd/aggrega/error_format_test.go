// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"encoding/json"
	stderrors "errors"
	"fmt"
	"strings"
	"testing"

	abi "github.com/aggrega-ai/aggrega-abi-go/pkg/abi"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/analyzer"
	"github.com/aggrega-ai/aggrega-cli/internal/compilerops"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/validator"
	"github.com/aggrega-ai/aggrega-cli/pkg/validator/envelope"
)

// headTag builds the bracketed head a diagnostic carries when its chain names a
// registered condition: the category word, then that condition's short code.
//
// It reads the short code off the condition rather than spelling the number
// out, and the reason is not brevity. A number written here would be a second
// copy of the one the condition was registered with, and the two agree until
// either is edited — while what these tests are about is WHERE the code lands,
// not which number a particular condition holds. The registry's own tests own
// that question.
//
// A condition reporting no short code fails the test rather than yielding a
// head with a blank in it, which would assert nothing and read as a pass.
func headTag(t *testing.T, category cerrors.Category, condition error) string {
	t.Helper()
	code, ok := cerrors.ShortCodeOf(condition)
	if !ok {
		t.Fatalf("condition %v reports no short code, so the head under test cannot be built", condition)
	}
	return "[" + string(category) + " " + code + "]"
}

// TestFormatCompilerError_RichShape verifies the formatter renders the
// documented multi-line shape for a CompilerError: category tag in
// brackets, source pointer, message, expression, active pillars list.
func TestFormatCompilerError_RichShape(t *testing.T) {
	t.Parallel()
	e := cerrors.Unidentified(
		cerrors.CatalogError,
		cerrors.SourceLocation{File: "recipes/api.yaml", Line: 12, Column: 3},
		"FeatureRecipe template references per-component-only field",
		map[string]any{
			".values": "...",
			".infra":  "...",
		},
		nil,
	).WithExpression(".context.instance_id")
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

// TestFormatCompilerError_DeveloperCategory_RendersViaResolverError
// verifies the DeveloperError category renders correctly through the
// formatter when the input is a ResolverError with Aggregate-prefixed
// Entity. Exercises the structured-error detection branch + recursion
// path.
func TestFormatCompilerError_DeveloperCategory_RendersViaResolverError(t *testing.T) {
	t.Parallel()
	re := &resolver.ResolverError{
		Entity:  "Aggregate my-app",
		Field:   ".spec.components.api.values.replicas",
		Message: "replicas must be an integer",
		Err:     resolver.ErrSchemaConformance(),
	}
	got := formatCompilerError(re)
	for _, want := range []string{
		headTag(t, cerrors.DeveloperError, resolver.ErrSchemaConformance()),
		".spec.components.api.values.replicas",
		"Aggregate my-app",
		"replicas must be an integer",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q\n--- got ---\n%s", want, got)
		}
	}
}

// TestFormatCompilerError_OpsCategory_RendersViaResolverError verifies
// OpsError renders correctly for Release-prefixed ResolverError.
func TestFormatCompilerError_OpsCategory_RendersViaResolverError(t *testing.T) {
	t.Parallel()
	re := &resolver.ResolverError{
		Entity:  "Release prod",
		Field:   ".spec.boundaries.frontend.parameters",
		Message: "parameter type mismatch",
		Err:     resolver.ErrInvalidParameterRef(),
	}
	got := formatCompilerError(re)
	if want := headTag(t, cerrors.OpsError, resolver.ErrInvalidParameterRef()); !strings.Contains(got, want) {
		t.Errorf("output missing %q\n--- got ---\n%s", want, got)
	}
}

// TestFormatCompilerError_CatalogCategory_RendersRecipeShapeRule verifies a
// Recipe shape-rule failure renders under the catalog category, naming the
// file, the rule and what the rule requires.
//
// It also pins that the diagnostic is said ONCE. A failure whose own text is
// not the text the envelope's message was built from renders a second time as
// a "caused by:" continuation, because the chain walk cannot tell a restatement
// from a new frame. Reporting through one shape is what keeps the two strings
// equal, so the count is the thing worth asserting rather than the absence of
// any particular marker.
func TestFormatCompilerError_CatalogCategory_RendersRecipeShapeRule(t *testing.T) {
	t.Parallel()
	rve := validator.NewShapeRuleError(validator.ShapeRuleViolation{
		Kind:    envelope.KindRecipe,
		Name:    "my-recipe",
		Version: "1.0.0",
		Path:    "/path/to/recipe.yaml",
		Rule:    "values-mapping",
		Err:     validator.ErrRecipeValuesMappingRequiresSource(),
	})
	got := formatCompilerError(rve)
	for _, want := range []string{
		headTag(t, cerrors.CatalogError, validator.ErrRecipeValuesMappingRequiresSource()),
		"/path/to/recipe.yaml",
		validator.ErrRecipeValuesMappingRequiresSource().Error(),
		"rule values-mapping",
		"Recipe my-recipe:1.0.0",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q\n--- got ---\n%s", want, got)
		}
	}
	if n := strings.Count(got, "valuesMapping requires source"); n != 1 {
		t.Errorf("diagnostic rendered %d times, want exactly 1\n--- got ---\n%s", n, got)
	}
}

// TestFormatCompilerError_CatalogCategory_RendersFeatureRecipeShapeRule covers
// the FeatureRecipe half of the same path. Both kinds report through one shape,
// so both reach the category tag; neither falls through to a bare string.
func TestFormatCompilerError_CatalogCategory_RendersFeatureRecipeShapeRule(t *testing.T) {
	t.Parallel()
	fre := validator.NewShapeRuleError(validator.ShapeRuleViolation{
		Kind:    envelope.KindFeatureRecipe,
		Name:    "my-featurerecipe",
		Version: "1.0.0",
		Path:    "/path/to/featurerecipe.yaml",
		Rule:    "values-mapping",
		Err:     validator.ErrFeatureRecipeValuesMappingRequiresSource(),
	})
	got := formatCompilerError(fre)
	for _, want := range []string{
		headTag(t, cerrors.CatalogError, validator.ErrFeatureRecipeValuesMappingRequiresSource()),
		"/path/to/featurerecipe.yaml",
		"FeatureRecipe my-featurerecipe:1.0.0",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q\n--- got ---\n%s", want, got)
		}
	}
	if n := strings.Count(got, "valuesMapping requires source"); n != 1 {
		t.Errorf("diagnostic rendered %d times, want exactly 1\n--- got ---\n%s", n, got)
	}
}

// TestFormatCompilerError_WorkloadValidation_CategorySaidOnce pins that the
// grouped Workload report names its category exactly once. The envelope carries
// the category as its own bracketed tag, so a message that also opened with the
// category word printed it twice in one diagnostic.
func TestFormatCompilerError_WorkloadValidation_CategorySaidOnce(t *testing.T) {
	t.Parallel()
	wve := &validator.WorkloadValidationError{
		Path:          "/path/to/recipe.yaml",
		RecipeName:    "my-recipe",
		RecipeVersion: "1.0.0",
		Stage:         "schema",
		Findings: []validator.WorkloadValidationFinding{
			{FieldPath: ".containers.main.image", Constraint: "required field missing"},
		},
		Err: validator.ErrWorkloadValidation(),
	}
	got := formatCompilerError(wve)
	if n := strings.Count(got, "CATALOG_ERROR"); n != 1 {
		t.Errorf("category named %d times, want exactly 1\n--- got ---\n%s", n, got)
	}
	for _, want := range []string{
		headTag(t, cerrors.CatalogError, validator.ErrWorkloadValidation()),
		".containers.main.image",
		"required field missing",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q\n--- got ---\n%s", want, got)
		}
	}
}

// TestFormatCompilerError_TranslationCategory_RendersViaCompilerError
// verifies TranslationError renders correctly for a directly-constructed
// CompilerError (TranslationError sites are not currently fed by any
// structured-error type — they emit CompilerError directly via cerrors.Wrap
// at sites like the orchestrator's INSTANCE_ID_COLLISION path). This test
// completes the per-category coverage.
func TestFormatCompilerError_TranslationCategory_RendersViaCompilerError(t *testing.T) {
	t.Parallel()
	ce := cerrors.Unidentified(
		cerrors.TranslationError,
		cerrors.SourceLocation{File: "internal/compiler/renderer/orchestrator.go"},
		"chart name mismatch across boundaries",
		nil,
		nil,
	)
	got := formatCompilerError(ce)
	if !strings.Contains(got, "[TRANSLATION_ERROR]") {
		t.Errorf("output missing [TRANSLATION_ERROR] tag\n--- got ---\n%s", got)
	}
}

// TestFormatCompilerError_AnalysisError_RendersAsDeveloper verifies that
// an AnalysisError bubbled through the error chain renders correctly via
// the new detection branch (the analyzer-violation refactor's CLI render
// path).
func TestFormatCompilerError_AnalysisError_RendersAsDeveloper(t *testing.T) {
	t.Parallel()
	ae := &analyzer.AnalysisError{
		Kind:       analyzer.AnalysisErrorLocated,
		Message:    "no matching component on socket \"apis\"",
		OriginFile: "aggregate.yaml",
		Field:      ".spec.components.frontend.uses.apis",
	}
	got := formatCompilerError(ae)
	for _, want := range []string{"[DEVELOPER_ERROR]", "aggregate.yaml", "no matching component"} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q\n--- got ---\n%s", want, got)
		}
	}
}

// TestFormatCompilerError_ErrorsJoin_RendersAll verifies that an
// errors.Join'd chain of three CompilerErrors renders all three with the
// `\n---\n` separator. Without the multi-error branch, errors.As walks
// the chain and matches only the first; the others would silently drop.
// The build.go Step 4 refactor's "bubble each AnalysisError as a
// CompilerError via errors.Join" depends on this rendering correctness.
func TestFormatCompilerError_ErrorsJoin_RendersAll(t *testing.T) {
	t.Parallel()
	ce1 := cerrors.Unidentified(cerrors.DeveloperError, cerrors.SourceLocation{File: "node-a"}, "violation a", nil, nil)
	ce2 := cerrors.Unidentified(cerrors.DeveloperError, cerrors.SourceLocation{File: "node-b"}, "violation b", nil, nil)
	ce3 := cerrors.Unidentified(cerrors.DeveloperError, cerrors.SourceLocation{File: "node-c"}, "violation c", nil, nil)
	joined := stderrors.Join(ce1, ce2, ce3)

	got := formatCompilerError(joined)
	for _, want := range []string{"violation a", "violation b", "violation c", "\n---\n"} {
		if !strings.Contains(got, want) {
			t.Errorf("Join'd render missing %q\n--- got ---\n%s", want, got)
		}
	}
	// Each violation rendered exactly once.
	for _, msg := range []string{"violation a", "violation b", "violation c"} {
		if strings.Count(got, msg) != 1 {
			t.Errorf("expected exactly 1 occurrence of %q, got %d\n--- got ---\n%s",
				msg, strings.Count(got, msg), got)
		}
	}
}

// TestFormatCompilerError_LocatedFindings_DistinguishTwoComponentsOfOneType
// pins that two components sharing a component type and failing one rule over
// the finalised graph do not render the same bytes.
//
// Such a rule names its subject by a locator and puts the *declaring type* in
// the message — not the instance — so two components of that type produce one
// message about one document. The locator is then the only thing that differs
// between the two findings, which makes it the only thing that can tell an
// author which of the two components to open.
//
// The assertion is on the rendered bytes rather than on the locator field being
// populated, and the distinction is the whole point: the field can be populated
// while the bytes a reader sees are identical, which is exactly the state this
// guards against.
func TestFormatCompilerError_LocatedFindings_DistinguishTwoComponentsOfOneType(t *testing.T) {
	t.Parallel()
	// One message and one document for both findings, composed the way the
	// presence-mandate rule composes them: the registered wording, then the
	// declaring component type, the demanded type and the mandate's label —
	// none of which names the component instance.
	const message = `missing required feature: attach a Feature of the required type to the component ` +
		`("core/compute/api-server", "core/observability/monitoring", "monitoring")`
	const document = "workspace/aggregate.yaml"
	const firstKey = ".spec.components.checkout"
	const secondKey = ".spec.components.inventory"

	findings := []analyzer.AnalysisError{
		{Kind: analyzer.AnalysisErrorLocated, Message: message, OriginFile: document, Field: firstKey},
		{Kind: analyzer.AnalysisErrorLocated, Message: message, OriginFile: document, Field: secondKey},
	}

	// The composition the build performs before the command line renders it:
	// each violation converts, and the set is joined.
	errs := make([]error, len(findings))
	for i := range findings {
		errs[i] = findings[i].ToCompilerError()
	}
	got := formatCompilerError(stderrors.Join(errs...))
	t.Logf("rendered reading for two components of one type:\n%s", got)

	blocks := strings.Split(got, "\n---\n")
	if len(blocks) != 2 {
		t.Fatalf("want two findings separated by the join rule; got %d\n--- got ---\n%s", len(blocks), got)
	}
	if blocks[0] == blocks[1] {
		t.Errorf("two components of one type rendered byte-identically, so the reading names neither of them\n--- got ---\n%s", got)
	}
	// Each finding names the authoring key its own author has to open.
	for i, want := range []string{firstKey, secondKey} {
		if !strings.Contains(blocks[i], want) {
			t.Errorf("finding %d does not name its authoring key %q\n--- got ---\n%s", i, want, blocks[i])
		}
	}
	// The document survives beside the key rather than being displaced by it:
	// an author opens a file with the one and finds the fault with the other.
	for i := range blocks {
		if !strings.Contains(blocks[i], document) {
			t.Errorf("finding %d dropped the document it is about\n--- got ---\n%s", i, blocks[i])
		}
	}
}

// TestFormatCompilerError_ErrorsJoin_MixedTypes verifies the multi-error
// branch handles a Join'd chain of mixed structured types (CompilerError +
// ResolverError + a Recipe shape-rule failure). Each sub-error converts via
// its own ToCompilerError path; all three render.
func TestFormatCompilerError_ErrorsJoin_MixedTypes(t *testing.T) {
	t.Parallel()
	ce := cerrors.Unidentified(cerrors.TranslationError, cerrors.SourceLocation{}, "translation msg", nil, nil)
	re := &resolver.ResolverError{Entity: "Aggregate my-app", Field: ".f", Message: "resolver msg", Err: resolver.ErrSchemaConformance()}
	rve := validator.NewShapeRuleError(validator.ShapeRuleViolation{
		Kind: envelope.KindRecipe,
		Name: "r",
		Path: "/r.yaml",
		Err:  validator.ErrRecipeValuesMappingRequiresSource(),
	})
	joined := stderrors.Join(ce, re, rve)

	got := formatCompilerError(joined)
	// The envelope built with cerrors.New wraps nothing, so its chain names no
	// condition and its head stays the category alone — the two that do name one
	// carry their code, because a top-level Join element leads its own
	// diagnostic rather than continuing its neighbour's.
	for _, want := range []string{
		"[TRANSLATION_ERROR]",
		headTag(t, cerrors.DeveloperError, resolver.ErrSchemaConformance()),
		headTag(t, cerrors.CatalogError, validator.ErrRecipeValuesMappingRequiresSource()),
		"translation msg",
		"resolver msg",
		validator.ErrRecipeValuesMappingRequiresSource().Error(),
	} {
		if !strings.Contains(got, want) {
			t.Errorf("Mixed-type Join missing %q\n--- got ---\n%s", want, got)
		}
	}
}

// TestFormatCompilerError_ErrorsJoin_RecursionSafe verifies nested
// errors.Join chains render fully without stack overflow. The recursive
// call flow: outer Join → multi-error branch → render each → inner
// Join hits multi-error branch → render each leaf. Termination is
// guaranteed because each leaf is non-multi-error and reaches a
// terminal rendering branch.
func TestFormatCompilerError_ErrorsJoin_RecursionSafe(t *testing.T) {
	t.Parallel()
	ce1 := cerrors.Unidentified(cerrors.DeveloperError, cerrors.SourceLocation{}, "leaf 1", nil, nil)
	ce2 := cerrors.Unidentified(cerrors.DeveloperError, cerrors.SourceLocation{}, "leaf 2", nil, nil)
	ce3 := cerrors.Unidentified(cerrors.DeveloperError, cerrors.SourceLocation{}, "leaf 3", nil, nil)
	inner := stderrors.Join(ce1, ce2)
	outer := stderrors.Join(inner, ce3)

	got := formatCompilerError(outer)
	for _, want := range []string{"leaf 1", "leaf 2", "leaf 3"} {
		if !strings.Contains(got, want) {
			t.Errorf("nested Join missing %q\n--- got ---\n%s", want, got)
		}
	}
}

// TestFormatCompilerError_WorkloadValidation_SentenceSaidOnce is the
// double-render guard on the grouped Workload report.
//
// The report's Error output and the envelope's message must be the same text,
// because the chain walk suppresses a terminal frame only when the text it
// would add is already present. Were the two to diverge, the whole grouped
// report would render a second time as a "caused by:" continuation — the
// findings included. Counting the condition's registered sentence in the
// rendered output is what detects that: it is the one string that opens the
// report, so a second occurrence means a second render.
//
// The expected sentence is read off the condition rather than written here, so
// this test cannot pass against wording that has drifted from the identity.
func TestFormatCompilerError_WorkloadValidation_SentenceSaidOnce(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		stage     string
		condition error
	}{
		{"schema", validator.ErrWorkloadValidation()},
		{"featuresSchema", validator.ErrWorkloadFeaturesSchema()},
		{"slotReferenceability", validator.ErrSlotNotReferenceable()},
	} {
		t.Run(tc.stage, func(t *testing.T) {
			t.Parallel()
			wve := &validator.WorkloadValidationError{
				Path:          "/path/to/recipe.yaml",
				RecipeName:    "my-recipe",
				RecipeVersion: "1.0.0",
				Stage:         tc.stage,
				Findings: []validator.WorkloadValidationFinding{
					{FieldPath: ".containers.main.image", Constraint: "required field missing"},
					{FieldPath: ".containers.main.ports", Constraint: "must be an array", Suggestion: "declare a port list"},
				},
				Err: tc.condition,
			}
			sentence := tc.condition.Error()
			if !strings.HasPrefix(wve.Error(), sentence) {
				t.Errorf("the report must open with %q; got %q", sentence, wve.Error())
			}
			if msg := wve.ToCompilerError().Message(); msg != wve.Error() {
				t.Errorf("the envelope's message must be exactly the report's own text, or the chain walk renders it twice\n envelope %q\n report   %q", msg, wve.Error())
			}
			got := formatCompilerError(wve)
			if n := strings.Count(got, sentence); n != 1 {
				t.Errorf("registered sentence rendered %d times, want exactly 1\n--- got ---\n%s", n, got)
			}
			if n := strings.Count(got, ".containers.main.ports"); n != 1 {
				t.Errorf("finding rendered %d times, want exactly 1\n--- got ---\n%s", n, got)
			}
			if !strings.Contains(got, "declare a port list") {
				t.Errorf("the finding's suggestion must survive the composition change\n--- got ---\n%s", got)
			}
		})
	}
}

// TestFormatCompilerError_SocketOutputShape_ReachesTheReader renders the
// socket-output shape refusal through the real formatter and pins what a reader
// and a machine consumer each get out of it.
//
// The chain is built the way a socket really builds it — the host decode
// boundary's own wrap, inside the wrap its caller adds — and nothing here
// constructs an envelope, because no socket does. So the assertions are on the
// line a reader reads and on the identity the chain carries, both of which
// survive arbitrary wrapping.
//
// The payload is the misdirection the refusal exists to catch: a verdict role
// whose entry function was overridden to name an annotation-transform export, so
// an injection envelope comes back where a verdict envelope is read. Decoded
// leniently it would have yielded an empty verdict list, which is a legitimate
// allow.
//
// Every socket output crosses in the binary encoding and decodes through one
// entry point, so each row hands the envelope over in that encoding and the
// refusal names the field. An answer the reader cannot read as a map at all halts
// too, but names no field; that case is exercised at the decode boundary rather
// than here, because this test's subject is the formatter.
func TestFormatCompilerError_SocketOutputShape_ReachesTheReader(t *testing.T) {
	t.Parallel()

	const (
		export = "transform_annotations"
		plugin = "example/policy-guard:1.0.0"
	)
	payload, err := abi.AnnotationTransformOutput{
		Injections: []abi.AnnotationInjection{{BoundaryPath: "main", ComponentName: "a", AnnotationKey: "k", AnnotationValue: json.RawMessage(`"v"`)}},
	}.EncodeCBOR()
	if err != nil {
		t.Fatalf("encode the misdirected envelope: %v", err)
	}

	for _, tc := range []struct {
		// name is the socket whose wrap the row reproduces.
		name string
		// wrap is what that socket adds around the refusal, in the shape its
		// own call site uses.
		wrap func(error) error
		// wantPrefix is what the socket's wrap contributes ahead of the refusal.
		wantPrefix string
	}{
		{
			name:       "the verdict role over the IR graph",
			wrap:       func(err error) error { return fmt.Errorf("ir-verdict: plugin %q: %w", plugin, err) },
			wantPrefix: `ir-verdict: plugin "` + plugin + `": `,
		},
		{
			name:       "the publishing linter",
			wrap:       func(err error) error { return fmt.Errorf("publishing_linter: %w", err) },
			wantPrefix: "publishing_linter: ",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			// The socket's call site reads its guest's output through the one
			// decode entry point every socket output routes through.
			var out abi.PolicyOutput
			refusal := wasm.DecodeGuestOutputCBOR(export, payload, &out)
			if refusal == nil {
				t.Fatal("the payload is not the shape the socket reads: expected a refusal, got nil")
			}
			err := tc.wrap(refusal)
			got := formatCompilerError(err)

			// The sentence is read off the condition rather than written out
			// here, so this cannot pass against wording that has drifted from
			// the identity the condition is registered with.
			sentence := wasm.ErrWasmOutputShape().Error()
			want := tc.wantPrefix + `call "` + export + `": ` + sentence + `: abi: unexpected field "injections"`
			if !strings.Contains(got, want) {
				t.Errorf("the rendered diagnostic does not carry the line a reader reads\n want %q\n--- got ---\n%s", want, got)
			}
			if n := strings.Count(got, sentence); n != 1 {
				t.Errorf("the condition's sentence rendered %d times, want exactly 1\n--- got ---\n%s", n, got)
			}
			if !stderrors.Is(err, wasm.ErrWasmOutputShape()) {
				t.Errorf("errors.Is against the condition must match through the socket's own wrap\n--- rendered ---\n%s", got)
			}
			wantCode, ok := cerrors.CodeOf(wasm.ErrWasmOutputShape())
			if !ok {
				t.Fatal("the condition reports no code, so what the chain should report cannot be built")
			}
			// This is the value the daemon's editor diagnostic publishes for
			// this failure, read through the same call the daemon makes.
			if code, found := cerrors.CodeOf(err); !found || code != wantCode {
				t.Errorf("the chain reports code %q (found=%v); want %q", code, found, wantCode)
			}
		})
	}
}

// joinOfThreeFindings builds the shape a pass reports when it found several
// things wrong at once: three envelopes, each naming its own site, joined.
//
// Every guard below is built on one input so that what separates the cases is
// only what sits ABOVE the join, which is the whole subject of those guards.
func joinOfThreeFindings() error {
	return stderrors.Join(
		cerrors.Unidentified(cerrors.CatalogError, cerrors.SourceLocation{File: "a.yaml", Line: 11}, "finding alpha", nil, nil),
		cerrors.Unidentified(cerrors.CatalogError, cerrors.SourceLocation{File: "b.yaml", Line: 22}, "finding beta", nil, nil),
		cerrors.Unidentified(cerrors.CatalogError, cerrors.SourceLocation{File: "c.yaml", Line: 33}, "finding gamma", nil, nil),
	)
}

// TestFormatCompilerError_JoinUnderAPlainWrap_RendersEveryElement is the guard
// on the reach of the multi-error branch.
//
// A pass that found several things wrong reports them joined, and a caller one
// frame up habitually names the step it was running with a plain wrap. That
// wrap carries a single-error Unwrap, so a multi-error test applied to the
// outermost frame alone answers no — while every branch below reaches through
// the wrap, matches ONE of the joined elements and renders it as though it were
// the whole failure. The remaining findings are dropped with nothing said, on a
// surface whose entire job is to say what is wrong.
//
// So the assertion is not that the shape is handled but that the COUNT
// survives: every element present, and the separator between them present as
// many times as there are gaps.
func TestFormatCompilerError_JoinUnderAPlainWrap_RendersEveryElement(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		err  error
	}{
		{"one wrap", fmt.Errorf("pillars: %w", joinOfThreeFindings())},
		{"two wraps", fmt.Errorf("load: %w", fmt.Errorf("pillars: %w", joinOfThreeFindings()))},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := formatCompilerError(tc.err)
			for _, want := range []string{"finding alpha", "finding beta", "finding gamma"} {
				if n := strings.Count(got, want); n != 1 {
					t.Errorf("finding %q rendered %d times, want exactly 1 — a wrap above a join must not cut the report down to its first element\n--- got ---\n%s", want, n, got)
				}
			}
			if n := strings.Count(got, "\n---\n"); n != 2 {
				t.Errorf("three findings must be separated by 2 rules, got %d\n--- got ---\n%s", n, got)
			}
		})
	}
}

// TestFormatCompilerError_FrameAboveAJoin_KeepsThatFramesOwnRendering is the
// other half of the same guard, and the half a widening of the reach above
// would break.
//
// Several producers deliberately put a join UNDER a frame that renders it with
// text of its own: an envelope naming the pass and counting what it found, a
// decoration carrying a note the reader acts on. For those the join is a
// detail, not the failure the reader leads with, and hoisting it would print
// the findings while discarding the one line that says what they are findings
// OF.
//
// Each row therefore asserts BOTH halves of the pairing: the owner's own text,
// and the findings still beneath it. A hoist destroys the pairing in both
// directions at once — the findings appear with the owner's line gone — so
// asserting the two together is what makes a row discriminating rather than
// merely satisfiable. A reach that stopped only at an envelope would pass the
// first row and fail the rest; one that stopped nowhere would fail them all.
//
// What differs per row is only the marker the owning branch lays the findings
// out with, which is a property of that branch rather than of this reach: an
// envelope and a domain conversion render them beneath a counted "caused by"
// marker, while a decoration's lead renders at the left margin and separates
// them with the unindented rule.
func TestFormatCompilerError_FrameAboveAJoin_KeepsThatFramesOwnRendering(t *testing.T) {
	t.Parallel()
	// The findings under the domain type are deliberately NOT envelopes. The
	// envelope branch runs ahead of the domain branches and errors.As descends
	// through a single-error frame into a join, so envelopes here would be
	// claimed by that branch and the row would say nothing about this reach.
	domainOverJoin := &resolver.ResolverError{
		Entity:  "Aggregate my-app",
		Field:   ".spec.parameters",
		Message: "the frame's own sentence",
		Err: stderrors.Join(
			stderrors.New("plain alpha"), stderrors.New("plain beta"), stderrors.New("plain gamma"),
		),
	}
	findings := []string{"finding alpha", "finding beta", "finding gamma"}
	for _, tc := range []struct {
		name        string
		err         error
		wantOwned   string
		wantPresent []string
	}{
		{
			// The shape a categorising boundary builds when it files several
			// findings under one concern. The marker counting them is what says
			// the join rendered as this envelope's detail rather than in place
			// of it.
			name:        "envelope",
			err:         cerrors.Wrap(joinOfThreeFindings(), cerrors.CatalogError, cerrors.SourceLocation{}, "the pass found three things"),
			wantOwned:   "the pass found three things",
			wantPresent: append([]string{"caused by (3):"}, findings...),
		},
		{
			// The decoration's note is often the only text naming what the
			// author wrote wrong, which is why it is a named type at all. Its
			// lead renders at the left margin, so the findings are separated by
			// the unindented rule.
			// Built through the annotator rather than hand-assembled, because the
			// decoration's fields are the annotator's to fill. The refusal's own
			// reason is what the note ends up quoting, so it is what this row
			// asserts the decoration owns.
			name: "decoration",
			// No local-catalog note: the subject here is which frame the formatter
			// renders from, and a second note would add text that frame did not
			// contribute.
			err: compilerops.AnnotateSkipContext(joinOfThreeFindings(), []catalog.SkippedEntity{{
				Path: "/cat/recipes/helm/server.yaml",
				Err:  stderrors.New("the note the reader acts on"),
			}}, ""),
			wantOwned:   "the note the reader acts on",
			wantPresent: append([]string{"\n---\n"}, findings...),
		},
		{
			// And the same frame one plain wrap down is still that frame: the
			// reach passes through wraps and stops at the frame, not before it
			// and not after it.
			name:        "envelope under a plain wrap",
			err:         fmt.Errorf("step: %w", cerrors.Wrap(joinOfThreeFindings(), cerrors.CatalogError, cerrors.SourceLocation{}, "the pass found three things")),
			wantOwned:   "the pass found three things",
			wantPresent: append([]string{"caused by (3):"}, findings...),
		},
		{
			// A structured domain type renders through its own conversion, and
			// that conversion keeps the join on the chain — so the frame's own
			// subject leads and the findings follow it as its detail, which is
			// the pairing a hoist destroys in both directions at once.
			name:        "structured domain type",
			err:         domainOverJoin,
			wantOwned:   "the frame's own sentence",
			wantPresent: []string{"caused by (3):", "plain alpha", "plain beta", "plain gamma"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := formatCompilerError(tc.err)
			if !strings.Contains(got, tc.wantOwned) {
				t.Errorf("the frame above the join must keep rendering its own text %q — hoisting the join discards it\n--- got ---\n%s", tc.wantOwned, got)
			}
			for _, want := range tc.wantPresent {
				if !strings.Contains(got, want) {
					t.Errorf("the join beneath must still render as this frame's detail; missing %q\n--- got ---\n%s", want, got)
				}
			}
		})
	}
}

// TestFormatCompilerError_NoJoinBeneath_RenderingUnchanged is the negative
// control for the two guards above: chains with no join anywhere must render
// exactly as they would with no reach at all, so a green on those two says
// something about joins rather than about the formatter having been rewritten.
func TestFormatCompilerError_NoJoinBeneath_RenderingUnchanged(t *testing.T) {
	t.Parallel()
	t.Run("plain wrap over a lone envelope", func(t *testing.T) {
		t.Parallel()
		got := formatCompilerError(fmt.Errorf("step: %w",
			cerrors.Unidentified(cerrors.CatalogError, cerrors.SourceLocation{File: "a.yaml"}, "the only finding", nil, nil)))
		if !strings.Contains(got, "the only finding") {
			t.Errorf("a lone envelope under a wrap must still render\n--- got ---\n%s", got)
		}
		if strings.Contains(got, "\n---\n") {
			t.Errorf("nothing was joined, so no separator may appear\n--- got ---\n%s", got)
		}
	})
	t.Run("plain wrap over a plain error", func(t *testing.T) {
		t.Parallel()
		got := formatCompilerError(fmt.Errorf("step: %w", stderrors.New("the only text")))
		if got != "step: the only text" {
			t.Errorf("a chain naming nothing this formatter renders keeps its own flattened text; got %q", got)
		}
	})
}

// TestHasRenderableFrame_StepsOverAClaimedFrame pins the one semantic that must
// NOT follow the reach above, because the two questions read alike and are not
// alike.
//
// The chain walk asks whether the chain beneath a link still reaches a frame
// THAT WALK renders — envelopes and joins, and nothing else. A structured
// domain type is not one of those: the walk cannot render it, so it must step
// over one and keep looking. Were it to report a claimed frame as renderable,
// the walk above would traverse its link silently, find nothing it can render,
// and end — and the terminal link's text, which carries the whole flattened
// tail, would never be printed.
func TestHasRenderableFrame_StepsOverAClaimedFrame(t *testing.T) {
	t.Parallel()
	buried := cerrors.Unidentified(cerrors.CatalogError, cerrors.SourceLocation{}, "buried envelope", nil, nil)
	claimedOverEnvelope := &resolver.ResolverError{
		Entity:  "Aggregate my-app",
		Field:   ".f",
		Message: "a frame this walk cannot render",
		Err:     buried,
	}
	if !hasRenderableFrame(claimedOverEnvelope) {
		t.Error("an envelope beneath a structured domain type is still reachable by the chain walk, so the walk must keep traversing toward it")
	}
	if hasRenderableFrame(&resolver.ResolverError{Entity: "Aggregate my-app", Field: ".f", Message: "m", Err: stderrors.New("leaf")}) {
		t.Error("a chain reaching neither an envelope nor a join has nothing this walk renders")
	}
}
