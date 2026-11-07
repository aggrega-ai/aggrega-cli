// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver

import (
	"errors"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/digest"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/pluginpath"
)

// TestVerdictLookupFallsThroughWhenTheCascadeHoldsNoArtifact holds the combinator
// to its contract against the REAL cascade rather than a stand-in error: a verdict
// gate the entity-fronted cascade cannot serve — its artifact is in none of the
// tiers — resolves from the pull-populated cache instead, and a real fault in the
// base (bytes that do not match a recorded digest) halts without consulting the
// cache.
//
// The base is built the way the build builds it, so whatever condition the cascade
// raises when it runs out of tiers is the one this reads; a combinator that stopped
// recognising that condition would turn every cache-served verdict gate into a halt,
// and this is the test that would say so.
func TestVerdictLookupFallsThroughWhenTheCascadeHoldsNoArtifact(t *testing.T) {
	const artifactRef = "governance/verdict-probe"

	exhausted := pluginpath.NewResolver(pluginpath.ResolutionContext{
		WorkspaceRoot: t.TempDir(),
		CacheDir:      ".aggrega/cache",
	}, pluginpath.PinPostureNone, pluginpath.PinMaps{})

	drifted := func() pluginpath.Resolver {
		ws := t.TempDir()
		if err := oci.WriteCached(ws, ".aggrega/cache", oci.PluginArtifactKind(), artifactRef, "", []byte("drifted-bytes")); err != nil {
			t.Fatalf("seed cache: %v", err)
		}
		return pluginpath.NewResolver(pluginpath.ResolutionContext{WorkspaceRoot: ws, CacheDir: ".aggrega/cache"},
			pluginpath.PinPostureLockfile, pluginpath.PinMaps{Pins: map[string]string{
				artifactRef: digest.ContentDigest([]byte("the-recorded-bytes")),
			}})
	}()

	cases := []struct {
		name         string
		base         pluginpath.Lookup
		wantFallback bool
	}{
		{"the cascade holds the artifact in no tier", exhausted.Resolve, true},
		{"the cascade's bytes do not match the recorded digest", drifted.Resolve, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			consulted := false
			lookup := VerdictLookup(tc.base, func(string) (pluginpath.ResolvedPlugin, error) {
				consulted = true
				return pluginpath.ResolvedPlugin{URI: artifactRef, Bytes: []byte("cache-served")}, nil
			})
			got, err := lookup(artifactRef)
			if consulted != tc.wantFallback {
				t.Fatalf("cache consulted = %v; want %v (base error: %v)", consulted, tc.wantFallback, err)
			}
			if tc.wantFallback {
				if err != nil || string(got.Bytes) != "cache-served" {
					t.Fatalf("want the cache-served plugin; got %+v, %v", got, err)
				}
				return
			}
			if !errors.Is(err, oci.ErrFetchIntegrity()) {
				t.Fatalf("a drifted base must halt with its own integrity fault; got %v", err)
			}
		})
	}
}
