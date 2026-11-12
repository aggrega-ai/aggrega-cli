// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package renderer

import (
	"context"
	"slices"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// The bundle files carry a template reference so the expansion goes through the
// same resolve-then-evaluate path a written-out entry does.
const (
	axisBundleOneBody = "kind: BundleOne\ninstance: {{ .context.instance_id }}\n"
	axisBundleTwoBody = "kind: BundleTwo\ninstance: {{ .context.instance_id }}\n"
	axisImportedBody  = "kind: Imported\ninstance: {{ .context.instance_id }}\n"
)

// The authored keys and the output names this fixture separates.
//
// The bundle form is authored under a key that is NOT an output name — one key
// expands into one output per bundle file — and that divergence is the whole
// point: on a corpus where the key set and the output-name set are the same list,
// an axis enumerating keys and an axis enumerating output names are
// indistinguishable, and every assertion over either passes under both designs.
var (
	// axisAuthoredKeys is what the Recipe's artifacts block is keyed by.
	axisAuthoredKeys = []string{"inline.yaml", "policy", "bundle-slot"}

	// axisOutputNames is what those three entries resolve to, in walk order: the
	// Recipe's entry order, and within the bundle entry the order that bundle
	// declares its files. One entry expands to two, so this list is LONGER than
	// the authored one.
	axisOutputNames = []string{"inline.yaml", "policy", "bundle-slot/bundle-one.yaml", "bundle-slot/bundle-two.yaml"}

	// axisBundleKey is the authored key that names no single output on its own: the
	// bundle it consumes expands into one output per file, each nested under it.
	axisBundleKey = "bundle-slot"

	// axisNamesCarryingNoOutput are names no artifact axis may present. The bundle
	// key names an expansion rather than a file, and the imported file's own name
	// selects an entry's content without naming its output. A rule invoked for
	// either would be asked about a name every artifact fact holds nothing for.
	axisNamesCarryingNoOutput = []string{axisBundleKey, "renamed-policy.yaml"}
)

// discriminatingArtifactCorpus renders one node whose Recipe exercises all three
// artifact authoring forms, with the authored key deliberately diverging from the
// output name wherever the form permits it.
//
// The imported file is written under a name that differs from the key its entry is
// authored under — so an axis that reached for the file's name instead of the key's
// would present a name nothing holds — and the bundle is consumed under a key that
// is the name of none of the files it holds.
func discriminatingArtifactCorpus(t *testing.T, node string) *RenderResult {
	t.Helper()

	imported := writeArtifactFile(t, t.TempDir(), "renamed-policy.yaml", axisImportedBody)

	artifacts := types.OrderedMap[types.ArtifactEntry]{}
	artifacts.Set("inline.yaml", types.ArtifactEntry{Inline: "kind: Inline\ninstance: {{ .context.instance_id }}\n"})
	artifacts.Set("policy", types.ArtifactEntry{Import: imported})
	artifacts.Set("bundle-slot", types.ArtifactEntry{Import: "SharedPolicies#/artifacts/PolicyPair"})

	recipe := sourceRecipe("axis-discriminating", nil)
	recipe.Spec.Artifacts = artifacts

	input, lookup, factory := corpusFromRecipes(map[string]*types.Recipe{node: recipe})
	input.ArtifactLibraries = map[string]types.ArtifactLibrary{
		"SharedPolicies": artifactLibraryFixtureOrdered("SharedPolicies", "PolicyPair",
			[]string{"bundle-one.yaml", "bundle-two.yaml"},
			[]string{axisBundleOneBody, axisBundleTwoBody}),
	}

	res, err := RenderAll(context.Background(), input, lookup, factory)
	if err != nil {
		t.Fatalf("RenderAll: %+v", err)
	}
	return res
}

// TestTheArtifactAxisEnumeratesOutputNamesAndNotAuthoredKeys is the enumeration
// assertion for this axis, and it is stated over a corpus that can tell the two
// candidate designs apart.
//
// An equivalence cross-check cannot stand in for it. A narrowed axis yields fewer
// subjects, both readings are then asked about fewer things, they agree, and
// nothing is raised — which is indistinguishable from a rule that ran and
// approved. So what the axis must CONTAIN is asserted here directly.
//
// The fixture's three entries resolve to four outputs, and one of its three
// authored keys names no output of its own. An axis built from the authored block
// would therefore enumerate three names, one of which no artifact fact holds
// anything for, and would miss both expanded bundle files outright.
func TestTheArtifactAxisEnumeratesOutputNamesAndNotAuthoredKeys(t *testing.T) {
	t.Parallel()

	const node = "discriminating"
	res := discriminatingArtifactCorpus(t, node)

	// The fixture has to actually separate the designs, so its own divergence is
	// asserted before anything is concluded from it. If the two lists were the
	// same length this test would pass under either design.
	if len(axisOutputNames) == len(axisAuthoredKeys) {
		t.Fatal("the fixture's authored keys and output names are the same length; it cannot separate the two designs")
	}

	axis := res.HydratedArtifactAxis(nil)
	if axis == nil {
		t.Fatal("the render retained no mid-render state, so the axis could not be built")
	}

	got := make([]string, 0, len(axisOutputNames))
	for _, c := range axis.Artifacts(node) {
		got = append(got, c.Value)
	}
	if !slices.Equal(got, axisOutputNames) {
		t.Errorf("the axis enumerates %v, want the output names in walk order %v.\n"+
			"INVARIANT: an artifact coordinate is the output name an entry resolves to.\n"+
			"CONSEQUENCE: an axis enumerating authored keys asks every artifact fact about "+
			"names it holds nothing for, and a rule reading an empty template raises nothing — "+
			"which reads exactly like a rule that ran and approved.", got, axisOutputNames)
	}

	// Stated in the other direction as well, because a superset would satisfy the
	// comparison above if the order happened to match: a name that carries no
	// output must not appear at all.
	for _, name := range axisNamesCarryingNoOutput {
		if slices.Contains(got, name) {
			t.Errorf("the axis enumerates %q, which names no artifact this Recipe projects", name)
		}
	}

	// And every name the axis yields must be answerable by the text a rule reads
	// through it. This is the property the refused design breaks: the coordinate
	// and the retention have to be one set, not two that merely overlap. The text
	// is read from the retention the presentation answers from, because the axis
	// and that retention being one set is exactly the claim.
	projection, ok := res.hydrated.projection(node)
	if !ok {
		t.Fatal("the node has no retained mid-render projection")
	}
	for _, name := range got {
		if text, held := projection.ArtifactTemplateText.Get(name); !held || text == "" {
			t.Errorf("the retained text for %q/%q is absent or empty for a name the axis enumerated; "+
				"the axis and the artifact facts are not one set", node, name)
		}
	}
}

// TestTheProjectedArtifactAxisEnumeratesEveryExpandedOutput states the same
// containment for the axis over what a render projected.
//
// The two axes are separate values and this fixture is where the bundle expansion
// could be lost from either one independently, so each is asserted over it. Every
// entry here evaluates to content, so this axis holds all four outputs; the two
// axes diverge on an entry that evaluates to nothing, which the divergence proof
// beside this file covers.
func TestTheProjectedArtifactAxisEnumeratesEveryExpandedOutput(t *testing.T) {
	t.Parallel()

	const node = "discriminating"
	res := discriminatingArtifactCorpus(t, node)

	// This corpus is assembled without an earlier stage to extend, so the render
	// publishes no presentation of its own. The presentation is built over the same
	// retention the render would have published, which is the value this axis reads
	// its name set from — so what is asserted below is unchanged by supplying it
	// here.
	view := presentationOverTheRender(t, res)
	res.RenderedView = view

	axis := res.RenderedArtifactAxis(nil)
	if axis == nil {
		t.Fatal("the axis refused to build over a completed render carrying a view")
	}

	got := make([]string, 0, len(axisOutputNames))
	for _, c := range axis.Artifacts(node) {
		got = append(got, c.Value)
	}
	if !slices.Equal(got, axisOutputNames) {
		t.Errorf("the projected axis enumerates %v, want %v", got, axisOutputNames)
	}
	for _, name := range axisNamesCarryingNoOutput {
		if slices.Contains(got, name) {
			t.Errorf("the projected axis enumerates %q, which names no projected artifact", name)
		}
	}

	// The bytes are reachable under every enumerated name, which is what makes the
	// expansion present rather than merely counted.
	for _, name := range got {
		if len(view.ArtifactProjectionContent(node, name)) == 0 {
			t.Errorf("ArtifactProjectionContent(%q, %q) is empty for a name the axis enumerated", node, name)
		}
	}
}
