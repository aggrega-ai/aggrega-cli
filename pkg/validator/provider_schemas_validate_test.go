// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package validator_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/compactform"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/validator"
	"go.yaml.in/yaml/v3"
)

// TestValidatePluginProviderSchemas_NilCasesPass pins the early-return
// guards: a nil PluginSpec, and a Plugin with no spec.provider block,
// both validate without error — the provider block is optional.
func TestValidatePluginProviderSchemas_NilCasesPass(t *testing.T) {
	t.Parallel()
	if errs := validator.ValidatePluginProviderSchemas("helm-renderer:1.0.0", "test.yaml", nil); errs != nil {
		t.Errorf("nil spec should return nil; got %v", errs)
	}
	spec := &types.PluginSpec{AbiVersion: "v1beta1"}
	if errs := validator.ValidatePluginProviderSchemas("helm-renderer:1.0.0", "test.yaml", spec); len(errs) != 0 {
		t.Errorf("absent spec.provider should pass; got %v", errs)
	}
}

// TestValidatePluginProviderSchemas_EmptyBodyPasses pins the
// whitespace-only branch: a spec.provider block whose workloadSchema is
// empty (a non-synthesizing provider plugin) validates silently.
func TestValidatePluginProviderSchemas_EmptyBodyPasses(t *testing.T) {
	t.Parallel()
	spec := &types.PluginSpec{
		Provider: types.PluginProvider{WorkloadSchema: "   "},
	}
	if errs := validator.ValidatePluginProviderSchemas("helm-chart-renderer:1.0.0", "test.yaml", spec); len(errs) != 0 {
		t.Errorf("empty body should pass; got %v", errs)
	}
}

// TestValidatePluginProviderSchemas_CleanSchemaPasses pins the happy
// path: a spec.provider.workloadSchema carrying a syntactically valid
// compact-form JSON Schema body compiles cleanly and returns no errors.
func TestValidatePluginProviderSchemas_CleanSchemaPasses(t *testing.T) {
	t.Parallel()
	spec := &types.PluginSpec{
		Provider: types.PluginProvider{
			WorkloadSchema: `
containers:
  type: object
  valuesType: object
replicas:
  type: integer
  min: 0
`,
		},
	}
	if errs := validator.ValidatePluginProviderSchemas("helm-renderer:1.0.0", "test.yaml", spec); len(errs) != 0 {
		t.Errorf("valid schema should pass; got %v", errs)
	}
}

// TestValidatePluginProviderSchemas_CompileErrorWrapsCatalog pins the
// reject path: a spec.provider.workloadSchema body that parses as YAML
// but fails to compile to JSON Schema 2020-12 (a constraint applied to
// the wrong declared type) surfaces as a CatalogError naming the Plugin
// and the spec.provider.workloadSchema field. This is the load-time
// acceptance gate a malformed provider-Plugin body is rejected by.
func TestValidatePluginProviderSchemas_CompileErrorWrapsCatalog(t *testing.T) {
	t.Parallel()
	spec := &types.PluginSpec{
		Provider: types.PluginProvider{
			WorkloadSchema: `port: { type: object, min: 5 }`,
		},
	}
	errs := validator.ValidatePluginProviderSchemas("helm-renderer:1.0.0", "plugin.yaml", spec)
	if len(errs) == 0 {
		t.Fatal("compile error should produce error")
	}
	msg := errs[0].Error()
	for _, want := range []string{`Plugin "helm-renderer:1.0.0"`, "provider.workloadSchema", "plugin.yaml"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error missing %q: %s", want, msg)
		}
	}
}

// recursiveAnchorWorkloadSchema is a hand-authorable
// spec.provider.workloadSchema body whose anchor is aliased inside the
// content that anchor covers. Reading it into a tree succeeds — reading
// does not expand aliases — so the cycle surfaces only when the body is
// compiled.
const recursiveAnchorWorkloadSchema = `
containers: &loop
  type: object
  valuesType: object
  properties:
    nested: *loop
`

// TestValidatePluginProviderSchemas_CompileErrorNamesItsCause pins the
// author-visible text, not the sentinel. A recursive anchor in a
// spec.provider.workloadSchema body must reach the author naming the
// anchor that closes the cycle and the change that fixes it; a message
// reporting only that the body "failed to compile" tells an author
// nothing they can act on.
//
// The assertion is deliberately on the rendered string. Sentinel identity
// is asserted separately below and is NOT the proof: the sentinel matched
// while the explanation was being discarded, which is exactly why the gap
// went unseen.
func TestValidatePluginProviderSchemas_CompileErrorNamesItsCause(t *testing.T) {
	t.Parallel()
	spec := &types.PluginSpec{
		Provider: types.PluginProvider{WorkloadSchema: recursiveAnchorWorkloadSchema},
	}
	errs := validator.ValidatePluginProviderSchemas(
		"my-provider:1.0.0", "plugins/my-provider.plugin.yaml", spec)
	if len(errs) != 1 {
		t.Fatalf("recursive anchor should produce exactly one error; got %d", len(errs))
	}
	msg := errs[0].Error()

	// The compiler's own diagnostic, produced independently over the same
	// body with the same consumer file, must appear in full inside the
	// rendered message. This is the contract: the wrapper frames the
	// diagnostic, it does not replace it.
	var body yaml.Node
	if err := yaml.Unmarshal([]byte(recursiveAnchorWorkloadSchema), &body); err != nil {
		t.Fatalf("fixture must parse into a tree; got %v", err)
	}
	_, inner := compactform.Compile(&body,
		compactform.WithConsumerFile("plugins/my-provider.plugin.yaml"))
	if inner == nil {
		t.Fatal("fixture must fail to compile, or this test proves nothing")
	}
	if !strings.Contains(msg, inner.Error()) {
		t.Errorf("rendered message drops the compiler diagnostic.\n got: %s\nwant substring: %s",
			msg, inner.Error())
	}

	// Stated independently of the wording above, so the author-visible
	// facts survive a refactor of the assertion that produced them.
	for _, want := range []string{
		"&loop",                                // the anchor the loop closes onto
		"*loop",                                // the alias that closes it
		"expands without end",                  // why the build cannot continue
		"or write the content out in full",     // the change that fixes it
		`Plugin "my-provider:1.0.0"`,           // which entity
		"spec.provider.workloadSchema",         // which field
		"plugins/my-provider.plugin.yaml",      // which file
		"vocabulary/compact-form-authoring.md", // the authoring surface pointer, retained
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("rendered message missing %q.\n got: %s", want, msg)
		}
	}

	// Sentinel identity is unchanged by the message carrying its cause.
	// This matched before the message named the anchor too, so it is a
	// regression guard rather than evidence for the assertions above.
	if !errors.Is(errs[0], compactform.ErrCircularRef()) {
		t.Errorf("sentinel identity lost; got %v", errs[0])
	}
}

// TestValidatePluginProviderSchemas_ParseErrorNamesItsCause pins the same
// contract on the branch above the compile: a body that does not parse as
// YAML reports where it stopped parsing, rather than only that it is
// invalid.
func TestValidatePluginProviderSchemas_ParseErrorNamesItsCause(t *testing.T) {
	t.Parallel()
	const malformed = "\ncontainers:\n  type: object\n valuesType: object\n"
	spec := &types.PluginSpec{
		Provider: types.PluginProvider{WorkloadSchema: malformed},
	}
	errs := validator.ValidatePluginProviderSchemas(
		"my-provider:1.0.0", "plugins/my-provider.plugin.yaml", spec)
	if len(errs) != 1 {
		t.Fatalf("malformed YAML should produce exactly one error; got %d", len(errs))
	}
	msg := errs[0].Error()

	var probe yaml.Node
	inner := yaml.Unmarshal([]byte(malformed), &probe)
	if inner == nil {
		t.Fatal("fixture must fail to parse, or this test proves nothing")
	}
	if !strings.Contains(msg, inner.Error()) {
		t.Errorf("rendered message drops the parse diagnostic.\n got: %s\nwant substring: %s",
			msg, inner.Error())
	}
	for _, want := range []string{"line ", "vocabulary/compact-form-authoring.md"} {
		if !strings.Contains(msg, want) {
			t.Errorf("rendered message missing %q.\n got: %s", want, msg)
		}
	}
}
