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
// over: every identity in the shared segment corpus reached both ways a node can
// carry one — as the node's own id, and as the component identity an
// FR-emitted node carries beside a different node id. Both are needed, because a
// derivation reading only the node id agrees with the file prefix for every
// context of the first shape and diverges on every context of the second.
func releaseNameContexts() []rendercore.NodeRenderContext {
	corpus := namesegmenttest.Corpus()
	out := make([]rendercore.NodeRenderContext, 0, 2*len(corpus))
	for _, s := range corpus {
		out = append(out, rendercore.NodeRenderContext{NodeID: s})
		out = append(out, rendercore.NodeRenderContext{NodeID: "carrier", LabelComponentID: s})
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
	corpus := namesegmenttest.Corpus()
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
// prefix a node's rendered output files carry are two writings of one identity:
// two contexts reach one release name exactly when they reach one prefix.
//
// The biconditional is what makes this more than an observation that the two
// agree today. A release name derived from the node id alone agrees with the
// prefix for every ordinary node and diverges for every node carrying a
// separate component identity, which the corpus supplies; a derivation that
// substitutes for punctuation instead of encoding it gives one name to two
// prefixes. Either failure breaks one direction of the equivalence, and neither
// is visible from a test that only checks the two match for some identity
// somebody thought of.
func TestReleaseNameFollowsTheFilePrefixSource(t *testing.T) {
	t.Parallel()
	contexts := releaseNameContexts()
	for i := range contexts {
		for j := range contexts {
			a, b := contexts[i], contexts[j]
			samePrefix := rendercore.ProvenanceComponentID(a) == rendercore.ProvenanceComponentID(b)
			sameRelease := releaseName(a) == releaseName(b)
			if samePrefix != sameRelease {
				t.Fatalf("node ids %q/%q with component identities %q/%q: file prefixes %q/%q, release names %q/%q — one identity must decide both",
					a.NodeID, b.NodeID, a.LabelComponentID, b.LabelComponentID,
					rendercore.ProvenanceComponentID(a), rendercore.ProvenanceComponentID(b),
					releaseName(a), releaseName(b))
			}
		}
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
			t.Errorf("release name %q for component identity %q carries %q at %d, which Helm does not admit",
				name, rendercore.ProvenanceComponentID(nodeCtx), string(c), i)
		}
	}
}

// TestReleaseNameIsRecoverable reads a release name back into the component
// identity it was built from and asserts it comes back exactly as supplied.
// Distinctness says no identity in the corpus collided; recovering the identity
// says why — the name still carries it. The reader shares no code with the
// encoder, because a reader that called into the encoder would agree with it
// whatever the encoder did.
func TestReleaseNameIsRecoverable(t *testing.T) {
	t.Parallel()
	for _, nodeCtx := range releaseNameContexts() {
		want := rendercore.ProvenanceComponentID(nodeCtx)
		got := namesegmenttest.DecodeSegment(t, releaseName(nodeCtx))
		if got != want {
			t.Errorf("release name %q read back as %q, want %q", releaseName(nodeCtx), got, want)
		}
	}
}

// TestReleaseNameForFeatureEmittedNode pins the release name of a node whose
// component identity is carried separately from its node id. A boundary-attached
// feature's node id is a dispatch marker rather than an identity anyone
// authored, and it carries characters no release name admits; the component
// identity beside it is the value the node's output files are named for, so it
// is the value the release name is built from too.
//
// The identity is built the way the compiler builds it — composed from the
// instance name and the boundary path — rather than restated as a literal, so
// this case follows the compiler's own derivation instead of a copy of it that
// could stop matching. The release name it reaches is written out, because that
// is the value under test.
func TestReleaseNameForFeatureEmittedNode(t *testing.T) {
	t.Parallel()
	nodeCtx := rendercore.NodeRenderContext{
		NodeID:           "<boundary-fr>:bridge@cluster",
		LabelComponentID: labels.BoundaryFeatureComponentID("bridge", []string{"cluster"}),
	}
	if got, want := releaseName(nodeCtx), "bfr-zh-cluster-zh-bridge"; got != want {
		t.Errorf("release name %q, want %q", got, want)
	}
}
