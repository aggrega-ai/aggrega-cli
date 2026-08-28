// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"errors"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
	"github.com/aggrega-ai/aggrega-cli/pkg/digest"
)

// The artifact-content-pin rule reads two declared facts, and this file runs each
// of the three declared values through the framework's own traversal over a
// fixture.
//
// # Why the facts are exercised rather than only declared
//
// A declaration is checkable against its own shape and nothing else: the generator
// confirms the stage exists, the subject kind is keyed and the result is an
// admitted shape, none of which says the accessor was read for the right instance
// or that the predicate branches the right way on it. Both facts here are keyed by
// the same coordinates, which is exactly where that gap bites — a predicate reading
// the text for one entry and the pin for another would satisfy every declaration
// assertion and be silently wrong, and the fixture below carries two entries whose
// answers are crossed so that such a read produces a different verdict.
//
// # The stub embeds the view interface, and the nil embedding is the point
//
// Each stub embeds the stage's view and overrides only the accessors its rule
// reads. The embedded interface is nil, so any other accessor panics rather than
// answering a zero value — which turns "the predicate reads these facts and nothing
// else" from a claim about the source into a property this file enforces.

const (
	// pinBodyIntact is content whose digest the fixture records as the pin, so the
	// rule must stay silent over it.
	pinBodyIntact = "kind: NetworkPolicy\nmetadata:\n  name: deny-all\n"

	// pinBodyDrifted is content the fixture records a DIFFERENT pin for, which is
	// the state a file edited after resolution leaves.
	pinBodyDrifted = "kind: NetworkPolicy\nmetadata:\n  name: allow-all\n"
)

// artifactContentPinFactStub answers the two facts the node-rooted value reads.
type artifactContentPinFactStub struct {
	constraint.HydratedView

	text map[string]string
	// pin is keyed the same way as text, and absence is the answer that says no
	// comparison is owed — which is why the map holds the pair rather than a bare
	// string.
	pin map[string]string
}

func (s artifactContentPinFactStub) ArtifactTemplateText(node, artifact string) string {
	return s.text[factKey(node, artifact)]
}

func (s artifactContentPinFactStub) ArtifactRecordedContentPin(node, artifact string) (string, bool) {
	got, ok := s.pin[factKey(node, artifact)]
	return got, ok
}

// artifactContentPinSites implements the two enumerators the node-rooted artifact
// traversal needs. The slices are indexed rather than ranged, so the order a
// traversal observes comes from the fixture and never from a map walk.
type artifactContentPinSites struct {
	nodes     []constraint.NodeInstance
	artifacts map[string][]constraint.AuthoredCoordinate
}

func (f artifactContentPinSites) Nodes() []constraint.NodeInstance { return f.nodes }

func (f artifactContentPinSites) Artifacts(node string) []constraint.AuthoredCoordinate {
	return f.artifacts[factKey(node)]
}

// TestArtifactContentPinRuleOverANodesOwnRecipe drives the node-rooted value over
// the three states an entry can be in, and over the crossed pair that makes the
// keying load-bearing.
//
// The rows are the whole branch set of the predicate: an entry with no recorded pin
// is silent whatever its content, an entry whose content digests to its pin is
// silent, and an entry whose content does not is raised. The silent rows are the
// positive controls for the raised one — without them a predicate that raised
// unconditionally would satisfy the raising assertion on its own.
func TestArtifactContentPinRuleOverANodesOwnRecipe(t *testing.T) {
	t.Parallel()

	const node = "api"
	intactPin := digest.ContentDigest([]byte(pinBodyIntact))

	for _, row := range []struct {
		name      string
		text      string
		pin       string
		pinned    bool
		wantRaise bool
	}{
		{
			name: "no recorded pin, so no comparison is owed",
			text: pinBodyDrifted,
		},
		{
			name:   "content digests to the recorded pin",
			text:   pinBodyIntact,
			pin:    intactPin,
			pinned: true,
		},
		{
			name:      "content no longer digests to the recorded pin",
			text:      pinBodyDrifted,
			pin:       intactPin,
			pinned:    true,
			wantRaise: true,
		},
		{
			name:      "an empty text against a recorded pin is a disagreement rather than a skip",
			text:      "",
			pin:       intactPin,
			pinned:    true,
			wantRaise: true,
		},
	} {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()

			stub := artifactContentPinFactStub{
				text: map[string]string{factKey(node, "policy"): row.text},
				pin:  map[string]string{},
			}
			if row.pinned {
				stub.pin[factKey(node, "policy")] = row.pin
			}
			// Held at the interface type, which is what the traversal's own type
			// parameter is instantiated at: handed the concrete stub, inference would
			// pick that type and the declared value would no longer match.
			var facts constraint.HydratedView = stub
			sites := artifactContentPinSites{
				nodes: []constraint.NodeInstance{{ID: node}},
				artifacts: map[string][]constraint.AuthoredCoordinate{
					factKey(node): {{Value: "policy", SourceFile: "recipe.yaml"}},
				},
			}

			raised, err := constraint.RunOnNodeArtifacts(
				nil, sites, sites, facts, ArtifactContentMatchesItsRecordedPin,
			)
			if err != nil {
				t.Fatalf("traversal: %v", err)
			}
			if got := len(raised) > 0; got != row.wantRaise {
				t.Fatalf("raised = %v, want %v (objections: %d)", got, row.wantRaise, len(raised))
			}
			if !row.wantRaise {
				return
			}
			if !errors.Is(raised[0].Sentinel(), ErrArtifactContentMismatch()) {
				t.Errorf("the objection carries %v, want the content-mismatch condition", raised[0].Sentinel())
			}
			msg := constraint.ObjectionMessage(raised[0])
			for _, want := range []string{
				ErrArtifactContentMismatch().Error(),
				`"policy"`,
				`the recorded pin is "` + intactPin + `"`,
				`its content digests to "` + digest.ContentDigest([]byte(row.text)) + `"`,
			} {
				if !strings.Contains(msg, want) {
					t.Errorf("the message does not carry %q.\nfull message: %s", want, msg)
				}
			}
		})
	}
}

// TestArtifactContentPinRuleReadsBothFactsAtTheSameEntry is the discriminator a
// per-row proof cannot supply: two entries on one node, each intact against its
// OWN pin and drifted against the other's.
//
// A predicate reading the text at one coordinate and the pin at another raises over
// both entries here, while the correct reading raises over neither. A fixture with
// one entry cannot tell the two apart, because there is only one pin to read.
func TestArtifactContentPinRuleReadsBothFactsAtTheSameEntry(t *testing.T) {
	t.Parallel()

	const node = "api"
	var facts constraint.HydratedView = artifactContentPinFactStub{
		text: map[string]string{
			factKey(node, "first"):  pinBodyIntact,
			factKey(node, "second"): pinBodyDrifted,
		},
		pin: map[string]string{
			factKey(node, "first"):  digest.ContentDigest([]byte(pinBodyIntact)),
			factKey(node, "second"): digest.ContentDigest([]byte(pinBodyDrifted)),
		},
	}
	sites := artifactContentPinSites{
		nodes: []constraint.NodeInstance{{ID: node}},
		artifacts: map[string][]constraint.AuthoredCoordinate{
			factKey(node): {
				{Value: "first", SourceFile: "recipe.yaml"},
				{Value: "second", SourceFile: "recipe.yaml"},
			},
		},
	}

	raised, err := constraint.RunOnNodeArtifacts(
		nil, sites, sites, facts, ArtifactContentMatchesItsRecordedPin,
	)
	if err != nil {
		t.Fatalf("traversal: %v", err)
	}
	if len(raised) != 0 {
		t.Fatalf("the rule raised %d objection(s) over two entries each intact against its own pin.\n"+
			"INVARIANT: both facts are read at the SAME entry. A predicate crossing the two "+
			"coordinates measures one entry's content against another's recorded pin, which raises "+
			"here and would raise on every Recipe declaring more than one imported artifact.",
			len(raised))
	}
}

// artifactContentPinCarrierStub answers the two facts the carrier-rooted values
// read, for both carriers, so one fixture drives both.
//
// It answers the NODE-rooted accessors too, and deliberately answers them with a
// pin that would disagree: a carrier-rooted value reading the node's accessors
// instead of its own raises here, which is what makes the carrier keying provable
// rather than asserted.
type artifactContentPinCarrierStub struct {
	constraint.HydratedView

	attachmentText map[string]string
	attachmentPin  map[string]string
	boundaryText   map[string]string
	boundaryPin    map[string]string
}

func (s artifactContentPinCarrierStub) ArtifactTemplateText(_, _ string) string {
	return pinBodyDrifted
}

func (s artifactContentPinCarrierStub) ArtifactRecordedContentPin(_, _ string) (string, bool) {
	return digest.ContentDigest([]byte(pinBodyIntact)), true
}

func (s artifactContentPinCarrierStub) AttachmentArtifactTemplateText(
	node, attachment, artifact string,
) string {
	return s.attachmentText[factKey(node, attachment, artifact)]
}

func (s artifactContentPinCarrierStub) AttachmentArtifactRecordedContentPin(
	node, attachment, artifact string,
) (string, bool) {
	got, ok := s.attachmentPin[factKey(node, attachment, artifact)]
	return got, ok
}

func (s artifactContentPinCarrierStub) BoundaryAttachmentArtifactTemplateText(
	boundary, attachment, artifact string,
) string {
	return s.boundaryText[factKey(boundary, attachment, artifact)]
}

func (s artifactContentPinCarrierStub) BoundaryAttachmentArtifactRecordedContentPin(
	boundary, attachment, artifact string,
) (string, bool) {
	got, ok := s.boundaryPin[factKey(boundary, attachment, artifact)]
	return got, ok
}

// artifactContentPinCarrierSites implements the enumerators both carrier-rooted
// traversals need, narrowed to one carrier and one entry.
type artifactContentPinCarrierSites struct {
	node       string
	boundary   string
	attachment string
	artifact   string
	sourceFile string
}

func (f artifactContentPinCarrierSites) Nodes() []constraint.NodeInstance {
	return []constraint.NodeInstance{{ID: f.node}}
}

func (f artifactContentPinCarrierSites) Attachments(node string) []string {
	if node != f.node {
		return nil
	}
	return []string{f.attachment}
}

func (f artifactContentPinCarrierSites) AttachmentArtifacts(
	node, attachment string,
) []constraint.AuthoredCoordinate {
	if node != f.node || attachment != f.attachment {
		return nil
	}
	return []constraint.AuthoredCoordinate{{Value: f.artifact, SourceFile: f.sourceFile}}
}

func (f artifactContentPinCarrierSites) Boundaries() []constraint.AuthoredCoordinate {
	return []constraint.AuthoredCoordinate{{Value: f.boundary}}
}

func (f artifactContentPinCarrierSites) BoundaryAttachments(boundary string) []string {
	if boundary != f.boundary {
		return nil
	}
	return []string{f.attachment}
}

func (f artifactContentPinCarrierSites) BoundaryAttachmentArtifacts(
	boundary, attachment string,
) []constraint.AuthoredCoordinate {
	if boundary != f.boundary || attachment != f.attachment {
		return nil
	}
	return []constraint.AuthoredCoordinate{{Value: f.artifact, SourceFile: f.sourceFile}}
}

// TestArtifactContentPinRuleOverEachAttachmentCarrier drives the two
// carrier-rooted values and holds the keying that makes them separate rules.
//
// The stub answers the NODE-rooted accessors with a pair that disagrees, so a value
// reading those instead of its own raises where the fixture says it must be silent
// — and its own facts are intact, so the silence is attributable to reading the
// right coordinates rather than to a rule that reads nothing.
func TestArtifactContentPinRuleOverEachAttachmentCarrier(t *testing.T) {
	t.Parallel()

	const (
		node       = "api"
		boundary   = "production"
		attachment = "monitoring"
		artifact   = "policy"
	)
	intactPin := digest.ContentDigest([]byte(pinBodyIntact))
	driftedPin := digest.ContentDigest([]byte(pinBodyDrifted))

	sites := artifactContentPinCarrierSites{
		node:       node,
		boundary:   boundary,
		attachment: attachment,
		artifact:   artifact,
		sourceFile: "featurerecipe.yaml",
	}

	t.Run("node-carried attachment: intact is silent, drifted raises", func(t *testing.T) {
		t.Parallel()
		key := factKey(node, attachment, artifact)

		var intact constraint.HydratedView = artifactContentPinCarrierStub{
			attachmentText: map[string]string{key: pinBodyIntact},
			attachmentPin:  map[string]string{key: intactPin},
		}
		raised, err := constraint.RunOnNodeAttachmentArtifacts(
			nil, sites, sites, sites, intact, AttachmentArtifactContentMatchesItsRecordedPin,
		)
		if err != nil {
			t.Fatalf("traversal: %v", err)
		}
		if len(raised) != 0 {
			t.Fatalf("the attachment-rooted value raised %d objection(s) over intact content.\n"+
				"INVARIANT: its facts are keyed by the attachment as well as by the node. The stub "+
				"answers the node-rooted accessors with a disagreeing pair, so reading those "+
				"instead of its own is what produces this.", len(raised))
		}

		var drifted constraint.HydratedView = artifactContentPinCarrierStub{
			attachmentText: map[string]string{key: pinBodyDrifted},
			attachmentPin:  map[string]string{key: intactPin},
		}
		raised, err = constraint.RunOnNodeAttachmentArtifacts(
			nil, sites, sites, sites, drifted, AttachmentArtifactContentMatchesItsRecordedPin,
		)
		if err != nil {
			t.Fatalf("traversal: %v", err)
		}
		if len(raised) != 1 {
			t.Fatalf("raised %d objection(s) over drifted content, want exactly 1", len(raised))
		}
		msg := constraint.ObjectionMessage(raised[0])
		if !strings.Contains(msg, `attachment "`+attachment+`"`) {
			t.Errorf("the message does not name the attachment, which is what says which "+
				"FeatureRecipe.\nfull message: %s", msg)
		}
	})

	t.Run("boundary-carried attachment: intact is silent, drifted raises", func(t *testing.T) {
		t.Parallel()
		key := factKey(boundary, attachment, artifact)

		var intact constraint.HydratedView = artifactContentPinCarrierStub{
			boundaryText: map[string]string{key: pinBodyDrifted},
			boundaryPin:  map[string]string{key: driftedPin},
		}
		raised, err := constraint.RunOnBoundaryAttachmentArtifacts(
			nil, sites, sites, sites, intact,
			BoundaryAttachmentArtifactContentMatchesItsRecordedPin,
		)
		if err != nil {
			t.Fatalf("traversal: %v", err)
		}
		if len(raised) != 0 {
			t.Fatalf("the boundary-rooted value raised %d objection(s) over content matching its "+
				"own recorded pin", len(raised))
		}

		var drifted constraint.HydratedView = artifactContentPinCarrierStub{
			boundaryText: map[string]string{key: pinBodyDrifted},
			boundaryPin:  map[string]string{key: intactPin},
		}
		raised, err = constraint.RunOnBoundaryAttachmentArtifacts(
			nil, sites, sites, sites, drifted,
			BoundaryAttachmentArtifactContentMatchesItsRecordedPin,
		)
		if err != nil {
			t.Fatalf("traversal: %v", err)
		}
		if len(raised) != 1 {
			t.Fatalf("raised %d objection(s) over drifted content, want exactly 1", len(raised))
		}
	})
}
