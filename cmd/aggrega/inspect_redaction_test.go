// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Tests for what the inspect layouts withhold. Both surfaces are asserted on
// their real emitted text, and every "the value is hidden" assertion sits
// beside a "this sibling's value is not" assertion on the same block — a
// display that blanked its whole pillar would satisfy the first alone.

package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/redact"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// TestInspectAggregateValues_WithholdsDeclaredLeaves covers the Aggregate
// layout's per-node values block, including a nested leaf reached by its
// dotted path.
func TestInspectAggregateValues_WithholdsDeclaredLeaves(t *testing.T) {
	t.Parallel()
	tuning := types.OrderedMap[any]{}
	tuning.Set("poolSize", 25)
	tuning.Set("adminToken", "adm1n-plaintext")
	values := types.OrderedMap[any]{}
	values.Set("appName", "demo")
	values.Set("dbPassword", "hunter2-plaintext")
	values.Set("tuning", &tuning)

	var buf bytes.Buffer
	opts := inspectOptions{out: &buf, colorize: newColorizer(colorNever, &buf, true)}
	renderAggregateNodeValues(opts, "    ", values, map[string]bool{
		"dbPassword":        true,
		"tuning.adminToken": true,
	})
	got := buf.String()

	for _, want := range []string{
		"dbPassword: " + redact.Placeholder(),
		"tuning.adminToken: " + redact.Placeholder(),
		"appName: demo",
		"tuning.poolSize: 25",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("inspect values block missing %q\n--- output ---\n%s", want, got)
		}
	}
	for _, leaked := range []string{"hunter2-plaintext", "adm1n-plaintext"} {
		if strings.Contains(got, leaked) {
			t.Errorf("inspect values block leaked %q\n--- output ---\n%s", leaked, got)
		}
	}
}

// TestInspectFeatureAttachment_WithholdsDeclaredLeaves covers the Release
// layout's per-leaf attachment block. The fixture pairs a locator the Feature
// leaves unmarked with a by-value secret it declares, which is the pairing the
// design turns on: hiding the whole attachment because it "handles secret
// material" would blank a public locator.
func TestInspectFeatureAttachment_WithholdsDeclaredLeaves(t *testing.T) {
	t.Parallel()
	values := types.OrderedMap[any]{}
	values.Set("secretName", "db-creds")
	values.Set("literalPassword", "by-value-plaintext")
	infra := types.OrderedMap[any]{}
	infra.Set("namespace", "prod")

	// Heterogeneous provenance forces the per-leaf layout — the only layout
	// that prints leaf values at all.
	valuesProv := types.OrderedMap[types.FieldProvenance]{}
	valuesProv.Set("secretName", explicitProvenance())
	valuesProv.Set("literalPassword", annotationProvenance("feature.test/pw", "aggregate.yaml:components.app.annotations.feature.test/pw", "aggregate"))
	infraProv := types.OrderedMap[types.FieldProvenance]{}
	infraProv.Set("namespace", explicitProvenance())

	entry := types.IRFeatureEntry{
		Type:             "feature.test/db-credential",
		Sensitive:        true,
		Values:           values,
		Infra:            infra,
		ValuesProvenance: valuesProv,
		InfraProvenance:  infraProv,
	}
	declared := resolver.SensitiveLeaves{
		Values: map[string]bool{"literalPassword": true},
		Infra:  map[string]bool{},
	}

	var buf bytes.Buffer
	renderFeatureAttachment(&buf, "credentials", entry, declared)
	got := buf.String()

	if !strings.Contains(got, "values.literalPassword: "+redact.Placeholder()) {
		t.Errorf("the Feature's declared-sensitive leaf must be withheld and keep its key\n--- output ---\n%s", got)
	}
	if strings.Contains(got, "by-value-plaintext") {
		t.Errorf("the attachment block leaked the declared-sensitive value\n--- output ---\n%s", got)
	}
	if !strings.Contains(got, "values.secretName: db-creds") {
		t.Errorf("the unmarked locator must survive verbatim — the roll-up alone must not hide it\n--- output ---\n%s", got)
	}
	if !strings.Contains(got, "infra.namespace: prod") {
		t.Errorf("an unmarked infra leaf must survive verbatim\n--- output ---\n%s", got)
	}
	// The provenance bracket is part of the line's shape and must survive the
	// substitution, so a reader still sees where the withheld leaf came from.
	if !strings.Contains(got, redact.Placeholder()+"    [annotation: feature.test/pw") {
		t.Errorf("the withheld leaf must keep its provenance bracket\n--- output ---\n%s", got)
	}
}

// TestInspectFeatureAttachment_NothingDeclaredIsUnchanged pins the no-op path:
// an attachment whose Feature declares nothing sensitive prints exactly what it
// printed before the axis existed.
func TestInspectFeatureAttachment_NothingDeclaredIsUnchanged(t *testing.T) {
	t.Parallel()
	values := types.OrderedMap[any]{}
	values.Set("secretName", "db-creds")
	values.Set("literalPassword", "by-value-plaintext")
	valuesProv := types.OrderedMap[types.FieldProvenance]{}
	valuesProv.Set("secretName", explicitProvenance())
	valuesProv.Set("literalPassword", annotationProvenance("feature.test/pw", "aggregate.yaml:x", "aggregate"))

	entry := types.IRFeatureEntry{
		Type:             "feature.test/db-credential",
		Values:           values,
		ValuesProvenance: valuesProv,
	}

	var buf bytes.Buffer
	renderFeatureAttachment(&buf, "credentials", entry, resolver.SensitiveLeaves{})
	got := buf.String()

	if strings.Contains(got, redact.Placeholder()) {
		t.Errorf("no declaration means no substitution\n--- output ---\n%s", got)
	}
	if !strings.Contains(got, "values.literalPassword: by-value-plaintext") {
		t.Errorf("an undeclared leaf must print its value\n--- output ---\n%s", got)
	}
}

// TestLeafDeclaredSensitive_PillarPrefixSelectsTheSet pins the join between the
// prefixed path the renderer prints and the pillar-rooted sets the declaration
// reader produces. A same-named leaf in the other pillar must not match: a
// prefix-blind lookup would hide a public `infra.password` because `values`
// declared one.
func TestLeafDeclaredSensitive_PillarPrefixSelectsTheSet(t *testing.T) {
	t.Parallel()
	declared := resolver.SensitiveLeaves{
		Values: map[string]bool{"password": true},
		Infra:  map[string]bool{"token": true},
	}
	for _, tc := range []struct {
		path string
		want bool
	}{
		{"values.password", true},
		{"infra.token", true},
		{"infra.password", false},
		{"values.token", false},
		{"password", false},
		{"provides.password", false},
	} {
		if got := leafDeclaredSensitive(declared, tc.path); got != tc.want {
			t.Errorf("leafDeclaredSensitive(%q) = %v, want %v", tc.path, got, tc.want)
		}
	}
}
