// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Tests for what a template's failure may say when the template read a value an
// author declared sensitive. Every withheld row sits beside rows that must be
// byte-identical to the same failure evaluated with nothing declared, because a
// mechanism that rewrote every failure would pass the withheld rows on its own.
package renderer

import (
	"encoding/base64"
	"encoding/json"
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

// jsonEscaped is s as a JSON encoder writes it inside a string, which is how a
// template's toJson quotes the placeholder.
func jsonEscaped(s string) string {
	b, err := json.Marshal(s)
	if err != nil {
		panic(err)
	}
	return string(b[1 : len(b)-1])
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
			// The branch depends on what the value holds, so a run over the
			// placeholder takes the other one; the failure is still reported as it
			// is, because the sentence is written out in the body — see
			// TestEvaluateAssertion_ReleasesASentenceWrittenOutBehindADeclaredValue.
			name:          "a written-out sentence behind a branch on what the value holds",
			body:          `{{- if hasPrefix "node-secret" .values.admin_password }}{{ fail "planted" }}{{ end -}}`,
			byteIdentical: true,
		},
		{
			name: "a composed sentence behind a branch on what the value holds",
			body: `{{- if hasPrefix "node-secret" .values.admin_password }}{{ fail (printf "planted %s" .values.admin_password) }}{{ end -}}`,
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
		{
			// The whole of the data handed to a function names no field, and it
			// reads every declared value: each is withheld where it stood, and the
			// undeclared one beside it is quoted as it is.
			name: "a failure handing the whole data to a function",
			body: `{{- fail (toJson .) -}}`,
			want: `"admin_password":"` + jsonEscaped(redact.Placeholder()) + `"`,
		},
		{
			// $ names the whole data under any rebinding, and the range iterates
			// nothing that reaches a declared value, so the bare $ is the only read.
			name: "a failure handing $ on inside a range body",
			body: `{{- range (list 1) }}{{ fail (printf "%v" $) }}{{ end -}}`,
			want: "admin_password:" + redact.Placeholder(),
		},
		{
			// A dot a with body rebound is the value the pipeline named, so handing
			// it on reads that value alone, and that value is not declared.
			name:          "a failure handing on a dot a with body rebound to an undeclared value",
			body:          `{{- with .values.admin_note }}{{ fail (printf "the admin note %s must not be inline" .) }}{{ end -}}`,
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

// TestEvaluateAssertion_ReleasesASentenceWrittenOutBehindADeclaredValue pins which
// failures of an assertion reading a declared value are reported with their own
// text although a run over the placeholder would not fail the same way: a fail
// call whose sentence is written out in the body, and nothing else. Every row
// fails only because of what the declared value holds, so the comparison alone
// would withhold every one of them; the released rows are what the tree decides,
// and the withheld rows beside them are the shapes the tree must not release — a
// sentence composed from the value, a sentence a pipeline hands to fail, and a
// sentence a call composes even from constants.
//
// The first two rows are the refusal shape a FeatureRecipe's assertion takes over
// an attachment's declared origin: the same assertion, once with its sentence
// written out and once with the origin interpolated into it.
func TestEvaluateAssertion_ReleasesASentenceWrittenOutBehindADeclaredValue(t *testing.T) {
	t.Parallel()
	const sentence = "this attachment sets its origin to a reference the target never substitutes; " +
		"set the real value through the Release instead"
	const origin = `{{- $origin := optional .features.admin_key.values "origin" "" -}}` +
		`{{- if regexMatch "^own-attachment-" $origin -}}`
	cases := []struct {
		name     string
		body     string
		released string // the sentence the failure must carry as written; empty: withheld
	}{
		{
			name:     "literal_sentence_behind_the_origin",
			body:     origin + `{{- fail "` + sentence + `" -}}{{- end -}}`,
			released: sentence,
		},
		{
			name: "interpolated_origin_behind_the_origin",
			body: origin + `{{- fail (printf "this attachment sets its origin to %s, which the target never substitutes" $origin) -}}{{- end -}}`,
		},
		{
			name: "sentence_piped_into_fail",
			body: origin + `{{- "planted" | fail -}}{{- end -}}`,
		},
		{
			name: "sentence_composed_by_a_call_of_constants",
			body: origin + `{{- fail (printf "planted") -}}{{- end -}}`,
		},
		{
			name:     "literal_sentence_in_a_template_the_body_invokes",
			body:     `{{- define "refuse" }}{{ fail "planted in a defined template" }}{{ end -}}` + origin + `{{- template "refuse" -}}{{- end -}}`,
			released: "planted in a defined template",
		},
		{
			name:     "literal_sentence_in_a_parenthesized_pipeline",
			body:     origin + `{{- printf "%s" (fail "planted in parentheses") -}}{{- end -}}`,
			released: "planted in parentheses",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			got := EvaluateAssertion("rule", c.body, assertionWithheldContext(), assertionWithheldKeys())
			if got == nil {
				t.Fatal("the assertion must fail")
			}
			if !errors.Is(got, ErrAssertionFailed()) {
				t.Errorf("the failure lost its condition: %v", got)
			}
			if code, _ := cerrors.ShortCodeOf(got); code != "E0033" {
				t.Errorf("the failure's short code = %q, want E0033", code)
			}
			text := got.Error() + cerrors.CauseChain(got)
			if strings.Contains(text, withheldOwnSecret) {
				t.Errorf("the failure carries the declared origin:\n%s", text)
			}
			if c.released == "" {
				if !strings.Contains(text, templateFailureWithheld) {
					t.Errorf("the failure must be withheld:\n%s", text)
				}
				return
			}
			if strings.Contains(text, templateFailureWithheld) {
				t.Errorf("a sentence written out in the body must be reported, not withheld:\n%s", text)
			}
			if want := "error calling fail: " + ErrAssertionFailed().Error() + ": " + c.released; !strings.Contains(text, want) {
				t.Errorf("the failure must read %q:\n%s", want, text)
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
		}}, nil, nil, redact.Derived{})

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
		{
			// The shared function set's fail, on a value surface: a sentence written
			// out in the template is reported as it is, though a run over the
			// placeholder would take the other branch.
			name:          "a written-out sentence behind a branch on the declared leaf",
			tmpl:          `{{ if hasPrefix "connection-" .context.uses.gpt.features.credential.values.origin }}{{ fail "the origin must be a reference" }}{{ end }}`,
			byteIdentical: true,
		},
		{
			name: "a composed sentence behind a branch on the declared leaf",
			tmpl: `{{ if hasPrefix "connection-" .context.uses.gpt.features.credential.values.origin }}{{ fail (printf "the origin %s must be a reference" .context.uses.gpt.features.credential.values.origin) }}{{ end }}`,
			want: templateFailureWithheld,
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
			want: "text `" + redact.Placeholder()[:7],
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

// TestSameDecodeFailure_ComparesEverythingOutsideTheQuotes pins which two decode
// failures count as one: the text a decoder quotes between backquotes may differ,
// and nothing else may — not the text around the quotes, not the number of them,
// and not the text after a backquote that opens no quote.
func TestSameDecodeFailure_ComparesEverythingOutsideTheQuotes(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name              string
		real, substituted string
		want              bool
	}{
		{name: "no quotes, the same text", real: "yaml: line 1: did not find expected key", substituted: "yaml: line 1: did not find expected key", want: true},
		{name: "no quotes, different text", real: "yaml: line 1: did not find expected key", substituted: "yaml: line 2: did not find expected key"},
		{name: "only the quoted text differs", real: "yaml: unmarshal errors:\n  line 1: cannot unmarshal !!str `long-de...` into int", substituted: "yaml: unmarshal errors:\n  line 1: cannot unmarshal !!str `<withh...` into int", want: true},
		{name: "the target type differs", real: "line 1: cannot unmarshal !!str `a` into int", substituted: "line 1: cannot unmarshal !!str `a` into bool"},
		{name: "the line differs", real: "line 1: cannot unmarshal !!str `a` into int", substituted: "line 2: cannot unmarshal !!str `b` into int"},
		{name: "every quote differs", real: "line 1: `a` into int; line 2: `b` into int", substituted: "line 1: `x` into int; line 2: `y` into int", want: true},
		{name: "an empty quote against a filled one", real: "line 1: cannot unmarshal !!str `` into int", substituted: "line 1: cannot unmarshal !!str `x` into int", want: true},
		{name: "a different number of quotes", real: "line 1: `a` into int", substituted: "line 1: `a` into int; line 2: `b` into int"},
		{name: "a quote on one side only", real: "line 1: cannot unmarshal into int", substituted: "line 1: cannot unmarshal `` into int"},
		{name: "after an unpartnered backquote, the same text", real: "line 1: `a` into int `rest", substituted: "line 1: `b` into int `rest", want: true},
		{name: "after an unpartnered backquote, different text", real: "line 1: `a` into int `rest", substituted: "line 1: `a` into int `other"},
		{name: "a third backquote after a closed quote, the same text between", real: "x `a`mid` y", substituted: "x `b`mid` y", want: true},
		{name: "a third backquote after a closed quote, different text between", real: "x `a`mid` y", substituted: "x `a`other` y"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if got := sameDecodeFailure(errors.New(c.real), errors.New(c.substituted)); got != c.want {
				t.Errorf("sameDecodeFailure(%q, %q) = %v, want %v", c.real, c.substituted, got, c.want)
			}
			if got := sameDecodeFailure(errors.New(c.substituted), errors.New(c.real)); got != c.want {
				t.Errorf("the verdict must not depend on the order of the two failures: (%q, %q) = %v, want %v",
					c.substituted, c.real, got, c.want)
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
