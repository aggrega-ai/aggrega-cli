// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// End-to-end proof, from real builds, of what happens when a Recipe authors its
// own CloudFormation Outputs entry carrying an Export name.
//
// A cross-boundary uses edge makes the consumed endpoint publish an export, and
// the export name is assembled from the deployment identity, the boundary, the
// component and the injected provides key. An Outputs entry a Recipe writes is
// carried through as authored, Export block included, so an author can spell a
// name the assembly also produces for the same build — leaving one stack
// declaring one export name twice, from two places, only one of which appears in
// any file. That coincidence halts; authoring an Export name does not.
//
// Each case here is a whole build through the real compiled CloudFormation Wasm
// guest against the sibling catalog, so the comparison is reached the way a real
// build reaches it: the build's link pass hands the consumed endpoint's
// assembled stack to the guest's publish, which compares there. Every case
// shares one Aggregate, Manifest and Release with the cross-boundary wiring
// proof next door, and varies only which of three alternative db-reader Recipes
// the catalog carries — so the export name the build derives is the same string
// in all of them, and the authored Outputs block is the only difference.
//
// These tests depend on both aggrega-cli AND aggrega-hub submodules being
// initialized in the umbrella checkout (the catalog paths cross the submodule
// boundary into aggrega-hub). They skip when the sibling catalog is absent.

package compiler_test

import (
	"sort"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline/pipelinetest"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
)

// cfnDerivedDbHostExportName is the export name the two-boundary build derives
// for the database host: the app-b -> db edge injects `host` across the boundary,
// so the publish on the db end assembles a name from this deployment's identity,
// the stack-a boundary path, the db component, and the `host` provides key.
//
// It is pinned here because the shadowing fixture has to spell it — a collision
// needs one string written twice, and one of the two writers is a Recipe. The
// pin is asserted against the emitted bytes in the shipping cases below, so a
// change to how the name is assembled fails there, naming both the new name and
// this constant, rather than leaving the shadowing fixture quietly no longer
// colliding.
const cfnDerivedDbHostExportName = "aggrega--cloudformation-two-boundary--dist-zsstack-a-zscloudformation--db--host"

// cfnAuthoredExportInput is the two-boundary workspace with the catalog's own
// db-reader Recipe replaced by the fixture-local variant in variantDir. Every
// other entity, and the whole Release, is the one the cross-boundary wiring
// proof builds, so the two builds differ only in the Outputs block the
// application's fragment authors.
func cfnAuthoredExportInput(t *testing.T, variantDir string) pipelinetest.Input {
	t.Helper()
	skipIfNoSiblingCatalog(t)
	const replaced = "/recipe/cloudformation/db-reader/1.0.0/recipe.yaml"
	shared := cfnCatalogPaths()
	paths := make([]string, 0, len(shared))
	dropped := 0
	for _, p := range shared {
		if strings.HasSuffix(p, replaced) {
			dropped++
			continue
		}
		paths = append(paths, p)
	}
	// A substitution that removed nothing would leave two definitions of one
	// Recipe identity, and the catalog keeps the one loaded first — which here is
	// the shared path, so the fixture-local Outputs block would simply not be in
	// the build. Every case below would then fail on a missing output or on a
	// build that shipped, neither of which names the actual cause. Name it here.
	if dropped != 1 {
		t.Fatalf("expected to replace exactly one catalog Recipe path ending %q, dropped %d; paths: %v", replaced, dropped, shared)
	}
	paths = append(paths, "testdata/cloudformation-two-boundary/catalog/"+variantDir+"/db-reader.recipe.yaml")
	return pipelinetest.Input{
		ReleasePath:  "testdata/cloudformation-two-boundary/release.yaml",
		CatalogPaths: paths,
	}
}

// TestCloudFormationAuthoredExport_ShadowingDerivedNameHaltsTheBuild builds a
// workspace whose application Recipe authors an Outputs entry carrying the exact
// export name the same build's cross-boundary link derives for the database
// host, and proves the build refuses.
//
// The refusal is proven by building, not by calling the comparison: the halt
// arrives out of pipelinetest.BuildE, which runs the same pipeline `aggrega
// build` runs, so the comparison is reached through the host's post-assembly
// link pass handing the consumed endpoint's assembled stack to the guest.
//
// The authored entry deliberately does NOT sit at the stack output the publish
// writes to. That output — the one the database's own `host` projection produced
// — is skipped rather than compared, because it holds either the projection the
// Export attaches to or an Export an earlier edge's publish already wrote. The
// authored entry is on the application component instead, so it reaches the
// stack under a different output name and is compared.
func TestCloudFormationAuthoredExport_ShadowingDerivedNameHaltsTheBuild(t *testing.T) {
	in := cfnAuthoredExportInput(t, "authored-export-shadowing")

	result, err := pipelinetest.BuildE(t, in)
	if err == nil {
		emitted := make([]string, 0, len(result.Artifacts))
		for k := range result.Artifacts {
			emitted = append(emitted, k)
		}
		sort.Strings(emitted)
		t.Fatalf("build succeeded; want a halt on the authored export name shadowing the derived one. emitted artifacts: %v", emitted)
	}
	// The whole chain, not the outermost frame: a build halt is re-wrapped on its
	// way up, and the frame that names the actual fault sits underneath.
	msg := cerrors.CauseChain(err)
	t.Logf("emitted diagnostic, verbatim:\n%s", msg)

	// The refusal is raised inside the guest, and its identity crosses the socket
	// with it: the guest reports the condition's identifiers on the link_publish
	// output's failure channel, and the host reports them back off the error. So
	// the error a real build produced is asserted to carry a code, and to carry
	// the GUEST's rather than the host's generic plugin-failure one.
	//
	// Which number it is cannot be asserted here without writing it out, and the
	// declaring package is internal to the guest tree so its accessor is
	// unreachable from this one. A written number would be a second copy of a
	// registered value with nothing comparing the two, so what is asserted here is
	// the property this boundary can check — that an identity crossed — and which
	// condition each code names is held where the conditions are declared, against
	// the accessors themselves.
	hostShort, _ := cerrors.ShortCodeOf(wasm.ErrWasmGuestError())
	short, ok := cerrors.ShortCodeOf(err)
	if !ok {
		t.Error("the build's own error reports no short code, so the condition the guest raised is " +
			"unquotable: its identity did not cross the socket")
	}
	if short == hostShort {
		t.Errorf("the build's own error reports the host's generic plugin-failure code %q rather than the "+
			"condition the guest raised", hostShort)
	}
	if _, ok := cerrors.CodeOf(err); !ok {
		t.Error("the build's own error reports no descriptive code, so no machine consumer can branch on " +
			"the condition without parsing the message")
	}

	for _, want := range []string{
		// The frame the build's post-assembly link pass adds when the publish on
		// the consumed end refuses. Its presence is what shows the comparison was
		// reached from a build, through the pass that hands the consumed
		// endpoint's assembled stack to the engine — and not from anywhere else.
		`cross-boundary link publish for uses edge "app-b" -> "db"`,
		// The condition's own sentence.
		"an export name a cross-boundary link derives is already declared by another Outputs entry in the same stack",
		// The export name, which is the one string both sides spell identically
		// and therefore the term an author can search their own files for.
		cfnDerivedDbHostExportName,
		// The derived side, by the four coordinates the name was assembled from.
		`provides key "host"`,
		`component "db"`,
		`boundary "dist/stack-a/cloudformation"`,
		`Release "cloudformation-two-boundary"`,
		// And the authored side: another Outputs entry of the same stack declares
		// that same name, reached through the stack output it sits under. The
		// halt says that output name is the provider's own rather than one a
		// Recipe wrote, because the authored entry key cannot be read back out
		// of it.
		"already declares that same export name",
		`stack output "appXzhaXXdbXzuendpoint"`,
		"a name this provider assembles rather than one a Recipe wrote",
		// The two edits that resolve it.
		"rename the export name the entry declares",
		"remove that entry's Export block",
	} {
		if !strings.Contains(msg, want) {
			t.Fatalf("diagnostic is missing %q; got:\n%s", want, msg)
		}
	}
}

// TestCloudFormationAuthoredExport_NameCollidingWithNothingShips builds the same
// workspace with an authored Export name that no derived name in the build
// matches, and proves the build ships it untouched. Authoring an Export name is
// supported; only the coincidence with a derived name is refused, so without
// this case a green would prove only that something now halts.
//
// It also carries the pin on cfnDerivedDbHostExportName: the derived export the
// link publishes is read out of the emitted bytes and compared against the
// constant the shadowing fixture spells, so the two cannot drift apart silently.
//
// A build that ships here is a decision rather than a comparison that never
// happened, and the shadowing case is what establishes that: it halts on this
// same fixture shape, differing only in the name the Outputs entry declares, so
// the comparison is demonstrably live over these bytes.
func TestCloudFormationAuthoredExport_NameCollidingWithNothingShips(t *testing.T) {
	in := cfnAuthoredExportInput(t, "authored-export-inert")

	result := pipelinetest.Build(t, in)

	producer, producerRaw := findCfnStack(t, result.Artifacts, "stack-a")
	t.Logf("producer stack template (stack-a):\n%s", producerRaw)

	outputs := mustCfnMap(t, producer, "Outputs")

	// The authored entry survived as written, under the output name the
	// assembly gives an entry of this component.
	authored := mustCfnMap(t, outputs, "appXzhaXXdbXzuendpoint")
	authoredExport := mustCfnMap(t, authored, "Export")
	if got := authoredExport["Name"]; got != "platform-team--app-a--db-endpoint" {
		t.Fatalf("authored Export.Name = %#v, want the name the Recipe wrote", got)
	}

	// The link's own export is published alongside it, and it is the string the
	// shadowing fixture spells — the pin that keeps that fixture colliding.
	hostOut := mustCfnMap(t, outputs, "dbXXhost")
	derivedExport := mustCfnMap(t, hostOut, "Export")
	derivedName, ok := derivedExport["Name"].(string)
	if !ok {
		t.Fatalf("Outputs.DbHost.Export.Name is not a string; got %#v", derivedExport["Name"])
	}
	if derivedName != cfnDerivedDbHostExportName {
		t.Fatalf("the derived export name is now %q but the shadowing fixture spells %q; update the fixture and the constant together",
			derivedName, cfnDerivedDbHostExportName)
	}
	t.Logf("authored export %q and derived export %q both published", authoredExport["Name"], derivedName)
}

// TestCloudFormationAuthoredExport_TwoDifferingNamesShip builds the same
// workspace with two authored Outputs entries carrying two different Export
// names, and proves both ship.
//
// The two are chosen to sit as close to the derived name as a different string
// can. One is in the derived shape and names the database's `port` provides key,
// which no uses-edge injects across a boundary in this deployment — so no export
// is derived for it. That is the case a comparison built from a component's
// declared provides keys rather than from the keys an edge actually injects
// would refuse, and refusing it would reject a name that meets nothing in this
// build. The other differs from the derived name by one trailing character, so a
// comparison looser than string equality would refuse it.
func TestCloudFormationAuthoredExport_TwoDifferingNamesShip(t *testing.T) {
	in := cfnAuthoredExportInput(t, "authored-export-two-names")

	result := pipelinetest.Build(t, in)

	producer, producerRaw := findCfnStack(t, result.Artifacts, "stack-a")
	t.Logf("producer stack template (stack-a):\n%s", producerRaw)

	outputs := mustCfnMap(t, producer, "Outputs")

	for _, tc := range []struct {
		scenario   string
		outputName string
		wantExport string
	}{
		{
			scenario:   "a name in the derived shape for a provides key no edge injects",
			outputName: "appXzhaXXdbXzuportXzuendpoint",
			wantExport: "aggrega--cloudformation-two-boundary--dist-zsstack-a-zscloudformation--db--port",
		},
		{
			scenario:   "a one-character near miss of the derived name",
			outputName: "appXzhaXXdbXzuhostXzuendpoint",
			wantExport: "aggrega--cloudformation-two-boundary--dist-zsstack-a-zscloudformation--db--hosts",
		},
	} {
		entry := mustCfnMap(t, outputs, tc.outputName)
		export := mustCfnMap(t, entry, "Export")
		if got := export["Name"]; got != tc.wantExport {
			t.Fatalf("%s: Outputs.%s.Export.Name = %#v, want %q", tc.scenario, tc.outputName, got, tc.wantExport)
		}
	}

	// Both authored names are different from the derived one, and the derived
	// export is published as well.
	hostOut := mustCfnMap(t, outputs, "dbXXhost")
	derivedExport := mustCfnMap(t, hostOut, "Export")
	if got := derivedExport["Name"]; got != cfnDerivedDbHostExportName {
		t.Fatalf("Outputs.DbHost.Export.Name = %#v, want %q", got, cfnDerivedDbHostExportName)
	}
	t.Logf("two differing authored export names published alongside the derived %q", derivedExport["Name"])
}

// TestCloudFormationAuthoredExport_CollidingWithNothingDeterministic asserts the
// build carrying one authored Export name is byte-identical across repeated runs
// (the Deterministic Output invariant), matching the companion every other input
// of this fixture carries. The authored Export blocks merge into the same
// assembled Outputs section the projected provides entries and the link's own
// Export patch land in, which is the section this guards.
func TestCloudFormationAuthoredExport_CollidingWithNothingDeterministic(t *testing.T) {
	pipelinetest.AssertDeterministic(t, cfnAuthoredExportInput(t, "authored-export-inert"))
}

// TestCloudFormationAuthoredExport_TwoDifferingNamesDeterministic asserts the
// build carrying two authored Export names is byte-identical across repeated
// runs. Two entries rather than one is the case where an unordered walk over the
// authored Outputs would show, so it is the shape worth guarding twice over.
func TestCloudFormationAuthoredExport_TwoDifferingNamesDeterministic(t *testing.T) {
	pipelinetest.AssertDeterministic(t, cfnAuthoredExportInput(t, "authored-export-two-names"))
}
