// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"errors"
	"strings"
	"testing"

	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// quotedScalarMapping writes the delivered value between double quotes as it
// stands, so a double quote or a backslash in the value breaks the output.
const quotedScalarMapping = "env:\n  {{ .values.key }}: \"{{ .values.origin }}\"\n"

// jsonStringMapping writes the delivered value as a JSON string, which YAML reads
// back exactly whatever the value holds.
const jsonStringMapping = "env:\n  {{ .values.key }}: {{ .values.origin | toJson }}\n"

// TestExecutePhaseA_UnparsableOutputHalts pins what happens to an injectMapping
// output the build cannot read: the build halts under
// ErrInjectMappingOutputNotParsable, naming the attachment and the FeatureRecipe,
// instead of merging nothing and letting the consumer render as though the
// attachment had delivered nothing.
//
// The failing rows are the credential shape with a value that breaks its quoted
// scalar. The row whose value is declared sensitive also asserts the value never
// reaches the report: the reason the output could not be read is withheld, and
// the fixed sentence saying so is what the report carries instead. The passing
// rows are the controls — the same shape with a value that does not break it, the
// JSON-string form delivering the breaking value intact, and outputs that parse as
// nothing — so a phase that halted on every output fails here as well.
func TestExecutePhaseA_UnparsableOutputHalts(t *testing.T) {
	t.Parallel()
	const instance = "root-password"
	declared := hostWithheld{attachment: leavesFor(map[string]resolver.SensitiveLeaves{instance: secretValueLeaves()})}

	for _, tc := range []struct {
		name     string
		mapping  string
		origin   string
		withheld hostWithheld
		// halts is whether the build must stop under the condition.
		halts bool
		// redacted is whether the report must carry the withheld sentence and not
		// the value; checked only on a halting row.
		redacted bool
		// delivered is the value the consumer must read on a passing row, or ""
		// where the row delivers nothing at all.
		delivered string
	}{
		{name: "a double quote in an undeclared value", mapping: quotedScalarMapping, origin: `PH-dquote"x`, halts: true},
		{name: "a backslash in an undeclared value", mapping: quotedScalarMapping, origin: `PH-bslash\qx`, halts: true},
		{name: "a double quote in a declared value", mapping: quotedScalarMapping, origin: `PH-declared-do-not-print"x`,
			withheld: declared, halts: true, redacted: true},

		{name: "a value that does not break the quoted scalar", mapping: quotedScalarMapping, origin: "PH-plain",
			delivered: "PH-plain"},
		{name: "the breaking value written as a JSON string", mapping: jsonStringMapping, origin: `PH-dquote"x\y`,
			withheld: declared, delivered: `PH-dquote"x\y`},
		{name: "an output delivering nothing", mapping: "env: {}\n", origin: `PH-dquote"x`},
		{name: "an output carrying only a comment", mapping: "# nothing to deliver\n", origin: `PH-dquote"x`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctx := componentInjectCtxWithAttachment(instance, secretValues("MARIADB_ROOT_PASSWORD", tc.origin))
			fr := dotenvSecretFR()
			fr.Spec.InjectMapping = tc.mapping

			delivered, err := ExecutePhaseA(ctx, []FeatureRecipeRef{{InstanceName: instance, FeatureRecipe: fr}}, tc.withheld)

			if !tc.halts {
				if err != nil {
					t.Fatalf("this output parses and must not halt; got %v", cerrors.CauseChain(err))
				}
				got, ok := ctx.Context.Inject.Env.Get("MARIADB_ROOT_PASSWORD")
				if tc.delivered == "" {
					if ok || len(delivered) != 0 {
						t.Errorf("an output delivering nothing must put nothing on the pipe; got %q (present %v), %d reports",
							got, ok, len(delivered))
					}
					return
				}
				if !ok || got != tc.delivered {
					t.Errorf("MARIADB_ROOT_PASSWORD = %q (present %v); want %q delivered intact", got, ok, tc.delivered)
				}
				return
			}

			if err == nil {
				t.Fatalf("an output that does not parse must halt the build; it returned %d reports and the pipe holds %v",
					len(delivered), ctx.Context.Inject.Env.Keys())
			}
			if !errors.Is(err, ErrInjectMappingOutputNotParsable()) {
				t.Fatalf("the halt must carry the registered condition; got %v", cerrors.CauseChain(err))
			}
			if code, ok := cerrors.CodeOf(err); !ok || code != "inject-mapping-output-not-parsable" {
				t.Errorf("the code read off the halt = %q (found %v); want inject-mapping-output-not-parsable", code, ok)
			}
			if delivered != nil {
				t.Errorf("a halted phase reports no delivery; got %v", delivered)
			}
			// The frame is what a terminal prints above the condition, so it is
			// where the attachment and the FeatureRecipe have to be named.
			text := cerrors.CauseChain(err)
			if want := `feature "` + instance + `" (recipe "dotenv-secret") injectMapping output`; !strings.Contains(text, want) {
				t.Errorf("the halt must name the attachment and the FeatureRecipe (%s); got %s", want, text)
			}
			if tc.redacted {
				if strings.Contains(text, "PH-declared-do-not-print") {
					t.Errorf("the halt quotes a value declared sensitive: %s", text)
				}
				if !strings.Contains(text, decodeFailureWithheld) {
					t.Errorf("the halt must say its reason is withheld; got %s", text)
				}
			}
		})
	}
}

// TestExecutePhaseA_UnparsableOutputHaltsBeforeAnyLaterMapping pins that the halt
// stops the phase at the mapping that failed: a later attachment's mapping does not
// run, so nothing a later mapping would have delivered reaches the pipe of a build
// that has stopped.
func TestExecutePhaseA_UnparsableOutputHaltsBeforeAnyLaterMapping(t *testing.T) {
	t.Parallel()
	features := types.OrderedMap[types.TemplateAttachment]{}
	features.Set("first", types.TemplateAttachment{Type: "core/secret-value", Values: secretValues("FIRST", `PH-dquote"x`)})
	features.Set("second", types.TemplateAttachment{Type: "core/secret-value", Values: secretValues("SECOND", "PH-plain")})
	ctx := componentInjectCtxWithAttachment("first", secretValues("FIRST", `PH-dquote"x`))
	ctx.Features = features

	_, err := ExecutePhaseA(ctx, []FeatureRecipeRef{
		{InstanceName: "first", FeatureRecipe: dotenvSecretFR()},
		{InstanceName: "second", FeatureRecipe: dotenvSecretFR()},
	}, hostWithheld{})
	if !errors.Is(err, ErrInjectMappingOutputNotParsable()) {
		t.Fatalf("the first mapping's output does not parse and must halt; got %v", err)
	}
	if !strings.Contains(cerrors.CauseChain(err), `feature "first" (recipe "dotenv-secret") injectMapping output`) {
		t.Errorf("the halt must name the attachment whose output failed; got %s", cerrors.CauseChain(err))
	}
	if _, ok := ctx.Context.Inject.Env.Get("SECOND"); ok {
		t.Error("a mapping after the one that failed must not deliver into a build that halted")
	}
}
