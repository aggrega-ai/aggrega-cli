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

// invocationRecord is what the rule was invoked over, in invocation order.
type invocationRecord struct {
	node     string
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
// It is also the proof that the FeatureRecipe path contributes no subject. The
// feature's entry flows through the same walk, under the same node identity, and
// differs only in that its render installs no retention and no presentation over
// one — so an invocation over it would be indistinguishable from the host's in
// every coordinate the subject carries, and the only thing that can show its
// absence is the recorded set.
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

// TestTheFeatureCarriedArtifactIsStillRefusedByThePreCheck is the coverage proof
// for the path the declared rule does not reach.
//
// The rule contributes no subject for a FeatureRecipe-carried entry, so if the
// pre-check's pillar arms were removed as dead the same authoring fault would
// render a placeholder into the output or halt with wording naming only the first
// hop — with no error, no failing test, and nothing in any diff to say the
// enforcement had gone. This asserts the refusal is still there, and reads the
// pre-check's own wording so the two paths' diagnostics are both on the record.
//
// It does not run in parallel: it replaces a package-level declaration.
func TestTheFeatureCarriedArtifactIsStillRefusedByThePreCheck(t *testing.T) {
	recorded := recordArtifactPillarInvocations(t)

	input := invocationInput(t)
	lookup := invocationLookup(
		artifactSet([2]string{invocationHostEntries[0], invocationPlainBody}),
		artifactSet([2]string{invocationOffendingEntry, invocationOffendingBody}),
	)

	_, err := renderer.RenderAll(context.Background(), input, lookup, testHelmFactory(t))
	if err == nil {
		t.Fatal("a FeatureRecipe artifact naming an undeclared pillar was admitted; want a halt from the pre-check")
	}
	if !errors.Is(err, renderer.ErrArtifactPillarNotDeclared()) {
		t.Fatalf("the halt does not carry the undeclared-pillar condition: %+v", err)
	}

	// The pre-check's own wording, which names which pillar is absent in prose.
	msg := cerrors.CauseChain(err)
	want := `artifact "` + invocationOffendingEntry + `" template references ".source.chart" but Recipe declares no source:`
	if !strings.Contains(msg, want) {
		t.Errorf("the halt does not carry the pre-check's wording %q.\nfull halt: %s", want, msg)
	}

	// And the declared rule was NOT invoked over it — the whole reason the
	// pre-check's arms are load-bearing here.
	for _, r := range recorded() {
		if r.artifact == invocationOffendingEntry {
			t.Errorf("the rule was invoked over the FeatureRecipe-carried entry %q; "+
				"the retention is deliberately absent on that render", r.artifact)
		}
	}

	t.Logf("DIAGNOSTIC WALK — FeatureRecipe-carried, BEFORE and AFTER (unchanged): halt = %s", msg)
}

// invocationTwoOffencesBody names TWO pillars the Recipe does not declare, in one
// template. It is what separates the two readings by count rather than by wording:
// the pre-check returns on the first offending reference it reaches, and the rule
// reports every one of them.
const invocationTwoOffencesBody = "kind: Offender\nchart: {{ .source.chart }}\nrepo: {{ .source.repository }}\n"

// TestTheArtifactPillarDiagnosticCountAndWordingOnBothReadings measures what an
// author is handed by each of the two readings over ONE input, and is the artifact
// the change's diagnostic effect is recorded from.
//
// The two readings are separated on the same build by the one thing that decides
// which runs: whether the render published the mid-render presentation the rule
// reads. A build that publishes it reaches the rule first; a build assembled
// without the presentation beneath it reaches the pre-check, which is also what
// every FeatureRecipe-carried entry reaches. So both columns are measured from a
// real build over the same Recipe, the same entry and the same template text
// rather than inferred from a format string.
//
// It does not run in parallel: it replaces a package-level declaration.
func TestTheArtifactPillarDiagnosticCountAndWordingOnBothReadings(t *testing.T) {
	recorded := recordArtifactPillarInvocations(t)

	hostArtifacts := func() types.OrderedMap[types.ArtifactEntry] {
		return artifactSet([2]string{invocationOffendingEntry, invocationTwoOffencesBody})
	}
	feature := artifactSet([2]string{invocationFeatureEntry, invocationFeatureBody})

	// The reading the pre-check gives. The presentation the rule reads is absent,
	// so the rule contributes no subject — the same state every FeatureRecipe
	// render is in, reached here on the node's own path so the two columns differ
	// in the reading and in nothing else.
	before := monitoringFeatureInput(t)
	_, errBefore := renderer.RenderAll(context.Background(), before, invocationLookup(hostArtifacts(), feature), testHelmFactory(t))
	if errBefore == nil {
		t.Fatal("the pre-check admitted a template naming two undeclared pillars")
	}
	if !errors.Is(errBefore, renderer.ErrArtifactPillarNotDeclared()) {
		t.Fatalf("the pre-check's halt does not carry the undeclared-pillar condition: %+v", errBefore)
	}

	// The reading the declared rule gives, over the same Recipe and the same text.
	after := invocationInput(t)
	_, errAfter := renderer.RenderAll(context.Background(), after, invocationLookup(hostArtifacts(), feature), testHelmFactory(t))
	if errAfter == nil {
		t.Fatal("the declared rule admitted a template naming two undeclared pillars")
	}
	if !errors.Is(errAfter, renderer.ErrArtifactPillarNotDeclared()) {
		t.Fatalf("the rule's halt does not carry the undeclared-pillar condition: %+v", errAfter)
	}

	// The pre-check names ONE reference: it returns on the first it reaches.
	msgBefore := cerrors.CauseChain(errBefore)
	if !strings.Contains(msgBefore, `".source.chart"`) {
		t.Errorf("the pre-check's halt does not name the first offending reference.\nfull halt: %s", msgBefore)
	}
	if strings.Contains(msgBefore, `".source.repository"`) {
		t.Errorf("the pre-check's halt names the SECOND offending reference; it is documented as returning on the first.\n"+
			"full halt: %s", msgBefore)
	}

	// The rule names BOTH, which is its stated improvement over the reading beside
	// it and the whole of the accepted diagnostic-count change.
	msgAfter := cerrors.CauseChain(errAfter)
	for _, want := range []string{`".source.chart"`, `".source.repository"`} {
		if !strings.Contains(msgAfter, want) {
			t.Errorf("the rule's halt does not name %s.\n"+
				"INVARIANT: the rule reports EVERY offending reference in one template rather than the first.\n"+
				"full halt: %s", want, msgAfter)
		}
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

	t.Logf("DIAGNOSTIC WALK — ArtifactPillarDeclared, one entry naming two undeclared pillars.\n"+
		"  BEFORE (pre-check, and still the FeatureRecipe path): 1 reference named\n    %s\n"+
		"  AFTER  (declared rule, node's own Recipe): 2 references named\n    %s",
		msgBefore, msgAfter)
}
