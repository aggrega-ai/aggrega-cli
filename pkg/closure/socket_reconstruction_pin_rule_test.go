// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package closure_test

import (
	"context"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	"github.com/aggrega-ai/aggrega-cli/pkg/plugintype"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// TestTheSocketReconstructionPinsWhatThePullPinned holds the workspace-socket
// reconstruction and the pull's artifact descent to one pin rule. Each row is a socket
// plugin entity a lockfile records, resolved from the tier the row names, with its
// artifact resolved from another. The pull side is the artifact descent over a closure
// holding that entity — which is how a pull's own descent visits a socket entity — and
// the build side is the socket reconstruction of the same entity URI from an empty
// closure. Whether the artifact is pinned must be the same on both sides, and equal to
// the row's want; the entity itself must be reconstructed on every row, because every
// URI the reconstruction is handed is a row the lockfile records.
//
// The core-provider-from-the-binary row is the one a reconstruction exempt from the pin
// rule gets wrong: the pull leaves that artifact out, because the build loads its
// rendering engine from the same binary, while an exempt reconstruction pins it — so a
// build holds the lockfile to a row the pull never wrote, and the pull refuses the
// lockfile it has just composed. The other rows are the controls that keep the table
// from passing by declining everything: an output-shaping governance attachment the
// binary carries is pinned on both sides, a provider served from the cache is pinned on
// both sides, and a transform attachment authored in a catalog directory — whose entity
// resolves the catalog tier — is kept on the entity axis and pinned from its local
// bytes on both sides.
func TestTheSocketReconstructionPinsWhatThePullPinned(t *testing.T) {
	t.Parallel()
	rows := []struct {
		name         string
		pluginType   string
		entityPath   string
		artifactPath string
		wantPinned   bool
	}{
		{"a core provider from the binary", string(plugintype.Provider), closure.ResolutionPathCache, closure.ResolutionPathEmbedded, false},
		{"a provider from the cache", string(plugintype.Provider), closure.ResolutionPathCache, closure.ResolutionPathCache, true},
		{"a governance attachment from the binary", string(plugintype.Governance), closure.ResolutionPathCache, closure.ResolutionPathEmbedded, true},
		{"a governance attachment from the cache", string(plugintype.Governance), closure.ResolutionPathCache, closure.ResolutionPathCache, true},
		{"a governance attachment authored in a catalog directory", string(plugintype.Governance), closure.ResolutionPathCatalog, "", true},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			const name = "socket-plugin"
			artifactURI := row.pluginType + "/" + name
			entityURI := artifactURI + ":1.0.0"
			lookup := &fakeLookup{
				entities: map[string]closure.Resolution{entityURI: resolutionOK("1.0.0", "sha256:socket-entity", row.entityPath)},
				plugins:  map[string]closure.Resolution{},
			}
			if row.artifactPath != "" {
				lookup.plugins[artifactURI] = resolutionOK("sha256:socket-artifact", "sha256:socket-artifact", row.artifactPath)
			}
			// The machine's own sources, asked only about an entity authored in a
			// catalog directory, answer with the bytes beside it.
			local := func(_, artifactRef string) (closure.Resolution, error) {
				if artifactRef != artifactURI {
					t.Errorf("the machine's own sources were asked about %q; only %q belongs there", artifactRef, artifactURI)
				}
				return resolutionOK("sha256:local-artifact", "sha256:local-artifact", closure.ResolutionPathCatalog), nil
			}
			loader := &fakeRecipeLoader{plugins: map[string]*types.Plugin{entityURI: pluginEntity(t, name, row.pluginType)}}

			pulled := closure.Closure{Entities: []closure.ResolvedRef{{
				URI:             entityURI,
				ResolvedVersion: "1.0.0",
				Digest:          "sha256:socket-entity",
				ResolutionPath:  row.entityPath,
				ReferencedFrom:  []string{"release plugin socket"},
				KindHint:        "plugin",
			}}}
			if err := closure.DescendRecipePluginArtifacts(context.Background(), &pulled, loader, lookup, local); err != nil {
				t.Fatalf("the pull's artifact descent: %v", err)
			}
			var rebuilt closure.Closure
			if err := closure.ReconstructWorkspaceSocketEntities(context.Background(), &rebuilt, []string{entityURI}, loader, lookup, local); err != nil {
				t.Fatalf("the workspace-socket reconstruction: %v", err)
			}

			pulledArt, pullPinned := pluginByURI(pulled, artifactURI)
			rebuiltArt, buildPinned := pluginByURI(rebuilt, artifactURI)
			if pullPinned != row.wantPinned {
				t.Errorf("the pull pinned the artifact = %v; want %v; plugins=%+v", pullPinned, row.wantPinned, pulled.Plugins)
			}
			if buildPinned != pullPinned {
				t.Errorf("the reconstruction pinned the artifact = %v and the pull = %v, so a build holds the lockfile to "+
					"a row the pull did not write; plugins=%+v", buildPinned, pullPinned, rebuilt.Plugins)
			}
			if pullPinned && buildPinned && (rebuiltArt.ResolvedVersion != pulledArt.ResolvedVersion || rebuiltArt.Digest != pulledArt.Digest) {
				t.Errorf("the reconstruction pinned (%s, %s); the pull pinned (%s, %s)",
					rebuiltArt.ResolvedVersion, rebuiltArt.Digest, pulledArt.ResolvedVersion, pulledArt.Digest)
			}
			if _, ok := entityByURI(rebuilt, entityURI); !ok {
				t.Errorf("the reconstruction dropped the socket entity %q the lockfile records; entities=%+v", entityURI, rebuilt.Entities)
			}
		})
	}
}
