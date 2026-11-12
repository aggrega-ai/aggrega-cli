// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package closure_test

import (
	"context"
	"slices"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	"github.com/aggrega-ai/aggrega-cli/pkg/plugintype"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// The governance plugin these tests attach at the binary's own version: a version
// only the pinned-socket resolution answers, as the compiler binary's own entities
// answer it, and which the general cascade leaves unresolved.
const (
	ownVersionName     = "own-version"
	ownVersionArtifact = "governance/" + ownVersionName
	ownVersionEntity   = ownVersionArtifact + ":9.0.0-dev"
	ownVersionVerdict  = "governance/verdict-only:1.0.0"
)

// socketLookup is the shared fake with the pinned-socket capability added: it answers
// a reference asked through ResolvePinnedSocketEntity from socket, and records which
// of the two entity resolutions every reference was asked through.
type socketLookup struct {
	*fakeLookup
	socket      map[string]closure.Resolution
	socketAsks  []string
	cascadeAsks []string
}

func (s *socketLookup) ResolveCatalogEntity(ctx context.Context, uri, kindHint string) (closure.Resolution, error) {
	s.cascadeAsks = append(s.cascadeAsks, uri)
	return s.fakeLookup.ResolveCatalogEntity(ctx, uri, kindHint)
}

func (s *socketLookup) ResolvePinnedSocketEntity(_ context.Context, uri string) (closure.Resolution, error) {
	s.socketAsks = append(s.socketAsks, uri)
	if res, ok := s.socket[uri]; ok {
		return res, nil
	}
	return closure.Resolution{NotFound: true}, nil
}

// cascadeOnlyLookup hides the capability: embedding the interface promotes the
// Lookup methods alone, so closure.ResolvePinnedSocketEntity has to take its
// fallback.
type cascadeOnlyLookup struct{ closure.Lookup }

// ownVersionFixture is a lookup whose pinned-socket resolution answers the attachment
// from the binary and whose general cascade does not hold it at all, with the
// attachment's artifact served by the binary's copy too, and a loader holding its
// entity as a governance plugin.
func ownVersionFixture(t *testing.T) (*socketLookup, *fakeRecipeLoader) {
	t.Helper()
	fake := pluginDescentLookup()
	fake.plugins[ownVersionArtifact] = resolutionOK("sha256:own-wasm", "sha256:own-wasm", closure.ResolutionPathEmbedded)
	lookup := &socketLookup{
		fakeLookup: fake,
		socket: map[string]closure.Resolution{
			ownVersionEntity: resolutionOK("9.0.0-dev", "sha256:own-entity", closure.ResolutionPathEmbedded),
		},
	}
	loader := &fakeRecipeLoader{plugins: map[string]*types.Plugin{
		ownVersionEntity: pluginEntity(t, ownVersionName, string(plugintype.Governance)),
	}}
	return lookup, loader
}

// TestATransformAttachmentResolvesThroughThePinnedSocketResolution holds both sites
// that resolve the entity of a transform-role attachment — the closure walk a pull
// and a build both run, and the build's reconstruction of the socket rows — to the
// pinned-socket resolution, and the verdict attachment beside it to neither.
//
// The fixture's general cascade does not hold the attachment, so a site that asked it
// there would leave the entity unresolved: the walk would refuse the reference and the
// reconstruction would drop the row. The cascade-only row is the fallback's own proof:
// a lookup without the capability is asked through the general cascade.
func TestATransformAttachmentResolvesThroughThePinnedSocketResolution(t *testing.T) {
	cfg := workspaceconfig.Config{}
	cfg.Plugins.Governance = []workspaceconfig.PluginRef{
		{URI: ownVersionVerdict, Role: plugintype.RoleIRVerdict},
		{URI: ownVersionEntity, Role: plugintype.RoleLabelTransform},
	}

	t.Run("the closure walk", func(t *testing.T) {
		lookup, _ := ownVersionFixture(t)
		cl, err := closure.ComputeClosure(context.Background(), loadRelease(t, providerDescentRelease),
			loadManifest(t, providerDescentManifest), types.NormalizedAggregate{}, cfg, lookup)
		if err != nil {
			t.Fatalf("ComputeClosure: %v — the transform attachment must resolve through the pinned-socket resolution", err)
		}
		ent, ok := entityByURI(cl, ownVersionEntity)
		if !ok || ent.ResolutionPath != closure.ResolutionPathEmbedded || ent.ResolvedVersion != "9.0.0-dev" {
			t.Fatalf("the attachment's entity is %+v (present=%v); want it on the entity axis as the binary's own copy", ent, ok)
		}
		if !slices.Equal(lookup.socketAsks, []string{ownVersionEntity}) {
			t.Errorf("pinned-socket asks = %v; want exactly the transform attachment", lookup.socketAsks)
		}
		if slices.Contains(lookup.cascadeAsks, ownVersionEntity) || slices.Contains(lookup.cascadeAsks, ownVersionVerdict) {
			t.Errorf("the general cascade was asked %v; the transform attachment is resolved through the socket "+
				"resolution and the verdict attachment is not on the closure at all", lookup.cascadeAsks)
		}
	})

	t.Run("the reconstruction of the socket rows", func(t *testing.T) {
		lookup, loader := ownVersionFixture(t)
		var rebuilt closure.Closure
		if err := closure.ReconstructWorkspaceSocketEntities(context.Background(), &rebuilt, []string{ownVersionEntity}, loader, lookup, nil); err != nil {
			t.Fatalf("ReconstructWorkspaceSocketEntities: %v", err)
		}
		if ent, ok := entityByURI(rebuilt, ownVersionEntity); !ok || ent.ResolutionPath != closure.ResolutionPathEmbedded {
			t.Fatalf("the reconstruction holds %+v (present=%v) for the attachment's entity; want the binary's own copy", ent, ok)
		}
		art, ok := pluginByURI(rebuilt, ownVersionArtifact)
		if !ok || art.Digest != "sha256:own-wasm" || art.ManifestDigest != "" {
			t.Errorf("the reconstruction pinned %+v (present=%v) for the attachment's artifact; want the binary's bytes, "+
				"pinned because a governance plugin shapes output", art, ok)
		}
		if !slices.Equal(lookup.socketAsks, []string{ownVersionEntity}) {
			t.Errorf("pinned-socket asks = %v; want exactly the socket row", lookup.socketAsks)
		}
	})

	t.Run("a lookup without the capability is asked through the general cascade", func(t *testing.T) {
		lookup, _ := ownVersionFixture(t)
		lookup.fakeLookup.entities[ownVersionEntity] = resolutionOK("9.0.0-dev", "sha256:cascade-entity", closure.ResolutionPathCatalog)
		res, err := closure.ResolvePinnedSocketEntity(context.Background(), cascadeOnlyLookup{lookup}, ownVersionEntity)
		if err != nil {
			t.Fatalf("ResolvePinnedSocketEntity: %v", err)
		}
		if res.Digest != "sha256:cascade-entity" {
			t.Errorf("got %+v; want the general cascade's answer", res)
		}
		if len(lookup.socketAsks) != 0 || !slices.Equal(lookup.cascadeAsks, []string{ownVersionEntity}) {
			t.Errorf("socket asks %v, cascade asks %v; want the cascade asked once and the capability never",
				lookup.socketAsks, lookup.cascadeAsks)
		}
	})
}
