// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package terraformcore_test

import (
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/plugins/internal/terraformcore"
)

// omOf builds an ordered map in the given key order, so a case fixes the order
// it asserts against rather than inheriting a Go map's iteration order.
func omOf(t *testing.T, pairs ...any) types.OrderedMap[any] {
	t.Helper()
	if len(pairs)%2 != 0 {
		t.Fatalf("omOf needs key/value pairs; got %d arguments", len(pairs))
	}
	om := types.NewOrderedMap[any]()
	for i := 0; i < len(pairs); i += 2 {
		k, ok := pairs[i].(string)
		if !ok {
			t.Fatalf("omOf key at %d is not a string: %#v", i, pairs[i])
		}
		om.Set(k, pairs[i+1])
	}
	return *om
}

// TestModuleArgumentExpression pins the expression a module-block argument
// carries, per value shape, byte for byte.
//
// Each expectation is the text an engine was observed to accept and evaluate for
// that shape, so this is a regression guard on a measured contract rather than a
// restatement of the implementation. The cases that matter most are the three
// where the encoding does something other than hand the JSON through:
//
//   - an interpolation is left intact, which is the whole reason a value is
//     emitted in this position rather than beside the module's own files;
//   - the template directive introducer is escaped, because unescaped it is not
//     a mis-evaluation but a parse failure that takes the whole configuration
//     with it;
//   - a doubled percent sign that is not followed by a brace is left alone,
//     because it is two literal characters and not an escape.
func TestModuleArgumentExpression(t *testing.T) {
	for _, tc := range []struct {
		name  string
		value any
		want  string
	}{
		{"plain string", "hello world", `"hello world"`},
		{"empty string", "", `""`},
		{"integer", float64(5), `5`},
		{"float", 1.5, `1.5`},
		{"boolean", true, `true`},
		{"null", nil, `null`},
		{"list", []any{"a", "b"}, `["a","b"]`},
		{"nested object", omOf(t, "k1", "v1", "k2", "v2"), `{"k1":"v1","k2":"v2"}`},
		{
			"deeply nested object",
			omOf(t, "a", omOf(t, "b", []any{"c", float64(1), true})),
			`{"a":{"b":["c",1,true]}}`,
		},

		// The pointer case. Nothing resolves it here; it reaches the engine as
		// the text the Recipe supplied, in a position the engine evaluates.
		{"interpolation survives unescaped", "${local.computed}", `"${local.computed}"`},
		{
			"interpolation naming another component's output",
			"${module.db.endpoint}",
			`"${module.db.endpoint}"`,
		},
		{
			"interpolation embedded in surrounding text",
			"postgres://${module.db.endpoint}/appdb",
			`"postgres://${module.db.endpoint}/appdb"`,
		},

		// The directive introducer. Unescaped, these do not parse at all.
		{"directive introducer is escaped", "has a %{ directive", `"has a %%{ directive"`},
		{
			"a value already carrying the escape round-trips",
			"%%{x}",
			`"%%%{x}"`,
		},
		{
			"escaping the introducer leaves an interpolation evaluated",
			"%{x} and ${local.computed}",
			`"%%{x} and ${local.computed}"`,
		},
		{
			"a doubled percent sign is not an escape and is left alone",
			"50%% off",
			`"50%% off"`,
		},
		{
			"a lone percent sign is left alone",
			"100% complete",
			`"100% complete"`,
		},
		{
			"the introducer is escaped inside a nested value too",
			omOf(t, "a", "%{x}", "b", []any{"%{y}"}),
			`{"a":"%%{x}","b":["%%{y}"]}`,
		},

		// String escapes. Each of these is a JSON escape that is also a
		// Terraform string escape, so one encoder covers both.
		{"double quote", `has a " quote`, `"has a \" quote"`},
		{"backslash", `has a \ backslash`, `"has a \\ backslash"`},
		{"newline", "line1\nline2", `"line1\nline2"`},
		{"tab", "col1\tcol2", `"col1\tcol2"`},
		{"carriage return", "a\rb", `"a\rb"`},
		{"a quote inside the literal part beside an interpolation", `say "hi" ${local.computed}`, `"say \"hi\" ${local.computed}"`},

		// Non-ASCII passes through as itself. An angle bracket and an ampersand
		// leave as their numeric escapes, which is what the JSON encoder emits by
		// default; a \uXXXX sequence is also a Terraform string escape, so the
		// engine reads each one back as the character the author wrote.
		{"unicode", "café ✓", `"café ✓"`},
		{
			"angle brackets and ampersand leave as numeric escapes",
			"angle < & > amp",
			`"angle \u003c \u0026 \u003e amp"`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := terraformcore.ModuleArgumentExpression(tc.value)
			if err != nil {
				t.Fatalf("ModuleArgumentExpression(%#v): %v", tc.value, err)
			}
			if got != tc.want {
				t.Errorf("expression mismatch\n got: %s\nwant: %s", got, tc.want)
			}
		})
	}
}

// TestModuleArgumentExpressionRefusesAnUnencodableValue proves the error path is
// a refusal rather than a silently empty expression: a value the encoder cannot
// represent returns an error, so nothing writes an argument with no value.
func TestModuleArgumentExpressionRefusesAnUnencodableValue(t *testing.T) {
	got, err := terraformcore.ModuleArgumentExpression(make(chan int))
	if err == nil {
		t.Fatalf("expected an error for a value with no encoding; got %q", got)
	}
	if got != "" {
		t.Errorf("a refused value must yield no expression; got %q", got)
	}
}

// TestValidModuleArgumentName pins the grammar an argument name must satisfy. A
// name is not mapped into the grammar the way a module name is, because an
// argument's name is the name of the input variable it binds — so the accept and
// refuse sets are both load-bearing: accepting too little refuses a module input
// an author may legitimately declare, and accepting too much emits a
// configuration that does not parse.
func TestValidModuleArgumentName(t *testing.T) {
	for _, name := range []string{
		"db_name",
		"a",
		"_",
		"_leading_underscore",
		"allocated_storage",
		"multi-az",
		"a1",
		"A",
		"engine_version_11_4",
	} {
		t.Run("accepts/"+name, func(t *testing.T) {
			if !terraformcore.ValidModuleArgumentName(name) {
				t.Errorf("name %q was refused; a module input may be declared under it", name)
			}
		})
	}

	for _, tc := range []struct {
		name string
		why  string
	}{
		{"", "an empty name has nothing to bind"},
		{"1a", "a name may not open with a digit"},
		{"-a", "a name may not open with a dash"},
		{"a.b", "a dot is not part of the grammar"},
		{"a b", "a space is not part of the grammar"},
		{"a$b", "a dollar sign is not part of the grammar"},
		{"a\"b", "a quote is not part of the grammar"},
		{"a/b", "a slash is not part of the grammar"},
	} {
		t.Run("refuses/"+tc.name, func(t *testing.T) {
			if terraformcore.ValidModuleArgumentName(tc.name) {
				t.Errorf("name %q was accepted; %s", tc.name, tc.why)
			}
		})
	}
}
