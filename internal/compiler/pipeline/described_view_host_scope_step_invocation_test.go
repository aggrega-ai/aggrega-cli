// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package pipeline

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer/enginefactory"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
	"github.com/aggrega-ai/aggrega-cli/internal/wasmtest"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/pluginpath"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// The proofs in this file are about the one condition the pipeline-step proofs
// beside them cannot reach: a step that addresses the documents of the component
// its attachment is attached to, rather than the attachment's own.
//
// A step declares which document root it operates on in its plugin's own
// self-report, not in the entity that declares the step — so which provider the
// step is matched against is decided by that self-report. A step addressing the
// attachment's own documents is matched against the provider the attachment's
// FeatureRecipe declares; a step addressing the host component's documents is
// matched against the provider the host component's own Recipe declares. The
// second is a different fact about a different authored entity, and the proofs
// beside this file cannot exercise it: every step their workspace declares
// addresses its own documents, and one of them asserts so explicitly.
//
// # Why this workspace and not the one beside it
//
// The step plugin here declares one compatible provider — the one that emits
// chart source — and it addresses the host component's documents. So the host
// component's Recipe has to declare that same provider for the build to be
// admitted at all, which is what makes this workspace the one that can be asked.
//
// # Two deployments, and why each verdict is attributable
//
// The workspace is compiled twice, under two deployments that differ in exactly
// one binding: which Recipe the host component binds, and therefore which
// provider renders the host component's own documents. Nothing else moves — the
// Aggregate, the attachment, its FeatureRecipe and the step's plugin are the same
// documents in both.
//
// The first binds the provider the step's plugin declares, so the step is
// compatible and the build is admitted. The attachment's own provider is a
// DIFFERENT value and is not one the plugin declares, so a reading that matched
// the step against the attachment's provider would have refused this build — the
// admission therefore follows from reading the host component's provider and from
// nothing else.
//
// The second binds a THIRD provider, which the plugin does not declare either. So
// both readings refuse it, and what separates them is the provider the finding
// NAMES: the host component's, not the attachment's. That is the sharper of the
// two directions, because it cannot be satisfied by a rule that raises for any
// reason at all.
//
// A workspace whose two providers coincided would pass under both readings, and a
// single deployment would leave one of the two directions unexercised.
const (
	hostScopeAggregatePath = "../../../internal/compiler/testdata/host-patch-helmchart/aggregate.yaml"
	hostScopeCatalogPath   = "../../../internal/compiler/testdata/host-patch-helmchart/catalog"

	// The deployment whose host component renders through the provider the step
	// plugin declares. Its build is admitted.
	hostScopeChartManifestPath = "../../../internal/compiler/testdata/host-patch-helmchart/manifest.yaml"
	hostScopeChartReleasePath  = "../../../internal/compiler/testdata/host-patch-helmchart/release.yaml"

	// The deployment whose host component renders through a third provider the
	// step's plugin does not declare. Its build halts, and the finding names that
	// provider rather than the attachment's own.
	hostScopeOtherManifestPath = "../../../internal/compiler/testdata/host-patch-helmchart/manifest-synthesized-host.yaml"
	hostScopeOtherReleasePath  = "../../../internal/compiler/testdata/host-patch-helmchart/release-synthesized-host.yaml"

	// The step plugin both deployments bind, and the artifact behind it. Its
	// self-report declares the host document root and one compatible provider.
	hostScopePluginURI      = "pipeline-step/host-patch:1.0.0"
	hostScopePluginArtifact = "frp-step-host-patch-rust.wasm"

	// The coordinates the authored documents carry. Spelled here so a workspace
	// that stopped carrying them fails loudly rather than leaving a proof
	// asserting nothing.
	hostScopeNode       = "api"
	hostScopeAttachment = "guardrail"
	hostScopeStep       = "inject_into_host"
)

// hostScopeBuild is one compiled workspace together with everything a rule over
// its pipeline steps needs, and the render's own error rather than a failure —
// so a proof about a build the render refuses can read that refusal.
type hostScopeBuild struct {
	out          *stepsOneToFourOutput
	recipeLookup renderer.RecipeLookup
	result       *renderer.RenderResult
	renderErr    error
	coordinates  renderer.GraphCoordinates
	axes         stepAxes
}

// buildHostScopeFixture compiles the host-patch workspace under one deployment
// and renders it the way the production spine renders: through the same hybrid
// engine factory, with the plugin resolution wired, and with the enumeration the
// finalised graph carries handed to the render.
//
// That last part is what makes these builds real rather than adjacent to real.
// The render invokes the rules declared over what its plugins report about
// themselves only when it is given that enumeration, so a render without it
// asks nothing and a refusal could only be observed by re-asking beside it.
// With it, the halt a refused build returns IS the rule's verdict.
func buildHostScopeFixture(t *testing.T, manifestPath, releasePath string) hostScopeBuild {
	t.Helper()

	catalogPath := stageColocatedStepPlugin(
		t, hostScopeCatalogPath, hostScopePluginURI, hostScopePluginArtifact,
	)
	out, catalogLookup := compileWorkspace(t, workspaceFixture{
		aggregatePath: hostScopeAggregatePath,
		releasePath:   releasePath,
		manifestPath:  manifestPath,
		catalogPath:   catalogPath,
	}, bytesOf(t, hostScopeAggregatePath))
	if out.GraphCoordinates == nil {
		t.Fatal("the compilation produced no enumeration of the finalised graph; the render would invoke no rule over it")
	}

	manifest, err := resolver.LoadManifest(manifestPath)
	if err != nil {
		t.Fatalf("load manifest: %v", err)
	}
	cat, err := catalog.LoadAll("", catalogPath)
	if err != nil {
		t.Fatalf("catalog load: %v", err)
	}
	if extErrs := cat.ResolveAllExtends(); len(extErrs) > 0 {
		t.Fatalf("catalog extends: %d error(s); first: %v", len(extErrs), extErrs[0])
	}
	recipeLookup := cat.RecipeLookupWithManifest(manifest)
	lookup := buildEntityPluginLookup(
		Inputs{WorkspaceConfig: workspaceconfig.Default()}, catalogLookup,
	)

	// The provider dispatch the build itself uses, rather than a stand-in. The
	// host component here is rendered from chart source, so a stand-in engine
	// would decide the outcome of the walk that follows the rules instead of the
	// provider the deployment named.
	ctx := context.Background()
	sharedLoader := wasm.NewCachingLoaderFromContext(ctx, wasm.ExternalInputsFromContext(ctx))
	factory, providerVersion, closeEngines := enginefactory.NewHybrid(catalogLookup, lookup, sharedLoader)
	t.Cleanup(func() { _ = closeEngines(ctx) })

	result, renderErr := renderer.RenderAll(ctx, renderer.RendererInput{
		Graph:            out.IRGraph,
		GraphView:        out.GraphView,
		GraphCoordinates: out.GraphCoordinates,
		FeatureOrder:     out.FeatureOrder,
		ResolveResult:    out.ResolveResult,
		CatalogLookup:    catalogLookup,
		PluginPathLookup: lookup,
		ProviderVersion:  providerVersion,
	}, recipeLookup, factory)

	var release *types.Release
	if out.ResolveResult != nil {
		release = out.ResolveResult.Release
	}
	return hostScopeBuild{
		out:          out,
		recipeLookup: recipeLookup,
		result:       result,
		renderErr:    renderErr,
		coordinates:  out.GraphCoordinates,
		axes: stepAxes{
			graph:        out.IRGraph,
			recipeLookup: recipeLookup,
			release:      release,
			releaseFile:  releasePath,
		},
	}
}

// stageColocatedStepPlugin copies a catalog into a directory the calling test
// owns and writes one step plugin's artifact beside its own Plugin entity, which
// is how the resolution cascade finds a binary the repository does not ship
// inside the catalog. It returns the copied catalog's path.
//
// It copies rather than writing into the catalog on disk because that catalog is
// read by every proof over it and has to keep producing what each expects.
func stageColocatedStepPlugin(t *testing.T, catalogPath, pluginURI, artifactFixture string) string {
	t.Helper()
	staged := filepath.Join(t.TempDir(), "catalog")
	copyDir(t, catalogPath, staged)

	cat, err := catalog.LoadAll("", staged)
	if err != nil {
		t.Fatalf("load the copied catalog: %v", err)
	}
	// Both halves of the staged path come from the artifact reference, which is
	// the key the cascade resolves by. Deriving the directory from the reference
	// and the filename from the URI would stage the artifact under a name the
	// resolver never looks for whenever the two differ — as they do here, the URI
	// carrying a version the reference does not.
	dirs := cat.PluginColocatedDirs()
	reference := artifactReferenceOf(t, cat, pluginURI)
	artifact := pluginpath.PluginLeaf(reference)
	dir, held := dirs[reference]
	if !held {
		t.Fatalf("the copied catalog indexes no plugin entity for %q (indexed: %v)", pluginURI, dirs)
	}
	if err := os.WriteFile(
		filepath.Join(dir, artifact), wasmtest.EnsureFixtureBytes(t, artifactFixture), 0o644,
	); err != nil {
		t.Fatalf("stage %q beside its entity: %v", artifactFixture, err)
	}
	return staged
}

// hostScopeStepCoordinates returns every attachment pipeline step the compiled
// workspace declares, as node, attachment and step identifiers.
//
// It walks the same two axes the rule is invoked over — the node and attachment
// enumeration the compilation produced, and the step axis read from the bound
// FeatureRecipe — so what it reports is what the rule is asked about and not a
// second account of the workspace.
func hostScopeStepCoordinates(build hostScopeBuild) [][3]string {
	var out [][3]string
	for _, node := range build.coordinates.Nodes() {
		for _, attachment := range build.coordinates.Attachments(node.ID) {
			for _, step := range build.axes.AttachmentPipelineSteps(node.ID, attachment) {
				out = append(out, [3]string{node.ID, attachment, step.Value})
			}
		}
	}
	return out
}

// TestTheAttachmentPipelineStepRuleAdmitsAHostAddressedStepOnACompiledBuild
// invokes the rule over an attachment pipeline step that addresses the host
// component's documents, on a build the render completed, and reads its verdict
// from the presentation that build published.
//
// # What the named assertion below is for
//
// An admission is what a rule reports when it approved, and it is also what a
// rule reports when it was asked about nothing. An enumeration that presented no
// step, or presented one whose plugin addresses its own documents, would produce
// the same empty result as a satisfied rule, and no comparison of verdicts can
// separate the two. So the first sub-test states what the exercise must CONTAIN
// and fails when it stops containing it — the enumerated coordinate, the document
// root its plugin declares, and the two provider values that make the verdict
// attributable.
func TestTheAttachmentPipelineStepRuleAdmitsAHostAddressedStepOnACompiledBuild(t *testing.T) {
	build := buildHostScopeFixture(t, hostScopeChartManifestPath, hostScopeChartReleasePath)
	if build.renderErr != nil {
		t.Fatalf("the render refused a build this proof needs completed: %+v", build.renderErr)
	}
	if build.result == nil || build.result.DescribedView == nil {
		t.Fatal("the render published no presentation of what its plugins report; nothing can be invoked through it")
	}
	view := build.result.DescribedView

	t.Run("the exercise contains a host-addressed step whose two providers differ", func(t *testing.T) {
		// The enumeration, stated as a set rather than a count: a count is
		// satisfied by the wrong coordinate as readily as the right one.
		enumerated := hostScopeStepCoordinates(build)
		want := [][3]string{{hostScopeNode, hostScopeAttachment, hostScopeStep}}
		if !slices.Equal(enumerated, want) {
			t.Fatalf("the workspace's attachment pipeline steps enumerate as %v; want %v", enumerated, want)
		}

		// The document root the step's plugin declares. This is the fact that
		// selects which provider the step is matched against, so a step that
		// stopped declaring the host root would leave this whole file exercising
		// the reading its siblings already cover.
		scope := view.AttachmentStepExportScope(hostScopeNode, hostScopeAttachment, hostScopeStep)
		if scope != types.ScopeHost {
			t.Fatalf("the step's plugin declares the %q document root; want %q — the host documents",
				scope, types.ScopeHost)
		}

		// The attachment generates per component, so the rule does not end on it
		// before reading anything. An attachment generating once per boundary is
		// answered by the rule rooted at a boundary instead.
		if got := view.AttachmentExecutionScope(hostScopeNode, hostScopeAttachment); got == "boundary" {
			t.Fatalf("the attachment generates once per boundary; the rule ends on such an attachment and would report nothing")
		}

		hostProvider := view.NodeRenderingProvider(hostScopeNode)
		ownProvider := view.AttachmentRenderingProvider(hostScopeNode, hostScopeAttachment)
		declared := view.AttachmentStepPluginCompatibleCapabilities(
			hostScopeNode, hostScopeAttachment, hostScopeStep,
		)

		// A host component with no bound Recipe renders no host documents, and the
		// rule ends there too — so an empty value here would make the admission
		// below unattributable.
		if hostProvider == "" {
			t.Fatal("the host component reports no provider; the rule ends on that and the admission would certify nothing")
		}
		if len(declared) == 0 {
			t.Fatal("the step's plugin reports no compatible provider; every match would refuse and the admission below could not happen")
		}

		// The two halves of attribution. The host component's provider is in the
		// plugin's declared set, so matching against it admits; the attachment's
		// own provider is NOT, so matching against that would refuse. The
		// admission in the sub-test below therefore follows from reading the host
		// component's provider and could follow from nothing else.
		if !slices.Contains(declared, hostProvider) {
			t.Fatalf("the step's plugin declares %v, which does not include the host component's provider %q; the build could not be admitted",
				declared, hostProvider)
		}
		if ownProvider == hostProvider {
			t.Fatalf("the host component and the attachment both render through %q; the two readings cannot be told apart on this workspace",
				hostProvider)
		}
		if slices.Contains(declared, ownProvider) {
			t.Fatalf("the step's plugin declares %v, which includes the attachment's own provider %q; an admission would not be attributable",
				declared, ownProvider)
		}

		t.Logf("one host-addressed step at %v: the host component renders through %q (declared by the plugin) "+
			"and the attachment through %q (not declared)", want[0], hostProvider, ownProvider)
	})

	t.Run("the rule admits it over the presentation the render published", func(t *testing.T) {
		objections, err := constraint.RunOnNodeAttachmentPipelineSteps(
			nil, build.coordinates, build.coordinates, build.axes, view,
			renderer.AttachmentPipelineStepProviderCompatible,
		)
		if err != nil {
			t.Fatalf("traversal: %v", err)
		}
		if len(objections) != 0 {
			t.Fatalf("rule raised %d objection(s) over a build the render admitted: %v", len(objections), objections)
		}
		t.Logf("rule invoked over %d attachment step(s) of the compiled workspace; the host-addressed one admitted",
			len(hostScopeStepCoordinates(build)))
	})
}

// TestTheAttachmentPipelineStepRuleRefusesAHostAddressedStepOnACompiledBuild
// reads the halt a real build returns when the host component renders through a
// provider the step's plugin does not declare.
//
// # Nothing is substituted here, and that is the point
//
// The refusal its sibling reading needs is one no completed render can show: a
// build carrying it halts, so it publishes no presentation to ask a rule about,
// and the proofs over an attachment's own documents therefore substitute one
// provider value into the presentation a different build published. This one
// needs no substitution at all. The rule runs inside the render, ahead of the
// node walk, so the build's own halt IS the verdict — read from a compilation of
// authored documents with every fact the build itself produced.
//
// # Why the finding is attributable, and why it is not the halt itself
//
// The step's plugin declares neither of this deployment's two providers, so BOTH
// readings refuse it and the existence of a halt separates nothing. What separates
// them is the provider the finding names. A reading matching the step against the
// attachment's own provider names that one; this one names the host component's,
// and the assertions below require exactly that and reject the other.
//
// That is deliberately the stronger direction: a rule that raised for any reason
// at all would satisfy "the build halts", and could not satisfy "the finding names
// this provider and not that one".
func TestTheAttachmentPipelineStepRuleRefusesAHostAddressedStepOnACompiledBuild(t *testing.T) {
	build := buildHostScopeFixture(t, hostScopeOtherManifestPath, hostScopeOtherReleasePath)

	recipe := recipeOfNode(build.out.IRGraph, build.recipeLookup, hostScopeNode)
	if recipe == nil {
		t.Fatalf("the compilation bound no Recipe to %q; the host component would render no documents", hostScopeNode)
	}
	fr, held := build.axes.attachmentFeatureRecipe(hostScopeNode, hostScopeAttachment)
	if !held || fr == nil {
		t.Fatalf("the compilation bound no FeatureRecipe to attachment %q on %q", hostScopeAttachment, hostScopeNode)
	}
	hostProvider := renderer.ProviderEngineKind(recipe.Spec.Capability)
	ownProvider := renderer.ProviderEngineKind(fr.Spec.Capability)

	t.Run("the exercise contains a host-addressed step whose two providers are distinct", func(t *testing.T) {
		enumerated := hostScopeStepCoordinates(build)
		want := [][3]string{{hostScopeNode, hostScopeAttachment, hostScopeStep}}
		if !slices.Equal(enumerated, want) {
			t.Fatalf("the workspace's attachment pipeline steps enumerate as %v; want %v", enumerated, want)
		}
		if hostProvider == "" {
			t.Fatal("the host component reports no provider; the rule ends on that and would raise nothing")
		}
		if ownProvider == hostProvider {
			t.Fatalf("the host component and the attachment both render through %q; a finding naming it would not say which reading produced it",
				hostProvider)
		}
		t.Logf("one host-addressed step at %v: the host component renders through %q and the attachment through %q",
			want[0], hostProvider, ownProvider)
	})

	t.Run("the build halts naming the host component's provider and not the attachment's", func(t *testing.T) {
		if build.renderErr == nil {
			t.Fatal("the render admitted a host-addressed step matched against a provider its plugin does not declare")
		}
		if !errors.Is(build.renderErr, renderer.ErrPluginProviderIncompatible()) {
			t.Fatalf("the halt does not carry the incompatibility condition: %+v", build.renderErr)
		}

		// Read structurally rather than by searching the rendered text: a
		// categorised diagnostic renders one frame in its own Error, so a string
		// search over the halt would report nothing whichever shape the chain has.
		var finding *cerrors.CompilerError
		if !errors.As(build.renderErr, &finding) {
			t.Fatalf("the halt is not a categorised diagnostic: %+v", build.renderErr)
		}

		// Where an author fixes it: the authored step inside the entity that
		// declares it, which is neither the document the attachment was written in
		// nor the one the host component's provider is named in.
		if finding.Source.File != fr.Metadata.Name {
			t.Errorf("finding positioned in %q; want the entity that declares the step, %q",
				finding.Source.File, fr.Metadata.Name)
		}
		if want := ".spec.pipeline." + hostScopeStep; finding.Source.Locator != want {
			t.Errorf("finding positioned at %q; want the authored step %q", finding.Source.Locator, want)
		}

		// The attribution. Both providers are ones the step's plugin does not
		// declare, so both readings refuse — and the finding names the host
		// component's rather than the attachment's, which is what says which
		// documents' provider the step was matched against.
		if !strings.Contains(finding.Message, `"`+hostProvider+`"`) {
			t.Errorf("finding %q does not name the host component's provider %q", finding.Message, hostProvider)
		}
		if strings.Contains(finding.Message, `"`+ownProvider+`"`) {
			t.Errorf("finding %q names the attachment's own provider %q; it was matched against the wrong documents' provider",
				finding.Message, ownProvider)
		}
		if !strings.Contains(finding.Message, `"`+hostScopeAttachment+`"`) {
			t.Errorf("finding %q does not name the attachment that bound the pipeline", finding.Message)
		}

		t.Logf("the build halted at %q in %q with %q", finding.Source.Locator, finding.Source.File, finding.Message)
	})
}
