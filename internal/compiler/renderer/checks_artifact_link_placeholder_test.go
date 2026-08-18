// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"errors"
	"slices"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/rendercore"
)

// The rule reading a projected artifact's bytes for a surviving cross-boundary
// link placeholder, exercised over the axis that presents it with subjects and
// against the hand-written scan that reports the same condition later.
//
// # Why the enumeration is asserted separately from the rule's verdict
//
// A narrowed axis yields fewer subjects; the rule is then asked about fewer
// things, agrees with whatever it is compared against, and raises nothing — which
// reads exactly like a rule that ran and approved. So a comparison between two
// readings cannot establish that the axis reaches everything, and what the
// enumeration must CONTAIN is asserted here directly, over a corpus that can tell
// the candidate axis designs apart.
//
// # Why the second wrapper exists
//
// linkPlaceholderRecordingCheck duplicates the shape of the wrapper the
// pillar-rule suite uses, for the reason that one records: a check carries its
// view as a type parameter, and these two rules read different stages, so one
// helper cannot serve both. Both copy the sentinel and the severity from the
// declaration rather than restating them, so a value whose sentinel changed is
// still the value under test.

// linkPlaceholderNodeSet enumerates the nodes a rule over projected artifacts is
// invoked across.
//
// It supplies the node axis only. The artifact axis is the production
// [ArtifactAxis] the render itself publishes, because the axis and the fact the
// predicate reads being one set is the claim under test — a hand-built artifact
// axis would be this file transcribing what it believes the render projected,
// which is exactly the agreement that proves nothing.
type linkPlaceholderNodeSet struct {
	nodes []constraint.NodeInstance
}

func (s linkPlaceholderNodeSet) Nodes() []constraint.NodeInstance { return s.nodes }

func linkPlaceholderNodesOf(ids ...string) linkPlaceholderNodeSet {
	out := linkPlaceholderNodeSet{}
	for _, id := range ids {
		out.nodes = append(out.nodes, constraint.NodeInstance{
			ID: id, SourceFile: "aggregate.yaml", AuthoredName: id,
			SubsystemPath: "default", SynthesizedSubsystem: true,
		})
	}
	return out
}

// refusedAuthoredKeyArtifactAxis is the axis design this project refused, kept as
// a live needle rather than as a comment.
//
// It enumerates the keys a Recipe's artifacts block is authored under. Only the
// inline form makes such a key the name of an output, so on any corpus where the
// two diverge this axis presents a rule with names every artifact fact holds
// nothing for, and misses every output a library bundle expanded into. A rule
// invoked over it reads empty bytes and raises nothing.
//
// It exists so the calibration below is a standing assertion instead of a
// measurement someone took once: the discriminator between the two designs is
// asserted, so a corpus that stopped discriminating fails rather than passing.
type refusedAuthoredKeyArtifactAxis struct {
	keys []string
}

func (a refusedAuthoredKeyArtifactAxis) Artifacts(string) []constraint.AuthoredCoordinate {
	out := make([]constraint.AuthoredCoordinate, 0, len(a.keys))
	for _, key := range a.keys {
		out = append(out, constraint.AuthoredCoordinate{Value: key, SourceFile: "recipes/axis-discriminating.yaml"})
	}
	return out
}

// linkPlaceholderContentSubstituted answers one artifact's projected bytes with
// chosen content and leaves every other fact to the presentation the render
// published.
//
// It exists because the input this rule is there to refuse is one whose render
// does not complete: the hand-written scan beside this file halts on the same
// marker once the file is assembled, so a build carrying one publishes no result
// and there is no presentation to read anything from. Substituting the single fact
// under test keeps every other value the real render produced, so the objections
// below follow from a real traversal over a real axis and only the offending bytes
// are chosen.
type linkPlaceholderContentSubstituted struct {
	constraint.RenderedView
	content []byte
}

func (v linkPlaceholderContentSubstituted) ArtifactProjectionContent(_ string, _ string) []byte {
	return v.content
}

// linkPlaceholderRecordingCheck wraps the declared value so the traversal's
// invocation count and the coordinates each objection was raised over are read
// from what the framework actually presented rather than reconstructed.
func linkPlaceholderRecordingCheck(
	declared constraint.Check[constraint.RenderedView, constraint.NodeArtifactSubject],
	invokedOver *[]string,
	raisedOver *[]string,
) constraint.Check[constraint.RenderedView, constraint.NodeArtifactSubject] {
	return constraint.Check[constraint.RenderedView, constraint.NodeArtifactSubject]{
		Sentinel: declared.Sentinel,
		Severity: declared.Severity,
		Run: func(v constraint.RenderedView, s constraint.NodeArtifactSubject) []constraint.Objection {
			*invokedOver = append(*invokedOver, s.ArtifactName())
			out := declared.Run(v, s)
			for range out {
				*raisedOver = append(*raisedOver, s.ArtifactName())
			}
			return out
		},
	}
}

// TestTheArtifactLinkPlaceholderRuleIsInvokedOncePerProjectedOutput is the
// enumeration assertion for this rule's subject set.
//
// It states what the enumeration must CONTAIN: one invocation per output name the
// render projected, in walk order, and no invocation for an authored key that
// names no output. The corpus is the one whose three authored keys resolve to four
// output names, two of the keys naming no output at all — so the ruled design and
// the refused one yield different lists here, where on a corpus whose keys all
// happen to equal their output names they would yield the same one and every
// assertion would pass under both.
func TestTheArtifactLinkPlaceholderRuleIsInvokedOncePerProjectedOutput(t *testing.T) {
	t.Parallel()

	const node = "discriminating"
	res := discriminatingArtifactCorpus(t, node)
	res.RenderedView = presentationOverTheRender(t, res)

	// The corpus has to separate the two designs, so its own divergence is
	// asserted before anything is concluded from it.
	if len(axisOutputNames) == len(axisAuthoredKeys) {
		t.Fatal("the corpus's authored keys and output names are the same length; it cannot separate the two designs")
	}

	axis := res.RenderedArtifactAxis(nil)
	if axis == nil {
		t.Fatal("the axis refused to build over a completed render carrying a presentation")
	}
	nodes := linkPlaceholderNodesOf(node)

	var invokedOver, raisedOver []string
	if _, err := constraint.RunOnNodeArtifacts(nil, nodes, axis, res.RenderedView,
		linkPlaceholderRecordingCheck(
			ArtifactCrossBoundaryLinkPlaceholderResolved, &invokedOver, &raisedOver),
	); err != nil {
		t.Fatalf("the traversal returned %v with no cancellation signal supplied; a stopped walk "+
			"hands back a short subject set, which is indistinguishable from a narrow axis", err)
	}

	if !slices.Equal(invokedOver, axisOutputNames) {
		t.Errorf("the rule was invoked over %v; want the output names in walk order %v.\n"+
			"INVARIANT: the rule is invoked once per artifact the render projected, keyed by the "+
			"output name the entry resolved to.\n"+
			"CONSEQUENCE: an axis enumerating authored keys asks the content fact about names it "+
			"holds nothing for, and a rule reading no bytes raises nothing — which reads exactly "+
			"like a rule that ran and approved.", invokedOver, axisOutputNames)
	}

	// Stated in the other direction as well, because a superset would satisfy the
	// comparison above if the order happened to match.
	for _, key := range axisKeysNamingNoOutput {
		if slices.Contains(invokedOver, key) {
			t.Errorf("the rule was invoked over the authored key %q, which names no artifact this Recipe projects", key)
		}
	}

	// Every name the rule was invoked over must be answerable by the fact its
	// predicate reads. This is the property the refused design breaks: the axis and
	// the content fact have to be one set, not two that merely overlap.
	for _, name := range invokedOver {
		if len(res.RenderedView.ArtifactProjectionContent(node, name)) == 0 {
			t.Errorf("the content fact answers nothing for %q/%q, a name the rule was invoked over; "+
				"the axis and the fact the predicate reads are not one set", node, name)
		}
	}

	// The corpus carries no unresolved marker, so the rule must be silent over it.
	if len(raisedOver) != 0 {
		t.Errorf("the rule raised over %v on a corpus the render completed for; want silence", raisedOver)
	}
}

// TestTheRefusedArtifactAxisWouldNarrowThisRule calibrates the assertion above by
// running the refused design through the same traversal and stating how its
// subject set differs.
//
// Without this, the assertion above is a claim nobody measured: a guard that
// never fired is indistinguishable from a guard that cannot fire. The refused
// design is planted here as a live needle, so the discriminator is re-established
// on every run rather than at the moment it was first observed.
func TestTheRefusedArtifactAxisWouldNarrowThisRule(t *testing.T) {
	t.Parallel()

	const node = "discriminating"
	res := discriminatingArtifactCorpus(t, node)
	res.RenderedView = presentationOverTheRender(t, res)

	refused := refusedAuthoredKeyArtifactAxis{keys: axisAuthoredKeys}
	nodes := linkPlaceholderNodesOf(node)

	var invokedOver, raisedOver []string
	if _, err := constraint.RunOnNodeArtifacts(nil, nodes, refused, res.RenderedView,
		linkPlaceholderRecordingCheck(
			ArtifactCrossBoundaryLinkPlaceholderResolved, &invokedOver, &raisedOver),
	); err != nil {
		t.Fatalf("the traversal over the refused axis returned %v", err)
	}

	if slices.Equal(invokedOver, axisOutputNames) {
		t.Fatalf("the refused axis and the ruled one present the same subject set %v; the corpus "+
			"no longer discriminates, so the enumeration assertion beside this one proves nothing",
			invokedOver)
	}

	// The refused design's cost is stated concretely rather than as a count: the
	// outputs it never reaches, and the names it substitutes for them.
	for _, missed := range []string{"renamed-policy.yaml", "bundle-one.yaml", "bundle-two.yaml"} {
		if slices.Contains(invokedOver, missed) {
			t.Errorf("the refused axis reached %q; it is expected to miss every output whose name "+
				"is not its own authored key, and this test's premise is that it does", missed)
		}
	}
	for _, key := range axisKeysNamingNoOutput {
		if !slices.Contains(invokedOver, key) {
			t.Errorf("the refused axis did not present the authored key %q; the needle is not the "+
				"design it is meant to stand for", key)
		}
	}

	// And the reason a cross-check cannot detect the narrowing: the narrowed rule
	// is silent, which is what a satisfied rule looks like.
	if len(raisedOver) != 0 {
		t.Errorf("the rule raised %v over the refused axis; the needle's point is that a narrowed "+
			"axis raises NOTHING, which is why an agreement between two readings cannot detect it",
			raisedOver)
	}
	if len(res.RenderedView.ArtifactProjectionContent(node, "policy")) != 0 {
		t.Error("the content fact answers bytes for the authored key \"policy\"; the refused design's " +
			"failure mode depends on it answering nothing there")
	}
}

// TestTheArtifactLinkPlaceholderRuleAndTheAssembledScanReportOneCondition is the
// two-reading comparison: the declared rule reading a projected artifact's bytes,
// and the hand-written scan reading an assembled file's, over the same bytes.
//
// The two are not one reading behind one interface — they are invoked over
// different subject sets at different points, which is the whole reason the
// declared one is worth having. What must hold between them is that they report
// the SAME condition: a build carrying no marker is admitted by both, a byte
// sequence carrying one is refused by both, and the condition each names is the
// same matchable one, so an author who removes one placeholder is answering one
// finding rather than two unrelated ones.
func TestTheArtifactLinkPlaceholderRuleAndTheAssembledScanReportOneCondition(t *testing.T) {
	t.Parallel()

	const node = "discriminating"
	res := discriminatingArtifactCorpus(t, node)
	res.RenderedView = presentationOverTheRender(t, res)
	axis := res.RenderedArtifactAxis(nil)
	if axis == nil {
		t.Fatal("the axis refused to build over a completed render carrying a presentation")
	}
	nodes := linkPlaceholderNodesOf(node)

	clean := res.RenderedView.ArtifactProjectionContent(node, axisOutputNames[0])
	if len(clean) == 0 {
		t.Fatalf("the render projected no bytes for %q; the admitted direction below would be vacuous", axisOutputNames[0])
	}
	offending := []byte("wired: " + rendercore.CrossBoundaryLinkSentinel("api", "orders-db", "database", "host") + "\n")

	t.Run("both readings admit bytes carrying no marker", func(t *testing.T) {
		objections, err := constraint.RunOnNodeArtifacts(nil, nodes, axis, res.RenderedView,
			ArtifactCrossBoundaryLinkPlaceholderResolved)
		if err != nil {
			t.Fatalf("traversal: %v", err)
		}
		if len(objections) != 0 {
			t.Errorf("the declared rule raised %d objection(s) over a render that completed: %v", len(objections), objections)
		}
		if err := scanOrphanCrossBoundaryLinkSentinels(assembledGroupHolding(clean)); err != nil {
			t.Errorf("the hand-written scan refused the same bytes the declared rule admitted: %v", err)
		}
	})

	t.Run("both readings refuse bytes carrying one", func(t *testing.T) {
		var substituted constraint.RenderedView = linkPlaceholderContentSubstituted{
			RenderedView: res.RenderedView, content: offending,
		}

		var invokedOver, raisedOver []string
		objections, err := constraint.RunOnNodeArtifacts(nil, nodes, axis, substituted,
			linkPlaceholderRecordingCheck(
				ArtifactCrossBoundaryLinkPlaceholderResolved, &invokedOver, &raisedOver),
		)
		if err != nil {
			t.Fatalf("traversal: %v", err)
		}
		// Every artifact the axis presents carries the substituted bytes, so every
		// one of them objects. Asserting against the invocation count rather than a
		// literal is what keeps this from passing on a narrowed axis.
		if len(objections) != len(invokedOver) {
			t.Fatalf("the declared rule raised %d objection(s) over %d artifact(s) all carrying an "+
				"unresolved marker; want one per artifact", len(objections), len(invokedOver))
		}
		if len(invokedOver) == 0 {
			t.Fatal("the traversal presented no artifact; a finding from it would certify nothing")
		}

		scanErr := scanOrphanCrossBoundaryLinkSentinels(assembledGroupHolding(offending))
		if scanErr == nil {
			t.Fatal("the hand-written scan admitted bytes the declared rule refused; the two readings " +
				"do not report one condition")
		}

		// The condition is the same matchable one from both readings, which is what
		// makes this rule an earlier report of an existing finding rather than a new
		// thing for an author to learn.
		want := ErrOrphanCrossBoundaryLinkSentinel()
		if !errors.Is(scanErr, want) {
			t.Errorf("the hand-written scan's condition = %v, want the unresolved cross-boundary marker condition", scanErr)
		}
		for _, o := range objections {
			if !errors.Is(o.Sentinel(), want) {
				t.Errorf("the declared rule's condition = %v, want the same one the hand-written scan names", o.Sentinel())
			}
			// The detail carries the two coordinates the locator does not reach on
			// its own: which node's build position, and which artifact inside the
			// block. A finding naming neither leaves an author with a Recipe and no
			// entry.
			detail := o.Detail()
			if len(detail) != 2 {
				t.Errorf("objection detail = %v, want the node and the artifact name", detail)
				continue
			}
			if detail[0] != node {
				t.Errorf("objection detail names node %q, want %q", detail[0], node)
			}
			if !slices.Contains(axisOutputNames, detail[1]) {
				t.Errorf("objection detail names artifact %q, which is not an output this Recipe projects", detail[1])
			}
		}
	})
}

// assembledGroupHolding is the hand-written scan's own input shape, holding one
// file with the given bytes.
//
// The scan takes the render's retained assembled output rather than a fact, so
// driving it means handing it that shape. One file is enough: the scan halts on
// the first offender, so a second would be unreachable and would state nothing
// this comparison needs.
func assembledGroupHolding(content []byte) map[dispatchKey]*assembledGroup {
	key := dispatchKey{Path: "prod", Provider: "helm-chart"}
	return map[dispatchKey]*assembledGroup{
		key: {
			provider: key.Provider,
			files:    map[string]AssembledFile{"artifacts/one.yaml": {Content: content}},
		},
	}
}
