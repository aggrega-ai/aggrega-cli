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
	nodes         []NodeInstance
	edges         []EdgeInstance
	connections   []CarriedConnectionInstance
	boundaries    []AuthoredCoordinate
	subsystems    []AuthoredCoordinate
	modules       []AuthoredCoordinate
	assemblyPaths []AuthoredCoordinate

	sockets           map[string][]string
	attachments       map[string][]string
	attachmentSockets map[string][]string
	slots             map[string][]string
	mechanisms        map[string][]string
	deliveryKeys      map[string][]string
	nodePillars       map[string][]string
	nodePillarLeaves  map[string][]string
	parameters        map[string][]string
	attachLeaves      map[string][]AttachmentLeaf
	providers         map[string][]string
	assemblyProviders map[string][]string
	emittedPaths      map[string][]string
	nodeEmittedPaths  map[string][]string
	boundaryAttach    map[string][]string
	subsystemAttach   map[string][]string

	locals        map[string][]AuthoredCoordinate
	provides      map[string][]AuthoredCoordinate
	pipeline      map[string][]AuthoredCoordinate
	artifacts     map[string][]AuthoredCoordinate
	attachPillars map[string][]AuthoredCoordinate

	attachPipeline         map[string][]AuthoredCoordinate
	boundaryAttachPipeline map[string][]AuthoredCoordinate

	attachArtifacts         map[string][]AuthoredCoordinate
	boundaryAttachArtifacts map[string][]AuthoredCoordinate

	attachLocals         map[string][]AuthoredCoordinate
	boundaryAttachLocals map[string][]AuthoredCoordinate

	// boundaryAttachFields carries its own coordinate shape because a template
	// field's locator is not derivable from its label: the blocks the fields sit
	// in differ from one another, and a keyed block's own key travels inside the
	// label. The authored keys therefore arrive from the enumerator.
	boundaryAttachFields map[string][]TemplateFieldCoordinate

	// boundaryPathFields carries bare names where the axis above carries a
	// coordinate, and the difference is the carrier's rather than an
	// inconsistency: a path field's name IS its authored key, one segment at the
	// scope's own level, and it is written in the same document as the scope — so
	// the traversal spells the locator from the name and takes the file from the
	// scope.
	boundaryPathFields map[string][]string

	attachEmittedPaths         map[string][]AuthoredCoordinate
	boundaryAttachEmittedPaths map[string][]AuthoredCoordinate
}

func fixtureKey(parts ...string) string { return strings.Join(parts, "\x00") }

func (f traversalFixture) Nodes() []NodeInstance { return f.nodes }
func (f traversalFixture) Edges() []EdgeInstance { return f.edges }

func (f traversalFixture) CarriedConnections() []CarriedConnectionInstance {
	return f.connections
}
func (f traversalFixture) Boundaries() []AuthoredCoordinate { return f.boundaries }

func (f traversalFixture) AssemblyPaths() []AuthoredCoordinate { return f.assemblyPaths }

func (f traversalFixture) AssemblyGroupProviders(assemblyPath string) []string {
	return f.assemblyProviders[fixtureKey(assemblyPath)]
}
func (f traversalFixture) Subsystems() []AuthoredCoordinate { return f.subsystems }
func (f traversalFixture) Modules() []AuthoredCoordinate    { return f.modules }

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

func (f traversalFixture) AttachmentPillars(node, attachment string) []AuthoredCoordinate {
	return f.attachPillars[fixtureKey(node, attachment)]
}

func (f traversalFixture) AttachmentPillarLeaves(node, attachment, pillar string) []AttachmentLeaf {
	return f.attachLeaves[fixtureKey(node, attachment, pillar)]
}

func (f traversalFixture) LocalsKeys(node string) []AuthoredCoordinate {
	return f.locals[fixtureKey(node)]
}

func (f traversalFixture) AttachmentLocalsKeys(node, attachment string) []AuthoredCoordinate {
	return f.attachLocals[fixtureKey(node, attachment)]
}

func (f traversalFixture) BoundaryAttachmentLocalsKeys(
	boundary, attachment string,
) []AuthoredCoordinate {
	return f.boundaryAttachLocals[fixtureKey(boundary, attachment)]
}

func (f traversalFixture) BoundaryAttachmentTemplateFields(
	boundary, attachment string,
) []TemplateFieldCoordinate {
	return f.boundaryAttachFields[fixtureKey(boundary, attachment)]
}

func (f traversalFixture) BoundaryPathTemplateFields(boundary string) []string {
	return f.boundaryPathFields[fixtureKey(boundary)]
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

func (f traversalFixture) AttachmentArtifacts(node, attachment string) []AuthoredCoordinate {
	return f.attachArtifacts[fixtureKey(node, attachment)]
}

func (f traversalFixture) BoundaryAttachmentArtifacts(boundary, attachment string) []AuthoredCoordinate {
	return f.boundaryAttachArtifacts[fixtureKey(boundary, attachment)]
}

func (f traversalFixture) Providers(boundary string) []string {
	return f.providers[fixtureKey(boundary)]
}

func (f traversalFixture) EmittedPaths(boundary, provider string) []string {
	return f.emittedPaths[fixtureKey(boundary, provider)]
}

func (f traversalFixture) NodeEmittedPaths(node string) []string {
	return f.nodeEmittedPaths[fixtureKey(node)]
}

func (f traversalFixture) NodeAttachmentEmittedPaths(node, attachment string) []AuthoredCoordinate {
	return f.attachEmittedPaths[fixtureKey(node, attachment)]
}

func (f traversalFixture) BoundaryAttachmentEmittedPaths(
	boundary, attachment string,
) []AuthoredCoordinate {
	return f.boundaryAttachEmittedPaths[fixtureKey(boundary, attachment)]
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
		// Two connections on one socket, then one authored in the short form. The
		// pair on "zebra" is what the alias in the edge's identity is for: without
		// it the two would key one fact between them, and a walk keyed on the node
		// and the socket alone would visit them once. The short-form entry carries
		// an alias no key in the file spells, which is the case the locator rule
		// discriminates — its coordinate arrives in full while its locator stops at
		// the socket.
		edges: []EdgeInstance{
			{Node: webNode, Socket: "zebra", Alias: "primary"},
			{Node: webNode, Socket: "zebra", Alias: "replica"},
			{Node: apiNode, Socket: "db", Alias: "store", SynthesizedAlias: true},
		},
		// The connection set, which the edge axis above cannot express. Two entries
		// share one node, one socket and one alias and differ only in the block
		// their author wrote them in — one attachment declared inside the
		// component's own block and one declared scope-wide — so the edge axis
		// enumerates that key once and would present one of the two to every rule.
		// The set covers every carrier the vocabulary admits, so the locator rule
		// is exercised on each of its arms and on both scope nestings, and the last
		// entry carries a derived alias so that rule is exercised here too.
		connections: []CarriedConnectionInstance{
			{
				Node: webNode, Socket: "zebra", Alias: "primary", Target: "db",
				Carrier: ConnectionCarrier{
					Kind:       CarrierComponent,
					Site:       AuthoredSite{Component: "web"},
					Document:   AuthoringDocumentAggregate,
					SourceFile: "aggregate.yaml",
				},
			},
			{
				Node: webNode, Socket: "issuer", Alias: "ca", Target: "ca",
				Carrier: ConnectionCarrier{
					Kind:       CarrierAttachment,
					Attachment: "tls",
					Site:       AuthoredSite{Component: "web"},
					Document:   AuthoringDocumentAggregate,
					SourceFile: "aggregate.yaml",
				},
			},
			{
				Node: webNode, Socket: "issuer", Alias: "ca", Target: "ca",
				Carrier: ConnectionCarrier{
					Kind:       CarrierAttachment,
					Attachment: "tls",
					Site:       AuthoredSite{ScopePath: "billing.payments"},
					Document:   AuthoringDocumentAggregate,
					SourceFile: "billing/aggregate.yaml",
				},
			},
			{
				Node: webNode, Socket: "zebra", Alias: "spare", Target: "cache-standby",
				Carrier: ConnectionCarrier{
					Kind:       CarrierDeployment,
					Site:       AuthoredSite{ScopePath: "prod/edge", Component: "web"},
					Document:   AuthoringDocumentRelease,
					SourceFile: "release.yaml",
				},
			},
			{
				Node: apiNode, Socket: "db", Alias: "store", Target: "store",
				SynthesizedAlias: true,
				Carrier: ConnectionCarrier{
					Kind:       CarrierComponent,
					Site:       AuthoredSite{ScopePath: "billing", Component: "api"},
					Document:   AuthoringDocumentAggregate,
					SourceFile: "billing/aggregate.yaml",
				},
			},
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
		// One nested module and one top-level module, reverse-alphabetical so a
		// sort would be visible. The nested one carries the file that DECLARES it,
		// which is the importing module's own document rather than the root's, and
		// that pairing is what proves the composition axis is not a third copy of
		// the two scope axes above: its levels sit one per document, so its
		// locator names the level that document spells and no other.
		modules: []AuthoredCoordinate{
			{Value: "data", SourceFile: "aggregate.yaml"},
			{Value: "billing.ledger", SourceFile: "billing/aggregate.yaml"},
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
		// An attachment's pillar is the key its configuration is written under,
		// and its leaf is a dotted path inside that key — which is why this axis
		// is spelled differently from the node pillars above, where the pillar is
		// a key within the block rather than the block itself. The two are not a
		// pair to be tidied into agreement: they are different axes, and a pillar
		// named anything but an authored key would put this walk's locator on a
		// site no document contains.
		//
		// One attachment carrying BOTH pillars, from two different documents, and
		// that pairing is the whole reason this axis carries a file at all. The
		// developer wrote the values in the application document; the operator
		// wrote the infrastructure configuration on the deployment, which is the
		// only surface whose attachment block admits one. A fixture keeping both
		// pillars in one document would produce the same transcript whether the
		// file came from the pillar or from the node, so it could not tell a
		// working axis from the defect this pairing exists to catch.
		attachPillars: map[string][]AuthoredCoordinate{
			fixtureKey("web", "tls"): {
				{Value: "values", SourceFile: "aggregate.yaml"},
				{Value: "infra", SourceFile: "release-flat.yaml"},
			},
		},
		// Each leaf carries the site the entry that supplied it was written at.
		// Both here are the component's own block, which is the shape a locator
		// built from the node would also have produced — so this row proves the
		// axis still spells the ordinary case, and the authored-site table beside
		// it proves the two shapes the node's own segments cannot express.
		attachLeaves: map[string][]AttachmentLeaf{
			fixtureKey("web", "tls", "values"): {
				{Value: "app.issuer", Site: AuthoredSite{Component: "web"}},
			},
			fixtureKey("web", "tls", "infra"): {
				{Value: "pool_size", Site: AuthoredSite{Component: "web"}},
			},
		},
		providers: map[string][]string{
			fixtureKey("prod/edge"): {"helm", "terraform"},
			fixtureKey("dev"):       {"helm"},
		},
		// Two assembly groups at one evaluated path and one at another. The pair is
		// what the kind exists to make visible — several providers assembling into
		// one directory — and the lone group is what tells a walk from an invocation
		// handed only the contested path.
		assemblyPaths: []AuthoredCoordinate{
			{Value: "dist/shared", SourceFile: "release.yaml"},
			{Value: "dist/solo", SourceFile: "release.yaml"},
		},
		assemblyProviders: map[string][]string{
			fixtureKey("dist/shared"): {"helm", "terraform"},
			fixtureKey("dist/solo"):   {"compose"},
		},
		emittedPaths: map[string][]string{
			fixtureKey("prod/edge", "helm"):      {"a.yaml", "b.yaml"},
			fixtureKey("prod/edge", "terraform"): {"main.tf.json"},
		},
		// Two names on one node and one on the other, so the transcript shows both
		// the several-per-node case and the two nodes' different authored sites —
		// which is the whole of what a node-rooted transcript can distinguish here,
		// since an emitted name contributes no segment of its own.
		nodeEmittedPaths: map[string][]string{
			fixtureKey("web"):         {"deployment.yaml", "service.yaml"},
			fixtureKey("billing.api"): {"main.tf.json"},
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

		// The SAME output name under two attachments on one node, which is what the
		// attachment coordinate exists to separate. Independently authored blocks may
		// each resolve an entry to one filename, nothing declares output names unique
		// across documents, and a key carrying only the node and the artifact would
		// answer for whichever was asked last.
		//
		// The name is spelled with the dialect's separator in it, where the node's own
		// axis above uses a hyphenated name, and the pair is deliberate rather than an
		// inconsistency to be tidied. An output name is a filename, so carrying the
		// separator is the ordinary case on this axis and the truncated locator is
		// what these entries exercise; the node's own entry keeps a separator-free
		// name so the untruncated locator stays exercised too. Neither shape proves
		// the other.
		attachArtifacts: map[string][]AuthoredCoordinate{
			fixtureKey("web", "tls"):     {{Value: "values.yaml", SourceFile: "featurerecipes/tls.yaml"}},
			fixtureKey("web", "logging"): {{Value: "values.yaml", SourceFile: "featurerecipes/logging.yaml"}},
		},
		// One of the boundary's two attachments projects an artifact and the other
		// projects none, so a transcript shows the walk entering an attachment that
		// declares no artifacts block and yielding nothing there rather than
		// stopping.
		boundaryAttachArtifacts: map[string][]AuthoredCoordinate{
			fixtureKey("prod/edge", "shared-db"): {{Value: "dashboard.json", SourceFile: "featurerecipes/shared-db.yaml"}},
		},

		// The SAME local key under two attachments on one node, which is what the
		// attachment coordinate exists to separate on this axis: two FeatureRecipes
		// write their locals with no knowledge of each other, nothing declares local
		// names unique across documents, and a key carrying only the node and the
		// local would answer for whichever was asked last. The files differ, so the
		// provenance transcript tells the two carriers apart as well as the readback.
		//
		// One attachment declares two locals in a deliberately non-alphabetical order,
		// which is what shows the traversal preserving author order — the order the
		// readable prefix at each entry is taken from.
		attachLocals: map[string][]AuthoredCoordinate{
			fixtureKey("web", "tls"): {
				{Value: "issuer", SourceFile: "featurerecipes/tls.yaml"},
				{Value: "fullname", SourceFile: "featurerecipes/tls.yaml"},
			},
			fixtureKey("web", "logging"): {{Value: "fullname", SourceFile: "featurerecipes/logging.yaml"}},
		},
		// One of the boundary's two attachments declares a locals block and the other
		// declares none, so a transcript shows the walk entering an attachment that
		// declares no block and yielding nothing there rather than stopping.
		boundaryAttachLocals: map[string][]AuthoredCoordinate{
			fixtureKey("prod/edge", "shared-db"): {{Value: "endpoint", SourceFile: "featurerecipes/shared-db.yaml"}},
		},
		// The template-field axis on the same carrier, and the three entries are
		// chosen for what they separate rather than for coverage of the blocks.
		//
		// The first sits at the spec's top level, the second inside a keyed block —
		// so a traversal splicing the label into the locator instead of using the
		// authored keys would spell .spec.provides[dsn] and fail here. The third is
		// keyed by a name carrying the locator dialect's own separator: its locator
		// STOPS at the block, which is the established shape for an authored key the
		// dialect cannot address, and a transcript showing it is what keeps that
		// behaviour visible rather than looking like a dropped segment.
		boundaryAttachFields: map[string][]TemplateFieldCoordinate{
			fixtureKey("prod/edge", "shared-db"): {
				{
					Value:        "valuesMapping",
					SourceFile:   "featurerecipes/shared-db.yaml",
					AuthoredKeys: []string{"valuesMapping"},
				},
				{
					Value:        "provides[dsn]",
					SourceFile:   "featurerecipes/shared-db.yaml",
					AuthoredKeys: []string{"provides", "dsn"},
				},
				{
					Value:        "artifacts[cm.yaml]",
					SourceFile:   "featurerecipes/shared-db.yaml",
					AuthoredKeys: []string{"artifacts", "cm.yaml"},
				},
			},
		},
		// The path fields the same scope writes. Both are carried because the two
		// are separate subjects over one scope, which is what shows the axis is
		// keyed by the field and not by the scope — a traversal invoking once per
		// scope would report one record here and pass every count-shaped check.
		boundaryPathFields: map[string][]string{
			fixtureKey("prod/edge"): {"directory", "subDirectory"},
		},

		// The SAME emitted name under two attachments on one node, for the reason the
		// artifact axis above gives: two FeatureRecipes may each place a file under one
		// name, nothing declares emitted names unique across documents, and a key
		// carrying only the node and the name would answer for whichever was asked last.
		//
		// One attachment emits two names and the other one, so the transcript shows the
		// several-per-carrier case as well as the shared name. The files differ, which is
		// what lets a provenance transcript tell the two carriers apart at all — an
		// emitted name contributes no locator segment.
		attachEmittedPaths: map[string][]AuthoredCoordinate{
			fixtureKey("web", "tls"): {
				{Value: "deployment.yaml", SourceFile: "featurerecipes/tls.yaml"},
				{Value: "secret.yaml", SourceFile: "featurerecipes/tls.yaml"},
			},
			fixtureKey("web", "logging"): {
				{Value: "deployment.yaml", SourceFile: "featurerecipes/logging.yaml"},
			},
		},
		// One of the boundary's two attachments emits and the other emits nothing, so a
		// transcript shows the walk entering an attachment whose render placed no file
		// and yielding nothing there rather than stopping.
		boundaryAttachEmittedPaths: map[string][]AuthoredCoordinate{
			fixtureKey("prod/edge", "shared-db"): {
				{Value: "statefulset.yaml", SourceFile: "featurerecipes/shared-db.yaml"},
			},
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
			// The only kind whose subjects contribute no locator segment: an assembly
			// path is evaluated output and a provider is selected, so neither is a key
			// the deployment document holds and naming one would spell a path the file
			// does not contain. The three findings are told apart by their detail
			// rather than their position, which is what this expectation pins.
			kind: SubjectAssemblyPathProvider,
			run: func(cancelled Cancellation, f traversalFixture, v traversalView) ([]Objection, error) {
				return RunOnAssemblyPathProviders(cancelled, f, f, v, raisingCheck[AssemblyPathProviderSubject]())
			},
			want: []string{
				"release.yaml|",
				"release.yaml|",
				"release.yaml|",
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
			// The two long-form connections name their own alias, because the author
			// wrote it as a key. The short-form one stops at the socket: its alias is
			// the target's own name, which appears in the file as an array element and
			// not as a key, so naming it would produce a path no file contains.
			kind: SubjectEdge,
			run: func(cancelled Cancellation, f traversalFixture, v traversalView) ([]Objection, error) {
				return RunOnEdges(cancelled, f, v, raisingCheck[EdgeSubject]())
			},
			want: []string{
				"aggregate.yaml|.spec.components.web.uses.zebra.primary",
				"aggregate.yaml|.spec.components.web.uses.zebra.replica",
				"billing/aggregate.yaml|.spec.subsystems.billing.components.api.uses.db",
			},
		},
		{
			// One socket and alias on one node, drawn four times by four carriers.
			// Each finding names the block its own author wrote — the component's
			// own dependency block, the attachment block inside it, the attachment
			// block of a scope, and the deployment's entry for the component — and
			// each carries that block's own file. Naming the consuming component's
			// dependency block for all four would spell three paths no file
			// contains.
			kind: SubjectCarriedConnection,
			run: func(cancelled Cancellation, f traversalFixture, v traversalView) ([]Objection, error) {
				return RunOnCarriedConnections(cancelled, f, v, raisingCheck[CarriedConnectionSubject]())
			},
			want: []string{
				"aggregate.yaml|.spec.components.web.uses.zebra.primary",
				"aggregate.yaml|.spec.components.web.features.tls.uses.issuer.ca",
				"billing/aggregate.yaml|.spec.subsystems.billing.subsystems.payments.features.tls.uses.issuer.ca",
				"release.yaml|.spec.boundaries.prod.boundaries.edge.components.web.uses.zebra.spare",
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
			// The pillar is itself the key the attachment's configuration is
			// written under, so it contributes that segment and the locator
			// spells no further key ahead of it. A second key spliced in here
			// would name .features.tls.values.values.app.issuer, which the
			// authored-site walk beside this table refuses.
			//
			// Two invocations over ONE attachment on ONE node, whose locators
			// differ only in the pillar segment and whose FILES differ outright.
			// That is the property the axis carries a file for: an attachment's
			// values are the developer's and are written in the application
			// document, its infrastructure configuration is the operator's and is
			// written on the deployment, and a file taken from the node would
			// attribute the second to a document whose attachment block cannot
			// hold the key its locator names.
			kind: SubjectNodeAttachmentPillarLeaf,
			run: func(cancelled Cancellation, f traversalFixture, v traversalView) ([]Objection, error) {
				return RunOnNodeAttachmentPillarLeaves(cancelled, f, f, f, f, v, raisingCheck[NodeAttachmentPillarLeafSubject]())
			},
			want: []string{
				"aggregate.yaml|.spec.components.web.features.tls.values.app.issuer",
				"release-flat.yaml|.spec.components.web.features.tls.infra.pool_size",
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
			// Two attachments on one node, each declaring a local named `fullname`
			// in its own document, plus a second local above one of them. The
			// locators are identical in their dotted part and differ only in the
			// file, which is the shape the attachment coordinate exists to
			// separate — and the pair of `fullname` entries is what a
			// node-and-key-only kind would have collapsed. The order inside the
			// first attachment is the authored one and not alphabetical, so a
			// traversal that sorted would fail here.
			kind: SubjectNodeAttachmentLocalsKey,
			run: func(cancelled Cancellation, f traversalFixture, v traversalView) ([]Objection, error) {
				return RunOnNodeAttachmentLocalsKeys(cancelled, f, f, f, v,
					raisingCheck[NodeAttachmentLocalsKeySubject]())
			},
			want: []string{
				"featurerecipes/tls.yaml|.spec.locals.issuer",
				"featurerecipes/tls.yaml|.spec.locals.fullname",
				"featurerecipes/logging.yaml|.spec.locals.fullname",
			},
		},
		{
			// The boundary-rooted twin: one authored block reached once, from the
			// scope it was declared at rather than once per component the boundary
			// holds. The boundary contributes no locator segment, because the entry
			// is authored in the FeatureRecipe and a boundary key spliced onto that
			// path would name something that document does not have.
			kind: SubjectBoundaryAttachmentLocalsKey,
			run: func(cancelled Cancellation, f traversalFixture, v traversalView) ([]Objection, error) {
				return RunOnBoundaryAttachmentLocalsKeys(cancelled, f, f, f, v,
					raisingCheck[BoundaryAttachmentLocalsKeySubject]())
			},
			want: []string{
				"featurerecipes/shared-db.yaml|.spec.locals.endpoint",
			},
		},
		{
			// The template-field axis on the same carrier. The three records show the
			// locator spelled from the AUTHORED KEYS rather than from the label: a
			// top-level field, a field inside a keyed block — which a traversal splicing
			// the label in would spell .spec.provides[dsn] and fail here — and a field
			// whose key carries the dialect's own separator, which positions at the
			// block and stops. That last is the established shape for an authored key
			// the dialect cannot address, and it is why the rule over this kind names
			// the field in its detail.
			kind: SubjectBoundaryAttachmentTemplateField,
			run: func(cancelled Cancellation, f traversalFixture, v traversalView) ([]Objection, error) {
				return RunOnBoundaryAttachmentTemplateFields(cancelled, f, f, f, v,
					raisingCheck[BoundaryAttachmentTemplateFieldSubject]())
			},
			want: []string{
				"featurerecipes/shared-db.yaml|.spec.valuesMapping",
				"featurerecipes/shared-db.yaml|.spec.provides.dsn",
				"featurerecipes/shared-db.yaml|.spec.artifacts",
			},
		},
		{
			// The path-template axis over the same scope. Both records position
			// INSIDE the deployment document, under the scope's own key with the
			// container's key repeated for the nesting — which is the half the
			// axis above deliberately does not do, because its field is written in
			// a different document from the scope that reaches it. Two records for
			// one scope is what shows the traversal is invoked per field.
			kind: SubjectBoundaryPathTemplateField,
			run: func(cancelled Cancellation, f traversalFixture, v traversalView) ([]Objection, error) {
				return RunOnBoundaryPathTemplateFields(cancelled, f, f, v,
					raisingCheck[BoundaryPathTemplateFieldSubject]())
			},
			want: []string{
				"release.yaml|.spec.boundaries.prod.boundaries.edge.directory",
				"release.yaml|.spec.boundaries.prod.boundaries.edge.subDirectory",
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
			// Three invocations over two nodes, and the provenance separates them
			// only by node: an emitted name is output rather than an authored key,
			// so the two names one node emitted carry one identical locator and the
			// third carries the nested site its own node was written at. What tells
			// the first two apart is their coordinates, which this transcript cannot
			// see — that is what
			// TestCoordinateAccessorsCarryTheEnumeratedCoordinateValues is for.
			kind: SubjectNodePath,
			run: func(cancelled Cancellation, f traversalFixture, v traversalView) ([]Objection, error) {
				return RunOnNodeEmittedPaths(cancelled, f, f, v, raisingCheck[NodePathSubject]())
			},
			want: []string{
				"aggregate.yaml|.spec.components.web",
				"aggregate.yaml|.spec.components.web",
				"billing/aggregate.yaml|.spec.subsystems.billing.components.api",
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
			// Two invocations whose locators are identical and stop at the artifacts
			// block, differing only in the file — and both halves of that are the
			// point. The block is where they stop because each entry resolves to a
			// filename, which carries the dialect's separator as one authored key the
			// dialect cannot address; the file differs because the entry is authored
			// on the attachment's own FeatureRecipe rather than on the Aggregate the
			// attachment was written in. The two entries resolve to ONE name, so this
			// transcript can tell them apart only by their files — which is exactly
			// why the attachment is a coordinate, and what the readback below shows.
			kind: SubjectNodeAttachmentArtifact,
			run: func(cancelled Cancellation, f traversalFixture, v traversalView) ([]Objection, error) {
				return RunOnNodeAttachmentArtifacts(cancelled, f, f, f, v,
					raisingCheck[NodeAttachmentArtifactSubject]())
			},
			want: []string{
				"featurerecipes/tls.yaml|.spec.artifacts",
				"featurerecipes/logging.yaml|.spec.artifacts",
			},
		},
		{
			// One invocation from two enumerated attachments on one boundary, and
			// none at all from the second boundary: the walk enters an attachment
			// that projects no artifact, yields nothing there, and carries on. The
			// locator names the FeatureRecipe's own artifacts block and stops, so the
			// boundary contributes no segment — a boundary key spliced onto a
			// FeatureRecipe-rooted locator would name a path that document does not
			// have.
			kind: SubjectBoundaryAttachmentArtifact,
			run: func(cancelled Cancellation, f traversalFixture, v traversalView) ([]Objection, error) {
				return RunOnBoundaryAttachmentArtifacts(cancelled, f, f, f, v,
					raisingCheck[BoundaryAttachmentArtifactSubject]())
			},
			want: []string{
				"featurerecipes/shared-db.yaml|.spec.artifacts",
			},
		},
		{
			// Three invocations from two attachments on one node, and the locators
			// carry no segment at all: an emitted name is output, so it contributes
			// none, and the FeatureRecipe's own spec is not an authored key path the
			// dialect walks into either. What the transcript CAN show is the file,
			// which is the FeatureRecipe rather than the Aggregate the attachment was
			// written in — so a rule's finding sends an author to the document the
			// colliding blocks are in.
			//
			// The two carriers share one emitted name and are told apart here only by
			// their files; what tells them apart as SUBJECTS is the attachment
			// coordinate, which this transcript cannot see — that is what the
			// readback below is for.
			kind: SubjectNodeAttachmentPath,
			run: func(cancelled Cancellation, f traversalFixture, v traversalView) ([]Objection, error) {
				return RunOnNodeAttachmentEmittedPaths(cancelled, f, f, f, v,
					raisingCheck[NodeAttachmentPathSubject]())
			},
			want: []string{
				"featurerecipes/tls.yaml|",
				"featurerecipes/tls.yaml|",
				"featurerecipes/logging.yaml|",
			},
		},
		{
			// One invocation from two enumerated attachments on one boundary, and none
			// at all from the second boundary: the walk enters an attachment whose
			// render placed no file, yields nothing there, and carries on. The
			// boundary contributes no segment, for the reason its artifact twin above
			// gives — a boundary key spliced onto a FeatureRecipe-rooted locator would
			// name a path that document does not have.
			kind: SubjectBoundaryAttachmentPath,
			run: func(cancelled Cancellation, f traversalFixture, v traversalView) ([]Objection, error) {
				return RunOnBoundaryAttachmentEmittedPaths(cancelled, f, f, f, v,
					raisingCheck[BoundaryAttachmentPathSubject]())
			},
			want: []string{
				"featurerecipes/shared-db.yaml|",
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
		{
			// The composition axis, and the contrast with the two scope cases
			// above is the whole point of the transcript. A scope's every level
			// appears under the container's key, because the levels are keys in
			// one document; a module's do not, because they are keys in one
			// document per level — so the nested module locates at
			// .spec.aggregates.ledger in the document that imported it, never at
			// .spec.aggregates.billing.aggregates.ledger, which is a path no file
			// contains. Each invocation carries the declaring file, which for the
			// nested module is the importing module's own.
			kind: SubjectModule,
			run: func(cancelled Cancellation, f traversalFixture, v traversalView) ([]Objection, error) {
				return RunOnModules(cancelled, f, v, raisingCheck[ModuleSubject]())
			},
			want: []string{
				"aggregate.yaml|.spec.aggregates.data",
				"billing/aggregate.yaml|.spec.aggregates.ledger",
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

		// The Manifest's annotation-binding kind reads through its push entry, so
		// the keys come from the caller. Two are presented for the reason the
		// parameter case states: one instance cannot tell a walk from an
		// invocation that ignored what it was handed. The two keys are
		// deliberately not prefixes of one another, so a readback that truncated
		// or confused them would not still compare equal.
		{
			kind: SubjectDocumentAnnotationBinding,
			seam: seamTraversal,
			read: func(_ traversalFixture) []string {
				return readback(func(
					c Check[traversalView, DocumentAnnotationBindingSubject],
				) ([]Objection, error) {
					return RunOnDocumentAnnotationBindings(
						documentFile,
						[]string{"feature.example/rewrite-host", "feature.example/tls"},
						traversalView{}, c,
					), nil
				}, func(s DocumentAnnotationBindingSubject) string {
					return s.SourceFile() + "|" + s.AnnotationBindingKey()
				})
			},
			want: []string{
				documentFile + "|feature.example/rewrite-host",
				documentFile + "|feature.example/tls",
			},
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
		// The assembly group's kind is rooted at what the build produced rather than
		// at anything a document declares, so both its coordinates are read back
		// together: a path alone cannot tell the two groups contesting it apart.
		{
			kind: SubjectAssemblyPathProvider,
			seam: seamTraversal,
			read: func(f traversalFixture) []string {
				return readback(func(c Check[traversalView, AssemblyPathProviderSubject]) ([]Objection, error) {
					return RunOnAssemblyPathProviders(nil, f, f, traversalView{}, c)
				}, func(s AssemblyPathProviderSubject) string {
					return s.AssemblyPath() + "|" + s.ProviderName()
				})
			},
			want: []string{"dist/shared|helm", "dist/shared|terraform", "dist/solo|compose"},
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
			want: []string{"web|tls|values|app.issuer", "web|tls|infra|pool_size"},
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

		// The two attachment-borne locals kinds, whose reason for existing is exactly
		// what these readbacks show. Both attachments on the node declare a local
		// named `fullname`, so a traversal that dropped the attachment would produce
		// two identical records while every declaration assertion stayed green — and
		// the node's own readback above carries a `fullname` too, which is the
		// collision a single node-and-key kind would have folded into one answer.
		{
			kind: SubjectNodeAttachmentLocalsKey,
			seam: seamTraversal,
			read: func(f traversalFixture) []string {
				return readback(func(c Check[traversalView, NodeAttachmentLocalsKeySubject]) ([]Objection, error) {
					return RunOnNodeAttachmentLocalsKeys(nil, f, f, f, traversalView{}, c)
				}, func(s NodeAttachmentLocalsKeySubject) string {
					return s.NodeID() + "|" + s.AttachmentName() + "|" + s.LocalsKey()
				})
			},
			want: []string{"web|tls|issuer", "web|tls|fullname", "web|logging|fullname"},
		},
		{
			kind: SubjectBoundaryAttachmentLocalsKey,
			seam: seamTraversal,
			read: func(f traversalFixture) []string {
				return readback(func(c Check[traversalView, BoundaryAttachmentLocalsKeySubject]) ([]Objection, error) {
					return RunOnBoundaryAttachmentLocalsKeys(nil, f, f, f, traversalView{}, c)
				}, func(s BoundaryAttachmentLocalsKeySubject) string {
					return s.BoundaryPath() + "|" + s.AttachmentName() + "|" + s.LocalsKey()
				})
			},
			want: []string{"prod/edge|shared-db|endpoint"},
		},
		{
			kind: SubjectBoundaryAttachmentTemplateField,
			seam: seamTraversal,
			read: func(f traversalFixture) []string {
				return readback(func(c Check[traversalView, BoundaryAttachmentTemplateFieldSubject]) ([]Objection, error) {
					return RunOnBoundaryAttachmentTemplateFields(nil, f, f, f, traversalView{}, c)
				}, func(s BoundaryAttachmentTemplateFieldSubject) string {
					return s.BoundaryPath() + "|" + s.AttachmentName() + "|" + s.TemplateField()
				})
			},
			want: []string{
				"prod/edge|shared-db|valuesMapping",
				"prod/edge|shared-db|provides[dsn]",
				"prod/edge|shared-db|artifacts[cm.yaml]",
			},
		},
		{
			kind: SubjectBoundaryPathTemplateField,
			seam: seamTraversal,
			read: func(f traversalFixture) []string {
				return readback(func(c Check[traversalView, BoundaryPathTemplateFieldSubject]) ([]Objection, error) {
					return RunOnBoundaryPathTemplateFields(nil, f, f, traversalView{}, c)
				}, func(s BoundaryPathTemplateFieldSubject) string {
					return s.BoundaryPath() + "|" + s.PathTemplateField()
				})
			},
			want: []string{
				"prod/edge|directory",
				"prod/edge|subDirectory",
			},
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

		// The kind whose reason for existing is exactly what this readback shows: the
		// two invocations carry the same node and the same output name, and the
		// attachment is the only thing that separates them. A traversal that dropped
		// the attachment would produce two identical records here, while every
		// declaration assertion stayed green — and the provenance transcript above
		// could not see it either, because both locators stop at the same block.
		{
			kind: SubjectNodeAttachmentArtifact,
			seam: seamTraversal,
			read: func(f traversalFixture) []string {
				return readback(func(c Check[traversalView, NodeAttachmentArtifactSubject]) ([]Objection, error) {
					return RunOnNodeAttachmentArtifacts(nil, f, f, f, traversalView{}, c)
				}, func(s NodeAttachmentArtifactSubject) string {
					return s.NodeID() + "|" + s.AttachmentName() + "|" + s.ArtifactName()
				})
			},
			want: []string{"web|tls|values.yaml", "web|logging|values.yaml"},
		},
		{
			kind: SubjectBoundaryAttachmentArtifact,
			seam: seamTraversal,
			read: func(f traversalFixture) []string {
				return readback(func(c Check[traversalView, BoundaryAttachmentArtifactSubject]) ([]Objection, error) {
					return RunOnBoundaryAttachmentArtifacts(nil, f, f, f, traversalView{}, c)
				}, func(s BoundaryAttachmentArtifactSubject) string {
					return s.BoundaryPath() + "|" + s.AttachmentName() + "|" + s.ArtifactName()
				})
			},
			want: []string{"prod/edge|shared-db|dashboard.json"},
		},

		// The emitted-name kind whose reason for existing this readback shows directly:
		// the first and third records carry the same node and the same output name, and
		// the attachment is the only thing separating them. A traversal that dropped the
		// attachment would produce two identical records while every declaration
		// assertion stayed green — and the provenance transcript above could not see it,
		// because an emitted name contributes no locator segment at all.
		{
			kind: SubjectNodeAttachmentPath,
			seam: seamTraversal,
			read: func(f traversalFixture) []string {
				return readback(func(c Check[traversalView, NodeAttachmentPathSubject]) ([]Objection, error) {
					return RunOnNodeAttachmentEmittedPaths(nil, f, f, f, traversalView{}, c)
				}, func(s NodeAttachmentPathSubject) string {
					return s.NodeID() + "|" + s.AttachmentName() + "|" + s.EmittedPath()
				})
			},
			want: []string{
				"web|tls|deployment.yaml",
				"web|tls|secret.yaml",
				"web|logging|deployment.yaml",
			},
		},
		{
			kind: SubjectBoundaryAttachmentPath,
			seam: seamTraversal,
			read: func(f traversalFixture) []string {
				return readback(func(c Check[traversalView, BoundaryAttachmentPathSubject]) ([]Objection, error) {
					return RunOnBoundaryAttachmentEmittedPaths(nil, f, f, f, traversalView{}, c)
				}, func(s BoundaryAttachmentPathSubject) string {
					return s.BoundaryPath() + "|" + s.AttachmentName() + "|" + s.EmittedPath()
				})
			},
			want: []string{"prod/edge|shared-db|statefulset.yaml"},
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
				}, func(s EdgeSubject) string {
					return s.NodeID() + "|" + s.SocketName() + "|" + s.AliasName()
				})
			},
			// The third entry's alias contributes no locator segment and still arrives
			// here in full, which is the property the provenance transcript cannot see:
			// there its locator is the correct answer either way.
			want: []string{"web|zebra|primary", "web|zebra|replica", "billing.api|db|store"},
		},

		{
			// Every coordinate of the widest kind, read back through the traversal.
			// The four entries sharing one node, socket and alias are what this
			// assertion exists for: they are one entry on the edge axis and four
			// here, so a traversal that dropped the carrier would read as correct
			// on every other assertion and present one connection's content for
			// all four.
			kind: SubjectCarriedConnection,
			seam: seamTraversal,
			read: func(f traversalFixture) []string {
				return readback(func(c Check[traversalView, CarriedConnectionSubject]) ([]Objection, error) {
					return RunOnCarriedConnections(nil, f, traversalView{}, c)
				}, func(s CarriedConnectionSubject) string {
					return s.NodeID() + "|" + s.CarrierKind() + "|" + s.CarrierAttachment() +
						"|" + s.SocketName() + "|" + s.AliasName() + "|" + s.TargetNodeID()
				})
			},
			want: []string{
				"web|component||zebra|primary|db",
				"web|attachment|tls|issuer|ca|ca",
				"web|attachment|tls|issuer|ca|ca",
				"web|deployment||zebra|spare|cache-standby",
				"billing.api|component||db|store|store",
			},
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
		// The node-rooted kind whose innermost coordinate contributes no locator
		// segment, which is why it needs this assertion rather than the provenance
		// transcript: the two names one node emitted carry one identical locator, so
		// nothing in that transcript distinguishes them and a traversal that
		// discarded the name would read as correct there.
		{
			kind: SubjectNodePath,
			seam: seamTraversal,
			read: func(f traversalFixture) []string {
				return readback(func(c Check[traversalView, NodePathSubject]) ([]Objection, error) {
					return RunOnNodeEmittedPaths(nil, f, f, traversalView{}, c)
				}, func(s NodePathSubject) string {
					return s.NodeID() + "|" + s.EmittedPath()
				})
			},
			want: []string{"web|deployment.yaml", "web|service.yaml", "billing.api|main.tf.json"},
		},
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

		// The composition axis reads back its coordinate whole, for the reason the
		// scope axes do: a module's identity is the dotted prefix its components
		// carry, two modules at different depths may carry one name, and a check
		// reading a bare name would key facts that cannot tell them apart. It is
		// the coordinate rather than the locator that carries the whole path here,
		// and the two differ on this axis more than on any other — the locator
		// names only the level the declaring document spells.
		{
			kind: SubjectModule,
			seam: seamTraversal,
			read: func(f traversalFixture) []string {
				return readback(func(c Check[traversalView, ModuleSubject]) ([]Objection, error) {
					return RunOnModules(nil, f, traversalView{}, c)
				}, func(s ModuleSubject) string {
					return s.ModulePath()
				})
			},
			want: []string{"data", "billing.ledger"},
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
