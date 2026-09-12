// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// The provider-compatibility rule is declared once per authored pipeline, and
// this file runs each declared value through the framework's own traversal over
// a fixture.
//
// # Why the rule is exercised here rather than left to the declaration assertions
//
// A declared check value compiles, satisfies the placement computation, and can
// still be wrong in a way no declaration assertion sees: it can read a fact for
// the wrong instance, branch the wrong way on a scope, or — the one that matters
// most for a rule spanning three authoring sites — cover fewer sites than it
// appears to. A rule whose declaration reads as placed while governing one of
// three pipelines is worse than a missing one, because nothing prompts a second
// look. So the site count is measured by running the rule, never asserted from
// the enumerators.
//
// # The view stub embeds the view interface, and the nil embedding is the point
//
// providerFactStub embeds [constraint.DescribedView] and overrides only the
// accessors these three rules read. The embedded interface is nil, so any other
// accessor panics if a predicate reaches for it — which turns "the predicate
// reads declared facts and nothing else" from a claim about the source into a
// property this test enforces. Adding a fact read to one of the rules without
// supplying it here fails loudly rather than silently returning a zero value.

// providerFactStub answers the facts the three provider-compatibility rules read
// and nothing else.
type providerFactStub struct {
	constraint.DescribedView

	// nodeProvider is keyed by node; attachProvider and boundaryAttachProvider by
	// their own coordinate pairs, joined by a separator no coordinate contains.
	nodeProvider           map[string]string
	attachProvider         map[string]string
	boundaryAttachProvider map[string]string

	// attachExecutionScope is keyed by the same pair attachProvider is, and
	// answers empty for an attachment the fixture leaves out — which is the same
	// answer a FeatureRecipe stating no scope gives.
	attachExecutionScope map[string]string

	// boundaryAttachExecutionScope is the same fact keyed at the boundary, read
	// by the rules rooted there. It is a separate map rather than a derivation of
	// the one above because the two are separate reads of the presentation: a
	// boundary-rooted rule is handed a boundary and an attachment name and has no
	// component to key the per-node fact by.
	boundaryAttachExecutionScope map[string]string

	// The per-step facts, keyed by each site's own coordinates.
	nodeStepScope     map[string]string
	nodeStepCaps      map[string][]string
	attachStepScope   map[string]string
	attachStepCaps    map[string][]string
	boundaryStepScope map[string]string
	boundaryStepCaps  map[string][]string
}

func factKey(parts ...string) string { return strings.Join(parts, "\x00") }

func (s providerFactStub) NodeRenderingProvider(node string) string {
	return s.nodeProvider[factKey(node)]
}

func (s providerFactStub) AttachmentRenderingProvider(node, attachment string) string {
	return s.attachProvider[factKey(node, attachment)]
}

func (s providerFactStub) BoundaryAttachmentRenderingProvider(boundary, attachment string) string {
	return s.boundaryAttachProvider[factKey(boundary, attachment)]
}

func (s providerFactStub) StepExportScope(node, step string) string {
	return s.nodeStepScope[factKey(node, step)]
}

func (s providerFactStub) StepPluginCompatibleCapabilities(node, step string) []string {
	return s.nodeStepCaps[factKey(node, step)]
}

func (s providerFactStub) AttachmentExecutionScope(node, attachment string) string {
	return s.attachExecutionScope[factKey(node, attachment)]
}

func (s providerFactStub) BoundaryAttachmentExecutionScope(boundary, attachment string) string {
	return s.boundaryAttachExecutionScope[factKey(boundary, attachment)]
}

func (s providerFactStub) AttachmentStepExportScope(node, attachment, step string) string {
	return s.attachStepScope[factKey(node, attachment, step)]
}

func (s providerFactStub) AttachmentStepPluginCompatibleCapabilities(node, attachment, step string) []string {
	return s.attachStepCaps[factKey(node, attachment, step)]
}

func (s providerFactStub) BoundaryAttachmentStepExportScope(boundary, attachment, step string) string {
	return s.boundaryStepScope[factKey(boundary, attachment, step)]
}

func (s providerFactStub) BoundaryAttachmentStepPluginCompatibleCapabilities(boundary, attachment, step string) []string {
	return s.boundaryStepCaps[factKey(boundary, attachment, step)]
}

// providerSiteFixture implements the enumerators the three traversals need.
//
// Its slices are indexed rather than ranged, so the order a traversal observes
// comes from the slices and never from a map walk.
type providerSiteFixture struct {
	nodes          []constraint.NodeInstance
	boundaries     []constraint.AuthoredCoordinate
	attachments    map[string][]string
	boundaryAttach map[string][]string
	pipeline       map[string][]constraint.AuthoredCoordinate
	attachPipeline map[string][]constraint.AuthoredCoordinate
	boundaryPipe   map[string][]constraint.AuthoredCoordinate
}

func (f providerSiteFixture) Nodes() []constraint.NodeInstance { return f.nodes }

func (f providerSiteFixture) Boundaries() []constraint.AuthoredCoordinate { return f.boundaries }

func (f providerSiteFixture) Attachments(node string) []string {
	return f.attachments[factKey(node)]
}

func (f providerSiteFixture) BoundaryAttachments(boundary string) []string {
	return f.boundaryAttach[factKey(boundary)]
}

func (f providerSiteFixture) PipelineSteps(node string) []constraint.AuthoredCoordinate {
	return f.pipeline[factKey(node)]
}

func (f providerSiteFixture) AttachmentPipelineSteps(node, attachment string) []constraint.AuthoredCoordinate {
	return f.attachPipeline[factKey(node, attachment)]
}

func (f providerSiteFixture) BoundaryAttachmentPipelineSteps(boundary, attachment string) []constraint.AuthoredCoordinate {
	return f.boundaryPipe[factKey(boundary, attachment)]
}

// The fixture's shape, chosen so each rule meets every branch it carries.
//
// One node renders through one provider and carries an attachment rendering
// through a different one, which is what makes the two-provider composition
// observable: a rule reading the wrong one of the two produces a different
// verdict rather than the same one.
func newProviderSiteFixture() providerSiteFixture {
	web := constraint.NodeInstance{
		ID: "web", SourceFile: "aggregate.yaml", AuthoredName: "web",
		SubsystemPath: "default", SynthesizedSubsystem: true,
	}
	// A second component the same boundary holds. The boundary's own attachment
	// reaches both, which is what makes a count over the attachment axis able to
	// tell a reading that asks about that attachment once from one that asks about
	// it per component.
	cache := constraint.NodeInstance{
		ID: "cache", SourceFile: "aggregate.yaml", AuthoredName: "cache",
		SubsystemPath: "default", SynthesizedSubsystem: true,
	}
	return providerSiteFixture{
		nodes:      []constraint.NodeInstance{web, cache},
		boundaries: []constraint.AuthoredCoordinate{{Value: "prod", SourceFile: "release.yaml"}},
		// The merged set each component carries: its own attachment where it has
		// one, plus the attachment the boundary declares, which every component the
		// boundary holds carries.
		attachments: map[string][]string{
			factKey("web"):   {"tls", "audit"},
			factKey("cache"): {"audit"},
		},
		boundaryAttach: map[string][]string{
			factKey("prod"): {"audit"},
		},
		// Two steps on the node's own pipeline: one addressing its own documents
		// and one addressing host documents it does not have.
		pipeline: map[string][]constraint.AuthoredCoordinate{
			factKey("web"): {
				{Value: "own_ok", SourceFile: "recipes/web.yaml"},
				{Value: "own_bad", SourceFile: "recipes/web.yaml"},
				{Value: "host_unreachable", SourceFile: "recipes/web.yaml"},
			},
		},
		// The attachment's pipeline reaches both document sets, which is the only
		// site where it can.
		attachPipeline: map[string][]constraint.AuthoredCoordinate{
			factKey("web", "tls"): {
				{Value: "own_ok", SourceFile: "featurerecipes/tls.yaml"},
				{Value: "host_ok", SourceFile: "featurerecipes/tls.yaml"},
				{Value: "host_bad", SourceFile: "featurerecipes/tls.yaml"},
			},
			// One authored pipeline, reached through two components because both
			// carry the attachment the boundary declares.
			factKey("web", "audit"): {
				{Value: "own_bad", SourceFile: "featurerecipes/audit.yaml"},
			},
			factKey("cache", "audit"): {
				{Value: "own_bad", SourceFile: "featurerecipes/audit.yaml"},
			},
		},
		boundaryPipe: map[string][]constraint.AuthoredCoordinate{
			factKey("prod", "audit"): {
				{Value: "own_bad", SourceFile: "featurerecipes/audit.yaml"},
			},
		},
	}
}

func newProviderFactStub() providerFactStub {
	return providerFactStub{
		// The node renders through helm; its attachment renders through compose.
		nodeProvider: map[string]string{factKey("web"): "helm"},
		attachProvider: map[string]string{
			factKey("web", "tls"):     "compose",
			factKey("web", "audit"):   "terraform",
			factKey("cache", "audit"): "terraform",
		},
		boundaryAttachProvider: map[string]string{factKey("prod", "audit"): "terraform"},

		nodeStepScope: map[string]string{
			factKey("web", "own_ok"):           types.ScopeOwn,
			factKey("web", "own_bad"):          "", // empty defaults to own
			factKey("web", "host_unreachable"): types.ScopeHost,
		},
		nodeStepCaps: map[string][]string{
			factKey("web", "own_ok"):  {"helm", "compose"},
			factKey("web", "own_bad"): {"compose"},
			// A plugin declaring nothing matches no provider — and this step is
			// still not reported, because its scope names documents the site has
			// none of.
			factKey("web", "host_unreachable"): nil,
		},

		// The attachment the boundary declares carries the boundary scope; the
		// component's own attachment carries none, which is the ordinary case.
		attachExecutionScope: map[string]string{
			factKey("web", "audit"):   executionScopeBoundary,
			factKey("cache", "audit"): executionScopeBoundary,
		},
		// The same recipe read at the boundary it was declared on. It has to agree
		// with the per-node answer above: the two keyings name one FeatureRecipe,
		// and a fixture where they disagreed would exercise a state no build can
		// reach while reading as though it covered something.
		boundaryAttachExecutionScope: map[string]string{
			factKey("prod", "audit"): executionScopeBoundary,
		},

		attachStepScope: map[string]string{
			factKey("web", "tls", "own_ok"):      types.ScopeOwn,
			factKey("web", "tls", "host_ok"):     types.ScopeHost,
			factKey("web", "tls", "host_bad"):    types.ScopeHost,
			factKey("web", "audit", "own_bad"):   types.ScopeOwn,
			factKey("cache", "audit", "own_bad"): types.ScopeOwn,
		},
		attachStepCaps: map[string][]string{
			// Matched against the attachment's own provider, compose.
			factKey("web", "tls", "own_ok"): {"compose"},
			// Matched against the HOST node's provider, helm — so a set naming
			// only compose passes the own arm and fails the host arm, which is
			// what proves the two arms read two different facts.
			factKey("web", "tls", "host_ok"):  {"helm"},
			factKey("web", "tls", "host_bad"): {"compose"},
			// The boundary-declared attachment's step is refused wherever it is
			// asked, so any component this rule does report it for shows up.
			factKey("web", "audit", "own_bad"):   {"nothing"},
			factKey("cache", "audit", "own_bad"): {"nothing"},
		},

		boundaryStepScope: map[string]string{
			factKey("prod", "audit", "own_bad"): types.ScopeOwn,
		},
		boundaryStepCaps: map[string][]string{
			factKey("prod", "audit", "own_bad"): {"helm"},
		},
	}
}

// objectionRecord is one raised objection flattened to what the assertions
// compare: where the framework said it was, and what the rule named.
func objectionRecord(o constraint.Objection) string {
	return o.SourceFile() + "|" + o.Locator() + "|" + strings.Join(o.Detail(), ",")
}

// TestTheProviderCompatibilityRuleReachesEveryAuthoredPipeline runs each declared
// value through its own traversal and states the site count it actually reached.
//
// The count is the number of distinct authored pipelines a declared value was
// invoked over, measured by running the rules rather than read off the
// enumerators — a rule can be declared over a kind whose traversal nothing
// reaches, and that failure looks identical to full coverage from the
// declaration side.
func TestTheProviderCompatibilityRuleReachesEveryAuthoredPipeline(t *testing.T) {
	t.Parallel()

	fixture := newProviderSiteFixture()
	facts := constraint.DescribedView(newProviderFactStub())

	// One row per authored pipeline. Each row names its own site, carries the
	// declared value it runs and the traversal that reaches it, and states what
	// that traversal owes over the fixture — so a row is matched by name below and
	// never by position: two lists agreeing positionally while both name the wrong
	// site is exactly the shape a count comparison cannot see.
	cases := []struct {
		site       string
		run        func(count *int) ([]constraint.Objection, error)
		invoked    int
		objections []string
	}{
		{
			// Three steps enumerated. own_ok passes against the node's provider,
			// own_bad objects against it, and host_unreachable is not enforced at
			// all — a component's own pipeline stands against no host component, so
			// its plugin declaring no capability at all still raises nothing here.
			site: "component Recipe pipeline",
			run: func(count *int) ([]constraint.Objection, error) {
				return constraint.RunOnNodePipelineSteps(nil, fixture, fixture, facts,
					countingCheck(PipelineStepProviderCompatible, count))
			},
			invoked:    3,
			objections: []string{"recipes/web.yaml|.spec.pipeline.own_bad|helm"},
		},
		{
			// Five steps enumerated: three on the component's own attachment, plus
			// the boundary-declared attachment's one step reached once through each
			// of the two components that carry it. own_ok passes against the
			// ATTACHMENT's provider while host_ok passes against the HOST NODE's —
			// the two arms read two different facts, and host_bad's objection naming
			// helm rather than compose is what proves the host arm read the
			// node-keyed one. The two boundary-declared subjects raise nothing
			// because the rule ends on an attachment carrying the boundary scope;
			// that is asserted on its own below, by name and by count.
			site: "attachment FeatureRecipe pipeline",
			run: func(count *int) ([]constraint.Objection, error) {
				return constraint.RunOnNodeAttachmentPipelineSteps(nil, fixture, fixture, fixture, facts,
					countingCheck(AttachmentPipelineStepProviderCompatible, count))
			},
			invoked:    5,
			objections: []string{"featurerecipes/tls.yaml|.spec.pipeline.host_bad|tls,helm"},
		},
		{
			// One step, matched against the boundary attachment's own provider.
			site: "boundary-scope FeatureRecipe pipeline",
			run: func(count *int) ([]constraint.Objection, error) {
				return constraint.RunOnBoundaryAttachmentPipelineSteps(nil, fixture, fixture, fixture, facts,
					countingCheck(BoundaryAttachmentPipelineStepProviderCompatible, count))
			},
			invoked:    1,
			objections: []string{"featurerecipes/audit.yaml|.spec.pipeline.own_bad|audit,terraform"},
		},
	}

	reached := 0
	for _, tc := range cases {
		invoked := 0
		got, err := tc.run(&invoked)
		if err != nil {
			t.Errorf("the traversal over the %s returned %v with no cancellation signal supplied; "+
				"want no error — a stopped walk hands back a short objection set, which is "+
				"indistinguishable from a rule that ran and approved", tc.site, err)
			continue
		}

		if invoked == 0 {
			t.Errorf("the declared rule over the %s was invoked zero times.\n"+
				"INVARIANT: the rule governs every authored pipeline, and a declared value covers a "+
				"site only if its traversal reaches it.\n"+
				"CONSEQUENCE: a rule invoked no times raises nothing, which is indistinguishable from "+
				"a rule that ran and approved — and the placement table reads as though the row were "+
				"closed.", tc.site)
			continue
		}
		reached++

		if invoked != tc.invoked {
			t.Errorf("the rule over the %s was invoked %d time(s); want %d", tc.site, invoked, tc.invoked)
		}

		records := objectionRecords(got)
		if missing, unexpected := setDifference(tc.objections, records); len(missing) > 0 || len(unexpected) > 0 {
			t.Errorf("the rule over the %s raised the wrong objections.\n"+
				"not raised: %v\nraised and not expected: %v\n"+
				"The detail names the provider the declaration was matched against, so a wrong "+
				"provider here is a rule reading a fact keyed at the wrong coordinates.",
				tc.site, missing, unexpected)
		}
	}

	if reached != len(cases) {
		t.Errorf("the declared rule reached %d of %d authored pipelines", reached, len(cases))
	}
	t.Logf("site coverage: the declared provider-compatibility rule reached %d authored pipeline(s) "+
		"of %d, measured by invocation rather than read off the enumerators", reached, len(cases))
}

// countingCheck wraps a declared value so a traversal's invocation count is
// observable without the value itself carrying test-only state.
//
// It copies the sentinel and the severity from the declaration rather than
// restating them, so a value whose sentinel changed is still the value under
// test here.
func countingCheck[S constraint.Subject](
	declared constraint.Check[constraint.DescribedView, S],
	count *int,
) constraint.Check[constraint.DescribedView, S] {
	return constraint.Check[constraint.DescribedView, S]{
		Sentinel: declared.Sentinel,
		Severity: declared.Severity,
		Run: func(v constraint.DescribedView, s S) []constraint.Objection {
			*count++
			return declared.Run(v, s)
		},
	}
}

// setDifference names what each side holds and the other does not, because two
// sides short by one still agree on a count.
func setDifference(want, got []string) (missing, unexpected []string) {
	index := func(ss []string) map[string]int {
		out := map[string]int{}
		for _, s := range ss {
			out[s]++
		}
		return out
	}
	wantIndex, gotIndex := index(want), index(got)
	for _, s := range want {
		if gotIndex[s] == 0 {
			missing = append(missing, s)
		}
	}
	for _, s := range got {
		if wantIndex[s] == 0 {
			unexpected = append(unexpected, s)
		}
	}
	return missing, unexpected
}

// objectionRecords flattens raised objections in the order they were raised.
func objectionRecords(objections []constraint.Objection) []string {
	out := make([]string, 0, len(objections))
	for _, o := range objections {
		out = append(out, objectionRecord(o))
	}
	return out
}

// TestTheProviderCompatibilityRuleReadsNoUndeclaredFact proves the predicate
// reaches for nothing the stub does not supply.
//
// The stub's embedded view interface is nil, so an accessor it does not override
// panics rather than returning a zero value. Running every rule against it and
// observing no panic is therefore a statement about what the predicates read,
// which no source inspection can make: a read added later fails here loudly
// instead of silently deciding on a zero value.
func TestTheProviderCompatibilityRuleReadsNoUndeclaredFact(t *testing.T) {
	t.Parallel()

	// Calibration: the nil embedding must actually panic, or the absence of a
	// panic below says only that nothing was ever checked.
	func() {
		defer func() {
			if recover() == nil {
				t.Errorf("reading an unsupplied fact from the stub did not panic, so this test cannot " +
					"detect a predicate reaching for an undeclared fact")
			}
		}()
		_ = newProviderFactStub().NodeLabels("web")
	}()

	fixture := newProviderSiteFixture()
	facts := constraint.DescribedView(newProviderFactStub())

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("a provider-compatibility predicate read a fact the stub does not supply: %v.\n"+
				"INVARIANT: a check reads the facts its view declares and nothing else, so every value "+
				"it reads was produced for it.\n"+
				"CONSEQUENCE: a predicate reaching further makes the computed placement describe "+
				"something other than what the check does.", r)
		}
	}()

	if _, err := constraint.RunOnNodePipelineSteps(nil, fixture, fixture, facts,
		PipelineStepProviderCompatible); err != nil {
		t.Fatalf("node pipeline traversal: %v", err)
	}
	if _, err := constraint.RunOnNodeAttachmentPipelineSteps(nil, fixture, fixture, fixture, facts,
		AttachmentPipelineStepProviderCompatible); err != nil {
		t.Fatalf("attachment pipeline traversal: %v", err)
	}
	if _, err := constraint.RunOnBoundaryAttachmentPipelineSteps(nil, fixture, fixture, fixture, facts,
		BoundaryAttachmentPipelineStepProviderCompatible); err != nil {
		t.Fatalf("boundary pipeline traversal: %v", err)
	}
}

// TestTheAttachmentRuleIsNotInvokedForABoundaryDeclaredAttachment is the named
// enumeration assertion for the boundary-scope skip.
//
// An attachment whose FeatureRecipe declares the boundary scope is evaluated once
// for the boundary that holds it, and it appears in the merged attachment set of
// every component that boundary holds. So the rule over a component's attachments
// is presented one authored step once per component that inherited it, on top of
// the report the boundary-rooted rule makes over the same step. The skip is what
// brings the declared set into line with the single evaluation.
//
// # Why a cross-check green could not stand in for this
//
// Two readings asked about fewer things agree about fewer things and raise
// nothing, which is indistinguishable from a satisfied rule. So the property is
// asserted by COUNT and by NAME over an input where every step is refused: the
// subjects the traversal presents are enumerated, and what the rule reports is
// compared against the names it should report and the names it should not.
//
// # The guards, and why each is fatal
//
// A count over a degenerate corpus proves nothing, so three conditions abort
// rather than fail: an axis holding one subject cannot show a difference between
// one report and several; a boundary-declared attachment sitting on one node
// makes "once per component" and "once" the same number; and a corpus where
// nothing is refused makes every count zero.
func TestTheAttachmentRuleIsNotInvokedForABoundaryDeclaredAttachment(t *testing.T) {
	t.Parallel()

	fixture := newProviderSiteFixture()
	stub := newProviderFactStub()
	facts := constraint.DescribedView(stub)

	const declaredAtBoundary = "audit"

	// GUARD 1 — the boundary-declared attachment must sit on more than one node.
	var carrying []string
	for _, node := range fixture.Nodes() {
		for _, attachment := range fixture.Attachments(node.ID) {
			if attachment == declaredAtBoundary {
				carrying = append(carrying, node.ID)
			}
		}
	}
	if len(carrying) < 2 {
		t.Fatalf("the attachment %q is carried by %v; this assertion needs it on more than one node, "+
			"or reporting it once and reporting it per component are the same number",
			declaredAtBoundary, carrying)
	}

	// GUARD 2 — the axis must present more than one subject.
	var presented, ownSubjects, boundarySubjects []string
	for _, node := range fixture.Nodes() {
		for _, attachment := range fixture.Attachments(node.ID) {
			for _, step := range fixture.AttachmentPipelineSteps(node.ID, attachment) {
				subject := node.ID + "/" + attachment + "/" + step.Value
				presented = append(presented, subject)
				if attachment == declaredAtBoundary {
					boundarySubjects = append(boundarySubjects, subject)
				} else {
					ownSubjects = append(ownSubjects, subject)
				}
			}
		}
	}
	if len(presented) < 2 {
		t.Fatalf("the attachment-step axis presents %d subject(s); a count over it cannot discriminate", len(presented))
	}
	if len(boundarySubjects) < 2 || len(ownSubjects) < 1 {
		t.Fatalf("the axis presents %d boundary-declared subject(s) and %d of the components' own; "+
			"the assertion needs both sides populated", len(boundarySubjects), len(ownSubjects))
	}

	// GUARD 3 — every boundary-declared subject must be refused when it IS asked,
	// or the absence of a report below says only that nothing was wrong.
	//
	// The control reads the same rule with the scope fact answering empty, which is
	// what a FeatureRecipe stating no scope reports. It must report every one of
	// them.
	control := stub
	control.attachExecutionScope = map[string]string{}
	controlObjections, err := constraint.RunOnNodeAttachmentPipelineSteps(
		nil, fixture, fixture, fixture, constraint.DescribedView(control),
		AttachmentPipelineStepProviderCompatible,
	)
	if err != nil {
		t.Fatalf("control traversal: %v", err)
	}
	controlReported := reportedAttachments(controlObjections)
	if got := countReported(controlReported, declaredAtBoundary); got != len(boundarySubjects) {
		t.Fatalf("the control reported the boundary-declared attachment %d time(s) over %d subject(s); "+
			"without one report per subject there is no duplicate for the skip to remove",
			got, len(boundarySubjects))
	}

	// THE ASSERTION. With the scope fact answering, the rule reports the
	// components' own attachments and none of the boundary-declared subjects.
	objections, err := constraint.RunOnNodeAttachmentPipelineSteps(
		nil, fixture, fixture, fixture, facts,
		AttachmentPipelineStepProviderCompatible,
	)
	if err != nil {
		t.Fatalf("traversal: %v", err)
	}
	reported := reportedAttachments(objections)
	if got := countReported(reported, declaredAtBoundary); got != 0 {
		t.Errorf("the rule reported the boundary-declared attachment %d time(s); want 0.\n"+
			"reported: %v", got, reported)
	}
	if len(objections) != len(controlObjections)-len(boundarySubjects) {
		t.Errorf("the rule raised %d objection(s); want %d — the control's %d less the %d "+
			"boundary-declared subjects the skip removes",
			len(objections), len(controlObjections)-len(boundarySubjects),
			len(controlObjections), len(boundarySubjects))
	}
	// The component's own attachment is still reported, so the skip removed the
	// inherited subjects and nothing else.
	if countReported(reported, "tls") == 0 {
		t.Errorf("the rule stopped reporting the component's own attachment; reported: %v", reported)
	}
	t.Logf("attachment-step axis presented %d subject(s) — %d of the components' own, %d inherited from "+
		"the boundary across %v; the rule reported %d objection(s) against the control's %d",
		len(presented), len(ownSubjects), len(boundarySubjects), carrying,
		len(objections), len(controlObjections))
}

// reportedAttachments names the attachment each objection was raised about, in
// the order they were raised. The attachment is the objection's first detail
// element at this site, which is where the rule puts it.
func reportedAttachments(objections []constraint.Objection) []string {
	out := make([]string, 0, len(objections))
	for _, o := range objections {
		detail := o.Detail()
		if len(detail) == 0 {
			out = append(out, "")
			continue
		}
		out = append(out, detail[0])
	}
	return out
}

// countReported counts how many objections named one attachment.
func countReported(reported []string, attachment string) int {
	n := 0
	for _, name := range reported {
		if name == attachment {
			n++
		}
	}
	return n
}
