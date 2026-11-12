// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package redact_test

import (
	"encoding/json"
	"slices"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/redact"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// coversExactly is a declaration predicate naming exactly the coordinates given,
// or a subtree under one of them, so each row states its declarations in the
// coordinate spelling the marking uses rather than through a compiled schema.
func coversExactly(declared ...string) func(string) bool {
	return func(coordinate string) bool {
		for _, d := range declared {
			if coordinate == d || len(coordinate) > len(d) && coordinate[:len(d)] == d && coordinate[len(d)] == '.' {
				return true
			}
		}
		return false
	}
}

// TestDeclaredLeaves names the covered leaves of both pillars in payload order,
// values before infra, including a leaf under a covered object, and names nothing
// when nothing is covered.
func TestDeclaredLeaves(t *testing.T) {
	t.Parallel()
	nested := om("user", "u", "password", "p")
	values := om("region", "eu", "db", &nested, "token", "t")
	infra := om("size", "small", "passphrase", "x")

	for _, row := range []struct {
		name     string
		declared []string
		want     []string
	}{
		{"nothing declared", nil, nil},
		{"a top-level leaf in each pillar", []string{"values.token", "infra.passphrase"}, []string{"values.token", "infra.passphrase"}},
		{"a declared object covers every leaf beneath it", []string{"values.db"}, []string{"values.db.user", "values.db.password"}},
		{"a sibling with a shared prefix is not reached", []string{"values.reg"}, nil},
	} {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			got := redact.DeclaredLeaves(values, infra, coversExactly(row.declared...))
			if !slices.Equal(got, row.want) {
				t.Errorf("DeclaredLeaves = %v, want %v", got, row.want)
			}
		})
	}
}

// TestWithholdDeclaredValues replaces each covered leaf of a values pillar with
// the declared withheld marker, leaves every uncovered sibling verbatim and in
// author order, leaves the input untouched, and returns values that declare no
// sensitivity unchanged.
func TestWithholdDeclaredValues(t *testing.T) {
	t.Parallel()
	nested := om("user", "u", "password", "p")
	in := om("region", "eu", "db", &nested, "token", "t")

	got := redact.WithholdDeclaredValues(in, coversExactly("values.token", "values.db.password"))
	encoded, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	marker, err := json.Marshal(markerFor(redact.WithheldDeclared))
	if err != nil {
		t.Fatalf("marshal marker: %v", err)
	}
	want := `{"region":"eu","db":{"user":"u","password":` + string(marker) + `},"token":` + string(marker) + `}`
	if string(encoded) != want {
		t.Errorf("withheld values:\n got: %s\nwant: %s", encoded, want)
	}

	original, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal input: %v", err)
	}
	if string(original) != `{"region":"eu","db":{"user":"u","password":"p"},"token":"t"}` {
		t.Errorf("the input was mutated: %s", original)
	}

	untouched := redact.WithholdDeclaredValues(in, coversExactly("infra.token"))
	if again, _ := json.Marshal(untouched); string(again) != string(original) {
		t.Errorf("values with no covered leaf changed: %s", again)
	}
	if empty := redact.WithholdDeclaredValues(types.OrderedMap[any]{}, coversExactly("values.token")); !empty.IsZero() {
		t.Errorf("empty values gained %d keys", empty.Len())
	}
}
