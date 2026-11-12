// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package composecore

import (
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/namesegmenttest"
)

// volumeNamePairs returns every (node identity, volume name) pair the volume-name
// assertions run over: the shared segment corpus crossed with itself, so both
// positions are exercised over the same edges and the pairs that move a character
// across the join are present.
//
// The cross product is what makes an injectivity assertion able to see the join
// at all. Two parts joined by a single hyphen collide exactly when a hyphen moves
// from the end of the first part to the start of the second, so a corpus that
// only varies one position at a time can be entirely distinct and still never
// contain the colliding pair.
func volumeNamePairs() [][2]string {
	corpus := namesegmenttest.Corpus('-')
	out := make([][2]string, 0, len(corpus)*len(corpus))
	for _, nodeID := range corpus {
		for _, name := range corpus {
			out = append(out, [2]string{nodeID, name})
		}
	}
	return out
}

// TestVolumeNameSeparatesPairsALossyDerivationFolds pins the pairs a derivation
// that joins with a single hyphen, or that substitutes for a character compose
// does not admit, cannot tell apart.
//
// The first three rows move a hyphen across the join, so both sides concatenate
// to one string under any one-character separator either part may itself carry.
// The last two pin folds a substituting derivation would introduce: the compose
// network name in this package lowercases and replaces, so a volume derivation
// growing the same habit is the regression these rows refuse.
//
// A compose volume is shared storage and the top-level volumes: block merges
// name collisions silently, so two authored volumes arriving at one name become
// one volume both nodes write into with nothing in the build to report it.
func TestVolumeNameSeparatesPairsALossyDerivationFolds(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		leftNode, leftName   string
		rightNode, rightName string
		whatItGuards         string
	}{
		{"db", "data-cache", "db-data", "cache",
			"a hyphen moved across the join must not fold two volumes onto one"},
		{"api", "-state", "api-", "state",
			"the empty-part form of that exchange must not fold either"},
		{"a", "b-c", "a-b", "c",
			"the shortest form of the same exchange must not fold either"},
		{"store", "data", "STORE", "data",
			"a change of case must not be folded away"},
		{"store", "log.rotate", "store", "log_rotate",
			"a dot and an underscore in the volume name must not meet"},
	} {
		left := volumeName(tc.leftNode, tc.leftName)
		right := volumeName(tc.rightNode, tc.rightName)
		if left == right {
			t.Errorf("(%q,%q) and (%q,%q) both reach the volume name %q; %s",
				tc.leftNode, tc.leftName, tc.rightNode, tc.rightName, left, tc.whatItGuards)
		}
	}
}

// TestVolumeNameIsInjective asserts no two distinct (node identity, volume name)
// pairs in the corpus reach one volume name. A collision is reported with both
// pairs that produced it, so a regression names the pair rather than only a
// count.
func TestVolumeNameIsInjective(t *testing.T) {
	t.Parallel()
	pairs := volumeNamePairs()
	seen := make(map[string][2]string, len(pairs))
	for _, pair := range pairs {
		got := volumeName(pair[0], pair[1])
		if prior, clash := seen[got]; clash {
			t.Fatalf("(%q,%q) and (%q,%q) both reach the volume name %q",
				prior[0], prior[1], pair[0], pair[1], got)
		}
		seen[got] = pair
	}
	if len(seen) != len(pairs) {
		t.Fatalf("built %d names from %d pairs; each pair must reach its own name", len(seen), len(pairs))
	}
}

// TestVolumeNameIsRecoverable reads a volume name back into the node identity
// and volume name it was built from and asserts both come back exactly as
// supplied. Distinctness says no pair in the corpus collided; recovering the
// parts says why — the name still carries them, and it carries how many there
// were, so a name divides at exactly the join it was assembled at.
//
// The reader shares no code with the encoder, because a reader that called into
// the encoder would agree with it whatever the encoder did.
func TestVolumeNameIsRecoverable(t *testing.T) {
	t.Parallel()
	for _, pair := range volumeNamePairs() {
		name := volumeName(pair[0], pair[1])
		got := namesegmenttest.ReadName(t, '-', name)
		if len(got) != 2 {
			t.Fatalf("volume name %q for (%q,%q) read back as %d parts, want 2: %q",
				name, pair[0], pair[1], len(got), got)
		}
		if got[0] != pair[0] || got[1] != pair[1] {
			t.Errorf("volume name %q read back as (%q,%q), want (%q,%q)",
				name, got[0], got[1], pair[0], pair[1])
		}
	}
}

// TestVolumeNameCarriesInvalidBytesDistinctly asserts input that is not valid
// UTF-8 still reaches its own volume name, in either position, rather than
// being folded onto the replacement character — and that such a name still reads
// back byte-for-byte as it was supplied. This is where a derivation's input is
// least controlled, so it is where a lossy one is least likely to be noticed.
func TestVolumeNameCarriesInvalidBytesDistinctly(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		scenario             string
		leftNode, leftName   string
		rightNode, rightName string
	}{
		{"two invalid bytes in the node identity", "a\xffb", "data", "a\xfeb", "data"},
		{"two invalid bytes in the volume name", "store", "a\xffb", "store", "a\xfeb"},
		{"an invalid byte against the replacement character it decodes to", "a\xffb", "data", "a�b", "data"},
	} {
		left := volumeName(tc.leftNode, tc.leftName)
		right := volumeName(tc.rightNode, tc.rightName)
		if left == right {
			t.Errorf("%s: (%q,%q) and (%q,%q) both reach %q",
				tc.scenario, tc.leftNode, tc.leftName, tc.rightNode, tc.rightName, left)
		}
		for _, pair := range [][2]string{{tc.leftNode, tc.leftName}, {tc.rightNode, tc.rightName}} {
			name := volumeName(pair[0], pair[1])
			got := namesegmenttest.ReadName(t, '-', name)
			if len(got) != 2 || got[0] != pair[0] || got[1] != pair[1] {
				t.Errorf("%s: volume name %q read back as %q, want (%q,%q)",
					tc.scenario, name, got, pair[0], pair[1])
			}
		}
	}
}

// TestVolumeNameUsesOnlyTheVolumeNameAlphabet asserts every byte a volume name
// emits is a letter, a digit, a dot, an underscore, or the hyphen the encoding
// reserves for its escapes and its join — the whole of what compose admits in a
// volume name. The corpus deliberately carries a space, a tab, a path separator
// and a non-ASCII letter, none of which may survive.
func TestVolumeNameUsesOnlyTheVolumeNameAlphabet(t *testing.T) {
	t.Parallel()
	for _, pair := range volumeNamePairs() {
		name := volumeName(pair[0], pair[1])
		for i := 0; i < len(name); i++ {
			c := name[i]
			if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' ||
				c == '.' || c == '_' || c == '-' {
				continue
			}
			t.Errorf("volume name %q for (%q,%q) carries %q at %d, which compose does not admit",
				name, pair[0], pair[1], string(c), i)
		}
	}
}

// TestVolumeNameIsDeterministic asserts the volume name is byte-stable across
// repeated calls with identical input, which is what lets a rendered compose
// file be compared byte-for-byte between builds.
func TestVolumeNameIsDeterministic(t *testing.T) {
	t.Parallel()
	for _, pair := range volumeNamePairs() {
		first := volumeName(pair[0], pair[1])
		for i := 0; i < 4; i++ {
			if again := volumeName(pair[0], pair[1]); again != first {
				t.Fatalf("volume name for (%q,%q) not stable on run %d: %q != %q",
					pair[0], pair[1], i, again, first)
			}
		}
	}
}

// TestVolumeNameMatchesItsDocumentedExamples pins every worked example the
// compose provider reference gives for this derivation, so a change to the
// derivation that leaves the page saying something else fails here rather than
// shipping a page that no longer describes the product.
//
// The expected strings are written out rather than computed, because an
// expectation computed from the encoder would agree with it whatever it did —
// which is exactly what a documented example must not do.
func TestVolumeNameMatchesItsDocumentedExamples(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		nodeID, name string
		want         string
	}{
		{"store", "data", "store--data"},
		{"db-server", "data", "db-server--data"},
		{"db", "data-cache", "db--data-cache"},
		{"db-data", "cache", "db-data--cache"},
		{"api-zone", "data", "api-zhzone--data"},
		{"db-", "data", "db-zh--data"},
		{"a.b_c", "data", "a.b_c--data"},
		{"a/b", "data", "a-zsb--data"},
		{"a b", "data", "a-zx000020b--data"},
		{"é", "data", "-zx0000e9--data"},
		{"a\xffb", "data", "a-zbffb--data"},
		{"store", "", "store---ze"},
	} {
		if got := volumeName(tc.nodeID, tc.name); got != tc.want {
			t.Errorf("volumeName(%q, %q) = %q, want %q", tc.nodeID, tc.name, got, tc.want)
		}
	}
}
