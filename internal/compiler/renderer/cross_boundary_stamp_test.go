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

func TestScanOrphanCrossBoundaryLinkSentinels(t *testing.T) {
	sentinel := rendercore.CrossBoundaryLinkSentinel("api", "db", "database", "host")

	t.Run("clean groups pass", func(t *testing.T) {
		groups := map[dispatchKey]*assembledGroup{
			{Path: "a", Provider: "cloudformation"}: {files: map[string][]byte{
				"cloudformation-stack.json": []byte(`{"Resources":{}}`),
			}},
		}
		if err := scanOrphanCrossBoundaryLinkSentinels(groups); err != nil {
			t.Fatalf("clean groups: unexpected error %v", err)
		}
	})

	t.Run("surviving placeholder halts loudly", func(t *testing.T) {
		groups := map[dispatchKey]*assembledGroup{
			{Path: "a", Provider: "cloudformation"}: {files: map[string][]byte{
				"cloudformation-stack.json": []byte(`{"host":"` + sentinel + `"}`),
			}},
		}
		err := scanOrphanCrossBoundaryLinkSentinels(groups)
		if err == nil {
			t.Fatalf("surviving placeholder: expected error, got nil")
		}
		if !errors.Is(err, ErrOrphanCrossBoundaryLinkSentinel) {
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
