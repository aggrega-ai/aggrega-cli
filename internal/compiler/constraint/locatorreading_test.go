// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package constraint

import (
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/pkg/yamlpos"
)

// The Recipe whose artifacts block holds an entry whose key is a filename.
//
// It is a real authoring shape rather than a contrived one: an artifacts key is
// an output filename, so a dot in it is the ordinary case and not an edge one.
const locatorReadingRecipe = `apiVersion: aggrega.ai/v1beta1
kind: Recipe
metadata:
  name: example/app
spec:
  artifacts:
    policy.yaml: |
      rule: deny
`

// TestALocatorHasExactlyOneReading is the injectivity assertion: two different
// segment lists cannot arrive at one locator.
//
// The pair is the one the dialect makes possible. An artifact's output name is a
// filename, so "policy.yaml" is ONE authored key; a path descending through a key
// "policy" to a key "yaml" is TWO. Joined with the separator both would read
// ".spec.artifacts.policy.yaml", and a reader handed that string — a person, or a
// walk recovering a position — has no way to tell which of the two it names.
//
// INVARIANT: splitting a locator on the separator yields exactly the segments it
// was assembled from.
// CONSEQUENCE: without it, a position is a guess. The author is pointed at a key
// the document does not contain, and cannot tell whether the fault is in the
// diagnostic or in the file.
func TestALocatorHasExactlyOneReading(t *testing.T) {
	t.Parallel()

	oneKey := documentSpecSegments(authoredKeyArtifacts, "policy.yaml")
	twoKeys := documentSpecSegments(authoredKeyArtifacts, "policy", "yaml")

	fromOneKey := authoredLocator(oneKey...)
	fromTwoKeys := authoredLocator(twoKeys...)

	if fromOneKey == fromTwoKeys {
		t.Fatalf("the segments %q and %q both produced %q, so one locator serves two readings",
			oneKey, twoKeys, fromOneKey)
	}

	// Stated as its own assertion rather than left to the inequality above: a
	// change producing two DIFFERENT wrong answers would satisfy that inequality
	// while naming nothing either document contains.
	if want := ".spec.artifacts.policy.yaml"; fromTwoKeys != want {
		t.Errorf("a path through two keys = %q; want %q — the reading that names a real "+
			"nesting is the one the dialect keeps", fromTwoKeys, want)
	}
	if want := ".spec.artifacts"; fromOneKey != want {
		t.Errorf("a single key carrying the separator = %q; want %q — the deepest site the "+
			"document can be walked to", fromOneKey, want)
	}

	// Every segment of what is emitted survives a round trip through the dialect,
	// which is the mechanical form of the invariant.
	for _, locator := range []string{fromOneKey, fromTwoKeys} {
		for _, segment := range strings.Split(strings.TrimPrefix(locator, "."), ".") {
			if strings.Contains(segment, locatorSeparator) {
				t.Errorf("the locator %q carries a segment %q holding the separator, so splitting "+
					"it does not recover what it was built from", locator, segment)
			}
		}
	}
}

// TestTruncatingAnUnaddressableKeyMovesNoPosition is the other half of the trade,
// and it is measured against the real position walk rather than argued.
//
// Stopping at the last addressable segment costs depth, so the question is what
// that costs a reader. It costs nothing: the walk that recovers a position from a
// locator descends by literal keys, so it already stops at exactly that segment
// when the next one is not a key the document holds. The long form and the short
// form therefore land a reader on the same key, and the only thing that changes
// is that the string stops asserting a path the file does not contain.
//
// It goes through [yamlpos.FieldNode] — the walk every surface turning a finding
// into a source position resolves through — rather than through a walk written
// here. A local walk would be a second copy of that fallback rule, and the two
// agreeing today is exactly what would hide a later divergence.
func TestTruncatingAnUnaddressableKeyMovesNoPosition(t *testing.T) {
	t.Parallel()

	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(locatorReadingRecipe), &doc); err != nil {
		t.Fatalf("the authored Recipe does not parse: %v", err)
	}

	// The form the dialect would have produced by joining the filename in, and the
	// form it produces instead.
	joined := ".spec.artifacts.policy.yaml"
	emitted := authoredLocator(documentSpecSegments(authoredKeyArtifacts, "policy.yaml")...)

	fromJoined := yamlpos.FieldNode(&doc, joined)
	fromEmitted := yamlpos.FieldNode(&doc, emitted)
	if fromJoined == nil || fromEmitted == nil {
		t.Fatalf("one of the two forms resolved to nothing (%q -> %v, %q -> %v), so the "+
			"comparison below has no operands", joined, fromJoined, emitted, fromEmitted)
	}
	if fromJoined.Line != fromEmitted.Line || fromJoined.Column != fromEmitted.Column {
		t.Errorf("the joined form positioned at %d:%d and the emitted form at %d:%d; the two are "+
			"expected to coincide, and a change separating them changes what the shorter form costs "+
			"a reader", fromJoined.Line, fromJoined.Column, fromEmitted.Line, fromEmitted.Column)
	}

	// The floor the comparison is measured against. Without it a reader could take
	// the agreement for a deliberate coarsening rather than for the walk having
	// stopped there all along.
	if fromEmitted.Value != authoredKeyArtifacts {
		t.Errorf("the emitted form reached key %q; want %q — the container that holds the entry",
			fromEmitted.Value, authoredKeyArtifacts)
	}
}

// TestALeafPathKeepsItsDepth pins that the two values reaching locator assembly
// are told apart.
//
// A configuration leaf names a PATH into its pillar and an artifact name is ONE
// key, and both arrive as a single string. Telling them apart is what lets the
// first keep its depth while the second stops at its container — so this is the
// assertion that the fix scoped itself to what it measured instead of coarsening
// every axis to reach one.
func TestALeafPathKeepsItsDepth(t *testing.T) {
	t.Parallel()

	segments := authoredSiteSegments(
		subsystemNesting,
		AuthoredSite{Component: "web"},
		append(
			[]string{authoredKeyFeatures, "audit", authoredKeyValues},
			pillarLeafSegments("retention.days")...,
		)...,
	)
	if want := ".spec.components.web.features.audit.values.retention.days"; authoredLocator(segments...) != want {
		t.Errorf("a leaf path produced %q; want %q — its levels are keys the document spells, "+
			"one under the next", authoredLocator(segments...), want)
	}

	// A raise positioned inside a subject takes its leaf in the same dialect, so
	// the same distinction has to hold there.
	p := newProvenance("aggregate.yaml", authoredKeySpec, authoredKeyComponents, "web")
	at := p.RaiseAt(errRuleBroken, "values.replicas", "minimum")
	if want := ".spec.components.web.values.replicas"; at.Locator() != want {
		t.Errorf("a raise at a leaf path produced %q; want %q", at.Locator(), want)
	}
	if got := at.Detail(); len(got) == 0 || got[0] != "values.replicas" {
		t.Errorf("the detail's leading value = %v; want the leaf whole — the detail names the "+
			"value the rule reported, and only the locator is a position", got)
	}
}

// TestADroppedKeyStillReachesTheReaderInTheDetail is the other half of the
// truncation: what a locator gives up, a rule's detail has to supply.
//
// The delivery-key axis is where this is reachable in an ordinary document rather
// than a contrived one. What a delivery key spells is the mechanism's own
// business, and a mechanism delivering into a file names a mount path — so the key
// carries the separator as ONE authored key, the locator stops at the mechanism's
// block, and the block alone cannot say which of the keys under it is at fault.
//
// It drives the real traversal rather than assembling segments, because the claim
// is about what the traversal hands a rule: the key is the last segment the
// traversal contributes, so a traversal that stopped contributing it would leave
// this passing against an assembly nothing performs.
//
// INVARIANT: a coordinate the locator cannot address reaches the author as a value
// the rule raises.
// CONSEQUENCE: without it the author is given a block and left to search it, which
// is the position they were already at when they read the file.
func TestADroppedKeyStillReachesTheReaderInTheDetail(t *testing.T) {
	t.Parallel()

	const mountPath = "/etc/app/config.yaml"

	fixture := traversalFixture{
		nodes: []NodeInstance{webNode},
		mechanisms: map[string][]string{
			fixtureKey("web"): {"file"},
		},
		deliveryKeys: map[string][]string{
			fixtureKey("web", "file"): {mountPath},
		},
	}

	// The rule shape the two axes share: it names the coordinate the locator could
	// not carry, captioned, alongside a value of its own.
	naming := Check[traversalView, NodeMechanismDeliveryKeySubject]{
		Sentinel: errRuleBroken,
		Severity: SeverityFatal,
		Run: func(_ traversalView, s NodeMechanismDeliveryKeySubject) []Objection {
			return []Objection{s.RaiseWith(
				errRuleBroken,
				Uncaptioned(s.DeliveryKey()),
				Captioned("socket", "db"),
			)}
		},
	}

	raised, err := RunOnNodeMechanismDeliveryKeys(
		nil, fixture, fixture, fixture, traversalView{}, naming,
	)
	if err != nil {
		t.Fatalf("the traversal returned %v with no cancellation signal supplied; want no error", err)
	}
	if len(raised) != 1 {
		t.Fatalf("the traversal raised %d objection(s) over one key; want 1 — with none there is "+
			"nothing below to read", len(raised))
	}
	o := raised[0]

	// The locator stops at the block, which is the truncation working. Stated here
	// as well as in the assertion above it, because the compensation below is only
	// needed while this holds: a locator that came to carry the key would make the
	// detail's copy of it redundant rather than load-bearing.
	if want := ".spec.components.web.inject.file"; o.Locator() != want {
		t.Fatalf("the locator reads %q; want %q — a mount path is one authored key carrying the "+
			"separator, so the dialect cannot address it and names the block instead",
			o.Locator(), want)
	}
	if strings.Contains(o.Locator(), mountPath) {
		t.Errorf("the locator %q spells the mount path, so it reads equally as a path through "+
			"keys the document does not contain", o.Locator())
	}

	// The compensation, in both the shapes a consumer reads: the values a
	// structured reader takes, and the wording every boundary shares.
	if got := o.Detail(); len(got) == 0 || got[0] != mountPath {
		t.Errorf("the detail reads %v; want the key %q first — the locator gave it up, so the "+
			"detail is the only thing that can say which key under the block is at fault",
			got, mountPath)
	}
	message := ObjectionMessage(o)
	if !strings.Contains(message, `"`+mountPath+`"`) {
		t.Errorf("the message reads %q and does not quote the key; a reader is given the block "+
			"and left to search it", message)
	}
	if want := `rule broken ("` + mountPath + `", socket "db")`; message != want {
		t.Errorf("the message reads\n  %s\nwant\n  %s\n"+
			"the second value is captioned because a socket name and a delivery key are both bare "+
			"words: rendered as two quoted strings side by side, a reader cannot tell which is "+
			"which and has been told nothing they can act on", message, want)
	}
}

// attachmentLeafFixture is one attachment leaf, enumerated on one node, with the
// site the entry that supplied it was written at.
//
// The site is the whole variable. Three authoring shapes reach one leaf and they
// differ only in it, so a fixture parameterised on anything else would be
// measuring something other than what this axis turns on.
func attachmentLeafFixture(node NodeInstance, pillar, leaf, file string, site AuthoredSite) traversalFixture {
	return traversalFixture{
		nodes:       []NodeInstance{node},
		attachments: map[string][]string{fixtureKey(node.ID): {"audit"}},
		attachPillars: map[string][]AuthoredCoordinate{
			fixtureKey(node.ID, "audit"): {{Value: pillar, SourceFile: file}},
		},
		attachLeaves: map[string][]AttachmentLeaf{
			fixtureKey(node.ID, "audit", pillar): {{Value: leaf, Site: site}},
		},
	}
}

// oneAttachmentLeaf drives the real traversal over the fixture and returns the
// single subject it enumerated.
//
// It goes through the traversal rather than the constructor because the claim is
// about what a rule is handed: the coordinate and the locator are assembled
// there, so a subject built directly would be this test's account of that
// assembly rather than the assembly itself.
func oneAttachmentLeaf(t *testing.T, f traversalFixture) NodeAttachmentPillarLeafSubject {
	t.Helper()

	var seen []NodeAttachmentPillarLeafSubject
	_, err := RunOnNodeAttachmentPillarLeaves(nil, f, f, f, f, traversalView{},
		Check[traversalView, NodeAttachmentPillarLeafSubject]{
			Sentinel: errRuleBroken,
			Severity: SeverityWarning,
			Run: func(_ traversalView, s NodeAttachmentPillarLeafSubject) []Objection {
				seen = append(seen, s)
				return nil
			},
		})
	if err != nil {
		t.Fatalf("the traversal returned %v with no cancellation signal supplied; want no error", err)
	}
	if len(seen) != 1 {
		t.Fatalf("the traversal was invoked %d time(s) over one leaf; want 1 — with none or several "+
			"there is nothing single to read below", len(seen))
	}
	return seen[0]
}

// The two nodes the three shapes below are reached through. The composed one
// carries the qualified identifier a composition gives a component it
// contributes, and the short name that component's own document wrote.
var (
	composedNode = NodeInstance{
		ID:                   "billing.api",
		SourceFile:           "billing/aggregate.yaml",
		AuthoredName:         "api",
		SubsystemPath:        "billing",
		SynthesizedSubsystem: false,
	}
	plainNode = NodeInstance{
		ID:                   "web",
		SourceFile:           "aggregate.yaml",
		AuthoredName:         "web",
		SubsystemPath:        "default",
		SynthesizedSubsystem: true,
	}
)

// TestAComposedComponentKeyLeavesTheLocatorAtTheBlock is the third axis, and the
// one an ordinary Release reaches rather than a contrived one.
//
// A Release addresses a component a composition contributed by the qualified
// identifier the composition gave it, and the Release's own component key
// admits that spelling. So the key carries the dialect's separator as ONE
// authored key, which the dialect cannot address: the locator stops at the
// components block and gives up the component AND everything written under it —
// the attachment, the pillar and the leaf — however ordinary each of those is on
// its own.
//
// The two other shapes are here because they are what makes the first one
// legible. A composed component's own block, in its own document, is keyed by the
// short name that document wrote, so it does not truncate at all; and an entry
// written scope-wide names no component, so it does not either. One axis, three
// shapes, and only one of them loses anything — which is why a rule over this kind
// names what it lost whether or not this particular invocation lost it.
//
// INVARIANT: a locator names a path the authored document spells, and stops rather
// than spelling one it does not.
// CONSEQUENCE: without the stop the position reads as a walk through keys the file
// does not contain; with it, three coordinates go unnamed unless the rule names
// them, which is what the message assertion below is for.
func TestAComposedComponentKeyLeavesTheLocatorAtTheBlock(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name        string
		fixture     traversalFixture
		wantLocator string
		wantLost    []string
	}{
		{
			// The shape that loses coordinates. The Release's key is the
			// qualified identifier, so the locator stops four segments in.
			name: "a Release keying a composed component under a boundary",
			fixture: attachmentLeafFixture(composedNode, authoredKeyInfra, "region",
				"releases/prod.yaml", AuthoredSite{ScopePath: "prod", Component: "billing.api"}),
			wantLocator: ".spec.boundaries.prod.components",
			wantLost:    []string{"billing.api", authoredKeyFeatures, "audit", authoredKeyInfra, "region"},
		},
		{
			// The same component, in the document that declares it, keyed by the
			// name that document wrote. Nothing is lost here, which is why a rule
			// cannot decide what to name by looking at its own locator.
			name: "the same component's own block in its own document",
			fixture: attachmentLeafFixture(composedNode, authoredKeyValues, "region",
				"billing/aggregate.yaml", ComponentSite("api", "billing", false)),
			wantLocator: ".spec.subsystems.billing.components.api.features.audit.values.region",
		},
		{
			// The entry a component-shaped path cannot express. It reaches every
			// component the scope holds and names none, so there is no component
			// segment to lose and the locator is whole.
			name: "an entry written scope-wide, naming no component",
			fixture: attachmentLeafFixture(plainNode, authoredKeyInfra, "region",
				"releases/prod.yaml", AuthoredSite{ScopePath: "prod"}),
			wantLocator: ".spec.boundaries.prod.features.audit.infra.region",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			s := oneAttachmentLeaf(t, tc.fixture)

			if s.Locator() != tc.wantLocator {
				t.Errorf("the locator reads %q; want %q", s.Locator(), tc.wantLocator)
			}
			// What the truncation cost, named rather than counted: a count moves
			// with the leaf's own depth, since a dotted leaf path contributes one
			// segment per level, so it says nothing stable about the axis.
			for _, lost := range tc.wantLost {
				if strings.Contains(s.Locator(), lost) {
					t.Errorf("the locator %q still spells %q; the truncation is supposed to have given "+
						"it up, and a locator carrying it reads as a path through keys the document "+
						"does not contain", s.Locator(), lost)
				}
			}
		})
	}
}

// TestTheDroppedCoordinatesReachTheReaderCaptioned is the compensation, read as a
// message rather than as values.
//
// It is the whole rendered string and not the values in it because what an author
// needs is to tell them apart. An annotation key, an attachment name and a
// configuration key are all bare words or all dotted words, so raised bare they
// arrive as a row of quoted strings that says nothing — which is a message every
// value-by-value assertion passes.
//
// Both shapes are driven with one rule, so the assertion is that the rule names
// the same coordinates either way. It cannot see which shape it is in, and a rule
// that named them only when they were lost would be deciding from its own locator
// what the framework already decided.
func TestTheDroppedCoordinatesReachTheReaderCaptioned(t *testing.T) {
	t.Parallel()

	// The site of the annotation that lost. It is a whole position of its own, and
	// the longest value the finding carries, which is why it is raised last.
	const declaredAt = "releases/prod.yaml:boundaries.prod.components.billing.api.annotations.aggrega.ai/audit"

	// The rule shape, written once and invoked over both fixtures: the suppressed
	// annotation bare, then every coordinate under the component key, then the
	// annotation's own site.
	raise := func(s NodeAttachmentPillarLeafSubject) Objection {
		return s.RaiseWith(errRuleBroken,
			Uncaptioned("aggrega.ai/audit"),
			Captioned("attachment", s.AttachmentName()),
			Captioned("pillar", s.PillarName()),
			Captioned("key", s.LeafName()),
			Captioned("annotation declared at", declaredAt),
		)
	}

	want := `rule broken ("aggrega.ai/audit", attachment "audit", pillar "infra", ` +
		`key "region", annotation declared at "` + declaredAt + `")`

	for _, tc := range []struct {
		name    string
		fixture traversalFixture
	}{
		{
			name: "a composed component key, whose coordinates the locator gave up",
			fixture: attachmentLeafFixture(composedNode, authoredKeyInfra, "region",
				"releases/prod.yaml", AuthoredSite{ScopePath: "prod", Component: "billing.api"}),
		},
		{
			name: "an entry naming no component, whose locator kept them",
			fixture: attachmentLeafFixture(plainNode, authoredKeyInfra, "region",
				"releases/prod.yaml", AuthoredSite{ScopePath: "prod"}),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			o := raise(oneAttachmentLeaf(t, tc.fixture))
			if got := ObjectionMessage(o); got != want {
				t.Errorf("the message reads\n  %s\nwant\n  %s", got, want)
			}
			// The coordinates reach a structured reader as well as a prose one, in
			// the order the rule supplied them. Asserted separately because the two
			// consumers are separate: one boundary composes the wording and another
			// takes the values, and a change dropping either leaves the other green.
			wantValues := []string{"aggrega.ai/audit", "audit", "infra", "region", declaredAt}
			got := o.Detail()
			if len(got) != len(wantValues) {
				t.Fatalf("the detail carries %d value(s) %q; want %d %q", len(got), got, len(wantValues), wantValues)
			}
			for i := range wantValues {
				if got[i] != wantValues[i] {
					t.Errorf("detail value %d reads %q; want %q", i, got[i], wantValues[i])
				}
			}
		})
	}
}
