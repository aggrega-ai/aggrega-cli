// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"errors"
	"slices"
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
	want := []string{"Values", "Infra", "Uses", "Features", "Metadata", "Provides", "Locals", "Recipe"}
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
	// .Recipe is a struct behind a pointer, so its keys are checked too.
	keys, gated := (templateScope{data: AssertionContext{}}).admittedKeys("Recipe")
	if !gated || strings.Join(keys, ",") != "Metadata,Source" {
		t.Errorf(".Recipe admits %v (gated=%v), want Metadata,Source", keys, gated)
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

// The assertion bodies shipped in the catalog today, verbatim. They are exercised
// here rather than inferred from the context type: an assertion surface whose
// authored references stop resolving is a build that fails on content nobody
// touched.
func shippedCatalogAssertions() map[string]string {
	return map[string]string{
		"kafka_operator_prerequisite": "{{- if not .Infra.control_plane_present -}}\n" +
			`{{- fail "the Strimzi Cluster Operator must be installed in the target cluster before this realization emits its Kafka custom resource; once the operator is present, affirm the prerequisite by setting infra.control_plane_present to true in the Release" -}}` +
			"\n{{- end -}}",
		"gateway_control_plane_prerequisite": "{{- if not .Infra.control_plane_present -}}\n" +
			`{{- fail "the Envoy AI Gateway control plane must be installed in the target cluster before this realization emits its custom resources; once the gateway is present, affirm the prerequisite by setting infra.control_plane_present to true in the Release" -}}` +
			"\n{{- end -}}",
		"kserve_control_plane_prerequisite": "{{- if not .Infra.control_plane_present -}}\n" +
			`{{- fail "the KServe control plane must be installed in the target cluster before this realization emits its InferenceService; once KServe is present, affirm the prerequisite by setting infra.control_plane_present to true in the Release" -}}` +
			"\n{{- end -}}",
		"kubeai_control_plane_prerequisite": "{{- if not .Infra.control_plane_present -}}\n" +
			`{{- fail "the KubeAI control plane must be installed in the target cluster before this realization emits its Model custom resource; once KubeAI is present, affirm the prerequisite by setting infra.control_plane_present to true in the Release" -}}` +
			"\n{{- end -}}",
	}
}

func TestEvaluateAssertion_ShippedCatalogBodiesEvaluate(t *testing.T) {
	t.Parallel()
	names := make([]string, 0, 4)
	for name := range shippedCatalogAssertions() {
		names = append(names, name)
	}
	slices.Sort(names)
	if len(names) != 4 {
		t.Fatalf("expected 4 shipped bodies, have %d", len(names))
	}
	for _, name := range names {
		body := shippedCatalogAssertions()[name]
		t.Run(name, func(t *testing.T) {
			// Prerequisite affirmed: the assertion passes.
			if err := EvaluateAssertion(name, body, AssertionContext{Infra: map[string]any{"control_plane_present": true}}); err != nil {
				t.Errorf("affirmed prerequisite must pass: %v", err)
			}
			// Prerequisite denied: the assertion halts with its authored message.
			err := EvaluateAssertion(name, body, AssertionContext{Infra: map[string]any{"control_plane_present": false}})
			if !errors.Is(err, ErrAssertionFailed) {
				t.Errorf("denied prerequisite must report ErrAssertionFailed: %v", err)
			}
			// Key absent entirely: infra is an open map, so the halt comes from the
			// surface's missing-key policy rather than from a reference refusal.
			err = EvaluateAssertion(name, body, AssertionContext{Infra: map[string]any{}})
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
// halt comes from the missing-key policy; .Recipe is a struct behind a pointer, so
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
		{`{{ if .Provides.host }}{{ end }}`, "host"},
		{`{{ if .Locals.x }}{{ end }}`, "x"},
		{`{{ if .Recipe.Metadata }}{{ end }}`, "Metadata"},
	} {
		err := EvaluateAssertion("nilphase", tc.body, step4b)
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
	err := EvaluateAssertion("nilphase", `{{ if .NoSuchRoot }}{{ end }}`, step4b)
	if !errors.Is(err, ErrTemplateUnknownRoot) {
		t.Errorf("an undefined reference must report ErrTemplateUnknownRoot: %v", err)
	}
	t.Logf("undefined reference   -> %v", err)
}
