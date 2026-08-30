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
	renderAggregateNodeValues(opts, "    ", values, resolver.SensitiveLeaves{Values: map[string]bool{
		"dbPassword":        true,
		"tuning.adminToken": true,
	}})
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

// TestInspectAggregateValues_WithholdsInsideADeclaredObject is the widening
// proof for the Aggregate layout: a schema that declares an OBJECT sensitive
// withholds every leaf authored inside it, not merely a leaf named exactly.
//
// The fixture is the disclosure shape itself — a declared object with children
// beside an undeclared sibling — so a display that matched only the exact
// declared path would print both children in the clear while reporting nothing
// wrong. The undeclared sibling is what keeps the assertion honest: a layout
// that blanked its whole values block would satisfy the withholding half alone.
func TestInspectAggregateValues_WithholdsInsideADeclaredObject(t *testing.T) {
	t.Parallel()
	credentials := types.OrderedMap[any]{}
	credentials.Set("username", "svc-account")
	credentials.Set("password", "hunter2-plaintext")
	values := types.OrderedMap[any]{}
	values.Set("appName", "demo")
	values.Set("credentials", &credentials)

	var buf bytes.Buffer
	opts := inspectOptions{out: &buf, colorize: newColorizer(colorNever, &buf, true)}
	renderAggregateNodeValues(opts, "    ", values, resolver.SensitiveLeaves{Values: map[string]bool{
		"credentials": true,
	}})
	got := buf.String()

	for _, want := range []string{
		"credentials.username: " + redact.Placeholder(),
		"credentials.password: " + redact.Placeholder(),
		"appName: demo",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("inspect values block missing %q\n--- output ---\n%s", want, got)
		}
	}
	for _, leaked := range []string{"hunter2-plaintext", "svc-account"} {
		if strings.Contains(got, leaked) {
			t.Errorf("a leaf inside a declared object leaked %q\n--- output ---\n%s", leaked, got)
		}
	}
}

// TestInspectFeatureAttachment_WithholdsInsideADeclaredObject is the same
// widening proof for the Release layout's per-leaf attachment block, and it
// carries the same undeclared sibling for the same reason.
func TestInspectFeatureAttachment_WithholdsInsideADeclaredObject(t *testing.T) {
	t.Parallel()
	credentials := types.OrderedMap[any]{}
	credentials.Set("password", "by-value-plaintext")
	values := types.OrderedMap[any]{}
	values.Set("secretName", "db-creds")
	values.Set("credentials", &credentials)

	// Heterogeneous provenance forces the per-leaf layout — the only layout
	// that prints leaf values at all.
	valuesProv := types.OrderedMap[types.FieldProvenance]{}
	valuesProv.Set("secretName", explicitProvenance())
	valuesProv.Set("credentials.password", annotationProvenance("feature.test/pw", "aggregate.yaml:x", "aggregate"))

	entry := types.IRFeatureEntry{
		Type:             "feature.test/db-credential",
		Sensitive:        true,
		Values:           values,
		ValuesProvenance: valuesProv,
	}

	var buf bytes.Buffer
	renderFeatureAttachment(&buf, "credentials", entry, resolver.SensitiveLeaves{
		Values: map[string]bool{"credentials": true},
	})
	got := buf.String()

	if !strings.Contains(got, "values.credentials.password: "+redact.Placeholder()) {
		t.Errorf("a leaf inside a declared object must be withheld\n--- output ---\n%s", got)
	}
	if strings.Contains(got, "by-value-plaintext") {
		t.Errorf("the attachment block leaked a value inside a declared object\n--- output ---\n%s", got)
	}
	if !strings.Contains(got, "values.secretName: db-creds") {
		t.Errorf("an undeclared sibling must survive verbatim\n--- output ---\n%s", got)
	}
}

// TestInspectDisplays_PillarPrefixSelectsTheSet pins the join between the
// prefixed coordinate the renderer prints and the pillar-rooted sets the
// declaration reader produces, asked THROUGH the display rather than of a
// helper. A same-named leaf in the other pillar must not match: a prefix-blind
// lookup would hide a public `infra.password` because `values` declared one.
func TestInspectDisplays_PillarPrefixSelectsTheSet(t *testing.T) {
	t.Parallel()
	values := types.OrderedMap[any]{}
	values.Set("password", "values-secret-plaintext")
	values.Set("token", "values-token-public")
	infra := types.OrderedMap[any]{}
	infra.Set("password", "infra-password-public")
	infra.Set("token", "infra-secret-plaintext")

	valuesProv := types.OrderedMap[types.FieldProvenance]{}
	valuesProv.Set("password", explicitProvenance())
	valuesProv.Set("token", annotationProvenance("feature.test/t", "aggregate.yaml:x", "aggregate"))
	infraProv := types.OrderedMap[types.FieldProvenance]{}
	infraProv.Set("password", explicitProvenance())
	infraProv.Set("token", explicitProvenance())

	entry := types.IRFeatureEntry{
		Type:             "feature.test/db-credential",
		Values:           values,
		Infra:            infra,
		ValuesProvenance: valuesProv,
		InfraProvenance:  infraProv,
	}

	var buf bytes.Buffer
	renderFeatureAttachment(&buf, "credentials", entry, resolver.SensitiveLeaves{
		Values: map[string]bool{"password": true},
		Infra:  map[string]bool{"token": true},
	})
	got := buf.String()

	for _, want := range []string{
		"values.password: " + redact.Placeholder(),
		"infra.token: " + redact.Placeholder(),
		"values.token: values-token-public",
		"infra.password: infra-password-public",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the pillar prefix did not select the set; missing %q\n--- output ---\n%s", want, got)
		}
	}
	for _, leaked := range []string{"values-secret-plaintext", "infra-secret-plaintext"} {
		if strings.Contains(got, leaked) {
			t.Errorf("the attachment block leaked %q\n--- output ---\n%s", leaked, got)
		}
	}
}
