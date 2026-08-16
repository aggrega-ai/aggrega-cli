// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver

import (
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// selfInjectCatalog holds the component types the self-delivery fixtures in this
// package author against: two permitting delivery through a named set of
// mechanisms, and one permitting none at all.
func selfInjectCatalog() CatalogLookup {
	return CatalogLookup{
		Component: func(typeURI string) (types.Component, bool) {
			switch typeURI {
			case "core/web-server": // permits env and file
				return types.Component{
					Metadata: types.ComponentMetadata{Name: "core/web-server"},
					Spec:     types.ComponentSpec{Inject: []string{"env", "file"}},
				}, true
			case "core/api-with-ssm": // permits env and a mechanism it names itself
				return types.Component{
					Metadata: types.ComponentMetadata{Name: "core/api-with-ssm"},
					Spec:     types.ComponentSpec{Inject: []string{"env", "ssm_param"}},
				}, true
			case "core/datastore/postgres": // declares no injection allowlist
				return types.Component{
					Metadata: types.ComponentMetadata{Name: "core/datastore/postgres"},
					Spec:     types.ComponentSpec{},
				}, true
			default:
				return types.Component{}, false
			}
		},
		Feature: func(string) (types.Feature, bool) { return types.Feature{}, false },
	}
}

// orderedAny builds the ordered mapping payload the delivery-key walk accepts
// defensively. No authored document produces it: a delivery mechanism is
// authored as an array of entries, and a mapping-shaped one is refused against
// the schema before any rule is asked. The helper exists so that arm is exercised
// rather than left unproven, and it walks in insertion order rather than
// assuming one.
func orderedAny(pairs ...string) *types.OrderedMap[any] {
	m := types.NewOrderedMap[any]()
	for i := 0; i+1 < len(pairs); i += 2 {
		m.Set(pairs[i], pairs[i+1])
	}
	return m
}

// TestCollectInjectDeliveryKeys_VerboseArrayForm pins the array-form decode at
// the delivery-key collector. The form's [{in: K, from?: P, value?: V}] elements
// get keyed by `in:` only — what is collected is delivery-key identity, not the
// value-or-reference distinction. Entries lacking `in:` or carrying a non-string
// `in:` skip.
func TestCollectInjectDeliveryKeys_VerboseArrayForm(t *testing.T) {
	t.Parallel()

	inject := &types.InjectBlock{
		Env: []any{
			map[string]any{"in": "DB_URL", "from": "connection.url"},
			map[string]any{"in": "PORT", "value": "8080"},
		},
		Arg: []any{
			map[string]any{"in": "FLAG", "value": "--enable"},
		},
	}

	keys := collectInjectDeliveryKeys(inject)
	if len(keys) != 2 {
		t.Fatalf("expected 2 mechanisms, got %d: %v", len(keys), keys)
	}
	if got := keys["env"]; len(got) != 2 || got[0] != "DB_URL" || got[1] != "PORT" {
		t.Errorf("env keys = %v, want [DB_URL PORT] (sorted)", got)
	}
	if got := keys["arg"]; len(got) != 1 || got[0] != "FLAG" {
		t.Errorf("arg keys = %v, want [FLAG]", got)
	}

	emptyInject := &types.InjectBlock{Env: []any{}}
	emptyKeys := collectInjectDeliveryKeys(emptyInject)
	if len(emptyKeys) != 0 {
		t.Errorf("empty []any{} should produce no mechanism keys; got %v", emptyKeys)
	}

	skipInject := &types.InjectBlock{
		Env: []any{
			map[string]any{"in": "GOOD", "value": "ok"},
			map[string]any{"value": "missing-in"},
			map[string]any{"in": "", "value": "empty-in"},
			"not-a-map",
		},
	}
	skipKeys := collectInjectDeliveryKeys(skipInject)
	if got := skipKeys["env"]; len(got) != 1 || got[0] != "GOOD" {
		t.Errorf("malformed entries should skip; got env=%v want [GOOD]", got)
	}
}

// TestCollectInjectDeliveryKeys_OrderedMapShapeParity holds the two
// delivery-key collectors to the same shape union. They read the same payload
// type from the same authoring surface — one from the component's own inject
// block, one from the inject block on a uses entry — so a shape either collector
// recognises must be a shape both recognise. Divergence here is not a style
// difference: whichever collector is narrower reports no keys, and a rule fed no
// keys approves a component it never examined.
//
// The two collectors share one shape-union walk, so this test cannot fail while
// that stays true. It is kept deliberately rather than removed as redundant: it
// asserts the collectors' observable behaviour, not the walk's, so it is what
// fires if either collector ever grows a payload walk of its own again.
func TestCollectInjectDeliveryKeys_OrderedMapShapeParity(t *testing.T) {
	t.Parallel()

	selfInject := &types.InjectBlock{Env: orderedAny("DB_URL", "host")}
	selfInject.Additional.Set("ssm_param", orderedAny("SECRET", "secret.arn"))

	selfKeys := collectInjectDeliveryKeys(selfInject)
	if got := selfKeys["env"]; len(got) != 1 || got[0] != "DB_URL" {
		t.Errorf("self collector env keys = %v, want [DB_URL]", got)
	}
	if got := selfKeys["ssm_param"]; len(got) != 1 || got[0] != "SECRET" {
		t.Errorf("self collector ssm_param keys = %v, want [SECRET]", got)
	}
	if len(selfKeys) == 0 {
		t.Errorf("self collector on ordered-map shape: got %v, want keys under %q and %q", selfKeys, "env", "ssm_param")
	}

	// The same payload read through the uses-side collector, which already
	// recognises the shape. Both sides must agree.
	verbose := types.OrderedMap[types.VerboseUsesEntry]{}
	verbose.Set("db", types.VerboseUsesEntry{Target: "my-db", Inject: selfInject})
	uses := types.OrderedMap[types.UsesSocketValue]{}
	uses.Set("databases", types.UsesSocketValue{Verbose: verbose})

	usesKeys := collectUsesInjectDeliveryKeys(uses)
	for mech, want := range usesKeys {
		got := selfKeys[mech]
		if len(got) != len(want) {
			t.Errorf("shape-union parity broken: uses collector sees %v for the same payload the self collector reads as %v", usesKeys, selfKeys)
			break
		}
		for i := range want {
			if got[i] != want[i] {
				t.Errorf("shape-union parity broken on mechanism %q: self=%v uses=%v", mech, got, want)
			}
		}
	}
}
