// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// End-to-end proof that a delivery entry naming a key the target does not
// publish is refused wherever the connection was written.
//
// The same authoring mistake halts on every surface it can be written on — a
// component's own dependency block and a Feature attachment alike — rather than
// building and delivering an empty value.
// Each case below is one authoring surface, built through the real pipeline.

package pipeline_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline/pipelinetest"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/topology"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
)

const deliveryOriginDir = "testdata/delivery-key-origin/"

// buildDeliveryOrigin compiles one of this fixture's workspaces through the real
// pipeline and returns the artifacts and the error, without failing the test on
// either — the negative cases assert on the error and the positive ones on the
// output.
func buildDeliveryOrigin(t *testing.T, variant string) (map[string][]byte, error) {
	t.Helper()
	result, err := pipelinetest.BuildE(t, pipelinetest.Input{
		ReleasePath:  deliveryOriginDir + "release-" + variant + ".yaml",
		CatalogPaths: []string{deliveryOriginDir + "catalog/base"},
	})
	if err != nil {
		return nil, err
	}
	return result.Artifacts, nil
}

// refusalText is everything a halt says, findings included.
//
// A group of findings renders as a header and a count, and each finding's own
// locator and detail sit on the group's members rather than in the header's
// text. So an assertion reading the top-level string alone would compare against
// "1 violation(s)" and pass on any finding whatever — which is the shape a
// message-only assertion takes when the halt is a group.
func refusalText(err error) string {
	var group *resolver.MultiResolverError
	if !errors.As(err, &group) {
		return err.Error()
	}
	parts := []string{err.Error()}
	for _, finding := range group.Violations {
		parts = append(parts, finding.Error())
	}
	return strings.Join(parts, "\n")
}

// TestADeliveryKeyTheTargetDoesNotPublishIsRefusedWhereverItWasWritten is the
// surface-independence proof.
//
// Every row writes the SAME mistake — a delivery entry reading from `hosst`,
// which the target's component type does not publish — on a different authoring
// surface, and every row must halt with the same condition. The locator differs
// per row and that difference is the point: each names the block its own author
// wrote, so the finding routes to an edit somebody can make rather than to a
// dependency block the consuming component does not have.
func TestADeliveryKeyTheTargetDoesNotPublishIsRefusedWhereverItWasWritten(t *testing.T) {
	t.Parallel()

	for _, row := range []struct {
		name    string
		variant string
		locator string
	}{
		{
			name:    "the component's own dependency block",
			variant: "component-authored",
			locator: ".spec.components.api.uses.backend.primary",
		},
		{
			name:    "an attachment declared on the component",
			variant: "attachment-contributed",
			locator: ".spec.components.api.features.shared_backend.uses.backend.primary",
		},
		{
			name:    "an attachment declared scope-wide",
			variant: "scope-contributed",
			locator: ".spec.subsystems.tier.features.shared_backend.uses.backend.primary",
		},
		{
			name:    "an attachment the Release declared against one component",
			variant: "release-attached",
			locator: ".spec.boundaries.cluster.components.api.features.shared_backend.uses.backend.primary",
		},
		{
			name:    "an attachment the Release declared for a whole boundary",
			variant: "boundary-attached",
			locator: ".spec.boundaries.cluster.features.shared_backend.uses.backend.primary",
		},
	} {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			_, err := buildDeliveryOrigin(t, row.variant)
			if err == nil {
				t.Fatalf("the build succeeded.\n" +
					"INVARIANT: a delivery entry reading from a key the target does not publish halts, " +
					"wherever the connection was written.\n" +
					"CONSEQUENCE: the mistake reaches rendered output as an empty value, and the build reports " +
					"success — the shape a defect takes when it looks exactly like a passing build.")
			}
			// The identity, not the message: a message comparison keeps compiling
			// after the condition is re-registered and silently stops matching.
			if !errors.Is(err, resolver.ErrInvalidInjectKey()) {
				t.Fatalf("the build halted on a different condition than the one this rule reports: %v", err)
			}
			refusal := refusalText(err)
			if !strings.Contains(refusal, row.locator) {
				t.Errorf("the refusal does not name %s.\n"+
					"INVARIANT: a locator names a key the author's own document contains.\n"+
					"CONSEQUENCE: a reader sent to a block their file does not hold cannot act on the finding.\n"+
					"got: %s", row.locator, refusal)
			}
			// The key the author mistyped and the alternatives, which is what makes
			// the finding actionable without opening the catalog entity.
			for _, detail := range []string{"hosst", "core/db", "host", "port"} {
				if !strings.Contains(refusal, detail) {
					t.Errorf("the refusal does not name %q; got: %s", detail, refusal)
				}
			}
		})
	}
}

// TestALiteralDeliveryOnAContributedConnectionIsNotRefused is the calibration
// this rule most needs, because the failure it guards against is the rule
// reproducing the defect it closes in a new place.
//
// A delivery entry may carry a literal instead of a reference, and the finalised
// graph stores both as one string per delivery key. A rule reading that form
// would resolve every literal against the target's published set and refuse the
// ones that happened not to name a key — refusing legal authoring on every build.
// The fixture's literal is deliberately spelled like a mistyped key, so a rule
// making that mistake refuses it here.
//
// The same connection carries a legal reference beside the literal, which is
// what stops the case passing for the wrong reason: a rule that skipped the
// connection wholesale would also be silent.
func TestALiteralDeliveryOnAContributedConnectionIsNotRefused(t *testing.T) {
	t.Parallel()

	artifacts, err := buildDeliveryOrigin(t, "legal-literal")
	if err != nil {
		t.Fatalf("the build halted on legal authoring.\n"+
			"INVARIANT: a literal is nothing to resolve, so no rule over published-key references may read one.\n"+
			"CONSEQUENCE: refusing a literal is the defect this rule exists to close, reproduced on the other side.\n"+
			"got: %v", err)
	}
	if len(artifacts) == 0 {
		t.Fatal("the build produced no artifact, so the case proves nothing about what it rendered")
	}
	// The reference beside the literal reached the output, which is what says the
	// connection was examined rather than skipped.
	var carriesReference bool
	for _, body := range artifacts {
		if strings.Contains(string(body), "DB_PORT=5432") {
			carriesReference = true
		}
	}
	if !carriesReference {
		t.Error("the legal reference on the same connection did not reach the output, so the silence above " +
			"could be a connection nothing looked at rather than a literal correctly left alone")
	}
}

// TestEachOriginOnOneSocketAndAliasIsRefusedSeparately is the ambiguity proof
// at the diagnostic level.
//
// The workspace writes one socket and one alias three times on one node: legally
// in the component's own dependency block, and with a DIFFERENT mistake in each
// of two attachments. A rule keyed by node, socket and alias reads one of the
// three for all three, so it would report at most one finding and would report
// it against whichever block it happened to read.
//
// Two findings, each naming its own attachment and its own mistyped key, is
// therefore the assertion — and the component's own legal delivery drawing no
// finding is the third of the three separations rather than an absence.
func TestEachOriginOnOneSocketAndAliasIsRefusedSeparately(t *testing.T) {
	t.Parallel()

	_, err := buildDeliveryOrigin(t, "three-way-typo")
	if err == nil {
		t.Fatal("the build succeeded over two mistyped deliveries")
	}
	if !errors.Is(err, resolver.ErrInvalidInjectKey()) {
		t.Fatalf("the build halted on a different condition: %v", err)
	}
	refusal := refusalText(err)
	for _, want := range []string{
		".spec.components.api.features.sb_one.uses.backend.primary",
		".spec.components.api.features.sb_two.uses.backend.primary",
		"one_typo",
		"two_typo",
	} {
		if !strings.Contains(refusal, want) {
			t.Errorf("the refusal does not name %q.\n"+
				"INVARIANT: each surface that drew a connection is judged separately.\n"+
				"CONSEQUENCE: a coordinate that cannot separate them reports one of the two and approves "+
				"the other, which reads exactly like a rule that examined both.\n"+
				"got: %s", want, refusal)
		}
	}
	// The component's own delivery is legal and must not appear. A finding
	// against it would mean the rule read one origin's block for another.
	if strings.Contains(refusal, "OWN_HOST") {
		t.Errorf("the refusal names the component's own delivery, which is legal; got: %s", refusal)
	}
	// The same pass refuses the three connections sharing one alias, once per
	// attachment-drawn connection and each at its own block. That finding stands
	// beside the delivery findings above rather than in place of them: every rule
	// over the finalised graph reports in one build.
	aliasFindings := pipelinetest.Findings(err, topology.ErrDuplicateConnectionAlias())
	var aliasAt []string
	for _, finding := range aliasFindings {
		aliasAt = append(aliasAt, finding.Source().Locator)
	}
	wantAliasAt := []string{
		".spec.components.api.features.sb_one.uses.backend.primary",
		".spec.components.api.features.sb_two.uses.backend.primary",
	}
	if strings.Join(aliasAt, "|") != strings.Join(wantAliasAt, "|") {
		t.Errorf("the alias refusals are positioned at %q; want %q", aliasAt, wantAliasAt)
	}
}

// TestTwoOriginsNamingDifferentTargetsAreEachJudgedAgainstTheirOwn closes the
// hole a key without the resolved target leaves.
//
// The component's own dependency block and an attachment's write one socket and
// one alias and name DIFFERENT components. Each delivers against the component it
// named, so a rule reading the target back by the connection triple would measure
// one origin's delivery against the other's published set — refusing legal
// authoring, or approving a mistake, depending on which way the mismatch fell.
//
// Two connections under one alias are refused, so the build halts — and the
// refusal is the whole of the evidence. It carries the alias finding, at the
// attachment's block and naming the component's own, and it carries NO delivery
// finding: both deliveries here are legal against their own target and neither is
// legal against the other's, and every rule over the finalised graph reports in
// the one pass, so a delivery rule reading one origin's target for the other would
// stand beside the alias finding in this same refusal.
func TestTwoOriginsNamingDifferentTargetsAreEachJudgedAgainstTheirOwn(t *testing.T) {
	t.Parallel()

	_, err := buildDeliveryOrigin(t, "split-target")
	if err == nil {
		t.Fatal("the build succeeded over two connections of one component under one alias")
	}
	findings := pipelinetest.Findings(err, topology.ErrDuplicateConnectionAlias())
	if len(findings) != 1 {
		t.Fatalf("the refusal carries %d alias finding(s); want 1, at the later connection; got: %v",
			len(findings), err)
	}
	if got, want := findings[0].Source().Locator, ".spec.components.api.features.sb_one.uses.backend.primary"; got != want {
		t.Errorf("the alias finding is positioned at %q; want %q, the later connection's own entry", got, want)
	}
	for _, want := range []string{`".spec.components.api.uses.backend.primary"`, `with target "db"`, `with target "db2"`} {
		if !strings.Contains(findings[0].Error(), want) {
			t.Errorf("the alias finding does not name %s; got: %s", want, findings[0].Error())
		}
	}
	if errors.Is(err, resolver.ErrInvalidInjectKey()) {
		t.Errorf("a legal delivery was refused, so one origin's delivery was measured against the other "+
			"origin's target; got: %v", err)
	}
}

// TestOneAttachmentNameAtTwoScopesLeavesNoDeliveryUnexamined is the residual-case
// proof.
//
// One attachment instance name declared both on a component and on the enclosing
// scope draws one connection from each block, and the composition records the two
// identically — same node, same socket, same alias, same attachment instance,
// same target. So no key derived from what the composition records can separate
// them, and the fact keyed by that origin answers with EVERY payload the key
// names rather than the first.
//
// Two connections under one alias are refused, so the build halts, and the
// refusal states the bound of that sameness rather than hiding it: one finding,
// for the second connection, positioned at the block the composition recorded —
// the component's own attachment, which the attachment cascade reads first — and
// naming that same block for the connection already holding the alias, with no
// list position, because no position separates two blocks recorded as one. The
// fixture writes a legal delivery in each block, and no delivery finding beside
// the alias finding is what says neither was refused.
func TestOneAttachmentNameAtTwoScopesLeavesNoDeliveryUnexamined(t *testing.T) {
	t.Parallel()

	_, err := buildDeliveryOrigin(t, "samename")
	if err == nil {
		t.Fatal("the build succeeded over two connections of one component under one alias")
	}
	findings := pipelinetest.Findings(err, topology.ErrDuplicateConnectionAlias())
	if len(findings) != 1 {
		t.Fatalf("the refusal carries %d alias finding(s); want 1, for the second connection; got: %v",
			len(findings), err)
	}
	const recorded = ".spec.subsystems.tier.components.api.features.shared_backend.uses.backend.primary"
	if got := findings[0].Source().Locator; got != recorded {
		t.Errorf("the alias finding is positioned at %q; want %q, the block the composition recorded", got, recorded)
	}
	if got := strings.Count(findings[0].Error(), `"`+recorded+`"`); got != 2 {
		t.Errorf("the alias finding names the recorded block %d time(s); want 2, once for each connection, "+
			"since the composition records both at it; got: %s", got, findings[0].Error())
	}
	// A list position is spelled as its caption and the opening quote of its
	// value. The bare words are not the needle: the advice every finding of the
	// condition carries explains what a list entry is, so they appear whether or
	// not a position is named.
	if strings.Contains(findings[0].Error(), `list entry "`) {
		t.Errorf("the alias finding names a list position for two blocks recorded as one; got: %s",
			findings[0].Error())
	}
	if errors.Is(err, resolver.ErrInvalidInjectKey()) {
		t.Errorf("a legal delivery was refused, so one block's delivery was left unexamined or misread; got: %v", err)
	}
}

// TestAFindingOnAnAttachmentDrawnConnectionNamesTheAttachmentsBlock proves a
// finding on a connection an attachment drew names the attachment's block.
//
// The socket-selector rule is asked once per connection the graph holds. For a
// connection an attachment drew, the consuming component has no dependency block
// for it, so a locator built from that block would name a path no file contains,
// on every one of the components the scope reaches.
//
// The fixture's attachment wires its socket to a component the socket's selector
// rejects, so the finding is a property of the compiled document rather than of
// this assertion.
func TestAFindingOnAnAttachmentDrawnConnectionNamesTheAttachmentsBlock(t *testing.T) {
	t.Parallel()

	_, err := buildDeliveryOrigin(t, "selector-miss")
	if err == nil {
		t.Fatal("the build succeeded over a connection whose target fails its socket's selector")
	}
	refusal := refusalText(err)
	const want = ".spec.subsystems.tier.features.shared_backend.uses.backend.other"
	if !strings.Contains(refusal, want) {
		t.Errorf("the refusal does not name %q; got: %s", want, refusal)
	}
	// A path through the consuming component's own dependency block names a key
	// these components do not have, so the refusal must not carry it.
	for _, stale := range []string{
		".spec.subsystems.tier.components.api.uses.backend",
		".spec.subsystems.tier.components.web.uses.backend",
	} {
		if strings.Contains(refusal, stale) {
			t.Errorf("the refusal still names %q, which the file does not contain; got: %s", stale, refusal)
		}
	}
}
