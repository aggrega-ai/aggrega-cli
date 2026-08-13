// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"errors"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// The artifact-pillar rule reads four declared facts, and this file runs the
// declared value through the framework's own traversal over a fixture.
//
// # Why the facts are exercised rather than only declared
//
// A fact declaration is checkable against its own shape and nothing else: the
// generator confirms a stage exists, a subject kind is keyed and a result is an
// admitted shape, none of which says the accessor was read for the right
// instance or that the predicate branches the right way on it. These four facts
// are exactly where that gap bites, because three are keyed per node and the
// fourth per artifact — so a predicate reading the artifact text for one artifact
// and the pillar answers for a different node would satisfy every declaration
// assertion and still be wrong. The fixture carries three nodes sharing one
// artifact set and disagreeing on the facts, which is what makes a cross-node
// read produce a different verdict rather than the same one.
//
// # The stub embeds the view interface, and the nil embedding is the point
//
// artifactPillarFactStub embeds [constraint.HydratedView] and overrides only the
// four accessors this rule reads. The embedded interface is nil, so any other
// accessor panics rather than returning a zero value — which turns "the
// predicate reads declared facts and nothing else" from a claim about the source
// into a property this file enforces. It is also what keeps the stub small: the
// stage's view transitively embeds every earlier stage, so an implementation
// supplying that whole set by hand would be most of the registry.

// artifactPillarFactStub answers the four facts the artifact-pillar rule reads
// and nothing else.
type artifactPillarFactStub struct {
	constraint.HydratedView

	// text is keyed by node and artifact together; the three pillar-side answers
	// by node alone, which is the keying the declarations state.
	text            map[string]string
	delimiters      map[string]constraint.ArtifactTemplateDelimiters
	workloadPresent map[string]bool
	sourceDeclared  map[string]bool
}

func (s artifactPillarFactStub) ArtifactTemplateText(node, artifact string) string {
	return s.text[factKey(node, artifact)]
}

func (s artifactPillarFactStub) ProviderArtifactTemplateDelimiters(node string) constraint.ArtifactTemplateDelimiters {
	return s.delimiters[factKey(node)]
}

func (s artifactPillarFactStub) ParsedNodeWorkloadPresent(node string) bool {
	return s.workloadPresent[factKey(node)]
}

func (s artifactPillarFactStub) ResolvedNodeSourceDeclared(node string) bool {
	return s.sourceDeclared[factKey(node)]
}

// artifactSiteFixture implements the two enumerators the artifact traversal needs.
//
// Its slices are indexed rather than ranged, so the order a traversal observes
// comes from the slices and never from a map walk.
type artifactSiteFixture struct {
	nodes     []constraint.NodeInstance
	artifacts map[string][]constraint.AuthoredCoordinate
}

func (f artifactSiteFixture) Nodes() []constraint.NodeInstance { return f.nodes }

func (f artifactSiteFixture) Artifacts(node string) []constraint.AuthoredCoordinate {
	return f.artifacts[factKey(node)]
}

// The artifact bodies, one set shared by every node, so the only thing that
// differs across nodes is the facts.
//
// nearMiss is the control: `.workloads` and `.sourcemap` are roots whose names
// START with a pillar's, so a predicate testing a bare prefix rather than the
// exact-or-descendant shape objects about them. They must stay silent even at a
// node declaring neither pillar.
//
// alternatePairRead and every usualPair body together are what make the
// delimiter fact load-bearing rather than incidental: the two are read as
// references at opposite nodes, so a predicate ignoring the fact and assuming
// one pair flips BOTH sets of rows rather than neither.
//
// unparseable is unterminated under the usual pair and is inert literal text
// under the alternate one, which is why it is silent everywhere for two
// different reasons.
var artifactPillarBodies = map[string]string{
	"reads-workload.yaml":      `image: {{ .workload }}`,
	"reads-workload-leaf.yaml": `image: {{ .workload.containers }}`,
	"reads-source.yaml":        `chart: {{ .source }}`,
	"reads-source-leaf.yaml":   `chart: {{ .source.version }}`,
	"reads-both.yaml":          `a: {{ .workload }}{{ .source }}`,
	"near-miss.yaml":           `a: {{ .workloads }}{{ .sourcemap }}`,
	"alternate-pair-read.yaml": `a: [[ .workload ]]`,
	"unparseable.yaml":         `a: {{ .workload `,
}

// The node set, chosen so every branch the rule carries is met and so each fact
// is what decides at least one row.
//
//   - bare  — the usual delimiter pair, neither pillar. Every usual-pair pillar
//     read objects here.
//   - full  — the usual delimiter pair, both pillars. Every row is silent, which
//     is the other direction of the proof.
//   - alt   — an alternate delimiter pair, neither pillar. The usual-pair reads
//     go silent because they are opaque text, and the alternate-pair read is the
//     only row that objects.
var artifactPillarNodes = []struct {
	id              string
	left, right     string
	workloadPresent bool
	sourceDeclared  bool

	// objections is every record this node alone must raise, as source file,
	// locator and detail. It is per node and not pooled across nodes on purpose:
	// a locator carries no node coordinate, so two nodes raising the same row
	// produce identical records — and the comparison below tests presence rather
	// than multiplicity, so a pooled expectation could not see a node that
	// objected when it should have been silent. Per node, silence is checkable.
	objections []string
}{
	{
		id: "bare", left: "{{", right: "}}",
		// Every usual-pair pillar read objects, and reads-both.yaml objects TWICE
		// — which is what states that the value reports every offending reference
		// rather than stopping at the first.
		objections: []string{
			"recipes/app.yaml|.spec.artifacts.reads-workload.yaml|.workload",
			"recipes/app.yaml|.spec.artifacts.reads-workload-leaf.yaml|.workload.containers",
			"recipes/app.yaml|.spec.artifacts.reads-source.yaml|.source",
			"recipes/app.yaml|.spec.artifacts.reads-source-leaf.yaml|.source.version",
			"recipes/app.yaml|.spec.artifacts.reads-both.yaml|.workload",
			"recipes/app.yaml|.spec.artifacts.reads-both.yaml|.source",
		},
	},
	{
		id: "full", left: "{{", right: "}}", workloadPresent: true, sourceDeclared: true,
		// Both pillars declared, so every row is silent. The empty expectation is
		// the other direction of the proof and the reason the rows are enumerated
		// per node.
		objections: nil,
	},
	{
		id: "alt", left: "[[", right: "]]",
		// Neither pillar, but the provider declares the alternate pair — so the
		// usual-pair bodies are opaque text and only the body written in the
		// alternate pair is read at all.
		objections: []string{
			"recipes/app.yaml|.spec.artifacts.alternate-pair-read.yaml|.workload",
		},
	},
}

// artifactEntryNames is the authored order the traversal observes, held as a
// slice because the body set is a map and a map has no order to observe.
var artifactEntryNames = []string{
	"reads-workload.yaml",
	"reads-workload-leaf.yaml",
	"reads-source.yaml",
	"reads-source-leaf.yaml",
	"reads-both.yaml",
	"near-miss.yaml",
	"alternate-pair-read.yaml",
	"unparseable.yaml",
}

// artifactFixtureFor builds a fixture over the named nodes, each projecting the
// whole artifact set in authored order.
//
// It takes the node list rather than always returning every node because an
// objection's locator carries no node coordinate: attributing objections back to
// a node means walking one node at a time, and a fixture that could only hold all
// three would make that impossible.
func artifactFixtureFor(nodeIDs ...string) artifactSiteFixture {
	entries := make([]constraint.AuthoredCoordinate, 0, len(artifactEntryNames))
	for _, name := range artifactEntryNames {
		entries = append(entries, constraint.AuthoredCoordinate{Value: name, SourceFile: "recipes/app.yaml"})
	}
	out := artifactSiteFixture{artifacts: map[string][]constraint.AuthoredCoordinate{}}
	for _, id := range nodeIDs {
		out.nodes = append(out.nodes, constraint.NodeInstance{
			ID: id, SourceFile: "aggregate.yaml", AuthoredName: id,
			SubsystemPath: "default", SynthesizedSubsystem: true,
		})
		out.artifacts[factKey(id)] = entries
	}
	return out
}

// newArtifactSiteFixture is the whole node set, for the tests that do not need
// per-node attribution.
func newArtifactSiteFixture() artifactSiteFixture {
	ids := make([]string, 0, len(artifactPillarNodes))
	for _, n := range artifactPillarNodes {
		ids = append(ids, n.id)
	}
	return artifactFixtureFor(ids...)
}

func newArtifactPillarFactStub() artifactPillarFactStub {
	stub := artifactPillarFactStub{
		text:            map[string]string{},
		delimiters:      map[string]constraint.ArtifactTemplateDelimiters{},
		workloadPresent: map[string]bool{},
		sourceDeclared:  map[string]bool{},
	}
	for _, n := range artifactPillarNodes {
		for name, body := range artifactPillarBodies {
			stub.text[factKey(n.id, name)] = body
		}
		stub.delimiters[factKey(n.id)] = constraint.ArtifactTemplateDelimiters{Left: n.left, Right: n.right}
		stub.workloadPresent[factKey(n.id)] = n.workloadPresent
		stub.sourceDeclared[factKey(n.id)] = n.sourceDeclared
	}
	return stub
}

// TestTheArtifactPillarRuleObjectsInBothDirections runs the declared value over
// the rule's real grid: each pillar referenced against each pillar declared,
// both arms, a near-miss control, a row that only the provider's own delimiter
// pair makes readable, and a body that does not parse.
//
// Both directions are driven rather than only the objecting one. A rule that
// objected about everything would satisfy a test listing only violations, and a
// rule that objected about nothing would satisfy one listing only the silent
// rows — so every combination is enumerated and the objection set is matched as
// a whole rather than by count.
func TestTheArtifactPillarRuleObjectsInBothDirections(t *testing.T) {
	t.Parallel()

	facts := constraint.HydratedView(newArtifactPillarFactStub())

	totalInvoked := 0
	for _, n := range artifactPillarNodes {
		invoked := 0
		single := artifactFixtureFor(n.id)
		got, err := constraint.RunOnNodeArtifacts(nil, single, single, facts,
			countingArtifactCheck(ArtifactPillarDeclared, &invoked))
		if err != nil {
			t.Errorf("the traversal over node %q's artifacts returned %v with no cancellation signal "+
				"supplied; want no error — a stopped walk hands back a short objection set, which is "+
				"indistinguishable from a rule that ran and approved", n.id, err)
			continue
		}
		totalInvoked += invoked

		if want := len(artifactEntryNames); invoked != want {
			t.Errorf("the declared rule was invoked %d time(s) over node %q; want %d.\n"+
				"INVARIANT: the rule is invoked once per artifact each node projects.\n"+
				"CONSEQUENCE: a rule invoked fewer times raises less, which is indistinguishable "+
				"from a rule that ran and approved.", invoked, n.id, want)
		}

		records := objectionRecords(got)
		if missing, unexpected := setDifference(n.objections, records); len(missing) > 0 || len(unexpected) > 0 {
			t.Errorf("the declared rule raised the wrong objections over node %q.\n"+
				"not raised: %v\nraised and not expected: %v\n"+
				"A near-miss root, a body opaque under this node's provider delimiter pair, and a "+
				"body that does not parse must all stay silent; a node declaring both pillars must "+
				"raise nothing at all.", n.id, missing, unexpected)
		}
		// Presence comparison cannot see multiplicity, so the count is stated
		// separately: it is what proves the value reports every offending
		// reference in a body rather than the first.
		if len(records) != len(n.objections) {
			t.Errorf("node %q raised %d objection(s); want %d.\n"+
				"The rule reports every offending reference in a body, so a body naming two absent "+
				"pillars owes two objections — a count short by one is invisible to the set "+
				"comparison above.", n.id, len(records), len(n.objections))
		}

		// The sentinel is asserted by identity rather than through the message, so
		// a rule wrapping a neighbouring halt is caught here.
		for _, o := range got {
			if !errors.Is(o.Sentinel(), ErrArtifactPillarNotDeclared()) {
				t.Errorf("an objection at %s on node %q carries sentinel %v; want the "+
					"artifact-pillar sentinel.\nINVARIANT: a consumer matches this halt by identity, "+
					"so a rule raising a neighbouring sentinel is indistinguishable from the right "+
					"one in the message.", o.Locator(), n.id, o.Sentinel())
			}
		}
	}

	if want := len(artifactPillarNodes) * len(artifactEntryNames); totalInvoked != want {
		t.Errorf("the declared rule was invoked %d time(s) across every node; want %d",
			totalInvoked, want)
	}
	if totalInvoked == 0 {
		t.Fatal("the declared rule was invoked zero times, so no row above proves anything")
	}
}

// TestTheArtifactPillarSentinelAssertionIsNeedleCalibrated proves the identity
// assertion above can fail.
//
// An assertion comparing two sentinels passes both when it is right and when it
// compares something that cannot differ, and the two are indistinguishable from
// a green run. Pointing the same comparison at a sibling sentinel — one raised by
// a different rule over the same authoring block, which is exactly the confusion
// the identity assertion exists to catch — and observing that it matches nothing
// is what separates them.
func TestTheArtifactPillarSentinelAssertionIsNeedleCalibrated(t *testing.T) {
	t.Parallel()

	fixture := newArtifactSiteFixture()
	facts := constraint.HydratedView(newArtifactPillarFactStub())

	got, err := constraint.RunOnNodeArtifacts(nil, fixture, fixture, facts, ArtifactPillarDeclared)
	if err != nil {
		t.Fatalf("the traversal over node artifacts returned %v; want no error", err)
	}
	if len(got) == 0 {
		t.Fatal("the rule raised nothing, so the calibration below would pass vacuously")
	}

	needled := 0
	own := 0
	for _, o := range got {
		if errors.Is(o.Sentinel(), ErrArtifactOutputCollision()) {
			needled++
		}
		if errors.Is(o.Sentinel(), ErrArtifactPillarNotDeclared()) {
			own++
		}
	}
	if needled != 0 {
		t.Errorf("%d of %d objection(s) matched the sibling sentinel, so the identity comparison "+
			"does not discriminate between the two and the assertion it calibrates proves nothing",
			needled, len(got))
	}
	if own != len(got) {
		t.Errorf("%d of %d objection(s) matched the rule's own sentinel; the calibration is only "+
			"meaningful when the same comparison succeeds in one direction and fails in the other",
			own, len(got))
	}
}

// TestTheArtifactPillarRuleReadsNoUndeclaredFact proves the predicate reaches
// for nothing the stub does not supply.
//
// The stub's embedded view interface is nil, so an accessor it does not override
// panics rather than returning a zero value. Running the rule against it and
// observing no panic is therefore a statement about what the predicate reads,
// which no source inspection can make: a read added later fails here loudly
// instead of silently deciding on a zero value.
func TestTheArtifactPillarRuleReadsNoUndeclaredFact(t *testing.T) {
	t.Parallel()

	// Calibration: the nil embedding must actually panic, or the absence of a
	// panic below says only that nothing was ever checked. The needle is another
	// fact of the same stage, so it is reachable through the same view and is a
	// read the stub genuinely does not answer.
	func() {
		defer func() {
			if recover() == nil {
				t.Errorf("reading an unsupplied fact from the stub did not panic, so this test cannot " +
					"detect a predicate reaching for an undeclared fact")
			}
		}()
		_ = newArtifactPillarFactStub().DeclaredLocalsKeys("bare")
	}()

	fixture := newArtifactSiteFixture()
	facts := constraint.HydratedView(newArtifactPillarFactStub())

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("the artifact-pillar predicate read a fact the stub does not supply: %v.\n"+
				"INVARIANT: a check reads the facts its view declares and nothing else, so every value "+
				"it reads was produced for it.\n"+
				"CONSEQUENCE: a predicate reaching further makes the computed placement describe "+
				"something other than what the check does.", r)
		}
	}()

	if _, err := constraint.RunOnNodeArtifacts(nil, fixture, fixture, facts,
		ArtifactPillarDeclared); err != nil {
		t.Fatalf("node artifact traversal: %v", err)
	}
}

// TestTheArtifactPillarRuleAndTheHandWrittenPreCheckAgree runs both forms of the
// rule over one input set and compares which entries each refuses.
//
// The two coexist deliberately while nothing invokes a declared value, and no
// structure holds them in step — they share the pillar roots and the
// exact-or-descendant test and nothing else — so the agreement is asserted here
// rather than assumed. It is agreement on WHICH ENTRIES are refused and not on
// how many objections each produces: the declared value reports every offending
// reference in a body while the hand-written form stops at the first, which is a
// stated difference rather than drift.
func TestTheArtifactPillarRuleAndTheHandWrittenPreCheckAgree(t *testing.T) {
	t.Parallel()

	stub := newArtifactPillarFactStub()

	// The declared side is run one node at a time, because an objection's locator
	// carries no node coordinate: the nodes share one artifact set, so a single
	// walk's objections could not be attributed back to a node. Running each
	// node's own walk is what makes the comparison per entry.
	perNodeDeclared := map[string]bool{}
	for _, n := range artifactPillarNodes {
		single := artifactFixtureFor(n.id)
		objections, err := constraint.RunOnNodeArtifacts(nil, single, single,
			constraint.HydratedView(stub), ArtifactPillarDeclared)
		if err != nil {
			t.Fatalf("node %q artifact traversal: %v", n.id, err)
		}
		for _, o := range objections {
			perNodeDeclared[factKey(n.id, o.Locator())] = true
		}
	}

	compared := 0
	for _, n := range artifactPillarNodes {
		for _, name := range artifactEntryNames {
			state := &PhaseBState{Recipe: &types.Recipe{}}
			if n.workloadPresent {
				state.ParsedWorkload = &types.Workload{}
			}
			if n.sourceDeclared {
				state.Recipe.Spec.Source = "oci://example.test/chart"
			}
			handWritten := preCheckArtifactPillars(state, name,
				artifactPillarBodies[name], n.left, n.right)

			// The hand-written form also refuses a body that does not parse, which
			// the declared value leaves to evaluation, so the comparison is on the
			// pillar verdict alone rather than on any error.
			handRefused := handWritten != nil && errors.Is(handWritten, ErrArtifactPillarNotDeclared())
			declRefused := perNodeDeclared[factKey(n.id, ".spec.artifacts."+name)]
			compared++
			if handRefused != declRefused {
				t.Errorf("node %q artifact %q: the hand-written pre-check refused=%v while the "+
					"declared value refused=%v.\n"+
					"INVARIANT: the two forms express one rule while both are present.\n"+
					"CONSEQUENCE: a divergence means the halt a user meets and the placement the "+
					"framework computes describe different rules.",
					n.id, name, handRefused, declRefused)
			}
		}
	}
	if compared == 0 {
		t.Fatal("no entry was compared, so this test proves nothing about the two forms")
	}
	// Coverage only. It deliberately does not claim the two forms agree: a log
	// line runs whether or not an assertion above failed, so one asserting the
	// verdict would print "both forms agree" underneath the errors proving they
	// do not.
	t.Logf("compared %d artifact entries across %d nodes", compared, len(artifactPillarNodes))
}

// countingArtifactCheck wraps a declared value so a traversal's invocation count
// is observable without the value itself carrying test-only state.
//
// It is the hydrated-stage counterpart of the described-stage wrapper beside it,
// and the near-duplicate is forced rather than chosen: the wrapper's view type is
// fixed by the value it wraps, and a check carries its view as a type parameter,
// so one helper cannot serve two stages. It copies the sentinel and the severity
// from the declaration rather than restating them, so a value whose sentinel
// changed is still the value under test.
func countingArtifactCheck[S constraint.Subject](
	declared constraint.Check[constraint.HydratedView, S],
	count *int,
) constraint.Check[constraint.HydratedView, S] {
	return constraint.Check[constraint.HydratedView, S]{
		Sentinel: declared.Sentinel,
		Severity: declared.Severity,
		Run: func(v constraint.HydratedView, s S) []constraint.Objection {
			*count++
			return declared.Run(v, s)
		},
	}
}
