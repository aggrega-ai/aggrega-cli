// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package constraint

import (
	"os"
	"strings"
	"testing"
)

// The traversal tests.
//
// Three properties are worth pinning and one shape is worth demonstrating.
//
// The properties: a traversal invokes the check exactly once per instance; it
// preserves the order its enumerators returned rather than imposing one of its
// own; and it assembles the subject's provenance itself, so a check receives a
// file and a locator it never built.
//
// The shape: every traversal is exercised through the same table, and the table
// is checked against the generated kind set rather than trusted — a hand-written
// table sitting beside a generated set it is meant to mirror is exactly the
// arrangement that reads as complete while missing one, and no count written here
// would make the mirror true, because the count is the thing that goes stale.

// traversalView is the view the tests declare their checks over. Its content is
// irrelevant: what is under test is the traversal, and a check's access to facts
// is the accessor layer's concern.
type traversalView struct{}

// traversalFixture implements every enumerator over one small authored shape.
//
// Its per-axis storage is keyed by the outer coordinates joined with a separator,
// and it is indexed rather than ranged — the order a traversal observes comes
// from the slices, never from a map walk.
//
// The inner orders are deliberately not alphabetical. A traversal that sorted
// what it enumerated would produce a different sequence, and the expected
// sequences below would catch it; alphabetical fixtures would not.
type traversalFixture struct {
	nodes      []NodeInstance
	edges      []EdgeInstance
	boundaries []AuthoredCoordinate
	subsystems []AuthoredCoordinate

	sockets           map[string][]string
	attachments       map[string][]string
	attachmentSockets map[string][]string
	slots             map[string][]string
	mechanisms        map[string][]string
	deliveryKeys      map[string][]string
	nodePillars       map[string][]string
	nodePillarLeaves  map[string][]string
	parameters        map[string][]string
	attachPillars     map[string][]string
	attachLeaves      map[string][]string
	providers         map[string][]string
	emittedPaths      map[string][]string
	boundaryAttach    map[string][]string
	subsystemAttach   map[string][]string

	locals    map[string][]AuthoredCoordinate
	provides  map[string][]AuthoredCoordinate
	pipeline  map[string][]AuthoredCoordinate
	artifacts map[string][]AuthoredCoordinate

	attachPipeline         map[string][]AuthoredCoordinate
	boundaryAttachPipeline map[string][]AuthoredCoordinate
}

func fixtureKey(parts ...string) string { return strings.Join(parts, "\x00") }

func (f traversalFixture) Nodes() []NodeInstance            { return f.nodes }
func (f traversalFixture) Edges() []EdgeInstance            { return f.edges }
func (f traversalFixture) Boundaries() []AuthoredCoordinate { return f.boundaries }
func (f traversalFixture) Subsystems() []AuthoredCoordinate { return f.subsystems }

func (f traversalFixture) NodeSockets(node string) []string { return f.sockets[fixtureKey(node)] }
func (f traversalFixture) Attachments(node string) []string { return f.attachments[fixtureKey(node)] }
func (f traversalFixture) Slots(node string) []string       { return f.slots[fixtureKey(node)] }
func (f traversalFixture) Mechanisms(node string) []string  { return f.mechanisms[fixtureKey(node)] }
func (f traversalFixture) NodePillars(node string) []string { return f.nodePillars[fixtureKey(node)] }
func (f traversalFixture) Parameters(node string) []string  { return f.parameters[fixtureKey(node)] }

func (f traversalFixture) AttachmentSockets(node, attachment string) []string {
	return f.attachmentSockets[fixtureKey(node, attachment)]
}

func (f traversalFixture) DeliveryKeys(node, mechanism string) []string {
	return f.deliveryKeys[fixtureKey(node, mechanism)]
}

func (f traversalFixture) NodePillarLeaves(node, pillar string) []string {
	return f.nodePillarLeaves[fixtureKey(node, pillar)]
}

func (f traversalFixture) AttachmentPillars(node, attachment string) []string {
	return f.attachPillars[fixtureKey(node, attachment)]
}

func (f traversalFixture) AttachmentPillarLeaves(node, attachment, pillar string) []string {
	return f.attachLeaves[fixtureKey(node, attachment, pillar)]
}

func (f traversalFixture) LocalsKeys(node string) []AuthoredCoordinate {
	return f.locals[fixtureKey(node)]
}

func (f traversalFixture) ProvidesKeys(node string) []AuthoredCoordinate {
	return f.provides[fixtureKey(node)]
}

func (f traversalFixture) PipelineSteps(node string) []AuthoredCoordinate {
	return f.pipeline[fixtureKey(node)]
}

func (f traversalFixture) Artifacts(node string) []AuthoredCoordinate {
	return f.artifacts[fixtureKey(node)]
}

func (f traversalFixture) AttachmentPipelineSteps(node, attachment string) []AuthoredCoordinate {
	return f.attachPipeline[fixtureKey(node, attachment)]
}

func (f traversalFixture) BoundaryAttachmentPipelineSteps(boundary, attachment string) []AuthoredCoordinate {
	return f.boundaryAttachPipeline[fixtureKey(boundary, attachment)]
}

func (f traversalFixture) Providers(boundary string) []string {
	return f.providers[fixtureKey(boundary)]
}

func (f traversalFixture) EmittedPaths(boundary, provider string) []string {
	return f.emittedPaths[fixtureKey(boundary, provider)]
}

func (f traversalFixture) BoundaryAttachments(boundary string) []string {
	return f.boundaryAttach[fixtureKey(boundary)]
}

func (f traversalFixture) SubsystemAttachments(subsystem string) []string {
	return f.subsystemAttach[fixtureKey(subsystem)]
}

// The two nodes the fixture carries, chosen so that every traversal is exercised
// against both halves of the component-locator rule in one pass.
//
//   - webNode was authored at the top level of its document and reached through a
//     synthesized container, which contributes no segment, so its locator names
//     the top-level site.
//   - apiNode sits in a container the author declared, whose authored site the
//     locator spells level by level under the container's own key — and so does
//     the site of anything authored inside it.
var (
	webNode = NodeInstance{
		ID:                   "web",
		SourceFile:           "aggregate.yaml",
		AuthoredName:         "web",
		SubsystemPath:        "default",
		SynthesizedSubsystem: true,
	}
	apiNode = NodeInstance{
		ID:                   "billing.api",
		SourceFile:           "billing/aggregate.yaml",
		AuthoredName:         "api",
		SubsystemPath:        "billing",
		SynthesizedSubsystem: false,
	}
)

func newTraversalFixture() traversalFixture {
	return traversalFixture{
		// Reverse-alphabetical, so a node sort would be visible.
		nodes: []NodeInstance{webNode, apiNode},
		edges: []EdgeInstance{
			{Node: webNode, Socket: "zebra"},
			{Node: apiNode, Socket: "db"},
		},
		// One nested scope and one top-level scope, reverse-alphabetical so a sort
		// would be visible — the exact shape the subsystem set below takes, because
		// the two axes are twins on separate authoring documents. The nested one is
		// what proves the locator names every level of the nesting rather than
		// joining the path in as one segment: a boundary path is one slash-joined
		// string and the authored Release spells each level under the container's key
		// again.
		boundaries: []AuthoredCoordinate{
			{Value: "prod/edge", SourceFile: "release.yaml"},
			{Value: "dev", SourceFile: "release.yaml"},
		},
		// One nested scope and one top-level scope, reverse-alphabetical so a sort
		// would be visible. The nested one is what proves the locator names every
		// level of the nesting rather than joining the path in as one segment: a
		// scope path is one dotted string and the authored document spells each
		// level under the container's key again.
		subsystems: []AuthoredCoordinate{
			{Value: "web", SourceFile: "aggregate.yaml"},
			{Value: "billing.payments", SourceFile: "billing/aggregate.yaml"},
		},

		sockets: map[string][]string{
			fixtureKey("web"):         {"zebra", "alpha"},
			fixtureKey("billing.api"): {"db"},
		},
		attachments: map[string][]string{
			fixtureKey("web"): {"tls", "logging"},
		},
		attachmentSockets: map[string][]string{
			fixtureKey("web", "tls"): {"issuer"},
		},
		slots: map[string][]string{
			fixtureKey("web"): {"sidecar"},
		},
		mechanisms: map[string][]string{
			fixtureKey("web"): {"env", "file"},
		},
		deliveryKeys: map[string][]string{
			fixtureKey("web", "env"): {"DB_HOST", "DB_PORT"},
		},
		nodePillars: map[string][]string{
			fixtureKey("web"): {"app"},
		},
		nodePillarLeaves: map[string][]string{
			fixtureKey("web", "app"): {"replicas", "image"},
		},
		parameters: map[string][]string{
			fixtureKey("web"):         {"db_size"},
			fixtureKey("billing.api"): {"region"},
		},
		attachPillars: map[string][]string{
			fixtureKey("web", "tls"): {"app"},
		},
		attachLeaves: map[string][]string{
			fixtureKey("web", "tls", "app"): {"issuer"},
		},
		providers: map[string][]string{
			fixtureKey("prod/edge"): {"helm", "terraform"},
			fixtureKey("dev"):       {"helm"},
		},
		emittedPaths: map[string][]string{
			fixtureKey("prod/edge", "helm"):      {"a.yaml", "b.yaml"},
			fixtureKey("prod/edge", "terraform"): {"main.tf.json"},
		},
		// Two on one boundary and none on the other, so a transcript shows the
		// walk entering a boundary that declares none and yielding nothing there
		// rather than stopping.
		boundaryAttach: map[string][]string{
			fixtureKey("prod/edge"): {"shared-db", "audit"},
		},
		// Two on the nested scope and none on the other, so a transcript shows the
		// walk entering a scope that declares none and yielding nothing there rather
		// than stopping.
		subsystemAttach: map[string][]string{
			fixtureKey("billing.payments"): {"tls", "audit"},
		},

		locals: map[string][]AuthoredCoordinate{
			fixtureKey("web"):         {{Value: "fullname", SourceFile: "recipes/web.yaml"}},
			fixtureKey("billing.api"): {{Value: "prefix", SourceFile: "recipes/api.yaml"}},
		},
		provides: map[string][]AuthoredCoordinate{
			fixtureKey("web"): {{Value: "host", SourceFile: "recipes/web.yaml"}},
		},
		pipeline: map[string][]AuthoredCoordinate{
			fixtureKey("web"): {{Value: "render", SourceFile: "recipes/web.yaml"}},
		},
		artifacts: map[string][]AuthoredCoordinate{
			fixtureKey("web"): {{Value: "values-yaml", SourceFile: "recipes/web.yaml"}},
		},

		// The SAME step identifier under two attachments on one node, which is the
		// case the attachment coordinate exists to separate: the two are distinct
		// steps in independently authored documents, nothing declares step
		// identifiers unique across documents, and a key carrying only the node and
		// the step would answer for whichever was asked last. The two files differ,
		// so the provenance transcript tells them apart as well as the readback.
		attachPipeline: map[string][]AuthoredCoordinate{
			fixtureKey("web", "tls"):     {{Value: "stamp", SourceFile: "featurerecipes/tls.yaml"}},
			fixtureKey("web", "logging"): {{Value: "stamp", SourceFile: "featurerecipes/logging.yaml"}},
		},
		// One of the boundary's two attachments carries a pipeline and the other
		// carries none, so a transcript shows the walk entering an attachment that
		// declares no pipeline and yielding nothing there rather than stopping.
		boundaryAttachPipeline: map[string][]AuthoredCoordinate{
			fixtureKey("prod/edge", "shared-db"): {{Value: "annotate", SourceFile: "featurerecipes/shared-db.yaml"}},
		},
	}
}

// raisingCheck raises exactly one objection per invocation, so the objections a
// traversal returns are a transcript of what it invoked the check on, in order.
func raisingCheck[S Subject]() Check[traversalView, S] {
	return Check[traversalView, S]{
		Sentinel: errRuleBroken,
		Severity: SeverityFatal,
		Run: func(_ traversalView, s S) []Objection {
			return []Objection{s.Raise(errRuleBroken)}
		},
	}
}

// traversalCase is one enumerated kind's traversal, together with the transcript
// it must produce over the fixture: one "file|locator" entry per invocation, in
// invocation order.
//
// run takes the cancellation signal rather than closing over one, so the same
// table drives both the uncancelled transcript and the assertion that every entry
// point honours a signal. A table that could only be driven one way would leave
// the second property to a hand-picked subset of the kinds.
type traversalCase struct {
	kind SubjectKind
	run  func(Cancellation, traversalFixture, traversalView) ([]Objection, error)
	want []string
}

func traversalCases() []traversalCase {
	return []traversalCase{
		{
			kind: SubjectNode,
			run: func(cancelled Cancellation, f traversalFixture, v traversalView) ([]Objection, error) {
				return RunOnNodes(cancelled, f, v, raisingCheck[NodeSubject]())
			},
			want: []string{
				"aggregate.yaml|.spec.components.web",
				"billing/aggregate.yaml|.spec.subsystems.billing.components.api",
			},
		},
		{
			kind: SubjectNodeSocket,
			run: func(cancelled Cancellation, f traversalFixture, v traversalView) ([]Objection, error) {
				return RunOnNodeSockets(cancelled, f, f, v, raisingCheck[NodeSocketSubject]())
			},
			want: []string{
				"aggregate.yaml|.spec.components.web.uses.zebra",
				"aggregate.yaml|.spec.components.web.uses.alpha",
				"billing/aggregate.yaml|.spec.subsystems.billing.components.api.uses.db",
			},
		},
		{
			kind: SubjectEdge,
			run: func(cancelled Cancellation, f traversalFixture, v traversalView) ([]Objection, error) {
				return RunOnEdges(cancelled, f, v, raisingCheck[EdgeSubject]())
			},
			want: []string{
				"aggregate.yaml|.spec.components.web.uses.zebra",
				"billing/aggregate.yaml|.spec.subsystems.billing.components.api.uses.db",
			},
		},
		{
			kind: SubjectNodeAttachment,
			run: func(cancelled Cancellation, f traversalFixture, v traversalView) ([]Objection, error) {
				return RunOnNodeAttachments(cancelled, f, f, v, raisingCheck[NodeAttachmentSubject]())
			},
			want: []string{
				"aggregate.yaml|.spec.components.web.features.tls",
				"aggregate.yaml|.spec.components.web.features.logging",
			},
		},
		{
			kind: SubjectNodeAttachmentSocket,
			run: func(cancelled Cancellation, f traversalFixture, v traversalView) ([]Objection, error) {
				return RunOnNodeAttachmentSockets(cancelled, f, f, f, v, raisingCheck[NodeAttachmentSocketSubject]())
			},
			want: []string{
				"aggregate.yaml|.spec.components.web.features.tls.uses.issuer",
			},
		},
		{
			kind: SubjectNodeSlot,
			run: func(cancelled Cancellation, f traversalFixture, v traversalView) ([]Objection, error) {
				return RunOnNodeSlots(cancelled, f, f, v, raisingCheck[NodeSlotSubject]())
			},
			want: []string{
				"aggregate.yaml|.spec.components.web.features.sidecar",
			},
		},
		{
			kind: SubjectNodeMechanism,
			run: func(cancelled Cancellation, f traversalFixture, v traversalView) ([]Objection, error) {
				return RunOnNodeMechanisms(cancelled, f, f, v, raisingCheck[NodeMechanismSubject]())
			},
			want: []string{
				"aggregate.yaml|.spec.components.web.inject.env",
				"aggregate.yaml|.spec.components.web.inject.file",
			},
		},
		{
			kind: SubjectNodeMechanismDeliveryKey,
			run: func(cancelled Cancellation, f traversalFixture, v traversalView) ([]Objection, error) {
				return RunOnNodeMechanismDeliveryKeys(cancelled, f, f, f, v, raisingCheck[NodeMechanismDeliveryKeySubject]())
			},
			want: []string{
				"aggregate.yaml|.spec.components.web.inject.env.DB_HOST",
				"aggregate.yaml|.spec.components.web.inject.env.DB_PORT",
			},
		},
		{
			kind: SubjectNodePillarLeaf,
			run: func(cancelled Cancellation, f traversalFixture, v traversalView) ([]Objection, error) {
				return RunOnNodePillarLeaves(cancelled, f, f, f, v, raisingCheck[NodePillarLeafSubject]())
			},
			want: []string{
				"aggregate.yaml|.spec.components.web.values.app.replicas",
				"aggregate.yaml|.spec.components.web.values.app.image",
			},
		},
		{
			// The node in an author-declared container names its parameter at the
			// document's spec level and not under .spec.subsystems, and that is the
			// point: a parameter is declared at its document's spec level rather
			// than inside the component, so the component's own site does not reach
			// it however deeply that site nests.
			kind: SubjectNodeParameter,
			run: func(cancelled Cancellation, f traversalFixture, v traversalView) ([]Objection, error) {
				return RunOnNodeParameters(cancelled, f, f, v, raisingCheck[NodeParameterSubject]())
			},
			want: []string{
				"aggregate.yaml|.spec.parameters.db_size",
				"billing/aggregate.yaml|.spec.parameters.region",
			},
		},
		{
			kind: SubjectNodeAttachmentPillarLeaf,
			run: func(cancelled Cancellation, f traversalFixture, v traversalView) ([]Objection, error) {
				return RunOnNodeAttachmentPillarLeaves(cancelled, f, f, f, f, v, raisingCheck[NodeAttachmentPillarLeafSubject]())
			},
			want: []string{
				"aggregate.yaml|.spec.components.web.features.tls.values.app.issuer",
			},
		},
		{
			// The Recipe's file, not the node's — the two are different
			// documents, and the enumerated entry is the only thing that knows
			// which.
			kind: SubjectNodeLocalsKey,
			run: func(cancelled Cancellation, f traversalFixture, v traversalView) ([]Objection, error) {
				return RunOnNodeLocalsKeys(cancelled, f, f, v, raisingCheck[NodeLocalsKeySubject]())
			},
			want: []string{
				"recipes/web.yaml|.spec.locals.fullname",
				"recipes/api.yaml|.spec.locals.prefix",
			},
		},
		{
			kind: SubjectNodeProvidesKey,
			run: func(cancelled Cancellation, f traversalFixture, v traversalView) ([]Objection, error) {
				return RunOnNodeProvidesKeys(cancelled, f, f, v, raisingCheck[NodeProvidesKeySubject]())
			},
			want: []string{
				"recipes/web.yaml|.spec.provides.host",
			},
		},
		{
			kind: SubjectNodePipelineStep,
			run: func(cancelled Cancellation, f traversalFixture, v traversalView) ([]Objection, error) {
				return RunOnNodePipelineSteps(cancelled, f, f, v, raisingCheck[NodePipelineStepSubject]())
			},
			want: []string{
				"recipes/web.yaml|.spec.pipeline.render",
			},
		},
		{
			// Two invocations whose locators are identical in their dotted part and
			// differ only in the file, because each step is authored at its own
			// FeatureRecipe's spec level under the same key. That is the same
			// document-spec shape a node's Recipe-authored coordinates take, and it
			// is why the file comes from the enumerated entry: the attachment was
			// written in the Aggregate and the step was not.
			kind: SubjectNodeAttachmentPipelineStep,
			run: func(cancelled Cancellation, f traversalFixture, v traversalView) ([]Objection, error) {
				return RunOnNodeAttachmentPipelineSteps(cancelled, f, f, f, v,
					raisingCheck[NodeAttachmentPipelineStepSubject]())
			},
			want: []string{
				"featurerecipes/tls.yaml|.spec.pipeline.stamp",
				"featurerecipes/logging.yaml|.spec.pipeline.stamp",
			},
		},
		{
			// One invocation from two enumerated attachments on one boundary, and
			// none at all from the second boundary: the walk enters an attachment
			// that declares no pipeline, yields nothing there, and carries on.
			kind: SubjectBoundaryAttachmentPipelineStep,
			run: func(cancelled Cancellation, f traversalFixture, v traversalView) ([]Objection, error) {
				return RunOnBoundaryAttachmentPipelineSteps(cancelled, f, f, f, v,
					raisingCheck[BoundaryAttachmentPipelineStepSubject]())
			},
			want: []string{
				"featurerecipes/shared-db.yaml|.spec.pipeline.annotate",
			},
		},
		{
			// Three invocations carrying the same provenance, because neither the
			// provider nor the emitted path is authored at this site: the
			// boundary is the whole of what the author wrote. The subjects differ
			// in their coordinates, which this transcript cannot see — that is
			// what TestCoordinateAccessorsCarryTheEnumeratedCoordinateValues is
			// for.
			kind: SubjectBoundaryProviderPath,
			run: func(cancelled Cancellation, f traversalFixture, v traversalView) ([]Objection, error) {
				return RunOnBoundaryProviderPaths(cancelled, f, f, f, v, raisingCheck[BoundaryProviderPathSubject]())
			},
			want: []string{
				"release.yaml|.spec.boundaries.prod.boundaries.edge",
				"release.yaml|.spec.boundaries.prod.boundaries.edge",
				"release.yaml|.spec.boundaries.prod.boundaries.edge",
			},
		},
		{
			kind: SubjectNodeArtifact,
			run: func(cancelled Cancellation, f traversalFixture, v traversalView) ([]Objection, error) {
				return RunOnNodeArtifacts(cancelled, f, f, v, raisingCheck[NodeArtifactSubject]())
			},
			want: []string{
				"recipes/web.yaml|.spec.artifacts.values-yaml",
			},
		},
		{
			// Both invocations land on the boundary that declares attachments, and
			// each names its own attachment: unlike the provider-and-path kind
			// above, every coordinate here is something the operator wrote, so the
			// locators differ and this transcript can tell the two apart. The
			// boundary is two levels deep, so each level appears under the
			// container's key — the same shape the subsystem twin below takes, on
			// the other authoring document.
			kind: SubjectBoundaryAttachment,
			run: func(cancelled Cancellation, f traversalFixture, v traversalView) ([]Objection, error) {
				return RunOnBoundaryAttachments(cancelled, f, f, v, raisingCheck[BoundaryAttachmentSubject]())
			},
			want: []string{
				"release.yaml|.spec.boundaries.prod.boundaries.edge.features.shared-db",
				"release.yaml|.spec.boundaries.prod.boundaries.edge.features.audit",
			},
		},
		{
			// The twin of the case above on the other authoring document: both
			// invocations land on the scope that declares attachments, whose path is
			// two levels deep, so each level appears under the container's key. The
			// two axes are joined with different separators and are named through one
			// rule, so the pair of transcripts is what shows the rule serving both.
			// The scope declaring none yields nothing and the walk carries on.
			kind: SubjectSubsystemAttachment,
			run: func(cancelled Cancellation, f traversalFixture, v traversalView) ([]Objection, error) {
				return RunOnSubsystemAttachments(cancelled, f, f, v, raisingCheck[SubsystemAttachmentSubject]())
			},
			want: []string{
				"billing/aggregate.yaml|.spec.subsystems.billing.subsystems.payments.features.tls",
				"billing/aggregate.yaml|.spec.subsystems.billing.subsystems.payments.features.audit",
			},
		},
	}
}

// TestEveryPullKindsTraversalIsExercised keeps the table above honest against the
// generated kind set. Without it a table one entry short of the generated set
// would report every case it contains as passing, and the kind it omitted would be
// the one nothing exercises.
func TestEveryPullKindsTraversalIsExercised(t *testing.T) {
	t.Parallel()

	covered := map[SubjectKind]int{}
	for _, tc := range traversalCases() {
		covered[tc.kind]++
	}

	var missing, duplicated []string
	pull := 0
	for _, info := range SubjectKinds {
		if info.Traversal != TraversalPull {
			continue
		}
		pull++
		switch covered[info.Kind] {
		case 0:
			missing = append(missing, string(info.Kind))
		case 1:
		default:
			duplicated = append(duplicated, string(info.Kind))
		}
		delete(covered, info.Kind)
	}
	if pull == 0 {
		t.Fatalf("the generated kind set carries no enumerated kind, so a clean result certifies nothing")
	}
	if len(missing) > 0 {
		t.Errorf("no traversal case exercises the enumerated kind(s) %s, so their traversals are "+
			"unproven by this file", strings.Join(missing, ", "))
	}
	if len(duplicated) > 0 {
		t.Errorf("more than one case exercises %s", strings.Join(duplicated, ", "))
	}
	for kind := range covered {
		t.Errorf("a case exercises %s, which the generated set does not carry as an enumerated kind", kind)
	}
	t.Logf("traversal coverage: %d enumerated kinds, %d exercised", pull, pull-len(missing))
}

// TestATraversalInvokesOncePerInstanceInEnumeratorOrderWithFrameworkProvenance is
// the behavioural assertion over every traversal at once: the number of
// invocations, the order of them, and the provenance each carries.
//
// The three are checked together because they are one transcript. Checking the
// count alone would pass a traversal that walked the right instances in the wrong
// order, and checking the order alone would pass one that walked a subset.
func TestATraversalInvokesOncePerInstanceInEnumeratorOrderWithFrameworkProvenance(t *testing.T) {
	t.Parallel()

	fixture := newTraversalFixture()

	for _, tc := range traversalCases() {
		t.Run(string(tc.kind), func(t *testing.T) {
			t.Parallel()

			objections, err := tc.run(nil, fixture, traversalView{})
			if err != nil {
				t.Fatalf("the traversal returned %v over a walk nothing cancelled.\n"+
					"INVARIANT: a traversal reports an error only when the cancellation signal it was given "+
					"returned one, and a nil signal never does.\n"+
					"CONSEQUENCE: an error here means the walk stopped for a reason no caller asked for, so "+
					"the transcript below is a truncation rather than the whole enumeration.", err)
			}

			got := make([]string, 0, len(objections))
			for _, o := range objections {
				if o.Sentinel() == nil {
					t.Errorf("an objection carries no sentinel, so the traversal handed the check a " +
						"subject it could not raise from")
				}
				got = append(got, o.SourceFile()+"|"+o.Locator())
			}

			if len(got) != len(tc.want) {
				t.Fatalf("the traversal invoked the check %d time(s); want %d.\n"+
					"INVARIANT: a check is invoked exactly once per instance of its declared subject.\n"+
					"CONSEQUENCE: the count is what \"once\" means for this kind. Too few and a rule is "+
					"unenforced over the instances that were skipped, silently; too many and one violation "+
					"is reported several times.\ngot:  %v\nwant: %v", len(got), len(tc.want), got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("invocation %d carried provenance %q; want %q.\n"+
						"INVARIANT: enumeration order is the order the enumerators returned, and a "+
						"subject's file and locator are assembled by the framework.\n"+
						"CONSEQUENCE: a check raises in the order it is invoked, so an order the framework "+
						"imposed rather than preserved changes the order diagnostics appear in — and "+
						"identical input stops producing identical output. A wrong locator points a reader "+
						"at a key the file does not contain.", i, got[i], tc.want[i])
				}
			}
		})
	}
}

// TestCoordinateAccessorsCarryTheEnumeratedCoordinateValues proves the coordinate
// accessors are wired to the traversal rather than merely declared: a check reads
// back the coordinates of the instance it was invoked for, and they are the values
// the enumerators handed over.
//
// It is a separate assertion from the provenance transcript above because the two
// travel independently, and one of them can be empty while the other must not be.
// A coordinate keys a fact accessor; a locator names an authored site, and a
// subject whose site is not a key at all still carries its coordinates in full —
// the case [TestACoordinateIsCarriedWhereThereIsNoAuthoredSiteToName] pins. A
// check handed an empty identifier would read facts for the wrong instance, or for
// no instance, and the provenance assertion cannot see that: it passes on an empty
// locator, because wherever an empty locator is produced it is the correct answer.
// # Every kind, and why the coverage is asserted rather than assumed
//
// The completeness assertion over the declarations cannot call an accessor, so
// this is the only place a coordinate is shown to actually arrive. A table of
// kinds beside a generated set of kinds is the arrangement that reads as complete
// while missing one, so the table is checked against the generated set by
// [TestEveryKindsCoordinateReadbackIsExercised] rather than trusted.
//
// # The two seams, named rather than blurred
//
// Most kinds are read through their real traversal — the exported entry a consumer
// calls, driven by the enumerator fixture. A push-model kind is read through its
// push entry, which takes what it walks from the caller rather than from the
// fixture. One kind is read through its constructor instead: the assertion kind of
// a presented document has no entry point, so the constructor is the nearest
// reachable seam. Each case records which seam it used and the census reports the
// split, because a proof through a constructor establishes the wiring from the
// constructor inward and nothing about a caller, and it must not be counted as the
// same fact as a traversal-driven one.
func TestCoordinateAccessorsCarryTheEnumeratedCoordinateValues(t *testing.T) {
	t.Parallel()

	fixture := newTraversalFixture()

	for _, tc := range coordinateFlowCases() {
		t.Run(string(tc.kind), func(t *testing.T) {
			t.Parallel()

			got := tc.read(fixture)

			if len(got) != len(tc.want) {
				t.Fatalf("the check was invoked %d time(s); want %d\ngot:  %v\nwant: %v",
					len(got), len(tc.want), got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("invocation %d read coordinates %q; want %q.\n"+
						"INVARIANT: a subject carries the coordinate values of the instance the check was "+
						"invoked for.\n"+
						"CONSEQUENCE: every fact is keyed, so a check reads facts through these values. Wrong "+
						"or empty ones read a different instance's facts, or none, and the predicate then "+
						"decides on evidence belonging to something else.", i, got[i], tc.want[i])
				}
			}

			// The record's arity against the accessor set the kind's coordinates owe.
			// Without it an accessor could be added, paired with its constructor
			// parameter, and never read here — which is the state this assertion exists
			// to make impossible.
			owed := 0
			for _, coordinate := range coordinatesOf(t, tc.kind) {
				owed += len(coordinateAccessors[coordinate])
			}
			if len(got) > 0 {
				if fields := len(strings.Split(got[0], "|")); fields != owed {
					t.Errorf("the readback records %d coordinate value(s) per invocation against %d accessor(s) "+
						"the kind's coordinates owe. Read every accessor the kind declares, or one of them is "+
						"proved by nothing here while every declaration assertion stays green.", fields, owed)
				}
			}
		})
	}
}

// TestACoordinateIsCarriedWhereThereIsNoAuthoredSiteToName pins that a subject's
// coordinates and its locator travel independently: the coordinate is carried in
// full even where there is no authored site for a locator to name.
//
// The property is pinned on the document kind because that kind's constructor
// takes no locator segments at all, so an empty locator there is structural
// rather than one rule's current answer. A pin resting on a rule moves whenever
// that rule is re-decided, and this property then stops being exercised without
// any assertion failing — an empty locator is what the provenance transcript
// expects, so the transcript cannot tell a carried coordinate from a lost one.
func TestACoordinateIsCarriedWhereThereIsNoAuthoredSiteToName(t *testing.T) {
	t.Parallel()

	const sourceFile = "billing/aggregate.yaml"

	subject := newDocumentSubject(sourceFile)

	if got := subject.Locator(); got != "" {
		t.Errorf("the document subject's locator = %q; want empty.\n"+
			"INVARIANT: a document is the whole file, so it holds no authored element for a locator "+
			"to name.\n"+
			"CONSEQUENCE: a locator here points a reader at a key rather than at the file the check "+
			"is raising about.", got)
	}
	if got := subject.SourceFile(); got != sourceFile {
		t.Errorf("the document subject's coordinate = %q; want %q.\n"+
			"INVARIANT: a coordinate keys a fact accessor and a locator names an authored site, so an "+
			"empty locator never implies an empty coordinate.\n"+
			"CONSEQUENCE: a check handed an empty coordinate reads facts for no instance, and the "+
			"provenance transcript cannot see it, because an empty locator is the correct answer "+
			"there.", got, sourceFile)
	}

	// Through the raise as well as on the subject: the pair is what a consumer
	// receives, and a coordinate carried on the subject but dropped at the raise
	// would satisfy both assertions above.
	if o := subject.Raise(errRuleBroken); o.SourceFile() != sourceFile || o.Locator() != "" {
		t.Errorf("the raised objection carries file %q and locator %q; want %q and an empty locator",
			o.SourceFile(), o.Locator(), sourceFile)
	}
}

// coordinatesOf returns the generated coordinates of one kind, failing rather
// than returning an empty set for a kind the generated table does not carry —
// which would make the arity check above pass by comparing zero against zero.
func coordinatesOf(t *testing.T, kind SubjectKind) []string {
	t.Helper()

	for _, info := range SubjectKinds {
		if info.Kind == kind {
			return info.Coordinates
		}
	}
	t.Fatalf("the generated kind set does not carry %s, so its owed accessor count cannot be derived", kind)
	return nil
}

// The two seams a coordinate readback can go through. The exported entry a
// consumer calls is the real path, whether it walks an enumeration or takes what
// it walks from the caller; a constructor is the fallback for a kind with no entry
// point at all.
const (
	seamTraversal   = "traversal"
	seamConstructor = "constructor"
)

// coordinateFlowCase is one kind's coordinate readback: how to obtain one record
// of the coordinate values per invocation, and the records expected over the
// fixture in invocation order.
type coordinateFlowCase struct {
	kind SubjectKind
	seam string
	read func(traversalFixture) []string
	want []string
}

// readback collects one record per invocation from a check declared over S,
// joining the coordinate values its accessors return.
//
// Nothing here supplies a cancellation signal, so a walk cannot stop early and
// the error is always nil. It is surfaced rather than discarded all the same: a
// stopped walk would hand back a short record set, and a short set is
// indistinguishable from a coordinate that never arrived.
func readback[S Subject](
	run func(Check[traversalView, S]) ([]Objection, error),
	of func(S) string,
) []string {
	var got []string
	if _, err := run(Check[traversalView, S]{
		Sentinel: errRuleBroken,
		Severity: SeverityFatal,
		Run: func(_ traversalView, s S) []Objection {
			got = append(got, of(s))
			return nil
		},
	}); err != nil {
		panic("a coordinate readback stopped early with no cancellation signal supplied: " + err.Error())
	}
	return got
}

func coordinateFlowCases() []coordinateFlowCase {
	const documentFile = "catalog/component.yaml"

	return []coordinateFlowCase{
		{
			kind: SubjectDocument,
			seam: seamTraversal,
			read: func(_ traversalFixture) []string {
				return readback(func(c Check[traversalView, DocumentSubject]) ([]Objection, error) {
					return RunOnDocument(documentFile, traversalView{}, c), nil
				}, func(s DocumentSubject) string { return s.SourceFile() })
			},
			want: []string{documentFile},
		},

		// The document's parameter kind reads through its push entry, so the names
		// come from the caller rather than from the enumerator fixture. Two are
		// presented rather than one: a single-instance readback cannot tell a walk
		// from an invocation that ignored what it was given.
		{
			kind: SubjectDocumentParameter,
			seam: seamTraversal,
			read: func(_ traversalFixture) []string {
				return readback(func(c Check[traversalView, DocumentParameterSubject]) ([]Objection, error) {
					return RunOnDocumentParameters(
						documentFile, []string{"db_size", "replica_count"}, traversalView{}, c,
					), nil
				}, func(s DocumentParameterSubject) string {
					return s.SourceFile() + "|" + s.ParameterName()
				})
			},
			want: []string{documentFile + "|db_size", documentFile + "|replica_count"},
		},

		// The document's assertion kind has no entry point, so the readback goes
		// through the constructor: it proves the coordinate reaches the accessor, and
		// it cannot prove a caller supplies it because there is no caller to
		// exercise.
		{
			kind: SubjectDocumentAssertion,
			seam: seamConstructor,
			read: func(_ traversalFixture) []string {
				s := newDocumentAssertionSubject(documentFile, "port-set", "spec", "assertions", "port-set")
				return []string{s.SourceFile() + "|" + s.AssertionName()}
			},
			want: []string{documentFile + "|port-set"},
		},

		// The node kind reads its identifier and never its authored name, and the
		// fixture makes the difference visible: the second node's identifier is
		// qualified by the container it was reached through while its authored name is
		// not, so a constructor handed the wrong one of the two is caught here.
		{
			kind: SubjectNode,
			seam: seamTraversal,
			read: func(f traversalFixture) []string {
				return readback(func(c Check[traversalView, NodeSubject]) ([]Objection, error) {
					return RunOnNodes(nil, f, traversalView{}, c)
				}, func(s NodeSubject) string { return s.NodeID() })
			},
			want: []string{"web", "billing.api"},
		},
		{
			kind: SubjectNodeAttachment,
			seam: seamTraversal,
			read: func(f traversalFixture) []string {
				return readback(func(c Check[traversalView, NodeAttachmentSubject]) ([]Objection, error) {
					return RunOnNodeAttachments(nil, f, f, traversalView{}, c)
				}, func(s NodeAttachmentSubject) string { return s.NodeID() + "|" + s.AttachmentName() })
			},
			want: []string{"web|tls", "web|logging"},
		},
		{
			kind: SubjectNodeAttachmentSocket,
			seam: seamTraversal,
			read: func(f traversalFixture) []string {
				return readback(func(c Check[traversalView, NodeAttachmentSocketSubject]) ([]Objection, error) {
					return RunOnNodeAttachmentSockets(nil, f, f, f, traversalView{}, c)
				}, func(s NodeAttachmentSocketSubject) string {
					return s.NodeID() + "|" + s.AttachmentName() + "|" + s.SocketName()
				})
			},
			want: []string{"web|tls|issuer"},
		},
		{
			kind: SubjectNodeSlot,
			seam: seamTraversal,
			read: func(f traversalFixture) []string {
				return readback(func(c Check[traversalView, NodeSlotSubject]) ([]Objection, error) {
					return RunOnNodeSlots(nil, f, f, traversalView{}, c)
				}, func(s NodeSlotSubject) string { return s.NodeID() + "|" + s.SlotName() })
			},
			want: []string{"web|sidecar"},
		},
		{
			kind: SubjectNodeMechanism,
			seam: seamTraversal,
			read: func(f traversalFixture) []string {
				return readback(func(c Check[traversalView, NodeMechanismSubject]) ([]Objection, error) {
					return RunOnNodeMechanisms(nil, f, f, traversalView{}, c)
				}, func(s NodeMechanismSubject) string { return s.NodeID() + "|" + s.MechanismName() })
			},
			want: []string{"web|env", "web|file"},
		},
		{
			kind: SubjectNodeMechanismDeliveryKey,
			seam: seamTraversal,
			read: func(f traversalFixture) []string {
				return readback(func(c Check[traversalView, NodeMechanismDeliveryKeySubject]) ([]Objection, error) {
					return RunOnNodeMechanismDeliveryKeys(nil, f, f, f, traversalView{}, c)
				}, func(s NodeMechanismDeliveryKeySubject) string {
					return s.NodeID() + "|" + s.MechanismName() + "|" + s.DeliveryKey()
				})
			},
			want: []string{"web|env|DB_HOST", "web|env|DB_PORT"},
		},
		{
			kind: SubjectNodePillarLeaf,
			seam: seamTraversal,
			read: func(f traversalFixture) []string {
				return readback(func(c Check[traversalView, NodePillarLeafSubject]) ([]Objection, error) {
					return RunOnNodePillarLeaves(nil, f, f, f, traversalView{}, c)
				}, func(s NodePillarLeafSubject) string {
					return s.NodeID() + "|" + s.PillarName() + "|" + s.LeafName()
				})
			},
			want: []string{"web|app|replicas", "web|app|image"},
		},
		{
			kind: SubjectNodeParameter,
			seam: seamTraversal,
			read: func(f traversalFixture) []string {
				return readback(func(c Check[traversalView, NodeParameterSubject]) ([]Objection, error) {
					return RunOnNodeParameters(nil, f, f, traversalView{}, c)
				}, func(s NodeParameterSubject) string { return s.NodeID() + "|" + s.ParameterName() })
			},
			want: []string{"web|db_size", "billing.api|region"},
		},
		{
			kind: SubjectNodeAttachmentPillarLeaf,
			seam: seamTraversal,
			read: func(f traversalFixture) []string {
				return readback(func(c Check[traversalView, NodeAttachmentPillarLeafSubject]) ([]Objection, error) {
					return RunOnNodeAttachmentPillarLeaves(nil, f, f, f, f, traversalView{}, c)
				}, func(s NodeAttachmentPillarLeafSubject) string {
					return s.NodeID() + "|" + s.AttachmentName() + "|" + s.PillarName() + "|" + s.LeafName()
				})
			},
			want: []string{"web|tls|app|issuer"},
		},

		// The Recipe-authored kinds are the ones whose identifier and whose source
		// file come from different places — the identifier from the node, the key and
		// the file from the enumerated entry. A readback that took the entry's value
		// as the identifier is visible here, and one that took the node's file is
		// visible in the provenance transcript.
		{
			kind: SubjectNodeLocalsKey,
			seam: seamTraversal,
			read: func(f traversalFixture) []string {
				return readback(func(c Check[traversalView, NodeLocalsKeySubject]) ([]Objection, error) {
					return RunOnNodeLocalsKeys(nil, f, f, traversalView{}, c)
				}, func(s NodeLocalsKeySubject) string { return s.NodeID() + "|" + s.LocalsKey() })
			},
			want: []string{"web|fullname", "billing.api|prefix"},
		},
		{
			kind: SubjectNodeProvidesKey,
			seam: seamTraversal,
			read: func(f traversalFixture) []string {
				return readback(func(c Check[traversalView, NodeProvidesKeySubject]) ([]Objection, error) {
					return RunOnNodeProvidesKeys(nil, f, f, traversalView{}, c)
				}, func(s NodeProvidesKeySubject) string { return s.NodeID() + "|" + s.ProvidesKey() })
			},
			want: []string{"web|host"},
		},
		{
			kind: SubjectNodePipelineStep,
			seam: seamTraversal,
			read: func(f traversalFixture) []string {
				return readback(func(c Check[traversalView, NodePipelineStepSubject]) ([]Objection, error) {
					return RunOnNodePipelineSteps(nil, f, f, traversalView{}, c)
				}, func(s NodePipelineStepSubject) string { return s.NodeID() + "|" + s.StepID() })
			},
			want: []string{"web|render"},
		},

		// The kind whose reason for existing is exactly what this readback shows: the
		// two invocations carry the same node and the same step identifier, and the
		// attachment is the only thing that separates them. A traversal that dropped
		// the attachment would produce two identical records here, while every
		// declaration assertion stayed green.
		{
			kind: SubjectNodeAttachmentPipelineStep,
			seam: seamTraversal,
			read: func(f traversalFixture) []string {
				return readback(func(c Check[traversalView, NodeAttachmentPipelineStepSubject]) ([]Objection, error) {
					return RunOnNodeAttachmentPipelineSteps(nil, f, f, f, traversalView{}, c)
				}, func(s NodeAttachmentPipelineStepSubject) string {
					return s.NodeID() + "|" + s.AttachmentName() + "|" + s.StepID()
				})
			},
			want: []string{"web|tls|stamp", "web|logging|stamp"},
		},
		{
			kind: SubjectBoundaryAttachmentPipelineStep,
			seam: seamTraversal,
			read: func(f traversalFixture) []string {
				return readback(func(c Check[traversalView, BoundaryAttachmentPipelineStepSubject]) ([]Objection, error) {
					return RunOnBoundaryAttachmentPipelineSteps(nil, f, f, f, traversalView{}, c)
				}, func(s BoundaryAttachmentPipelineStepSubject) string {
					return s.BoundaryPath() + "|" + s.AttachmentName() + "|" + s.StepID()
				})
			},
			want: []string{"prod/edge|shared-db|annotate"},
		},
		{
			kind: SubjectNodeArtifact,
			seam: seamTraversal,
			read: func(f traversalFixture) []string {
				return readback(func(c Check[traversalView, NodeArtifactSubject]) ([]Objection, error) {
					return RunOnNodeArtifacts(nil, f, f, traversalView{}, c)
				}, func(s NodeArtifactSubject) string { return s.NodeID() + "|" + s.ArtifactName() })
			},
			want: []string{"web|values-yaml"},
		},
		{
			kind: SubjectNodeSocket,
			seam: seamTraversal,
			read: func(f traversalFixture) []string {
				return readback(func(c Check[traversalView, NodeSocketSubject]) ([]Objection, error) {
					return RunOnNodeSockets(nil, f, f, traversalView{}, c)
				}, func(s NodeSocketSubject) string { return s.NodeID() + "|" + s.SocketName() })
			},
			want: []string{"web|zebra", "web|alpha", "billing.api|db"},
		},
		{
			kind: SubjectEdge,
			seam: seamTraversal,
			read: func(f traversalFixture) []string {
				return readback(func(c Check[traversalView, EdgeSubject]) ([]Objection, error) {
					return RunOnEdges(nil, f, traversalView{}, c)
				}, func(s EdgeSubject) string { return s.NodeID() + "|" + s.SocketName() })
			},
			want: []string{"web|zebra", "billing.api|db"},
		},

		// The kind whose innermost coordinate contributes no locator segment, which is
		// exactly why it needs this assertion rather than the provenance transcript:
		// all three of its invocations carry one identical locator, so nothing in that
		// transcript distinguishes them and a traversal that discarded the path would
		// read as correct there.
		//
		// The boundary coordinate arrives whole here, as the path the enumerator
		// handed over, and the locator names the same scope spelled level by level.
		// Those are two correct spellings of one scope rather than a discrepancy: the
		// coordinate is what a keyed fact takes, and the locator is what the authored
		// document contains.
		{
			kind: SubjectBoundaryProviderPath,
			seam: seamTraversal,
			read: func(f traversalFixture) []string {
				return readback(func(c Check[traversalView, BoundaryProviderPathSubject]) ([]Objection, error) {
					return RunOnBoundaryProviderPaths(nil, f, f, f, traversalView{}, c)
				}, func(s BoundaryProviderPathSubject) string {
					return s.BoundaryPath() + "|" + s.ProviderName() + "|" + s.EmittedPath()
				})
			},
			want: []string{"prod/edge|helm|a.yaml", "prod/edge|helm|b.yaml", "prod/edge|terraform|main.tf.json"},
		},
		{
			kind: SubjectBoundaryAttachment,
			seam: seamTraversal,
			read: func(f traversalFixture) []string {
				return readback(func(c Check[traversalView, BoundaryAttachmentSubject]) ([]Objection, error) {
					return RunOnBoundaryAttachments(nil, f, f, traversalView{}, c)
				}, func(s BoundaryAttachmentSubject) string {
					return s.BoundaryPath() + "|" + s.AttachmentName()
				})
			},
			want: []string{"prod/edge|shared-db", "prod/edge|audit"},
		},

		// A scope coordinate arrives as the path the enumerator handed over rather
		// than as the innermost name, which is what a fact keyed by it takes: two
		// scopes at different depths may carry one name, so a check reading a bare
		// name would key facts that cannot tell them apart. It holds for both
		// authoring documents' scope axes, which is why the boundary rows above read
		// back a path too.
		{
			kind: SubjectSubsystemAttachment,
			seam: seamTraversal,
			read: func(f traversalFixture) []string {
				return readback(func(c Check[traversalView, SubsystemAttachmentSubject]) ([]Objection, error) {
					return RunOnSubsystemAttachments(nil, f, f, traversalView{}, c)
				}, func(s SubsystemAttachmentSubject) string {
					return s.SubsystemPath() + "|" + s.AttachmentName()
				})
			},
			want: []string{"billing.payments|tls", "billing.payments|audit"},
		},
	}
}

// TestEveryKindsCoordinateReadbackIsExercised keeps the readback table honest
// against the generated kind set, which is the guard the traversal table carries
// and for the same reason: a hand-written table sitting beside the generated set
// it mirrors reads as complete while missing one, and the one it misses is a kind
// whose coordinates are proved by nothing. No count written here would close that,
// because a count beside a derived set is itself the thing that goes stale.
func TestEveryKindsCoordinateReadbackIsExercised(t *testing.T) {
	t.Parallel()

	if len(SubjectKinds) == 0 {
		t.Fatalf("the generated kind set is empty, so a clean result here certifies nothing")
	}

	cases := coordinateFlowCases()

	covered := map[SubjectKind]string{}
	for _, tc := range cases {
		if seam, duplicate := covered[tc.kind]; duplicate {
			t.Errorf("%s has two readback cases (seams %s and %s); one kind's coordinates have one answer",
				tc.kind, seam, tc.seam)
		}
		covered[tc.kind] = tc.seam
	}

	declaredKinds := map[SubjectKind]bool{}
	var missing []string
	for _, info := range SubjectKinds {
		declaredKinds[info.Kind] = true
		if _, ok := covered[info.Kind]; !ok {
			missing = append(missing, string(info.Kind))
		}
	}
	if len(missing) > 0 {
		t.Errorf("no coordinate readback for the derived kind(s) %s.\n"+
			"INVARIANT: a coordinate a kind declares reaches the check that declared the kind.\n"+
			"CONSEQUENCE: the declaration assertions compare names and never call an accessor, so a kind "+
			"absent from this table can carry a full accessor set where every one of them returns the empty "+
			"string, with every assertion green.", strings.Join(missing, ", "))
	}
	for _, tc := range cases {
		if !declaredKinds[tc.kind] {
			t.Errorf("the readback table carries %s, which the generated kind set does not declare", tc.kind)
		}
	}

	traversalSeams := 0
	for _, seam := range covered {
		if seam == seamTraversal {
			traversalSeams++
		}
	}
	t.Logf("coordinate readback: %d of %d derived kind(s) covered, %d through a traversal and %d through a "+
		"constructor", len(covered), len(SubjectKinds), traversalSeams, len(covered)-traversalSeams)
}

// TestATraversalOverNoInstancesInvokesNothing pins the empty case explicitly
// rather than leaving it to be inferred. A traversal that invoked a check once
// with a zero subject over an empty enumeration would raise about something that
// does not exist.
func TestATraversalOverNoInstancesInvokesNothing(t *testing.T) {
	t.Parallel()

	empty := traversalFixture{}

	for _, tc := range traversalCases() {
		t.Run(string(tc.kind), func(t *testing.T) {
			t.Parallel()

			got, err := tc.run(nil, empty, traversalView{})
			if err != nil {
				t.Fatalf("the traversal returned %v over an empty enumeration nothing cancelled; "+
					"want no error", err)
			}
			if len(got) != 0 {
				t.Errorf("the traversal returned %d objection(s) over an empty enumeration; want none", len(got))
			}
		})
	}
}

// TestAScopeHoldingNoComponentsStillHasItsAttachmentsChecked is the assertion the
// scope-rooted keying exists for, and it is stated as a comparison rather than as a
// count because the count alone proves nothing.
//
// An attachment declared on a scope applies to every component that scope holds.
// Keyed by the component it reaches, a rule over it is asked once per component —
// so for a scope holding none it is asked no times at all, the rule reports
// nothing, and the build reads that silence as approval. Keyed by the scope, it is
// asked once per attachment the author wrote, whatever the scope contains.
//
// # Why both halves are run over one fixture
//
// The scope-keyed count is only meaningful beside the component-keyed count over
// the same authored shape. A non-zero scope-keyed count on its own says the
// traversal walks something; it does not show that the alternative keying would
// have walked nothing, which is the whole reason the coordinate exists. So the same
// fixture — attachments declared on a scope, and no components anywhere — is walked
// both ways and the two counts are asserted against each other.
//
// The component-keyed half is a fixture-level demonstration and not a claim about
// any shipped check: it shows what a subject set keyed after the projection to
// components yields for this shape, which is nothing.
func TestAScopeHoldingNoComponentsStillHasItsAttachmentsChecked(t *testing.T) {
	t.Parallel()

	// A scope declaring two attachments and holding no components: no nodes at all,
	// so nothing keyed by a component has anything to be asked about.
	scopeWithNoComponents := traversalFixture{
		subsystems: []AuthoredCoordinate{
			{Value: "billing.payments", SourceFile: "billing/aggregate.yaml"},
		},
		subsystemAttach: map[string][]string{
			fixtureKey("billing.payments"): {"tls", "audit"},
		},
	}
	const authoredAttachments = 2

	if len(scopeWithNoComponents.Nodes()) != 0 {
		t.Fatalf("the fixture reports %d node(s); this assertion is about a scope holding none, so a "+
			"non-empty node set would make both counts below say something else",
			len(scopeWithNoComponents.Nodes()))
	}

	scopeKeyed := readback(func(c Check[traversalView, SubsystemAttachmentSubject]) ([]Objection, error) {
		return RunOnSubsystemAttachments(nil, scopeWithNoComponents, scopeWithNoComponents, traversalView{}, c)
	}, func(s SubsystemAttachmentSubject) string {
		return s.SubsystemPath() + "|" + s.AttachmentName() + "|" + s.Locator()
	})

	componentKeyed := readback(func(c Check[traversalView, NodeAttachmentSubject]) ([]Objection, error) {
		return RunOnNodeAttachments(nil, scopeWithNoComponents, scopeWithNoComponents, traversalView{}, c)
	}, func(s NodeAttachmentSubject) string {
		return s.NodeID() + "|" + s.AttachmentName()
	})

	if len(scopeKeyed) != authoredAttachments {
		t.Errorf("the scope-keyed traversal invoked the check %d time(s) over a scope declaring %d "+
			"attachment(s) and holding no components; want %d.\n"+
			"INVARIANT: an attachment authored on a scope is checked once per attachment the author wrote, "+
			"whatever that scope holds.\n"+
			"CONSEQUENCE: a count of zero here is the failure the coordinate exists to prevent — the rule "+
			"never runs, raises nothing, and the absence of a diagnostic is indistinguishable from the "+
			"input satisfying the rule.\ngot: %v",
			len(scopeKeyed), authoredAttachments, authoredAttachments, scopeKeyed)
	}

	if len(componentKeyed) != 0 {
		t.Errorf("the component-keyed traversal invoked the check %d time(s) over a shape with no "+
			"components; want none — the comparison below rests on this half being the empty one.\ngot: %v",
			len(componentKeyed), componentKeyed)
	}

	if len(scopeKeyed) == len(componentKeyed) {
		t.Errorf("both keyings invoked the check %d time(s), so this fixture does not separate them and a "+
			"pass here would certify nothing about where the attachment is keyed", len(scopeKeyed))
	}

	// The locator half of the same property: naming the scope is what makes the
	// diagnostic actionable, and a subject carrying the right count with an empty
	// locator would satisfy every count above while pointing a reader nowhere.
	for i, got := range scopeKeyed {
		if strings.Contains(got, "|.spec.subsystems.billing.subsystems.payments.features.") {
			continue
		}
		t.Errorf("invocation %d read %q, which does not name the authored scope and attachment.\n"+
			"INVARIANT: a subject carries a locator back to the site the author wrote.\n"+
			"CONSEQUENCE: a rule over a scope-wide attachment can only be acted on where it was written. A "+
			"locator naming a component names one of the many places the attachment takes effect, and none "+
			"of those is a place anyone can edit.", i, got)
	}

	t.Logf("scope-keyed invocation count: %d over a scope declaring %d attachment(s) and holding no "+
		"components; the same shape keyed per component yields %d",
		len(scopeKeyed), authoredAttachments, len(componentKeyed))
}

// TestNoTraversalNamesAMap is the determinism guard at the source level.
//
// Enumeration order reaches output, so a traversal deriving its order from a Go
// map walk would make two builds of one input differ. The strongest available
// check is also the simplest: the file that holds every traversal names no map
// type at all, so it cannot walk one.
//
// It is a floor rather than the rule — an enumerator implementation elsewhere can
// still hand back a map-derived order, which is why that obligation is stated on
// every enumerator interface.
func TestNoTraversalNamesAMap(t *testing.T) {
	t.Parallel()

	const traversalFile = "traversal.go"

	source, err := os.ReadFile(traversalFile)
	if err != nil {
		t.Fatalf("read %s: %v", traversalFile, err)
	}
	if len(source) == 0 {
		t.Fatalf("%s is empty, so a clean result certifies nothing", traversalFile)
	}

	// Calibration: the pattern must fire on a planted occurrence, otherwise a
	// clean verdict says only that the pattern never matches anything.
	if !strings.Contains("var planted map[string]int", "map[") {
		t.Fatalf("the pattern does not match a planted map declaration, so it is not a working instrument")
	}

	if strings.Contains(string(source), "map[") {
		t.Errorf("%s names a map type.\n"+
			"INVARIANT: identical input produces byte-identical output, and enumeration order reaches "+
			"output because a check raises in the order it is invoked.\n"+
			"CONSEQUENCE: Go randomises map iteration per run, so an order derived from a map walk makes "+
			"two builds of the same input emit diagnostics in different orders. Order arrives from the "+
			"enumerators, which promise one; the traversals preserve it and never construct one.",
			traversalFile)
	}
	// The size is reported and no verdict is repeated here: a clean result and a
	// result over nothing are different facts, and only the count separates them.
	t.Logf("determinism floor: %d bytes of %s inspected", len(source), traversalFile)
}
