// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package cfncore

import (
	"encoding/json"
	"strings"
	"testing"
)

// fourNamesDifferingOnlyBySeparatorAndCase are instance names that differ from
// one another in nothing but which separator they use and the case of their
// letters. They are the control the logical-id alphabet's lossiness calls for:
// the alphabet admits no separator, so a derivation reading one has to drop it,
// and dropping it maps several authored names onto one.
func fourNamesDifferingOnlyBySeparatorAndCase() []string {
	return []string{"my-id", "my_id", "My-Id", "MY_ID"}
}

// TestFourNamesDifferingOnlyBySeparatorAndCaseFoldOntoOne states what this
// core's own name derivation makes of those four names, and it is written as a
// statement of the loss rather than as a check for its absence.
//
// Keeping the four apart is not achievable here and is not the goal. A logical
// id admits letters and digits, so there is no character left over to escape
// with: any encoding that kept the names distinct would have to emit something
// the alphabet does not admit, which is a worse failure than the collision. So
// the derivation folds, three of these four arrive at one name, and what the
// rest of this file covers is that arriving there is never quiet.
//
// The fourth name is the discriminator rather than an afterthought. A
// derivation that simply lower-cased everything would fold all four, and a
// test over three would not notice; this one capitalizes the first letter of
// each word and leaves the rest, so a name already in capitals keeps them and
// stays apart. Asserting that the fold is partial is what pins the derivation
// rather than merely observing that some names meet.
func TestFourNamesDifferingOnlyBySeparatorAndCaseFoldOntoOne(t *testing.T) {
	got := map[string]string{}
	for _, name := range fourNamesDifferingOnlyBySeparatorAndCase() {
		// Read through the output-name derivation, which is where this core states
		// that fold once. The empty entry keeps the instance half alone.
		got[name] = OutputLogicalID(name, "")
		t.Logf("instance %q derives %q", name, got[name])
	}

	for _, folded := range []string{"my-id", "my_id", "My-Id"} {
		if got[folded] != "MyId" {
			t.Errorf("instance %q derives %q, want %q — these three differ only by separator and case and arrive at one name",
				folded, got[folded], "MyId")
		}
	}
	if got["MY_ID"] != "MYID" {
		t.Errorf("instance %q derives %q, want %q — its letters are already capitals, so nothing recases them",
			"MY_ID", got["MY_ID"], "MYID")
	}
	if got["MY_ID"] == got["my_id"] {
		t.Errorf("all four names folded onto %q; the fourth has to stay apart or this control cannot tell a partial fold from a total one",
			got["MY_ID"])
	}
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
