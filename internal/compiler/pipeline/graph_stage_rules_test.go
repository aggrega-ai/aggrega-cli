// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package pipeline

import (
	"bytes"
	"context"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-abi-go/pkg/abi"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/analyzer"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/pluginpath"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
)

// The workspace the advisory proof compiles. It is a different one from the
// composed fixture above because no committed workspace produces the finding
// that rule reports: the shape needs an annotation contribution and an explicit
// one contesting one leaf, and the fixture below is the one carrying the
// annotation binding that makes such a contest expressible.
const (
	overrideAggregatePath = "../../../internal/compiler/testdata/fullstack-app/aggregates/annotation-values-binding-annotation.yaml"
	overrideReleasePath   = "../../../internal/compiler/testdata/fullstack-app/releases/annotation-values-binding-annotation.yaml"
	overrideManifestPath  = "../../../internal/compiler/testdata/fullstack-app/manifests/annotation-values-binding.yaml"
)

// The workspace the slot proof compiles. It is a third one because the slot axis
// enumerates what a component TYPE declares, and the composed workspace above
// happens to use component types that declare no attachment slot at all — so the
// axis answers nothing there, truthfully, and an assertion over it would pass on
// an axis that never worked.
const (
	slotAggregatePath = "../../../internal/compiler/testdata/feature-matrix/aggregate.yaml"
	slotReleasePath   = "../../../internal/compiler/testdata/feature-matrix/releases/helm-synthesized.yaml"
	slotManifestPath  = "../../../internal/compiler/testdata/feature-matrix/releases/manifest-helm-synthesized.yaml"
	slotCatalogPath   = "../../../internal/compiler/testdata/feature-matrix/catalog"
)

// workspaceFixture names the four documents a compilation is driven from.
//
// The catalog is named once: catalogPath for one directory, or catalogDirs for
// several loaded together, and compileWorkspace refuses a fixture setting both.
type workspaceFixture struct {
	aggregatePath string
	releasePath   string
	manifestPath  string
	catalogPath   string
	catalogDirs   []string
}

// compileWorkspace runs the real pre-render pipeline over one workspace and
// hands back what it produced, together with the catalog it compiled against —
// which the enumeration needs for the one axis the graph does not carry.
//
// The aggregate bytes are taken rather than read, so a proof needing a shape no
// committed document carries can rewrite them in memory and still compile
// everything downstream for real.
func compileWorkspace(
	t *testing.T,
	fixture workspaceFixture,
	aggregate []byte,
) (*stepsOneToFourOutput, resolver.CatalogLookup) {
	t.Helper()

	rel, err := resolver.LoadRelease(fixture.releasePath)
	if err != nil {
		t.Fatalf("load release: %v", err)
	}
	manifest, err := resolver.LoadManifest(fixture.manifestPath)
	if err != nil {
		t.Fatalf("load manifest: %v", err)
	}
	if fixture.catalogPath != "" && len(fixture.catalogDirs) > 0 {
		t.Fatal("the workspace fixture names its catalog twice: set catalogPath or catalogDirs, not both")
	}
	catalogDirs := []string{fixture.catalogPath}
	if len(fixture.catalogDirs) > 0 {
		catalogDirs = fixture.catalogDirs
	}
	cat, err := catalog.LoadAll("", catalogDirs...)
	if err != nil {
		t.Fatalf("catalog load: %v", err)
	}
	if extErrs := cat.ResolveAllExtends(); len(extErrs) > 0 {
		t.Fatalf("catalog extends: %d error(s); first: %v", len(extErrs), extErrs[0])
	}

	var captured *stepsOneToFourOutput
	runErr := runStepsOneToFour(
		context.Background(),
		Inputs{
			Release:           rel,
			Manifest:          manifest,
			AggregateRawBytes: aggregate,
			AggregatePath:     fixture.aggregatePath,
			// The Release's path is supplied because one enumerated axis is
			// authored there: an attachment's infra pillar. Leaving it out would
			// leave that axis unpositioned, so a proof over it would be measuring
			// the omission rather than the compiler.
			ReleasePath:     fixture.releasePath,
			WorkspaceConfig: defaultWorkspaceConfigForTest(),
			PluginPins:      pluginpath.NoPins(),
			CompilerVersion: invocationVersion,
		},
		cat.CatalogLookup(),
		cat.RecipeLookupWithManifest(manifest),
		cat.SubAggregateLoader(),
		func(out *stepsOneToFourOutput) error {
			captured = out
			return nil
		},
	)
	if runErr != nil {
		t.Fatalf("pre-render pipeline: %v", runErr)
	}
	if captured == nil {
		t.Fatal("pipeline completed without invoking the continuation")
	}
	return captured, cat.CatalogLookup()
}

// bytesOf reads a fixture document.
func bytesOf(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return b
}

// TestGraphStageEnumerationIsKeyedByTheCompositionsOwnIdentifiers proves the
// enumeration the production path builds is keyed the way the facts at this
// stage are keyed, over a workspace whose components do not all come from one
// document.
//
// The composed case is the one that matters and the only one that can show it.
// A component a module contributes carries the module's prefix in the flattened
// set and its own bare name in the file it was written in, so an enumeration
// keyed by what the author typed holds no entry for it at all — and a rule
// invoked over such an enumeration is simply never asked about it, which raises
// nothing and leaves the build green. A single-document workspace cannot tell
// the two keyings apart, because there the two are the same string.
func TestGraphStageEnumerationIsKeyedByTheCompositionsOwnIdentifiers(t *testing.T) {
	out := compileFixture(t, readAggregate(t))
	coordinates := coordinatesOf(t, out, fixtureCatalog(t))

	nodes := coordinates.Nodes()
	if len(nodes) == 0 {
		t.Fatal("the enumeration holds no nodes; every node-keyed rule would be invoked zero times")
	}

	const contributed = "data.main-db"
	index := slices.IndexFunc(nodes, func(n constraint.NodeInstance) bool { return n.ID == contributed })
	if index < 0 {
		ids := make([]string, 0, len(nodes))
		for _, n := range nodes {
			ids = append(ids, n.ID)
		}
		t.Fatalf("the enumeration holds no entry for the component the module contributed; it holds %v", ids)
	}

	node := nodes[index]
	// The identifier is what every fact at this stage takes, and the presentation
	// answering for it is the evidence that the two axes agree.
	if _, present := out.GraphView.FlattenedNodeComponentType(node.ID); !present {
		t.Errorf("the view answers nothing for the enumerated identifier %q; the enumeration and the facts are keyed differently", node.ID)
	}
	// The authored name is the other half and is deliberately not the identifier:
	// it is what the locator names, and it is the name in the module's own file.
	if node.AuthoredName != "main-db" {
		t.Errorf("authored name = %q, want %q — a locator built from the identifier would name a key the contributing file does not contain", node.AuthoredName, "main-db")
	}
	if node.SourceFile == "" {
		t.Errorf("node %q carries no source file; a finding about it could not be positioned", node.ID)
	}

	// Every node the graph holds is enumerated, so no rule is silently unasked
	// about one of them.
	if got, want := len(nodes), out.IRGraph.Spec.Nodes.Len(); got != want {
		t.Errorf("enumerated %d node(s), the graph holds %d", got, want)
	}

	// The edge axis is enumerated off the same graph and keyed the same way, so
	// every edge names a consumer the node axis also holds.
	edges := coordinates.Edges()
	if len(edges) == 0 {
		t.Fatal("the enumeration holds no edges; every edge-keyed rule would be invoked zero times")
	}
	for _, edge := range edges {
		if !slices.ContainsFunc(nodes, func(n constraint.NodeInstance) bool { return n.ID == edge.Node.ID }) {
			t.Errorf("edge on socket %q names consumer %q, which the node axis does not hold", edge.Socket, edge.Node.ID)
		}
	}
}

// TestGraphStageEnumerationAnswersTheAxesTheDeclaredRulesWalk proves each axis
// the rules over this stage are invoked once per answers from the compilation
// rather than emptily, and that the coordinates it hands back are ones the
// presentation can then be asked about.
//
// An axis answering emptily is the failure this is looking for: a rule invoked
// zero times reports exactly what a rule invoked and satisfied reports, so a
// green build says nothing about whether the axis worked.
func TestGraphStageEnumerationAnswersTheAxesTheDeclaredRulesWalk(t *testing.T) {
	out := compileFixture(t, readAggregate(t))
	coordinates := coordinatesOf(t, out, fixtureCatalog(t))

	var attachmentsSeen int
	for _, node := range coordinates.Nodes() {
		for _, attachment := range coordinates.Attachments(node.ID) {
			attachmentsSeen++
			// The coordinate the rules read a fact by, asked of the presentation
			// the same rules read: an attachment the enumeration names but the
			// presentation does not know would be a rule invoked about nothing.
			if !slices.Contains(out.GraphView.MergedAttachmentNames(node.ID), attachment) {
				t.Errorf("node %q: attachment %q is enumerated but the view does not hold it", node.ID, attachment)
			}
		}
	}
	if attachmentsSeen == 0 {
		t.Error("no attachment was enumerated across the whole composition; every attachment-keyed rule ran zero times")
	}

	// A node the graph does not hold answers empty on every keyed axis rather
	// than inventing coordinates for it.
	const absent = "no-such-node"
	if got := coordinates.Attachments(absent); len(got) != 0 {
		t.Errorf("attachments of an absent node = %v, want none", got)
	}
	if got := coordinates.Slots(absent); len(got) != 0 {
		t.Errorf("slots of an absent node = %v, want none", got)
	}
	if got := coordinates.AttachmentPillars(absent, "anything"); len(got) != 0 {
		t.Errorf("pillars of an absent node = %v, want none", got)
	}
	if got := coordinates.AttachmentPillarLeaves(absent, "anything", "values"); len(got) != 0 {
		t.Errorf("leaves of an absent node = %v, want none", got)
	}
}

// TestGraphStageEnumerationWalksTheSlotsAComponentTypeDeclares proves the slot
// axis over a workspace whose component types declare slots.
//
// It is a separate workspace deliberately. The axis is the set a component TYPE
// declares rather than the set a node ended up bound to, which is exactly what
// lets a rule be asked about a slot nothing binds — and no assertion over a
// workspace whose types declare no slot can tell a working axis from one that
// answers nothing.
func TestGraphStageEnumerationWalksTheSlotsAComponentTypeDeclares(t *testing.T) {
	fixture := workspaceFixture{
		aggregatePath: slotAggregatePath,
		releasePath:   slotReleasePath,
		manifestPath:  slotManifestPath,
		catalogPath:   slotCatalogPath,
	}
	out, catalogLookup := compileWorkspace(t, fixture, bytesOf(t, slotAggregatePath))
	coordinates := coordinatesOf(t, out, catalogLookup)

	declared := map[string][]string{}
	for _, node := range coordinates.Nodes() {
		if slots := coordinates.Slots(node.ID); len(slots) > 0 {
			declared[node.ID] = slots
		}
	}
	if len(declared) == 0 {
		t.Fatal("no attachment slot was enumerated; every slot-keyed rule ran zero times over a workspace whose component types declare slots")
	}

	// A slot the type declares is enumerated whether or not anything binds it,
	// which is the property the rules over an unbound slot depend on.
	for node, slots := range declared {
		bound := out.GraphView.MergedAttachmentNames(node)
		var unbound int
		for _, slot := range slots {
			if !slices.Contains(bound, slot) {
				unbound++
			}
		}
		if unbound == 0 {
			continue
		}
		// Reached: the axis presented a slot nothing binds, which is the case a
		// rule over an unbound required slot exists to catch.
		return
	}
	t.Error("every enumerated slot happened to be bound, so the axis was never shown presenting the unbound case the rules over it exist for")
}

// giveTheRoutingAttachmentAValuesPillar rewrites the fixture so the attachment
// the Release already configures also carries a developer-authored values
// leaf.
//
// The pairing is what the proof needs and no committed workspace carries it: the
// Release's own document already attaches this feature with an infra pillar,
// and the rewrite adds the other half in the Aggregate document, so one
// attachment ends up carrying one pillar from each of the two authoring
// surfaces. The rewrite is in memory, because the fixture on disk is shared with
// other proofs and must keep producing exactly what they expect.
func giveTheRoutingAttachmentAValuesPillar(t *testing.T, aggregate []byte) []byte {
	t.Helper()
	const authored = "    frontend:\n" +
		"      type: core/compute/web-ui\n" +
		"      uses:\n"
	const withValues = "    frontend:\n" +
		"      type: core/compute/web-ui\n" +
		"      features:\n" +
		"        cross-namespace-routing:\n" +
		"          type: core/k8s-dns-resolver\n" +
		"          values:\n" +
		"            resolver_mode: \"cluster-local\"\n" +
		"      uses:\n"
	if !bytes.Contains(aggregate, []byte(authored)) {
		t.Fatalf("fixture no longer declares %q the way the rewrite expects; the rewrite would be silently inert", "frontend")
	}
	return bytes.Replace(aggregate, []byte(authored), []byte(withValues), 1)
}

// pillarLeafPosition is one invocation of the attachment-pillar-leaf traversal,
// reduced to the three things this proof is about.
type pillarLeafPosition struct {
	pillar     string
	sourceFile string
	locator    string
}

// TestEachAttachmentPillarNamesItsOwnAuthoringDocument is the proof that an
// attachment's two configuration pillars are positioned in the two documents
// they are actually written in.
//
// It is driven over a real compilation and through the framework's own
// traversal, so what is asserted is the provenance a rule would receive rather
// than an enumerator's return value on its own.
//
// The two-document pairing is the whole of it. A developer writes an
// attachment's values in the Aggregate document; an operator writes its
// infrastructure configuration on the Release, which is the only authoring
// surface whose attachment block admits one. A leaf of the second positioned in
// the first names a key that document cannot contain, so an editor following the
// position lands nowhere — and a single-document fixture cannot see that,
// because there the two answers are the same string.
func TestEachAttachmentPillarNamesItsOwnAuthoringDocument(t *testing.T) {
	fixture := workspaceFixture{
		aggregatePath: invocationAggregatePath,
		releasePath:   invocationReleasePath,
		manifestPath:  invocationManifestPath,
		catalogPath:   invocationCatalogPath,
	}
	out, catalogLookup := compileWorkspace(t, fixture, giveTheRoutingAttachmentAValuesPillar(t, readAggregate(t)))
	coordinates := coordinatesOf(t, out, catalogLookup)

	const (
		node       = "frontend"
		attachment = "cross-namespace-routing"
	)

	var seen []pillarLeafPosition
	_, err := constraint.RunOnNodeAttachmentPillarLeaves(
		nil,
		singleNode(coordinates, node),
		coordinates,
		coordinates,
		coordinates,
		out.GraphView,
		constraint.Check[constraint.GraphView, constraint.NodeAttachmentPillarLeafSubject]{
			Sentinel: analyzer.ErrOverriddenAnnotation(),
			Severity: constraint.SeverityWarning,
			Run: func(_ constraint.GraphView, s constraint.NodeAttachmentPillarLeafSubject) []constraint.Objection {
				if s.AttachmentName() != attachment {
					return nil
				}
				seen = append(seen, pillarLeafPosition{
					pillar:     s.PillarName(),
					sourceFile: s.SourceFile(),
					locator:    s.Locator(),
				})
				return nil
			},
		},
	)
	if err != nil {
		t.Fatalf("the traversal returned %v over a walk nothing cancelled", err)
	}

	// The two paths differ as well as the two files, and that is the same
	// property rather than a second one. Each pillar is written in the document
	// that admits it, and the two documents nest their components differently:
	// the Aggregate writes this one at its own top level, while the Release
	// routes it into a boundary. So the position of each is the shape its own
	// document has, and a single shape spelled for both would be wrong for one of
	// them wherever the two documents disagree — which is most builds.
	want := []pillarLeafPosition{
		{
			pillar:     "values",
			sourceFile: fixture.aggregatePath,
			locator:    ".spec.components.frontend.features.cross-namespace-routing.values.resolver_mode",
		},
		{
			pillar:     "infra",
			sourceFile: fixture.releasePath,
			locator: ".spec.boundaries.frontend-ns.components.frontend" +
				".features.cross-namespace-routing.infra.target_var",
		},
	}
	if len(seen) != len(want) {
		t.Fatalf("the traversal was invoked %d time(s) for attachment %q; want %d.\n"+
			"CONSEQUENCE: the pairing this proof rests on is one pillar from each authoring "+
			"surface, so a different count measures a different shape.\ngot: %+v", len(seen), attachment, len(want), seen)
	}
	for i := range want {
		if seen[i] != want[i] {
			t.Errorf("invocation %d carried %+v; want %+v.\n"+
				"INVARIANT: a pillar's leaf is positioned in the document that pillar was authored in.\n"+
				"CONSEQUENCE: an infrastructure leaf attributed to the Aggregate document names a key "+
				"that document's attachment block cannot hold, so the position resolves nowhere.",
				i, seen[i], want[i])
		}
	}

	// The two files being different is the property, stated separately so it
	// cannot be satisfied by both halves drifting onto one document together.
	if seen[0].sourceFile == seen[1].sourceFile {
		t.Errorf("both pillars were positioned in %q; the two are authored in different documents "+
			"and one of the two answers is therefore wrong", seen[0].sourceFile)
	}
}

// contestOneLeafBetweenAnAnnotationAndAnExplicitValue rewrites the fixture so an
// annotation contributes an attachment to the component while an explicit block
// at the scope above contests one of its leaves.
//
// The contest has to be authored this way round. An explicit contribution that
// lands first suppresses the annotation silently and records nothing, by design,
// so the reverse authoring produces no finding at all — the shape below is the
// one the rule exists to report. The rewrite is in memory: the fixture on disk
// is shared with other proofs and must keep producing exactly what they expect.
func contestOneLeafBetweenAnAnnotationAndAnExplicitValue(t *testing.T, aggregate []byte) []byte {
	t.Helper()
	const authored = "    data:\n" +
		"      annotations:\n" +
		"        db-pool: \"values-only-db\"\n" +
		"      components:\n" +
		"        dbclient:\n" +
		"          type: core/compute/web-ui\n"
	const contested = "    data:\n" +
		"      features:\n" +
		"        db-pool:\n" +
		"          type: feature.myco/db-pool\n" +
		"          values:\n" +
		"            db_name: \"explicitly-authored-db\"\n" +
		"      components:\n" +
		"        dbclient:\n" +
		"          type: core/compute/web-ui\n" +
		"          annotations:\n" +
		"            db-pool: \"annotation-supplied-db\"\n"
	if !bytes.Contains(aggregate, []byte(authored)) {
		t.Fatalf("fixture no longer carries the scope-level %q annotation; the rewrite would be silently inert", "db-pool")
	}
	return bytes.Replace(aggregate, []byte(authored), []byte(contested), 1)
}

// compileContestedOverride compiles the contested workspace through the real
// pre-render pipeline and hands back what it produced.
func compileContestedOverride(t *testing.T) *stepsOneToFourOutput {
	t.Helper()
	fixture := workspaceFixture{
		aggregatePath: overrideAggregatePath,
		releasePath:   overrideReleasePath,
		manifestPath:  overrideManifestPath,
		catalogPath:   invocationCatalogPath,
	}
	out, _ := compileWorkspace(t, fixture, contestOneLeafBetweenAnAnnotationAndAnExplicitValue(t, bytesOf(t, overrideAggregatePath)))
	return out
}

// TestAnnotationOverrideAdvisoryRaisesOverAContestedWorkspace drives the declared
// rule over one real compilation of one workspace, which is what this proof adds
// to the rule's own unit proof: that one builds the axis and the per-leaf
// provenance by hand, so it cannot say whether the compilation presents the rule
// anything to read.
//
// The first assertion is that the rule RAISED. It is written first and as a
// Fatalf because a rule that never fires reads exactly like a rule that ran and
// approved, and every assertion below it would pass over an empty slice.
func TestAnnotationOverrideAdvisoryRaisesOverAContestedWorkspace(t *testing.T) {
	out := compileContestedOverride(t)

	registered := analyzer.ErrOverriddenAnnotation().Error()
	var raised []abi.Diagnostic
	for _, reported := range out.Analysis.Warnings {
		if strings.Contains(reported.Message, registered) {
			raised = append(raised, reported)
		}
	}
	if len(raised) == 0 {
		t.Fatalf("the contested workspace produced no finding carrying the text registered "+
			"against the rule; it reported %v", out.Analysis.Warnings)
	}

	// What the author has to act on: which annotation was displaced, and where
	// that annotation was written. A finding that were only the registered text
	// would read identically for every displacement in every workspace.
	for _, reported := range raised {
		for _, want := range []string{`"db-pool"`, overrideAggregatePath} {
			if !strings.Contains(reported.Message, want) {
				t.Errorf("the finding does not name %s: %q", want, reported.Message)
			}
		}
	}

	// And the same position as a field, which is what a surface holding the parsed
	// document navigates by. The sentence above carries it for a reader; this
	// carries it for a consumer, and a whole real compilation is the only place
	// both can be shown to survive together.
	for _, reported := range raised {
		if reported.Location == nil || reported.Location.KeyPath == "" {
			t.Errorf("the finding carries no authoring key as a field, so a consumer can place it no closer "+
				"than the document itself while the message names the key: %q", reported.Message)
			continue
		}
		if !strings.Contains(reported.Message, reported.Location.KeyPath) {
			t.Errorf("the field position %q and the displayed position disagree: %q",
				reported.Location.KeyPath, reported.Message)
		}
	}

	// An advisory never reaches the slice the build gates on. This is the half of
	// the replacement that must not change: the workspace above renders, and a
	// finding routed to the wrong stream would refuse it.
	if len(out.Analysis.Errors) != 0 {
		t.Errorf("an advisory reached the halting stream: %d error(s)", len(out.Analysis.Errors))
	}
}

// TestAnnotationOverrideAdvisorySaysNothingOnTheUncontestedWorkspace pins the
// other direction: the fixture as authored has no contest, and the build reports
// nothing about one.
//
// It is the calibration for the test above rather than a second scenario. Every
// assertion there is about what the rule says when it fires; without a workspace
// the rule stays silent on, a rule firing at every leaf would satisfy all of
// them and flood a build with advisories about values nobody displaced.
func TestAnnotationOverrideAdvisorySaysNothingOnTheUncontestedWorkspace(t *testing.T) {
	out := compileFixture(t, readAggregate(t))
	for _, reported := range out.Analysis.Warnings {
		if strings.Contains(reported.Message, analyzer.ErrOverriddenAnnotation().Error()) {
			t.Errorf("the rule reported a displacement on a workspace that authors none: %q", reported.Message)
		}
	}
}
