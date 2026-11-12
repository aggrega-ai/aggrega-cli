// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package redact_test

import (
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/redact"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// connectionNodeKeys assembles a node's set whose only content is one
// connection's marking, through the one assembler, so every assertion below is
// attributable to the connection half alone.
func connectionNodeKeys() redact.Keys {
	return redact.NodeKeys(nil, nil, types.OrderedMap[types.IRFeatureEntry]{},
		redact.CrossNode{Connections: map[string]redact.ConnectionKeys{
			"main": {Values: map[string]bool{"token": true}, Infra: map[string]bool{"api_key": true}},
			// A connection whose marking names nothing is dropped, so it cannot make
			// the set read as withholding something.
			"quiet": {},
		}},
		nil, nil, redact.Derived{})
}

// TestKeysUses_WithholdsAConnectionsMarkedConfiguration is the display half: the
// uses map a template's context carries shows each marked leaf of a connection's
// own values and infra as the replacement text, keeps every sibling, and leaves a
// connection the set names nothing for exactly as it was — including a leaf of the
// same name on that connection.
func TestKeysUses_WithholdsAConnectionsMarkedConfiguration(t *testing.T) {
	t.Parallel()
	keys := connectionNodeKeys()

	in := types.OrderedMap[types.EnrichedEdge]{}
	in.Set("main", types.EnrichedEdge{
		Target: "db", Socket: "databases",
		Values: om("endpoint", "db.internal", "token", "edge-token-secret"),
		Infra:  om("pool_size", 10, "api_key", "edge-infra-secret"),
	})
	in.Set("quiet", types.EnrichedEdge{
		Target: "db", Socket: "caches",
		Values: om("token", "quiet-token-readable"),
	})

	out := keys.Uses(in)
	main, _ := out.Get("main")
	if got, _ := main.Values.Get("token"); got != redact.Placeholder() {
		t.Errorf("main values.token = %#v, want the replacement text", got)
	}
	if got, _ := main.Infra.Get("api_key"); got != redact.Placeholder() {
		t.Errorf("main infra.api_key = %#v, want the replacement text", got)
	}
	if got, _ := main.Values.Get("endpoint"); got != "db.internal" {
		t.Errorf("main values.endpoint = %#v, want the authored value", got)
	}
	if got, _ := main.Infra.Get("pool_size"); got != 10 {
		t.Errorf("main infra.pool_size = %#v, want the authored value", got)
	}
	quiet, _ := out.Get("quiet")
	if got, _ := quiet.Values.Get("token"); got != "quiet-token-readable" {
		t.Errorf("quiet values.token = %#v, want the authored value: its connection marks nothing", got)
	}

	// The input is the render context's own map, so it must still carry the value.
	original, _ := in.Get("main")
	if got, _ := original.Values.Get("token"); got != "edge-token-secret" {
		t.Errorf("the input uses map was mutated: values.token = %#v", got)
	}
}

// TestKeysReachedBy_NamesAConnectionsMarkedConfiguration is the reading half: a
// template reference reaching a marked leaf, or an object holding one, reaches the
// set — which is what makes a workload re-render and a refusal's text withhold the
// leaf — under the render context's spelling and, once the connection's socket is
// named, under the assertion context's.
func TestKeysReachedBy_NamesAConnectionsMarkedConfiguration(t *testing.T) {
	t.Parallel()
	keys := connectionNodeKeys()
	if !keys.AnyWithheld() {
		t.Fatal("a set naming a connection's marked leaf reports withholding nothing")
	}

	for _, row := range []struct {
		ref  string
		want bool
		why  string
	}{
		{".context.uses.main.values.token", true, "the marked values leaf itself"},
		{".context.uses.main.infra.api_key", true, "the marked infra leaf itself"},
		{".context.uses.main.values", true, "the object holding a marked leaf"},
		{".context.uses.main", true, "the connection as a whole"},
		{".context.uses.main.values.endpoint", false, "an unmarked sibling"},
		{".context.uses.quiet.values.token", false, "the same name on a connection marking nothing"},
		{".uses.databases.main.values.token", false, "the assertion spelling, before the socket is named"},
	} {
		if got := keys.ReachedBy([]string{row.ref}); got != row.want {
			t.Errorf("ReachedBy(%s) = %v, want %v — %s", row.ref, got, row.want, row.why)
		}
	}

	keys.ConnectionSockets = map[string]string{"main": "databases"}
	if !keys.ReachedBy([]string{".uses.databases.main.values.token"}) {
		t.Error("with the connection's socket named, the assertion spelling of the marked leaf must reach the set")
	}
}

// TestNodeKeys_UnmarkedConnectionsLeaveTheSetEmpty is the invisibility control: a
// node whose connections mark nothing assembles a set that withholds nothing, so
// the substituted context and the workload re-render built behind that guard are
// never built for it.
func TestNodeKeys_UnmarkedConnectionsLeaveTheSetEmpty(t *testing.T) {
	t.Parallel()
	keys := redact.NodeKeys(nil, nil, types.OrderedMap[types.IRFeatureEntry]{},
		redact.CrossNode{Connections: map[string]redact.ConnectionKeys{"quiet": {}}},
		nil, nil, redact.Derived{})
	if !keys.Empty() {
		t.Errorf("a set built from connections marking nothing is not empty: %+v", keys)
	}
}
