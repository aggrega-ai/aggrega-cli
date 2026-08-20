// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package merger

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"

	validator "github.com/aggrega-ai/aggrega-cli/pkg/validator/jsonschema"
)

// TestMerge_EmptyChainErrors verifies Merge rejects an empty input
// chain with ErrMergeInvalid.
func TestMerge_EmptyChainErrors(t *testing.T) {
	t.Parallel()
	_, err := Merge(nil)
	if err == nil {
		t.Fatal("expected error on empty chain; got nil")
	}
	if !errors.Is(err, ErrMergeInvalid()) {
		t.Errorf("expected ErrMergeInvalid; got %v", err)
	}
}

// TestMerge_NilSchemaInChainErrors verifies Merge rejects a chain
// containing a nil schema.
func TestMerge_NilSchemaInChainErrors(t *testing.T) {
	t.Parallel()
	good := mustSchema(t, `{"type": "string"}`, "good.yaml:1")
	_, err := Merge([]*Schema{good, nil})
	if err == nil {
		t.Fatal("expected error on nil schema in chain; got nil")
	}
	if !errors.Is(err, ErrMergeInvalid()) {
		t.Errorf("expected ErrMergeInvalid; got %v", err)
	}
}

// TestMerge_SingleSchemaReturnsAsIs verifies a single-element chain
// returns the element with provenance tagged from its Source.
func TestMerge_SingleSchemaReturnsAsIs(t *testing.T) {
	t.Parallel()
	s := mustSchema(t, `{"type": "string", "minLength": 1}`, "single.yaml:1")
	out, err := Merge([]*Schema{s})
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	if out.Type != "string" {
		t.Errorf("expected type=string; got %q", out.Type)
	}
	if out.MinLength == nil || *out.MinLength != 1 {
		t.Errorf("expected minLength=1; got %v", out.MinLength)
	}
	origins := extractProvenance(t, out.Schema)
	if origins["type"] != "single.yaml:1" {
		t.Errorf("expected type origin=single.yaml:1; got %q", origins["type"])
	}
	if origins["minLength"] != "single.yaml:1" {
		t.Errorf("expected minLength origin=single.yaml:1; got %q", origins["minLength"])
	}
}

// TestMerge_ScalarOverride verifies child scalar constraints override
// parent's. Covers type / pattern / minLength as representative
// string + numeric + format keywords; the merge implementation runs
// the same logic for every scalar field via the merge table.
func TestMerge_ScalarOverride(t *testing.T) {
	t.Parallel()
	parent := mustSchema(t, `{"type": "string", "minLength": 1, "maxLength": 100, "pattern": "^a"}`, "parent.yaml:5")
	child := mustSchema(t, `{"minLength": 5, "pattern": "^b"}`, "child.yaml:12")
	out, err := Merge([]*Schema{parent, child})
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	if out.Type != "string" {
		t.Errorf("expected type=string (from parent); got %q", out.Type)
	}
	if *out.MinLength != 5 {
		t.Errorf("expected minLength=5 (child override); got %d", *out.MinLength)
	}
	if *out.MaxLength != 100 {
		t.Errorf("expected maxLength=100 (parent preserved); got %d", *out.MaxLength)
	}
	if out.Pattern != "^b" {
		t.Errorf("expected pattern=^b (child override); got %q", out.Pattern)
	}

	origins := extractProvenance(t, out.Schema)
	if origins["type"] != "parent.yaml:5" {
		t.Errorf("expected type origin=parent.yaml:5; got %q", origins["type"])
	}
	if origins["minLength"] != "child.yaml:12" {
		t.Errorf("expected minLength origin=child.yaml:12; got %q", origins["minLength"])
	}
	if origins["maxLength"] != "parent.yaml:5" {
		t.Errorf("expected maxLength origin=parent.yaml:5 (preserved from parent); got %q", origins["maxLength"])
	}
}

// TestMerge_EnumOverride verifies child enum overrides parent's.
// Narrowing detection is exercised in TestMerge_NarrowingEnumWidens.
func TestMerge_EnumOverride(t *testing.T) {
	t.Parallel()
	parent := mustSchema(t, `{"enum": ["a", "b", "c"]}`, "parent.yaml:1")
	child := mustSchema(t, `{"enum": ["a", "b"]}`, "child.yaml:1")
	out, err := Merge([]*Schema{parent, child})
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	if len(out.Enum) != 2 {
		t.Errorf("expected enum len=2 (child override); got %d (%v)", len(out.Enum), out.Enum)
	}
}

// TestMerge_RequiredUnion verifies the required-keys merge is a
// sorted, deduplicated union.
func TestMerge_RequiredUnion(t *testing.T) {
	t.Parallel()
	parent := mustSchema(t, `{"required": ["alpha", "bravo"]}`, "parent.yaml:1")
	child := mustSchema(t, `{"required": ["bravo", "charlie"]}`, "child.yaml:1")
	out, err := Merge([]*Schema{parent, child})
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	expected := []string{"alpha", "bravo", "charlie"}
	if !reflect.DeepEqual(out.Required, expected) {
		t.Errorf("expected required=%v; got %v", expected, out.Required)
	}
}

// TestMerge_PropertiesLayer verifies child property keys layer into
// parent's, with matched keys recursively merged.
func TestMerge_PropertiesLayer(t *testing.T) {
	t.Parallel()
	parent := mustSchema(t, `{
        "properties": {
            "alpha": {"type": "string"},
            "bravo": {"type": "integer", "minimum": 0}
        }
    }`, "parent.yaml:1")
	child := mustSchema(t, `{
        "properties": {
            "bravo": {"minimum": 5},
            "charlie": {"type": "boolean"}
        }
    }`, "child.yaml:1")
	out, err := Merge([]*Schema{parent, child})
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	if _, ok := out.Properties["alpha"]; !ok {
		t.Errorf("expected properties.alpha preserved from parent; got %v", out.Properties)
	}
	if _, ok := out.Properties["charlie"]; !ok {
		t.Errorf("expected properties.charlie added from child; got %v", out.Properties)
	}
	bravo, ok := out.Properties["bravo"]
	if !ok {
		t.Fatalf("expected properties.bravo present; got %v", out.Properties)
	}
	if bravo.Type != "integer" {
		t.Errorf("expected bravo.type=integer (from parent, not overridden); got %q", bravo.Type)
	}
	if bravo.Minimum == nil || *bravo.Minimum != 5 {
		t.Errorf("expected bravo.minimum=5 (child override); got %v", bravo.Minimum)
	}
	// PropertyOrder is sorted-alphabetic union.
	expected := []string{"alpha", "bravo", "charlie"}
	if !reflect.DeepEqual(out.PropertyOrder, expected) {
		t.Errorf("expected PropertyOrder=%v; got %v", expected, out.PropertyOrder)
	}
}

// TestMerge_DefsLayer verifies $defs entries merge (child wins on
// collision).
func TestMerge_DefsLayer(t *testing.T) {
	t.Parallel()
	parent := mustSchema(t, `{
        "$defs": {
            "Shared": {"type": "string", "minLength": 1},
            "ParentOnly": {"type": "integer"}
        }
    }`, "parent.yaml:1")
	child := mustSchema(t, `{
        "$defs": {
            "Shared": {"type": "string", "minLength": 5},
            "ChildOnly": {"type": "boolean"}
        }
    }`, "child.yaml:1")
	out, err := Merge([]*Schema{parent, child})
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	if _, ok := out.Defs["ParentOnly"]; !ok {
		t.Errorf("expected $defs.ParentOnly preserved; got %v", out.Defs)
	}
	if _, ok := out.Defs["ChildOnly"]; !ok {
		t.Errorf("expected $defs.ChildOnly added; got %v", out.Defs)
	}
	shared := out.Defs["Shared"]
	if shared == nil || shared.MinLength == nil || *shared.MinLength != 5 {
		t.Errorf("expected $defs.Shared.minLength=5 (child wins on collision); got %v", shared)
	}
}

// TestMerge_CompositionAppend verifies allOf / anyOf / oneOf lists
// append child entries to parent's (preserving both authors' clauses).
func TestMerge_CompositionAppend(t *testing.T) {
	t.Parallel()
	parent := mustSchema(t, `{
        "allOf": [{"type": "string"}, {"minLength": 1}]
    }`, "parent.yaml:1")
	child := mustSchema(t, `{
        "allOf": [{"maxLength": 100}]
    }`, "child.yaml:1")
	out, err := Merge([]*Schema{parent, child})
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	if len(out.AllOf) != 3 {
		t.Errorf("expected allOf len=3 (parent 2 + child 1 appended); got %d", len(out.AllOf))
	}
}

// TestMerge_IfThenElseAtomicOverride verifies child's if/then/else
// group fully replaces parent's group when any of the three is set
// on child.
func TestMerge_IfThenElseAtomicOverride(t *testing.T) {
	t.Parallel()
	parent := mustSchema(t, `{
        "if": {"properties": {"a": {"const": "yes"}}},
        "then": {"required": ["b"]},
        "else": {"required": ["c"]}
    }`, "parent.yaml:1")
	child := mustSchema(t, `{
        "if": {"properties": {"a": {"const": "no"}}},
        "then": {"required": ["d"]}
    }`, "child.yaml:1")
	out, err := Merge([]*Schema{parent, child})
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	if out.If == nil || out.If.Properties["a"] == nil || out.If.Properties["a"].Const == nil {
		t.Fatalf("expected child's if branch active; got %v", out.If)
	}
	if got := *out.If.Properties["a"].Const; got != "no" {
		t.Errorf("expected child's if (const=no); got %v", got)
	}
	if out.Then == nil || len(out.Then.Required) != 1 || out.Then.Required[0] != "d" {
		t.Errorf("expected child's then (required=[d]); got %v", out.Then)
	}
	// Parent's else is dropped because child set if/then.
	if out.Else != nil {
		t.Errorf("expected else dropped (child didn't set it; atomic override); got %v", out.Else)
	}
}

// TestMerge_ExtraExtensions verifies Extra entries merge — child
// entries override parent on collision; non-colliding entries union.
// __source_origin is reserved-namespace and recomputed by the
// provenance pass (any author-supplied value is overwritten).
func TestMerge_ExtraExtensions(t *testing.T) {
	t.Parallel()
	parent := mustSchema(t, `{
        "x-aggrega-validate": "{{ ne . \"reserved\" }}",
        "x-parent-only": "p"
    }`, "parent.yaml:1")
	child := mustSchema(t, `{
        "x-aggrega-validate": "{{ gt . 5 }}",
        "x-child-only": "c"
    }`, "child.yaml:1")
	out, err := Merge([]*Schema{parent, child})
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	if got := out.Extra["x-aggrega-validate"].(string); got != "{{ gt . 5 }}" {
		t.Errorf("expected x-aggrega-validate child override; got %q", got)
	}
	if got, ok := out.Extra["x-parent-only"].(string); !ok || got != "p" {
		t.Errorf("expected x-parent-only preserved; got %v", out.Extra["x-parent-only"])
	}
	if got, ok := out.Extra["x-child-only"].(string); !ok || got != "c" {
		t.Errorf("expected x-child-only added; got %v", out.Extra["x-child-only"])
	}
}

// TestMerge_MultiLevelChain verifies grandparent + parent + child
// chain merges correctly through the pairwise left-fold.
func TestMerge_MultiLevelChain(t *testing.T) {
	t.Parallel()
	grandparent := mustSchema(t, `{
        "type": "object",
        "properties": {
            "alpha": {"type": "string"}
        }
    }`, "grandparent.yaml:1")
	parent := mustSchema(t, `{
        "properties": {
            "bravo": {"type": "integer"}
        }
    }`, "parent.yaml:1")
	child := mustSchema(t, `{
        "properties": {
            "charlie": {"type": "boolean"}
        }
    }`, "child.yaml:1")
	out, err := Merge([]*Schema{grandparent, parent, child})
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	if out.Type != "object" {
		t.Errorf("expected type=object from grandparent; got %q", out.Type)
	}
	for _, k := range []string{"alpha", "bravo", "charlie"} {
		if _, ok := out.Properties[k]; !ok {
			t.Errorf("expected properties.%s present; got %v", k, out.Properties)
		}
	}
}

// TestMerge_ProvenanceMultiKeywordNode verifies a node with
// constraints from both parent and child carries __source_origin
// entries for each keyword's actual contributor.
func TestMerge_ProvenanceMultiKeywordNode(t *testing.T) {
	t.Parallel()
	parent := mustSchema(t, `{
        "properties": {
            "port": {"type": "integer", "minimum": 1}
        }
    }`, "parent.yaml:7")
	child := mustSchema(t, `{
        "properties": {
            "port": {"maximum": 65535}
        }
    }`, "child.yaml:11")
	out, err := Merge([]*Schema{parent, child})
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	port := out.Properties["port"]
	if port == nil {
		t.Fatalf("expected properties.port; got nil")
	}
	origins := extractProvenance(t, port)
	if origins["type"] != "parent.yaml:7" {
		t.Errorf("expected port.type origin=parent.yaml:7; got %q", origins["type"])
	}
	if origins["minimum"] != "parent.yaml:7" {
		t.Errorf("expected port.minimum origin=parent.yaml:7; got %q", origins["minimum"])
	}
	if origins["maximum"] != "child.yaml:11" {
		t.Errorf("expected port.maximum origin=child.yaml:11; got %q", origins["maximum"])
	}
}

// TestMerge_ProvenanceMultiLevelChain verifies each constraint's
// origin in a grandparent + parent + child chain is tagged with the
// correct contributing schema's Source.
func TestMerge_ProvenanceMultiLevelChain(t *testing.T) {
	t.Parallel()
	grandparent := mustSchema(t, `{
        "properties": {
            "port": {"type": "integer", "minimum": 1, "maximum": 65535}
        }
    }`, "grandparent.yaml:1")
	parent := mustSchema(t, `{
        "properties": {
            "port": {"maximum": 9999}
        }
    }`, "parent.yaml:1")
	child := mustSchema(t, `{
        "properties": {
            "port": {"minimum": 1024}
        }
    }`, "child.yaml:1")
	out, err := Merge([]*Schema{grandparent, parent, child})
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	port := out.Properties["port"]
	origins := extractProvenance(t, port)
	if origins["type"] != "grandparent.yaml:1" {
		t.Errorf("expected port.type origin=grandparent.yaml:1 (preserved through chain); got %q", origins["type"])
	}
	if origins["minimum"] != "child.yaml:1" {
		t.Errorf("expected port.minimum origin=child.yaml:1 (child override); got %q", origins["minimum"])
	}
	if origins["maximum"] != "parent.yaml:1" {
		t.Errorf("expected port.maximum origin=parent.yaml:1 (parent override of grandparent); got %q", origins["maximum"])
	}
}

// TestMerge_NarrowingMinWidens verifies a NarrowingViolation lands
// when child widens parent's minimum (child < parent).
func TestMerge_NarrowingMinWidens(t *testing.T) {
	t.Parallel()
	parent := mustSchema(t, `{"properties": {"port": {"minimum": 1024}}}`, "parent.yaml:1")
	child := mustSchema(t, `{"properties": {"port": {"minimum": 0}}}`, "child.yaml:1")
	out, err := Merge([]*Schema{parent, child})
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	if len(out.NarrowingViolations) != 1 {
		t.Fatalf("expected exactly 1 NarrowingViolation; got %d (%v)", len(out.NarrowingViolations), out.NarrowingViolations)
	}
	v := out.NarrowingViolations[0]
	if v.FieldPath != "port" {
		t.Errorf("expected FieldPath=port (instance-relative); got %q", v.FieldPath)
	}
	if v.Keyword != "minimum" {
		t.Errorf("expected Keyword=minimum; got %q", v.Keyword)
	}
	if v.ParentSource != "parent.yaml:1" {
		t.Errorf("expected ParentSource=parent.yaml:1; got %q", v.ParentSource)
	}
	if v.ChildSource != "child.yaml:1" {
		t.Errorf("expected ChildSource=child.yaml:1; got %q", v.ChildSource)
	}
}

// TestMerge_NarrowingMultiKeyword verifies multiple narrowing
// violations across different keywords on the same node all land.
func TestMerge_NarrowingMultiKeyword(t *testing.T) {
	t.Parallel()
	parent := mustSchema(t, `{"properties": {"port": {"minimum": 1024, "maximum": 9999}}}`, "parent.yaml:1")
	child := mustSchema(t, `{"properties": {"port": {"minimum": 0, "maximum": 65535}}}`, "child.yaml:1")
	out, err := Merge([]*Schema{parent, child})
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	if len(out.NarrowingViolations) != 2 {
		t.Fatalf("expected exactly 2 NarrowingViolations (minimum + maximum); got %d (%v)", len(out.NarrowingViolations), out.NarrowingViolations)
	}
}

// TestMerge_NarrowingOrderDeterministic verifies that a single merge's
// multiple narrowing violations are emitted in a fixed (FieldPath → Keyword →
// Detail → ParentSource → ChildSource) order regardless of the map-iteration
// order the per-field detection walks. Two independent merges of the same
// inputs must yield byte-identical NarrowingViolations slices, and the order
// must be the sorted one — the property the closure / whole-catalog narrowing
// advisories rely on to be stable run-to-run.
func TestMerge_NarrowingOrderDeterministic(t *testing.T) {
	t.Parallel()
	// alpha widens two keywords; mid and zeta one each — four violations across
	// three field paths, exercising the FieldPath sort and the per-path Keyword
	// tiebreak. Sorted order: alpha/maxLength, alpha/minimum, mid/maximum, zeta/minimum.
	parentJSON := `{"properties": {
		"zeta": {"minimum": 10},
		"alpha": {"minimum": 10, "maxLength": 5},
		"mid": {"maximum": 100}
	}}`
	childJSON := `{"properties": {
		"zeta": {"minimum": 0},
		"alpha": {"minimum": 0, "maxLength": 99},
		"mid": {"maximum": 999}
	}}`
	merge := func() []NarrowingViolation {
		parent := mustSchema(t, parentJSON, "parent.yaml:1")
		child := mustSchema(t, childJSON, "child.yaml:1")
		out, err := Merge([]*Schema{parent, child})
		if err != nil {
			t.Fatalf("Merge: %v", err)
		}
		return out.NarrowingViolations
	}

	got := merge()
	want := []struct{ field, keyword string }{
		{"alpha", "maxLength"},
		{"alpha", "minimum"},
		{"mid", "maximum"},
		{"zeta", "minimum"},
	}
	if len(got) != len(want) {
		t.Fatalf("expected %d narrowing violations; got %d (%v)", len(want), len(got), got)
	}
	for i, w := range want {
		if got[i].FieldPath != w.field || got[i].Keyword != w.keyword {
			t.Errorf("violation[%d] = (%s,%s); want (%s,%s) — not in sorted order: %v",
				i, got[i].FieldPath, got[i].Keyword, w.field, w.keyword, got)
		}
	}
	// Full-struct stability across an independent merge: the 5-field sort
	// canonicalizes the whole slice, so a re-merge (with its own map-iteration
	// order) produces an identical slice — this is the de-flake guarantee.
	if again := merge(); !reflect.DeepEqual(got, again) {
		t.Errorf("non-deterministic NarrowingViolations across merges:\n--first--\n%v\n--again--\n%v", got, again)
	}
}

// TestMerge_NarrowingEnumWidens verifies enum widening detection —
// child enum contains values not present in parent enum.
func TestMerge_NarrowingEnumWidens(t *testing.T) {
	t.Parallel()
	parent := mustSchema(t, `{"enum": ["a", "b"]}`, "parent.yaml:1")
	child := mustSchema(t, `{"enum": ["a", "c"]}`, "child.yaml:1")
	out, err := Merge([]*Schema{parent, child})
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	if len(out.NarrowingViolations) != 1 {
		t.Fatalf("expected 1 NarrowingViolation for enum widening; got %v", out.NarrowingViolations)
	}
	if out.NarrowingViolations[0].Keyword != "enum" {
		t.Errorf("expected enum violation; got %q", out.NarrowingViolations[0].Keyword)
	}
}

// TestMerge_NarrowingEnumSubsetNoViolation verifies a child enum that
// is a strict subset of parent's produces no violation (the merge is
// a valid narrowing).
func TestMerge_NarrowingEnumSubsetNoViolation(t *testing.T) {
	t.Parallel()
	parent := mustSchema(t, `{"enum": ["a", "b", "c"]}`, "parent.yaml:1")
	child := mustSchema(t, `{"enum": ["a", "b"]}`, "child.yaml:1")
	out, err := Merge([]*Schema{parent, child})
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	if len(out.NarrowingViolations) != 0 {
		t.Errorf("expected no violations (child subset is narrowing); got %v", out.NarrowingViolations)
	}
}

// TestMerge_NarrowingPatternHeuristic verifies the flag-and-defer
// pattern heuristic — patterns differ in any way produces a violation
// with a deferral message.
func TestMerge_NarrowingPatternHeuristic(t *testing.T) {
	t.Parallel()
	parent := mustSchema(t, `{"properties": {"name": {"pattern": "^[a-z]+$"}}}`, "parent.yaml:1")
	child := mustSchema(t, `{"properties": {"name": {"pattern": "^[A-Z]+$"}}}`, "child.yaml:1")
	out, err := Merge([]*Schema{parent, child})
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	if len(out.NarrowingViolations) != 1 {
		t.Fatalf("expected 1 violation for differing patterns; got %v", out.NarrowingViolations)
	}
	if !strings.Contains(out.NarrowingViolations[0].Detail, "deferred to policy plugin") {
		t.Errorf("expected deferral message in Detail; got %q", out.NarrowingViolations[0].Detail)
	}
}

// TestMerge_NarrowingSignalOnly verifies the merger does not halt on
// narrowing violations — Merge returns a successful merged schema
// with violations populated on the result.
func TestMerge_NarrowingSignalOnly(t *testing.T) {
	t.Parallel()
	parent := mustSchema(t, `{"properties": {"port": {"minimum": 1024}}}`, "parent.yaml:1")
	child := mustSchema(t, `{"properties": {"port": {"minimum": 0}}}`, "child.yaml:1")
	out, err := Merge([]*Schema{parent, child})
	if err != nil {
		t.Fatalf("Merge returned error on narrowing (expected signal-only); got %v", err)
	}
	if out == nil {
		t.Fatal("Merge returned nil schema on narrowing (expected merged result); got nil")
	}
	if len(out.NarrowingViolations) == 0 {
		t.Error("expected NarrowingViolations populated; got empty")
	}
}

// TestMerge_NarrowingParentSourceAcrossChain verifies that when a
// grandparent supplies a constraint, the parent doesn't override,
// and the child widens, the violation's ParentSource names the
// grandparent's file:line (not the synthetic accumulator state).
// This is the operator's concern-6 assertion from the first-gate
// review.
func TestMerge_NarrowingParentSourceAcrossChain(t *testing.T) {
	t.Parallel()
	grandparent := mustSchema(t, `{"properties": {"port": {"minimum": 1024}}}`, "grandparent.yaml:5")
	parent := mustSchema(t, `{"properties": {"port": {"type": "integer"}}}`, "parent.yaml:5")
	child := mustSchema(t, `{"properties": {"port": {"minimum": 0}}}`, "child.yaml:5")
	out, err := Merge([]*Schema{grandparent, parent, child})
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	if len(out.NarrowingViolations) != 1 {
		t.Fatalf("expected 1 violation; got %v", out.NarrowingViolations)
	}
	v := out.NarrowingViolations[0]
	if v.ParentSource != "grandparent.yaml:5" {
		t.Errorf("expected ParentSource=grandparent.yaml:5 (actual originator via provenance map); got %q", v.ParentSource)
	}
	if v.ChildSource != "child.yaml:5" {
		t.Errorf("expected ChildSource=child.yaml:5; got %q", v.ChildSource)
	}
}

// TestMerge_PropertyOrderDeterministic verifies the merged output's
// PropertyOrder is the sorted-alphabetic union of input schemas'
// property names regardless of input order.
func TestMerge_PropertyOrderDeterministic(t *testing.T) {
	t.Parallel()
	parent := mustSchema(t, `{
        "properties": {
            "zulu": {"type": "string"},
            "alpha": {"type": "string"}
        }
    }`, "parent.yaml:1")
	child := mustSchema(t, `{
        "properties": {
            "mike": {"type": "string"},
            "bravo": {"type": "string"}
        }
    }`, "child.yaml:1")
	out, err := Merge([]*Schema{parent, child})
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	expected := []string{"alpha", "bravo", "mike", "zulu"}
	if !reflect.DeepEqual(out.PropertyOrder, expected) {
		t.Errorf("expected PropertyOrder=%v (sorted union); got %v", expected, out.PropertyOrder)
	}
}

// TestMerge_Determinism verifies the merged output is byte-identical
// across 25 trials. Sidesteps Go's randomised map iteration: each
// merge run produces the same Findings + same ordering.
func TestMerge_Determinism(t *testing.T) {
	t.Parallel()
	parent := mustSchema(t, `{
        "type": "object",
        "properties": {
            "alpha": {"type": "string"},
            "bravo": {"type": "integer", "minimum": 0},
            "charlie": {"type": "boolean"}
        },
        "required": ["alpha", "bravo"]
    }`, "parent.yaml:1")
	child := mustSchema(t, `{
        "properties": {
            "bravo": {"minimum": 10},
            "delta": {"type": "string"}
        },
        "required": ["delta"]
    }`, "child.yaml:1")
	assertDeterministicMerge(t, []*Schema{parent, child}, 25)
}

// TestResolveChain_NoExtends verifies a child with no
// Extra["extends"] returns a single-element chain.
func TestResolveChain_NoExtends(t *testing.T) {
	t.Parallel()
	child := mustSchema(t, `{"type": "string"}`, "child.yaml:1")
	chain, err := ResolveChain(child, func(uri string) (*Schema, bool) {
		t.Errorf("lookup should not be called when no extends; called with %q", uri)
		return nil, false
	})
	if err != nil {
		t.Fatalf("ResolveChain: %v", err)
	}
	if len(chain) != 1 || chain[0] != child {
		t.Errorf("expected single-element chain [child]; got %v", chain)
	}
}

// TestResolveChain_SingleParent verifies a child with extends to one
// parent returns [parent, child].
func TestResolveChain_SingleParent(t *testing.T) {
	t.Parallel()
	parent := mustSchema(t, `{"type": "object"}`, "parent.yaml:1")
	child := mustSchema(t, `{"properties": {"a": {"type": "string"}}}`, "child.yaml:1")
	child.Extra = map[string]any{"extends": "parent-uri"}

	chain, err := ResolveChain(child, func(uri string) (*Schema, bool) {
		if uri == "parent-uri" {
			return parent, true
		}
		return nil, false
	})
	if err != nil {
		t.Fatalf("ResolveChain: %v", err)
	}
	if len(chain) != 2 {
		t.Fatalf("expected chain len=2; got %d", len(chain))
	}
	if chain[0] != parent || chain[1] != child {
		t.Errorf("expected chain=[parent, child]; got [%v, %v]", chain[0], chain[1])
	}
}

// TestResolveChain_CycleDetection verifies a circular extends chain
// produces ErrCyclicExtension.
func TestResolveChain_CycleDetection(t *testing.T) {
	t.Parallel()
	a := mustSchema(t, `{"type": "object"}`, "a-uri")
	a.Extra = map[string]any{"extends": "b-uri"}
	b := mustSchema(t, `{"type": "object"}`, "b-uri")
	b.Extra = map[string]any{"extends": "a-uri"}

	_, err := ResolveChain(a, func(uri string) (*Schema, bool) {
		switch uri {
		case "a-uri":
			return a, true
		case "b-uri":
			return b, true
		}
		return nil, false
	})
	if err == nil {
		t.Fatal("expected ErrCyclicExtension; got nil")
	}
	if !errors.Is(err, ErrCyclicExtension()) {
		t.Errorf("expected ErrCyclicExtension; got %v", err)
	}
}

// TestResolveChain_MaxDepth verifies an 11-level chain produces
// ErrMaxDepth.
func TestResolveChain_MaxDepth(t *testing.T) {
	t.Parallel()
	// Build 12 schemas where each extends the next via a unique URI.
	// The walker exits with ErrMaxDepth at length 10.
	const total = 12
	schemas := make([]*Schema, total)
	for i := 0; i < total; i++ {
		s := mustSchema(t, `{"type": "object"}`, itoaTest(i))
		if i < total-1 {
			s.Extra = map[string]any{"extends": itoaTest(i + 1)}
		}
		schemas[i] = s
	}
	lookup := func(uri string) (*Schema, bool) {
		for _, s := range schemas {
			if s.Source == uri {
				return s, true
			}
		}
		return nil, false
	}
	_, err := ResolveChain(schemas[0], lookup)
	if err == nil {
		t.Fatal("expected ErrMaxDepth; got nil")
	}
	if !errors.Is(err, ErrMaxDepth()) {
		t.Errorf("expected ErrMaxDepth; got %v", err)
	}
}

// TestResolveChain_MissingParent verifies an extends URI that the
// lookup callback cannot resolve produces a clear error.
func TestResolveChain_MissingParent(t *testing.T) {
	t.Parallel()
	child := mustSchema(t, `{"type": "object"}`, "child.yaml:1")
	child.Extra = map[string]any{"extends": "missing-uri"}

	_, err := ResolveChain(child, func(uri string) (*Schema, bool) {
		return nil, false
	})
	if err == nil {
		t.Fatal("expected error on missing parent; got nil")
	}
	if !errors.Is(err, ErrMergeInvalid()) {
		t.Errorf("expected ErrMergeInvalid; got %v", err)
	}
}

// TestResolveChain_NilChild verifies the boundary on nil input.
func TestResolveChain_NilChild(t *testing.T) {
	t.Parallel()
	_, err := ResolveChain(nil, func(uri string) (*Schema, bool) {
		return nil, false
	})
	if err == nil {
		t.Fatal("expected error on nil child; got nil")
	}
	if !errors.Is(err, ErrMergeInvalid()) {
		t.Errorf("expected ErrMergeInvalid; got %v", err)
	}
}

// TestMergedSchemaProvenanceIgnoredByValidator is a regression-guard
// test against future changes to the validator's Extra-walking
// behavior that would inadvertently process __source_origin as a
// semantic extension.
//
// If this test fails, the validator started walking non-x-aggrega-*
// Extra keys; either filter __source_origin explicitly in the
// validator's walker, or revisit the merger's namespace convention.
//
// Today the test passes by construction — the validator's walker at
// pkg/validator/jsonschema/walker.go only inspects Extra entries
// prefixed with "x-aggrega-when" / "x-aggrega-validate" /
// "x-aggrega-validateMessage". __source_origin is opaque to the
// validator. The merged schema with provenance flows through New()
// without error and the resulting validator validates instances
// normally.
//
// The test exercises the full merge surface: Merge produces a
// schema carrying __source_origin; jsonschema.New consumes the same
// schema without error; the resulting Validator validates instances
// and emits Findings only on real constraint violations, never on
// the __source_origin keys.
func TestMergedSchemaProvenanceIgnoredByValidator(t *testing.T) {
	t.Parallel()

	parent := mustSchema(t, `{
        "type": "object",
        "properties": {
            "port": {"type": "integer", "minimum": 1, "maximum": 65535}
        }
    }`, "parent.yaml:1")
	child := mustSchema(t, `{
        "properties": {
            "port": {"minimum": 1024}
        }
    }`, "child.yaml:1")
	merged, err := Merge([]*Schema{parent, child})
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	// Confirm __source_origin is on the merged tree (precondition).
	origins := extractProvenance(t, merged.Properties["port"])
	if len(origins) == 0 {
		t.Fatal("precondition failed: merged port has no __source_origin")
	}

	// Construct a Validator from the merged schema. If the validator's
	// walker treated __source_origin as a semantic extension, this
	// constructor would fail or produce a Validator that emits
	// spurious findings about the provenance keys.
	v, err := validator.New(merged.Schema)
	if err != nil {
		t.Fatalf("validator.New on merged schema: %v", err)
	}

	// Validate a good instance — the validator should emit zero
	// findings. If __source_origin were misprocessed, the validator
	// might emit findings about the unrecognised extension or about
	// constraint failures against the provenance value.
	good := map[string]any{"port": 8080}
	rpt, err := v.Validate(good, nil)
	if err != nil {
		t.Fatalf("Validator.Validate good: %v", err)
	}
	if len(rpt.Findings) != 0 {
		t.Errorf("expected zero findings on good instance with __source_origin in schema; got %v", rpt.Findings)
	}

	// Validate a bad instance — the validator should emit findings
	// for the actual constraint violation (port < 1024 from child's
	// override), confirming the validator's regular validation path
	// works against a merged-with-provenance schema.
	bad := map[string]any{"port": 80}
	rptBad, err := v.Validate(bad, nil)
	if err != nil {
		t.Fatalf("Validator.Validate bad: %v", err)
	}
	if len(rptBad.Findings) == 0 {
		t.Errorf("expected finding for port=80 < minimum=1024; got none")
	}
}

// TestMerge_ItemsRecurse verifies the items sub-schema merge recurses
// pairwise when both parent and child supply items.
func TestMerge_ItemsRecurse(t *testing.T) {
	t.Parallel()
	parent := mustSchema(t, `{
        "type": "array",
        "items": {"type": "string", "minLength": 1}
    }`, "parent.yaml:1")
	child := mustSchema(t, `{
        "items": {"minLength": 5}
    }`, "child.yaml:1")
	out, err := Merge([]*Schema{parent, child})
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	if out.Items == nil {
		t.Fatal("expected merged items; got nil")
	}
	if out.Items.Type != "string" {
		t.Errorf("expected items.type=string (parent preserved); got %q", out.Items.Type)
	}
	if out.Items.MinLength == nil || *out.Items.MinLength != 5 {
		t.Errorf("expected items.minLength=5 (child override); got %v", out.Items.MinLength)
	}
	origins := extractProvenance(t, out.Items)
	if origins["type"] != "parent.yaml:1" {
		t.Errorf("expected items.type origin=parent.yaml:1; got %q", origins["type"])
	}
	if origins["minLength"] != "child.yaml:1" {
		t.Errorf("expected items.minLength origin=child.yaml:1; got %q", origins["minLength"])
	}
}

// TestMerge_ItemsParentOnly verifies parent's items is preserved
// when child has no items.
func TestMerge_ItemsParentOnly(t *testing.T) {
	t.Parallel()
	parent := mustSchema(t, `{
        "type": "array",
        "items": {"type": "string", "minLength": 1}
    }`, "parent.yaml:1")
	child := mustSchema(t, `{
        "minItems": 1
    }`, "child.yaml:1")
	out, err := Merge([]*Schema{parent, child})
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	if out.Items == nil {
		t.Fatal("expected items preserved from parent; got nil")
	}
	if out.Items.Type != "string" {
		t.Errorf("expected items.type=string; got %q", out.Items.Type)
	}
	if *out.MinItems != 1 {
		t.Errorf("expected minItems=1 from child; got %v", out.MinItems)
	}
}

// TestMerge_ItemsChildOnly verifies child's items is added with its
// provenance seeded from childSource when parent has no items.
func TestMerge_ItemsChildOnly(t *testing.T) {
	t.Parallel()
	parent := mustSchema(t, `{"type": "array"}`, "parent.yaml:1")
	child := mustSchema(t, `{"items": {"type": "string"}}`, "child.yaml:1")
	out, err := Merge([]*Schema{parent, child})
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	if out.Items == nil || out.Items.Type != "string" {
		t.Fatalf("expected items.type=string from child; got %v", out.Items)
	}
	origins := extractProvenance(t, out.Items)
	if origins["type"] != "child.yaml:1" {
		t.Errorf("expected items.type origin=child.yaml:1; got %q", origins["type"])
	}
}

// TestMerge_AdditionalPropertiesRecurse exercises the
// additionalProperties merge path through mergeSubSchema.
func TestMerge_AdditionalPropertiesRecurse(t *testing.T) {
	t.Parallel()
	parent := mustSchema(t, `{
        "additionalProperties": {"type": "string", "minLength": 1}
    }`, "parent.yaml:1")
	child := mustSchema(t, `{
        "additionalProperties": {"minLength": 5}
    }`, "child.yaml:1")
	out, err := Merge([]*Schema{parent, child})
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	if out.AdditionalProperties == nil {
		t.Fatal("expected additionalProperties; got nil")
	}
	if out.AdditionalProperties.Type != "string" {
		t.Errorf("expected addProps.type=string; got %q", out.AdditionalProperties.Type)
	}
	if *out.AdditionalProperties.MinLength != 5 {
		t.Errorf("expected addProps.minLength=5; got %v", out.AdditionalProperties.MinLength)
	}
}

// TestMerge_NotOverride verifies child's not overrides parent's.
func TestMerge_NotOverride(t *testing.T) {
	t.Parallel()
	parent := mustSchema(t, `{"not": {"type": "string"}}`, "parent.yaml:1")
	child := mustSchema(t, `{"not": {"type": "integer"}}`, "child.yaml:1")
	out, err := Merge([]*Schema{parent, child})
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	if out.Not == nil || out.Not.Type != "integer" {
		t.Errorf("expected not.type=integer (child override); got %v", out.Not)
	}
}

// TestMerge_DefinitionsLayer covers the draft-07 definitions map
// merge path (parallel to $defs).
func TestMerge_DefinitionsLayer(t *testing.T) {
	t.Parallel()
	parent := mustSchema(t, `{
        "definitions": {
            "Shared": {"type": "string"}
        }
    }`, "parent.yaml:1")
	child := mustSchema(t, `{
        "definitions": {
            "Shared": {"type": "integer"},
            "ChildOnly": {"type": "boolean"}
        }
    }`, "child.yaml:1")
	out, err := Merge([]*Schema{parent, child})
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	if out.Definitions["Shared"].Type != "integer" {
		t.Errorf("expected definitions.Shared.type=integer (child wins); got %q", out.Definitions["Shared"].Type)
	}
	if out.Definitions["ChildOnly"] == nil {
		t.Errorf("expected definitions.ChildOnly added; got nil")
	}
}

// TestMerge_NarrowingMaxWidens verifies maximum widening detection.
func TestMerge_NarrowingMaxWidens(t *testing.T) {
	t.Parallel()
	parent := mustSchema(t, `{"properties": {"port": {"maximum": 9999}}}`, "parent.yaml:1")
	child := mustSchema(t, `{"properties": {"port": {"maximum": 65535}}}`, "child.yaml:1")
	out, err := Merge([]*Schema{parent, child})
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	if len(out.NarrowingViolations) != 1 || out.NarrowingViolations[0].Keyword != "maximum" {
		t.Errorf("expected 1 maximum-widening violation; got %v", out.NarrowingViolations)
	}
}

// TestMerge_NarrowingExclusiveBounds verifies exclusiveMinimum and
// exclusiveMaximum narrowing detection.
func TestMerge_NarrowingExclusiveBounds(t *testing.T) {
	t.Parallel()
	parent := mustSchema(t, `{"exclusiveMinimum": 0, "exclusiveMaximum": 100}`, "parent.yaml:1")
	child := mustSchema(t, `{"exclusiveMinimum": -5, "exclusiveMaximum": 200}`, "child.yaml:1")
	out, err := Merge([]*Schema{parent, child})
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	if len(out.NarrowingViolations) != 2 {
		t.Errorf("expected 2 violations (exclusiveMin + exclusiveMax); got %v", out.NarrowingViolations)
	}
}

// TestMerge_NarrowingLengthAndItemBounds verifies the lower- and
// upper-bound narrowing checks for minLength, maxLength, minItems,
// maxItems, minProperties, maxProperties, minContains, maxContains.
func TestMerge_NarrowingLengthAndItemBounds(t *testing.T) {
	t.Parallel()
	parent := mustSchema(t, `{
        "minLength": 5, "maxLength": 100,
        "minItems": 1, "maxItems": 10,
        "minProperties": 2, "maxProperties": 20,
        "minContains": 1, "maxContains": 5
    }`, "parent.yaml:1")
	child := mustSchema(t, `{
        "minLength": 1, "maxLength": 200,
        "minItems": 0, "maxItems": 20,
        "minProperties": 1, "maxProperties": 40,
        "minContains": 0, "maxContains": 10
    }`, "child.yaml:1")
	out, err := Merge([]*Schema{parent, child})
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	// All 8 bounds widen.
	if len(out.NarrowingViolations) != 8 {
		t.Errorf("expected 8 violations across all length/item/property/contains bounds; got %d (%v)", len(out.NarrowingViolations), out.NarrowingViolations)
	}
}

// TestMerge_NarrowingConstChanges verifies const-change detection.
// Any change to const is widening per the rule.
func TestMerge_NarrowingConstChanges(t *testing.T) {
	t.Parallel()
	parent := mustSchema(t, `{"const": "fixed"}`, "parent.yaml:1")
	child := mustSchema(t, `{"const": "changed"}`, "child.yaml:1")
	out, err := Merge([]*Schema{parent, child})
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	if len(out.NarrowingViolations) != 1 || out.NarrowingViolations[0].Keyword != "const" {
		t.Errorf("expected 1 const-change violation; got %v", out.NarrowingViolations)
	}
}

// TestMerge_NarrowingTypeMismatch verifies type-mismatch narrowing
// detection.
func TestMerge_NarrowingTypeMismatch(t *testing.T) {
	t.Parallel()
	parent := mustSchema(t, `{"type": "string"}`, "parent.yaml:1")
	child := mustSchema(t, `{"type": "integer"}`, "child.yaml:1")
	out, err := Merge([]*Schema{parent, child})
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	if len(out.NarrowingViolations) != 1 || out.NarrowingViolations[0].Keyword != "type" {
		t.Errorf("expected 1 type-mismatch violation; got %v", out.NarrowingViolations)
	}
}

// TestMerge_NarrowingTypesListWidens verifies the multi-type
// (types-list) narrowing check — child's set must be a subset of
// parent's.
func TestMerge_NarrowingTypesListWidens(t *testing.T) {
	t.Parallel()
	// Construct schemas with Types directly (the library's *jsonschema.Schema
	// supports a Types []string field independently of the JSON
	// rendering — JSON renders type as either string or array).
	parent := &Schema{
		Schema: &jsonschema.Schema{Types: []string{"string", "integer"}},
		Source: "parent.yaml:1",
	}
	child := &Schema{
		Schema: &jsonschema.Schema{Types: []string{"string", "boolean"}},
		Source: "child.yaml:1",
	}
	out, err := Merge([]*Schema{parent, child})
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	if len(out.NarrowingViolations) != 1 || out.NarrowingViolations[0].Keyword != "types" {
		t.Errorf("expected 1 types-list violation; got %v", out.NarrowingViolations)
	}
}

// TestMerge_NarrowingTypesListSubsetNoViolation verifies a child types
// list that is a strict subset of parent's produces no violation.
func TestMerge_NarrowingTypesListSubsetNoViolation(t *testing.T) {
	t.Parallel()
	parent := &Schema{
		Schema: &jsonschema.Schema{Types: []string{"string", "integer", "boolean"}},
		Source: "parent.yaml:1",
	}
	child := &Schema{
		Schema: &jsonschema.Schema{Types: []string{"string"}},
		Source: "child.yaml:1",
	}
	out, err := Merge([]*Schema{parent, child})
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	if len(out.NarrowingViolations) != 0 {
		t.Errorf("expected no violations (subset is narrowing); got %v", out.NarrowingViolations)
	}
}

// TestMergeError_Format verifies *MergeError.Error renders the
// path / detail / source slots when populated.
func TestMergeError_Format(t *testing.T) {
	t.Parallel()
	e := &MergeError{Path: "port", Source: "parent.yaml:1", Detail: "constraint X failed", Err: ErrMergeInvalid()}
	got := e.Error()
	for _, frag := range []string{"port", "constraint X failed", "parent.yaml:1"} {
		if !strings.Contains(got, frag) {
			t.Errorf("expected %q in error message; got %q", frag, got)
		}
	}
	// Empty path skips the "at " prefix.
	e2 := &MergeError{Detail: "structural failure", Err: ErrMergeInvalid()}
	if !strings.Contains(e2.Error(), "structural failure") {
		t.Errorf("expected structural failure in message; got %q", e2.Error())
	}
	if strings.Contains(e2.Error(), "at :") {
		t.Errorf("empty path should not render 'at ' prefix; got %q", e2.Error())
	}
}

// TestResolveChain_NonStringExtends verifies a non-string
// Extra["extends"] terminates the chain cleanly (single-element
// chain returned).
func TestResolveChain_NonStringExtends(t *testing.T) {
	t.Parallel()
	child := mustSchema(t, `{"type": "object"}`, "child.yaml:1")
	child.Extra = map[string]any{"extends": 42} // non-string value
	chain, err := ResolveChain(child, func(uri string) (*Schema, bool) {
		t.Errorf("lookup should not be called when extends is non-string; called with %q", uri)
		return nil, false
	})
	if err != nil {
		t.Fatalf("ResolveChain: %v", err)
	}
	if len(chain) != 1 {
		t.Errorf("expected single-element chain; got %v", chain)
	}
}

// TestResolveChain_LookupReturnsNilParent verifies a lookup returning
// (nil-schema, true) is treated as a missing-parent error rather
// than silently succeeding.
func TestResolveChain_LookupReturnsNilParent(t *testing.T) {
	t.Parallel()
	child := mustSchema(t, `{"type": "object"}`, "child.yaml:1")
	child.Extra = map[string]any{"extends": "parent-uri"}
	_, err := ResolveChain(child, func(uri string) (*Schema, bool) {
		return nil, true // nil schema but "found"
	})
	if err == nil {
		t.Fatal("expected error on nil-parent lookup; got nil")
	}
	if !errors.Is(err, ErrMergeInvalid()) {
		t.Errorf("expected ErrMergeInvalid; got %v", err)
	}
}

// TestMerge_ParentOnlyConstraintsPreserved exercises the parent-only
// branches for every keyword family — when parent has a constraint
// and child does not override, the value flows through to the merged
// output and the existing provenance entry is preserved.
func TestMerge_ParentOnlyConstraintsPreserved(t *testing.T) {
	t.Parallel()
	// Parent supplies a rich set of constraints; child contributes
	// nothing (empty schema). Merge must preserve all of parent's.
	parent := mustSchema(t, `{
        "type": "object",
        "title": "Parent",
        "description": "parent-only desc",
        "default": "p-default",
        "enum": ["a", "b"],
        "const": "fixed",
        "minLength": 1,
        "maxLength": 100,
        "minItems": 1,
        "maxItems": 10,
        "minProperties": 1,
        "maxProperties": 20,
        "minContains": 1,
        "maxContains": 5,
        "minimum": 0,
        "maximum": 100,
        "exclusiveMinimum": -1,
        "exclusiveMaximum": 101,
        "multipleOf": 2,
        "pattern": "^p$",
        "format": "uri",
        "uniqueItems": true,
        "not": {"type": "integer"}
    }`, "parent.yaml:1")
	child := mustSchema(t, `{}`, "child.yaml:1")
	out, err := Merge([]*Schema{parent, child})
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	if out.Type != "object" {
		t.Errorf("type=object preserved; got %q", out.Type)
	}
	if out.Title != "Parent" {
		t.Errorf("title preserved; got %q", out.Title)
	}
	if !out.UniqueItems {
		t.Errorf("uniqueItems preserved; got false")
	}
	if len(out.Enum) != 2 {
		t.Errorf("enum preserved (len 2); got %d", len(out.Enum))
	}
	if out.Const == nil {
		t.Errorf("const preserved; got nil")
	}
	if out.Default == nil {
		t.Errorf("default preserved; got nil")
	}
	if out.Not == nil {
		t.Errorf("not preserved; got nil")
	}
	if out.Minimum == nil || *out.Minimum != 0 {
		t.Errorf("minimum=0 preserved; got %v", out.Minimum)
	}
	if out.MultipleOf == nil || *out.MultipleOf != 2 {
		t.Errorf("multipleOf=2 preserved; got %v", out.MultipleOf)
	}

	// Provenance for parent-only entries should be parent.yaml:1
	// (seeded by Merge's initial seedProvenance walk).
	origins := extractProvenance(t, out.Schema)
	if origins["type"] != "parent.yaml:1" {
		t.Errorf("type origin=parent.yaml:1; got %q", origins["type"])
	}
	if origins["enum"] != "parent.yaml:1" {
		t.Errorf("enum origin=parent.yaml:1; got %q", origins["enum"])
	}
	if origins["uniqueItems"] != "parent.yaml:1" {
		t.Errorf("uniqueItems origin=parent.yaml:1; got %q", origins["uniqueItems"])
	}
}

// TestMerge_TypesParentOnlyPreserved covers the multi-type (Types
// slice) parent-only branch.
func TestMerge_TypesParentOnlyPreserved(t *testing.T) {
	t.Parallel()
	parent := &Schema{
		Schema: &jsonschema.Schema{Types: []string{"string", "integer"}},
		Source: "parent.yaml:1",
	}
	child := mustSchema(t, `{}`, "child.yaml:1")
	out, err := Merge([]*Schema{parent, child})
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	if len(out.Types) != 2 || out.Types[0] != "string" || out.Types[1] != "integer" {
		t.Errorf("expected Types preserved; got %v", out.Types)
	}
}

// TestMerge_IfThenElseParentPreserved covers the parent-branch path
// of if/then/else (child has none set; parent's group is preserved).
func TestMerge_IfThenElseParentPreserved(t *testing.T) {
	t.Parallel()
	parent := mustSchema(t, `{
        "if": {"properties": {"a": {"const": "yes"}}},
        "then": {"required": ["b"]},
        "else": {"required": ["c"]}
    }`, "parent.yaml:1")
	child := mustSchema(t, `{}`, "child.yaml:1")
	out, err := Merge([]*Schema{parent, child})
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	if out.If == nil || out.Then == nil || out.Else == nil {
		t.Errorf("expected parent's if/then/else group preserved; got if=%v then=%v else=%v", out.If, out.Then, out.Else)
	}
}

// TestMerge_NilCloneBoundaries exercises cloneSchema and
// cloneInnerSchema's nil branches via Schema-side helpers.
func TestMerge_NilCloneBoundaries(t *testing.T) {
	t.Parallel()
	if cloneSchema(nil) != nil {
		t.Error("cloneSchema(nil) should return nil")
	}
	if cloneInnerSchema(nil) != nil {
		t.Error("cloneInnerSchema(nil) should return nil")
	}
}

// TestItoa_NegativeBoundary covers the negative-number branch of
// the merger's local itoa helper.
func TestItoa_NegativeBoundary(t *testing.T) {
	t.Parallel()
	if got := itoa(-5); got != "-5" {
		t.Errorf("itoa(-5) = %q; want -5", got)
	}
	if got := itoa(0); got != "0" {
		t.Errorf("itoa(0) = %q; want 0", got)
	}
}

// TestJoinPath_EdgeCases covers the empty-prefix and empty-segment
// branches.
func TestJoinPath_EdgeCases(t *testing.T) {
	t.Parallel()
	if got := joinPath("", "port"); got != "port" {
		t.Errorf("joinPath empty-prefix; got %q", got)
	}
	if got := joinPath("foo", ""); got != "foo" {
		t.Errorf("joinPath empty-segment; got %q", got)
	}
}

// TestSeedProvenance_NilInputs covers the nil-input early-return
// branches of seedProvenance.
func TestSeedProvenance_NilInputs(t *testing.T) {
	t.Parallel()
	provenance := map[string]map[string]string{}
	seedProvenance(nil, "", "source.yaml:1", provenance)
	seedProvenance(&Schema{Schema: nil, Source: "source.yaml:1"}, "", "source.yaml:1", provenance)
	seedProvenance(&Schema{Schema: &jsonschema.Schema{}, Source: ""}, "", "", provenance)
	if len(provenance) != 0 {
		t.Errorf("nil/empty inputs should not seed provenance; got %v", provenance)
	}
}

// TestRecordProvenance_EmptySource covers the empty-source early-
// return branch of recordProvenance.
func TestRecordProvenance_EmptySource(t *testing.T) {
	t.Parallel()
	provenance := map[string]map[string]string{}
	recordProvenance(provenance, "port", "minimum", "")
	if _, ok := provenance["port"]; ok {
		t.Errorf("empty source should not be recorded; got %v", provenance)
	}
}

// TestSeedInnerProvenance_FullCoverage exercises all the keyword-
// presence branches of seedInnerProvenance — content keywords, ref,
// id, anchor, title, description that the other tests don't touch.
func TestSeedInnerProvenance_FullCoverage(t *testing.T) {
	t.Parallel()
	parent := mustSchema(t, `{
        "$id": "parent-id",
        "$ref": "#/$defs/X",
        "$anchor": "p-anchor",
        "$comment": "parent comment",
        "contentEncoding": "base64",
        "contentMediaType": "text/plain"
    }`, "parent.yaml:1")
	child := mustSchema(t, `{}`, "child.yaml:1")
	out, err := Merge([]*Schema{parent, child})
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	if out.ID != "parent-id" || out.Ref != "#/$defs/X" || out.Anchor != "p-anchor" {
		t.Errorf("expected ID/Ref/Anchor preserved; got id=%q ref=%q anchor=%q", out.ID, out.Ref, out.Anchor)
	}
	if out.Comment != "parent comment" {
		t.Errorf("expected comment preserved; got %q", out.Comment)
	}
	if out.ContentEncoding != "base64" || out.ContentMediaType != "text/plain" {
		t.Errorf("expected content encoding/mediaType preserved")
	}
}

// mustSchema is a test helper that JSON-unmarshals src into a
// *jsonschema.Schema and wraps it with the given Source.
func mustSchema(t *testing.T, src, source string) *Schema {
	t.Helper()
	inner := &jsonschema.Schema{}
	if err := json.Unmarshal([]byte(src), inner); err != nil {
		t.Fatalf("json.Unmarshal: %v\nsrc: %s", err, src)
	}
	return &Schema{Schema: inner, Source: source}
}

// extractProvenance returns the __source_origin map on the given
// schema's Extra, or nil if absent.
func extractProvenance(t *testing.T, s *jsonschema.Schema) map[string]string {
	t.Helper()
	if s == nil || s.Extra == nil {
		return nil
	}
	raw, ok := s.Extra[provenanceKey]
	if !ok {
		return nil
	}
	switch v := raw.(type) {
	case map[string]string:
		return v
	case map[string]any:
		out := map[string]string{}
		for k, val := range v {
			if sv, ok := val.(string); ok {
				out[k] = sv
			}
		}
		return out
	default:
		t.Errorf("unexpected __source_origin type: %T", raw)
		return nil
	}
}

// itoaTest is a test-side int-to-string for chain URI generation
// (avoids importing strconv at the test top level just for this).
func itoaTest(n int) string {
	return itoa(n)
}

// assertDeterministicMerge runs Merge(schemas) the given number of
// trials and asserts every merged result marshals to the same JSON
// bytes. Parallels assertDeterministicValidation in the validator
// package; small duplication accepted, shared-helper extraction is a
// possible cleanup if a third determinism-helper appears.
func assertDeterministicMerge(t *testing.T, schemas []*Schema, trials int) {
	t.Helper()
	var first string
	for i := 0; i < trials; i++ {
		out, err := Merge(schemas)
		if err != nil {
			t.Fatalf("Merge trial %d: %v", i, err)
		}
		current, err := json.Marshal(out.Schema)
		if err != nil {
			t.Fatalf("json.Marshal trial %d: %v", i, err)
		}
		if i == 0 {
			first = string(current)
			continue
		}
		if string(current) != first {
			t.Fatalf("trial %d merged schema drifted from first:\nfirst:    %s\ncurrent:  %s",
				i, first, current)
		}
	}
}

// TestMergeError_OpensWithItsConditionAndSaysThePrefixOnce pins both halves of
// this composer's contract.
//
// The report opens with the condition's registered sentence, read off the
// condition rather than restated here — a restated copy would let this test
// pass against wording that had drifted from the identity.
//
// And the package label these conditions register their sentences with appears
// exactly once. It appears because the sentence carries it, not because the
// composer writes one of its own; a composer that also wrote one would put the
// label twice in a single line. Counting is what detects that, since both
// spellings would otherwise look correct in isolation.
func TestMergeError_OpensWithItsConditionAndSaysThePrefixOnce(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name      string
		err       *MergeError
		wantValue string
	}{
		{
			name:      "detail only",
			err:       &MergeError{Detail: "extends chain revisits ./base.yaml", Err: ErrCyclicExtension()},
			wantValue: "(extends chain revisits ./base.yaml)",
		},
		{
			name:      "path, detail and source",
			err:       &MergeError{Path: "properties.port", Source: "./base.yaml:12", Detail: "chain depth 11 exceeds the maximum of 10", Err: ErrMaxDepth()},
			wantValue: "(at properties.port, chain depth 11 exceeds the maximum of 10, from ./base.yaml:12)",
		},
		{
			name:      "no occurrence detail at all",
			err:       &MergeError{Err: ErrMergeInvalid()},
			wantValue: "",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := tc.err.Error()
			sentence := tc.err.Err.Error()
			if !strings.HasPrefix(got, sentence) {
				t.Errorf("must open with the registered sentence %q; got %q", sentence, got)
			}
			if tc.wantValue == "" {
				if got != sentence {
					t.Errorf("a failure supplying no values must render the sentence alone; got %q", got)
				}
			} else if !strings.HasSuffix(got, tc.wantValue) {
				t.Errorf("occurrence values must follow the sentence as %q; got %q", tc.wantValue, got)
			}
			if n := strings.Count(got, "merger:"); n != 1 {
				t.Errorf("the package label must appear exactly once, from the sentence; appeared %d times in %q", n, got)
			}
		})
	}
}
