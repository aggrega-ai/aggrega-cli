// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/gotmpl"
	"github.com/aggrega-ai/aggrega-cli/internal/redact"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
)

// TestEvaluateAssertion_PassesOnEmptyTemplate pins the happy path: a
// template with no fail call and no output renders cleanly.
func TestEvaluateAssertion_PassesOnEmptyTemplate(t *testing.T) {
	t.Parallel()
	ctx := AssertionContext{Values: map[string]any{"port": 8080}}
	if err := EvaluateAssertion("rule", `{{- /* noop */ -}}`, ctx, redact.Keys{}); err != nil {
		t.Errorf("expected nil; got %v", err)
	}
}

// TestEvaluateAssertion_FailsWithCustomMessage pins the substantive fail
// path: the operator-supplied message reaches the error chain.
func TestEvaluateAssertion_FailsWithCustomMessage(t *testing.T) {
	t.Parallel()
	ctx := AssertionContext{Values: map[string]any{"replicas": 0}}
	err := EvaluateAssertion("min-replicas", `{{- if lt (.values.replicas | int) 1 -}}{{- fail "replicas must be positive" -}}{{- end -}}`, ctx, redact.Keys{})
	if err == nil {
		t.Fatal("expected error; got nil")
	}
	if !errors.Is(err, ErrAssertionFailed()) {
		t.Errorf("expected ErrAssertionFailed in chain; got %v", err)
	}
	if !strings.Contains(err.Error(), "replicas must be positive") {
		t.Errorf("expected custom message in error; got %q", err.Error())
	}
}

// TestEvaluateAssertion_PreservesErrAssertionFailedChain pins the
// errors.Is contract — callers using errors.Is(err, ErrAssertionFailed())
// at wire-in sites match regardless of intermediate wrapping.
func TestEvaluateAssertion_PreservesErrAssertionFailedChain(t *testing.T) {
	t.Parallel()
	ctx := AssertionContext{Values: map[string]any{}}
	err := EvaluateAssertion("rule", `{{ fail "halt" }}`, ctx, redact.Keys{})
	if err == nil {
		t.Fatal("expected error; got nil")
	}
	if !errors.Is(err, ErrAssertionFailed()) {
		t.Errorf("errors.Is(err, ErrAssertionFailed) = false; want true")
	}
}

// TestEvaluateAssertion_MissingKeyProducesError pins the strict-policy
// behavior. A reference to .values.typo against a context without that key
// halts with a no-such-key diagnostic from template execution, under the
// assertion condition, as a stop the assertion did not call fail for.
func TestEvaluateAssertion_MissingKeyProducesError(t *testing.T) {
	t.Parallel()
	ctx := AssertionContext{Values: map[string]any{"port": 8080}}
	err := EvaluateAssertion("typo-catch", `{{ if .values.typo }}{{ fail "should not reach" }}{{ end }}`, ctx, redact.Keys{})
	if err == nil {
		t.Fatal("expected missing-key error; got nil")
	}
	if !errors.Is(err, ErrAssertionFailed()) {
		t.Errorf("want the missing-key stop under ErrAssertionFailed; got %v", err)
	}
	if AssertionCalledFail(err) {
		t.Errorf("a missing-key stop was reported as a fail call: %v", err)
	}
	if !strings.Contains(err.Error(), `map has no entry for key "typo"`) {
		t.Errorf("expected missing-key error to name the bad reference; got %q", err.Error())
	}
}

// TestEvaluateAssertion_EveryStopCarriesAShortCode pins the head an assertion
// stop is printed under: the short code the formatter reads off the chain. One
// fault per row, with a passing assertion as the control.
//
// A fail call and a missing key both carry the assertion condition; only the
// fail call is reported as one by AssertionCalledFail, which is what keeps a
// check of an entity on its own reporting the first as a warning and halting on
// the second. A stop whose own error already names a condition keeps that
// condition, so the assertion condition does not displace a more specific one.
//
// INVARIANT: every stop an assertion evaluation returns carries a short code, and
// a missing key is filed under the assertion condition without reading as a fail
// call.
// CONSEQUENCE: were a row to lose its code, the halt would print a head with no
// short code for `aggrega explain` to resolve.
func TestEvaluateAssertion_EveryStopCarriesAShortCode(t *testing.T) {
	t.Parallel()
	ctx := AssertionContext{Values: map[string]any{"port": 8080}}
	assertionCode, _ := cerrors.ShortCodeOf(ErrAssertionFailed())
	indexCode, _ := cerrors.ShortCodeOf(gotmpl.ErrIndexOfAbsentMapKey())
	for _, tc := range []struct {
		name       string
		body       string
		wantCode   string
		calledFail bool
	}{
		{"a passing assertion (control)", `{{ if .values.port }}{{ end }}`, "", false},
		{"a fail call", `{{ fail "boom" }}`, assertionCode, true},
		{"a key the context does not hold", `{{ if .values.nosuchkey }}{{ end }}`, assertionCode, false},
		{"an index of a key the map does not hold", `{{ if index .values "nosuchkey" }}{{ end }}`, indexCode, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := EvaluateAssertion("rule", tc.body, ctx, redact.Keys{})
			if tc.wantCode == "" {
				if err != nil {
					t.Fatalf("the control stopped: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("%s passed; want a stop", tc.body)
			}
			if got, _ := cerrors.ShortCodeOf(err); got != tc.wantCode {
				t.Errorf("%s: short code %q; want %q (%v)", tc.body, got, tc.wantCode, err)
			}
			if got := AssertionCalledFail(err); got != tc.calledFail {
				t.Errorf("%s: AssertionCalledFail = %v; want %v", tc.body, got, tc.calledFail)
			}
		})
	}
}

// TestEvaluateAssertion_OptionalWorksAroundMissingKey pins the author
// workaround for optional-field idioms: the optional helper returns the
// fallback when the referenced field is missing. (Sprig's default alone does
// NOT work under missingkey=error — the reference itself halts before default
// can fire.) Reading the key through index and then applying default does not
// work either: index halts on a map key the map does not hold, naming the key,
// before default runs — the second half below holds that.
func TestEvaluateAssertion_OptionalWorksAroundMissingKey(t *testing.T) {
	t.Parallel()
	ctx := AssertionContext{Values: map[string]any{"port": 8080}}
	// .values.replicas is absent; optional supplies the fallback 1.
	err := EvaluateAssertion("optional", `{{ if lt (optional .values "replicas" 1 | int) 1 }}{{ fail "below 1" }}{{ end }}`, ctx, redact.Keys{})
	if err != nil {
		t.Errorf("expected nil (optional fallback); got %v", err)
	}

	err = EvaluateAssertion("index-default", `{{ if lt ((index .values "replicas") | default 1 | int) 1 }}{{ fail "below 1" }}{{ end }}`, ctx, redact.Keys{})
	if err == nil {
		t.Fatal("index of an absent key piped through default passed; want a halt naming the key")
	}
	if !errors.Is(err, gotmpl.ErrIndexOfAbsentMapKey()) || errors.Is(err, ErrAssertionFailed()) {
		t.Errorf("want the absent-key condition and not an assertion failure; got %v", err)
	}
	if !strings.Contains(err.Error(), `key "replicas"`) {
		t.Errorf("the halt does not name the key: %v", err)
	}
}

// TestEvaluateAssertion_SprigHasKeyWorksAroundMissingKey pins the
// alternative author workaround: hasKey gates the reference.
func TestEvaluateAssertion_SprigHasKeyWorksAroundMissingKey(t *testing.T) {
	t.Parallel()
	ctx := AssertionContext{Values: map[string]any{"port": 8080}}
	err := EvaluateAssertion("optional-haskey", `{{ if and (hasKey .values "replicas") (lt (.values.replicas | int) 1) }}{{ fail "below 1" }}{{ end }}`, ctx, redact.Keys{})

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
	err := EvaluateAssertion("specific-rule-name", `{{ fail "x" }}`, ctx, redact.Keys{})
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
	err := EvaluateAssertion("rule", `{{ fail "the-exact-message-text" }}`, ctx, redact.Keys{})
	if err == nil {
		t.Fatal("expected error; got nil")
	}
	if !strings.Contains(err.Error(), "the-exact-message-text") {
		t.Errorf("expected fail message intact; got %q", err.Error())
	}
}

// The key set an assertion body reads is a published authoring contract, so it is
// pinned directly against the delivered keys rather than left to whatever a field
// name happens to lower-case to. A change to either side of this — a tag, or a new
// field without one — is a change to what every authored assertion in every catalog
// can reference.
func TestAssertionContext_DeliveredKeySet(t *testing.T) {
	t.Parallel()
	delivered, ok := toLowercaseMap(AssertionContext{}).(map[string]any)
	if !ok {
		t.Fatalf("the assertion context must be delivered as a keyed map, got %T", toLowercaseMap(AssertionContext{}))
	}
	want := []string{"values", "infra", "uses", "features", "metadata", "provides", "locals", "recipe"}
	for _, key := range want {
		if _, present := delivered[key]; !present {
			t.Errorf("delivered key set omits %q: %v", key, sortedMapKeys(delivered))
		}
	}
	if len(delivered) != len(want) {
		t.Errorf("delivered key set is %v, want exactly %v", sortedMapKeys(delivered), want)
	}
	// The reference check consults the same derivation, so the admitted set and the
	// delivered set are one reading of the type rather than two.
	admitted, derivable := (templateScope{data: AssertionContext{}}).admittedRoots()
	if !derivable {
		t.Fatal("the assertion context's reference set must be derivable from its type")
	}
	if strings.Join(admitted, ",") != strings.Join(want, ",") {
		t.Errorf("admitted set %v does not match the delivered set %v", admitted, want)
	}
	// .recipe is a struct behind a pointer, so its keys are checked too.
	keys, gated := (templateScope{data: AssertionContext{}}).admittedKeys("recipe")
	if !gated || strings.Join(keys, ",") != "metadata,source" {
		t.Errorf(".recipe admits %v (gated=%v), want metadata,source", keys, gated)
	}
}

func sortedMapKeys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

// The assertion bodies shipped in the catalog today, verbatim — each is authored as
// a folded scalar, so its source lines arrive joined by one space. They are exercised
// here rather than inferred from the context type: an assertion surface whose
// authored references stop resolving is a build that fails on content nobody
// touched.
func shippedCatalogAssertions() map[string]string {
	return map[string]string{
		"kafka_operator_prerequisite": "{{- if not .infra.control_plane_present -}} " +
			`{{- fail "the Strimzi Cluster Operator must be installed in the target cluster before this realization emits its Kafka custom resource; once the operator is present, affirm the prerequisite by setting infra.control_plane_present to true in the Release" -}}` +
			" {{- end -}}",
		"kserve_control_plane_prerequisite": "{{- if not .infra.control_plane_present -}} " +
			`{{- fail "the KServe control plane must be installed in the target cluster before this realization emits its InferenceService; once KServe is present, affirm the prerequisite by setting infra.control_plane_present to true in the Release" -}}` +
			" {{- end -}}",
		"kubeai_control_plane_prerequisite": "{{- if not .infra.control_plane_present -}} " +
			`{{- fail "the KubeAI control plane must be installed in the target cluster before this realization emits its Model custom resource; once KubeAI is present, affirm the prerequisite by setting infra.control_plane_present to true in the Release" -}}` +
			" {{- end -}}",
	}
}

func TestEvaluateAssertion_ShippedCatalogBodiesEvaluate(t *testing.T) {
	t.Parallel()
	names := make([]string, 0, 3)
	for name := range shippedCatalogAssertions() {
		names = append(names, name)
	}
	slices.Sort(names)
	if len(names) != 3 {
		t.Fatalf("expected 3 shipped bodies, have %d", len(names))
	}
	for _, name := range names {
		body := shippedCatalogAssertions()[name]
		t.Run(name, func(t *testing.T) {
			// Prerequisite affirmed: the assertion passes.
			if err := EvaluateAssertion(name, body, AssertionContext{Infra: map[string]any{"control_plane_present": true}}, redact.Keys{}); err != nil {
				t.Errorf("affirmed prerequisite must pass: %v", err)
			}
			// Prerequisite denied: the assertion halts with its authored message.
			err := EvaluateAssertion(name, body, AssertionContext{Infra: map[string]any{"control_plane_present": false}}, redact.Keys{})
			if !errors.Is(err, ErrAssertionFailed()) {
				t.Errorf("denied prerequisite must report ErrAssertionFailed: %v", err)
			}
			// Key absent entirely: infra is an open map, so the halt comes from the
			// surface's missing-key policy rather than from a reference refusal.
			err = EvaluateAssertion(name, body, AssertionContext{Infra: map[string]any{}}, redact.Keys{})
			if err == nil {
				t.Error("an absent prerequisite key must halt under missingkey=error")
			}
			if !strings.Contains(err.Error(), "control_plane_present") {
				t.Errorf("the halt must name the absent key: %v", err)
			}
			t.Logf("absent-key halt: %v", err)
		})
	}
}

// At the Component / Feature phase Provides, Locals and Recipe carry nothing, and a
// reference to one halts rather than rendering. The pillars are open maps so the
// halt comes from the missing-key policy; .recipe is a struct behind a pointer, so
// a reference under it halts on the nil rather than resolving to a zero struct.
func TestEvaluateAssertion_NilPhaseReferencesHalt(t *testing.T) {
	t.Parallel()
	step4b := AssertionContext{
		Values:   map[string]any{"port": 8080},
		Infra:    map[string]any{},
		Uses:     map[string]any{},
		Features: map[string]any{},
		Metadata: map[string]any{},
	}
	for _, tc := range []struct{ body, names string }{
		{`{{ if .provides.host }}{{ end }}`, "host"},
		{`{{ if .locals.x }}{{ end }}`, "x"},
		{`{{ if .recipe.metadata }}{{ end }}`, "metadata"},
	} {
		err := EvaluateAssertion("nilphase", tc.body, step4b, redact.Keys{})
		if err == nil {
			t.Errorf("%s must halt at the Component/Feature phase", tc.body)
			continue
		}
		if !strings.Contains(err.Error(), tc.names) {
			t.Errorf("%s: the halt must name %q: %v", tc.body, tc.names, err)
		}
		t.Logf("%-34s -> %v", tc.body, err)
	}
	// A reference to a name the context does not define at all is refused before
	// execution, which is a different class from an absent key under a defined one.
	err := EvaluateAssertion("nilphase", `{{ if .NoSuchRoot }}{{ end }}`, step4b, redact.Keys{})
	if !errors.Is(err, ErrTemplateUnknownRoot()) {
		t.Errorf("an undefined reference must report ErrTemplateUnknownRoot: %v", err)
	}
	t.Logf("undefined reference   -> %v", err)
}
