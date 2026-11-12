// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package envelope

import (
	"errors"
	"strings"
	"testing"
)

// componentExtendsDoc builds a Component document whose spec carries the given
// extends line and schema block, each already indented for the spec body.
func componentExtendsDoc(specBody string) []byte {
	return []byte(`apiVersion: aggrega.ai/v1beta1
kind: Component
metadata:
  name: acc/subject
  version: "1.0.0"
  labels:
    component.aggrega.ai/type: server
spec:
` + specBody)
}

// TestEnvelope_ExtendingComponentSchemaMarker is the load-time pin of the third
// mechanical ban on this surface: a Component that extends a base inherits the
// base's values / infra / provides exactly as the base authored them, so it
// declares `schema: inherited` and authors no pillar content.
//
// The point of the marker is that all three outcomes are LEGIBLE and no case is
// silent, so the test asserts on all three and on their diagnostics being
// DIFFERENT. Omitting the block and authoring content are different mistakes: an
// author who forgot the declaration and an author who tried to declare pillars
// need to be told different things. One diagnostic for both is the failure mode
// this marker exists to remove, and a single shared message would pass a
// weaker test that only checked that both are rejected.
func TestEnvelope_ExtendingComponentSchemaMarker(t *testing.T) {
	t.Parallel()

	// Admitted: extends plus the marker.
	if _, err := ValidateBytes(KindComponent, componentExtendsDoc(
		"  extends: acc/base\n  schema: inherited\n"), "child.yaml"); err != nil {
		t.Fatalf("extends plus the inheritance marker must pass the envelope: %v", err)
	}

	// Rejected: extends plus authored pillar content.
	_, authoredErr := ValidateBytes(KindComponent, componentExtendsDoc(
		"  extends: acc/base\n  schema:\n    values:\n      port: { type: integer }\n"), "child.yaml")
	if authoredErr == nil {
		t.Fatal("extends plus an authored spec.schema must fail the envelope; got nil")
	}
	if !errors.Is(authoredErr, ErrEntityEnvelope()) {
		t.Fatalf("want errors.Is(err, ErrEntityEnvelope); got %v", authoredErr)
	}

	// Rejected: extends with spec.schema omitted. Absence is an error, never a
	// silent inheritance.
	_, omittedErr := ValidateBytes(KindComponent, componentExtendsDoc(
		"  extends: acc/base\n"), "child.yaml")
	if omittedErr == nil {
		t.Fatal("extends with spec.schema omitted must fail the envelope; got nil")
	}
	if !errors.Is(omittedErr, ErrEntityEnvelope()) {
		t.Fatalf("want errors.Is(err, ErrEntityEnvelope); got %v", omittedErr)
	}

	// The two mistakes are separately diagnosable.
	if authoredErr.Error() == omittedErr.Error() {
		t.Errorf("authoring pillar content and omitting the declaration must produce DIFFERENT diagnostics; both said:\n%s", authoredErr.Error())
	}
	// Each diagnostic names the rule it violated, so the author learns what the
	// constraint is and not merely that something did not match.
	const rule = "extending-component-must-declare-schema-inherited"
	for name, err := range map[string]error{"authored": authoredErr, "omitted": omittedErr} {
		if !strings.Contains(err.Error(), rule) {
			t.Errorf("the %s diagnostic must name the rule %q:\n%s", name, rule, err.Error())
		}
	}
	// The authored-content diagnostic additionally names the remedy value.
	if !strings.Contains(authoredErr.Error(), "inherited") {
		t.Errorf("the authored-content diagnostic must name the value to write instead:\n%s", authoredErr.Error())
	}
	// The omitted diagnostic names the missing key.
	if !strings.Contains(omittedErr.Error(), "schema") {
		t.Errorf("the omitted diagnostic must name the missing key:\n%s", omittedErr.Error())
	}
}

// TestEnvelope_NonExtendingComponentAuthorsItsOwnSchema is the other half of the
// constraint, and the half a careless widening would break: the marker is
// admitted ONLY where extends is present. A Component that extends nothing has
// no base to inherit from, so writing the marker is meaningless and rejected —
// and its spec.schema must still be a map, exactly as before.
func TestEnvelope_NonExtendingComponentAuthorsItsOwnSchema(t *testing.T) {
	t.Parallel()

	// Admitted: authored content with no extends.
	if _, err := ValidateBytes(KindComponent, componentExtendsDoc(
		"  schema:\n    values:\n      port: { type: integer }\n"), "solo.yaml"); err != nil {
		t.Fatalf("a non-extending Component authoring pillar content must pass: %v", err)
	}
	// Admitted: no extends and no schema at all — a Component may declare no
	// pillars.
	if _, err := ValidateBytes(KindComponent, componentExtendsDoc(
		"  inject: [env]\n"), "solo.yaml"); err != nil {
		t.Fatalf("a non-extending Component with no spec.schema must pass: %v", err)
	}

	const rule = "non-extending-component-authors-its-own-schema"
	for _, tc := range []struct{ name, specBody string }{
		{"the marker without extends", "  schema: inherited\n"},
		{"any other scalar without extends", "  schema: something\n"},
		{"a sequence without extends", "  schema:\n    - values\n"},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := ValidateBytes(KindComponent, componentExtendsDoc(tc.specBody), "solo.yaml")
			if err == nil {
				t.Fatalf("%s must fail the envelope; got nil", tc.name)
			}
			if !errors.Is(err, ErrEntityEnvelope()) {
				t.Fatalf("want errors.Is(err, ErrEntityEnvelope); got %v", err)
			}
			if !strings.Contains(err.Error(), rule) {
				t.Errorf("the diagnostic must name the rule %q:\n%s", rule, err.Error())
			}
		})
	}
}

// TestEnvelope_ExtendingComponentMarkerIsExact pins that the marker is one
// value, not a family: a near-miss spelling is rejected rather than quietly
// treated as an inheritance declaration.
func TestEnvelope_ExtendingComponentMarkerIsExact(t *testing.T) {
	t.Parallel()
	for _, value := range []string{"inherit", "Inherited", "INHERITED", "inherited\\n", "true"} {
		value := value
		t.Run(value, func(t *testing.T) {
			t.Parallel()
			_, err := ValidateBytes(KindComponent, componentExtendsDoc(
				"  extends: acc/base\n  schema: "+value+"\n"), "child.yaml")
			if err == nil {
				t.Fatalf("spec.schema: %q on an extending Component must fail the envelope; got nil", value)
			}
			if !errors.Is(err, ErrEntityEnvelope()) {
				t.Fatalf("want errors.Is(err, ErrEntityEnvelope); got %v", err)
			}
		})
	}
}

// TestEnvelope_FeatureExtendsUnaffected pins the scope of the constraint: it is
// Component-side. An extending Feature still composes its base's pillars with
// its own, so it authors pillar content and does not write the marker.
func TestEnvelope_FeatureExtendsUnaffected(t *testing.T) {
	t.Parallel()
	doc := []byte(`apiVersion: aggrega.ai/v1beta1
kind: Feature
metadata:
  name: acc/child-feature
  version: "1.0.0"
spec:
  extends: acc/base-feature
  schema:
    values:
      threshold: { type: integer }
`)
	if _, err := ValidateBytes(KindFeature, doc, "feature.yaml"); err != nil {
		t.Fatalf("an extending Feature authoring pillar content must still pass: %v", err)
	}
}
