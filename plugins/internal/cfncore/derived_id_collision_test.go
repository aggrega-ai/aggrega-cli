// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package cfncore

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/alphanumeric"
	"github.com/aggrega-ai/aggrega-cli/pkg/namesegmenttest"
)

// fourNamesDifferingOnlyBySeparatorAndCase are instance names that differ from
// one another in nothing but which separator they use and the case of their
// letters. They are the control the logical-id alphabet calls for: the alphabet
// admits no separator, so a derivation reading one has to do something with it,
// and dropping it is what maps several authored names onto one.
func fourNamesDifferingOnlyBySeparatorAndCase() []string {
	return []string{"my-id", "my_id", "My-Id", "MY_ID"}
}

// TestFourNamesDifferingOnlyBySeparatorAndCaseStayApart states what this core's
// own name derivation makes of those four names: four names, one each.
//
// A logical id admits letters and digits and no separator, so a derivation has
// to carry the separator across somehow. Dropping it is lossy and reaching a
// character outside the alphabet is illegal — but a character can be spared from
// *inside* the alphabet and used to open an escape, which is what this
// derivation does. The four expected names are written out rather than computed,
// because computing them through the derivation would assert it against itself.
//
// The fourth name is the discriminator rather than an afterthought. It differs
// from the second in case alone, so a derivation that lower-cased everything
// would still fold those two while keeping the punctuation apart, and a test
// over the first three would not notice.
func TestFourNamesDifferingOnlyBySeparatorAndCaseStayApart(t *testing.T) {
	// The empty entry keeps the instance half alone: the derivation names an
	// output from a component and an entry, and an absent entry is written as a
	// mark no authored entry can produce, so the four names below differ in
	// exactly the instance half.
	want := map[string]string{
		"my-id": "myQzhidQQQze",
		"my_id": "myQzuidQQQze",
		"My-Id": "MyQzhIdQQQze",
		"MY_ID": "MYQzuIDQQQze",
	}

	seen := map[string]string{}
	for _, name := range fourNamesDifferingOnlyBySeparatorAndCase() {
		got := OutputLogicalID(name, "")
		t.Logf("instance %q derives %q", name, got)
		if got != want[name] {
			t.Errorf("instance %q derives %q, want %q", name, got, want[name])
		}
		if prior, clash := seen[got]; clash {
			t.Errorf("instances %q and %q both derive %q; they differ only by separator or case and must stay apart", prior, name, got)
		}
		seen[got] = name
	}
	if len(seen) != len(fourNamesDifferingOnlyBySeparatorAndCase()) {
		t.Errorf("four instances reached %d names", len(seen))
	}
}

// outputNameCorpus is the corpus the round trip below is established over. It is
// the shared segment corpus for this derivation's opener — which already carries
// the opener in every position that decides whether it is kept or escaped, the
// escape mark, the absent-segment selector, a doubled opener and text spelling an
// escape out — extended with the edges that belong to this derivation rather than
// to the encoding.
//
// The extension is adjacent runs of each specially-selected character, a leading
// digit, both cases of the reserved letter, and the ordinary component and entry
// names an author actually writes, so a regression shows up on a realistic name
// and not only on a constructed one.
//
// The result is deduplicated, and that is not tidiness. An injectivity assertion
// over a corpus carrying one segment twice fails on a pair that is not a
// collision, and the failure names the same segment on both sides — so it reads
// as a defect in the derivation when it is a defect in the corpus. The shared
// corpus and this extension are written independently and both legitimately
// carry an entry like a doubled hyphen, so the overlap is expected rather than a
// mistake to remove by hand.
func outputNameCorpus() []string {
	extended := append(namesegmenttest.Corpus(outputLogicalIDOpener),
		"a__b", "a..b", "a//b", "a--b", "___", "...", "//", "--",
		"1st", "9", "0-9",
		"QaQ", "qQq", "QQQ", "Qq", "qQ",
		"payments-db", "payments_db", "payments.db", "payments/db",
		"host", "Host", "HOST", "db-host", "db_host", "db.host",
	)
	out := make([]string, 0, len(extended))
	seen := make(map[string]bool, len(extended))
	for _, s := range extended {
		if seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}

// TestOutputLogicalIDIsInjectiveAndRecoverable is the round trip, and it is the
// proof the derivation rests on rather than a spot check.
//
// It runs at both arities the join can be crossed at — one segment against two —
// because the join is a separate failure surface from the segment: a derivation
// can be perfectly injective per segment and still let ("a-b", "c") and ("a",
// "b-c") assemble to one name. Reading each name back into the pair it was built
// from is what says the name still carries them, rather than merely that no two
// of these particular inputs happened to meet.
func TestOutputLogicalIDIsInjectiveAndRecoverable(t *testing.T) {
	corpus := outputNameCorpus()
	seen := make(map[string][]string)
	total := 0

	record := func(segments ...string) {
		t.Helper()
		name := OutputLogicalID(segments[0], segments[len(segments)-1])
		total++
		if prior, clash := seen[name]; clash {
			t.Fatalf("%q and %q both reach the Output name %q", prior, segments, name)
		}
		seen[name] = append([]string(nil), segments...)

		// Every name is a legal logical id, whatever was authored.
		for _, r := range name {
			if !alphanumeric.IsRune(r) {
				t.Fatalf("%q reaches %q, which carries %q — a logical id admits letters and digits only", segments, name, r)
			}
		}

		// And the name still carries the pair it was built from.
		read := namesegmenttest.ReadName(t, outputLogicalIDOpener, name)
		if !reflect.DeepEqual(read, segments) {
			t.Fatalf("Output name %q read back as %q, want %q", name, read, segments)
		}
	}

	for _, node := range corpus {
		for _, entry := range corpus {
			record(node, entry)
		}
	}
	t.Logf("%d component/entry pairs reached %d distinct Output names", total, len(seen))
	if len(seen) != total {
		t.Fatalf("built %d names from %d pairs", len(seen), total)
	}
}

// TestTheJoinIsWhatSeparatesTheTwoHalves is the negative control for the test
// above: a corpus that cannot fail proves nothing.
//
// It rebuilds the same names over the same corpus with the join weakened to a
// single opener — the shortest join anyone would reach for — and asserts that
// this collides. Without it, the round trip's green would be consistent with a
// join that never mattered, and a later change shortening it would pass.
//
// The two halves are encoded exactly as the real derivation encodes them, so the
// only difference between this and the real thing is the join's width. That is
// what makes the failure attributable to the join rather than to the segments.
func TestTheJoinIsWhatSeparatesTheTwoHalves(t *testing.T) {
	weak := func(node, entry string) string {
		return outputLogicalIDEncoding.Segment(node) +
			string(outputLogicalIDOpener) +
			outputLogicalIDEncoding.Segment(entry)
	}

	corpus := outputNameCorpus()
	seen := make(map[string][]string)
	for _, node := range corpus {
		for _, entry := range corpus {
			name := weak(node, entry)
			if prior, clash := seen[name]; clash {
				t.Logf("a one-character join collides: %q and %q both reach %q",
					prior, []string{node, entry}, name)
				// The same pair must NOT collide under the real derivation, or the
				// collision would be the segments' rather than the join's.
				a, b := OutputLogicalID(prior[0], prior[1]), OutputLogicalID(node, entry)
				if a == b {
					t.Fatalf("the pair that separates the two joins collides under both: %q", a)
				}
				t.Logf("the real join keeps them apart: %q and %q", a, b)
				return
			}
			seen[name] = []string{node, entry}
		}
	}
	t.Fatalf("a one-character join produced %d distinct names over the whole corpus; "+
		"the corpus cannot tell the two joins apart, so the round trip above proves nothing about the join", len(seen))
}

// TestEveryContributorClaimsASharedNameEvenWhenTheirBodiesMatch asserts that
// each of several contributors arriving at one resource logical id declares
// that id, whatever the resources under it look like.
//
// This is the whole mechanism by which such a collision is caught. What a
// contributor hands over is a claim on a name and never the body behind it, so
// the claims of two contributors that arrived at one name are identical and
// therefore comparable — which is what makes the collision visible without
// anything having to decide whether two resources are "the same".
//
// The fragment is byte-identical across the four on purpose. Bodies that match
// are the case where nothing about the content distinguishes the contributors,
// so a claim is all there is to go on.
func TestEveryContributorClaimsASharedNameEvenWhenTheirBodiesMatch(t *testing.T) {
	frag := []byte("Resources:\n  MyId:\n    Type: AWS::SQS::Queue\n")

	for _, contributor := range fourNamesDifferingOnlyBySeparatorAndCase() {
		resources, _, err := DeclaredNames(contributor, [][]byte{frag})
		if err != nil {
			t.Fatalf("DeclaredNames(%q): %v", contributor, err)
		}
		if len(resources) != 1 || resources[0] != "MyId" {
			t.Errorf("contributor %q declares %v, want the one claimed id [MyId] — a contributor that declares nothing cannot be told to have met another",
				contributor, resources)
		}
	}
}

// TestTheMergeAloneCannotSeparateContributorsWhoseBodiesMatch records what the
// merge does and does not decide, because the division of labour here is easy
// to misread and a change to either half would be silent.
//
// The merge is the only place both bodies are in hand, so it is what catches a
// name whose two bodies differ — a case no comparison of names can see, since
// the names are equal by construction. It deliberately lets a name whose bodies
// MATCH proceed: the space already holds exactly that value, so nothing is
// displaced by writing it again, and refusing would reject a contributor that
// restated a resource harmlessly.
//
// What that leaves is the pair of contributors whose bodies match, which the
// merge cannot separate and does not try to. On the path a build takes, two
// independent things reach that pair first, and both were measured rather than
// assumed: the claims each contributor hands over carry the shared name with no
// body attached, and the provenance a build stamps into every emitted resource
// carries the contributing component's own id, so two contributors' resources
// are not byte-identical by the time they are merged. This test pins the
// merge's half of that split; the end-to-end control pins the build's.
func TestTheMergeAloneCannotSeparateContributorsWhoseBodiesMatch(t *testing.T) {
	identical := []byte("Resources:\n  MyId:\n    Type: AWS::SQS::Queue\n")
	differing := []byte("Resources:\n  MyId:\n    Type: AWS::S3::Bucket\n")

	t.Run("bodies that differ are refused, naming both contributors", func(t *testing.T) {
		_, err := AssembleStack([]NodeFragment{
			{NodeID: "my-id", Files: [][]byte{identical}},
			{NodeID: "my_id", Files: [][]byte{differing}},
		})
		if err == nil {
			t.Fatal("two contributors gave one logical id two different resources and the merge kept one of them silently")
		}
		msg := err.Error()
		t.Logf("halt: %s", msg)
		for _, want := range []string{KeySpaceResourceLogicalIDs, "MyId", `"my-id"`, `"my_id"`} {
			if !strings.Contains(msg, want) {
				t.Errorf("the refusal does not name %q: %s", want, msg)
			}
		}
	})

	t.Run("bodies that match displace nothing, so the merge proceeds", func(t *testing.T) {
		out, err := AssembleStack([]NodeFragment{
			{NodeID: "my-id", Files: [][]byte{identical}},
			{NodeID: "my_id", Files: [][]byte{identical}},
		})
		if err != nil {
			t.Fatalf("restating one name with the identical body loses nothing, so the merge must proceed: %v", err)
		}
		var top map[string]json.RawMessage
		if err := json.Unmarshal(out, &top); err != nil {
			t.Fatalf("assembled stack is not valid JSON: %v", err)
		}
		var resources map[string]json.RawMessage
		if err := json.Unmarshal(top["Resources"], &resources); err != nil {
			t.Fatalf("Resources is not an object: %v", err)
		}
		if len(resources) != 1 {
			t.Fatalf("the merge carries %d resource(s) %v, want the one both contributors named", len(resources), keysOf(resources))
		}
	})
}
