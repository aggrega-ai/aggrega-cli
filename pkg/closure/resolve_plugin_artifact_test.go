// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package closure_test

import (
	"context"
	"errors"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
)

// versionAsk is one plugin-artifact question a lookup was asked: the reference and
// the version it was asked by.
type versionAsk struct{ uri, version string }

// askRecordingLookup is the shared fake with every plugin-artifact question recorded
// along with the version it carried.
type askRecordingLookup struct {
	*fakeLookup
	asks []versionAsk
}

func (r *askRecordingLookup) ResolvePlugin(ctx context.Context, uri, version string) (closure.Resolution, error) {
	r.asks = append(r.asks, versionAsk{uri, version})
	return r.fakeLookup.ResolvePlugin(ctx, uri, version)
}

// TestResolvePluginArtifactRoutesByWhereTheEntityResolved holds the one routing
// decision every lock-time pass makes about a plugin artifact: an entity authored in a
// local catalog directory has its artifact answered from the machine and the lookup —
// which can fetch from a registry — is never asked, even when no local question is
// supplied; every other entity's artifact is asked of the lookup by the entity's
// version, and the local question is never asked about it.
func TestResolvePluginArtifactRoutesByWhereTheEntityResolved(t *testing.T) {
	const (
		entityURI   = "governance/gate:1.0.0"
		artifactRef = "governance/gate"
		version     = "1.0.0"
	)
	fromMachine := closure.Resolution{ResolvedVersion: "sha256:beside", Digest: "sha256:beside", ResolutionPath: closure.ResolutionPathCatalog}
	fromLookup := closure.Resolution{ResolvedVersion: "sha256:fetched", Digest: "sha256:fetched", ManifestDigest: "sha256:manifest", ResolutionPath: closure.ResolutionPathRegistry}
	errAbsent := errors.New("no local source holds this artifact")

	cases := []struct {
		name        string
		entityPath  string
		local       bool
		refuse      bool
		want        closure.Resolution
		wantErr     error
		lookupAsked bool
		localAsked  bool
	}{
		{name: "an entity authored in a catalog directory is answered from the machine",
			entityPath: closure.ResolutionPathCatalog, local: true, want: fromMachine, localAsked: true},
		{name: "a refusal from the machine is returned as it was raised",
			entityPath: closure.ResolutionPathCatalog, local: true, refuse: true, wantErr: errAbsent, localAsked: true},
		{name: "with no local question such an artifact is not found and the lookup is still not asked",
			entityPath: closure.ResolutionPathCatalog, want: closure.Resolution{NotFound: true}},
		{name: "an entity a registry served is asked of the lookup",
			entityPath: closure.ResolutionPathRegistry, local: true, want: fromLookup, lookupAsked: true},
		{name: "an entity the content cache served is asked of the lookup",
			entityPath: closure.ResolutionPathCache, local: true, want: fromLookup, lookupAsked: true},
		{name: "an entity the binary carries is asked of the lookup",
			entityPath: closure.ResolutionPathEmbedded, local: true, want: fromLookup, lookupAsked: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fake := pluginDescentLookup()
			fake.plugins[artifactRef] = fromLookup
			lookup := &askRecordingLookup{fakeLookup: fake}
			var localAsks []string
			var local closure.LocalPluginArtifact
			if tc.local {
				local = func(gotEntity, gotArtifact string) (closure.Resolution, error) {
					localAsks = append(localAsks, gotEntity+" "+gotArtifact)
					if tc.refuse {
						return closure.Resolution{}, errAbsent
					}
					return fromMachine, nil
				}
			}

			got, err := closure.ResolvePluginArtifact(context.Background(), lookup, local, entityURI, tc.entityPath, artifactRef, version)
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("err = %v; want %v", err, tc.wantErr)
				}
			} else if err != nil {
				t.Fatalf("unexpected error: %v", err)
			} else if got != tc.want {
				t.Errorf("resolution = %+v; want %+v", got, tc.want)
			}

			if tc.lookupAsked {
				if len(lookup.asks) != 1 || lookup.asks[0] != (versionAsk{artifactRef, version}) {
					t.Errorf("the lookup was asked %v; want exactly one question for %q at the entity's version %q", lookup.asks, artifactRef, version)
				}
			} else if len(lookup.asks) != 0 {
				t.Errorf("the lookup, which can fetch from a registry, was asked %v about an entity authored in a catalog directory", lookup.asks)
			}
			if tc.localAsked {
				if len(localAsks) != 1 || localAsks[0] != entityURI+" "+artifactRef {
					t.Errorf("the local question was asked %v; want exactly one question naming %q and %q", localAsks, entityURI, artifactRef)
				}
			} else if len(localAsks) != 0 {
				t.Errorf("the local question was asked %v about an entity no catalog directory authored", localAsks)
			}
		})
	}
}
