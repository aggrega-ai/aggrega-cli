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
// table of sixteen entries beside a generated set of sixteen kinds is exactly the
// arrangement that reads as complete while missing one.

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

	locals    map[string][]AuthoredCoordinate
	provides  map[string][]AuthoredCoordinate
	pipeline  map[string][]AuthoredCoordinate
	artifacts map[string][]AuthoredCoordinate
}

func fixtureKey(parts ...string) string { return strings.Join(parts, "\x00") }

func (f traversalFixture) Nodes() []NodeInstance            { return f.nodes }
func (f traversalFixture) Edges() []EdgeInstance            { return f.edges }
func (f traversalFixture) Boundaries() []AuthoredCoordinate { return f.boundaries }

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

func (f traversalFixture) Providers(boundary string) []string {
	return f.providers[fixtureKey(boundary)]
}

func (f traversalFixture) EmittedPaths(boundary, provider string) []string {
	return f.emittedPaths[fixtureKey(boundary, provider)]
}

// The two nodes the fixture carries, chosen so that every traversal is exercised
// against both halves of the component-locator rule in one pass.
//
//   - webNode was authored at the top level of its document and reached through a
//     synthesized container, so it has an authored site the dotted form names.
//   - apiNode sits in a container the author declared, whose authored site the
//     dotted form cannot express, so it has no locator at all — and neither does
//     anything authored inside it.
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
		boundaries: []AuthoredCoordinate{
			{Value: "prod", SourceFile: "release.yaml"},
			{Value: "dev", SourceFile: "release.yaml"},
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
			fixtureKey("prod"): {"helm", "terraform"},
			fixtureKey("dev"):  {"helm"},
		},
		emittedPaths: map[string][]string{
			fixtureKey("prod", "helm"):      {"a.yaml", "b.yaml"},
			fixtureKey("prod", "terraform"): {"main.tf.json"},
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
	}
}

// raisingCheck raises exactly one objection per invocation, so the objections a
// traversal returns are a transcript of what it invoked the check on, in order.
func raisingCheck[S Subject]() Check[traversalView, NoHostPowers, S] {
	return Check[traversalView, NoHostPowers, S]{
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
type traversalCase struct {
	kind SubjectKind
	run  func(traversalFixture, traversalView) []Objection
	want []string
}

func traversalCases() []traversalCase {
	return []traversalCase{
		{
			kind: SubjectNode,
			run: func(f traversalFixture, v traversalView) []Objection {
				return RunOnNodes(f, v, raisingCheck[NodeSubject]())
			},
			want: []string{
				"aggregate.yaml|.spec.components.web",
				"billing/aggregate.yaml|",
			},
		},
		{
			kind: SubjectNodeSocket,
			run: func(f traversalFixture, v traversalView) []Objection {
				return RunOnNodeSockets(f, f, v, raisingCheck[NodeSocketSubject]())
			},
			want: []string{
				"aggregate.yaml|.spec.components.web.uses.zebra",
				"aggregate.yaml|.spec.components.web.uses.alpha",
				"billing/aggregate.yaml|",
			},
		},
		{
			kind: SubjectEdge,
			run: func(f traversalFixture, v traversalView) []Objection {
				return RunOnEdges(f, v, raisingCheck[EdgeSubject]())
			},
			want: []string{
				"aggregate.yaml|.spec.components.web.uses.zebra",
				"billing/aggregate.yaml|",
			},
		},
		{
			kind: SubjectNodeAttachment,
			run: func(f traversalFixture, v traversalView) []Objection {
				return RunOnNodeAttachments(f, f, v, raisingCheck[NodeAttachmentSubject]())
			},
			want: []string{
				"aggregate.yaml|.spec.components.web.features.tls",
				"aggregate.yaml|.spec.components.web.features.logging",
			},
		},
		{
			kind: SubjectNodeAttachmentSocket,
			run: func(f traversalFixture, v traversalView) []Objection {
				return RunOnNodeAttachmentSockets(f, f, f, v, raisingCheck[NodeAttachmentSocketSubject]())
			},
			want: []string{
				"aggregate.yaml|.spec.components.web.features.tls.uses.issuer",
			},
		},
		{
			kind: SubjectNodeSlot,
			run: func(f traversalFixture, v traversalView) []Objection {
				return RunOnNodeSlots(f, f, v, raisingCheck[NodeSlotSubject]())
			},
			want: []string{
				"aggregate.yaml|.spec.components.web.features.sidecar",
			},
		},
		{
			kind: SubjectNodeMechanism,
			run: func(f traversalFixture, v traversalView) []Objection {
				return RunOnNodeMechanisms(f, f, v, raisingCheck[NodeMechanismSubject]())
			},
			want: []string{
				"aggregate.yaml|.spec.components.web.inject.env",
				"aggregate.yaml|.spec.components.web.inject.file",
			},
		},
		{
			kind: SubjectNodeMechanismDeliveryKey,
			run: func(f traversalFixture, v traversalView) []Objection {
				return RunOnNodeMechanismDeliveryKeys(f, f, f, v, raisingCheck[NodeMechanismDeliveryKeySubject]())
			},
			want: []string{
				"aggregate.yaml|.spec.components.web.inject.env.DB_HOST",
				"aggregate.yaml|.spec.components.web.inject.env.DB_PORT",
			},
		},
		{
			kind: SubjectNodePillarLeaf,
			run: func(f traversalFixture, v traversalView) []Objection {
				return RunOnNodePillarLeaves(f, f, f, v, raisingCheck[NodePillarLeafSubject]())
			},
			want: []string{
				"aggregate.yaml|.spec.components.web.values.app.replicas",
				"aggregate.yaml|.spec.components.web.values.app.image",
			},
		},
		{
			// The one node with no authored site still yields a locator here, and
			// that is the point: a parameter is declared at its document's spec
			// level rather than inside the component, so the component's
			// inexpressible site does not reach it.
			kind: SubjectNodeParameter,
			run: func(f traversalFixture, v traversalView) []Objection {
				return RunOnNodeParameters(f, f, v, raisingCheck[NodeParameterSubject]())
			},
			want: []string{
				"aggregate.yaml|.spec.parameters.db_size",
				"billing/aggregate.yaml|.spec.parameters.region",
			},
		},
		{
			kind: SubjectNodeAttachmentPillarLeaf,
			run: func(f traversalFixture, v traversalView) []Objection {
				return RunOnNodeAttachmentPillarLeaves(f, f, f, f, v, raisingCheck[NodeAttachmentPillarLeafSubject]())
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
			run: func(f traversalFixture, v traversalView) []Objection {
				return RunOnNodeLocalsKeys(f, f, v, raisingCheck[NodeLocalsKeySubject]())
			},
			want: []string{
				"recipes/web.yaml|.spec.locals.fullname",
				"recipes/api.yaml|.spec.locals.prefix",
			},
		},
		{
			kind: SubjectNodeProvidesKey,
			run: func(f traversalFixture, v traversalView) []Objection {
				return RunOnNodeProvidesKeys(f, f, v, raisingCheck[NodeProvidesKeySubject]())
			},
			want: []string{
				"recipes/web.yaml|.spec.provides.host",
			},
		},
		{
			kind: SubjectNodePipelineStep,
			run: func(f traversalFixture, v traversalView) []Objection {
				return RunOnNodePipelineSteps(f, f, v, raisingCheck[NodePipelineStepSubject]())
			},
			want: []string{
				"recipes/web.yaml|.spec.pipeline.render",
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
			run: func(f traversalFixture, v traversalView) []Objection {
				return RunOnBoundaryProviderPaths(f, f, f, v, raisingCheck[BoundaryProviderPathSubject]())
			},
			want: []string{
				"release.yaml|.spec.boundaries.prod",
				"release.yaml|.spec.boundaries.prod",
				"release.yaml|.spec.boundaries.prod",
			},
		},
		{
			kind: SubjectNodeArtifact,
			run: func(f traversalFixture, v traversalView) []Objection {
				return RunOnNodeArtifacts(f, f, v, raisingCheck[NodeArtifactSubject]())
			},
			want: []string{
				"recipes/web.yaml|.spec.artifacts.values-yaml",
			},
		},
	}
}

// TestEveryPullKindsTraversalIsExercised keeps the table above honest against the
// generated kind set. Without it a table of fifteen entries beside a generated set
// of sixteen kinds would report every case it contains as passing.
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
// the behavioural assertion over all sixteen traversals at once: the number of
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

			objections := tc.run(fixture, traversalView{})

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
// A coordinate keys a fact accessor; a locator names an authored site. So a
// subject whose authored site the dotted form cannot express still carries its
// coordinates in full — which is why the node with no locator at all appears here
// with its identifier intact. A check handed an empty identifier would read facts
// for the wrong instance, or for no instance, and the provenance assertion cannot
// see that: it passes on an empty locator, because for that node an empty locator
// is the correct answer.
func TestCoordinateAccessorsCarryTheEnumeratedCoordinateValues(t *testing.T) {
	t.Parallel()

	fixture := newTraversalFixture()

	assert := func(t *testing.T, got, want []string) {
		t.Helper()

		if len(got) != len(want) {
			t.Fatalf("the check was invoked %d time(s); want %d\ngot:  %v\nwant: %v",
				len(got), len(want), got, want)
		}
		for i := range got {
			if got[i] != want[i] {
				t.Errorf("invocation %d read coordinates %q; want %q.\n"+
					"INVARIANT: a subject carries the coordinate values of the instance the check was "+
					"invoked for.\n"+
					"CONSEQUENCE: every fact is keyed, so a check reads facts through these values. Wrong "+
					"or empty ones read a different instance's facts, or none, and the predicate then "+
					"decides on evidence belonging to something else.", i, got[i], want[i])
			}
		}
	}

	t.Run(string(SubjectNodeSocket), func(t *testing.T) {
		t.Parallel()

		var got []string
		_ = RunOnNodeSockets(fixture, fixture, traversalView{}, Check[traversalView, NoHostPowers, NodeSocketSubject]{
			Sentinel: errRuleBroken,
			Severity: SeverityFatal,
			Run: func(_ traversalView, s NodeSocketSubject) []Objection {
				got = append(got, s.NodeID()+"|"+s.SocketName())
				return nil
			},
		})

		assert(t, got, []string{"web|zebra", "web|alpha", "billing.api|db"})
	})

	t.Run(string(SubjectEdge), func(t *testing.T) {
		t.Parallel()

		var got []string
		_ = RunOnEdges(fixture, traversalView{}, Check[traversalView, NoHostPowers, EdgeSubject]{
			Sentinel: errRuleBroken,
			Severity: SeverityFatal,
			Run: func(_ traversalView, s EdgeSubject) []Objection {
				got = append(got, s.NodeID()+"|"+s.SocketName())
				return nil
			},
		})

		assert(t, got, []string{"web|zebra", "billing.api|db"})
	})

	// The kind whose innermost coordinate contributes no locator segment, which is
	// exactly why it needs this assertion rather than the provenance transcript:
	// all three of its invocations carry one identical locator, so nothing in that
	// transcript distinguishes them and a traversal that discarded the path would
	// read as correct there.
	t.Run(string(SubjectBoundaryProviderPath), func(t *testing.T) {
		t.Parallel()

		var got []string
		_ = RunOnBoundaryProviderPaths(fixture, fixture, fixture, traversalView{},
			Check[traversalView, NoHostPowers, BoundaryProviderPathSubject]{
				Sentinel: errRuleBroken,
				Severity: SeverityFatal,
				Run: func(_ traversalView, s BoundaryProviderPathSubject) []Objection {
					got = append(got, s.BoundaryName()+"|"+s.ProviderName()+"|"+s.EmittedPath())
					return nil
				},
			})

		assert(t, got, []string{"prod|helm|a.yaml", "prod|helm|b.yaml", "prod|terraform|main.tf.json"})
	})
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

			if got := tc.run(empty, traversalView{}); len(got) != 0 {
				t.Errorf("the traversal returned %d objection(s) over an empty enumeration; want none", len(got))
			}
		})
	}
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
