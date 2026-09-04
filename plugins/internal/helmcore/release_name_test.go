// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package helmcore

import (
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/rendercore"
	"github.com/aggrega-ai/aggrega-cli/pkg/labels"
	"github.com/aggrega-ai/aggrega-cli/pkg/namesegmenttest"
)

// releaseNameContexts returns the node contexts the release-name assertions run
// over: every name in the shared segment corpus reached both ways a node can
// carry a target-name basis — as the node's own id, and as the basis a node
// carries beside a different node id, which is the shape a boundary-attached
// feature has. Both are needed, because a derivation reading only the node id
// agrees with the file prefix for every context of the first shape and diverges
// on every context of the second.
func releaseNameContexts() []rendercore.NodeRenderContext {
	corpus := namesegmenttest.Corpus('-')
	out := make([]rendercore.NodeRenderContext, 0, 2*len(corpus))
	for _, s := range corpus {
		out = append(out, rendercore.NodeRenderContext{NodeID: s})
		out = append(out, rendercore.NodeRenderContext{NodeID: "carrier", DerivedNameBasis: s})
	}
	return out
}

// TestReleaseNameIsInjective asserts no two distinct component identities in the
// shared segment corpus reach one release name. A collision is reported with
// both identities that produced it, so a regression names the pair rather than
// only a count.
func TestReleaseNameIsInjective(t *testing.T) {
	t.Parallel()
	seen := make(map[string]string)
	corpus := namesegmenttest.Corpus('-')
	for _, s := range corpus {
		got := releaseName(rendercore.NodeRenderContext{NodeID: s})
		if prior, clash := seen[got]; clash {
			t.Fatalf("%q and %q both reach the release name %q", prior, s, got)
		}
		seen[got] = s
	}
	if len(seen) != len(corpus) {
		t.Fatalf("built %d names from %d identities; each must reach its own name", len(seen), len(corpus))
	}
}

// TestReleaseNameSeparatesFoldedPunctuation pins the pairs a derivation that
// substitutes for punctuation cannot tell apart. Each pair differs only in
// characters a release name does not admit, so a substituting derivation gives
// both one name and hands two components a single in-cluster identity.
func TestReleaseNameSeparatesFoldedPunctuation(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		left, right  string
		whatItGuards string
	}{
		{"data-layer.main-db", "data.layer-main-db",
			"a dot and a hyphen exchanged across one identity must not meet"},
		{"db-host", "db_host",
			"an underscore and a hyphen must not meet"},
		{"db.host", "db_host",
			"a dot and an underscore must not meet"},
		{"api", "API",
			"a change of case must not be folded away"},
	} {
		left := releaseName(rendercore.NodeRenderContext{NodeID: tc.left})
		right := releaseName(rendercore.NodeRenderContext{NodeID: tc.right})
		if left == right {
			t.Errorf("%q and %q both reach %q; %s", tc.left, tc.right, left, tc.whatItGuards)
		}
	}
}

// TestReleaseNameFollowsTheFilePrefixSource asserts the release name and the
// prefix a node's rendered output files carry are two writings of one basis: two
// contexts reach one release name exactly when they reach one prefix.
//
// The biconditional is what makes this more than an observation that the two
// agree today. A release name derived from the node id alone agrees with the
// prefix for every ordinary node and diverges for every node carrying a separate
// basis, which the corpus supplies; a derivation that substitutes for
// punctuation instead of encoding it gives one name to two prefixes. Either
// failure breaks one direction of the equivalence, and neither is visible from a
// test that only checks the two match for some name somebody thought of.
//
// The basis both sides read is the TARGET-name basis, not the component
// identity. Those are two carries answering two questions, and this equivalence
// is a statement about the one the author's own source renders against; the
// identity's own reading is asserted where the identity is written.
func TestReleaseNameFollowsTheFilePrefixSource(t *testing.T) {
	t.Parallel()
	contexts := releaseNameContexts()
	for i := range contexts {
		for j := range contexts {
			a, b := contexts[i], contexts[j]
			samePrefix := rendercore.TargetNameBasis(a) == rendercore.TargetNameBasis(b)
			sameRelease := releaseName(a) == releaseName(b)
			if samePrefix != sameRelease {
				t.Fatalf("node ids %q/%q with target-name bases %q/%q: file prefixes %q/%q, release names %q/%q — one basis must decide both",
					a.NodeID, b.NodeID, a.DerivedNameBasis, b.DerivedNameBasis,
					rendercore.TargetNameBasis(a), rendercore.TargetNameBasis(b),
					releaseName(a), releaseName(b))
			}
		}
	}
}

// TestReleaseNameIgnoresTheComponentIdentity is the negative half of the split,
// and it is the case a fix that separated only two of the three consumers would
// fail. A node carrying a component identity that differs from its target-name
// basis — the shape a Feature attachment delivering source its author wrote has —
// must reach the release name its BASIS gives, not the one its identity would.
//
// The two expected names are written out rather than computed, so a derivation
// that started reading the identity cannot satisfy this by agreeing with itself.
func TestReleaseNameIgnoresTheComponentIdentity(t *testing.T) {
	t.Parallel()
	// The host component's name is the basis; the attachment's own composed
	// identity is what its resources are labelled with.
	attached := rendercore.NodeRenderContext{
		NodeID:           "alpha",
		LabelComponentID: "cfr--alpha--cache",
	}
	if got, want := releaseName(attached), "alpha"; got != want {
		t.Errorf("release name %q, want %q — an attachment's chart renders against the host component's name", got, want)
	}
	// The same node with the basis set: now the name moves, which is what proves
	// the case above is reading the basis rather than merely ignoring a field.
	explicit := attached
	explicit.DerivedNameBasis = "cfr--alpha--cache"
	if got, want := releaseName(explicit), "cfr-zh-alpha-zh-cache"; got != want {
		t.Errorf("release name %q, want %q — a node carrying a basis must derive from it", got, want)
	}
}

// TestReleaseNameUsesOnlyTheReleaseNameAlphabet asserts every byte a release
// name emits is a lowercase ASCII letter, a digit, or the hyphen the encoding
// reserves for its escapes — the whole of what Helm admits in a release name
// once the dot is left out. The corpus deliberately carries an uppercase
// letter, a space, a tab and a non-ASCII letter, none of which may survive.
func TestReleaseNameUsesOnlyTheReleaseNameAlphabet(t *testing.T) {
	t.Parallel()
	for _, nodeCtx := range releaseNameContexts() {
		name := releaseName(nodeCtx)
		for i := 0; i < len(name); i++ {
			c := name[i]
			if c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-' {
				continue
			}
			t.Errorf("release name %q for target-name basis %q carries %q at %d, which Helm does not admit",
				name, rendercore.TargetNameBasis(nodeCtx), string(c), i)
		}
	}
}

// TestReleaseNameIsRecoverable reads a release name back into the target-name
// basis it was built from and asserts it comes back exactly as supplied.
// Distinctness says no name in the corpus collided; recovering the basis says
// why — the name still carries it. The reader shares no code with the encoder,
// because a reader that called into the encoder would agree with it whatever the
// encoder did.
func TestReleaseNameIsRecoverable(t *testing.T) {
	t.Parallel()
	for _, nodeCtx := range releaseNameContexts() {
		want := rendercore.TargetNameBasis(nodeCtx)
		got := namesegmenttest.DecodeSegment(t, '-', releaseName(nodeCtx))
		if got != want {
			t.Errorf("release name %q read back as %q, want %q", releaseName(nodeCtx), got, want)
		}
	}
}

// TestReleaseNameForFeatureEmittedNode pins the release name of a
// boundary-attached feature, which is the one node shape that carries a
// target-name basis of its own. Its node id is a dispatch marker rather than a
// name anyone authored, and it carries characters no release name admits — so
// falling back to the node id there would produce a name no shortening repairs.
// The composed value beside it is the basis, and it is what the node's output
// files are named for too.
//
// The value is built the way the compiler builds it — composed from the instance
// name and the boundary path — rather than restated as a literal, so this case
// follows the compiler's own derivation instead of a copy of it that could stop
// matching. The release name it reaches is written out, because that is the value
// under test, and because it is the value this change must leave exactly where it
// was: the dispatch sets the basis and the identity to one composed value, so a
// boundary-attached feature's release name and output prefix do not move.
func TestReleaseNameForFeatureEmittedNode(t *testing.T) {
	t.Parallel()
	// The bound is the one this target's own answer carries for a component-id
	// label value, stated by the caller because the composition holds no bound of
	// its own: how long an identity may be belongs to the target, and the compiler
	// learns it by asking rather than by holding a number.
	composed := labels.ComposeComponentIdentity(labels.BoundaryFeatureScope, 63, "cluster", "bridge")
	nodeCtx := rendercore.NodeRenderContext{
		NodeID: "<boundary-fr>:bridge@cluster",
		// Both carries, set to one value, exactly as the boundary-scope dispatch
		// sets them.
		LabelComponentID: composed,
		DerivedNameBasis: composed,
	}
	if got, want := releaseName(nodeCtx), "bfr-zh-cluster-zh-bridge"; got != want {
		t.Errorf("release name %q, want %q", got, want)
	}
	// And the prefix the sourced render keys its files with is the same basis, so
	// the two names this node emits are one name written twice.
	if got, want := rendercore.TargetNameBasis(nodeCtx), "bfr--cluster--bridge"; got != want {
		t.Errorf("output-file prefix %q, want %q", got, want)
	}
}
