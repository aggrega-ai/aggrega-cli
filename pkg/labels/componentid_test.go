// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package labels

import (
	"regexp"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/namesegmenttest"
)

// k8sLabelValuePattern is what Kubernetes accepts as a label value, written out
// here rather than inferred from the composition: a check derived from the code
// under test agrees with that code whatever it does.
var k8sLabelValuePattern = regexp.MustCompile(`^(([A-Za-z0-9][-A-Za-z0-9_.]*)?[A-Za-z0-9])?$`)

// testIdentityBound is the bound these properties compose against.
//
// It is stated here rather than read from the composition because the
// composition holds no bound: how long an identity may be belongs to the target
// it is written for, and reaches the compiler as that target's own answer. The
// figure is Kubernetes' label-value limit, which is the tightest bound a shipped
// provider returns for an identity of ordinary characters — so these properties
// stay stated over the shortening path a real build takes rather than over a
// bound nothing uses.
const testIdentityBound = 63

// boundaryID composes a boundary-scope identity from the parts the compiler
// composes one from — the boundary path outermost first, then the instance —
// so the properties below are stated over the shipped composition rather than
// over a copy of it.
func boundaryID(instance string, path []string) string {
	parts := make([]string, 0, len(path)+1)
	parts = append(parts, path...)
	parts = append(parts, instance)
	return ComposeComponentIdentity(BoundaryFeatureScope, testIdentityBound, parts...)
}

// componentIDParts is the part corpus these properties are established over: the
// shared segment corpus, which is chosen for the encoding's own edges, plus
// input that is NOT valid UTF-8 — which is where an encoding's input is least
// controlled and where a fold is least likely to be noticed.
func componentIDParts() []string {
	return append(namesegmenttest.Corpus(),
		string([]byte{0xff}),
		string([]byte{0x80, 'A'}),
		"a"+string([]byte{0xc3})+"b",
		string([]byte{0xef, 0xbf, 0xbd}), // an authored replacement character
		// A part ENDING on each character a label value admits but the alphabet
		// here does not. These are the cases that decide whether the composed
		// value can end on a character Kubernetes refuses, and a corpus without
		// them lets a wider alphabet pass unnoticed.
		"a.", "a_", ".a", "_a", ".", "_", "a.b_c",
		"bridge", "bridge-x", "bridge x", "cluster",
	)
}

// componentIDPaths is the boundary-path corpus: no path at all, every single
// part as a one-deep path, and pairs drawn from the head of the part corpus so a
// depth change is exercised as well as a spelling change.
func componentIDPaths() [][]string {
	parts := componentIDParts()
	paths := [][]string{nil}
	for _, p := range parts {
		paths = append(paths, []string{p})
	}
	for _, p := range parts[:8] {
		for _, q := range parts[:8] {
			paths = append(paths, []string{p, q})
		}
	}
	return paths
}

// TestBoundaryScopeIdentity_PlainFormReadsBackToItsParts is the injectivity
// proof, and it is a round trip rather than an assertion: the composed identity
// is split at its joins and each part decoded, and what comes back has to be
// exactly the parts it was built from — the prefix, then the boundary path, then
// the instance. The reader shares no code with the encoder, so it cannot agree
// with the encoder by construction.
//
// A composition that fitted the limit carries its parts intact, so every case
// here is asserted to fit as well: a case that shortened would be proving
// nothing about recovery, and silently skipping it would hide that.
func TestBoundaryScopeIdentity_PlainFormReadsBackToItsParts(t *testing.T) {
	t.Parallel()
	for _, instance := range componentIDParts() {
		for _, path := range componentIDPaths() {
			got := boundaryID(instance, path)
			if len(got) > testIdentityBound {
				t.Fatalf("corpus case (%q, %q) composed to %d characters; the corpus is meant to fit so recovery is exercised", instance, path, len(got))
			}
			want := append([]string{BoundaryFeatureScope.prefix}, path...)
			want = append(want, instance)
			read := namesegmenttest.ReadName(t, got)
			if len(read) != len(want) {
				t.Fatalf("(%q, %q) -> %q read back as %d parts, want %d: %q", instance, path, got, len(read), len(want), read)
			}
			for i := range want {
				if read[i] != want[i] {
					t.Errorf("(%q, %q) -> %q read back part %d as %q, want %q", instance, path, got, i, read[i], want[i])
				}
			}
		}
	}
}

// TestBoundaryScopeIdentity_DistinctIdentitiesReachDistinctValues is the
// same property stated as the thing that matters: no two identities in the
// corpus arrive at one value. Recovery above says why; this says it directly, so
// a reader who trusts neither the encoder nor the reader still has the claim.
func TestBoundaryScopeIdentity_DistinctIdentitiesReachDistinctValues(t *testing.T) {
	t.Parallel()
	type identity struct {
		instance string
		path     string
	}
	seen := map[string]identity{}
	for _, instance := range componentIDParts() {
		for _, path := range componentIDPaths() {
			got := boundaryID(instance, path)
			id := identity{instance: instance, path: strings.Join(path, "\x1f") + "\x1e" + itoa(len(path))}
			if prior, clash := seen[got]; clash && prior != id {
				t.Errorf("value %q is reached by both %+v and %+v", got, prior, id)
			}
			seen[got] = id
		}
	}
}

// TestBoundaryScopeIdentity_TheMeasuredFoldsNowSeparate pins the two
// collisions that motivated this derivation, as the concrete pairs they were
// measured as. Each pair reached one value when the identity was scrubbed out of
// a single joined string, and each reaches two now — the first because a
// character the alphabet refuses is written out instead of folded onto a hyphen,
// the second because the parts are joined with a run no written part can contain
// so moving a boundary between them changes the value.
func TestBoundaryScopeIdentity_TheMeasuredFoldsNowSeparate(t *testing.T) {
	t.Parallel()
	for _, pair := range []struct {
		name             string
		aInst, bInst     string
		aPath, bPath     []string
		formerlyOneValue string
	}{
		{
			name:  "a refused character folded onto a hyphen",
			aInst: "bridge x", aPath: []string{"cluster"},
			bInst: "bridge-x", bPath: []string{"cluster"},
			formerlyOneValue: "boundary-fr-bridge-x-cluster",
		},
		{
			name:  "a boundary between two parts moved across the join",
			aInst: "bridge", aPath: []string{"cluster", "x"},
			bInst: "bridge-cluster", bPath: []string{"x"},
			formerlyOneValue: "boundary-fr-bridge-cluster-x",
		},
	} {
		pair := pair
		t.Run(pair.name, func(t *testing.T) {
			t.Parallel()
			a := boundaryID(pair.aInst, pair.aPath)
			b := boundaryID(pair.bInst, pair.bPath)
			if a == b {
				t.Fatalf("both identities still reach %q", a)
			}
			for _, got := range []string{a, b} {
				if got == pair.formerlyOneValue {
					t.Errorf("value %q is still the folded form", got)
				}
			}
		})
	}
}

// TestBoundaryScopeIdentity_EveryInputYieldsAValidLabelValue is the claim
// that there is NO input the derivation cannot produce an answer for,
// demonstrated rather than asserted. The adversarial cases are the three the
// budget could go wrong on — far too long in the instance, far too long in the
// boundary path, and far too long in both — alongside the whole corpus, and each
// result is checked against what Kubernetes accepts and against the length
// limit.
func TestBoundaryScopeIdentity_EveryInputYieldsAValidLabelValue(t *testing.T) {
	t.Parallel()

	long := strings.Repeat("i", 400)
	longPath := []string{strings.Repeat("c", 400)}
	deepPath := make([]string, 0, 64)
	for i := 0; i < 64; i++ {
		deepPath = append(deepPath, strings.Repeat("s", 20))
	}
	// Every part escaping to nine characters apiece, which is the worst
	// expansion the encoding has: a code-point escape per character.
	escaping := strings.Repeat("✓", 200)

	cases := []struct {
		name     string
		instance string
		path     []string
	}{
		{"too long in the instance", long, []string{"cluster"}},
		{"too long in the boundary path", "bridge", longPath},
		{"too long in both", long, longPath},
		{"too long in both, deeply nested", long, deepPath},
		{"every character escaping", escaping, []string{escaping}},
		{"one character over the limit", strings.Repeat("i", testIdentityBound-len("bfr--cluster--")+1), []string{"cluster"}},
		{"exactly at the limit", strings.Repeat("i", testIdentityBound-len("bfr--cluster--")), []string{"cluster"}},
		{"nothing authored at all", "", nil},
		{"an empty path segment", "", []string{""}},
		{"not valid UTF-8, at length", strings.Repeat(string([]byte{0xff}), 200), []string{string([]byte{0x80})}},
	}
	for _, c := range cases {
		got := boundaryID(c.instance, c.path)
		if got == "" {
			t.Errorf("%s: produced no value at all", c.name)
		}
		if len(got) > testIdentityBound {
			t.Errorf("%s: %q is %d characters, over the %d limit", c.name, got, len(got), testIdentityBound)
		}
		if !k8sLabelValuePattern.MatchString(got) {
			t.Errorf("%s: %q is not a valid label value", c.name, got)
		}
	}

	// The same claim over the whole corpus, including the parts that are not
	// valid UTF-8, so validity is established over the edges as well as over the
	// lengths.
	for _, instance := range componentIDParts() {
		for _, path := range componentIDPaths() {
			got := boundaryID(instance, path)
			if got == "" || len(got) > testIdentityBound || !k8sLabelValuePattern.MatchString(got) {
				t.Errorf("(%q, %q) -> %q is not a valid label value of at most %d characters", instance, path, got, testIdentityBound)
			}
		}
	}
}

// TestBoundaryScopeIdentity_ShorteningReservesAFixedRunAndKeepsTheHead
// reads the two reserves back and pins what they buy. The instance keeps its
// first twelve written characters whether it needed them or not, which is what
// makes the boundary path's budget one number; and both regions keep their
// beginnings, because that is where two attachments in one deployment differ.
func TestBoundaryScopeIdentity_ShorteningReservesAFixedRunAndKeepsTheHead(t *testing.T) {
	t.Parallel()

	if identityInstanceReserve != 12 {
		t.Errorf("the instance reserve is %d, and the derivation is documented as twelve", identityInstanceReserve)
	}
	// Four regions is what a feature carrying a boundary path assembles — prefix,
	// path, instance, digest — so three joins is what its budget pays for.
	//
	// Every term after the limit is written as a literal deliberately, and
	// routing any of them through the declaration it stands for is the one change
	// that would empty this assertion: the budget is computed from those same
	// declarations, so a term read back out of one agrees with itself whatever it
	// holds. The literals are the second side, written independently, and the
	// disagreement between the two sides is the entire signal here. A reserve
	// that moves is therefore expected to fail this line until the literal is
	// moved with it, which is the point rather than a maintenance cost.
	shippedBudget := identityContextReserve(BoundaryFeatureScope, testIdentityBound, 4)
	if want := testIdentityBound - 3 - 3*2 - 12 - 3 - 10; shippedBudget != want {
		t.Errorf("the boundary-path budget is %d, want %d", shippedBudget, want)
	}

	instance := strings.Repeat("i", 40) + "TAIL"
	// Both the long-instance and the short-instance case below have to reach the
	// shortened path, so the boundary path alone is over the limit.
	path := []string{strings.Repeat("c", 80) + "TAIL"}
	got := boundaryID(instance, path)

	regions := strings.Split(got, componentIdentityJoin)
	if len(regions) != 4 {
		t.Fatalf("%q split into %d regions, want prefix, path, instance and digest", got, len(regions))
	}
	if regions[0] != BoundaryFeatureScope.prefix {
		t.Errorf("the value opens with %q, want the prefix %q", regions[0], BoundaryFeatureScope.prefix)
	}
	if want := strings.Repeat("c", shippedBudget); regions[1] != want {
		t.Errorf("the boundary-path region is %q, want the first %d written characters %q", regions[1], shippedBudget, want)
	}
	if want := strings.Repeat("i", identityInstanceReserve); regions[2] != want {
		t.Errorf("the instance region is %q, want the first %d written characters %q", regions[2], identityInstanceReserve, want)
	}
	if strings.Contains(got, "TAIL") {
		t.Errorf("%q kept a tail, so the cut is not head-first", got)
	}

	// An instance shorter than its reserve does not lend the spare characters to
	// the boundary path: the budget is a constant, not a remainder.
	short := boundaryID("ab", path)
	shortRegions := strings.Split(short, componentIdentityJoin)
	if len(shortRegions) != 4 {
		t.Fatalf("%q split into %d regions, want four", short, len(shortRegions))
	}
	if len(shortRegions[1]) != shippedBudget {
		t.Errorf("with a two-character instance the boundary-path region is %d characters, want the same %d as with a long one",
			len(shortRegions[1]), shippedBudget)
	}
}

// TestIdentityContextReserve_DerivesItsJoinTermFromTheRegionsPassed is the
// proof that the budget's join term is a function of what is being assembled
// rather than a constant correct for one caller.
//
// The shipped composition assembles three regions or four, so the shape it never
// exercises is a fifth and beyond — which is exactly the shape a second caller
// would bring, and exactly where a fixed multiple silently stops being right. The
// failure that arithmetic produces is not a less readable name: it is a value
// LONGER than the target accepts, because the budget hands the flexible region
// characters that the joins have already spent.
//
// The worst case at each arity is assembled by hand rather than driven through
// the composition, because the composition has no way to assemble a fifth region
// — which is the point. Every region carries its own reserve at full length: the
// flexible one filled to its budget, the instance to its reserve, the prefix and
// the digest as they stand, and any further region empty, since an empty region
// still costs the join in front of it and so is the worst case for the term under
// test.
func TestIdentityContextReserve_DerivesItsJoinTermFromTheRegionsPassed(t *testing.T) {
	t.Parallel()

	join := len(componentIdentityJoin)

	// One more region costs exactly one more join, and nothing else moves.
	for regions := 1; regions <= 8; regions++ {
		got, next := identityContextReserve(BoundaryFeatureScope, testIdentityBound, regions), identityContextReserve(BoundaryFeatureScope, testIdentityBound, regions+1)
		if got-next != join {
			t.Errorf("going from %d regions to %d changes the budget by %d, want one join width %d",
				regions, regions+1, got-next, join)
		}
	}

	// The worst case an assembly of n regions can produce still fits the limit, at
	// every arity including the ones no caller uses today.
	//
	// Four is where the budget starts being spent: three regions is the shipped
	// shape for a feature attached at the top, which carries no flexible region at
	// all and so cannot overrun whatever the budget says.
	for regions := 4; regions <= 8; regions++ {
		assembled := make([]string, 0, regions)
		assembled = append(assembled, componentIdentityEncoding.Segment(BoundaryFeatureScope.prefix))
		assembled = append(assembled, strings.Repeat("c", identityContextReserve(BoundaryFeatureScope, testIdentityBound, regions)))
		assembled = append(assembled, strings.Repeat("i", identityInstanceReserve))
		for len(assembled) < regions-1 {
			assembled = append(assembled, "")
		}
		assembled = append(assembled,
			componentIdentityAbsentMark+strings.Repeat("f", identityDigestLength))

		got := strings.Join(assembled, componentIdentityJoin)
		if len(got) > testIdentityBound {
			t.Errorf("an identity assembled from %d regions is %d characters, over the %d-character limit: %q",
				regions, len(got), testIdentityBound, got)
		}
	}
}

// TestBoundaryScopeIdentity_TheDigestMarkerIsUnreachableByAnAuthoredPart is
// the proof that a shortened identity can never be mistaken for one somebody
// authored. The marker is the encoding's absent-part mark, whose selector stands
// for no character — so the property to establish is that no non-empty part
// writes to anything beginning with it, and that the empty part writes to it
// exactly and nothing more. A shortened identity's last region is the marker
// followed by the digest, which is therefore outside everything the encoding can
// produce for a part.
func TestBoundaryScopeIdentity_TheDigestMarkerIsUnreachableByAnAuthoredPart(t *testing.T) {
	t.Parallel()

	parts := componentIDParts()
	// Every single byte as well, so the claim is not resting on a corpus somebody
	// chose.
	for b := 0; b < 256; b++ {
		parts = append(parts, string([]byte{byte(b)}), "a"+string([]byte{byte(b)}), string([]byte{byte(b)})+"a")
	}
	for _, part := range parts {
		written := componentIdentityEncoding.Segment(part)
		if part == "" {
			if written != componentIdentityAbsentMark {
				t.Fatalf("the empty part writes to %q, want the marker %q", written, componentIdentityAbsentMark)
			}
			continue
		}
		if strings.HasPrefix(written, componentIdentityAbsentMark) {
			t.Errorf("part %q writes to %q, which begins with the digest marker %q", part, written, componentIdentityAbsentMark)
		}
	}

	// And the shortened form does carry the marker where the claim says it does.
	shortened := boundaryID(strings.Repeat("i", 400), []string{"cluster"})
	regions := strings.Split(shortened, componentIdentityJoin)
	last := regions[len(regions)-1]
	if !strings.HasPrefix(last, componentIdentityAbsentMark) {
		t.Errorf("shortened value %q ends in region %q, which does not carry the marker", shortened, last)
	}
	if want := len(componentIdentityAbsentMark) + identityDigestLength; len(last) != want {
		t.Errorf("the shortened value's last region %q is %d characters, want the marker plus %d digest characters (%d)",
			last, len(last), identityDigestLength, want)
	}
	if last == componentIdentityAbsentMark {
		t.Errorf("the last region is the bare marker, which an empty part reaches")
	}
}

// TestBoundaryScopeIdentity_TheDigestSeparatesWhatTheCutRemoved pins that
// the digest is doing the work the shortening leaves it: two identities
// differing only past the cut still reach two values. Remove the digest and both
// of these become one value with nothing recording that two were meant.
func TestBoundaryScopeIdentity_TheDigestSeparatesWhatTheCutRemoved(t *testing.T) {
	t.Parallel()
	head := strings.Repeat("i", 400)
	a := boundaryID(head+"A", []string{"cluster"})
	b := boundaryID(head+"B", []string{"cluster"})
	if a == b {
		t.Fatalf("two identities differing only past the cut both reach %q", a)
	}
	// The cut regions are identical, so the digest is the only thing telling them
	// apart — which is the statement worth pinning.
	if strings.TrimSuffix(a, strings.Split(a, componentIdentityJoin)[3]) != strings.TrimSuffix(b, strings.Split(b, componentIdentityJoin)[3]) {
		t.Errorf("%q and %q differ before the digest, so this case is not exercising it", a, b)
	}
}

// TestBoundaryScopeIdentity_IsAPureFunctionOfItsInput pins the derivation
// against the byte-identical-output contract: the same parts produce the same
// value, on the plain path and on the shortened one alike.
func TestBoundaryScopeIdentity_IsAPureFunctionOfItsInput(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		instance string
		path     []string
	}{
		{"bridge", []string{"cluster"}},
		{strings.Repeat("i", 400), []string{strings.Repeat("c", 400)}},
		{"", nil},
	} {
		first := boundaryID(c.instance, c.path)
		for i := 0; i < 4; i++ {
			if again := boundaryID(c.instance, c.path); again != first {
				t.Fatalf("(%q, %q) produced %q then %q", c.instance, c.path, first, again)
			}
		}
	}
}

// itoa keeps the identity key in the injectivity test above free of a dependency
// on a formatter, so the key is exactly the parts and their count.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
