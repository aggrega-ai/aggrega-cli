// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// The rule declared over an artifact's pillars is invoked once per entry EACH
// owner projects, and this file drives a real build over all three owners and
// reads what each was invoked over.
//
// # Why an outcome proof would certify nothing here
//
// A rule can be declared, wired and invoked zero times while every determinism,
// byte-identity and equivalence proof over the same build stays green: a rule
// invoked no times raises nothing, and raising nothing is what a rule that ran and
// approved also does. So the invocation itself is what is asserted — by identity
// and not only by count, because a count cannot separate "invoked once per entry"
// from "invoked the right number of times over the wrong entries", and an owner
// key assembled from the wrong pair of the strings this build holds produces a set
// of exactly the right size.
//
// # What the fixture makes visible
//
// It is the four-site fixture beside this file, and three of its properties are
// what the refusal proofs rest on rather than fixture decoration:
//
//   - The host component declares a source and every attachment used below does
//     not. So a rule reading node-keyed facts about an attachment's entry would
//     ADMIT a `.source` reference, and the halt asserted below could not happen.
//   - Every site declares its own delimiter pair and none declares the usual one.
//     So a rule reading the host's pair would collect no reference from an
//     attachment's template and would again admit it.
//   - Every site declares an entry under one shared output name, so a collapsed
//     key space answers one owner's text where another's belongs with the count
//     unchanged.
//
// # Why this instrument is not the one beside the external test file
//
// The two record the same shape and are deliberately not unified: this one is in
// the package under test because the four-site fixture is, and the other is in the
// external test package where the build it observes is assembled. Neither can see
// the other's fixture, and exporting one for the other would widen the package's
// surface for a test helper. Do not "unify" them by moving either.

// The three declared values this file records, named so a record says which was
// invoked rather than only how often.
const (
	ownerRuleNodesOwn         = "node's own Recipe"
	ownerRuleNodeAttachment   = "attachment on a node"
	ownerRuleBoundaryAttached = "boundary-scope attachment"
)

// The artifact names the refusal proofs add to the fixture's own. Every name this
// file and the fixture use carries a prefix no other fixture in the package uses,
// which is load-bearing rather than tidy: the recorder replaces package-level
// values, so a render running in another test would be recorded too. Filtering by
// that prefix makes a foreign render unable to change any assertion here.
const (
	ownerNamePrefix        = "owner-"
	ownerOffendingArtifact = ownerNamePrefix + "offender.yaml"
)

// ownerOffendingBody descends through the source pillar, written with the pair
// the site's OWN provider declares.
//
// The depth matters: a one-segment read is admitted by evaluation and writes the
// literal placeholder into the output, while this one fails evaluation with wording
// naming only the first hop. The rule stands in front of both.
func ownerOffendingBody(capability string) string {
	pair := ownerDelimiters[capability]
	return "kind: Offender\nchart: " + pair[0] + " .source.chart " + pair[1] + "\n"
}

// ownerTwoOffencesBody names TWO pillars the site does not declare, in one
// template. It is what pins the rule to reporting EVERY offending reference a
// template makes rather than the first: over a body carrying one offence, a rule
// that stopped at the first would be indistinguishable from this one.
func ownerTwoOffencesBody(capability string) string {
	pair := ownerDelimiters[capability]
	return "kind: Offender\nchart: " + pair[0] + " .source.chart " + pair[1] +
		"\nrepo: " + pair[0] + " .source.repository " + pair[1] + "\n"
}

// ownerOffendingBlock is an artifacts block holding one offending entry and
// nothing else.
func ownerOffendingBlock(body string) types.OrderedMap[types.ArtifactEntry] {
	out := types.OrderedMap[types.ArtifactEntry]{}
	out.Set(ownerOffendingArtifact, types.ArtifactEntry{Inline: body})
	return out
}

// ownerInvocation is one invocation of one of the three values, in invocation
// order.
type ownerInvocation struct {
	// rule names which of the three values was invoked.
	rule string

	// owner is the node id for the two node-rooted values and the boundary's
	// scope path for the boundary-rooted one.
	owner string

	// attachment is empty for the value declared over a node's own Recipe.
	attachment string

	// artifact is the output name the entry resolved to.
	artifact string
}

// recordOwnerPillarInvocations replaces all three declared values with wrappers
// that record every subject the production path invokes them over, and restores
// the declarations when the test ends.
//
// Each wrapper DELEGATES rather than substituting a verdict, so the build under
// observation behaves exactly as it does unobserved: what is measured is the
// invocation, not a stand-in for it. All three are wrapped by one call because the
// proofs are about which owner reached which entry, and a recorder covering one
// value could not show an entry reaching the wrong one.
//
// The caller must not run in parallel: it replaces package-level declarations.
func recordOwnerPillarInvocations(t *testing.T) func() []ownerInvocation {
	t.Helper()

	var (
		mu   sync.Mutex
		seen []ownerInvocation
	)
	add := func(r ownerInvocation) {
		mu.Lock()
		seen = append(seen, r)
		mu.Unlock()
	}

	own := ArtifactPillarDeclared
	onNode := AttachmentArtifactPillarDeclared
	onBoundary := BoundaryAttachmentArtifactPillarDeclared

	ArtifactPillarDeclared = constraint.Check[constraint.HydratedView, constraint.NodeArtifactSubject]{
		Sentinel: own.Sentinel,
		Severity: own.Severity,
		Run: func(v constraint.HydratedView, s constraint.NodeArtifactSubject) []constraint.Objection {
			add(ownerInvocation{
				rule: ownerRuleNodesOwn, owner: s.NodeID(), artifact: s.ArtifactName(),
			})
			return own.Run(v, s)
		},
	}
	AttachmentArtifactPillarDeclared = constraint.Check[
		constraint.HydratedView, constraint.NodeAttachmentArtifactSubject,
	]{
		Sentinel: onNode.Sentinel,
		Severity: onNode.Severity,
		Run: func(
			v constraint.HydratedView, s constraint.NodeAttachmentArtifactSubject,
		) []constraint.Objection {
			add(ownerInvocation{
				rule:       ownerRuleNodeAttachment,
				owner:      s.NodeID(),
				attachment: s.AttachmentName(),
				artifact:   s.ArtifactName(),
			})
			return onNode.Run(v, s)
		},
	}
	BoundaryAttachmentArtifactPillarDeclared = constraint.Check[
		constraint.HydratedView, constraint.BoundaryAttachmentArtifactSubject,
	]{
		Sentinel: onBoundary.Sentinel,
		Severity: onBoundary.Severity,
		Run: func(
			v constraint.HydratedView, s constraint.BoundaryAttachmentArtifactSubject,
		) []constraint.Objection {
			add(ownerInvocation{
				rule:       ownerRuleBoundaryAttached,
				owner:      s.BoundaryPath(),
				attachment: s.AttachmentName(),
				artifact:   s.ArtifactName(),
			})
			return onBoundary.Run(v, s)
		},
	}
	t.Cleanup(func() {
		ArtifactPillarDeclared = own
		AttachmentArtifactPillarDeclared = onNode
		BoundaryAttachmentArtifactPillarDeclared = onBoundary
	})

	return func() []ownerInvocation {
		mu.Lock()
		defer mu.Unlock()
		out := make([]ownerInvocation, 0, len(seen))
		for _, r := range seen {
			if strings.HasPrefix(r.artifact, ownerNamePrefix) {
				out = append(out, r)
			}
		}
		return out
	}
}

// ownerInvocationsOf narrows a record to one of the three values, keeping
// invocation order.
func ownerInvocationsOf(got []ownerInvocation, rule string) []ownerInvocation {
	out := make([]ownerInvocation, 0, len(got))
	for _, r := range got {
		if r.rule == rule {
			out = append(out, r)
		}
	}
	return out
}

// TestTheArtifactPillarRuleIsInvokedOncePerArtifactEachOwnerProjects is the
// enumeration assertion for the production invocation on all three owners,
// stated over one build that reaches all of them.
//
// It asserts what each value was invoked over, by identity and in walk order,
// rather than that two readings agreed. An agreement proof cannot stand in for it:
// a narrowed axis presents fewer subjects, both readings are then asked about
// fewer things, they agree, and nothing is raised — which is exactly what a rule
// that ran and approved looks like.
//
// It is also the proof that the three owners do not reach into each other. Every
// key here is built from strings the render holds under other names — the host's
// node id, each attachment's instance name, the boundary's scope path — so a
// owner assembled from the wrong pair of them yields a set of the right size,
// and only the named identities can tell the two apart.
//
// It does not run in parallel: it replaces package-level declarations.
func TestTheArtifactPillarRuleIsInvokedOncePerArtifactEachOwnerProjects(t *testing.T) {
	recorded := recordOwnerPillarInvocations(t)

	res, err := RenderAll(
		context.Background(), ownerInput(), ownerLookup(), ownerFactory(),
	)
	if err != nil {
		t.Fatalf("the admitting build did not complete, so nothing below is attributable to the "+
			"invocation: %+v", err)
	}
	if res.HydratedView == nil {
		t.Fatal("the render published no mid-render view; every assertion below would be " +
			"unreachable for a reason unrelated to the invocation")
	}

	got := recorded()

	// ANTI-NEEDLE, per owner. A build carrying artifacts on all three must
	// produce invocations on all three; a zero on any of them is the whole failure
	// this file exists to catch, and it passes every byte-identity, determinism and
	// equivalence proof over the same build.
	for _, rule := range []string{
		ownerRuleNodesOwn, ownerRuleNodeAttachment, ownerRuleBoundaryAttached,
	} {
		if len(ownerInvocationsOf(got, rule)) == 0 {
			t.Fatalf("the value declared over the %s was invoked ZERO times over a build that "+
				"projects artifacts there.\n"+
				"CONSEQUENCE: a rule invoked zero times raises nothing, which is indistinguishable "+
				"from a rule that ran and approved.", rule)
		}
	}

	// The node's own Recipe: one invocation per entry that Recipe declares, and no
	// entry any attachment declares. An attachment's entries flow through the same
	// walk under this node's identity, so an invocation over one of them here would
	// mean the node-keyed facts had been widened to cover them.
	wantOwn := []ownerInvocation{
		{rule: ownerRuleNodesOwn, owner: ownerNode, artifact: ownerSharedArtifactName},
		{rule: ownerRuleNodesOwn, owner: ownerNode, artifact: ownerHostArtifactName},
	}
	if diff := ownerInvocationsOf(got, ownerRuleNodesOwn); !slices.Equal(diff, wantOwn) {
		t.Errorf("the node's own value was invoked over %v; want exactly %v — one per entry the "+
			"node's OWN Recipe declares, in walk order", diff, wantOwn)
	}

	// The node-carried attachments: one invocation per entry per attachment, in
	// attachment order then entry order. Both attachments declare an entry under
	// the shared name, so an owner missing the attachment coordinate would collapse
	// the four into two.
	wantOnNode := []ownerInvocation{
		{
			rule: ownerRuleNodeAttachment, owner: ownerNode,
			attachment: ownerMonInstance, artifact: ownerSharedArtifactName,
		},
		{
			rule: ownerRuleNodeAttachment, owner: ownerNode,
			attachment: ownerMonInstance, artifact: ownerMonArtifactName,
		},
		{
			rule: ownerRuleNodeAttachment, owner: ownerNode,
			attachment: ownerLogsInstance, artifact: ownerSharedArtifactName,
		},
		{
			rule: ownerRuleNodeAttachment, owner: ownerNode,
			attachment: ownerLogsInstance, artifact: ownerLogsArtifactName,
		},
	}
	if diff := ownerInvocationsOf(got, ownerRuleNodeAttachment); !slices.Equal(diff, wantOnNode) {
		t.Errorf("the node-carried attachment value was invoked over %v; want exactly %v",
			diff, wantOnNode)
	}

	// The boundary-scope attachment: rooted at the boundary, once per entry. It is
	// declared once and reaches every component the boundary holds, so a node-rooted
	// invocation would ask about one authored entry once per member.
	wantOnBoundary := []ownerInvocation{
		{
			rule: ownerRuleBoundaryAttached, owner: ownerBoundaryPath,
			attachment: ownerBoundaryInstance, artifact: ownerSharedArtifactName,
		},
		{
			rule: ownerRuleBoundaryAttached, owner: ownerBoundaryPath,
			attachment: ownerBoundaryInstance, artifact: ownerBoundaryArtifact,
		},
	}
	if diff := ownerInvocationsOf(got, ownerRuleBoundaryAttached); !slices.Equal(diff, wantOnBoundary) {
		t.Errorf("the boundary-scope attachment value was invoked over %v; want exactly %v",
			diff, wantOnBoundary)
	}

	// The boundary-scope attachment appears in the node's merged attachment set, so
	// it must not ALSO be reached through the node-rooted value.
	for _, r := range ownerInvocationsOf(got, ownerRuleNodeAttachment) {
		if r.attachment == ownerBoundaryInstance {
			t.Errorf("the node-rooted value was invoked over the boundary-scope attachment %q; "+
				"one authored entry would then be asked about once per component the boundary holds",
				r.attachment)
		}
	}

	t.Logf("DIAGNOSTIC WALK — invocations per owner: node's own %d %v; attachment-on-node %d %v; "+
		"boundary-scope %d %v; objections raised: 0",
		len(ownerInvocationsOf(got, ownerRuleNodesOwn)),
		ownerInvocationsOf(got, ownerRuleNodesOwn),
		len(ownerInvocationsOf(got, ownerRuleNodeAttachment)),
		ownerInvocationsOf(got, ownerRuleNodeAttachment),
		len(ownerInvocationsOf(got, ownerRuleBoundaryAttached)),
		ownerInvocationsOf(got, ownerRuleBoundaryAttached))
}

// ownerRefusalProof drives one build in which exactly one site's artifacts block
// offends, and returns the halt and the record.
func ownerRefusalProof(
	t *testing.T, blocks ownerArtifactBlocks,
) (error, []ownerInvocation) {
	t.Helper()
	recorded := recordOwnerPillarInvocations(t)
	_, err := RenderAll(
		context.Background(), ownerInput(), ownerLookupWithArtifacts(blocks), ownerFactory(),
	)
	if err == nil {
		t.Fatal("an artifact naming a pillar its FeatureRecipe does not declare was admitted; want a halt")
	}
	if !errors.Is(err, ErrArtifactPillarNotDeclared()) {
		t.Fatalf("the halt does not carry the undeclared-pillar condition: %+v", err)
	}
	return err, recorded()
}

// ownerAssertDeclaredRuleRefused reads the halt an author is handed and requires
// it to be the DECLARED rule's finding.
//
// The wording is what makes the message able to say which mechanism ran, and three
// things are required of it: the registered sentence with the offending expression
// and the artifact, which is the objection form only a declared value produces; the
// attachment, which the locator alone cannot name; and the ABSENCE of the sentence
// a second, hand-written reading of this condition would produce. That third one
// keeps a second implementation from appearing beside the rule unnoticed, which is
// what turns "a halt happened" into "this halt came from here".
func ownerAssertDeclaredRuleRefused(t *testing.T, err error, attachment string) string {
	t.Helper()
	msg := cerrors.CauseChain(err)
	for _, want := range []string{
		"artifact template references a pillar the Recipe does not declare",
		`".source.chart"`,
		`artifact "` + ownerOffendingArtifact + `"`,
		`attachment "` + attachment + `"`,
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
	return msg
}

// TestTheDeclaredRuleRefusesAnArtifactAnAttachmentOnANodeCarries is the refusal
// proof for the node-carried owner.
//
// It is the direction the enumeration proof above cannot give: a value that
// objected about nothing would satisfy that one on its own. The fixture differs
// from the admitting one in one thing — the attachment's block names a pillar its
// FeatureRecipe does not declare.
//
// Two properties of the fixture make the halt attributable to the attachment's own
// facts rather than to the host's. The host component DECLARES a source, so a value
// reading node-keyed facts would admit this reference and no halt would occur at
// all; and the attachment's provider declares a delimiter pair of its own, so a
// value reading the host's pair would collect no reference from this template and
// would likewise admit it.
//
// It does not run in parallel: it replaces package-level declarations.
func TestTheDeclaredRuleRefusesAnArtifactAnAttachmentOnANodeCarries(t *testing.T) {
	blocks := ownerAdmittingArtifacts()
	blocks.mon = ownerOffendingBlock(ownerOffendingBody(ownerMonCapability))

	err, got := ownerRefusalProof(t, blocks)
	msg := ownerAssertDeclaredRuleRefused(t, err, ownerMonInstance)

	// ANTI-NEEDLE, the other half: the halt must have come from an INVOKED value
	// rather than from anything else refusing the same input.
	want := ownerInvocation{
		rule: ownerRuleNodeAttachment, owner: ownerNode,
		attachment: ownerMonInstance, artifact: ownerOffendingArtifact,
	}
	if !slices.Contains(got, want) {
		t.Errorf("the record holds %v, which does not include %v — so the halt above did not come "+
			"from the value declared over that owner", got, want)
	}

	// And the node's own value was not invoked over it: the entry belongs to the
	// attachment's FeatureRecipe, and the facts keyed by the node alone do not cover
	// it.
	for _, r := range ownerInvocationsOf(got, ownerRuleNodesOwn) {
		if r.artifact == ownerOffendingArtifact {
			t.Errorf("the node's own value was invoked over %q, which an attachment's FeatureRecipe "+
				"carries", r.artifact)
		}
	}

	t.Logf("DIAGNOSTIC WALK — attachment on a node, refused: invoked over %v; halt = %s", got, msg)
}

// TestTheDeclaredRuleRefusesAnArtifactABoundaryScopeAttachmentCarries is the
// refusal proof for the boundary-rooted owner, and it is the example the widening
// was opened for: a boundary-scope FeatureRecipe writing a source reference in an
// artifact with no source declared.
//
// The host component declares a source here too, so the same reasoning applies: a
// value reading node-keyed facts would admit this reference. And the boundary-scope
// attachment's provider declares its own delimiter pair, so a value reading any
// other site's pair would collect nothing from this template.
//
// It does not run in parallel: it replaces package-level declarations.
func TestTheDeclaredRuleRefusesAnArtifactABoundaryScopeAttachmentCarries(t *testing.T) {
	blocks := ownerAdmittingArtifacts()
	blocks.bridge = ownerOffendingBlock(ownerOffendingBody(ownerBoundaryCapability))

	err, got := ownerRefusalProof(t, blocks)
	msg := ownerAssertDeclaredRuleRefused(t, err, ownerBoundaryInstance)

	want := ownerInvocation{
		rule: ownerRuleBoundaryAttached, owner: ownerBoundaryPath,
		attachment: ownerBoundaryInstance, artifact: ownerOffendingArtifact,
	}
	if !slices.Contains(got, want) {
		t.Errorf("the record holds %v, which does not include %v — so the halt above did not come "+
			"from the value declared over the boundary-rooted owner", got, want)
	}

	// The offending entry must be reached ONCE, through the boundary-rooted owner
	// and not also through the node-rooted one. The attachment appears in the node's
	// merged attachment set, so the wrong rooting is reachable rather than
	// hypothetical.
	for _, r := range ownerInvocationsOf(got, ownerRuleNodeAttachment) {
		if r.artifact == ownerOffendingArtifact {
			t.Errorf("the node-rooted value was invoked over %q, which a boundary-scope attachment "+
				"carries", r.artifact)
		}
	}

	t.Logf("DIAGNOSTIC WALK — boundary-scope attachment, refused: invoked over %v; halt = %s", got, msg)
}

// TestTheOwnerRuleReportsEveryOffendingReferenceInOneTemplate measures what an
// author is handed when one attachment-carried template names two pillars its
// FeatureRecipe does not declare.
//
// It pins the rule to reporting EVERY offending reference in one template rather
// than the first, and it is the one build where the halt's envelope is read: a
// single finding is reported as itself, and several findings from one entry are
// wrapped in a sentence naming the entry and the attachment whose FeatureRecipe
// carries it.
//
// It does not run in parallel: it replaces package-level declarations.
func TestTheOwnerRuleReportsEveryOffendingReferenceInOneTemplate(t *testing.T) {
	blocks := ownerAdmittingArtifacts()
	blocks.mon = ownerOffendingBlock(ownerTwoOffencesBody(ownerMonCapability))

	err, got := ownerRefusalProof(t, blocks)

	msg := cerrors.CauseChain(err)
	for _, want := range []string{`".source.chart"`, `".source.repository"`} {
		if !strings.Contains(msg, want) {
			t.Errorf("the halt does not name %s.\n"+
				"INVARIANT: the rule reports EVERY offending reference in one template rather than "+
				"the first.\nfull halt: %s", want, msg)
		}
	}

	// The envelope, which names the document to edit rather than the Recipe: the
	// entry is in the artifacts block of the FeatureRecipe the attachment binds.
	envelope := `artifact "` + ownerOffendingArtifact +
		`" references pillars the FeatureRecipe attachment "` + ownerMonInstance +
		`" binds does not declare`
	if !strings.Contains(msg, envelope) {
		t.Errorf("the halt does not carry %q, which is what says which document the finding is "+
			"about.\nfull halt: %s", envelope, msg)
	}

	// One invocation, several findings: the count change is in the findings and not
	// in the invocations.
	invocations := 0
	for _, r := range got {
		if r.artifact == ownerOffendingArtifact {
			invocations++
		}
	}
	if invocations != 1 {
		t.Errorf("the value was invoked %d time(s) over %q; want exactly 1 — the several findings "+
			"come from one invocation reading the whole template", invocations, ownerOffendingArtifact)
	}

	t.Logf("DIAGNOSTIC WALK — one attachment-carried entry naming two undeclared pillars: "+
		"%d invocation(s), halt = %s", invocations, msg)
}
