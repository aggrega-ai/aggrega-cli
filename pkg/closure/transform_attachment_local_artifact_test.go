// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package closure_test

import (
	"context"
	"errors"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	"github.com/aggrega-ai/aggrega-cli/pkg/plugintype"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// The governance plugin these tests attach at a transform role, authored in a local
// catalog directory, and the two answers its artifact could get: the bytes beside
// the entity, and a registry copy published under the same identity with different
// bytes.
const (
	localMutatorName     = "local-mutator"
	localMutatorArtifact = "governance/" + localMutatorName
	localMutatorEntity   = localMutatorArtifact + ":1.0.0"

	localMutatorDigest    = "sha256:bytes-beside-the-entity"
	registryMutatorDigest = "sha256:registry-copy"
)

// recordingLookup is the shared fake with every plugin-artifact question it is asked
// recorded, so a test can say which artifacts reached the lock-time lookup.
type recordingLookup struct {
	*fakeLookup
	pluginAsks []string
}

func (r *recordingLookup) ResolvePlugin(ctx context.Context, uri, version string) (closure.Resolution, error) {
	r.pluginAsks = append(r.pluginAsks, uri)
	return r.fakeLookup.ResolvePlugin(ctx, uri, version)
}

func (r *recordingLookup) asked(uri string) bool {
	for _, a := range r.pluginAsks {
		if a == uri {
			return true
		}
	}
	return false
}

// localAnswers is a recording LocalPluginArtifact answering every artifact from the
// machine: the attachment's from beside its entity, and a pipeline step's likewise.
type localAnswers struct {
	asks   map[string]int
	refuse error
}

func (l *localAnswers) ask(_, artifactRef string) (closure.Resolution, error) {
	if l.asks == nil {
		l.asks = map[string]int{}
	}
	l.asks[artifactRef]++
	if l.refuse != nil {
		return closure.Resolution{}, l.refuse
	}
	return closure.Resolution{ResolvedVersion: localMutatorDigest, Digest: localMutatorDigest, ResolutionPath: closure.ResolutionPathCatalog}, nil
}

// transformAttachmentFixture wires a Release whose Recipe runs a pipeline step named by
// a Plugin entity authored in a local catalog directory, and a workspace configuration
// attaching, at the label-transform role, a governance Plugin entity authored there
// too. The lookup would answer the attachment's artifact from a registry copy, with a
// manifest digest, if it were asked.
func transformAttachmentFixture(t *testing.T) (*recordingLookup, *fakeRecipeLoader, closure.Closure) {
	t.Helper()
	fake := pluginDescentLookup()
	fake.entities[localMutatorEntity] = resolutionOK("1.0.0", "sha256:mutator-entity", closure.ResolutionPathCatalog)
	registryCopy := resolutionOK(registryMutatorDigest, registryMutatorDigest, closure.ResolutionPathRegistry)
	registryCopy.ManifestDigest = "sha256:registry-manifest"
	fake.plugins[localMutatorArtifact] = registryCopy
	lookup := &recordingLookup{fakeLookup: fake}

	loader := &fakeRecipeLoader{
		recipes: map[string]*types.Recipe{
			recipeURI: recipeWithPipelinePlugin(t, "svc", registryProvider, localPluginEntity),
		},
		plugins: map[string]*types.Plugin{
			localMutatorEntity: pluginEntity(t, localMutatorName, string(plugintype.Governance)),
			localPluginEntity:  pluginEntity(t, localPluginName, string(plugintype.PipelineStep)),
		},
	}

	cfg := workspaceconfig.Config{}
	cfg.Plugins.Governance = []workspaceconfig.PluginRef{{URI: localMutatorEntity, Role: plugintype.RoleLabelTransform}}
	cl, err := closure.ComputeClosure(context.Background(), loadRelease(t, providerDescentRelease),
		loadManifest(t, providerDescentManifest), types.NormalizedAggregate{}, cfg, lookup)
	if err != nil {
		t.Fatalf("ComputeClosure: %v", err)
	}
	if ent, ok := entityByURI(cl, localMutatorEntity); !ok || ent.ResolutionPath != closure.ResolutionPathCatalog {
		t.Fatalf("the fixture must carry the attachment's entity on the entity axis, resolved from a catalog "+
			"directory, or nothing below reaches the path under test; entities=%+v", cl.Entities)
	}
	return lookup, loader, cl
}

// TestACatalogAuthoredTransformAttachmentIsPinnedFromTheLocalBytes drives the pull's
// two descents over a transform-role attachment authored in a local catalog
// directory, beside a pipeline step authored there too.
//
// The attachment's artifact is pinned from the local answer — its digest, as both
// digest and version, with no manifest digest — and the lock-time lookup, which
// would have answered with a registry copy, is never asked about it. The pipeline
// step is the contrast: it is asked about once, by the entity descent's refusal, and
// is pinned on neither axis, so the registry-only rule for everything else a catalog
// directory authors is unchanged.
func TestACatalogAuthoredTransformAttachmentIsPinnedFromTheLocalBytes(t *testing.T) {
	lookup, loader, cl := transformAttachmentFixture(t)
	local := &localAnswers{}
	if err := descendPluginsWith(&cl, loader, lookup, nil, local.ask); err != nil {
		t.Fatalf("plugin descent: %v", err)
	}

	art, ok := pluginByURI(cl, localMutatorArtifact)
	if !ok {
		t.Fatalf("the transform-role attachment's artifact is not pinned; plugins=%+v", cl.Plugins)
	}
	want := closure.ResolvedRef{
		URI:             localMutatorArtifact,
		ResolvedVersion: localMutatorDigest,
		Digest:          localMutatorDigest,
		ResolutionPath:  closure.ResolutionPathCatalog,
		ReferencedFrom:  []string{localMutatorEntity},
		KindHint:        "plugin",
	}
	if art.URI != want.URI || art.ResolvedVersion != want.ResolvedVersion || art.Digest != want.Digest ||
		art.ManifestDigest != "" || art.ResolutionPath != want.ResolutionPath ||
		len(art.ReferencedFrom) != 1 || art.ReferencedFrom[0] != localMutatorEntity {
		t.Errorf("artifact pin = %+v; want %+v — the local bytes' digest and source, and no manifest digest", art, want)
	}
	if lookup.asked(localMutatorArtifact) {
		t.Errorf("the lock-time lookup was asked for %q, which can fetch it from a registry; asks=%v",
			localMutatorArtifact, lookup.pluginAsks)
	}

	if got := local.asks[localMutatorArtifact]; got != 2 {
		t.Errorf("the attachment's artifact was asked about %d times; want 2 — the entity descent's refusal and the artifact descent's pin", got)
	}
	if got := local.asks[localPluginArtifact]; got != 1 {
		t.Errorf("the pipeline step's artifact was asked about %d times; want 1 — the entity descent's refusal only", got)
	}
	if _, ok := pluginByURI(cl, localPluginArtifact); ok {
		t.Errorf("a catalog-authored pipeline step's artifact was pinned; plugins=%+v", cl.Plugins)
	}
	if _, ok := entityByURI(cl, localPluginEntity); ok {
		t.Errorf("a catalog-authored pipeline step's entity was pinned; entities=%+v", cl.Entities)
	}
}

// TestTheArtifactDescentRefusesACatalogAuthoredArtifactNoLocalSourceHolds proves the
// artifact descent halts with the local answer's own refusal rather than skipping the
// artifact or asking the lock-time lookup: no later step can supply an artifact the
// machine does not hold. A nil answer is the other bound — it pins nothing for such
// an entity and still asks the lookup nothing.
func TestTheArtifactDescentRefusesACatalogAuthoredArtifactNoLocalSourceHolds(t *testing.T) {
	errAbsent := errors.New("no local source holds this artifact")

	t.Run("a refusing answer halts the descent", func(t *testing.T) {
		lookup, loader, cl := transformAttachmentFixture(t)
		local := &localAnswers{refuse: errAbsent}
		err := closure.DescendRecipePluginArtifacts(context.Background(), &cl, loader, lookup, local.ask)
		if !errors.Is(err, errAbsent) {
			t.Fatalf("the artifact descent must halt with the local refusal; got %v", err)
		}
		if lookup.asked(localMutatorArtifact) {
			t.Errorf("the refused artifact was asked of the lock-time lookup; asks=%v", lookup.pluginAsks)
		}
	})

	t.Run("a nil answer pins nothing and asks the lookup nothing", func(t *testing.T) {
		lookup, loader, cl := transformAttachmentFixture(t)
		if err := closure.DescendRecipePluginArtifacts(context.Background(), &cl, loader, lookup, nil); err != nil {
			t.Fatalf("artifact descent: %v", err)
		}
		if _, ok := pluginByURI(cl, localMutatorArtifact); ok {
			t.Errorf("with no local answer the artifact was pinned anyway; plugins=%+v", cl.Plugins)
		}
		if lookup.asked(localMutatorArtifact) {
			t.Errorf("with no local answer the lock-time lookup was asked instead; asks=%v", lookup.pluginAsks)
		}
	})
}

// TestTheReconstructionPinsACatalogAuthoredAttachmentAsThePullDid holds the two sides
// of the drift check to one answer: the pin the pull's descents record for a
// transform-role attachment authored in a catalog directory, and the pin a build's
// reconstruction of the same closure — both of its plugin passes — derives with the
// same local answer. A drift check compares the URI, the version and the digest, so
// those are what must agree.
func TestTheReconstructionPinsACatalogAuthoredAttachmentAsThePullDid(t *testing.T) {
	lookup, loader, acquired := transformAttachmentFixture(t)
	if err := descendPluginsWith(&acquired, loader, lookup, nil, (&localAnswers{}).ask); err != nil {
		t.Fatalf("acquisition: %v", err)
	}
	pulled, ok := pluginByURI(acquired, localMutatorArtifact)
	if !ok {
		t.Fatalf("the acquisition pinned no artifact for the attachment; plugins=%+v", acquired.Plugins)
	}

	rebuildLookup, rebuildLoader, rebuilt := transformAttachmentFixture(t)
	local := (&localAnswers{}).ask
	if err := closure.ReconstructClosureAxes(context.Background(), &rebuilt, nil, rebuildLoader, rebuildLookup, rebuildLookup, nil, nil, local); err != nil {
		t.Fatalf("reconstruction: %v", err)
	}
	if err := closure.ReconstructWorkspaceSocketEntities(context.Background(), &rebuilt, []string{localMutatorEntity}, rebuildLoader, rebuildLookup, local); err != nil {
		t.Fatalf("workspace-socket reconstruction: %v", err)
	}
	reconstructed, ok := pluginByURI(rebuilt, localMutatorArtifact)
	if !ok {
		t.Fatalf("the reconstruction derived no artifact for the attachment the pull pinned; plugins=%+v", rebuilt.Plugins)
	}
	if reconstructed.URI != pulled.URI || reconstructed.ResolvedVersion != pulled.ResolvedVersion || reconstructed.Digest != pulled.Digest {
		t.Errorf("the reconstruction derived (%s, %s, %s); the pull pinned (%s, %s, %s) — a build would report drift on a tree nobody changed",
			reconstructed.URI, reconstructed.ResolvedVersion, reconstructed.Digest, pulled.URI, pulled.ResolvedVersion, pulled.Digest)
	}
	if rebuildLookup.asked(localMutatorArtifact) {
		t.Errorf("the reconstruction asked the lock-time lookup for %q; asks=%v", localMutatorArtifact, rebuildLookup.pluginAsks)
	}
}
