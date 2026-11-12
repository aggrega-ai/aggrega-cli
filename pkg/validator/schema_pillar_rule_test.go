// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package validator

import (
	"errors"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
	"go.yaml.in/yaml/v3"
)

// TestValidateSchemaPillars_RefusesAnUnknownKeyOnBothAuthoredKinds drives the
// pillar-key rule over each authored kind that carries a schema block, because a
// rule declared per document kind can cover one kind and silently pass the other — and a
// half-covering refusal reads as coverage.
//
// It asserts the condition's identity as well as the words, because a consumer
// matches the identity and the two are carried independently.
func TestValidateSchemaPillars_RefusesAnUnknownKeyOnBothAuthoredKinds(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		kind    string
		entity  string
		path    string
		body    string
		locator string
		key     string
	}{
		{
			name:    "Component",
			kind:    "Component",
			entity:  "core/datastore/first",
			path:    "fixtures/first.yaml",
			body:    "bogusPillarKey:\n  host: { type: string }\n",
			locator: ".spec.schema.bogusPillarKey",
			key:     `"bogusPillarKey"`,
		},
		{
			name:    "Feature",
			kind:    "Feature",
			entity:  "core/prom-exporter",
			path:    "fixtures/prom.yaml",
			body:    "alsoBogus:\n  port: { type: integer }\n",
			locator: ".spec.schema.alsoBogus",
			key:     `"alsoBogus"`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := ValidateSchemaPillars(
				tc.kind, tc.entity, tc.path, "", parseSchemaBody(t, tc.body), nil,
			)
			if err == nil {
				t.Fatalf("a %s whose schema block names no pillar must be refused; got nil", tc.kind)
			}
			if !errors.Is(err, ErrUnknownSchemaPillar()) {
				t.Errorf("the refusal must carry the unknown-pillar condition; got: %v", err)
			}
			msg := err.Error()
			for _, want := range []string{
				tc.kind, tc.entity, tc.path, tc.locator, tc.key,
				"allowed: values, infra, provides",
				"data-pillars.md",
			} {
				if !strings.Contains(msg, want) {
					t.Errorf("the diagnostic should carry %q; got: %s", want, msg)
				}
			}
		})
	}
}

// TestValidateSchemaPillars_ReportsTheFirstOffendingKeyTheAuthorWrote pins which
// of several offending keys reaches a caller that reads one error.
//
// The rule is invoked once per key and reports each, so which one a caller sees is
// the order the keys were authored in rather than an artifact of the walk. The
// fixture writes the two in an order the alphabet reverses, so a reading that
// sorted would report the other one.
func TestValidateSchemaPillars_ReportsTheFirstOffendingKeyTheAuthorWrote(t *testing.T) {
	t.Parallel()
	node := parseSchemaBody(t, "zebra:\n  host: { type: string }\nalpha:\n  port: { type: integer }\n")
	err := ValidateSchemaPillars("Component", "core/two-bad", "fixtures/two.yaml", "", node, nil)
	if err == nil {
		t.Fatal("a schema block with two keys naming no pillar must be refused; got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, ".spec.schema.zebra") {
		t.Errorf("the first key the author wrote should be reported; got: %s", msg)
	}
	if strings.Contains(msg, "alpha") {
		t.Errorf("only one finding reaches a caller that reads one error; got: %s", msg)
	}
}

// TestValidateSchemaPillars_AcceptsEveryPillarOnBothKinds is the positive control
// for the table above: without it, a rule that refused every block would satisfy
// every assertion there.
func TestValidateSchemaPillars_AcceptsEveryPillarOnBothKinds(t *testing.T) {
	t.Parallel()
	body := "values:\n  port: { type: integer }\ninfra:\n  size: { type: string }\nprovides:\n  host: { type: string }\n"
	for _, kind := range []string{"Component", "Feature"} {
		if err := ValidateSchemaPillars(
			kind, "core/ok", "fixtures/ok.yaml", "", parseSchemaBody(t, body), nil,
		); err != nil {
			t.Errorf("a %s declaring every pillar must pass; got %v", kind, err)
		}
	}
}

// TestValidateSchemaPillars_RefusesAKindThatCarriesNoSchemaBlock pins the loud
// refusal on a kind no declaration covers.
//
// Answering nil there would pass every block of that kind in silence, which is the
// outcome a closed dispatch exists to prevent.
func TestValidateSchemaPillars_RefusesAKindThatCarriesNoSchemaBlock(t *testing.T) {
	t.Parallel()
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("a kind carrying no schema block must be refused, not passed over")
		}
		if !strings.Contains(strings.ToLower(errorText(r)), "recipe") {
			t.Errorf("the refusal should name the kind it was handed; got: %v", r)
		}
	}()
	_ = ValidateSchemaPillars(
		"Recipe", "core/api", "fixtures/api.yaml", "",
		parseSchemaBody(t, "values:\n  port: { type: integer }\n"), nil,
	)
}

// TestSchemaBlockViewsAnswerTheKeysTheAuthorWrote reads the fact off each kind's
// presentation directly, because the rule reads the key off its subject and would
// therefore pass over a presentation that enumerated nothing.
func TestSchemaBlockViewsAnswerTheKeysTheAuthorWrote(t *testing.T) {
	t.Parallel()
	node := parseSchemaBody(t, "provides:\n  host: { type: string }\nvalues:\n  port: { type: integer }\n")
	want := []string{"provides", "values"}

	component := newComponentSchemaBlockView("fixtures/c.yaml", node)
	if got := component.ComponentSchemaPillarKeys("fixtures/c.yaml"); !equalStrings(got, want) {
		t.Errorf("the Component view should answer %v in author order; got %v", want, got)
	}
	feature := newFeatureSchemaBlockView("fixtures/f.yaml", node)
	if got := feature.FeatureSchemaPillarKeys("fixtures/f.yaml"); !equalStrings(got, want) {
		t.Errorf("the Feature view should answer %v in author order; got %v", want, got)
	}
}

// TestComponentDocumentViewRefusesAFactItsPresentationDoesNotCarry pins the guard
// that keeps a Component's two presentations apart.
//
// One type answers every fact a Component carries while each presentation is built
// from one source, so the fact a presentation was not built for has to refuse: an
// empty answer is one a rule cannot tell from an entity that declared nothing.
func TestComponentDocumentViewRefusesAFactItsPresentationDoesNotCarry(t *testing.T) {
	t.Parallel()
	t.Run("the schema view refuses an assertion fact", func(t *testing.T) {
		t.Parallel()
		defer func() {
			if recover() == nil {
				t.Fatal("a schema-block view must refuse the assertion facts")
			}
		}()
		v := newComponentSchemaBlockView("fixtures/c.yaml", parseSchemaBody(t, "values: {}\n"))
		_ = v.EntityAssertionNameSet("fixtures/c.yaml")
	})
	t.Run("the chain view refuses the schema fact", func(t *testing.T) {
		t.Parallel()
		defer func() {
			if recover() == nil {
				t.Fatal("an extends-chain view must refuse the schema-block fact")
			}
		}()
		v := newComponentDocumentView("", nil)
		_ = v.ComponentSchemaPillarKeys("")
	})
}

// TestFeatureDocumentViewRefusesAnotherDocument pins the one-document contract on
// the presentation this increment added, because a view answering about a document
// it does not present hands back one document's fact under another's name.
func TestFeatureDocumentViewRefusesAnotherDocument(t *testing.T) {
	t.Parallel()
	defer func() {
		if recover() == nil {
			t.Fatal("a Feature view must refuse a document it was not built for")
		}
	}()
	v := newFeatureSchemaBlockView("fixtures/f.yaml", parseSchemaBody(t, "values: {}\n"))
	_ = v.FeatureSchemaPillarKeys("fixtures/other.yaml")
}

// TestAuthoredSchemaPillarKeysReadsOnlyAMapping pins that a node which is not a
// mapping yields no keys, which is what lets the caller settle the block's
// authoring shape in one place and hand over what it settled.
func TestAuthoredSchemaPillarKeysReadsOnlyAMapping(t *testing.T) {
	t.Parallel()
	if got := authoredSchemaPillarKeys(nil); got != nil {
		t.Errorf("a nil node declares no keys; got %v", got)
	}
	scalar := &yaml.Node{Kind: yaml.ScalarNode, Value: InheritedSchemaMarker}
	if got := authoredSchemaPillarKeys(scalar); got != nil {
		t.Errorf("a scalar node declares no keys; got %v", got)
	}
}

// TestSchemaPillarChecksShareOneConditionAndOneSeverity pins that the two document-kind
// declarations are two spellings of one rule.
//
// Two declarations raising two identities would make the diagnostic an author
// reads depend on which authored kind they wrote, which is the outcome declaring
// the rule per document kind exists to avoid.
func TestSchemaPillarChecksShareOneConditionAndOneSeverity(t *testing.T) {
	t.Parallel()
	if !errors.Is(componentSchemaKeyNamesAPillar.Sentinel, featureSchemaKeyNamesAPillar.Sentinel) {
		t.Errorf("the two document-kind declarations must raise one condition; got %v and %v",
			componentSchemaKeyNamesAPillar.Sentinel, featureSchemaKeyNamesAPillar.Sentinel)
	}
	if !errors.Is(componentSchemaKeyNamesAPillar.Sentinel, ErrUnknownSchemaPillar()) {
		t.Errorf("that condition must be the unknown-pillar condition; got %v",
			componentSchemaKeyNamesAPillar.Sentinel)
	}
	if componentSchemaKeyNamesAPillar.Severity != constraint.SeverityFatal ||
		featureSchemaKeyNamesAPillar.Severity != constraint.SeverityFatal {
		t.Errorf("both declarations must halt; got %v and %v",
			componentSchemaKeyNamesAPillar.Severity, featureSchemaKeyNamesAPillar.Severity)
	}
}

// errorText renders whatever a recover returned, so a refusal's wording can be
// asserted without the assertion caring how it was raised.
func errorText(r any) string {
	if err, ok := r.(error); ok {
		return err.Error()
	}
	if s, ok := r.(string); ok {
		return s
	}
	return ""
}
