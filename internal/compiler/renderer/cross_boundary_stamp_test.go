// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"errors"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/rendercore"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

func TestClassifyCrossBoundaryLink(t *testing.T) {
	cfnA := dispatchKey{Path: "a", Provider: "cloudformation"}
	cfnB := dispatchKey{Path: "b", Provider: "cloudformation"}
	helmB := dispatchKey{Path: "b", Provider: "helm-chart"}

	cases := []struct {
		name             string
		src, tgt         dispatchKey
		srcLinks, tgtLnk bool
		want             crossBoundaryLinkClass
	}{
		{"fires: cross-boundary same linking provider", cfnA, cfnB, true, true, crossBoundaryLinkFires},
		{"intra: same dispatch group", cfnA, cfnA, true, true, crossBoundaryLinkInert},
		{"inert: neither provider links", cfnA, cfnB, false, false, crossBoundaryLinkInert},
		{"cross-provider: different providers, one links", cfnA, helmB, true, false, crossBoundaryLinkCrossProvider},
		{"inert: same provider, half claim", cfnA, cfnB, true, false, crossBoundaryLinkInert},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := classifyCrossBoundaryLink(tc.src, tc.tgt, tc.srcLinks, tc.tgtLnk); got != tc.want {
				t.Fatalf("classifyCrossBoundaryLink = %d, want %d", got, tc.want)
			}
		})
	}
}

// TestAssembledOutputRetainingALinkPlaceholder covers the condition as the render
// asks it: over the attribution the assembly recorded, through the rule declared
// for it, which is what the build reaches.
func TestAssembledOutputRetainingALinkPlaceholder(t *testing.T) {
	sentinel := rendercore.CrossBoundaryLinkSentinel("api", "db", "database", "host")

	t.Run("clean output passes", func(t *testing.T) {
		res := assembledOutputHolding([]string{"a"}, "cloudformation", map[string][]byte{
			"cloudformation-stack.json": []byte(`{"Resources":{}}`),
		})
		if err := orphanCrossBoundaryLinkHalt(t, res); err != nil {
			t.Fatalf("clean output: unexpected error %v", err)
		}
	})

	t.Run("surviving placeholder halts loudly", func(t *testing.T) {
		res := assembledOutputHolding([]string{"a"}, "cloudformation", map[string][]byte{
			"cloudformation-stack.json": []byte(`{"host":"` + sentinel + `"}`),
		})
		err := orphanCrossBoundaryLinkHalt(t, res)
		if err == nil {
			t.Fatalf("surviving placeholder: expected error, got nil")
		}
		if !errors.Is(err, ErrOrphanCrossBoundaryLinkSentinel()) {
			t.Fatalf("surviving placeholder: error %v does not match ErrOrphanCrossBoundaryLinkSentinel", err)
		}
	})
}

func TestStampEdgeSentinels(t *testing.T) {
	edge := types.IREdge{Source: "api", Target: "db", Socket: "database"}
	var env types.OrderedMap[string]
	env.Set("DB_HOST", "host") // delivered
	env.Set("DB_PORT", "port") // not delivered — must stay absent
	edge.Inject.Set("env", env)

	inject := types.InjectPipe{Env: types.OrderedMap[string]{}}
	inject.Env.Set("DB_HOST", "the-delivered-value")

	if changed := stampEdgeSentinels(&inject, edge); !changed {
		t.Fatalf("stampEdgeSentinels reported no change, want change")
	}

	wantHost := rendercore.CrossBoundaryLinkSentinel("api", "db", "database", "host")
	if got, _ := inject.Env.Get("DB_HOST"); got != wantHost {
		t.Fatalf("DB_HOST = %q, want stamped placeholder %q", got, wantHost)
	}
	if _, present := inject.Env.Get("DB_PORT"); present {
		t.Fatalf("DB_PORT was introduced by the stamp; only delivered keys may be overwritten")
	}
}

// TestStampEdgeSentinels_ACustomMechanismIsStampedAsEnvIs holds the stamp to every
// mechanism a uses edge delivers under: a key the edge delivers under a mechanism
// of its author's naming carries the same link placeholder an env key does, so the
// provider's link resolution replaces it as it replaces the env one. Each row
// names what the edge maps and what the pipe holds before the stamp; a key the
// pipe does not hold stays absent, and a mechanism the pipe does not carry gains
// no entry.
func TestStampEdgeSentinels_ACustomMechanismIsStampedAsEnvIs(t *testing.T) {
	host := rendercore.CrossBoundaryLinkSentinel("api", "db", "database", "host")
	mapping := func(pairs ...[3]string) types.OrderedMap[types.OrderedMap[string]] {
		var out types.OrderedMap[types.OrderedMap[string]]
		for _, p := range pairs {
			keys, _ := out.Get(p[0])
			keys.Set(p[1], p[2])
			out.Set(p[0], keys)
		}
		return out
	}
	delivered := func(mech string, keys ...string) types.OrderedMap[string] {
		var out types.OrderedMap[string]
		for _, k := range keys {
			out.Set(k, "the-delivered-value-of-"+mech)
		}
		return out
	}

	for _, tc := range []struct {
		name        string
		edge        types.OrderedMap[types.OrderedMap[string]]
		env         types.OrderedMap[string]
		custom      map[string]types.OrderedMap[string]
		wantChanged bool
		wantEnv     map[string]string
		wantCustom  map[string]map[string]string
		absent      map[string][]string
	}{
		{
			name:        "env and a custom mechanism on one edge",
			edge:        mapping([3]string{"env", "DB_HOST", "host"}, [3]string{"ssm_ref", "DB_HOST_REF", "host"}),
			env:         delivered("env", "DB_HOST"),
			custom:      map[string]types.OrderedMap[string]{"ssm_ref": delivered("ssm_ref", "DB_HOST_REF")},
			wantChanged: true,
			wantEnv:     map[string]string{"DB_HOST": host},
			wantCustom:  map[string]map[string]string{"ssm_ref": {"DB_HOST_REF": host}},
		},
		{
			name:        "a custom mechanism alone",
			edge:        mapping([3]string{"ssm_ref", "DB_HOST_REF", "host"}),
			custom:      map[string]types.OrderedMap[string]{"ssm_ref": delivered("ssm_ref", "DB_HOST_REF")},
			wantChanged: true,
			wantCustom:  map[string]map[string]string{"ssm_ref": {"DB_HOST_REF": host}},
		},
		{
			name:        "a custom key the pipe does not hold stays absent",
			edge:        mapping([3]string{"ssm_ref", "DB_HOST_REF", "host"}, [3]string{"ssm_ref", "DB_PORT_REF", "port"}),
			custom:      map[string]types.OrderedMap[string]{"ssm_ref": delivered("ssm_ref", "DB_HOST_REF")},
			wantChanged: true,
			wantCustom:  map[string]map[string]string{"ssm_ref": {"DB_HOST_REF": host}},
			absent:      map[string][]string{"ssm_ref": {"DB_PORT_REF"}},
		},
		{
			name: "a custom mechanism the pipe does not carry gains no entry",
			edge: mapping([3]string{"vault_ref", "DB_HOST_REF", "host"}),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			inject := types.InjectPipe{Env: tc.env}
			for _, mech := range []string{"ssm_ref", "vault_ref"} {
				if keys, ok := tc.custom[mech]; ok {
					inject.Additional.Set(mech, keys)
				}
			}
			edge := types.IREdge{Source: "api", Target: "db", Socket: "database", Inject: tc.edge}

			if changed := stampEdgeSentinels(&inject, edge); changed != tc.wantChanged {
				t.Fatalf("stampEdgeSentinels reported changed=%v, want %v", changed, tc.wantChanged)
			}
			for key, want := range tc.wantEnv {
				if got, _ := inject.Env.Get(key); got != want {
					t.Errorf("env %s = %q, want the placeholder %q", key, got, want)
				}
			}
			for mech, keys := range tc.wantCustom {
				held, ok := inject.Additional.Get(mech)
				if !ok {
					t.Fatalf("the pipe lost its %s mechanism", mech)
				}
				for key, want := range keys {
					if got, _ := held.Get(key); got != want {
						t.Errorf("%s %s = %q, want the placeholder %q, as an env key on the same edge carries", mech, key, got, want)
					}
				}
			}
			for mech, keys := range tc.absent {
				held, _ := inject.Additional.Get(mech)
				for _, key := range keys {
					if _, present := held.Get(key); present {
						t.Errorf("%s %s was introduced by the stamp; only delivered keys may be overwritten", mech, key)
					}
				}
			}
			if tc.custom == nil && inject.Additional.Len() != 0 {
				t.Errorf("the stamp added the mechanisms %v to a pipe that carried none", inject.Additional.Keys())
			}
		})
	}
}

// TestStampCrossBoundaryLinkSentinels_TheUnionViewCarriesACustomKeysPlaceholder
// drives the per-node stamp the render calls, over an edge the render classified
// as crossing into another output: the custom mechanism's key carries the link
// placeholder both under its own name and in the union view a template reads as
// .context.inject.all, as the env key beside it does.
func TestStampCrossBoundaryLinkSentinels_TheUnionViewCarriesACustomKeysPlaceholder(t *testing.T) {
	edge := types.IREdge{Source: "api", Target: "db", Socket: "database"}
	var env, custom types.OrderedMap[string]
	env.Set("DB_HOST", "host")
	custom.Set("DB_HOST_REF", "host")
	edge.Inject.Set("env", env)
	edge.Inject.Set("ssm_ref", custom)
	graph := &types.IRGraph{Spec: types.IRSpec{Edges: []types.IREdge{edge}}}
	links := newCrossBoundaryLinks()
	links.fires[linkTripleOf(edge)] = true

	tmplCtx := &types.TemplateContext{}
	tmplCtx.Context.Inject.Env.Set("DB_HOST", "!GetAtt Db.Endpoint.Address")
	var delivered types.OrderedMap[string]
	delivered.Set("DB_HOST_REF", "!GetAtt Db.Endpoint.Address")
	tmplCtx.Context.Inject.Additional.Set("ssm_ref", delivered)
	tmplCtx.Context.Inject.All = buildInjectAll(tmplCtx.Context.Inject)

	if err := stampCrossBoundaryLinkSentinels(tmplCtx, "api", graph, links); err != nil {
		t.Fatalf("stampCrossBoundaryLinkSentinels: %v", err)
	}
	want := rendercore.CrossBoundaryLinkSentinel("api", "db", "database", "host")
	held, _ := tmplCtx.Context.Inject.Additional.Get("ssm_ref")
	if got, _ := held.Get("DB_HOST_REF"); got != want {
		t.Errorf("ssm_ref DB_HOST_REF = %q, want the placeholder %q", got, want)
	}
	for _, key := range []string{"DB_HOST", "DB_HOST_REF"} {
		if got, _ := tmplCtx.Context.Inject.All.Get(key); got != want {
			t.Errorf("all %s = %q, want the placeholder %q", key, got, want)
		}
	}
}
