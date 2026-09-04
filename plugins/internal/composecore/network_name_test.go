// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package composecore

import (
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/namesegmenttest"
)

// TestNetworkNameKeepsCaseApart is the first of the three folds a substituting
// derivation introduces, each pinned by a fixture of its own.
//
// A derivation that lowercases for stability puts two identities differing only
// in case on one network. A compose network is shared L3 connectivity, so that
// is two sets of services reaching each other with nothing in the emitted file
// to show that two networks were meant.
func TestNetworkNameKeepsCaseApart(t *testing.T) {
	t.Parallel()
	upper := deriveNodeNetworkName("Prod", "")
	lower := deriveNodeNetworkName("prod", "")
	if upper == lower {
		t.Errorf("the namespaces %q and %q both reach the network %q; a change of case must not be folded away",
			"Prod", "prod", upper)
	}
}

// TestNetworkNameKeepsSeparatorsApart is the second fold: a derivation that
// substitutes a single admitted character for every character it does not admit
// merges every identity differing only in those characters — and merges them
// with an identity that spells the substitute out, since the substitute is
// itself admitted.
func TestNetworkNameKeepsSeparatorsApart(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		left, right  string
		whatItGuards string
	}{
		{"a/b", "a_b", "a path separator must not be folded onto an underscore an identity may itself carry"},
		{"a b", "a_b", "a space must not be folded onto that underscore either"},
		{"a/b", "a b", "two identities a fold sends to one substitute must not meet"},
	} {
		left := deriveNodeNetworkName(tc.left, "")
		right := deriveNodeNetworkName(tc.right, "")
		if left == right {
			t.Errorf("the namespaces %q and %q both reach the network %q; %s",
				tc.left, tc.right, left, tc.whatItGuards)
		}
	}
}

// TestNetworkNameKeepsAnAbsentIdentityApartFromAnAuthoredOne is the third fold,
// and it is the one an author can walk into without writing anything unusual.
//
// A node with no namespace annotation under a boundary with no path has no
// identity to build a name from. Standing a literal in for it puts that node on
// whatever network an identity spelled the same way reaches — so the fallback
// takes the encoding's absent-segment mark instead, which carries a selector
// standing for no character and so cannot be produced by any identity an author
// wrote. The rows below pin that against every spelling of the literal the
// fallback used to take.
func TestNetworkNameKeepsAnAbsentIdentityApartFromAnAuthoredOne(t *testing.T) {
	t.Parallel()
	absent := deriveNodeNetworkName("", "")
	for _, authored := range []struct {
		namespace, boundaryPath string
	}{
		{"default", ""},
		{"", "default"},
		{"-ze", ""},
		{"", "-ze"},
	} {
		if got := deriveNodeNetworkName(authored.namespace, authored.boundaryPath); got == absent {
			t.Errorf("an absent identity and the authored (namespace %q, boundary path %q) both reach the network %q; "+
				"a node with no identity must not share a network with one that has one",
				authored.namespace, authored.boundaryPath, got)
		}
	}
}

// TestNetworkNameIsInjectiveWithinEachBranch asserts no two distinct identities
// reach one network name, taken one branch at a time: the namespace branch over
// every non-empty identity in the corpus, and the boundary-path branch over the
// whole of it.
//
// One branch at a time is the honest scope, and it is stated here rather than
// left to be inferred from a passing test. Both branches write their identity
// out into the same space, so a node whose namespace is X and a node with no
// namespace under a boundary path spelled X still reach one network. That is a
// property of which identity is selected, not of how it is written out, and no
// injective encoding can close it.
func TestNetworkNameIsInjectiveWithinEachBranch(t *testing.T) {
	t.Parallel()
	corpus := namesegmenttest.Corpus('-')
	if len(corpus) == 0 {
		t.Fatal("the corpus is empty, so distinctness over it would hold vacuously")
	}
	for _, branch := range []struct {
		name    string
		derive  func(string) string
		skipsAt func(string) bool
	}{
		{
			name:    "namespace",
			derive:  func(s string) string { return deriveNodeNetworkName(s, "unread-when-the-namespace-is-set") },
			skipsAt: func(s string) bool { return s == "" },
		},
		{
			name:    "boundary path",
			derive:  func(s string) string { return deriveNodeNetworkName("", s) },
			skipsAt: func(string) bool { return false },
		},
	} {
		seen := map[string]string{}
		derived := 0
		for _, identity := range corpus {
			if branch.skipsAt(identity) {
				continue
			}
			got := branch.derive(identity)
			if prior, clash := seen[got]; clash {
				t.Errorf("the %s branch sends %q and %q both to the network %q",
					branch.name, prior, identity, got)
			}
			seen[got] = identity
			derived++
		}
		if derived == 0 {
			t.Errorf("the %s branch derived no name at all, so its distinctness held over nothing", branch.name)
		}
		if len(seen) != derived {
			t.Errorf("the %s branch built %d names from %d identities; each identity must reach its own network",
				branch.name, len(seen), derived)
		}
	}
}

// TestNetworkNameIsRecoverable reads a network name back into the identity it
// was built from and asserts it comes back exactly as supplied. Distinctness
// says no identity in the corpus collided; recovering the identity says why —
// the name still carries it.
//
// The reader shares no code with the encoder, because a reader that called into
// the encoder would agree with it whatever the encoder did.
func TestNetworkNameIsRecoverable(t *testing.T) {
	t.Parallel()
	for _, identity := range namesegmenttest.Corpus('-') {
		name := deriveNodeNetworkName("", identity)
		if got := namesegmenttest.DecodeSegment(t, '-', name); got != identity {
			t.Errorf("the network name %q read back as %q, want %q", name, got, identity)
		}
		if identity == "" {
			continue
		}
		fromNamespace := deriveNodeNetworkName(identity, "unread-when-the-namespace-is-set")
		if got := namesegmenttest.DecodeSegment(t, '-', fromNamespace); got != identity {
			t.Errorf("the network name %q read back as %q, want %q", fromNamespace, got, identity)
		}
	}
}

// TestNetworkNameCarriesInvalidBytesDistinctly asserts input that is not valid
// UTF-8 still reaches its own network name rather than being folded onto the
// replacement character, and that such a name still reads back byte-for-byte as
// it was supplied. This is where a derivation's input is least controlled, so it
// is where a lossy one is least likely to be noticed.
func TestNetworkNameCarriesInvalidBytesDistinctly(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		scenario    string
		left, right string
	}{
		{"two identities differing only in one invalid byte", "a\xffb", "a\xfeb"},
		{"an invalid byte against the replacement character it decodes to", "a\xffb", "a�b"},
		{"an invalid byte against the same identity without it", "a\xffb", "ab"},
	} {
		left := deriveNodeNetworkName(tc.left, "")
		right := deriveNodeNetworkName(tc.right, "")
		if left == right {
			t.Errorf("%s: %q and %q both reach the network %q", tc.scenario, tc.left, tc.right, left)
		}
		for _, identity := range []string{tc.left, tc.right} {
			name := deriveNodeNetworkName(identity, "")
			if got := namesegmenttest.DecodeSegment(t, '-', name); got != identity {
				t.Errorf("%s: the network name %q read back as %q, want %q", tc.scenario, name, got, identity)
			}
		}
	}
}

// TestNetworkNameUsesOnlyTheNetworkNameAlphabet asserts every byte a network
// name emits is a letter, a digit, a dot, an underscore, or the hyphen the
// encoding reserves for its escapes — the whole of what compose admits in a
// network name. The corpus deliberately carries a space, a tab, a path separator
// and a non-ASCII letter, none of which may survive.
func TestNetworkNameUsesOnlyTheNetworkNameAlphabet(t *testing.T) {
	t.Parallel()
	for _, identity := range namesegmenttest.Corpus('-') {
		for _, name := range []string{
			deriveNodeNetworkName("", identity),
			deriveNodeNetworkName(identity, "unread-when-the-namespace-is-set"),
		} {
			for i := 0; i < len(name); i++ {
				c := name[i]
				if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' ||
					c == '.' || c == '_' || c == '-' {
					continue
				}
				t.Errorf("the network name %q for the identity %q carries %q at %d, which compose does not admit",
					name, identity, string(c), i)
			}
		}
	}
}

// TestNetworkNameIsDeterministic asserts the network name is byte-stable across
// repeated calls with identical input, which is what lets a rendered compose
// file be compared byte-for-byte between builds.
func TestNetworkNameIsDeterministic(t *testing.T) {
	t.Parallel()
	for _, identity := range namesegmenttest.Corpus('-') {
		first := deriveNodeNetworkName("", identity)
		for i := 0; i < 4; i++ {
			if again := deriveNodeNetworkName("", identity); again != first {
				t.Fatalf("the network name for %q is not stable on run %d: %q != %q", identity, i, again, first)
			}
		}
	}
}

// TestNetworkNameMatchesItsDocumentedExamples pins every worked example the
// compose provider reference gives for this derivation, so a change to the
// derivation that leaves the page saying something else fails here rather than
// shipping a page that no longer describes the product.
//
// The expected strings are written out rather than computed, because an
// expectation computed from the encoder would agree with it whatever it did —
// which is exactly what a documented example must not do.
func TestNetworkNameMatchesItsDocumentedExamples(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		namespace, boundaryPath string
		want                    string
	}{
		{"prod", "", "prod"},
		{"Prod", "", "Prod"},
		{"a.b_c", "", "a.b_c"},
		{"core-net", "", "core-net"},
		{"api-zone", "", "api-zhzone"},
		{"db-", "", "db-zh"},
		{"", "dist/app/compose", "dist-zsapp-zscompose"},
		{"", "STAGE-01", "STAGE-01"},
		{"a b", "", "a-zx000020b"},
		{"é", "", "-zx0000e9"},
		{"a\xffb", "", "a-zbffb"},
		{"", "", "-ze"},
	} {
		if got := deriveNodeNetworkName(tc.namespace, tc.boundaryPath); got != tc.want {
			t.Errorf("deriveNodeNetworkName(%q, %q) = %q, want %q", tc.namespace, tc.boundaryPath, got, tc.want)
		}
	}
}

// TestBridgeSeamFollowsTheDerivationRatherThanTheRawIdentity is the behaviour
// half of this change, asserted rather than reasoned about.
//
// The same derivation names a node's network and decides whether an edge crosses
// a network seam, so writing an identity out instead of folding it does not only
// move emitted names: an edge whose two ends used to meet on one network only
// BECAUSE of the fold now crosses a seam, and the compose bridge is what carries
// it. An injective derivation can only split ends that previously compared
// equal, never merge ends that did not, so the last row is the direction that
// must stay put.
func TestBridgeSeamFollowsTheDerivationRatherThanTheRawIdentity(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		scenario       string
		srcNS, srcPath string
		tgtNS, tgtPath string
		wantBridged    bool
	}{
		{"ends differing only in case", "Prod", "", "prod", "", true},
		{"ends differing only in a separator a fold would substitute for", "a/b", "", "a_b", "", true},
		{"one end with no identity at all against one spelling the old literal", "", "", "default", "", true},
		{"ends carrying the same identity", "prod", "", "prod", "", false},
		{"ends with no identity at all", "", "", "", "", false},
	} {
		spec := bridgeSpec{
			Source: "consumer", Target: "producer", Socket: "db",
			SrcNS: tc.srcNS, SrcPath: tc.srcPath,
			TgtNS: tc.tgtNS, TgtPath: tc.tgtPath,
			UpstreamHost: "producer", TargetPort: 5432,
		}
		resolved := resolveBridge([]bridgeSpec{spec}, 10000)
		if bridged := len(resolved) == 1; bridged != tc.wantBridged {
			t.Errorf("%s: source network %q, target network %q — bridged = %v, want %v",
				tc.scenario,
				deriveNodeNetworkName(tc.srcNS, tc.srcPath),
				deriveNodeNetworkName(tc.tgtNS, tc.tgtPath),
				bridged, tc.wantBridged)
		}
	}
}
