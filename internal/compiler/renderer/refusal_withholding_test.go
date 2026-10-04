// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Tests for what a template's failure may say when the template read a value an
// author declared sensitive. Every withheld row sits beside rows that must be
// byte-identical to the same failure evaluated with nothing declared, because a
// mechanism that rewrote every failure would pass the withheld rows on its own.
package renderer

import (
	"encoding/base64"
	"errors"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/redact"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/validator"
)

const (
	// The declared-sensitive values the fixtures carry — one on the node's own
	// values, one on an attachment the node holds itself, one on an attachment a
	// connection carries — and one value nobody declared.
	withheldNodeSecret       = "node-secret-plaintext"
	withheldOwnSecret        = "own-attachment-secret-plaintext"
	withheldConnectionSecret = "connection-secret-plaintext"
	withheldUndeclared       = "undeclared-note-plaintext"
)

// assertionWithheldContext is an assertion's context carrying each declared value
// beside an undeclared one.
func assertionWithheldContext() AssertionContext {
	return AssertionContext{
		Values: map[string]any{"admin_password": withheldNodeSecret, "admin_note": withheldUndeclared},
		Features: map[string]any{
			"admin_key": map[string]any{
				"type":   "core/referenced-secret",
				"values": map[string]any{"origin": withheldOwnSecret, "key": "token"},
			},
		},
	}
}

// assertionWithheldKeys names the two declared values the assertion context
// carries, as a refusal's set names them: the node's own leaf, and the leaf the
// Feature attached to the node declares.
func assertionWithheldKeys() redact.Keys {
	return redact.Keys{
		Values:   map[string]bool{"admin_password": true},
		Features: map[string]redact.AttachmentKeys{"admin_key": {Values: map[string]bool{"origin": true}}},
	}
}

// TestEvaluateAssertion_WithholdsADeclaredValueFromItsFailure is the assertion
// table. byteIdentical rows must produce exactly the failure the same assertion
// produces with nothing declared; withheld rows must name no declared value, carry
// what is stated in want, and keep the assertion's condition.
func TestEvaluateAssertion_WithholdsADeclaredValueFromItsFailure(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name          string
		body          string
		byteIdentical bool
		want          string
	}{
		{
			name:          "a failure reading no declared value",
			body:          `{{- fail "replicas must be positive" -}}`,
			byteIdentical: true,
		},
		{
			name:          "a failure reading a declared value without quoting it",
			body:          `{{- if .values.admin_password }}{{ fail "the admin password must be supplied as a reference" }}{{ end -}}`,
			byteIdentical: true,
		},
		{
			name: "a failure quoting the declared value",
			body: `{{- fail (printf "the admin password %s must be supplied as a reference" .values.admin_password) -}}`,
			want: "the admin password " + redact.Placeholder() + " must be supplied as a reference",
		},
		{
			name: "a failure that happens only because of what the value holds",
			body: `{{- if hasPrefix "node-secret" .values.admin_password }}{{ fail "planted" }}{{ end -}}`,
			want: templateFailureWithheld,
		},
		{
			name: "a failure quoting the value transformed",
			body: `{{- fail (printf "the admin password encodes to %s" (b64enc .values.admin_password)) -}}`,
			want: "the admin password encodes to " + base64.StdEncoding.EncodeToString([]byte(redact.Placeholder())),
		},
		{
			name: "a failure quoting the declared leaf of an attachment the node holds itself",
			body: `{{- fail (printf "the admin key origin %s is not a reference" .features.admin_key.values.origin) -}}`,
			want: "the admin key origin " + redact.Placeholder() + " is not a reference",
		},
		{
			name:          "a failure quoting a value nobody declared",
			body:          `{{- fail (printf "the admin note %s must not be inline" .values.admin_note) -}}`,
			byteIdentical: true,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			ctx := assertionWithheldContext()
			got := EvaluateAssertion("rule", c.body, ctx, assertionWithheldKeys())
			plain := EvaluateAssertion("rule", c.body, ctx, redact.Keys{})
			if got == nil || plain == nil {
				t.Fatalf("the assertion must fail either way; withheld=%v plain=%v", got, plain)
			}
			if !errors.Is(got, ErrAssertionFailed()) {
				t.Errorf("the failure lost its condition: %v", got)
			}
			if code, _ := cerrors.ShortCodeOf(got); code != "E0033" {
				t.Errorf("the failure's short code = %q, want E0033", code)
			}
			text := got.Error() + cerrors.CauseChain(got)
			if c.byteIdentical {
				if got.Error() != plain.Error() {
					t.Errorf("a failure quoting no declared value must be byte-identical:\nwithheld: %s\nplain:    %s", got, plain)
				}
				return
			}
			for _, secret := range []string{
				withheldNodeSecret, withheldOwnSecret,
				base64.StdEncoding.EncodeToString([]byte(withheldNodeSecret)),
			} {
				if strings.Contains(text, secret) {
					t.Errorf("the failure carries %q:\n%s", secret, text)
				}
			}
			if !strings.Contains(text, c.want) {
				t.Errorf("the failure must read %q:\n%s", c.want, text)
			}
		})
	}
}

// TestEvalTemplate_WithholdsAConnectionAttachmentsDeclaredLeaf: the render context
// surfaces read a connection's attachment at .context.uses.<alias>.features.<slot>,
// and a failure quoting its declared leaf names no value; the same failure quoting
// the attachment's undeclared leaf is unchanged.
func TestEvalTemplate_WithholdsAConnectionAttachmentsDeclaredLeaf(t *testing.T) {
	t.Parallel()
	attachments := types.OrderedMap[types.TemplateAttachment]{}
	values := types.OrderedMap[any]{}
	values.Set("origin", withheldConnectionSecret)
	values.Set("key", "api-key")
	attachments.Set("credential", types.TemplateAttachment{Type: "core/referenced-secret", Values: values})
	ctx := &types.TemplateContext{}
	ctx.Context.Uses.Set("gpt", types.EnrichedEdge{Target: "openai", Socket: "models", Features: attachments})
	keys := redact.NodeKeys(nil, nil, types.OrderedMap[types.IRFeatureEntry]{},
		redact.CrossNode{Attachments: map[string]map[string]redact.AttachmentKeys{
			"gpt": {"credential": {Values: map[string]bool{"origin": true}}},
		}}, nil, nil, nil, redact.Derived{})

	cases := []struct {
		name          string
		tmpl          string
		byteIdentical bool
		want          string
	}{
		{
			name: "the declared leaf",
			tmpl: `{{ fail (printf "origin %s is not allowed" .context.uses.gpt.features.credential.values.origin) }}`,
			want: "origin " + redact.Placeholder() + " is not allowed",
		},
		{
			name:          "the undeclared leaf beside it",
			tmpl:          `{{ fail (printf "key %s is not allowed" .context.uses.gpt.features.credential.values.key) }}`,
			byteIdentical: true,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			_, gotErr := EvalTemplate("t", c.tmpl, ctx, keys)
			_, plainErr := EvalTemplate("t", c.tmpl, ctx, redact.Keys{})
			if gotErr == nil || plainErr == nil {
				t.Fatalf("the template must fail either way; withheld=%v plain=%v", gotErr, plainErr)
			}
			// The surface wraps the failure in a frame of its own, so the whole
			// chain is what a reader is shown.
			got, plain := cerrors.CauseChain(gotErr), cerrors.CauseChain(plainErr)
			if c.byteIdentical {
				if got != plain {
					t.Errorf("must be byte-identical:\nwithheld: %s\nplain:    %s", got, plain)
				}
				return
			}
			if strings.Contains(got, withheldConnectionSecret) {
				t.Errorf("the failure carries the connection's declared value: %s", got)
			}
			if !strings.Contains(got, c.want) {
				t.Errorf("the failure must read %q: %s", c.want, got)
			}
		})
	}
}

// TestEvalTemplateDecoded_WithholdsADeclaredValueFromADecodeFailure covers a
// template's output that does not read as the document the surface wants: the
// decoder quotes what it could not read, so where the template read a declared
// value the failure is reported from a run that never saw it, or by a fixed
// sentence where the failure depended on the value itself.
func TestEvalTemplateDecoded_WithholdsADeclaredValueFromADecodeFailure(t *testing.T) {
	t.Parallel()
	type portDoc struct {
		Port int `yaml:"port"`
	}
	type textDoc struct {
		Name string `yaml:"name"`
	}
	values := types.OrderedMap[any]{}
	values.Set("password", "long-declared-secret-value")
	values.Set("colon", "declared: value")
	values.Set("note", "long-undeclared-note-value")
	ctx := &types.TemplateContext{Values: values}
	keys := redact.Keys{Values: map[string]bool{"password": true, "colon": true}}

	cases := []struct {
		name   string
		tmpl   string
		into   func() any
		secret string
		want   string // empty: byte-identical to the failure with nothing declared
	}{
		{
			name:   "a declared value where the document wants a number",
			tmpl:   "port: {{ .values.password }}",
			into:   func() any { return &portDoc{} },
			secret: "long-de",
			// The decoder quotes a short scalar whole and a long one by its first
			// characters, so the placeholder's opening is what either form shows.
			want: "cannot unmarshal !!str `" + redact.Placeholder()[:7],
		},
		{
			name:   "a declared value whose content breaks the document",
			tmpl:   "name: {{ .values.colon }}",
			into:   func() any { return &textDoc{} },
			secret: "declared: value",
			want:   decodeFailureWithheld,
		},
		{
			name: "a value nobody declared where the document wants a number",
			tmpl: "port: {{ .values.note }}",
			into: func() any { return &portDoc{} },
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			_, evalErr, gotErr := EvalTemplateDecoded("t", c.tmpl, ctx, keys, c.into())
			_, _, plainErr := EvalTemplateDecoded("t", c.tmpl, ctx, redact.Keys{}, c.into())
			if evalErr != nil {
				t.Fatalf("the evaluation itself must succeed: %v", evalErr)
			}
			if gotErr == nil || plainErr == nil {
				t.Fatalf("the output must fail to decode either way; withheld=%v plain=%v", gotErr, plainErr)
			}
			got, plain := cerrors.CauseChain(gotErr), cerrors.CauseChain(plainErr)
			if c.want == "" {
				if got != plain {
					t.Errorf("must be byte-identical:\nwithheld: %s\nplain:    %s", got, plain)
				}
				return
			}
			if strings.Contains(got, c.secret) {
				t.Errorf("the decode failure carries the declared value: %s", got)
			}
			if !strings.Contains(got, c.want) {
				t.Errorf("the decode failure must read %q: %s", c.want, got)
			}
		})
	}
}

// TestRenderWorkloadForDisplay_SubstitutesThroughTheOneWalker pins the display's
// re-render to the substitution of the template's data: the document it shows
// carries the placeholder where the declared value stood, and an evaluation
// against the same set with the substitution switched off would not.
func TestRenderWorkloadForDisplay_SubstitutesThroughTheOneWalker(t *testing.T) {
	t.Parallel()
	values := types.OrderedMap[any]{}
	values.Set("password", withheldNodeSecret)
	values.Set("name", "shop")
	ctx := &types.TemplateContext{Values: values}
	keys := redact.Keys{Values: map[string]bool{"password": true}}
	text := "apiVersion: aggrega.ai/v1alpha1\nkind: Workload\ncontainers:\n  main:\n    image: shop:1.0\n" +
		"    variables:\n      SECRET: '{{ .values.password }}'\n      NAME: '{{ .values.name }}'\n"

	shown, produced := renderWorkloadForDisplay("shop", text, ctx, keys, validator.WorkloadSlots{})
	if !produced {
		t.Fatal("the re-render must be produced")
	}
	main, ok := shown.Containers.Get("main")
	if !ok {
		t.Fatal("the re-render lost its container")
	}
	if got, _ := main.Variables.Get("SECRET"); got != redact.Placeholder() {
		t.Errorf("the declared value must read as the placeholder in the shown document; got %v", got)
	}
	if got, _ := main.Variables.Get("NAME"); got != "shop" {
		t.Errorf("an undeclared value must be shown as rendered; got %v", got)
	}
	if got, _ := ctx.Values.Get("password"); got != withheldNodeSecret {
		t.Errorf("the live context must not be mutated; it now reads %v", got)
	}
}
