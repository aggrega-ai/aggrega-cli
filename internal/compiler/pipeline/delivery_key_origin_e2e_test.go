// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// End-to-end proof that a delivery entry naming a key the target does not
// publish is refused wherever the connection was written.
//
// The same authoring mistake used to behave differently depending on the surface
// it was written on: written in a component's own dependency block it halted,
// and contributed by a Feature attachment it built, rendered, and delivered
// nothing — a defect whose only outward sign was an empty value in the output.
// Each case below is one authoring surface, built through the real pipeline.

package pipeline_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline/pipelinetest"
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
// Both deliveries here are legal against their own target and neither is legal
// against the other's, which is what makes the green discriminating.
func TestTwoOriginsNamingDifferentTargetsAreEachJudgedAgainstTheirOwn(t *testing.T) {
	t.Parallel()

	artifacts, err := buildDeliveryOrigin(t, "split-target")
	if err != nil {
		t.Fatalf("the build halted on legal authoring: %v", err)
	}
	// Both deliveries reached the output, each carrying the component its own
	// connection named — which is what says the two connections were kept apart
	// all the way through rather than collapsed onto one.
	var carriesBoth bool
	for _, body := range artifacts {
		text := string(body)
		if strings.Contains(text, "OWN_HOST=db;") && strings.Contains(text, "ONE_HOST=db2;") {
			carriesBoth = true
		}
	}
	if !carriesBoth {
		t.Error("the two connections did not each deliver their own target's value, so the case proves " +
			"nothing about which target each was judged against")
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
// The fixture writes a legal delivery in each block. Both reaching the output is
// what says both were carried; the build staying green is what says neither was
// refused.
func TestOneAttachmentNameAtTwoScopesLeavesNoDeliveryUnexamined(t *testing.T) {
	t.Parallel()

	artifacts, err := buildDeliveryOrigin(t, "samename")
	if err != nil {
		t.Fatalf("the build halted on legal authoring: %v", err)
	}
	var carriesBoth bool
	for _, body := range artifacts {
		text := string(body)
		if strings.Contains(text, "OWN_HOST=db;") && strings.Contains(text, "SCOPE_HOST=db;") {
			carriesBoth = true
		}
	}
	if !carriesBoth {
		t.Error("the two blocks did not both deliver, so the case is not the two-connections-one-key shape " +
			"it exists to cover")
	}
}

// TestAFindingOnAnAttachmentDrawnConnectionNamesTheAttachmentsBlock is the
// locator repair, proved on a rule this item did not otherwise touch.
//
// The socket-selector rule is asked once per connection the graph holds, and it
// built every locator from the consuming component's own dependency block. For a
// connection an attachment drew, that block does not exist: the finding named a
// path no file contains, on every one of the components the scope reaches.
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
	// The shape it used to name: the consuming component's own dependency block,
	// which these components do not have.
	for _, stale := range []string{
		".spec.subsystems.tier.components.api.uses.backend",
		".spec.subsystems.tier.components.web.uses.backend",
	} {
		if strings.Contains(refusal, stale) {
			t.Errorf("the refusal still names %q, which the file does not contain; got: %s", stale, refusal)
		}
	}
}
