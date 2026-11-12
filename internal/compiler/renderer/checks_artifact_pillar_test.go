// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package renderer

import (
	"errors"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
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

	// objections is every record this node alone must raise, as artifact, source
	// file, locator and detail.
	//
	// The artifact comes first and comes from the subject the framework invoked
	// the rule with, because the locator cannot supply it. A locator is a
	// position: it names the deepest site the author actually wrote, and an entry
	// whose output name is a filename is not separately addressable — so every
	// entry in this block positions at the block itself. Carrying the name is what
	// keeps the record per entry; without it, two entries objecting about one
	// pillar produce byte-identical records, and a rule that raised one of them
	// twice and the other never would satisfy both comparisons below.
	//
	// It is per node and not pooled across nodes on purpose: a record carries no
	// node coordinate either, so two nodes raising the same row produce identical
	// records — and the comparison below tests presence rather than multiplicity,
	// so a pooled expectation could not see a node that objected when it should
	// have been silent. Per node, silence is checkable.
	//
	// The detail's second value is the same artifact, and the repetition is the
	// assertion rather than an accident of the format: the record's leading field
	// is the framework's attribution, taken from the subject the rule was invoked
	// with, while the detail is what the RULE said. A rule naming some other
	// artifact — or naming none — produces a record whose two halves disagree,
	// which no comparison over the rule's detail alone could see.
	objections []string
}{
	{
		id: "bare", left: "{{", right: "}}",
		// Every usual-pair pillar read objects, and reads-both.yaml objects TWICE
		// — which is what states that the value reports every offending reference
		// rather than stopping at the first.
		objections: []string{
			"reads-workload.yaml|recipes/app.yaml|.spec.artifacts|.workload,reads-workload.yaml",
			"reads-workload-leaf.yaml|recipes/app.yaml|.spec.artifacts|.workload.containers,reads-workload-leaf.yaml",
			"reads-source.yaml|recipes/app.yaml|.spec.artifacts|.source,reads-source.yaml",
			"reads-source-leaf.yaml|recipes/app.yaml|.spec.artifacts|.source.version,reads-source-leaf.yaml",
			"reads-both.yaml|recipes/app.yaml|.spec.artifacts|.source,reads-both.yaml",
			"reads-both.yaml|recipes/app.yaml|.spec.artifacts|.workload,reads-both.yaml",
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
			"alternate-pair-read.yaml|recipes/app.yaml|.spec.artifacts|.workload,alternate-pair-read.yaml",
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
		var raised []artifactObjection
		single := artifactFixtureFor(n.id)
		got, err := constraint.RunOnNodeArtifacts(nil, single, single, facts,
			countingArtifactCheck(
				attributedArtifactCheck(ArtifactPillarDeclared, &raised), &invoked))
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

		// The records are built from what the wrapper saw at invocation rather than
		// from the traversal's return, so the two are held level here: a traversal
		// that dropped or added an objection on the way back would otherwise be
		// invisible to every comparison below.
		if len(raised) != len(got) {
			t.Errorf("node %q: the rule raised %d objection(s) at invocation while the traversal "+
				"returned %d.\nCONSEQUENCE: the attributed set compared below is then not the set "+
				"a consumer receives, so the comparison speaks for neither.",
				n.id, len(raised), len(got))
		}

		records := artifactObjectionRecords(raised)
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

// The cross-check that ran both forms of this rule over one input set and compared
// which entries each refused is REMOVED, and the reason is that its premise is
// discharged rather than that it stopped being useful.
//
// It existed because the two forms coexisted with no structure holding them in
// step, so their agreement had to be asserted rather than assumed. The
// hand-written form is now gone: the pre-check beside the walk keeps only its parse
// condition, so there is no second implementation of the pillar verdict left to
// compare against, and a comparison against the production path would compare the
// framework with itself.
//
// What it proved is not lost. It asserted the declared side's verdict per entry
// over the same grid TestTheArtifactPillarRuleObjectsInBothDirections drives —
// each pillar referenced against each pillar declared, both arms, the near-miss
// control, the delimiter row and the non-parsing body — and that test matches the
// objection set as a whole rather than by count, so the per-entry verdict stays
// asserted. The only thing it additionally held was the equality with a form that
// no longer exists.

// artifactObjection is one raised objection together with the artifact the
// framework invoked the rule over.
//
// The pairing is captured at invocation rather than recovered from the objection
// afterwards because nothing an objection carries identifies the entry: its
// locator is a position, and an artifact entry whose output name is a filename is
// not separately addressable, so every entry in the block positions at the block.
// Recovering the entry from the locator would be reading an identity out of a
// coordinate that does not carry one — the confusion this pairing exists to end.
type artifactObjection struct {
	artifact  string
	objection constraint.Objection
}

// attributedArtifactCheck wraps a declared value so every objection it raises is
// recorded against the artifact the traversal invoked it over.
//
// It copies the sentinel and the severity from the declaration rather than
// restating them, so a value whose sentinel changed is still the value under test.
func attributedArtifactCheck(
	declared constraint.Check[constraint.HydratedView, constraint.NodeArtifactSubject],
	raised *[]artifactObjection,
) constraint.Check[constraint.HydratedView, constraint.NodeArtifactSubject] {
	return constraint.Check[constraint.HydratedView, constraint.NodeArtifactSubject]{
		Sentinel: declared.Sentinel,
		Severity: declared.Severity,
		Run: func(v constraint.HydratedView, s constraint.NodeArtifactSubject) []constraint.Objection {
			out := declared.Run(v, s)
			for _, o := range out {
				*raised = append(*raised, artifactObjection{artifact: s.ArtifactName(), objection: o})
			}
			return out
		},
	}
}

// artifactObjectionRecords flattens attributed objections in the order they were
// raised, each as the artifact the rule was invoked over followed by what the
// framework and the rule said.
//
// It delegates the second half to the shared flattener rather than restating the
// format, so a record here and a record in a sibling suite cannot drift apart in
// everything but the identity this one prepends.
func artifactObjectionRecords(raised []artifactObjection) []string {
	out := make([]string, 0, len(raised))
	for _, r := range raised {
		out = append(out, r.artifact+"|"+objectionRecord(r.objection))
	}
	return out
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

// TestTheArtifactPillarFindingNamesWhichArtifactItIsAbout reads the whole message
// an author is handed, rather than the values inside it.
//
// The rule names two values and they are the same shape: an output name is a
// filename and a pillar reference is a dotted word, so rendered as two bare quoted
// strings side by side there is nothing in the message that says which is which.
// Reading each value out separately passes on exactly that message, which is why
// this asserts the caption and the punctuation around it.
//
// It is also what the position cannot supply. An output name carries the separator
// the locator dialect divides its segments with, and it carries it as one authored
// key — so the locator names the artifacts block and stops, and in a Recipe
// declaring twenty entries the block is where the author already was.
func TestTheArtifactPillarFindingNamesWhichArtifactItIsAbout(t *testing.T) {
	t.Parallel()

	const entry = "policy.yaml"

	fixture := artifactSiteFixture{
		nodes: []constraint.NodeInstance{{
			ID: "web", SourceFile: "aggregate.yaml", AuthoredName: "web",
			SubsystemPath: "default", SynthesizedSubsystem: true,
		}},
		artifacts: map[string][]constraint.AuthoredCoordinate{
			factKey("web"): {{Value: entry, SourceFile: "recipes/app.yaml"}},
		},
	}
	stub := artifactPillarFactStub{
		text:            map[string]string{factKey("web", entry): `image: {{ .workload }}`},
		delimiters:      map[string]constraint.ArtifactTemplateDelimiters{factKey("web"): {Left: "{{", Right: "}}"}},
		workloadPresent: map[string]bool{factKey("web"): false},
		sourceDeclared:  map[string]bool{factKey("web"): false},
	}

	raised, err := constraint.RunOnNodeArtifacts(
		nil, fixture, fixture, constraint.HydratedView(stub), ArtifactPillarDeclared,
	)
	if err != nil {
		t.Fatalf("the traversal returned %v with no cancellation signal supplied; want no error", err)
	}
	if len(raised) != 1 {
		t.Fatalf("the rule raised %d objection(s) over one offending entry; want 1 — with none "+
			"there is nothing below to read", len(raised))
	}
	o := raised[0]

	if want := ".spec.artifacts"; o.Locator() != want {
		t.Fatalf("the finding is positioned at %q; want %q — an output name is one authored key "+
			"carrying the separator, so the dialect names the block instead. The assertion below "+
			"is only load-bearing while this holds.", o.Locator(), want)
	}
	if got, want := o.SourceFile(), "recipes/app.yaml"; got != want {
		t.Errorf("the finding names document %q; want %q — the block is in the Recipe's own file, "+
			"not in the Aggregate the node was authored in", got, want)
	}

	message := constraint.ObjectionMessage(o)
	want := ErrArtifactPillarNotDeclared().Error() + ` (".workload", artifact "` + entry + `")`
	if message != want {
		t.Errorf("the finding reads\n  %s\nwant\n  %s\n"+
			"INVARIANT: the registered wording names the pillar reference, so that value stands "+
			"bare and the artifact says what it is.\nCONSEQUENCE: two bare quoted values leave an "+
			"author unable to tell the reference from the filename.", message, want)
	}
	t.Logf("the finding an author reads: %s at %s: %s", o.SourceFile(), o.Locator(), message)
}
