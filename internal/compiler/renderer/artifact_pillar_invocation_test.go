// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// invocationGraphFacts stands in for the presentation the graph finalisation
// produces, which the render embeds beneath the two presentations built over it.
//
// The rule under test reads four facts and none of them is a graph-stage one — the
// delimiter pair is answered from the provider the render bound, and the other
// three from what the artifacts walk retained — so this is never read. It carries
// no value behind the interface deliberately: a read panics here rather than
// answering a zero value, so a rule that later starts reading a graph-stage fact
// fails loudly instead of being quietly handed nothing.
//
// It is supplied because the presentation chain is cumulative and refuses to
// assemble with a hole in it: the render publishes no mid-render presentation
// unless the one it extends exists. Production always supplies this; a fixture
// omitting it would leave the rule invoked zero times for a reason that has
// nothing to do with what is being proved.
type invocationGraphFacts struct{ constraint.GraphView }

// invocationInput is the monitoring-feature build with the graph-stage
// presentation supplied, so the render publishes the mid-render presentation the
// rule is invoked against.
func invocationInput(t *testing.T) renderer.RendererInput {
	t.Helper()
	input := monitoringFeatureInput(t)
	input.GraphView = invocationGraphFacts{}
	return input
}

// The artifact names this file's fixtures use.
//
// Every one carries a prefix no other fixture in the package uses, and that is
// load-bearing rather than tidy: the recording seam below replaces a package-level
// value, so a render running in another test would be recorded too. Filtering the
// record by a name only these fixtures write makes a foreign render unable to
// change any assertion here, whatever order the suite runs in.
const invocationPrefix = "pillar-invocation-"

var (
	// invocationHostEntries are the entries the HOST component's own Recipe
	// declares, in the order that Recipe declares them — which is the order the
	// artifacts walk reaches them in, and therefore the order the rule must be
	// invoked in.
	invocationHostEntries = []string{
		invocationPrefix + "plain.yaml",
		invocationPrefix + "values.yaml",
		invocationPrefix + "instance.yaml",
	}

	// invocationFeatureEntry is the entry a FEATURE's FeatureRecipe declares. It
	// flows through the same artifacts walk as the host's own entries, under the
	// host component's node identity, and must contribute no subject.
	invocationFeatureEntry = invocationPrefix + "feature.yaml"

	// invocationOffendingEntry names a pillar the Recipe does not declare.
	invocationOffendingEntry = invocationPrefix + "offender.yaml"
)

// The template bodies. The admitted ones read only roots every Recipe has, so the
// admitting build completes and the rule's silence is attributable to the rule
// rather than to a build that never reached it.
const (
	invocationPlainBody    = "kind: Plain\n"
	invocationValuesBody   = "kind: Values\nport: {{ .values.port }}\n"
	invocationInstanceBody = "kind: Instance\nid: {{ .context.instance_id }}\n"
	invocationFeatureBody  = "kind: FromTheFeature\nport: {{ .values.scrape_port }}\n"

	// invocationOffendingBody descends through a pillar the Recipe does not carry.
	// The fixtures below declare a workload and no source, so this is the source
	// arm, and the DEPTH matters: a one-segment read would be admitted by
	// evaluation and would write the literal placeholder into the output, while
	// this one fails evaluation with wording that names only the first hop. The
	// rule stands in front of both.
	invocationOffendingBody = "kind: Offender\nchart: {{ .source.chart }}\n"
)

// invocationRecord is what a value was invoked over, in invocation order.
type invocationRecord struct {
	node string

	// attachment is empty for the value declared over a node's own Recipe, and
	// carries the attachment for the value declared over the carrier an
	// attachment's FeatureRecipe is reached through.
	attachment string

	artifact string
}

// recordArtifactPillarInvocations replaces the declared rule with a wrapper that
// records every subject the production path invokes it over, and restores the
// declaration when the test ends.
//
// The wrapper DELEGATES rather than substituting a verdict, so the build under
// observation behaves exactly as it does unobserved: what is measured is the
// invocation, not a stand-in for it.
//
// It records rather than counting, and the difference is the whole instrument. A
// count alone cannot separate "invoked once per entry" from "invoked the right
// number of times over the wrong entries", and this rule's axis is exactly where
// that confusion lives — a narrowed axis asks fewer questions, both readings then
// agree, nothing is raised, and that is indistinguishable from a rule that ran and
// approved. So the identities are kept and compared.
//
// The caller must not run in parallel with itself, and no assertion here may rest
// on a record entry this file's own fixtures did not write.
func recordArtifactPillarInvocations(t *testing.T) func() []invocationRecord {
	t.Helper()

	var (
		mu   sync.Mutex
		seen []invocationRecord
	)
	declared := renderer.ArtifactPillarDeclared
	renderer.ArtifactPillarDeclared = constraint.Check[constraint.HydratedView, constraint.NodeArtifactSubject]{
		Sentinel: declared.Sentinel,
		Severity: declared.Severity,
		Run: func(v constraint.HydratedView, s constraint.NodeArtifactSubject) []constraint.Objection {
			mu.Lock()
			seen = append(seen, invocationRecord{node: s.NodeID(), artifact: s.ArtifactName()})
			mu.Unlock()
			return declared.Run(v, s)
		},
	}
	t.Cleanup(func() { renderer.ArtifactPillarDeclared = declared })

	return func() []invocationRecord {
		mu.Lock()
		defer mu.Unlock()
		out := make([]invocationRecord, 0, len(seen))
		for _, r := range seen {
			if strings.HasPrefix(r.artifact, invocationPrefix) {
				out = append(out, r)
			}
		}
		return out
	}
}

// recordAttachmentArtifactPillarInvocations is the same instrument for the value
// declared over the carrier an attachment's FeatureRecipe is reached through on a
// node.
//
// It is a second function rather than a widening of the one above because the two
// wrap different package-level declarations, and because the assertions above are
// about what the node's OWN value was invoked over — folding an attachment's
// invocations into that record would make the enumeration there report entries the
// node's Recipe never declared.
//
// The caller must not run in parallel with itself.
func recordAttachmentArtifactPillarInvocations(t *testing.T) func() []invocationRecord {
	t.Helper()

	var (
		mu   sync.Mutex
		seen []invocationRecord
	)
	declared := renderer.AttachmentArtifactPillarDeclared
	renderer.AttachmentArtifactPillarDeclared = constraint.Check[
		constraint.HydratedView, constraint.NodeAttachmentArtifactSubject,
	]{
		Sentinel: declared.Sentinel,
		Severity: declared.Severity,
		Run: func(
			v constraint.HydratedView, s constraint.NodeAttachmentArtifactSubject,
		) []constraint.Objection {
			mu.Lock()
			seen = append(seen, invocationRecord{
				node: s.NodeID(), attachment: s.AttachmentName(), artifact: s.ArtifactName(),
			})
			mu.Unlock()
			return declared.Run(v, s)
		},
	}
	t.Cleanup(func() { renderer.AttachmentArtifactPillarDeclared = declared })

	return func() []invocationRecord {
		mu.Lock()
		defer mu.Unlock()
		out := make([]invocationRecord, 0, len(seen))
		for _, r := range seen {
			if strings.HasPrefix(r.artifact, invocationPrefix) {
				out = append(out, r)
			}
		}
		return out
	}
}

// invocationLookup binds the host component to a Recipe declaring the given
// artifacts and a workload, and the monitoring feature to a FeatureRecipe
// declaring the given artifacts and a workload.
//
// Both sides declare a workload and neither declares a source, so a `.source`
// reference is undeclared on either — which is what lets one template body be
// carried down both paths and have the same thing be wrong with it.
func invocationLookup(
	hostArtifacts, featureArtifacts types.OrderedMap[types.ArtifactEntry],
) renderer.RecipeLookup {
	const workload = `apiVersion: aggrega.ai/v1alpha1
kind: Workload
containers:
  main:
    image: myapp:latest
`
	return renderer.RecipeLookup{
		Recipe: func(_, componentType string) (*types.Recipe, bool) {
			if componentType != "core/compute/api-server" {
				return nil, false
			}
			return &types.Recipe{
				Metadata: types.RecipeMetadata{Name: "api-recipe", Version: "1.0.0"},
				Spec: types.RecipeSpec{
					Capability: "helm",
					Implements: "core/compute/api-server",
					Workload:   types.WorkloadTemplate{Inline: workload},
					Artifacts:  hostArtifacts,
				},
			}, true
		},
		FeatureRecipe: func(_, featureType string) (*types.FeatureRecipe, bool) {
			if featureType != "core/monitoring" {
				return nil, false
			}
			return &types.FeatureRecipe{
				Metadata: types.FeatureRecipeMetadata{Name: "mon"},
				Spec: types.FeatureRecipeSpec{
					Capability: "helm",
					Implements: "core/monitoring",
					Workload:   types.WorkloadTemplate{Inline: workload},
					Artifacts:  featureArtifacts,
				},
			}, true
		},
	}
}

// artifactSet builds an artifacts block in the order the pairs are given.
func artifactSet(pairs ...[2]string) types.OrderedMap[types.ArtifactEntry] {
	out := types.OrderedMap[types.ArtifactEntry]{}
	for _, p := range pairs {
		out.Set(p[0], types.ArtifactEntry{Inline: p[1]})
	}
	return out
}

// TestTheArtifactPillarRuleIsInvokedOncePerArtifactANodesOwnRecipeProjects is the
// enumeration assertion for the production invocation, stated over a build that
// carries BOTH paths through the artifacts walk.
//
// It asserts what the rule was invoked over, by identity and in order, rather than
// that two readings agreed. An agreement proof cannot stand in for it: a narrowed
// axis presents fewer subjects, both readings are then asked about fewer things,
// they agree, and nothing is raised — which is exactly what a rule that ran and
// approved looks like. Two refused axis designs have already been planted as
// needles on this surface and both left an agreement assertion green.
//
// It is also the proof that the FeatureRecipe path contributes no subject to THIS
// value. The feature's entry flows through the same walk, under the same node
// identity, and differs only in which carrier its render records against and reads
// through — so an invocation over it here would be indistinguishable from the
// host's in every coordinate this subject carries, and the only thing that can show
// its absence is the recorded set. What that entry does reach is the value declared
// over the attachment's own carrier, which carries the attachment as a coordinate
// and is asserted separately.
//
// It does not run in parallel: it replaces a package-level declaration.
func TestTheArtifactPillarRuleIsInvokedOncePerArtifactANodesOwnRecipeProjects(t *testing.T) {
	recorded := recordArtifactPillarInvocations(t)

	input := invocationInput(t)
	lookup := invocationLookup(
		artifactSet(
			[2]string{invocationHostEntries[0], invocationPlainBody},
			[2]string{invocationHostEntries[1], invocationValuesBody},
			[2]string{invocationHostEntries[2], invocationInstanceBody},
		),
		artifactSet([2]string{invocationFeatureEntry, invocationFeatureBody}),
	)

	result, err := renderer.RenderAll(context.Background(), input, lookup, testHelmFactory(t))
	if err != nil {
		t.Fatalf("the admitting build did not complete, so nothing below is attributable to the rule: %+v", err)
	}

	// The fixture must actually carry both paths, or the exclusion asserted below
	// would hold over a build that never produced the entry it is about.
	var sawHostFile, sawFeatureFile bool
	for k := range result.Files {
		if strings.Contains(k, invocationHostEntries[0]) {
			sawHostFile = true
		}
		if strings.Contains(k, invocationFeatureEntry) {
			sawFeatureFile = true
		}
	}
	if !sawHostFile {
		t.Fatalf("the build emitted no host artifact; the enumeration below would certify nothing. keys: %v", fileKeys(result.Files))
	}
	if !sawFeatureFile {
		t.Fatalf("the build emitted no feature-carried artifact; the exclusion below would hold vacuously. keys: %v", fileKeys(result.Files))
	}

	got := recorded()

	// ANTI-NEEDLE. A build carrying artifacts must produce invocations; a zero here
	// is the whole failure this assertion exists to catch, and it passes every
	// equivalence and byte-identity proof.
	if len(got) == 0 {
		t.Fatal("the rule was invoked ZERO times over a build projecting artifacts.\n" +
			"INVARIANT: the rule is invoked once per artifact a node's own Recipe projects.\n" +
			"CONSEQUENCE: a rule invoked zero times raises nothing, which is indistinguishable " +
			"from a rule that ran and approved — and every determinism, golden and equivalence " +
			"proof over this surface stays green while it happens.")
	}

	// BY COUNT, and by identity in walk order.
	names := make([]string, 0, len(got))
	for _, r := range got {
		names = append(names, r.artifact)
	}
	if !slices.Equal(names, invocationHostEntries) {
		t.Errorf("the rule was invoked over %v; want exactly the host Recipe's own entries in walk order %v.\n"+
			"INVARIANT: one invocation per artifact the node's OWN Recipe projects, in the order the walk reached them.",
			names, invocationHostEntries)
	}
	if len(got) != len(invocationHostEntries) {
		t.Errorf("the rule was invoked %d time(s); want %d — one per entry the host Recipe declares",
			len(got), len(invocationHostEntries))
	}

	// The FeatureRecipe-carried entry must appear in no invocation.
	for _, r := range got {
		if r.artifact == invocationFeatureEntry {
			t.Errorf("the rule was invoked over %q, which a FeatureRecipe carries.\n"+
				"INVARIANT: the fact is declared over a node's OWN Recipe. A FeatureRecipe's entries flow "+
				"through the same walk under the host component's node identity, so admitting them would "+
				"widen the fact and collide with the host's own entries under one identity.", r.artifact)
		}
	}

	// Every invocation is over the one node the fixture declares, so a wider node
	// axis would show up here as a foreign coordinate rather than only as a count.
	for _, r := range got {
		if r.node != "api" {
			t.Errorf("the rule was invoked over node %q; the fixture declares only %q", r.node, "api")
		}
	}

	t.Logf("DIAGNOSTIC WALK — admitting build: rule invoked %d time(s) over %v; objections raised: 0",
		len(got), names)
}

// TestTheArtifactPillarRuleHaltsTheBuildOnANodesOwnRecipe is the control that MUST
// raise, and it reads the halt an author is handed rather than an objection count.
//
// It is the other direction of the proof above. A rule that objected about nothing
// would satisfy the admitting assertion on its own, so the raising direction is
// driven over a fixture differing from it in one thing: one entry names a pillar
// the Recipe does not declare.
//
// It also fixes WHICH of the two readings produced the halt. The rule and the
// pre-check beside it refuse the same input and word it differently, so the
// message is what says the declared rule ran first — and on this path it must,
// because the pre-check returns on the first offending reference while the rule
// reports every one of them.
//
// It does not run in parallel: it replaces a package-level declaration.
func TestTheArtifactPillarRuleHaltsTheBuildOnANodesOwnRecipe(t *testing.T) {
	recorded := recordArtifactPillarInvocations(t)

	input := invocationInput(t)
	lookup := invocationLookup(
		artifactSet(
			[2]string{invocationHostEntries[0], invocationPlainBody},
			[2]string{invocationOffendingEntry, invocationOffendingBody},
		),
		artifactSet([2]string{invocationFeatureEntry, invocationFeatureBody}),
	)

	_, err := renderer.RenderAll(context.Background(), input, lookup, testHelmFactory(t))
	if err == nil {
		t.Fatal("the build completed over an artifact naming an undeclared pillar; want a halt")
	}
	if !errors.Is(err, renderer.ErrArtifactPillarNotDeclared()) {
		t.Fatalf("the halt does not carry the undeclared-pillar condition: %+v", err)
	}

	// The declared rule's wording, which the pre-check does not produce: the
	// registered sentence followed by the offending expression and the captioned
	// artifact. The pre-check words the same refusal as a sentence naming which
	// pillar is absent, so these two cannot be confused for one another.
	msg := cerrors.CauseChain(err)
	for _, want := range []string{
		"artifact template references a pillar the Recipe does not declare",
		`".source.chart"`,
		`artifact "` + invocationOffendingEntry + `"`,
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("the halt does not carry %q, so it is not the declared rule's finding.\nfull halt: %s", want, msg)
		}
	}

	// ANTI-NEEDLE, the other half: the halt must have come from an invoked rule
	// rather than from anything else refusing the same input.
	got := recorded()
	if len(got) == 0 {
		t.Fatal("the rule was invoked zero times, so the halt above came from somewhere else")
	}
	names := make([]string, 0, len(got))
	for _, r := range got {
		names = append(names, r.artifact)
	}
	if !slices.Contains(names, invocationOffendingEntry) {
		t.Errorf("the rule was invoked over %v, which does not include the offending entry %q",
			names, invocationOffendingEntry)
	}

	t.Logf("DIAGNOSTIC WALK — node's own Recipe, AFTER: invoked over %v; halt = %s", names, msg)
}

// TestTheFeatureCarriedArtifactIsRefusedByTheDeclaredRule is the coverage proof
// for the path the node's own value does not reach.
//
// A FeatureRecipe's entries flow through the same artifacts walk as the host's own,
// under the host component's node identity, so the value declared over node-keyed
// facts contributes no subject for one of them — and must not, because those facts
// would answer about the host. The value declared over the attachment's own carrier
// is what reaches them, and this reads the halt an author is handed to say that the
// declared rule is what produced it.
//
// The wording is the whole discriminator, and there are three of them here: the
// registered sentence with the offending expression and the artifact, which only a
// declared value produces; the attachment, which names the carrier the finding is
// about; and the absence of the sentence a second, hand-written reading of this
// condition would produce, which is what turns "the build still fails" into "this
// mechanism refused it".
//
// It does not run in parallel: it replaces package-level declarations.
func TestTheFeatureCarriedArtifactIsRefusedByTheDeclaredRule(t *testing.T) {
	recordedOwn := recordArtifactPillarInvocations(t)
	recordedAttachment := recordAttachmentArtifactPillarInvocations(t)

	input := invocationInput(t)
	lookup := invocationLookup(
		artifactSet([2]string{invocationHostEntries[0], invocationPlainBody}),
		artifactSet([2]string{invocationOffendingEntry, invocationOffendingBody}),
	)

	_, err := renderer.RenderAll(context.Background(), input, lookup, testHelmFactory(t))
	if err == nil {
		t.Fatal("a FeatureRecipe artifact naming an undeclared pillar was admitted; want a halt")
	}
	if !errors.Is(err, renderer.ErrArtifactPillarNotDeclared()) {
		t.Fatalf("the halt does not carry the undeclared-pillar condition: %+v", err)
	}

	msg := cerrors.CauseChain(err)
	for _, want := range []string{
		"artifact template references a pillar the Recipe does not declare",
		`".source.chart"`,
		`artifact "` + invocationOffendingEntry + `"`,
		`attachment "mon"`,
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("the halt does not carry %q, so it is not the declared rule's finding.\n"+
				"full halt: %s", want, msg)
		}
	}
	if strings.Contains(msg, "but Recipe declares no source:") {
		t.Errorf("the halt carries the hand-written pre-check's wording, so the declared rule did "+
			"not refuse this input first.\nfull halt: %s", msg)
	}

	// ANTI-NEEDLE: the halt must have come from an invoked value.
	attachmentInvocations := recordedAttachment()
	if len(attachmentInvocations) == 0 {
		t.Fatal("the value declared over the attachment's carrier was invoked zero times, so the " +
			"halt above came from somewhere else")
	}
	var sawOffender bool
	for _, r := range attachmentInvocations {
		if r.artifact == invocationOffendingEntry && r.attachment == "mon" {
			sawOffender = true
		}
	}
	if !sawOffender {
		t.Errorf("the value was invoked over %v, which does not include the offending entry %q under "+
			"attachment %q", attachmentInvocations, invocationOffendingEntry, "mon")
	}

	// And the node's own value was NOT invoked over it: the facts keyed by the node
	// alone do not cover an entry a FeatureRecipe carries, which is the whole reason
	// there is a second value.
	for _, r := range recordedOwn() {
		if r.artifact == invocationOffendingEntry {
			t.Errorf("the node's own value was invoked over the FeatureRecipe-carried entry %q; "+
				"the facts it reads are keyed by the node alone and would answer about the host",
				r.artifact)
		}
	}

	t.Logf("DIAGNOSTIC WALK — FeatureRecipe-carried, refused by the declared rule: invoked over %v; "+
		"halt = %s", attachmentInvocations, msg)
}

// TestNoPillarReadingRunsInABuildThatPublishesNoPresentation pins the residual
// state the pillar rule's reach leaves behind, and it is what holds the deletion of
// the pre-check's pillar arms in place.
//
// The arms once refused this input. They are gone: the pillar verdict belongs to
// the rule declared over each carrier the artifacts walk is reached by, and those
// three values read one presentation. A build assembled without it reaches none of
// them — the node's own has no presentation, and each attachment carrier is handed
// none — so nothing reads the pillar facts and the reference travels on to
// evaluation.
//
// Three things are asserted and each catches a different regression. Neither
// declared value is invoked, which is what makes this the presentation-less state
// rather than a build where a rule ran and approved. The halt does NOT carry the
// undeclared-pillar condition, which is the needle on the deletion: an arm added
// back to the pre-check would refuse here and fail this line. And the build still
// halts, from evaluation, because the reference descends through the absent pillar.
//
// The residual is a diagnostic-quality gap rather than a silent admission for THIS
// input, and it is worth being exact about the bound: a reference of one segment to
// an undeclared pillar does not fail evaluation at all — it renders the literal
// placeholder into the artifact — so in this state that shape would ship a file
// nobody asked for, and the build reports success while doing it.
//
// How far that state is out of reach is stated carefully, because an earlier
// version of this comment overstated it and the overstatement is what held the
// deletion in place. What is true: the per-node projection this reading needs is
// unexported, it is installed unconditionally at one site, and no caller outside
// this package can withhold it. What is NOT true is that the state is therefore
// unreachable from outside — the value that silences all three readings is the
// presentation of the finalised graph, which arrives on an EXPORTED input field,
// and an absent one there disables the chain the whole way down without any site
// refusing it. Whether a real compilation can present nothing there is not
// established either way here, so this bound says what the code guarantees and
// stops: the readings are silent whenever that presentation is absent, and nothing
// in the render path checks that it is present.
//
// It does not run in parallel: it replaces package-level declarations.
func TestNoPillarReadingRunsInABuildThatPublishesNoPresentation(t *testing.T) {
	recordedOwn := recordArtifactPillarInvocations(t)
	recordedAttachment := recordAttachmentArtifactPillarInvocations(t)

	// monitoringFeatureInput carries no graph-stage presentation, so the render
	// publishes no mid-render one for any declared value to read.
	input := monitoringFeatureInput(t)
	lookup := invocationLookup(
		artifactSet([2]string{invocationHostEntries[0], invocationPlainBody}),
		artifactSet([2]string{invocationOffendingEntry, invocationOffendingBody}),
	)

	_, err := renderer.RenderAll(context.Background(), input, lookup, testHelmFactory(t))
	if err == nil {
		t.Fatal("a FeatureRecipe artifact descending through an undeclared pillar was admitted " +
			"outright; want a halt, from evaluation if from nothing else")
	}
	msg := cerrors.CauseChain(err)
	if errors.Is(err, renderer.ErrArtifactPillarNotDeclared()) {
		t.Errorf("the halt carries the undeclared-pillar condition in a build no declared value "+
			"could read.\nCONSEQUENCE: a pillar arm has been restored to the hand-written "+
			"pre-check, so one authoring fault is reported by two mechanisms again.\n"+
			"full halt: %s", msg)
	}
	if strings.Contains(msg, "but Recipe declares no source:") {
		t.Errorf("the halt carries the deleted pillar arm's wording.\nfull halt: %s", msg)
	}

	// No declared value ran, which is what makes the reading above the
	// presentation-less one rather than a rule that ran and approved.
	for _, r := range recordedOwn() {
		if r.artifact == invocationOffendingEntry {
			t.Errorf("the node's own value was invoked over %q in a build that published no "+
				"view", r.artifact)
		}
	}
	for _, r := range recordedAttachment() {
		if r.artifact == invocationOffendingEntry {
			t.Errorf("the attachment carrier's value was invoked over %q in a build that published "+
				"no view", r.artifact)
		}
	}

	t.Logf("DIAGNOSTIC WALK — FeatureRecipe-carried, no view published, pillar arms deleted: "+
		"halt = %s", msg)
}

// invocationTwoOffencesBody names TWO pillars the Recipe does not declare, in one
// template. It is what separates the two readings by count rather than by wording:
// the pre-check returns on the first offending reference it reaches, and the rule
// reports every one of them.
const invocationTwoOffencesBody = "kind: Offender\nchart: {{ .source.chart }}\nrepo: {{ .source.repository }}\n"

// TestTheArtifactPillarDiagnosticNamesEveryOffendingReference measures what an
// author is handed for one entry naming TWO pillars its document does not declare,
// and is the artifact the change's diagnostic effect is recorded from.
//
// It previously measured two columns over the same input — the hand-written
// pre-check's reading beside the declared rule's — because both mechanisms were
// live and the count was what separated them: the pre-check returned on the first
// offending reference and the rule reports every one. The pre-check's pillar arms
// are gone, so the BEFORE column has no mechanism left to measure and only the
// remaining reading is asserted here. Nothing about the declared reading's
// assertions changed; what is measured elsewhere is that no pillar reading runs at
// all in a build publishing no presentation.
//
// Two invariants, and the second is why the invocation count sits beside the
// wording. The halt names both offending references, which is the whole of the
// accepted diagnostic-count change. And the rule was invoked exactly ONCE for the
// entry, so the several findings come from one invocation reading the whole
// template rather than from several invocations over the same entry — a
// distinction the message alone cannot express.
//
// It does not run in parallel: it replaces a package-level declaration.
func TestTheArtifactPillarDiagnosticNamesEveryOffendingReference(t *testing.T) {
	recorded := recordArtifactPillarInvocations(t)

	hostArtifacts := func() types.OrderedMap[types.ArtifactEntry] {
		return artifactSet([2]string{invocationOffendingEntry, invocationTwoOffencesBody})
	}
	feature := artifactSet([2]string{invocationFeatureEntry, invocationFeatureBody})

	// The reading the declared rule gives, taken on the node's own path so the
	// entry, the Recipe and the template text are all the ones a reader can look up.
	input := invocationInput(t)
	_, err := renderer.RenderAll(context.Background(), input, invocationLookup(hostArtifacts(), feature), testHelmFactory(t))
	if err == nil {
		t.Fatal("the declared rule admitted a template naming two undeclared pillars")
	}
	if !errors.Is(err, renderer.ErrArtifactPillarNotDeclared()) {
		t.Fatalf("the rule's halt does not carry the undeclared-pillar condition: %+v", err)
	}

	// The rule names BOTH references, which is its stated improvement over the
	// reading it replaced and the whole of the accepted diagnostic-count change.
	msg := cerrors.CauseChain(err)
	for _, want := range []string{`".source.chart"`, `".source.repository"`} {
		if !strings.Contains(msg, want) {
			t.Errorf("the rule's halt does not name %s.\n"+
				"INVARIANT: the rule reports EVERY offending reference in one template rather than the first.\n"+
				"full halt: %s", want, msg)
		}
	}

	// And the wording the deleted arms produced is absent, so the halt is the
	// declared rule's rather than a reinstated second reading of the same input.
	if strings.Contains(msg, "but Recipe declares no source:") {
		t.Errorf("the halt carries the deleted pillar arm's wording.\nfull halt: %s", msg)
	}

	// And the rule was invoked exactly once for the entry, raising two findings from
	// that one invocation — so the count change is in the findings and not in the
	// invocations.
	got := recorded()
	invocations := 0
	for _, r := range got {
		if r.artifact == invocationOffendingEntry {
			invocations++
		}
	}
	if invocations != 1 {
		t.Errorf("the rule was invoked %d time(s) over %q; want exactly 1 — the several findings come from "+
			"one invocation reading the whole template, not from several invocations",
			invocations, invocationOffendingEntry)
	}

	t.Logf("DIAGNOSTIC WALK — ArtifactPillarDeclared, one entry naming two undeclared pillars, "+
		"node's own Recipe: 2 references named\n    %s", msg)
}
