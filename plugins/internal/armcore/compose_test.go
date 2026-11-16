// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package armcore

import (
	"errors"
	"strings"
	"testing"

	abi "github.com/aggrega-ai/aggrega-abi/go/pkg/abi"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/rendercore"
	"github.com/aggrega-ai/aggrega-cli/internal/plugincondition"
	"github.com/aggrega-ai/aggrega-cli/plugins/internal/armcore/armexpr"
)

// appEnv is a fragment whose resource App writes value as its first
// environment entry's value.
func appEnv(value string) string {
	return appResource("    name: app\n    properties:\n      env:\n        - name: DATABASE_URL\n          value: " + yamlQuote(value) + "\n")
}

// yamlQuote writes s as a double-quoted YAML scalar.
func yamlQuote(s string) string {
	return `"` + strings.ReplaceAll(strings.ReplaceAll(s, `\`, `\\`), `"`, `\"`) + `"`
}

func TestComposeRewritesALiteralHoldingAValue(t *testing.T) {
	t.Parallel()
	l := levelOf(t,
		contribution{"payments-db", dbFragment},
		contribution{"orders-api", appEnv("postgres://[reference('Db').outputs.fqdn.value]:5432")})
	diags, err := l.composeLiterals()
	if err != nil {
		t.Fatal(err)
	}
	const pointer = "/resources/App/properties/env/0/value"
	got := at(t, l, pointer)
	const want = "[format('postgres://{0}:5432', reference('Db').outputs.fqdn.value)]"
	if got != want {
		t.Errorf("got %v\nwant %s", got, want)
	}
	if _, err := armexpr.Parse(want); err != nil {
		t.Errorf("the rewrite does not parse: %v", err)
	}
	if name := at(t, l, "/resources/App/properties/env/0/name"); name != "DATABASE_URL" {
		t.Errorf("literal text without a value changed: %v", name)
	}
	if len(diags) != 1 {
		t.Fatalf("want one diagnostic, got %v", diags)
	}
	d := diags[0]
	if d.Severity != abi.SeverityInfo || d.Code != "" {
		t.Errorf("want an info diagnostic with no code, got severity %q code %q", d.Severity, d.Code)
	}
	for _, s := range []string{"arm-provider:", `"orders-api"`, pointer, "format()"} {
		if !strings.Contains(d.Message, s) {
			t.Errorf("diagnostic lacks %s: %s", s, d.Message)
		}
	}
	if strings.Contains(d.Message, "postgres://") {
		t.Errorf("diagnostic quotes the value: %s", d.Message)
	}
}

func TestComposeRewritesAPlaceholder(t *testing.T) {
	t.Parallel()
	placeholder := rendercore.CrossBoundaryLinkSentinel("orders-api", "payments-db", "db", "fqdn")
	digits := strings.TrimPrefix(placeholder, rendercore.CrossBoundaryLinkSentinelPrefix)
	read := rendercore.CrossBoundaryLinkReadPlaceholder("orders-api", "payments-db", "db", "fqdn")
	cases := []struct {
		name, value, want string
		diags             int
	}{
		{"inside literal text", "postgres://" + placeholder + ":5432",
			"[format('postgres://{0}:5432', '" + placeholder + "')]", 1},
		{"beside an expression", placeholder + "/[reference('Db').outputs.path.value]",
			"[format('{0}/{1}', '" + placeholder + "', reference('Db').outputs.path.value)]", 1},
		{"the whole string", placeholder, placeholder, 0},
		{"prefix followed by the read marker", "postgres://" + read + ":5432", "postgres://" + read + ":5432", 0},
		{"prefix followed by too few digits", "x-" + rendercore.CrossBoundaryLinkSentinelPrefix + digits[:63] + "-y",
			"x-" + rendercore.CrossBoundaryLinkSentinelPrefix + digits[:63] + "-y", 0},
		{"prefix followed by upper-case digits", "x-" + rendercore.CrossBoundaryLinkSentinelPrefix + strings.ToUpper(digits),
			"x-" + rendercore.CrossBoundaryLinkSentinelPrefix + strings.ToUpper(digits), 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			l := levelOf(t, contribution{"payments-db", dbFragment}, contribution{"orders-api", appEnv(c.value)})
			diags, err := l.composeLiterals()
			if err != nil {
				t.Fatal(err)
			}
			if got := at(t, l, "/resources/App/properties/env/0/value"); got != c.want {
				t.Errorf("got %v\nwant %s", got, c.want)
			}
			if len(diags) != c.diags {
				t.Errorf("want %d diagnostics, got %v", c.diags, diags)
			}
		})
	}
}

func TestComposeLeavesBracketedTextAlone(t *testing.T) {
	t.Parallel()
	texts := []string{
		"element array[0] of it",
		"pattern is [a-z] only",
		"[test] value",
		"run [ -f x ] first",
		"call [notAFunction(1)] here",
		"[[reference('Db').outputs.a.value]",
		"[format('see [reference(''Db'').outputs.a.value] here')]",
	}
	for _, text := range texts {
		t.Run(text, func(t *testing.T) {
			t.Parallel()
			l := levelOf(t, contribution{"payments-db", dbFragment}, contribution{"orders-api", appEnv(text)})
			diags, err := l.composeLiterals()
			if err != nil {
				t.Fatal(err)
			}
			if got := at(t, l, "/resources/App/properties/env/0/value"); got != text {
				t.Errorf("text changed: got %v want %s", got, text)
			}
			if len(diags) != 0 {
				t.Errorf("want no diagnostic, got %v", diags)
			}
		})
	}
}

func TestComposeEscapesTheLiteralParts(t *testing.T) {
	t.Parallel()
	l := levelOf(t,
		contribution{"payments-db", dbFragment},
		contribution{"orders-api", appEnv("it's {x} [reference('Db').outputs.a.value]")})
	if _, err := l.composeLiterals(); err != nil {
		t.Fatal(err)
	}
	const want = "[format('it''s {{x}} {0}', reference('Db').outputs.a.value)]"
	if got := at(t, l, "/resources/App/properties/env/0/value"); got != want {
		t.Errorf("got %v\nwant %s", got, want)
	}
}

func TestComposeRecognisesTheTemplatesOwnFunctions(t *testing.T) {
	t.Parallel()
	l := levelOf(t, contribution{"orders-api", `
functions:
  - namespace: my
    members:
      fn:
        parameters:
          - {name: n, type: int}
        output: {type: string, value: "[string(parameters('n'))]"}
variables:
  own: "x-[my.fn(1)]"
  other: "x-[other.fn(1)]"
`})
	diags, err := l.composeLiterals()
	if err != nil {
		t.Fatal(err)
	}
	if got := at(t, l, "/variables/own"); got != "[format('x-{0}', my.fn(1))]" {
		t.Errorf("own function: got %v", got)
	}
	if got := at(t, l, "/variables/other"); got != "x-[other.fn(1)]" {
		t.Errorf("undeclared function rewritten: got %v", got)
	}
	if len(diags) != 1 || !strings.Contains(diags[0].Message, "/variables/own") {
		t.Errorf("want one diagnostic naming /variables/own, got %v", diags)
	}
}

// list is itself a function of the list family, so a call to it inside literal
// text is a value written there.
func TestComposeRecognisesTheBareListFunction(t *testing.T) {
	t.Parallel()
	l := levelOf(t,
		contribution{"payments-db", dbFragment},
		contribution{"orders-api", appEnv("x-[list('Db', '2023-01-01').properties]")})
	diags, err := l.composeLiterals()
	if err != nil {
		t.Fatal(err)
	}
	const want = "[format('x-{0}', list('Db', '2023-01-01').properties)]"
	if got := at(t, l, "/resources/App/properties/env/0/value"); got != want {
		t.Errorf("got %v\nwant %s", got, want)
	}
	if len(diags) != 1 {
		t.Errorf("want one diagnostic, got %v", diags)
	}
}

func TestComposeRefusesAPlaceholderInsideAnExpression(t *testing.T) {
	t.Parallel()
	placeholder := rendercore.CrossBoundaryLinkSentinel("orders-api", "payments-db", "db", "fqdn")
	cases := []struct{ name, value string }{
		{"a whole-string expression", "[concat('postgres://', '" + placeholder + "')]"},
		{"inside a string of an expression", "[concat('postgres://" + placeholder + ":5432')]"},
		{"an expression inside literal text", "postgres://[concat('" + placeholder + "')]:5432"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			l := levelOf(t, contribution{"payments-db", dbFragment}, contribution{"orders-api", appEnv(c.value)})
			_, err := l.composeLiterals()
			if !errors.Is(err, plugincondition.ErrPluginValueNotRepresentable()) {
				t.Fatalf("want E0291, got %v", err)
			}
			msg := err.Error()
			for _, s := range []string{"arm-provider:", `"orders-api"`, "/resources/App/properties/env/0/value", "inside an expression"} {
				if !strings.Contains(msg, s) {
					t.Errorf("sentence lacks %s: %s", s, msg)
				}
			}
			if strings.Contains(msg, placeholder) {
				t.Errorf("sentence quotes the value: %s", msg)
			}
		})
	}
}

// A level holds what the fragment reader admitted, which never includes an
// expression that does not parse; one placed there directly is refused, not
// passed over and not a panic.
func TestComposeRefusesAnExpressionThatDoesNotParse(t *testing.T) {
	t.Parallel()
	l := newLevel()
	l.put(sectionVariables, "v", &levelEntry{body: "[concat('a', ]", contributor: "orders-api", pointer: "/variables/v"})
	_, err := l.composeLiterals()
	if !errors.Is(err, plugincondition.ErrPluginValueNotRepresentable()) {
		t.Fatalf("want E0291, got %v", err)
	}
	for _, s := range []string{"arm-provider:", `"orders-api"`, "/variables/v", "[["} {
		if !strings.Contains(err.Error(), s) {
			t.Errorf("sentence lacks %s: %v", s, err)
		}
	}
	if err := l.checkReferences(); !errors.Is(err, plugincondition.ErrPluginValueNotRepresentable()) {
		t.Fatalf("checkReferences: want E0291, got %v", err)
	}
}

// Composition rewrites a copy of what it changes, so a subtree two entries share
// is rewritten once for each, never seen by the second already rewritten.
func TestComposeLeavesASharedSubtreeUntouched(t *testing.T) {
	t.Parallel()
	placeholder := rendercore.CrossBoundaryLinkSentinel("orders-api", "payments-db", "db", "fqdn")
	shared := map[string]any{"url": "postgres://" + placeholder + ":5432"}
	l := newLevel()
	l.put(sectionVariables, "a", &levelEntry{body: shared, contributor: "orders-api", pointer: "/variables/a"})
	l.put(sectionVariables, "b", &levelEntry{body: shared, contributor: "orders-api", pointer: "/variables/b"})
	diags, err := l.composeLiterals()
	if err != nil {
		t.Fatal(err)
	}
	if len(diags) != 2 {
		t.Errorf("want two diagnostics, got %v", diags)
	}
	want := "[format('postgres://{0}:5432', '" + placeholder + "')]"
	for _, p := range []string{"/variables/a/url", "/variables/b/url"} {
		if got := at(t, l, p); got != want {
			t.Errorf("%s: got %v", p, got)
		}
	}
	if shared["url"] != "postgres://"+placeholder+":5432" {
		t.Errorf("the shared subtree was changed in place: %v", shared["url"])
	}
}
