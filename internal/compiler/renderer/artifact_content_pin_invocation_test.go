// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	"github.com/aggrega-ai/aggrega-cli/pkg/digest"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// The artifact-content-pin rule expressed as declared values is only as good as the
// build's invocation of it, and a check-level proof cannot see that: a value that is
// never invoked raises nothing, which reads exactly like a value that ran and
// approved. So the halt is driven at the entry point, once per carrier the condition
// reaches.
//
// The two directions are both driven over each carrier. An admitting build is what
// makes the raising build attributable: without it a rule refusing everything would
// satisfy the raising assertion on its own, and a rule invoked zero times would
// satisfy the admitting one.

// pinImportBody is the content frozen into the recorded pin.
const pinImportBody = "kind: NetworkPolicy\nmetadata:\n  name: deny-all\n"

// pinImportDrifted is what the file holds after an edit that the freeze never saw.
const pinImportDrifted = "kind: NetworkPolicy\nmetadata:\n  name: allow-all\n"

// pinImportFile writes the imported artifact file and returns its absolute path
// together with the digest of the content written.
//
// The catalog rewrites a relative $import to an absolute path when it loads the
// entity, so a fixture assembling a Recipe directly supplies the absolute form the
// walk would otherwise have been handed.
func pinImportFile(t *testing.T, body string) (string, string) {
	t.Helper()
	p := filepath.Join(t.TempDir(), "policy.yaml")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	return p, digest.ContentDigest([]byte(body))
}

const pinWorkload = `apiVersion: aggrega.ai/v1alpha1
kind: Workload
containers:
  main:
    image: myapp:latest
`

// pinHostLookup binds the host component to a Recipe whose artifacts block carries
// one imported entry with the given recorded pin, and the monitoring feature to a
// FeatureRecipe carrying no artifacts at all — so any halt is attributable to the
// host's own carrier.
//
// An empty pin builds a Recipe carrying NO artifactDigests block, which is the
// state of a Recipe that was never frozen. It is spelled as an absent block rather
// than as an entry with an empty digest because the two are different inputs: an
// entry recording an empty digest is a recorded pin whose value is empty, which
// the rule compares against and refuses.
func pinHostLookup(importPath, pin string) renderer.RecipeLookup {
	artifacts := types.OrderedMap[types.ArtifactEntry]{}
	artifacts.Set("pin-host-policy.yaml", types.ArtifactEntry{Import: importPath})
	var digests []types.ArtifactDigest
	if pin != "" {
		digests = []types.ArtifactDigest{{Name: "pin-host-policy.yaml", Digest: pin}}
	}
	return renderer.RecipeLookup{
		Recipe: func(_, componentType string) (*types.Recipe, bool) {
			if componentType != "core/compute/api-server" {
				return nil, false
			}
			return &types.Recipe{
				Metadata: types.RecipeMetadata{Name: "api-recipe", Version: "1.0.0"},
				Spec: types.RecipeSpec{
					Capability:      "helm",
					Implements:      "core/compute/api-server",
					Workload:        types.WorkloadTemplate{Inline: pinWorkload},
					Artifacts:       artifacts,
					ArtifactDigests: digests,
				},
			}, true
		},
		FeatureRecipe: func(_, featureType string) (*types.FeatureRecipe, bool) {
			if featureType != "core/monitoring" {
				return nil, false
			}
			return &types.FeatureRecipe{
				Metadata: types.FeatureRecipeMetadata{Name: "mon"},
				Spec: types.FeatureRecipeSpec{
					Capability: "helm",
					Implements: "core/monitoring",
					Workload:   types.WorkloadTemplate{Inline: pinWorkload},
				},
			}, true
		},
	}
}

// pinFeatureLookup is the mirror: the host Recipe declares no artifacts and the
// FeatureRecipe carries the imported entry and the recorded pin, so any halt is
// attributable to the attachment's own carrier.
func pinFeatureLookup(importPath, pin string) renderer.RecipeLookup {
	artifacts := types.OrderedMap[types.ArtifactEntry]{}
	artifacts.Set("pin-feature-policy.yaml", types.ArtifactEntry{Import: importPath})
	return renderer.RecipeLookup{
		Recipe: func(_, componentType string) (*types.Recipe, bool) {
			if componentType != "core/compute/api-server" {
				return nil, false
			}
			return &types.Recipe{
				Metadata: types.RecipeMetadata{Name: "api-recipe", Version: "1.0.0"},
				Spec: types.RecipeSpec{
					Capability: "helm",
					Implements: "core/compute/api-server",
					Workload:   types.WorkloadTemplate{Inline: pinWorkload},
				},
			}, true
		},
		FeatureRecipe: func(_, featureType string) (*types.FeatureRecipe, bool) {
			if featureType != "core/monitoring" {
				return nil, false
			}
			return &types.FeatureRecipe{
				Metadata: types.FeatureRecipeMetadata{Name: "mon"},
				Spec: types.FeatureRecipeSpec{
					Capability:      "helm",
					Implements:      "core/monitoring",
					Workload:        types.WorkloadTemplate{Inline: pinWorkload},
					Artifacts:       artifacts,
					ArtifactDigests: []types.ArtifactDigest{{Name: "pin-feature-policy.yaml", Digest: pin}},
				},
			}, true
		},
	}
}

// TestAnImportedArtifactWithNoRecordedPinRendersWhateverItsContent holds the
// branch the two directions above do not reach: an entry supplied by an imported
// file whose Recipe records no pin for it.
//
// The render retains the file that entry was supplied from for EVERY imported
// entry, not only for one carrying a pin, because which file supplied an entry
// and whether a document froze a digest for it are different questions. So this
// is the row that says the wider retention introduced no verdict: a Recipe that
// was never frozen has nothing to compare, and content that would fail the
// comparison if a pin existed renders here.
//
// It is driven at the render entry rather than at check level because a
// check-level fixture supplies the facts by hand and would answer this question
// with whatever the fixture was written to say.
func TestAnImportedArtifactWithNoRecordedPinRendersWhateverItsContent(t *testing.T) {
	t.Parallel()

	driftedPath, _ := pinImportFile(t, pinImportDrifted)
	if _, err := renderer.RenderAll(
		context.Background(), pinPinnedInput(t),
		pinHostLookup(driftedPath, ""), testHelmFactory(t),
	); err != nil {
		t.Fatalf("a Recipe that recorded no pin for its imported artifact must render "+
			"whatever that file now holds: %v", err)
	}
}

// TestTheArtifactContentPinRuleAdmitsAndRefusesOnANodesOwnRecipe drives the
// node-rooted value through the production path in both directions.
func TestTheArtifactContentPinRuleAdmitsAndRefusesOnANodesOwnRecipe(t *testing.T) {
	t.Parallel()

	intactPath, intactPin := pinImportFile(t, pinImportBody)
	if _, err := renderer.RenderAll(
		context.Background(), pinPinnedInput(t), pinHostLookup(intactPath, intactPin), testHelmFactory(t),
	); err != nil {
		t.Fatalf("a Recipe whose imported artifact still matches its recorded pin must render: %v", err)
	}

	driftedPath, _ := pinImportFile(t, pinImportDrifted)
	_, err := renderer.RenderAll(
		context.Background(), pinPinnedInput(t), pinHostLookup(driftedPath, intactPin), testHelmFactory(t),
	)
	if err == nil {
		t.Fatal("a Recipe whose imported artifact no longer matches its recorded pin rendered; want a halt")
	}
	if !errors.Is(err, renderer.ErrArtifactContentMismatch()) {
		t.Fatalf("the halt does not carry the content-mismatch condition: %+v", err)
	}

	// The declared rule's own wording: the registered sentence, the bare artifact
	// name, the file the entry was supplied from, and the two captioned digests. A
	// message carrying the registered sentence alone would not say which of the two
	// readings produced the halt.
	//
	// The path is asserted here and not only at check level because it is the one
	// detail whose value comes from a fact the production walk has to have recorded
	// at the same coordinate the pin was recorded at. A check-level fixture supplies
	// both by hand and cannot tell a walk that records neither from one that records
	// them under crossed names.
	msg := cerrors.CauseChain(err)
	for _, want := range []string{
		renderer.ErrArtifactContentMismatch().Error(),
		`"pin-host-policy.yaml"`,
		`imported file "` + driftedPath + `"`,
		`the recorded pin is "` + intactPin + `"`,
		`its content digests to "` + digest.ContentDigest([]byte(pinImportDrifted)) + `"`,
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("the halt does not carry %q, so it is not the declared rule's finding.\nfull halt: %s", want, msg)
		}
	}
}

// TestTheArtifactContentPinRuleAdmitsAndRefusesOnAnAttachmentsFeatureRecipe drives
// the node-carried attachment value through the production path in both directions.
//
// It is the proof the node-rooted test above structurally cannot give. A
// FeatureRecipe's entries flow through the same artifacts walk under the HOST
// component's node identity, so a build refusing them through the node-keyed facts
// would be measuring this FeatureRecipe's content against the host Recipe's recorded
// pin — and here the host records none, so such a build admits the drift silently.
func TestTheArtifactContentPinRuleAdmitsAndRefusesOnAnAttachmentsFeatureRecipe(t *testing.T) {
	t.Parallel()

	intactPath, intactPin := pinImportFile(t, pinImportBody)
	if _, err := renderer.RenderAll(
		context.Background(), pinPinnedInput(t), pinFeatureLookup(intactPath, intactPin), testHelmFactory(t),
	); err != nil {
		t.Fatalf("a FeatureRecipe whose imported artifact still matches its recorded pin must render: %v", err)
	}

	driftedPath, _ := pinImportFile(t, pinImportDrifted)
	_, err := renderer.RenderAll(
		context.Background(), pinPinnedInput(t), pinFeatureLookup(driftedPath, intactPin), testHelmFactory(t),
	)
	if err == nil {
		t.Fatal("a FeatureRecipe whose imported artifact no longer matches its recorded pin rendered; want a halt")
	}
	if !errors.Is(err, renderer.ErrArtifactContentMismatch()) {
		t.Fatalf("the halt does not carry the content-mismatch condition: %+v", err)
	}

	msg := cerrors.CauseChain(err)
	for _, want := range []string{
		renderer.ErrArtifactContentMismatch().Error(),
		`"pin-feature-policy.yaml"`,
		`imported file "` + driftedPath + `"`,
		`attachment "mon"`,
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("the halt does not carry %q, so it is not the attachment carrier's finding.\nfull halt: %s", want, msg)
		}
	}
}

// pinPinnedInput is the monitoring-feature build with the graph-stage presentation
// supplied, so the render publishes the mid-render presentation the rules are
// invoked against. A fixture omitting it would leave them invoked zero times for a
// reason that has nothing to do with what is being proved.
func pinPinnedInput(t *testing.T) renderer.RendererInput {
	t.Helper()
	input := monitoringFeatureInput(t)
	input.GraphView = invocationGraphFacts{}
	return input
}
