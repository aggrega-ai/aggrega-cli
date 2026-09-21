// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/ir"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// The mid-render facts an attachment's render reaches are retained under that
// attachment's own owner, and this file drives a real build over both owners
// and reads what the retention holds.
//
// # Why a presence check would certify nothing
//
// Each of these facts has a sibling keyed by the node alone, and every accessor
// answers a zero value for an owner the retention does not hold. So a retention
// that recorded nothing, and one that recorded everything under the wrong key,
// both answer exactly what a correct one answers for a build where the host and
// its attachments happen to agree. Three things are therefore asserted rather
// than presence: the count of owners recorded, the identity of each owner's
// entries, and — the load-bearing one — that every attachment's answer DIFFERS
// from the host node's.
//
// The fixture is built so no two of those answers can coincide. The host declares
// a source and no workload; one attachment declares a workload and no source, the
// other the reverse of that; and every owner declares an artifact under one
// shared output name, so a key space that collapsed would hand one owner's text
// back for another's.
//
// # Each owner renders through its own provider
//
// The delimiter pair an artifact entry is read with is a property of the provider
// that reads it, and an attachment binds its own FeatureRecipe declaring its own
// capability. The four sites here therefore declare four distinct pairs, none of
// them the usual one on more than one site — which is what makes a delimiter
// answered from the host's provider visible as a wrong pair rather than as the
// same pair by luck.

// The four capability bindings the fixture declares, one per render site, each
// resolving to a provider declaring its own delimiter pair.
const (
	ownerHostCapability      = "host-provider"
	ownerMonCapability       = "mon-provider"
	ownerLogsCapability      = "logs-provider"
	ownerBoundaryCapability  = "boundary-provider"
	ownerSharedArtifactName  = "owner-shared.yaml"
	ownerHostArtifactName    = "owner-host-own.yaml"
	ownerMonArtifactName     = "owner-mon-own.yaml"
	ownerLogsArtifactName    = "owner-logs-own.yaml"
	ownerBoundaryArtifact    = "owner-bridge-own.yaml"
	ownerNode                = "api"
	ownerBoundaryPath        = "cluster"
	ownerMonInstance         = "mon"
	ownerLogsInstance        = "logs"
	ownerBoundaryInstance    = "bridge"
	ownerMonFeatureType      = "core/monitoring"
	ownerLogsFeatureType     = "core/logging"
	ownerBoundaryFeatureType = "core/bridge"
)

// ownerDelimiters is the pair each capability's provider declares. No pair
// repeats, and none is the usual one, so a delimiter answered from the wrong
// provider cannot read as the right answer.
var ownerDelimiters = map[string][2]string{
	ownerHostCapability:     {"<{", "}>"},
	ownerMonCapability:      {"[[", "]]"},
	ownerLogsCapability:     {"<<", ">>"},
	ownerBoundaryCapability: {"((", "))"},
}

// ownerSynthesizingProvider is a provider that places artifacts and also
// synthesizes a workload, which is what lets a fixture site declare a workload
// while another declares a source.
type ownerSynthesizingProvider struct {
	stubArtifactsEmitter
}

func (p *ownerSynthesizingProvider) SynthesizeWorkload(
	_ context.Context, _ *types.TemplateContext, _ *types.Workload, nodeCtx NodeRenderContext,
) ([]EmittedFile, error) {
	return []EmittedFile{{
		Name:   "owner-synth.yaml",
		Origin: EmitOriginWorkload,
		Body:   []byte("kind: Synthesized\nsite: " + nodeCtx.LabelComponentID + "\n"),
	}}, nil
}

// ownerFactory resolves each declared capability to a provider carrying that
// capability's own delimiter pair. An unknown binding fails, which is what the
// unresolved-provider case reads through.
func ownerFactory() EngineFactory {
	return func(capability string) (Provider, error) {
		pair, ok := ownerDelimiters[capability]
		if !ok {
			return nil, errors.New("no provider for that capability")
		}
		p := &ownerSynthesizingProvider{}
		p.left, p.right = pair[0], pair[1]
		return p, nil
	}
}

// ownerGraphFacts stands in for the presentation the graph finalisation
// produces, which the render's later presentations are built over.
//
// It carries no value behind the interface: none of the facts read here is a
// graph-stage one, so a read panics rather than answering a zero value. It is
// supplied because the presentation chain refuses to assemble with a hole in it —
// without it the render publishes no mid-render presentation and every accessor
// assertion below would be unreachable for a reason unrelated to the retention.
type ownerGraphFacts struct{ constraint.GraphView }

// ownerWorkload is the workload body a site declaring one carries.
const ownerWorkload = `apiVersion: aggrega.ai/v1alpha1
kind: Workload
containers:
  main:
    image: owner:latest
`

// ownerArtifacts builds an artifacts block holding the shared output name and
// one name of the site's own, each body naming the site so a text read from the
// wrong owner is legible in the failure.
func ownerArtifacts(ownName, site string) types.OrderedMap[types.ArtifactEntry] {
	out := types.OrderedMap[types.ArtifactEntry]{}
	out.Set(ownerSharedArtifactName, types.ArtifactEntry{Inline: "kind: Shared\nsite: " + site + "\n"})
	out.Set(ownName, types.ArtifactEntry{Inline: "kind: Own\nsite: " + site + "\n"})
	return out
}

// ownerText is the template text the fixture authored for one site's entry, as
// the retention must hold it: before anything evaluates it.
func ownerText(kind, site string) string {
	return "kind: " + kind + "\nsite: " + site + "\n"
}

// ownerArtifactBlocks are the artifacts blocks the fixture's four render sites
// declare, one per site.
//
// They are a parameter rather than a literal inside the lookup because more than
// one proof drives this fixture and each needs its own entries: the retention
// proofs want four admitting blocks, and a proof of what the declared rule refuses
// wants one site's entry to name a pillar that site does not carry. One lookup
// taking the blocks keeps the four sites' capabilities, pillars and identities in
// one place, which is what the proofs depend on being identical between them.
type ownerArtifactBlocks struct {
	host   types.OrderedMap[types.ArtifactEntry]
	mon    types.OrderedMap[types.ArtifactEntry]
	logs   types.OrderedMap[types.ArtifactEntry]
	bridge types.OrderedMap[types.ArtifactEntry]
}

// ownerAdmittingArtifacts are the blocks every site admits: the shared output
// name plus one name of the site's own, neither reading any pillar.
func ownerAdmittingArtifacts() ownerArtifactBlocks {
	return ownerArtifactBlocks{
		host:   ownerArtifacts(ownerHostArtifactName, "host"),
		mon:    ownerArtifacts(ownerMonArtifactName, "mon"),
		logs:   ownerArtifacts(ownerLogsArtifactName, "logs"),
		bridge: ownerArtifacts(ownerBoundaryArtifact, "bridge"),
	}
}

// ownerLookup binds the host component and the three attachments over the
// admitting artifacts blocks.
func ownerLookup() RecipeLookup {
	return ownerLookupWithArtifacts(ownerAdmittingArtifacts())
}

// ownerLookupWithArtifacts binds the host component and the three attachments,
// each to a document declaring its own capability and its own pillars, over the
// given artifacts blocks.
//
//	site                     source   workload
//	host node's own Recipe    yes       no
//	attachment "mon"           no      yes
//	attachment "logs"         yes       no
//	boundary attachment        no      yes
//
// The host disagrees with every attachment on at least one pillar, and the two
// node-carried attachments disagree with each other on both — so an answer read
// from the wrong owner is a different answer rather than the same one.
func ownerLookupWithArtifacts(blocks ownerArtifactBlocks) RecipeLookup {
	return RecipeLookup{
		Recipe: func(_, componentType string) (*types.Recipe, bool) {
			if componentType != "core/compute/api-server" {
				return nil, false
			}
			return &types.Recipe{
				APIVersion: "aggrega.ai/v1alpha1",
				Kind:       "Recipe",
				Metadata:   types.RecipeMetadata{Name: "api-recipe", Version: "1.0.0"},
				Spec: types.RecipeSpec{
					Capability: ownerHostCapability,
					Implements: "core/compute/api-server",
					Source:     "./host-chart",
					Artifacts:  blocks.host,
				},
			}, true
		},
		FeatureRecipe: func(_, featureType string) (*types.FeatureRecipe, bool) {
			switch featureType {
			case ownerMonFeatureType:
				return &types.FeatureRecipe{
					Metadata: types.FeatureRecipeMetadata{Name: "mon-fr", Version: "1.0.0"},
					Spec: types.FeatureRecipeSpec{
						Capability: ownerMonCapability,
						Implements: ownerMonFeatureType,
						Workload:   types.WorkloadTemplate{Inline: ownerWorkload},
						Artifacts:  blocks.mon,
					},
				}, true
			case ownerLogsFeatureType:
				return &types.FeatureRecipe{
					Metadata: types.FeatureRecipeMetadata{Name: "logs-fr", Version: "1.0.0"},
					Spec: types.FeatureRecipeSpec{
						Capability: ownerLogsCapability,
						Implements: ownerLogsFeatureType,
						Source:     "./logs-chart",
						Artifacts:  blocks.logs,
					},
				}, true
			case ownerBoundaryFeatureType:
				return &types.FeatureRecipe{
					Metadata: types.FeatureRecipeMetadata{Name: "bridge-fr", Version: "1.0.0"},
					Spec: types.FeatureRecipeSpec{
						Capability:     ownerBoundaryCapability,
						Implements:     ownerBoundaryFeatureType,
						ExecutionScope: "boundary",
						Workload:       types.WorkloadTemplate{Inline: ownerWorkload},
						Artifacts:      blocks.bridge,
					},
				}, true
			}
			return nil, false
		},
	}
}

// ownerInput assembles the one build both owners are reached from: a single
// node holding two attachments, plus a boundary-scope attachment on the boundary
// that node sits in.
func ownerInput() RendererInput {
	nodes := types.OrderedMap[types.IRNode]{}
	nodes.Set(ownerNode, types.IRNode{
		Component:       "core/compute/api-server",
		BoundaryPath:    ownerBoundaryPath,
		ResolvedVersion: "1.0.0",
	})

	attachments := types.OrderedMap[types.IRFeatureEntry]{}
	attachments.Set(ownerMonInstance, types.IRFeatureEntry{Type: ownerMonFeatureType})
	attachments.Set(ownerLogsInstance, types.IRFeatureEntry{Type: ownerLogsFeatureType})
	attachments.Set(ownerBoundaryInstance, types.IRFeatureEntry{Type: ownerBoundaryFeatureType})
	fvm := types.OrderedMap[types.OrderedMap[types.IRFeatureEntry]]{}
	fvm.Set(ownerNode, attachments)

	boundaryProjection := types.OrderedMap[types.IRBoundaryFeature]{}
	boundaryProjection.Set(ownerBoundaryInstance, types.IRBoundaryFeature{
		Type:  ownerBoundaryFeatureType,
		Infra: types.OrderedMap[any]{},
	})
	boundaryFeatures := types.OrderedMap[types.OrderedMap[types.IRBoundaryFeature]]{}
	boundaryFeatures.Set(ownerBoundaryPath, boundaryProjection)

	authoredFeatures := types.OrderedMap[types.BoundaryFeature]{}
	authoredFeatures.Set(ownerBoundaryInstance, types.BoundaryFeature{
		Type:  ownerBoundaryFeatureType,
		Infra: types.OrderedMap[any]{},
	})
	boundary := types.Boundary{
		Directory:  "dist/cluster",
		Features:   authoredFeatures,
		Components: types.OrderedMap[types.BoundaryComponent]{},
		Boundaries: types.OrderedMap[types.Boundary]{},
	}
	boundary.Components.Set(ownerNode, types.BoundaryComponent{})
	boundaries := types.OrderedMap[types.Boundary]{}
	boundaries.Set(ownerBoundaryPath, boundary)

	return RendererInput{
		GraphView: ownerGraphFacts{},
		Graph: &types.IRGraph{
			Metadata: types.IRMetadata{AggregateName: "owner-app"},
			Spec: types.IRSpec{
				Nodes:                nodes,
				ValuesMapping:        types.OrderedMap[json.RawMessage]{},
				InfraMapping:         types.OrderedMap[json.RawMessage]{},
				FeatureValuesMapping: fvm,
				BoundaryFeatures:     boundaryFeatures,
				Provenance:           types.IRProvenance{CompilerVersion: "test", AggregateVersion: "1.0.0"},
			},
		},
		FeatureOrder: ir.FeatureOrder{
			ownerNode: {ownerMonInstance, ownerLogsInstance, ownerBoundaryInstance},
		},
		ResolveResult: &resolver.ResolveResult{
			Aggregate: &types.Aggregate{Metadata: types.AggregateMetadata{Name: "owner-app"}},
			Release: &types.Release{
				Metadata: types.ReleaseMetadata{Name: "owner-release", Version: "1.0.0", Environment: "test"},
				Spec:     types.ReleaseSpec{Boundaries: boundaries},
			},
			BoundaryInfra: map[string]*resolver.BoundaryInfra{
				ownerNode: {BoundaryPath: ownerBoundaryPath, Directory: "dist/cluster"},
			},
		},
	}
}

// ownerRender runs the fixture build and fails the test if it did not complete,
// because nothing read afterwards is attributable to the retention otherwise.
func ownerRender(t *testing.T) *RenderResult {
	t.Helper()
	res, err := RenderAll(context.Background(), ownerInput(), ownerLookup(), ownerFactory())
	if err != nil {
		t.Fatalf("the fixture build did not complete, so nothing below is attributable: %+v", err)
	}
	if res.HydratedView == nil {
		t.Fatal("the render published no mid-render view; every accessor assertion below would be unreachable")
	}
	return res
}

// TestTheRetentionHoldsOneEntryPerAttachmentOwner asserts the recorded set by
// COUNT and by IDENTITY on both owners.
//
// A count alone cannot separate a set of the right size over the right keys from
// one of the right size over the wrong keys, and this is exactly the surface where
// that confusion lives: every key here is built from strings the render also has
// under other names — the host's node id, the attachment's instance name, the
// boundary's path — so an owner assembled from the wrong pair of them produces a
// set of the same size. The keys are therefore named explicitly.
func TestTheRetentionHoldsOneEntryPerAttachmentOwner(t *testing.T) {
	t.Parallel()

	res := ownerRender(t)

	// ANTI-NEEDLE. A build carrying attachments with artifacts must record
	// owners; a zero here is the whole failure this file exists to catch, and it
	// passes every byte-identity and determinism proof over the same build.
	if got := res.hydrated.recordedNodeAttachments(); got == 0 {
		t.Fatal("the retention recorded ZERO node-carried attachment renders over a build carrying two.\n" +
			"CONSEQUENCE: every fact keyed by that owner answers its zero value, which reads exactly " +
			"like an attachment that declared nothing.")
	}
	if got := res.hydrated.recordedBoundaryAttachments(); got == 0 {
		t.Fatal("the retention recorded ZERO boundary-scope attachment renders over a build carrying one.")
	}

	// BY COUNT: one entry per attachment render, and no more. A wider owner —
	// one keyed by the node alone, say — would collapse the two node-carried
	// attachments into one entry and show up here.
	if got := res.hydrated.recordedNodeAttachments(); got != 2 {
		t.Errorf("the retention holds %d node-carried attachment renders, want 2 — one per attachment the node declares", got)
	}
	if got := res.hydrated.recordedBoundaryAttachments(); got != 1 {
		t.Errorf("the retention holds %d boundary-scope attachment renders, want 1", got)
	}

	// BY IDENTITY: the keys are the ones named, not merely two of the right shape.
	for _, key := range []nodeAttachmentKey{
		{node: ownerNode, attachment: ownerMonInstance},
		{node: ownerNode, attachment: ownerLogsInstance},
	} {
		if _, ok := res.hydrated.nodeAttachmentProjectionOf(key); !ok {
			t.Errorf("nothing is recorded for owner (node %q, attachment %q)", key.node, key.attachment)
		}
	}
	boundaryKey := boundaryAttachmentKey{boundary: ownerBoundaryPath, attachment: ownerBoundaryInstance}
	if _, ok := res.hydrated.boundaryAttachmentProjectionOf(boundaryKey); !ok {
		t.Errorf("nothing is recorded for owner (boundary %q, attachment %q)",
			boundaryKey.boundary, boundaryKey.attachment)
	}

	// The boundary-scope attachment must NOT also be recorded as a node-carried
	// one. It is declared once at the boundary and reaches every component the
	// boundary holds, so a node-rooted recording would hold the same authored
	// document once per member.
	if _, ok := res.hydrated.nodeAttachmentProjectionOf(
		nodeAttachmentKey{node: ownerNode, attachment: ownerBoundaryInstance},
	); ok {
		t.Errorf("the boundary-scope attachment %q is also recorded under the node owner; "+
			"one authored document would then be held under as many keys as the boundary has members",
			ownerBoundaryInstance)
	}

	t.Logf("DIAGNOSTIC WALK — owners recorded: %d node-carried, %d boundary-scope",
		res.hydrated.recordedNodeAttachments(), res.hydrated.recordedBoundaryAttachments())
}

// TestEveryOwnersArtifactTextIsItsOwn drives the shared output name through all
// four sites and requires each to answer its own text.
//
// The shared name is what makes this more than a presence check: all four sites
// declare an entry under it, so a key space that collapsed — an attachment filed
// under the host node, or the two attachments filed under the node alone — would
// answer one site's text where another's belongs, with the count unchanged.
func TestEveryOwnersArtifactTextIsItsOwn(t *testing.T) {
	t.Parallel()

	view := ownerRender(t).HydratedView

	// The node's own Recipe, read through the fact declared over it.
	if got, want := view.ArtifactTemplateText(ownerNode, ownerSharedArtifactName),
		ownerText("Shared", "host"); got != want {
		t.Errorf("the node's own shared entry reads %q, want %q", got, want)
	}

	for _, tc := range []struct {
		attachment string
		site       string
		ownName    string
	}{
		{ownerMonInstance, "mon", ownerMonArtifactName},
		{ownerLogsInstance, "logs", ownerLogsArtifactName},
	} {
		shared := view.AttachmentArtifactTemplateText(ownerNode, tc.attachment, ownerSharedArtifactName)
		if want := ownerText("Shared", tc.site); shared != want {
			t.Errorf("attachment %q reads %q for the shared entry, want %q.\n"+
				"INVARIANT: the entry is authored on the FeatureRecipe the attachment binds, so the "+
				"attachment coordinate is what tells it from the node's own and from another attachment's.",
				tc.attachment, shared, want)
		}
		own := view.AttachmentArtifactTemplateText(ownerNode, tc.attachment, tc.ownName)
		if want := ownerText("Own", tc.site); own != want {
			t.Errorf("attachment %q reads %q for its own entry %q, want %q", tc.attachment, own, tc.ownName, want)
		}
		// The attachment's own name is not the node's, so asking the node-keyed
		// fact for it must answer empty rather than the attachment's text.
		if leaked := view.ArtifactTemplateText(ownerNode, tc.ownName); leaked != "" {
			t.Errorf("the node-keyed fact answers %q for %q, which attachment %q authored; "+
				"the attachment's entries have leaked into a fact declared over the node's own Recipe",
				leaked, tc.ownName, tc.attachment)
		}
	}

	// The boundary-scope attachment, whose entries are keyed by the boundary.
	shared := view.BoundaryAttachmentArtifactTemplateText(
		ownerBoundaryPath, ownerBoundaryInstance, ownerSharedArtifactName)
	if want := ownerText("Shared", "bridge"); shared != want {
		t.Errorf("the boundary-scope attachment reads %q for the shared entry, want %q", shared, want)
	}
	own := view.BoundaryAttachmentArtifactTemplateText(
		ownerBoundaryPath, ownerBoundaryInstance, ownerBoundaryArtifact)
	if want := ownerText("Own", "bridge"); own != want {
		t.Errorf("the boundary-scope attachment reads %q for its own entry, want %q", own, want)
	}
	// And it is not reachable through the node-rooted attachment key.
	if leaked := view.AttachmentArtifactTemplateText(
		ownerNode, ownerBoundaryInstance, ownerBoundaryArtifact); leaked != "" {
		t.Errorf("the node-rooted attachment key answers %q for a boundary-scope entry", leaked)
	}
}

// TestEveryOwnersPillarAnswersAreItsOwn is the independence assertion, and it is
// the reason these facts carry an attachment coordinate at all.
//
// Each attachment's render runs under the host component's node identity over a
// source and a workload its FeatureRecipe declares. So asked of the node, the
// answers would be the host's — admitting a reference to a pillar the attachment
// does not carry wherever the host carries it, and refusing a legitimate one
// wherever the host does not. Both directions are wrong and neither shows in the
// rendered output, which is why the fixture makes every pair disagree: an
// assertion over a build where the host and its attachments agreed would pass on
// exactly the defect it exists to catch.
func TestEveryOwnersPillarAnswersAreItsOwn(t *testing.T) {
	t.Parallel()

	view := ownerRender(t).HydratedView

	// The host node's own answers, which every attachment's must differ from.
	hostSource := view.ResolvedNodeSourceDeclared(ownerNode)
	hostWorkload := view.ParsedNodeWorkloadPresent(ownerNode)
	if !hostSource || hostWorkload {
		t.Fatalf("the fixture's host answers source=%v workload=%v; it must declare a source and no "+
			"workload, or the disagreement every assertion below rests on does not exist",
			hostSource, hostWorkload)
	}

	for _, tc := range []struct {
		attachment   string
		wantSource   bool
		wantWorkload bool
	}{
		{ownerMonInstance, false, true},
		{ownerLogsInstance, true, false},
	} {
		gotSource := view.AttachmentResolvedSourceDeclared(ownerNode, tc.attachment)
		gotWorkload := view.AttachmentParsedWorkloadPresent(ownerNode, tc.attachment)
		if gotSource != tc.wantSource {
			t.Errorf("attachment %q: source declared = %v, want %v (the host answers %v)",
				tc.attachment, gotSource, tc.wantSource, hostSource)
		}
		if gotWorkload != tc.wantWorkload {
			t.Errorf("attachment %q: workload present = %v, want %v (the host answers %v)",
				tc.attachment, gotWorkload, tc.wantWorkload, hostWorkload)
		}
	}

	// The two attachments disagree with each other on both pillars, so an owner
	// keyed by the node alone would answer whichever rendered last for both.
	if view.AttachmentResolvedSourceDeclared(ownerNode, ownerMonInstance) ==
		view.AttachmentResolvedSourceDeclared(ownerNode, ownerLogsInstance) {
		t.Error("the two attachments on one node answer the same resolved source; the fixture must " +
			"make them disagree, or an owner missing the attachment coordinate would pass")
	}
	if view.AttachmentParsedWorkloadPresent(ownerNode, ownerMonInstance) ==
		view.AttachmentParsedWorkloadPresent(ownerNode, ownerLogsInstance) {
		t.Error("the two attachments on one node answer the same parsed workload")
	}

	// The boundary-scope attachment declares a workload and no source: the reverse
	// of the host on both.
	if got := view.BoundaryAttachmentResolvedSourceDeclared(ownerBoundaryPath, ownerBoundaryInstance); got {
		t.Errorf("the boundary-scope attachment answers source declared = %v, want false (the host answers %v)",
			got, hostSource)
	}
	if got := view.BoundaryAttachmentParsedWorkloadPresent(ownerBoundaryPath, ownerBoundaryInstance); !got {
		t.Errorf("the boundary-scope attachment answers workload present = %v, want true (the host answers %v)",
			got, hostWorkload)
	}

	t.Logf("DIAGNOSTIC WALK — pillars per owner: host(source=%v workload=%v) "+
		"mon(source=%v workload=%v) logs(source=%v workload=%v) bridge(source=%v workload=%v)",
		hostSource, hostWorkload,
		view.AttachmentResolvedSourceDeclared(ownerNode, ownerMonInstance),
		view.AttachmentParsedWorkloadPresent(ownerNode, ownerMonInstance),
		view.AttachmentResolvedSourceDeclared(ownerNode, ownerLogsInstance),
		view.AttachmentParsedWorkloadPresent(ownerNode, ownerLogsInstance),
		view.BoundaryAttachmentResolvedSourceDeclared(ownerBoundaryPath, ownerBoundaryInstance),
		view.BoundaryAttachmentParsedWorkloadPresent(ownerBoundaryPath, ownerBoundaryInstance))
}

// TestEveryOwnersDelimiterPairComesFromItsOwnProvider asserts each site's
// artifact-template delimiters are answered from the provider that reads that
// site's entries.
//
// The pair is a property of the provider, and an attachment binds its own
// FeatureRecipe declaring its own capability — so a node whose attachment renders
// through a different provider is ordinary rather than exceptional. Read with the
// wrong pair, a template's downstream target syntax surfaces as references it does
// not make, so an answer taken from the host's provider is a wrong verdict rather
// than a mis-filing. No two sites here declare the same pair, and none declares
// the usual one, which is what makes a wrong answer visible.
func TestEveryOwnersDelimiterPairComesFromItsOwnProvider(t *testing.T) {
	t.Parallel()

	view := ownerRender(t).HydratedView

	want := func(capability string) constraint.ArtifactTemplateDelimiters {
		pair := ownerDelimiters[capability]
		return constraint.ArtifactTemplateDelimiters{Left: pair[0], Right: pair[1]}
	}

	if got, w := view.ProviderArtifactTemplateDelimiters(ownerNode), want(ownerHostCapability); got != w {
		t.Errorf("the node's own provider answers %v, want %v", got, w)
	}
	for _, tc := range []struct {
		attachment string
		capability string
	}{
		{ownerMonInstance, ownerMonCapability},
		{ownerLogsInstance, ownerLogsCapability},
	} {
		got := view.AttachmentProviderArtifactTemplateDelimiters(ownerNode, tc.attachment)
		if w := want(tc.capability); got != w {
			t.Errorf("attachment %q answers %v, want %v — the pair its OWN provider declares", tc.attachment, got, w)
		}
		if got == want(ownerHostCapability) {
			t.Errorf("attachment %q answers the HOST provider's pair; the pair is a property of the "+
				"provider that reads the entry, and the attachment binds its own", tc.attachment)
		}
	}

	got := view.BoundaryAttachmentProviderArtifactTemplateDelimiters(ownerBoundaryPath, ownerBoundaryInstance)
	if w := want(ownerBoundaryCapability); got != w {
		t.Errorf("the boundary-scope attachment answers %v, want %v", got, w)
	}
	if got == want(ownerHostCapability) {
		t.Error("the boundary-scope attachment answers the host provider's pair")
	}

	// An attachment nothing binds is answered with the usual pair rather than with
	// an absence, which is the fallback the render itself applies.
	usual := constraint.ArtifactTemplateDelimiters{Left: "{{", Right: "}}"}
	if got := view.AttachmentProviderArtifactTemplateDelimiters(ownerNode, "no-such-attachment"); got != usual {
		t.Errorf("an unbound attachment answers %v, want the usual pair %v", got, usual)
	}
	if got := view.BoundaryAttachmentProviderArtifactTemplateDelimiters(
		ownerBoundaryPath, "no-such-attachment"); got != usual {
		t.Errorf("an unbound boundary-scope attachment answers %v, want the usual pair %v", got, usual)
	}

	t.Logf("DIAGNOSTIC WALK — delimiter pairs per owner: host %v, mon %v, logs %v, bridge %v",
		view.ProviderArtifactTemplateDelimiters(ownerNode),
		view.AttachmentProviderArtifactTemplateDelimiters(ownerNode, ownerMonInstance),
		view.AttachmentProviderArtifactTemplateDelimiters(ownerNode, ownerLogsInstance),
		view.BoundaryAttachmentProviderArtifactTemplateDelimiters(ownerBoundaryPath, ownerBoundaryInstance))
}

// TestTheNodesOwnArtifactAxisStillEnumeratesOnlyItsOwnEntries holds the boundary
// the attachment retention must not cross.
//
// Every attachment's entries flow through the same artifacts walk as the node's
// own, under the node's identity. Before this retention existed, what kept them
// out of the node's own values was that an attachment's render carried no
// retention at all; now it carries one, so the discriminator is which retention it
// carries rather than whether it carries any. If an attachment's render were given
// the per-node retention, its entries would be recorded under the host node — and
// the axis enumerating that node's artifacts, which is what invokes a rule once
// per entry, would present them as the node's own.
func TestTheNodesOwnArtifactAxisStillEnumeratesOnlyItsOwnEntries(t *testing.T) {
	t.Parallel()

	res := ownerRender(t)

	names := res.hydrated.artifactNames(ownerNode)
	if len(names) == 0 {
		t.Fatal("the node's own artifacts walk recorded nothing; the exclusion below would hold vacuously")
	}
	wantOwn := map[string]bool{ownerSharedArtifactName: true, ownerHostArtifactName: true}
	for _, name := range names {
		if !wantOwn[name] {
			t.Errorf("the node's own artifact axis enumerates %q, which no entry of its Recipe declares; "+
				"an attachment's entries have been recorded under the host node", name)
		}
	}
	if len(names) != len(wantOwn) {
		t.Errorf("the node's own artifact axis enumerates %v; want exactly the two entries its Recipe declares", names)
	}
}
